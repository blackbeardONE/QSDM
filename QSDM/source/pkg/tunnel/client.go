package tunnel

// Client side of the QSDM reverse-HTTP tunnel: the
// piece that runs INSIDE qsdm-attester (or any future binary
// that wants to publish its local HTTP service through the
// relay). The client opens a single outbound HTTPS connection
// to the relay, performs the 101 Upgrade handshake, hijacks
// the resulting TCP connection, wraps it in a yamux server,
// and serves every incoming yamux stream by handing it to a
// caller-supplied http.Handler.
//
// Usage from cmd/qsdm-attester:
//
//	cli := tunnel.Client{
//	    RelayURL: "https://relay.qsdm.tech",
//	    SlotID:   "blackbeard-3050",
//	    SignerID: signer.SignerID(),
//	    Key:      key,
//	    Handler:  attesterMux, // SAME mux as the local listener
//	    Logf:     log.Printf,
//	}
//	go cli.Run(ctx) // reconnects forever; respects ctx cancellation

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/libp2p/go-yamux/v5"
)

// Client holds all the state needed to maintain a tunnel.
// Zero value is invalid; use the field documentation as
// required-vs-optional. Run blocks until ctx is cancelled,
// reconnecting with exponential backoff on any underlying
// error so a flaky home internet connection self-heals.
type Client struct {
	// RelayURL is the public URL of the relay's tunnel
	// ingress (e.g. "https://relay.qsdm.tech"). Scheme MUST
	// be http or https. The path TunnelEndpoint is appended
	// automatically — pass only the origin.
	RelayURL string

	// SlotID is the registered slot name on the relay
	// (e.g. "blackbeard-3050"). Public miners reach this
	// attester at <relay>/<SlotID>/api/v1/mining/challenge.
	SlotID string

	// SignerID is logged + sent in the X-QSDM-Signer-ID
	// header. The relay does NOT use it for auth (that
	// belongs to SlotID + Key) but operators set it so
	// log lines on the relay say which attester just
	// connected.
	SignerID string

	// Key is the 32-byte HMAC key shared with the relay's
	// slot allowlist entry. Same kind of key as the
	// attester's signer key — operators typically reuse
	// the signer key directly so they only manage one
	// secret per attester.
	Key []byte

	// Handler is the local HTTP mux that should serve
	// requests arriving via the tunnel. Pass the same
	// *ServeMux the attester's local 127.0.0.1:7733
	// listener uses, so the public-facing behaviour is
	// byte-identical to the local one.
	Handler http.Handler

	// TLSConfig overrides the default tls.Config when
	// dialing https://. nil = use the system default
	// (recommended).
	TLSConfig *tls.Config

	// Logf is the structured logger. The first argument is
	// a fixed event name (e.g. "tunnel: session established"),
	// followed by alternating key/value pairs. nil =
	// silently swallow log messages (useful in tests).
	// Production callers should always set it.
	Logf func(msg string, kv ...any)

	// MinBackoff/MaxBackoff bound the retry-loop wait
	// between failed connection attempts. Zero falls back
	// to 1s / 60s respectively — sane defaults for a home
	// connection.
	MinBackoff time.Duration
	MaxBackoff time.Duration
}

// Run blocks until ctx is cancelled. It runs an outer reconnect
// loop: dial, serve, on disconnect wait backoff, repeat. Returns
// nil on a clean ctx cancellation, the last error otherwise.
func (c *Client) Run(ctx context.Context) error {
	if err := c.validate(); err != nil {
		return err
	}
	logf := c.logger()
	backoff := c.minBackoff()
	max := c.maxBackoff()

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		err := c.runOnce(ctx)
		if err == nil || errors.Is(err, context.Canceled) {
			// Clean shutdown.
			return nil
		}
		logf("tunnel: session ended", "err", err.Error(), "retry_in", backoff.String())
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > max {
			backoff = max
		}
	}
}

// runOnce performs exactly one connect → serve → disconnect
// cycle. Surfaces the underlying error to Run for backoff
// accounting. Exposed (lowercase) so tests can drive a
// single iteration without the retry loop.
func (c *Client) runOnce(ctx context.Context) error {
	conn, err := c.dial(ctx)
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}
	// dial returns the raw net.Conn AFTER a successful
	// handshake. From here, both sides speak yamux.
	cfg := yamux.DefaultConfig()
	// yamux's default LogOutput is os.Stderr. Silence it
	// here because the Client's own Logf provides the
	// observable surface; yamux internals would otherwise
	// double-print on every disconnect.
	cfg.LogOutput = io.Discard

	session, err := yamux.Server(conn, cfg, nil)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("yamux server: %w", err)
	}
	defer session.Close()

	c.logger()("tunnel: session established", "slot", c.SlotID, "relay", c.RelayURL)

	// Stop the session when ctx cancels. Without this, a
	// hung session would prevent Run from returning on
	// shutdown.
	go func() {
		<-ctx.Done()
		_ = session.Close()
	}()

	// http.Serve blocks until the listener (the yamux
	// session) closes. Each accepted yamux stream is
	// dispatched to c.Handler exactly like a local HTTP
	// request — the attester sees no difference.
	srv := &http.Server{
		Handler:           c.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// *yamux.Session already implements net.Listener
	// (Accept, Close, Addr) — pass it straight to Serve.
	if err := srv.Serve(session); err != nil &&
		!errors.Is(err, yamux.ErrSessionShutdown) &&
		!strings.Contains(err.Error(), "use of closed network connection") {
		return fmt.Errorf("http serve: %w", err)
	}
	return nil
}

