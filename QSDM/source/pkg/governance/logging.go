package governance

import (
	"github.com/blackbeardONE/QSDM/internal/logging"
)

func LogProposalAdded(logger *logging.Logger, id string) {
	logger.Info("Governance proposal added", "id", id)
}

func LogVoteCast(logger *logging.Logger, proposalID, voterID string, weight int, support bool) {
	logger.Info("Vote cast on proposal", "proposalID", proposalID, "voterID", voterID, "weight", weight, "support", support)
}

func LogProposalFinalized(logger *logging.Logger, proposalID string, passed bool) {
	if passed {
		logger.Info("Governance proposal passed", "proposalID", proposalID)
	} else {
		logger.Info("Governance proposal failed", "proposalID", proposalID)
	}
}
