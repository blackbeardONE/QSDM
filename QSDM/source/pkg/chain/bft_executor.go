package chain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// BFTExecutor runs BFTConsensus with optional libp2p gossip (publish + inbound apply).
type BFTExecutor struct {
	bc *BFTConsensus

	mu       sync.Mutex
	publish  func([]byte) error
	onCommit func(height uint64, round uint32, blockHash string)

	commitNotified map[uint64]struct{}
	pending        *PendingProposalStore

	appendOK       atomic.Uint64
	appendSkip     atomic.Uint64
	appendConflict atomic.Uint64

	evidence atomic.Pointer[EvidenceManager]

	// lastInboundBFTGossipPeer is the libp2p peer ID from the most recent BFT gossip message passed to ApplyInbound (best-effort attribution).
	lastInboundBFTGossipPeer atomic.Value // string

	// pendingPeers records which gossip peer last supplied a propose-with-body for (height, vote_value).
	pendingPeerMu sync.Mutex
	pendingPeers  map[uint64]map[string]string // height -> vote_value (BlockHash field) -> peer ID

	diagMu       sync.Mutex
	lastRecorded bool
	lastAt       time.Time
	lastOK       bool
	lastErrMsg   string
}

// NewBFTExecutor wraps a BFT consensus instance for networked execution.
func NewBFTExecutor(bc *BFTConsensus) *BFTExecutor {
	if bc == nil {
		return nil
	}
	return &BFTExecutor{
		bc:             bc,
		commitNotified: make(map[uint64]struct{}),
		pending:        NewPendingProposalStore(),
	}
}

// Consensus returns the underlying engine (for POL publish, tests, etc.).
func (e *BFTExecutor) Consensus() *BFTConsensus {
	if e == nil {
		return nil
	}
	return e.bc
}

// SetLastInboundBFTGossipPeer records which peer delivered the current inbound BFT payload (called by networking before ApplyInbound).
func (e *BFTExecutor) SetLastInboundBFTGossipPeer(peerID string) {
	if e == nil {
		return
	}
	e.lastInboundBFTGossipPeer.Store(peerID)
}

// LastInboundBFTGossipPeer returns the last peer set by SetLastInboundBFTGossipPeer (empty if none).
func (e *BFTExecutor) LastInboundBFTGossipPeer() string {
	if e == nil {
		return ""
	}
	v, _ := e.lastInboundBFTGossipPeer.Load().(string)
	return v
}

// ClearLastInboundBFTGossipPeer clears attribution after a commit callback or tests.
func (e *BFTExecutor) ClearLastInboundBFTGossipPeer() {
	if e == nil {
		return
	}
	e.lastInboundBFTGossipPeer.Store("")
}

func (e *BFTExecutor) recordPendingProposeSource(height uint64, voteValue, peerID string) {
	if e == nil || peerID == "" || voteValue == "" {
		return
	}
	e.pendingPeerMu.Lock()
	defer e.pendingPeerMu.Unlock()
	if e.pendingPeers == nil {
		e.pendingPeers = make(map[uint64]map[string]string)
	}
	inner := e.pendingPeers[height]
	if inner == nil {
		inner = make(map[string]string)
		e.pendingPeers[height] = inner
	}
	inner[voteValue] = peerID
}

// PendingProposeSource returns the libp2p peer that last gossiped a full block body for this height and vote value (BFT propose BlockHash / committed vote value).
func (e *BFTExecutor) PendingProposeSource(height uint64, voteValue string) (peerID string, ok bool) {
	if e == nil || voteValue == "" {
		return "", false
	}
	e.pendingPeerMu.Lock()
	defer e.pendingPeerMu.Unlock()
	inner, ho := e.pendingPeers[height]
	if !ho {
		return "", false
	}
	p, po := inner[voteValue]
	return p, po && p != ""
}

func (e *BFTExecutor) prunePendingPeersAtHeight(height uint64) {
	if e == nil {
		return
	}
	e.pendingPeerMu.Lock()
	defer e.pendingPeerMu.Unlock()
	delete(e.pendingPeers, height)
}

func (e *BFTExecutor) prunePendingPeersBelow(keepMinHeight uint64) {
	if e == nil || keepMinHeight == 0 {
		return
	}
	e.pendingPeerMu.Lock()
	defer e.pendingPeerMu.Unlock()
	for h := range e.pendingPeers {
		if h < keepMinHeight {
			delete(e.pendingPeers, h)
		}
	}
}

// ClearPendingProposeSource removes stored relay attribution for one (height, vote_value) entry.
func (e *BFTExecutor) ClearPendingProposeSource(height uint64, voteValue string) {
	if e == nil || voteValue == "" {
		return
	}
	e.pendingPeerMu.Lock()
	defer e.pendingPeerMu.Unlock()
	inner, ok := e.pendingPeers[height]
	if !ok {
		return
	}
	delete(inner, voteValue)
	if len(inner) == 0 {
		delete(e.pendingPeers, height)
	}
}

