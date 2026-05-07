package main

// HTTP server for the qsdm-attester binary. Exposes four routes:
//
//   GET /api/v1/challenge   - mints + returns a fresh signed
//                              challenge (the only consensus-
//                              relevant route).
//   GET /healthz            - liveness probe; always 200 once
//                              the issuer is wired.
//   GET /info               - non-secret metadata (signer_id,
//                              key fingerprint, note, version)
//                              so a validator operator can
//                              confidently allowlist this
//                              attester before pasting into
//                              peer_signers.toml.
//   GET /metrics            - hand-rolled OpenMetrics text
//                              exposition. Self-contained so
//                              the binary stays small and free
//                              of the upstream prometheus
//                              client_golang dependency.
//
// Wire compatibility: /api/v1/challenge returns a JSON body
// byte-identical to the validator's GET /api/v1/mining/challenge
// response (api.ChallengeWire). This is intentional — miners
// can swap the URL transparently. We import api purely for the
// shared wire type so the two endpoints cannot drift.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// buildVersion is overridden at link-time via -ldflags
// "-X main.buildVersion=...". A dev build leaves the default
// "dev" so /info still answers something sensible.
var buildVersion = "dev"

// Server bundles the issuer, configuration snapshot, and a small
// counter set into a value that hangs off Run(). Stateless from
// a consensus point of view: all "have I seen this nonce" memory
// lives inside the *challenge.Issuer's seen-map, which is
// process-local and bounded by the issuer's retention window.
type Server struct {
	cfg     *Config
	issuer  *challenge.Issuer
	signer  *challenge.HMACSigner
	keyFP   string
	started time.Time

	// Atomic counters for /metrics. atomic.Uint64 is safe to
	// load/add from any goroutine; no mutex needed.
	issuedTotal     atomic.Uint64
	issuerErrTotal  atomic.Uint64
	requestsTotal   atomic.Uint64
	notFoundTotal   atomic.Uint64
	methodNotAllow  atomic.Uint64
	logIssuanceN    atomic.Uint64 // running count for sampling
	logEvery        uint64
	logIssuanceFn   func(snap LogIssuance)

	// telemetry, when non-nil, attaches the Reference
	// Telemetry Oracle: a signed catalog of observed GPU
	// fingerprints served at /api/v1/telemetry/reference.
	// See cmd/qsdm-attester/telemetry.go for the full
	// wiring rationale.
	telemetry *TelemetryProvider
}

// LogIssuance is the structured event the optional issuance
// logger receives once every Config.LogIssuanceEvery successes.
// Exposed as a struct so tests + a future structured logger
// can consume the same payload without parsing log strings.
type LogIssuance struct {
	SignerID  string
	IssuedAt  int64
	NonceHex  string
	Total     uint64
	RemoteIP  string
}

// NewServer wires the issuer + config snapshot. The signer
// must be the same one the issuer was built from; we store it
// only to expose SignerID() on /info without pinning a separate
// holder. Returns an error on any pre-condition violation so
// boot fails loudly rather than silently exposing a half-wired
// HTTP service.
func NewServer(cfg *Config, issuer *challenge.Issuer, signer *challenge.HMACSigner, keyFingerprint string) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("attester: NewServer requires non-nil cfg")
	}
	if issuer == nil {
		return nil, errors.New("attester: NewServer requires non-nil issuer")
	}
	if signer == nil {
		return nil, errors.New("attester: NewServer requires non-nil signer")
	}
	if keyFingerprint == "" {
		return nil, errors.New("attester: NewServer requires non-empty keyFingerprint")
	}
	return &Server{
		cfg:      cfg,
		issuer:   issuer,
		signer:   signer,
		keyFP:    keyFingerprint,
		started:  time.Now(),
		logEvery: cfg.LogIssuanceEvery,
	}, nil
}

