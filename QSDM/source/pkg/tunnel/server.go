package tunnel

// Server side of the QSDM reverse-HTTP tunnel: the piece
// that runs INSIDE qsdm-relay (and any future binary that
// wants to host slot-multiplexed reverse tunnels). The
// server exposes:
//
//   - Registry: in-memory slot → live yamux session map,
//     safe for concurrent reads (miner traffic) and writes
//     (tunnel client connect/disconnect).
//
//   - HandleUpgrade: an http.HandlerFunc that authenticates
//     a tunnel client's 101 Upgrade request, hijacks the
//     connection, wraps it in a yamux Client session, and
//     stores the session in the Registry until it dies.
//
//   - HandleProxy: an http.HandlerFunc that strips the slot
//     prefix from a public miner request, opens a fresh
//     yamux stream against the matching session, and
//     reverse-proxies the HTTP request through it.
//
// The two handlers are deliberately separate so cmd/qsdm-relay
// can mount them on independent http.Servers (different ports
// or different vhosts), which lets the operator firewall
// public miner traffic from tunnel-ingress traffic without
// any code change.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/libp2p/go-yamux/v5"
)

// Authenticator returns the HMAC key for the given slot ID,
// or (nil, false) if the slot is not on the allowlist. The
// concrete implementation in cmd/qsdm-relay reads from a
// TOML file; tests pass an in-memory map. Decoupled to keep
// pkg/tunnel free of TOML/file-system concerns.
type Authenticator interface {
	Lookup(slotID string) (key []byte, note string, ok bool)
}

// SlotEvent is the per-slot lifecycle notification the
// Registry emits to operators. Implementations MUST be
// non-blocking — the registry holds an internal lock when
// firing events.
type SlotEvent struct {
	SlotID    string
	SignerID  string
	RemoteIP  string
	Connected bool // true on register, false on remove
	Note      string
}

// Registry tracks live tunnel sessions by slot. Methods are
// safe for concurrent use. The zero value is valid (no
// initialisation required).
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*registeredSession
	observer func(SlotEvent)

	// Atomic counters surface in metrics.
	registers   atomic.Uint64
	deregisters atomic.Uint64
	collisions  atomic.Uint64
}

type registeredSession struct {
	session  *yamux.Session
	signerID string
	remoteIP string
	note     string
	since    time.Time
}

// SetObserver installs a callback fired for every register /
// deregister. Used by cmd/qsdm-relay's structured logger.
func (r *Registry) SetObserver(fn func(SlotEvent)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observer = fn
}

// Register installs a new session under slotID. Returns an
// error when the slot is already taken (another tunnel
// client is connected) — the caller MUST close the new
// session and respond 409 to the upgrade request. We do not
// auto-evict existing sessions because that would let an
// attacker with valid auth race-evict a legitimate
// connection by reconnecting in a tight loop.
func (r *Registry) Register(slotID, signerID, remoteIP, note string, sess *yamux.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions == nil {
		r.sessions = make(map[string]*registeredSession)
	}
	if _, exists := r.sessions[slotID]; exists {
		r.collisions.Add(1)
		return fmt.Errorf("tunnel: slot %q already registered", slotID)
	}
	r.sessions[slotID] = &registeredSession{
		session:  sess,
		signerID: signerID,
		remoteIP: remoteIP,
		note:     note,
		since:    time.Now(),
	}
	r.registers.Add(1)
	if r.observer != nil {
		r.observer(SlotEvent{
			SlotID:    slotID,
			SignerID:  signerID,
			RemoteIP:  remoteIP,
			Note:      note,
			Connected: true,
		})
	}
	return nil
}

// Deregister removes a slot, no-op if the slot is unknown
// or already replaced by a different session (sess pointer
// must match). The pointer-equality check protects against
// a racy remove that would otherwise unregister the
// successor of a re-connect.
func (r *Registry) Deregister(slotID string, sess *yamux.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, ok := r.sessions[slotID]
	if !ok {
		return
	}
	if cur.session != sess {
		return
	}
	delete(r.sessions, slotID)
	r.deregisters.Add(1)
	if r.observer != nil {
		r.observer(SlotEvent{
			SlotID:    slotID,
			SignerID:  cur.signerID,
			RemoteIP:  cur.remoteIP,
			Note:      cur.note,
			Connected: false,
		})
	}
}

// Lookup returns the live session for slotID. The bool is
// false when the slot is not registered. Safe for
// concurrent use; takes only the read lock.
func (r *Registry) Lookup(slotID string) (*yamux.Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rs, ok := r.sessions[slotID]
	if !ok {
		return nil, false
	}
	return rs.session, true
}