// SetEvidenceManager optionally submits proposer equivocation from gossip to automatic slashing.
func (e *BFTExecutor) SetEvidenceManager(em *EvidenceManager) {
	if e == nil {
		return
	}
	e.evidence.Store(em)
}

func (e *BFTExecutor) maybeRecordProposerEquivocation(err error) {
	if e == nil || err == nil {
		return
	}
	var pe *ProposerEquivocationError
	if !errors.As(err, &pe) {
		return
	}
	em := e.evidence.Load()
	if em == nil {
		return
	}
	em.SubmitEvidenceBestEffort(ConsensusEvidence{
		Type:        EvidenceEquivocation,
		Validator:   pe.Proposer,
		Height:      pe.Height,
		Round:       pe.Round,
		BlockHashes: []string{pe.ExistingHash, pe.NewHash},
		Details:     "conflicting BFT propose at same height/round",
		Timestamp:   time.Now(),
	})
}

// SetPublisher sets the gossip publish function (may be nil for local-only).
func (e *BFTExecutor) SetPublisher(fn func([]byte) error) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.publish = fn
}

// SetOnCommitted registers a callback invoked once per committed height (best-effort).
func (e *BFTExecutor) SetOnCommitted(fn func(height uint64, round uint32, blockHash string)) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onCommit = fn
}

func (e *BFTExecutor) emit(b []byte) error {
	e.mu.Lock()
	fn := e.publish
	e.mu.Unlock()
	if fn == nil || len(b) == 0 {
		return nil
	}
	return fn(b)
}

// BroadcastPropose publishes a propose message (does not mutate consensus).
// When body is non-nil it is included on the wire so peers can cache the block under (height, blockHash).
func (e *BFTExecutor) BroadcastPropose(height uint64, round uint32, proposer, blockHash string, body *Block) error {
	if e == nil {
		return nil
	}
	b, err := MarshalBFTWire(BFTWirePropose, BFTWireProposeMsg{
		Height: height, Round: round, Proposer: proposer, BlockHash: blockHash, Block: body,
	})
	if err != nil {
		return err
	}
	return e.emit(b)
}

// PendingBlock returns a gossip-cached block body for this height and vote value (e.g. StateRoot), if known.
func (e *BFTExecutor) PendingBlock(height uint64, voteValue string) (*Block, bool) {
	if e == nil || e.pending == nil {
		return nil, false
	}
	return e.pending.Get(height, voteValue)
}

// PrunePendingHeight removes cached proposals at one height (e.g. after local seal or follower append).
func (e *BFTExecutor) PrunePendingHeight(height uint64) {
	if e == nil || e.pending == nil {
		return
	}
	e.pending.RemoveHeight(height)
	e.prunePendingPeersAtHeight(height)
}

// PrunePendingBelow clears gossip caches for heights strictly below keepMinHeight (bounded retention).
func (e *BFTExecutor) PrunePendingBelow(keepMinHeight uint64) {
	if e == nil || e.pending == nil {
		return
	}
	e.pending.PruneHeightsBelow(keepMinHeight)
	e.prunePendingPeersBelow(keepMinHeight)
}

// NoteFollowerAppend records success (err == nil) or failure of TryAppendExternalBlock for metrics.
func (e *BFTExecutor) NoteFollowerAppend(err error) {
	if e == nil {
		return
	}
	now := time.Now()
	e.diagMu.Lock()
	e.lastRecorded = true
	e.lastAt = now
	if err == nil {
		e.lastOK = true
		e.lastErrMsg = ""
	} else {
		e.lastOK = false
		e.lastErrMsg = err.Error()
	}
	e.diagMu.Unlock()
	if err == nil {
		e.appendOK.Add(1)
	} else if errors.Is(err, ErrExternalAppendConflict) {
		e.appendConflict.Add(1)
	} else {
		e.appendSkip.Add(1)
	}
}

// FollowerAppendStats returns cumulative TryAppendExternalBlock outcomes since process start
// (ok = success, skip = other failures, conflict = ErrExternalAppendConflict).
func (e *BFTExecutor) FollowerAppendStats() (ok, skip, conflict uint64) {
	if e == nil {
		return 0, 0, 0
	}
	return e.appendOK.Load(), e.appendSkip.Load(), e.appendConflict.Load()
}

// FollowerAppendDiagnostic returns the last NoteFollowerAppend outcome (empty map if none yet).
func (e *BFTExecutor) FollowerAppendDiagnostic() map[string]interface{} {
	if e == nil {
		return nil
	}
	e.diagMu.Lock()
	defer e.diagMu.Unlock()
	if !e.lastRecorded {
		return map[string]interface{}{}
	}
	return map[string]interface{}{
		"last_at":    e.lastAt.UTC().Format(time.RFC3339Nano),
		"last_ok":    e.lastOK,
		"last_error": e.lastErrMsg,
	}
}