// SetIssuanceLogger installs an optional callback fired once
// every Config.LogIssuanceEvery successful issuances. Wired
// from main.go to the structured stdout logger; tests pass a
// fake.
func (s *Server) SetIssuanceLogger(fn func(snap LogIssuance)) {
	s.logIssuanceFn = fn
}

// Routes returns a configured *http.ServeMux ready to be
// passed to http.Server. Exposed (instead of mutating an
// internal field) so tests can mount the same routes on
// httptest.Server without booting the full goroutine.
//
// The challenge handler is registered on TWO paths:
//
//   - /api/v1/mining/challenge — wire-compatible with the
//     validator's same endpoint, so a miner with the URL of
//     this attester in its challenge_urls list can pull
//     challenges using its existing FetchChallenge code path
//     unmodified.
//   - /api/v1/challenge — short-form, used by operator tooling
//     (curl, /info pages, attester-vs-attester comparison
//     scripts) where the /mining/ segment makes no sense
//     because there's no mining service in this binary.
//
// Both URLs return byte-identical bodies — they share one
// handler. Removing the short form would break operator
// muscle memory; removing the long form would force every
// miner to learn an attester-specific URL convention.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/challenge", s.handleChallenge)
	mux.HandleFunc("/api/v1/challenge", s.handleChallenge)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/info", s.handleInfo)
	mux.HandleFunc("/metrics", s.handleMetrics)
	// Telemetry route registered unconditionally — when
	// the provider is nil the handler returns 404 with a
	// "telemetry_disabled" body, so a curious caller
	// always gets a machine-readable explanation rather
	// than a generic mux miss.
	mux.HandleFunc("/api/v1/telemetry/reference", s.handleTelemetryReference)
	mux.HandleFunc("/", s.handleNotFound)
	return mux
}

// Run boots the HTTP server, blocks until ctx is cancelled,
// then performs a 5s graceful shutdown. Returns the first
// non-context error encountered.
func (s *Server) Run(ctx context.Context, log func(string, ...any)) error {
	srv := &http.Server{
		Addr:              s.cfg.ListenAddr,
		Handler:           s.Routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		log("attester: serving",
			"addr", s.cfg.ListenAddr,
			"signer_id", s.signer.SignerID(),
			"key_fingerprint", s.keyFP,
			"version", buildVersion)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		log("attester: shutdown signal received; draining…")
	case err := <-errCh:
		return err
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("attester: graceful shutdown: %w", err)
	}
	log("attester: stopped",
		"issued_total", s.issuedTotal.Load(),
		"issuer_errors", s.issuerErrTotal.Load(),
		"uptime", time.Since(s.started).Round(time.Second).String())
	return nil
}

