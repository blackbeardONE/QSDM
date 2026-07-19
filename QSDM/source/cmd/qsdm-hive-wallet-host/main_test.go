package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeMessageRoundTrip(t *testing.T) {
	payload := []byte(`{"version":"qsdm-hive-wallet-provider/v1"}`)
	var framed bytes.Buffer
	if err := writeNativeMessage(&framed, payload); err != nil {
		t.Fatalf("writeNativeMessage: %v", err)
	}

	got, err := readNativeMessage(&framed)
	if err != nil {
		t.Fatalf("readNativeMessage: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q", got)
	}
}

func TestNativeMessageRejectsOversizedInput(t *testing.T) {
	var framed bytes.Buffer
	if err := binary.Write(&framed, binary.LittleEndian, uint32(maxInputBytes+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := readNativeMessage(&framed); err == nil {
		t.Fatal("expected oversized input to be rejected")
	}
}

func TestLoadBrokerStateRejectsInvalidToken(t *testing.T) {
	directory := t.TempDir()
	statePath := filepath.Join(directory, "broker.json")
	state := []byte(`{"version":"qsdm-hive-wallet-provider/v1","host":"127.0.0.1","port":1234,"token":"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"}`)
	if err := os.WriteFile(statePath, state, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("QSDM_HIVE_BROKER_STATE", statePath)

	if _, err := loadBrokerState(); err == nil {
		t.Fatal("expected invalid broker token to be rejected")
	}
}

func TestServeNativeMessageProcessesExactlyOneRequest(t *testing.T) {
	first := []byte(`{"id":"first"}`)
	second := []byte(`{"id":"second"}`)
	var input bytes.Buffer
	if err := writeNativeMessage(&input, first); err != nil {
		t.Fatal(err)
	}
	if err := writeNativeMessage(&input, second); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	forwarded := 0
	err := serveNativeMessage(&input, &output, func(payload []byte) ([]byte, error) {
		forwarded++
		return append([]byte(nil), payload...), nil
	})
	if err != nil {
		t.Fatalf("serveNativeMessage: %v", err)
	}
	if forwarded != 1 {
		t.Fatalf("expected one forwarded request, got %d", forwarded)
	}
	response, err := readNativeMessage(&output)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !bytes.Equal(response, first) {
		t.Fatalf("unexpected response: %s", response)
	}
	if output.Len() != 0 {
		t.Fatal("expected exactly one framed response")
	}
}

func TestServeNativeMessagesProcessesRequestsUntilEOF(t *testing.T) {
	payloads := [][]byte{[]byte(`{"id":"first"}`), []byte(`{"id":"second"}`)}
	var input bytes.Buffer
	for _, payload := range payloads {
		if err := writeNativeMessage(&input, payload); err != nil {
			t.Fatal(err)
		}
	}

	var output bytes.Buffer
	err := serveNativeMessages(&input, &output, func(payload []byte) ([]byte, error) {
		return append([]byte(nil), payload...), nil
	})
	if err != nil {
		t.Fatalf("serveNativeMessages: %v", err)
	}
	for _, expected := range payloads {
		response, readErr := readNativeMessage(&output)
		if readErr != nil {
			t.Fatalf("read response: %v", readErr)
		}
		if !bytes.Equal(response, expected) {
			t.Fatalf("unexpected response: %s", response)
		}
	}
	if output.Len() != 0 {
		t.Fatal("unexpected extra native response")
	}
}