// dial opens the relay-side TCP+TLS connection AND completes
// the HTTP/1.1 Upgrade handshake. On success the returned
// net.Conn is positioned right after the relay's
// "101 Switching Protocols\r\n\r\n" response — both sides can
// start speaking yamux immediately.
//
// Steps:
//
//  1. Resolve RelayURL (scheme + host).
//  2. tls.Dial (or net.Dial for plain http).
//  3. Write a hand-rolled HTTP/1.1 Upgrade request — we do NOT
//     use net/http's Client because it would auto-buffer the
//     hijacked body and drop the bytes that come after the
//     Upgrade response.
//  4. Read response line + headers via bufio.Reader.
//  5. Validate 101 + Upgrade: qsdm-tunnel/1.
//  6. Return the underlying conn (the bufio.Reader's buffered
//     bytes, if any, are placed in front of the conn via a
//     small wrapper so no protocol bytes are lost).
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	u, err := url.Parse(c.RelayURL)
	if err != nil {
		return nil, fmt.Errorf("bad RelayURL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("RelayURL scheme %q must be http or https", u.Scheme)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}

	d := &net.Dialer{Timeout: 15 * time.Second}
	rawConn, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, fmt.Errorf("tcp dial %s: %w", host, err)
	}

	var conn net.Conn = rawConn
	if u.Scheme == "https" {
		tlsCfg := c.TLSConfig
		if tlsCfg == nil {
			tlsCfg = &tls.Config{ServerName: u.Hostname()}
		} else if tlsCfg.ServerName == "" {
			tlsCfg = tlsCfg.Clone()
			tlsCfg.ServerName = u.Hostname()
		}
		tlsConn := tls.Client(rawConn, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = rawConn.Close()
			return nil, fmt.Errorf("tls handshake %s: %w", host, err)
		}
		conn = tlsConn
	}

	// Compute the auth headers.
	now := time.Now().Unix()
	authIn := AuthInputs{
		Version:   UpgradeProtocol,
		SlotID:    c.SlotID,
		SignerID:  c.SignerID,
		Timestamp: now,
	}
	auth, err := SignAuth(c.Key, authIn)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("sign auth: %w", err)
	}

	// Hand-roll the Upgrade request. We deliberately do not
	// use http.NewRequest + Transport because Transport
	// would treat our "101 Switching Protocols" response as
	// a normal response and stash any subsequent bytes in
	// an internal bufio reader we cannot reach.
	req := "GET " + TunnelEndpoint + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: " + UpgradeProtocol + "\r\n" +
		HeaderVersion + ": " + UpgradeProtocol + "\r\n" +
		HeaderSlotID + ": " + c.SlotID + "\r\n" +
		HeaderSignerID + ": " + c.SignerID + "\r\n" +
		HeaderTimestamp + ": " + fmt.Sprintf("%d", now) + "\r\n" +
		HeaderAuth + ": " + auth + "\r\n" +
		"\r\n"

	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write upgrade request: %w", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read upgrade response: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body := readSmall(resp.Body, 256)
		_ = resp.Body.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("relay rejected upgrade: status=%d body=%q",
			resp.StatusCode, body)
	}
	if up := resp.Header.Get("Upgrade"); !strings.EqualFold(up, UpgradeProtocol) {
		_ = resp.Body.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("relay accepted but with wrong Upgrade=%q (want %s)",
			up, UpgradeProtocol)
	}
	_ = resp.Body.Close()

	// If bufio buffered any bytes after the response
	// headers, they belong to the post-101 byte stream
	// (yamux frames). Stitch them back in front of the
	// underlying conn so the yamux session sees them.
	buffered := br.Buffered()
	if buffered > 0 {
		peek, _ := br.Peek(buffered)
		conn = &prefixedConn{prefix: append([]byte(nil), peek...), Conn: conn}
	}
	return conn, nil
}

func (c *Client) validate() error {
	if c == nil {
		return errors.New("tunnel: nil Client")
	}
	if c.RelayURL == "" {
		return errors.New("tunnel: Client.RelayURL empty")
	}
	if !ValidSlotID(c.SlotID) {
		return fmt.Errorf("tunnel: Client.SlotID %q invalid", c.SlotID)
	}
	if c.SignerID == "" {
		return errors.New("tunnel: Client.SignerID empty")
	}
	if len(c.Key) < 16 {
		return fmt.Errorf("tunnel: Client.Key length %d < 16", len(c.Key))
	}
	if c.Handler == nil {
		return errors.New("tunnel: Client.Handler nil")
	}
	return nil
}

func (c *Client) logger() func(string, ...any) {
	if c.Logf == nil {
		return func(string, ...any) {}
	}
	return c.Logf
}

func (c *Client) minBackoff() time.Duration {
	if c.MinBackoff <= 0 {
		return 1 * time.Second
	}
	return c.MinBackoff
}

func (c *Client) maxBackoff() time.Duration {
	if c.MaxBackoff <= 0 {
		return 60 * time.Second
	}
	return c.MaxBackoff
}

// readSmall drains up to n bytes from r without panicking on
// short reads. Used only to enrich error messages with the
// relay's rejection body — never on the hot path.
func readSmall(r interface{ Read([]byte) (int, error) }, n int) string {
	if r == nil {
		return ""
	}
	buf := make([]byte, n)
	got, _ := r.Read(buf)
	return string(buf[:got])
}

// prefixedConn lets us re-emit bytes that were already read
// into a bufio.Reader during the Upgrade handshake. The
// dial() helper attaches one of these whenever
// http.ReadResponse over-read into the post-101 byte stream.
type prefixedConn struct {
	prefix []byte
	net.Conn
}

func (p *prefixedConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

