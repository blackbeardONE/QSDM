package governance

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// ActionType defines what an approved proposal triggers.
type ActionType string

const (
	ActionConfigChange    ActionType = "config_change"
	ActionContractUpgrade ActionType = "contract_upgrade"
	ActionParameterSet    ActionType = "parameter_set"
	ActionCustom          ActionType = "custom"
)

// ProposalAction describes the action to execute when a proposal passes.
type ProposalAction struct {
	Type       ActionType             `json:"type"`
	Parameters map[string]interface{} `json:"parameters"`
}

// ExecutionRecord records the result of an executed proposal.
type ExecutionRecord struct {
	ProposalID string     `json:"proposal_id"`
	Action     ActionType `json:"action"`
	Success    bool       `json:"success"`
	Error      string     `json:"error,omitempty"`
	ExecutedAt time.Time  `json:"executed_at"`
}

// ActionHandler is called when a proposal of a given ActionType passes.
type ActionHandler func(proposalID string, params map[string]interface{}) error

// ProposalExecutor monitors proposals and executes actions when they pass.
type ProposalExecutor struct {
	voting     *SnapshotVoting
	actions    map[string]*ProposalAction // proposalID -> action
	handlers   map[ActionType]ActionHandler
	records    []ExecutionRecord
	mu         sync.Mutex
	ctx        chan struct{} // close to stop
	wg         sync.WaitGroup
	interval   time.Duration
}

// NewProposalExecutor creates an executor that watches a SnapshotVoting instance.
func NewProposalExecutor(voting *SnapshotVoting, pollInterval time.Duration) *ProposalExecutor {
	if pollInterval <= 0 {
		pollInterval = 10 * time.Second
	}
	return &ProposalExecutor{
		voting:   voting,
		actions:  make(map[string]*ProposalAction),
		handlers: make(map[ActionType]ActionHandler),
		records:  make([]ExecutionRecord, 0),
		ctx:      make(chan struct{}),
		interval: pollInterval,
	}
}

// RegisterHandler registers a handler for a specific action type.
func (pe *ProposalExecutor) RegisterHandler(actionType ActionType, handler ActionHandler) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.handlers[actionType] = handler
}

// AttachAction links a proposal to an action that executes if the proposal passes.
func (pe *ProposalExecutor) AttachAction(proposalID string, action *ProposalAction) {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	pe.actions[proposalID] = action
}

// Start begins the execution polling loop.
func (pe *ProposalExecutor) Start() {
	pe.wg.Add(1)
	go func() {
		defer pe.wg.Done()
		ticker := time.NewTicker(pe.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pe.pollAndExecute()
			case <-pe.ctx:
				return
			}
		}
	}()
}

// Stop halts the executor.
func (pe *ProposalExecutor) Stop() {
	close(pe.ctx)
	pe.wg.Wait()
}

func (pe *ProposalExecutor) pollAndExecute() {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	pe.voting.Mu.RLock()
	proposals := make(map[string]*Proposal, len(pe.voting.Proposals))
	for k, v := range pe.voting.Proposals {
		proposals[k] = v
	}
	pe.voting.Mu.RUnlock()

	for pid, proposal := range proposals {
		action, hasAction := pe.actions[pid]
		if !hasAction {
			continue
		}

		// Skip already-executed proposals
		if pe.alreadyExecuted(pid) {
			continue
		}

		// Only act on expired, non-finalized proposals (auto-finalize if needed)
		if !proposal.Finalized && time.Now().After(proposal.ExpiresAt) {
			passed, err := pe.voting.FinalizeProposal(pid)
			if err != nil {
				log.Printf("[governance-executor] finalize %s: %v", pid, err)
				pe.recordExecution(pid, action.Type, false, fmt.Sprintf("finalize: %v", err))
				continue
			}
			if !passed {
				pe.recordExecution(pid, action.Type, false, "proposal did not pass")
				continue
			}
		} else if !proposal.Finalized {
			continue // not yet expired
		} else {
			// Already finalized — check if it passed (majority + quorum)
			totalVotes := proposal.VotesFor + proposal.VotesAgainst
			if proposal.VotesFor <= proposal.VotesAgainst || totalVotes < proposal.Quorum {
				pe.recordExecution(pid, action.Type, false, "proposal did not pass")
				continue
			}
		}

		// Execute the action
		handler, ok := pe.handlers[action.Type]
		if !ok {
			pe.recordExecution(pid, action.Type, false, fmt.Sprintf("no handler for action type %s", action.Type))
			continue
		}

		err := handler(pid, action.Parameters)
		if err != nil {
			log.Printf("[governance-executor] execute %s (%s): %v", pid, action.Type, err)
			pe.recordExecution(pid, action.Type, false, err.Error())
		} else {
			log.Printf("[governance-executor] executed %s (%s) successfully", pid, action.Type)
			pe.recordExecution(pid, action.Type, true, "")
		}
	}
}

func (pe *ProposalExecutor) alreadyExecuted(proposalID string) bool {
	for _, r := range pe.records {
		if r.ProposalID == proposalID {
			return true
		}
	}
	return false
}

func (pe *ProposalExecutor) recordExecution(proposalID string, action ActionType, success bool, errMsg string) {
	pe.records = append(pe.records, ExecutionRecord{
		ProposalID: proposalID,
		Action:     action,
		Success:    success,
		Error:      errMsg,
		ExecutedAt: time.Now(),
	})
}

// ExecutionHistory returns all execution records.
func (pe *ProposalExecutor) ExecutionHistory() []ExecutionRecord {
	pe.mu.Lock()
	defer pe.mu.Unlock()
	out := make([]ExecutionRecord, len(pe.records))
	copy(out, pe.records)
	return out
}

// ExecuteNow immediately finalizes and executes a specific proposal (admin override).
func (pe *ProposalExecutor) ExecuteNow(proposalID string) error {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	action, ok := pe.actions[proposalID]
	if !ok {
		return fmt.Errorf("no action attached to proposal %s", proposalID)
	}

	if pe.alreadyExecuted(proposalID) {
		return fmt.Errorf("proposal %s already executed", proposalID)
	}

	handler, ok := pe.handlers[action.Type]
	if !ok {
		return fmt.Errorf("no handler for action type %s", action.Type)
	}

	err := handler(proposalID, action.Parameters)
	if err != nil {
		pe.recordExecution(proposalID, action.Type, false, err.Error())
		return err
	}
	pe.recordExecution(proposalID, action.Type, true, "")
	return nil
}
