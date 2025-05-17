package alerting

import (
    "log"
)

// AlertType represents the type of alert
type AlertType string

const (
    AlertQuarantineTriggered AlertType = "QuarantineTriggered"
    AlertQuarantineRemoved   AlertType = "QuarantineRemoved"
    AlertReputationPenalty   AlertType = "ReputationPenalty"
)

// Alert represents an alert event
type Alert struct {
    Type    AlertType
    Message string
}

// AlertSender defines the interface for sending alerts
type AlertSender interface {
    SendAlert(alert Alert) error
}

// ConsoleAlertSender is a simple alert sender that logs alerts to console
type ConsoleAlertSender struct{}

func (c *ConsoleAlertSender) SendAlert(alert Alert) error {
    log.Printf("[ALERT] %s: %s\n", alert.Type, alert.Message)
    return nil
}

// Global alert sender instance (can be replaced with other implementations)
var Sender AlertSender = &ConsoleAlertSender{}

// Send sends an alert using the global sender
func Send(alert Alert) error {
    return Sender.SendAlert(alert)
}
