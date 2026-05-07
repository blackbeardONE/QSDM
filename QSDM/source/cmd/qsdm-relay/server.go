package main

// Three independent http.Servers compose the relay:
//
//   - tunnelSrv (default :7700): receives 101-Upgrade requests
//     from qsdm-attester instances. Caddy in front terminates
//     TLS and forwards to this. The exact path is
//     tunnel.TunnelEndpoint = "/_tunnel/connect".
//
//   - proxySrv (default :7710): receives plain HTTP from
//     public miners. Path layout is /<slot>/<…>; the
//     <slot> segment is stripped and the remainder is
//     forwarded into the matching tunnel session via a
//     fresh yamux stream. Caddy in front terminates TLS.
//
//   - metricsSrv (default :7720): operator-only OpenMetrics
//     + JSON /info + /healthz. NEVER exposed publicly.
//
// Splitting them into three servers (rather than virtual-
// hosting on one Listener) lets the operator firewall each
// independently — a typical posture is "443 forwards to
// :7700 + :7710, :7720 binds to localhost only".

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/tunnel"
)

// buildVersion is overridden at link-time via -ldflags
// "-X main.buildVersion=…". Default "dev" so /info is
// answerable in unbranded local builds.
var buildVersion = "dev"

// Server bundles every running piece. Held in a single
// struct so main.go can shutdown them all in one place.
type Server struct {
	cfg      *Config
	registry *tunnel.Registry
	auth     tunnel.AuthMap
	logf     func(string, ...any)
	started  time.Time

	tunnelSrv  *http.Server
	proxySrv   *http.Server
	metricsSrv *http.Server

	// Atomic counters for /metrics; tunnel.Registry has
	// its own counters but we add proxy-side ones here so
	// the metrics endpoint is one-stop.
	proxyRequests       atomic.Uint64
	proxyMissingSlot    atomic.Uint64
	proxyBadSlotChars   atomic.Uint64
	upgradeRequests     atomic.Uint64
	upgradeRejected     atomic.Uint64
	metricsRequests     atomic.Uint64
	healthzRequests     atomic.Uint64
	infoRequests        atomic.Uint64
	notFoundOnMetricsSr atomic.Uint64
}

// NewServer constructs the three http.Servers but does NOT
// start any goroutines. Run() does the actual binding +
// serving. Returns an error on any pre-condition violation.
func NewServer(cfg *Config, auth tunnel.AuthMap, logf func(string, ...any)) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("relay: NewServer requires non-nil cfg")
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &Server{
		cfg:      cfg,
		registry: &tunnel.Registry{},
		auth:     auth,
		logf:     logf,
		started:  time.Now(),
	}

	if cfg.LogTunnelEvents {
		s.registry.SetObserver(func(e tunnel.SlotEvent) {
			if e.Connected {
				logf("relay: tunnel registered",
					"slot", e.SlotID, "signer", e.SignerID, "remote", e.RemoteIP, "note", e.Note)
				return
			}
			logf("relay: tunnel deregistered",
				"slot", e.SlotID, "signer", e.SignerID)
		})
	}

	tunnelMux := http.NewServeMux()
	tunnelMux.HandleFunc(tunnel.TunnelEndpoint, s.wrapUpgradeMetrics(
		tunnel.HandleUpgrade(s.registry, s.auth, logf)))
	tunnelMux.HandleFunc("/", s.handleTunnelDefault)
	s.tunnelSrv = &http.Server{
		Addr:              cfg.TunnelListenAddr,
		Handler:           tunnelMux,
		ReadHeaderTimeout: 10 * time.Second,
		// IdleTimeout doesn't apply once the connection is
		// hijacked — the yamux session takes over and uses
		// its own keepalive.
	}

	proxyMux := http.NewServeMux()
	proxyMux.HandleFunc("/", s.wrapProxyMetrics(
		tunnel.HandleProxy(s.registry, logf)))
	s.proxySrv = &http.Server{
		Addr:              cfg.ProxyListenAddr,
		Handler:           proxyMux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/healthz", s.handleHealthz)
	metricsMux.HandleFunc("/info", s.handleInfo)
	metricsMux.HandleFunc("/metrics", s.handleMetrics)
	metricsMux.HandleFunc("/", s.handleMetricsDefault)
	s.metricsSrv = &http.Server{
		Addr:              cfg.MetricsListenAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	return s, nil
}

// Run starts all three servers concurrently and blocks
// until either ctx is cancelled OR any server returns a
// non-graceful-shutdown error. Returns the first such error,
// nil on a clean ctx-cancel shutdown.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 3)
	startServer := func(name string, srv *http.Server) {
		s.logf("relay: server listening", "role", name, "addr", srv.Addr)
		go func() {
			err := srv.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s: %w", name, err)
				return
			}
			errCh <- nil
		}()
	}
	startServer("tunnel-ingress", s.tunnelSrv)
	startServer("proxy", s.proxySrv)
	startServer("metrics", s.metricsSrv)

	var firstErr error
	select {
	case <-ctx.Done():
		s.logf("relay: shutdown requested; draining…")
	case err := <-errCh:
		s.logf("relay: server error before shutdown", "err", err.Error())
		firstErr = err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range []*http.Server{s.tunnelSrv, s.proxySrv, s.metricsSrv} {
		if err := srv.Shutdown(shutdownCtx); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("shutdown: %w", err)
		}
	}

	// Drain any remaining errCh sends so leftover goroutines
	// don't block on the buffered channel.
	for i := 0; i < 3; i++ {
		select {
		case <-errCh:
		default:
		}
	}

	registers, deregisters, collisions := s.registry.Counters()
	s.logf("relay: stopped",
		"uptime", time.Since(s.started).Round(time.Second).String(),
		"registers", registers,
		"deregisters", deregisters,
		"collisions", collisions,
		"proxy_requests", s.proxyRequests.Load(),
		"upgrade_requests", s.upgradeRequests.Load())
	return firstErr
}