// handleChallenge mints a fresh challenge and returns it as
// api.ChallengeWire JSON — byte-identical to the validator's
// existing /api/v1/mining/challenge response.
func (s *Server) handleChallenge(w http.ResponseWriter, r *http.Request) {
	s.requestsTotal.Add(1)
	if r.Method != http.MethodGet {
		s.methodNotAllow.Add(1)
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	c, err := s.issuer.Issue()
	if err != nil {
		s.issuerErrTotal.Add(1)
		http.Error(w, "issuer error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.issuedTotal.Add(1)
	if s.logEvery > 0 && s.logIssuanceFn != nil {
		n := s.logIssuanceN.Add(1)
		if n%s.logEvery == 0 {
			s.logIssuanceFn(LogIssuance{
				SignerID: c.SignerID,
				IssuedAt: c.IssuedAt,
				NonceHex: challenge.NonceHex(c.Nonce),
				Total:    s.issuedTotal.Load(),
				RemoteIP: clientIP(r),
			})
		}
	}
	wire := api.ChallengeFromCore(c)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(wire); err != nil {
		// Encoding failure after status is implicitly committed
		// by NewEncoder; nothing useful to do but bump a counter.
		s.issuerErrTotal.Add(1)
	}
}

// handleHealth is a 1-byte liveness probe. Returns 200 once the
// issuer is wired (which is true by construction here).
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.requestsTotal.Add(1)
	if r.Method != http.MethodGet {
		s.methodNotAllow.Add(1)
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

// InfoResponse is the public-safe metadata payload. Includes
// the data the validator operator needs to add this attester
// to peer_signers.toml — except the key itself, which the
// operator MUST copy out-of-band (scp, encrypted email, etc.).
type InfoResponse struct {
	SignerID         string `json:"signer_id"`
	KeyFingerprint   string `json:"key_fingerprint"`
	Note             string `json:"note"`
	Version          string `json:"version"`
	UptimeSeconds    int64  `json:"uptime_seconds"`
	IssuedTotal      uint64 `json:"issued_total"`
	TelemetryEnabled bool   `json:"telemetry_enabled"`
	TelemetryGPUs    int    `json:"telemetry_gpus,omitempty"`
	TelemetryTicks   uint64 `json:"telemetry_ticks,omitempty"`
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.requestsTotal.Add(1)
	if r.Method != http.MethodGet {
		s.methodNotAllow.Add(1)
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	resp := InfoResponse{
		SignerID:       s.signer.SignerID(),
		KeyFingerprint: s.keyFP,
		Note:           s.cfg.Note,
		Version:        buildVersion,
		UptimeSeconds:  int64(time.Since(s.started).Seconds()),
		IssuedTotal:    s.issuedTotal.Load(),
	}
	if s.telemetryEnabled() {
		resp.TelemetryEnabled = true
		_, gpuCount := s.telemetry.Registry.Counters()
		resp.TelemetryGPUs = gpuCount
		resp.TelemetryTicks = s.telemetry.collectionTicks.Load()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleMetrics renders Prometheus text-format counters. We
// hand-roll the exposition (instead of importing
// prometheus/client_golang) to keep the attester binary tiny —
// it has only six counters and no histograms or summaries, so
// the upstream library would be 3 MB of dependencies for
// ~30 lines of output.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.requestsTotal.Add(1)
	if r.Method != http.MethodGet {
		s.methodNotAllow.Add(1)
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	uptime := time.Since(s.started).Seconds()
	signerLabel := `signer_id="` + s.signer.SignerID() + `"`
	body := "" +
		"# HELP qsdm_attester_issued_total Number of challenges successfully minted and returned.\n" +
		"# TYPE qsdm_attester_issued_total counter\n" +
		"qsdm_attester_issued_total{" + signerLabel + "} " + strconv.FormatUint(s.issuedTotal.Load(), 10) + "\n" +
		"# HELP qsdm_attester_issuer_errors_total Number of times the issuer failed to mint a challenge.\n" +
		"# TYPE qsdm_attester_issuer_errors_total counter\n" +
		"qsdm_attester_issuer_errors_total{" + signerLabel + "} " + strconv.FormatUint(s.issuerErrTotal.Load(), 10) + "\n" +
		"# HELP qsdm_attester_requests_total Total HTTP requests handled, all routes.\n" +
		"# TYPE qsdm_attester_requests_total counter\n" +
		"qsdm_attester_requests_total{" + signerLabel + "} " + strconv.FormatUint(s.requestsTotal.Load(), 10) + "\n" +
		"# HELP qsdm_attester_method_not_allowed_total Requests rejected for using a disallowed HTTP method.\n" +
		"# TYPE qsdm_attester_method_not_allowed_total counter\n" +
		"qsdm_attester_method_not_allowed_total{" + signerLabel + "} " + strconv.FormatUint(s.methodNotAllow.Load(), 10) + "\n" +
		"# HELP qsdm_attester_not_found_total Requests rejected for hitting an unknown route.\n" +
		"# TYPE qsdm_attester_not_found_total counter\n" +
		"qsdm_attester_not_found_total{" + signerLabel + "} " + strconv.FormatUint(s.notFoundTotal.Load(), 10) + "\n" +
		"# HELP qsdm_attester_uptime_seconds Seconds since the attester process started.\n" +
		"# TYPE qsdm_attester_uptime_seconds gauge\n" +
		"qsdm_attester_uptime_seconds{" + signerLabel + "} " + strconv.FormatFloat(uptime, 'f', 1, 64) + "\n"
	if s.telemetryEnabled() {
		applies, gpuCount := s.telemetry.Registry.Counters()
		body += "" +
			"# HELP qsdm_attester_telemetry_gpus Currently-tracked GPUs in the reference profile.\n" +
			"# TYPE qsdm_attester_telemetry_gpus gauge\n" +
			"qsdm_attester_telemetry_gpus{" + signerLabel + "} " + strconv.Itoa(gpuCount) + "\n" +
			"# HELP qsdm_attester_telemetry_collection_ticks_total Number of completed collector ticks.\n" +
			"# TYPE qsdm_attester_telemetry_collection_ticks_total counter\n" +
			"qsdm_attester_telemetry_collection_ticks_total{" + signerLabel + "} " + strconv.FormatUint(s.telemetry.collectionTicks.Load(), 10) + "\n" +
			"# HELP qsdm_attester_telemetry_collection_errors_total Collector failures (Collect / Apply / Save).\n" +
			"# TYPE qsdm_attester_telemetry_collection_errors_total counter\n" +
			"qsdm_attester_telemetry_collection_errors_total{" + signerLabel + "} " + strconv.FormatUint(s.telemetry.collectionErrs.Load(), 10) + "\n" +
			"# HELP qsdm_attester_telemetry_apply_calls_total Total Apply() invocations across all observed GPUs.\n" +
			"# TYPE qsdm_attester_telemetry_apply_calls_total counter\n" +
			"qsdm_attester_telemetry_apply_calls_total{" + signerLabel + "} " + strconv.FormatUint(applies, 10) + "\n" +
			"# HELP qsdm_attester_telemetry_requests_total /api/v1/telemetry/reference HTTP requests.\n" +
			"# TYPE qsdm_attester_telemetry_requests_total counter\n" +
			"qsdm_attester_telemetry_requests_total{" + signerLabel + "} " + strconv.FormatUint(s.telemetry.requests.Load(), 10) + "\n" +
			"# HELP qsdm_attester_telemetry_sign_failures_total Profile signing or encoding failures.\n" +
			"# TYPE qsdm_attester_telemetry_sign_failures_total counter\n" +
			"qsdm_attester_telemetry_sign_failures_total{" + signerLabel + "} " + strconv.FormatUint(s.telemetry.signFailures.Load(), 10) + "\n"
	}
	_, _ = io.WriteString(w, body)
}

// handleNotFound catches every unrouted path. Required because
// the default ServeMux's "/" handler matches everything and
// we want the right counter + JSON shape for unknown URLs.
func (s *Server) handleNotFound(w http.ResponseWriter, r *http.Request) {
	s.requestsTotal.Add(1)
	if r.URL.Path != "/" {
		s.notFoundTotal.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":"not_found"}`)
		return
	}
	// Exact "/" prints a tiny self-description so an operator
	// who curls the root URL gets pointed at /info instead of
	// a 404.
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"service":"qsdm-attester","version":%q,"info":"/info","challenge":"/api/v1/challenge"}`, buildVersion)
}

// clientIP extracts the best-effort remote IP for /info and
// log sampling. Honours X-Forwarded-For (one hop), then falls
// back to RemoteAddr's host portion. Used for log enrichment
// only — never for authorization, since these headers are
// trivially spoofable on a public endpoint.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Strip everything after the first comma so we keep
		// the immediate-most-client hop and not the proxy
		// chain.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	if r.RemoteAddr == "" {
		return ""
	}
	for i := len(r.RemoteAddr) - 1; i >= 0; i-- {
		if r.RemoteAddr[i] == ':' {
			return r.RemoteAddr[:i]
		}
	}
	return r.RemoteAddr
}