// BroadcastPrevote publishes a prevote message.
func (e *BFTExecutor) BroadcastPrevote(height uint64, round uint32, validator, blockHash string) error {
	if e == nil {
		return nil
	}
	b, err := MarshalBFTWire(BFTWirePrevote, BFTWirePrevoteMsg{
		Height: height, Round: round, Validator: validator, BlockHash: blockHash,
	})
	if err != nil {
		return err
	}
	return e.emit(b)
}

// BroadcastPrecommit publishes a precommit message.
func (e *BFTExecutor) BroadcastPrecommit(height uint64, round uint32, validator, blockHash string) error {
	if e == nil {
		return nil
	}
	b, err := MarshalBFTWire(BFTWirePrecommit, BFTWirePrecommitMsg{
		Height: height, Round: round, Validator: validator, BlockHash: blockHash,
	})
	if err != nil {
		return err
	}
	return e.emit(b)
}

func (e *BFTExecutor) maybeNotifyCommit(height uint64, round uint32, blockHash string) {
	e.mu.Lock()
	fn := e.onCommit
	if _, dup := e.commitNotified[height]; dup {
		e.mu.Unlock()
		return
	}
	e.commitNotified[height] = struct{}{}
	e.mu.Unlock()
	if fn != nil {
		fn(height, round, blockHash)
	}
}

// ApplyInbound decodes a gossip payload and applies it to consensus (idempotent for benign duplicates).
func (e *BFTExecutor) ApplyInbound(payload []byte) error {
	if e == nil || e.bc == nil {
		return nil
	}
	kind, raw, err := UnmarshalBFTWire(payload)
	if err != nil {
		return err
	}
	switch kind {
	case BFTWirePropose:
		var m BFTWireProposeMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if err := validateInboundProposeBlock(&m); err != nil {
			return err
		}
		if _, err := e.bc.Propose(m.Height, m.Round, m.Proposer, m.BlockHash); err != nil {
			e.maybeRecordProposerEquivocation(err)
			if isBenignBFTErr(err) {
				return nil
			}
			return err
		}
		if m.Block != nil && e.pending != nil {
			e.pending.Put(m.Height, m.BlockHash, m.Block)
			if p := e.LastInboundBFTGossipPeer(); p != "" {
				e.recordPendingProposeSource(m.Height, m.BlockHash, p)
			}
		}
		return nil
	case BFTWirePrevote:
		var m BFTWirePrevoteMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if err := e.bc.PreVote(m.Height, m.Validator, m.BlockHash); err != nil {
			if isBenignBFTErr(err) {
				return nil
			}
			return err
		}
		return nil
	case BFTWirePrecommit:
		var m BFTWirePrecommitMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		if err := e.bc.PreCommit(m.Height, m.Validator, m.BlockHash); err != nil {
			if isBenignBFTErr(err) {
				return nil
			}
			return err
		}
		e.checkCommitted(m.Height)
		return nil
	default:
		return fmt.Errorf("bft wire: unknown kind %q", kind)
	}
}

func (e *BFTExecutor) checkCommitted(height uint64) {
	if e.bc == nil || !e.bc.IsCommitted(height) {
		return
	}
	cr, ok := e.bc.GetCommitted(height)
	if !ok || cr == nil {
		return
	}
	e.maybeNotifyCommit(cr.Height, cr.Round, cr.BlockHash)
}

// NotifyFromConsensus runs the commit callback if consensus already committed this height (local drive).
func (e *BFTExecutor) NotifyFromConsensus(height uint64) {
	if e == nil {
		return
	}
	e.checkCommitted(height)
}

func validateInboundProposeBlock(m *BFTWireProposeMsg) error {
	if m == nil || m.Block == nil {
		return nil
	}
	if m.Block.Height != m.Height {
		return fmt.Errorf("bft propose: block height %d != envelope height %d", m.Block.Height, m.Height)
	}
	if m.Block.StateRoot != m.BlockHash {
		return fmt.Errorf("bft propose: block state_root must match block_hash vote field")
	}
	if want := computeBlockHash(m.Block); m.Block.Hash != want {
		return fmt.Errorf("bft propose: block hash does not match canonical hash")
	}
	return nil
}

func isBenignBFTErr(err error) bool {
	if err == nil {
		return true
	}
	s := err.Error()
	return strings.Contains(s, "already committed") ||
		strings.Contains(s, "already pre-voted") ||
		strings.Contains(s, "already pre-committed") ||
		strings.Contains(s, "no active round") ||
		strings.Contains(s, "cannot prevote") ||
		strings.Contains(s, "needs prevote quorum") ||
		strings.Contains(s, "does not match locked value") ||
		strings.Contains(s, "still active at height") ||
		strings.Contains(s, "is behind active round") ||
		strings.Contains(s, "proposer mismatch") ||
		strings.Contains(s, "not an active validator") ||
		strings.Contains(s, "is not active")
}
