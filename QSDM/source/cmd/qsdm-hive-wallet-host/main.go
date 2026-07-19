package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	providerVersion = "qsdm-hive-wallet-provider/v1"
	maxInputBytes   = 1024 * 1024
	maxOutputBytes  = 1024 * 1024
)

type brokerState struct {
	Version string `json:"version"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Token   string `json:"token"`
}

type nativeError struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func appDataRoot() (string, error) {
	if override := strings.TrimSpace(os.Getenv("QSDM_HIVE_BROKER_STATE")); override != "" {
		return filepath.Dir(override), nil
	}

	switch runtime.GOOS {
	case "windows":
		root := strings.TrimSpace(os.Getenv("APPDATA"))
		if root == "" {
			return "", errors.New("APPDATA is not set")
		}
		return filepath.Join(root, "QSDM-Hive", "wallet-provider"), nil
	case "linux":
		root := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			root = filepath.Join(home, ".config")
		}
		return filepath.Join(root, "QSDM-Hive", "wallet-provider"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "QSDM-Hive", "wallet-provider"), nil
	default:
		return "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func brokerStatePath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("QSDM_HIVE_BROKER_STATE")); override != "" {
		return override, nil
	}
	root, err := appDataRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "broker.json"), nil
}

func loadBrokerState() (brokerState, error) {
	statePath, err := brokerStatePath()
	if err != nil {
		return brokerState{}, err
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return brokerState{}, fmt.Errorf("start QSDM Hive before using the wallet extension: %w", err)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(statePath)
		if statErr != nil {
			return brokerState{}, statErr
		}
		if info.Mode().Perm()&0o077 != 0 {
			return brokerState{}, errors.New("QSDM Hive broker state is not private to this user")
		}
	}
	var state brokerState
	if err := json.Unmarshal(raw, &state); err != nil {
		return brokerState{}, fmt.Errorf("invalid QSDM Hive broker state: %w", err)
	}
	if state.Version != providerVersion || state.Host != "127.0.0.1" || state.Port < 1 || state.Port > 65535 || len(state.Token) != 64 || strings.Trim(state.Token, "0123456789abcdef") != "" {
		return brokerState{}, errors.New("QSDM Hive broker state failed validation")
	}
	return state, nil
}

func readNativeMessage(reader io.Reader) ([]byte, error) {
	var length uint32
	if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
		return nil, err
	}
	if length == 0 || length > maxInputBytes {
		return nil, fmt.Errorf("invalid native message length: %d", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return nil, err
	}
	if !json.Valid(payload) {
		return nil, errors.New("native message is not valid JSON")
	}
	return payload, nil
}

func writeNativeMessage(writer io.Writer, payload []byte) error {
	if len(payload) == 0 || len(payload) > maxOutputBytes {
		return fmt.Errorf("invalid native response length: %d", len(payload))
	}
	if err := binary.Write(writer, binary.LittleEndian, uint32(len(payload))); err != nil {
		return err
	}
	_, err := writer.Write(payload)
	return err
}

func errorPayload(err error) []byte {
	payload, _ := json.Marshal(nativeError{OK: false, Error: err.Error()})
	return payload
}

func forwardToHive(payload []byte) ([]byte, error) {
	state, err := loadBrokerState()
	if err != nil {
		return nil, err
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/v1/request", state.Port)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() != "127.0.0.1" {
		return nil, errors.New("refusing a non-loopback QSDM Hive broker")
	}

	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+state.Token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("QSDM Hive wallet broker is unavailable: %w", err)
	}
	defer response.Body.Close()
	result, err := io.ReadAll(io.LimitReader(response.Body, maxOutputBytes+1))
	if err != nil {
		return nil, err
	}
	if len(result) > maxOutputBytes {
		return nil, errors.New("QSDM Hive wallet response is too large")
	}
	if !json.Valid(result) {
		return nil, errors.New("QSDM Hive returned an invalid response")
	}
	return result, nil
}

func serveNativeMessage(reader io.Reader, writer io.Writer, forward func([]byte) ([]byte, error)) error {
	payload, err := readNativeMessage(reader)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return writeNativeMessage(writer, errorPayload(err))
	}

	response, err := forward(payload)
	if err != nil {
		response = errorPayload(err)
	}
	return writeNativeMessage(writer, response)
}

func serveNativeMessages(reader io.Reader, writer io.Writer, forward func([]byte) ([]byte, error)) error {
	for {
		payload, err := readNativeMessage(reader)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return writeNativeMessage(writer, errorPayload(err))
		}

		response, err := forward(payload)
		if err != nil {
			response = errorPayload(err)
		}
		if err := writeNativeMessage(writer, response); err != nil {
			return err
		}
	}
}

func main() {
	if err := serveNativeMessages(os.Stdin, os.Stdout, forwardToHive); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