// handleTunnelDefault catches every URL on the tunnel-
// ingress server that isn't TunnelEndpoint. Returns a tiny
// JSON pointer so a curl to the wrong URL is self-diagnosing
// instead of silent.
func (s *Server) handleTunnelDefault(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"service":"qsdm-relay","tunnel":%q,"version":%q}`,
			tunnel.TunnelEndpoint, buildVersion)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"error":"not_found","hint":"tunnel ingress only serves "+`+strconv.Quote(tunnel.TunnelEndpoint)+`}`)
}

func (s *Server) handleMetricsDefault(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"service":"qsdm-relay","metrics":"/metrics","info":"/info","healthz":"/healthz","version":%q}`, buildVersion)
		return
	}
	s.notFoundOnMetricsSr.Add(1)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"error":"not_found"}`)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	s.healthzRequests.Add(1)
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"status":"ok"}`)
}

// InfoResponse is the public-safe JSON dump on /info.
// Includes the live slot table so an operator at the
// console can see "who's connected right now".
type InfoResponse struct {
	Service       string             `json:"service"`
	Version       string             `json:"version"`
	UptimeSeconds int64              `json:"uptime_seconds"`
	Slots         []tunnel.SlotEvent `json:"slots"`
	SlotsTotal    int                `json:"slots_total"`
	SlotsLive     int                `json:"slots_live"`
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	s.infoRequests.Add(1)
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	live := s.registry.Snapshot()
	resp := InfoResponse{
		Service:       "qsdm-relay",
		Version:       buildVersion,
		UptimeSeconds: int64(time.Since(s.started).Seconds()),
		Slots:         live,
		SlotsTotal:    len(s.auth),
		SlotsLive:     len(live),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.metricsRequests.Add(1)
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	registers, deregisters, collisions := s.registry.Counters()
	live := s.registry.Snapshot()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	body := "" +
		"# HELP qsdm_relay_uptime_seconds Seconds since the relay process started.\n" +
		"# TYPE qsdm_relay_uptime_seconds gauge\n" +
		"qsdm_relay_uptime_seconds " + strconv.FormatFloat(time.Since(s.started).Seconds(), 'f', 1, 64) + "\n" +
		"# HELP qsdm_relay_slots_total Configured slot allowlist size.\n" +
		"# TYPE qsdm_relay_slots_total gauge\n" +
		"qsdm_relay_slots_total " + strconv.Itoa(len(s.auth)) + "\n" +
		"# HELP qsdm_relay_slots_live Currently-connected tunnel sessions.\n" +
		"# TYPE qsdm_relay_slots_live gauge\n" +
		"qsdm_relay_slots_live " + strconv.Itoa(len(live)) + "\n" +
		"# HELP qsdm_relay_tunnel_registers_total Cumulative tunnel registrations.\n" +
		"# TYPE qsdm_relay_tunnel_registers_total counter\n" +
		"qsdm_relay_tunnel_registers_total " + strconv.FormatUint(registers, 10) + "\n" +
		"# HELP qsdm_relay_tunnel_deregisters_total Cumulative tunnel deregistrations.\n" +
		"# TYPE qsdm_relay_tunnel_deregisters_total counter\n" +
		"qsdm_relay_tunnel_deregisters_total " + strconv.FormatUint(deregisters, 10) + "\n" +
		"# HELP qsdm_relay_tunnel_collisions_total Tunnel registrations rejected because slot was already live.\n" +
		"# TYPE qsdm_relay_tunnel_collisions_total counter\n" +
		"qsdm_relay_tunnel_collisions_total " + strconv.FormatUint(collisions, 10) + "\n" +
		"# HELP qsdm_relay_proxy_requests_total Public miner HTTP requests handled.\n" +
		"# TYPE qsdm_relay_proxy_requests_total counter\n" +
		"qsdm_relay_proxy_requests_total " + strconv.FormatUint(s.proxyRequests.Load(), 10) + "\n" +
		"# HELP qsdm_relay_upgrade_requests_total Tunnel-ingress upgrade requests received.\n" +
		"# TYPE qsdm_relay_upgrade_requests_total counter\n" +
		"qsdm_relay_upgrade_requests_total " + strconv.FormatUint(s.upgradeRequests.Load(), 10) + "\n" +
		"# HELP qsdm_relay_upgrade_rejected_total Tunnel-ingress upgrade requests rejected (auth, slot, etc).\n" +
		"# TYPE qsdm_relay_upgrade_rejected_total counter\n" +
		"qsdm_relay_upgrade_rejected_total " + strconv.FormatUint(s.upgradeRejected.Load(), 10) + "\n"
	_, _ = io.WriteString(w, body)
}

// wrapProxyMetrics increments per-request counters then
// delegates to the wrapped handler. We do NOT inspect the
// response body — the inner handler already correctly
// returns 502 on missing-slot, 400 on malformed path.
func (s *Server) wrapProxyMetrics(inner http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.proxyRequests.Add(1)
		inner.ServeHTTP(&proxyStatusInterceptor{ResponseWriter: w, srv: s, status: 0}, r)
	}
}

// wrapUpgradeMetrics increments a counter on every upgrade
// attempt + on every rejection (status >= 400). Wrapped
// around tunnel.HandleUpgrade so the relay surfaces auth-
// failure rates without changing pkg/tunnel's API.
func (s *Server) wrapUpgradeMetrics(inner http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.upgradeRequests.Add(1)
		inner.ServeHTTP(&upgradeStatusInterceptor{ResponseWriter: w, srv: s, status: 0}, r)
	}
}

// proxyStatusInterceptor + upgradeStatusInterceptor capture
// the status code so we can update rejection counters
// without forcing pkg/tunnel to know about metrics. Both
// types implement net/http.Hijacker (when the underlying
// ResponseWriter does) so HandleUpgrade's Hijack call still
// works through the wrapper.

type proxyStatusInterceptor struct {
	http.ResponseWriter
	srv    *Server
	status int
}

func (p *proxyStatusInterceptor) WriteHeader(code int) {
	if p.status == 0 {
		p.status = code
		switch code {
		case http.StatusBadGateway:
			p.srv.proxyMissingSlot.Add(1)
		case http.StatusBadRequest:
			p.srv.proxyBadSlotChars.Add(1)
		}
	}
	p.ResponseWriter.WriteHeader(code)
}

type upgradeStatusInterceptor struct {
	http.ResponseWriter
	srv    *Server
	status int
}

func (u *upgradeStatusInterceptor) WriteHeader(code int) {
	if u.status == 0 {
		u.status = code
		if code >= 400 {
			u.srv.upgradeRejected.Add(1)
		}
	}
	u.ResponseWriter.WriteHeader(code)
}

// Hijack lets the wrapped handler hijack the connection
// (required by tunnel.HandleUpgrade). Pass-through to the
// inner ResponseWriter; if it doesn't support Hijack the
// inner handler will surface the right 500 error.
func (u *upgradeStatusInterceptor) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := u.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("relay: ResponseWriter %T does not support Hijack", u.ResponseWriter)
}

// Flush lets streamed responses (none today, but reserved)
// pass through.
func (u *upgradeStatusInterceptor) Flush() {
	if f, ok := u.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