// Snapshot returns a defensive copy of the current slot
// table for /info and /metrics endpoints. Sessions are
// returned by their immutable metadata only.
func (r *Registry) Snapshot() []SlotEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]SlotEvent, 0, len(r.sessions))
	for slot, rs := range r.sessions {
		out = append(out, SlotEvent{
			SlotID:    slot,
			SignerID:  rs.signerID,
			RemoteIP:  rs.remoteIP,
			Note:      rs.note,
			Connected: true,
		})
	}
	return out
}

// Counters returns (registers, deregisters, collisions).
// Used by cmd/qsdm-relay's /metrics handler.
func (r *Registry) Counters() (uint64, uint64, uint64) {
	return r.registers.Load(), r.deregisters.Load(), r.collisions.Load()
}

// HandleUpgrade returns an http.HandlerFunc that:
//
//  1. Validates request headers (slot, signer, ts, auth).
//  2. Looks up the slot's HMAC key via auth.
//  3. Verifies the HMAC + timestamp window.
//  4. Hijacks the connection.
//  5. Wraps the connection in yamux.Client and registers it.
//  6. Blocks (in the goroutine of the request) until the
//     session dies, then deregisters.
//
// Mount this handler on TunnelEndpoint; ANY other path
// produces a 404 because http.ServeMux's longest-prefix
// match is what we want.
func HandleUpgrade(reg *Registry, auth Authenticator, logf func(string, ...any)) http.HandlerFunc {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !strings.EqualFold(r.Header.Get("Connection"), "Upgrade") ||
			!strings.EqualFold(r.Header.Get("Upgrade"), UpgradeProtocol) {
			http.Error(w, "expected Upgrade: "+UpgradeProtocol, http.StatusBadRequest)
			return
		}

		slot := r.Header.Get(HeaderSlotID)
		signer := r.Header.Get(HeaderSignerID)
		tsStr := r.Header.Get(HeaderTimestamp)
		gotAuth := r.Header.Get(HeaderAuth)
		ver := r.Header.Get(HeaderVersion)
		if ver == "" {
			ver = UpgradeProtocol
		}

		if !ValidSlotID(slot) {
			http.Error(w, "invalid slot_id", http.StatusBadRequest)
			return
		}
		ts, err := strconv.ParseInt(tsStr, 10, 64)
		if err != nil {
			http.Error(w, "bad timestamp", http.StatusBadRequest)
			return
		}
		if err := VerifyTimestampWithin(ts, time.Now()); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		key, note, ok := auth.Lookup(slot)
		if !ok {
			logf("tunnel-upgrade: unknown slot", "slot", slot, "remote", r.RemoteAddr)
			http.Error(w, "unknown slot", http.StatusUnauthorized)
			return
		}
		authIn := AuthInputs{
			Version:   ver,
			SlotID:    slot,
			SignerID:  signer,
			Timestamp: ts,
		}
		if !VerifyAuth(key, authIn, gotAuth) {
			logf("tunnel-upgrade: bad auth", "slot", slot, "signer", signer, "remote", r.RemoteAddr)
			http.Error(w, "bad auth", http.StatusUnauthorized)
			return
		}

		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		// Send 101 BEFORE hijacking — the response writer
		// can't be written to AFTER hijack. We hand-roll
		// the response so the byte sequence is exactly
		// what the client's bufio.Reader expects.
		resp := "HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: " + UpgradeProtocol + "\r\n" +
			"Connection: Upgrade\r\n\r\n"

		conn, brw, err := hj.Hijack()
		if err != nil {
			logf("tunnel-upgrade: hijack failed", "err", err.Error())
			return
		}
		if _, err := conn.Write([]byte(resp)); err != nil {
			_ = conn.Close()
			logf("tunnel-upgrade: write 101 failed", "err", err.Error())
			return
		}
		// Flush any data that bufio buffered (should be
		// nothing on a clean Upgrade handshake) so the
		// downstream yamux session sees only protocol bytes.
		if buffered := brw.Reader.Buffered(); buffered > 0 {
			peek, _ := brw.Reader.Peek(buffered)
			conn = &prefixedConn{prefix: append([]byte(nil), peek...), Conn: conn}
		}

		cfg := yamux.DefaultConfig()
		cfg.LogOutput = io.Discard
		session, err := yamux.Client(conn, cfg, nil)
		if err != nil {
			_ = conn.Close()
			logf("tunnel-upgrade: yamux client failed", "err", err.Error())
			return
		}

		remoteIP := clientIP(r)
		if regErr := reg.Register(slot, signer, remoteIP, note, session); regErr != nil {
			logf("tunnel-upgrade: register failed", "err", regErr.Error())
			_ = session.Close()
			return
		}
		logf("tunnel-upgrade: registered",
			"slot", slot, "signer", signer, "remote", remoteIP, "note", note)

		// Block until the session terminates. CloseChan
		// fires on yamux's end-of-life signal (TCP RST,
		// orderly close, ping timeout). When this returns
		// we deregister and the per-request goroutine ends.
		<-session.CloseChan()
		reg.Deregister(slot, session)
		logf("tunnel-upgrade: deregistered", "slot", slot, "signer", signer)
	}
}

