package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWSHub_BroadcastToClients(t *testing.T) {
	hub := NewWSHub()
	hub.Run()
	defer hub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"

	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer conn1.Close()

	conn2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer conn2.Close()

	time.Sleep(50 * time.Millisecond)
	if hub.ClientCount() != 2 {
		t.Fatalf("expected 2 clients, got %d", hub.ClientCount())
	}

	hub.Broadcast("metrics", map[string]interface{}{"cpu": 42})

	for _, conn := range []*websocket.Conn{conn1, conn2} {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var msg WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if msg.Type != "metrics" {
			t.Fatalf("expected type 'metrics', got %q", msg.Type)
		}
	}
}

func TestWSHub_ClientDisconnect(t *testing.T) {
	hub := NewWSHub()
	hub.Run()
	defer hub.Stop()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	if hub.ClientCount() != 1 {
		t.Fatalf("expected 1 client, got %d", hub.ClientCount())
	}

	conn.Close()
	time.Sleep(100 * time.Millisecond)

	// Broadcast should not hang with disconnected client
	hub.Broadcast("test", nil)
	time.Sleep(50 * time.Millisecond)
}

func TestWSHub_StopCleansUp(t *testing.T) {
	hub := NewWSHub()
	hub.Run()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeWS(w, r)
	}))

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"
	conn, _, _ := websocket.DefaultDialer.Dial(wsURL, nil)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)
	hub.Stop()
	srv.Close()

	if hub.ClientCount() != 0 {
		t.Fatalf("expected 0 clients after stop, got %d", hub.ClientCount())
	}
}
