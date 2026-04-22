package alerting

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

var (
	defaultManager *Manager
	once           sync.Once
)

func getManager() *Manager {
	once.Do(func() {
		defaultManager = NewManager()
	})
	return defaultManager
}

// Severity levels for alerts
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// AlertEvent is a structured alert payload
type AlertEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Severity  Severity  `json:"severity"`
	Message   string    `json:"message"`
	Source    string    `json:"source,omitempty"`
}

// Manager dispatches alerts to configured backends
type Manager struct {
	mu         sync.Mutex
	webhookURL string
	logger     *log.Logger
}

// NewManager creates a Manager that logs locally and optionally POSTs to a webhook.
// Set QSDM_ALERT_WEBHOOK to enable the webhook backend.
func NewManager() *Manager {
	m := &Manager{
		logger: log.New(os.Stderr, "[ALERT] ", log.LstdFlags|log.Lmsgprefix),
	}
	if url := os.Getenv("QSDM_ALERT_WEBHOOK"); url != "" {
		m.webhookURL = url
	}
	return m
}

// SetWebhook configures (or clears) the webhook URL at runtime.
func (m *Manager) SetWebhook(url string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.webhookURL = url
}

// Send dispatches an alert to all configured backends.
func (m *Manager) Send(evt AlertEvent) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now()
	}

	m.logger.Printf("[%s] %s", evt.Severity, evt.Message)

	m.mu.Lock()
	url := m.webhookURL
	m.mu.Unlock()

	if url != "" {
		go m.postWebhook(url, evt)
	}
}

func (m *Manager) postWebhook(url string, evt AlertEvent) {
	body, err := json.Marshal(evt)
	if err != nil {
		m.logger.Printf("webhook marshal error: %v", err)
		return
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		m.logger.Printf("webhook POST error: %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		m.logger.Printf("webhook returned %d", resp.StatusCode)
	}
}

// ---------- package-level convenience functions ----------

// Alert sends an info-level alert (backward-compatible with old API).
func Alert(message string) {
	getManager().Send(AlertEvent{Severity: SeverityInfo, Message: message})
}

// Alertf sends a formatted info-level alert.
func Alertf(format string, args ...interface{}) {
	Alert(fmt.Sprintf(format, args...))
}

// Warn sends a warning-level alert.
func Warn(message string) {
	getManager().Send(AlertEvent{Severity: SeverityWarning, Message: message})
}

// Critical sends a critical-level alert.
func Critical(message string) {
	getManager().Send(AlertEvent{Severity: SeverityCritical, Message: message})
}

// SetWebhookURL configures the global webhook URL.
func SetWebhookURL(url string) {
	getManager().SetWebhook(url)
}
