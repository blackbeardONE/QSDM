package dashboard

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
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
