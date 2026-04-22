package quarantine

import (
	"github.com/blackbeardONE/QSDM/internal/alerting"
	"github.com/blackbeardONE/QSDM/internal/logging"
)

func TriggerQuarantine(logger *logging.Logger, message string) {
	logger.Warn("Quarantine triggered:", message)
	alerting.Alert(message)
	// alerting.AlertQuarantineTriggered() // Remove or implement this function if needed
}
