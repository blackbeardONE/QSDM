package governance

import (
    "github.com/blackbeardONE/QSDM/internal/logging"
)

// LogProposalAdded logs when a proposal is added.
func LogProposalAdded(id string) {
    logging.Info.Printf("Governance proposal added: %s", id)
}

// LogVoteCast logs when a vote is cast.
func LogVoteCast(proposalID, voterID string, weight int, support bool) {
    logging.Info.Printf("Vote cast on proposal %s by voter %s: weight=%d support=%v", proposalID, voterID, weight, support)
}

// LogProposalFinalized logs when a proposal is finalized.
func LogProposalFinalized(proposalID string, passed bool) {
    if passed {
        logging.Info.Printf("Governance proposal passed: %s", proposalID)
    } else {
        logging.Info.Printf("Governance proposal failed: %s", proposalID)
    }
}
