package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed web/*
var webFiles embed.FS

const controlCookieName = "qsdm_edge_control"

type controlServer struct {
	controller *controller
	token      string
	quit       chan struct{}
	index      []byte
	assets     http.Handler
}

func newControlServer(controller *controller, token string, quit chan struct{}) (*controlServer, error) {
	index, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		return nil, err
	}
	root, err := fs.Sub(webFiles, "web")
	if err != nil {
		return nil, err
	}
	return &controlServer{
		controller: controller,
		token:      token,
		quit:       quit,
		index:      index,
		assets:     http.FileServer(http.FS(root)),
	}, nil
}

func (s *controlServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.Handle("/assets/", s.authenticated(http.StripPrefix("/assets/", s.assets)))
	mux.Handle("/api/state", s.authenticated(http.HandlerFunc(s.handleState)))
	mux.Handle("/api/settings", s.authenticated(http.HandlerFunc(s.handleSettings)))
	mux.Handle("/api/start", s.authenticated(http.HandlerFunc(s.handleStart)))
	mux.Handle("/api/stop", s.authenticated(http.HandlerFunc(s.handleStop)))
	mux.Handle("/api/pair-agent", s.authenticated(http.HandlerFunc(s.handlePairAgent)))
	mux.Handle("/api/pairing-codes", s.authenticated(http.HandlerFunc(s.handlePairingCodes)))
	mux.Handle("/api/connect-mother", s.authenticated(http.HandlerFunc(s.handleConnectMother)))
	mux.Handle("/api/quit", s.authenticated(http.HandlerFunc(s.handleQuit)))
	return s.securityHeaders(mux)
}

func (s *controlServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if queryToken := r.URL.Query().Get("t"); queryToken != "" {
		if !secureEqual(queryToken, s.token) {
			http.Error(w, "QSDM Edge Control access denied", http.StatusForbidden)
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name:     controlCookieName,
			Value:    s.token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	if !s.authorized(r) {
		http.Error(w, "Open QSDM Edge Control from the installed application.", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.index)
}

func (s *controlServer) handleState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeControlJSON(w, http.StatusOK, s.controller.snapshot())
}

func (s *controlServer) handleSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !validLocalOrigin(r) {
		writeControlError(w, http.StatusForbidden, "request origin is not allowed")
		return
	}
	var settings controlSettings
	if err := decodeControlJSON(r, &settings); err != nil {
		writeControlError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.controller.updateSettings(settings); err != nil {
		writeControlError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *controlServer) handleStart(w http.ResponseWriter, r *http.Request) {
	if !requireLocalPost(w, r) {
		return
	}
	if err := s.controller.start(); err != nil {
		writeControlError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *controlServer) handleStop(w http.ResponseWriter, r *http.Request) {
	if !requireLocalPost(w, r) {
		return
	}
	if err := s.controller.stop(); err != nil {
		writeControlError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *controlServer) handlePairAgent(w http.ResponseWriter, r *http.Request) {
	if !requireLocalPost(w, r) {
		return
	}
	var request struct {
		Code string `json:"code"`
	}
	if err := decodeControlJSON(r, &request); err != nil {
		writeControlError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.controller.pairAgent(request.Code); err != nil {
		writeControlError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *controlServer) handlePairingCodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	codes, err := s.controller.getPairingCodes()
	if err != nil {
		writeControlError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, codes)
}

func (s *controlServer) handleConnectMother(w http.ResponseWriter, r *http.Request) {
	if !requireLocalPost(w, r) {
		return
	}
	if err := s.controller.connectLocalMother(); err != nil {
		writeControlError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *controlServer) handleQuit(w http.ResponseWriter, r *http.Request) {
	if !requireLocalPost(w, r) {
		return
	}
	writeControlJSON(w, http.StatusOK, map[string]bool{"ok": true})
	select {
	case s.quit <- struct{}{}:
	default:
	}
}

func (s *controlServer) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			writeControlError(w, http.StatusUnauthorized, "Edge Control session expired; reopen the application")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *controlServer) authorized(r *http.Request) bool {
	if secureEqual(r.Header.Get("X-QSDM-Edge-Token"), s.token) {
		return true
	}
	cookie, err := r.Cookie(controlCookieName)
	return err == nil && secureEqual(cookie.Value, s.token)
}

func (s *controlServer) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func requireLocalPost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		writeControlError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	if !validLocalOrigin(r) {
		writeControlError(w, http.StatusForbidden, "request origin is not allowed")
		return false
	}
	return true
}

func validLocalOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Scheme == "http" && parsed.Host == r.Host
}

func decodeControlJSON(r *http.Request, target any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 64*1024+1))
	if err != nil {
		return fmt.Errorf("read request: %w", err)
	}
	if len(raw) > 64*1024 {
		return errors.New("request is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is required")
		}
		return fmt.Errorf("invalid request: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func writeControlJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeControlError(w http.ResponseWriter, status int, message string) {
	writeControlJSON(w, status, map[string]any{"ok": false, "error": message})
}

func secureEqual(left, right string) bool {
	if len(left) != len(right) || left == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func probeExistingControl(address, token string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	request, err := http.NewRequest(http.MethodGet, address+"/api/state", nil)
	if err != nil {
		return false
	}
	request.Header.Set("X-QSDM-Edge-Token", token)
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode == http.StatusOK
}

func shutdownHTTPServer(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
