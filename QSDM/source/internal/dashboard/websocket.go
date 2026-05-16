package dashboard

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/gorilla/websocket"
)

// wsAllowedOriginsValue is read atomically by the upgrader's
// CheckOrigin callback. Updated by SetWebSocketAllowedOrigins so a
// test or operator runbook can change the allowlist without
// restarting the dashboard process. nil pointer ⇒ allowlist not yet
// initialised ⇒ permissive (production wiring MUST call
// SetWebSocketAllowedOrigins at boot; the dev path leaves it nil so
// `wscat` works locally).
var wsAllowedOriginsValue atomic.Pointer[[]string]

// initialiseWSAllowedOriginsFromEnv reads the production-default
// origin list at first use. We allow either an explicit
// QSDM_WS_ALLOWED_ORIGINS list (comma-separated) OR fall back to
// QSDM_CORS_ALLOWED_ORIGINS so the operator runbook for setting up
// a public dashboard does not require two separate variables.
// Empty / unset means "no allowlist" (dev mode).
//
// Audit row net-04 ("WebSocket origin validation"): the upgrader's
// CheckOrigin used to be `func(*http.Request) bool { return true }`.
// That is permissive for ALL origins — fine in dev but in production
// it lets any web page on any domain open a WebSocket against the
// validator's dashboard, which can be a CSRF-shaped surface for any
// streaming endpoint that pushes account / metrics data. Switching
// to the allowlist-checked closure below closes that gap; the
// counter qsdm_security_cors_rejections_total is bumped on each
// reject so dashboards can alert on probing.
var wsAllowedOriginsInitOnce sync.Once