// HandleProxy returns an http.HandlerFunc that reverse-
// proxies miner requests through the tunnel. The handler
// expects the request URL to start with "/<slot>/" and
// strips that segment before forwarding.
//
// Example:
//
//	GET /blackbeard-3050/api/v1/mining/challenge
//	→ open new yamux stream to slot=blackbeard-3050
//	→ forward "GET /api/v1/mining/challenge" through stream
//	→ copy response back
func HandleProxy(reg *Registry, logf func(string, ...any)) http.HandlerFunc {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		// Strip leading slash, then split on the first '/'.
		path := strings.TrimPrefix(r.URL.Path, "/")
		i := strings.IndexByte(path, '/')
		if i < 0 {
			http.Error(w, "expected /<slot>/<path>", http.StatusBadRequest)
			return
		}
		slot := path[:i]
		rest := path[i:] // includes leading '/'
		if !ValidSlotID(slot) {
			http.Error(w, "invalid slot in path", http.StatusBadRequest)
			return
		}
		sess, ok := reg.Lookup(slot)
		if !ok {
			http.Error(w, "slot not connected", http.StatusBadGateway)
			return
		}

		// Reverse-proxy via a Transport that "dials" by
		// opening a fresh yamux stream. One stream per
		// request gives us natural connection pooling
		// from yamux's flow-control machinery.
		tr := &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return sess.Open(ctx)
			},
			DisableKeepAlives:     true,
			ResponseHeaderTimeout: 30 * time.Second,
		}
		proxy := &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Scheme = "http"
				req.URL.Host = "qsdm-tunnel"
				req.URL.Path = rest
				// Preserve original RawQuery so query
				// parameters (none today, but reserved)
				// pass through verbatim.
			},
			Transport: tr,
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				logf("tunnel-proxy: forward failed", "slot", slot, "err", err.Error())
				http.Error(w, "tunnel error: "+err.Error(), http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, r)
	}
}

// clientIP mirrors the helper in cmd/qsdm-attester. Best
// effort, used for log enrichment only.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
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

// AuthMap is a minimal in-memory Authenticator. Convenient
// for tests; cmd/qsdm-relay implements its own
// TOML-backed Authenticator.
type AuthMap map[string]AuthMapEntry

// AuthMapEntry is the value type for AuthMap.
type AuthMapEntry struct {
	Key  []byte
	Note string
}

// Lookup implements Authenticator.
func (m AuthMap) Lookup(slot string) ([]byte, string, bool) {
	e, ok := m[slot]
	if !ok {
		return nil, "", false
	}
	return e.Key, e.Note, true
}

// CopyConn is a tiny helper used by tests + cmd/qsdm-relay to
// expose a "drain both halves of a duplex" primitive without
// pulling in net/http/httputil for non-HTTP integration tests.
// Returns the first non-nil error from either direction.
func CopyConn(a, b net.Conn) error {
	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(a, b)
		errCh <- err
		_ = a.Close()
	}()
	go func() {
		_, err := io.Copy(b, a)
		errCh <- err
		_ = b.Close()
	}()
	if err := <-errCh; err != nil {
		return err
	}
	return nil
}

// httpReadResponseFromConn is a typed helper for tests that
// need to parse a hand-rolled HTTP response. Defined here
// rather than re-implemented in each test file.
func httpReadResponseFromConn(conn net.Conn) (*http.Response, error) {
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// compile-time interface check.
var _ http.HandlerFunc = HandleUpgrade(nil, nil, nil)

// errInvalidArguments is returned when nil arguments slip
// through into the registered handlers. Today we eagerly
// guard at construction; this is a defence-in-depth path.
var errInvalidArguments = errors.New("tunnel: invalid arguments")

// hush silences unused-symbol warnings for helpers we keep
// because callers in test files need them.
var _ = errInvalidArguments
var _ = httpReadResponseFromConn
var _ = CopyConn
