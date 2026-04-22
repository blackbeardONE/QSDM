package alerting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAlertLogsWithoutWebhook(t *testing.T) {
	m := NewManager()
	m.Send(AlertEvent{Severity: SeverityInfo, Message: "test alert"})
}

func TestAlertWebhookDelivery(t *testing.T) {
	var mu sync.Mutex
	var received []AlertEvent

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var evt AlertEvent
		json.NewDecoder(r.Body).Decode(&evt)
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer srv.Close()

	m := NewManager()
	m.SetWebhook(srv.URL)
	m.Send(AlertEvent{Severity: SeverityCritical, Message: "webhook test"})

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 {
		t.Fatalf("expected 1 webhook call, got %d", len(received))
	}
	if received[0].Severity != SeverityCritical {
		t.Errorf("severity = %s, want critical", received[0].Severity)
	}
	if received[0].Message != "webhook test" {
		t.Errorf("message = %q, want 'webhook test'", received[0].Message)
	}
}

func TestPackageLevelFunctions(t *testing.T) {
	Alert("info level")
	Alertf("formatted %d", 42)
	Warn("warning level")
	Critical("critical level")
}