func initialiseWSAllowedOriginsFromEnv() {
	wsAllowedOriginsInitOnce.Do(func() {
		raw := strings.TrimSpace(os.Getenv("QSDM_WS_ALLOWED_ORIGINS"))
		if raw == "" {
			raw = strings.TrimSpace(os.Getenv("QSDM_CORS_ALLOWED_ORIGINS"))
		}
		if raw == "" {
			return
		}
		list := splitAndTrim(raw)
		if len(list) > 0 {
			SetWebSocketAllowedOrigins(list)
		}
	})
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// SetWebSocketAllowedOrigins installs (or, with an empty slice,
// clears) the allowlist consulted by the WebSocket upgrader's
// CheckOrigin callback. Concurrency-safe; safe to call from a
// config-reload path.
//
// An entry matches if it exactly equals the request's Origin header
// (after URL-canonicalisation: scheme + host[:port], lower-cased
// host). Wildcards / subdomain glob are intentionally NOT supported
// — the audit row asks for "production validates Origin", and a
// strict-match allowlist is the simplest implementation that is
// also the safest.
func SetWebSocketAllowedOrigins(origins []string) {
	if len(origins) == 0 {
		wsAllowedOriginsValue.Store(nil)
		return
	}
	// Canonicalise: lowercase the host so a mismatched-case Origin
	// (e.g. "https://Dashboard.Qsdm.Tech") still matches.
	cleaned := make([]string, 0, len(origins))
	for _, o := range origins {
		u, err := url.Parse(strings.TrimSpace(o))
		if err != nil || u.Scheme == "" || u.Host == "" {
			continue
		}
		canon := u.Scheme + "://" + strings.ToLower(u.Host)
		cleaned = append(cleaned, canon)
	}
	cp := append([]string(nil), cleaned...)
	wsAllowedOriginsValue.Store(&cp)
}

// WebSocketAllowedOriginsSnapshot returns a copy of the current
// allowlist (or nil if none is installed). Used by tests and
// /api/v1/status dashboards to confirm the wiring matches the
// operator's expectation.
func WebSocketAllowedOriginsSnapshot() []string {
	p := wsAllowedOriginsValue.Load()
	if p == nil {
		return nil
	}
	out := make([]string, len(*p))
	copy(out, *p)
	return out
}

// wsCheckOrigin is the production CheckOrigin callback. Empty
// allowlist ⇒ permissive (dev mode); installed allowlist ⇒
// strict match against canonicalised scheme://host.
func wsCheckOrigin(r *http.Request) bool {
	initialiseWSAllowedOriginsFromEnv()
	p := wsAllowedOriginsValue.Load()
	if p == nil || len(*p) == 0 {
		// Dev mode: no allowlist configured. Production deploys are
		// expected to set QSDM_WS_ALLOWED_ORIGINS or fall through to
		// QSDM_CORS_ALLOWED_ORIGINS — see audit row net-04.
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin header: typical for non-browser clients (wscat,
		// k6, etc). In production we REJECT these to keep the gate
		// CSRF-shaped: any user-agent presenting a WebSocket upgrade
		// from a page that knows what it's doing will set Origin.
		monitoring.RecordCORSRejection()
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		monitoring.RecordCORSRejection()
		return false
	}
	canon := u.Scheme + "://" + strings.ToLower(u.Host)
	for _, allowed := range *p {
		if canon == allowed {
			return true
		}
	}
	monitoring.RecordCORSRejection()
	return false
}

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	// Audit row net-04: production validates Origin against the
	// allowlist set by SetWebSocketAllowedOrigins (or its env
	// equivalents QSDM_WS_ALLOWED_ORIGINS /
	// QSDM_CORS_ALLOWED_ORIGINS). Unset allowlist preserves the
	// permissive dev-mode behaviour.
	CheckOrigin: wsCheckOrigin,
}

// WSMessage is a typed envelope for WebSocket push messages.
type WSMessage struct {
	Type string      `json:"type"` // "metrics", "health", "event", "topology"
	Data interface{} `json:"data"`
}

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// WSHub manages connected WebSocket clients and broadcasts messages.
type WSHub struct {
	mu         sync.RWMutex
	clients    map[*wsClient]bool
	broadcast  chan []byte
	register   chan *wsClient
	unregister chan *wsClient
	stopCh     chan struct{}
	wg         sync.WaitGroup
}

// NewWSHub creates a hub for WebSocket connections.
func NewWSHub() *WSHub {
	return &WSHub{
		clients:    make(map[*wsClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *wsClient),
		unregister: make(chan *wsClient),
		stopCh:     make(chan struct{}),
	}
}

// Run starts the hub message loop. Call in a goroutine.
func (h *WSHub) Run() {
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case <-h.stopCh:
				h.mu.Lock()
				for c := range h.clients {
					close(c.send)
					c.conn.Close()
				}
				h.clients = make(map[*wsClient]bool)
				h.mu.Unlock()
				return

			case client := <-h.register:
				h.mu.Lock()
				h.clients[client] = true
				h.mu.Unlock()

			case client := <-h.unregister:
				h.mu.Lock()
				if _, ok := h.clients[client]; ok {
					delete(h.clients, client)
					close(client.send)
				}
				h.mu.Unlock()

			case message := <-h.broadcast:
				h.mu.RLock()
				for client := range h.clients {
					select {
					case client.send <- message:
					default:
						// slow client — drop and disconnect
						h.mu.RUnlock()
						h.mu.Lock()
						delete(h.clients, client)
						close(client.send)
						h.mu.Unlock()
						h.mu.RLock()
					}
				}
				h.mu.RUnlock()
			}
		}
	}()
}

// Stop shuts down the hub.
func (h *WSHub) Stop() {
	close(h.stopCh)
	h.wg.Wait()
}

// Broadcast sends a message to all connected clients.
func (h *WSHub) Broadcast(msgType string, data interface{}) {
	msg := WSMessage{Type: msgType, Data: data}
	raw, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case h.broadcast <- raw:
	default:
		// broadcast channel full, drop message
	}
}

// ClientCount returns how many clients are connected.
func (h *WSHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ServeWS handles the HTTP upgrade to WebSocket.
func (h *WSHub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}

	client := &wsClient{
		conn: conn,
		send: make(chan []byte, 64),
	}
	h.register <- client

	go h.writePump(client)
	go h.readPump(client)
}

func (h *WSHub) writePump(c *wsClient) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (h *WSHub) readPump(c *wsClient) {
	defer func() {
		h.unregister <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}
