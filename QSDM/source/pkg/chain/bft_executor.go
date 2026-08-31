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

	// voteSigner authenticates outbound consensus messages (guarded by mu).
	voteSigner BFTSigner

	// requireSignedVotes rejects unsigned inbound consensus messages.
	requireSignedVotes atomic.Bool
	// signedVoteActivationHeight preserves unsigned historical traffic below
	// a coordinated rollout height. Zero retains immediate enforcement for
	// direct callers and legacy tests.
	signedVoteActivationHeight atomic.Uint64

	// Auth counters let operators observe remaining unsigned compatibility
	// traffic before choosing a coordinated enforcement height.
	authSignedAccepted       atomic.Uint64
	authUnsignedAccepted     atomic.Uint64
	authUnsignedRejected     atomic.Uint64
	authBadSignatureRejected atomic.Uint64
	commitNotified           map[uint64]struct{}
	pending                  *PendingProposalStore

	appendOK       atomic.Uint64
	appendSkip     atomic.Uint64
	appendConflict atomic.Uint64

	evidence atomic.Pointer[EvidenceManager]

	// lastInboundBFTGossipPeer is the libp2p peer ID from the most recent BFT gossip message passed to ApplyInbound (best-effort attribution).
	lastInboundBFTGossipPeer atomic.Value // string

	// pendingPeers records which gossip peer last supplied a propose-with-body for (height, vote_value).
	pendingPeerMu sync.Mutex
	pendingPeers  map[uint64]map[string]string // height -> vote_value (BlockHash field) -> peer ID

	// proposeExhibits stores the first signed proposal seen for each
	// height/round/proposer so a later conflict can carry proof.
	proposeExhibitMu sync.Mutex
	proposeExhibits  map[proposeExhibitKey]SignedVoteExhibit

	diagMu       sync.Mutex
	lastRecorded bool
	lastAt       time.Time
	lastOK       bool
	lastErrMsg   string
}

type proposeExhibitKey struct {
	height   uint64
	round    uint32
	proposer string
}

// NewBFTExecutor wraps a BFT consensus instance for networked execution.
func NewBFTExecutor(bc *BFTConsensus) *BFTExecutor {
	if bc == nil {
		return nil
	}
	return &BFTExecutor{
		bc:              bc,
		commitNotified:  make(map[uint64]struct{}),
		pending:         NewPendingProposalStore(),
		proposeExhibits: make(map[proposeExhibitKey]SignedVoteExhibit),
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

func proposalExhibitKey(height uint64, round uint32, proposer string) proposeExhibitKey {
	return proposeExhibitKey{height: height, round: round, proposer: proposer}
}

func signedProposeExhibitFromMessage(m BFTWireProposeMsg) (SignedVoteExhibit, bool) {
	if !m.Auth.Signed() {
		return SignedVoteExhibit{}, false
	}
	return SignedVoteExhibit{
		Kind:      BFTWirePropose,
		Height:    m.Height,
		Round:     m.Round,
		Validator: m.Proposer,
		BlockHash: m.BlockHash,
		BodyHash:  proposeBodyHash(m.Block),
		Auth:      m.Auth,
	}, true
}

func (e *BFTExecutor) recordProposeExhibit(m BFTWireProposeMsg) {
	if e == nil {
		return
	}
	exhibit, ok := signedProposeExhibitFromMessage(m)
	if !ok {
		return
	}
	key := proposalExhibitKey(m.Height, m.Round, m.Proposer)
	e.proposeExhibitMu.Lock()
	defer e.proposeExhibitMu.Unlock()
	if e.proposeExhibits == nil {
		e.proposeExhibits = make(map[proposeExhibitKey]SignedVoteExhibit)
	}
	if _, exists := e.proposeExhibits[key]; !exists {
		e.proposeExhibits[key] = exhibit
	}
}

func (e *BFTExecutor) lookupProposeExhibit(height uint64, round uint32, proposer string) (SignedVoteExhibit, bool) {
	if e == nil {
		return SignedVoteExhibit{}, false
	}
	e.proposeExhibitMu.Lock()
	defer e.proposeExhibitMu.Unlock()
	exhibit, ok := e.proposeExhibits[proposalExhibitKey(height, round, proposer)]
	return exhibit, ok
}

func (e *BFTExecutor) pruneProposeExhibitsAtHeight(height uint64) {
	if e == nil {
		return
	}
	e.proposeExhibitMu.Lock()
	defer e.proposeExhibitMu.Unlock()
	for key := range e.proposeExhibits {
		if key.height == height {
			delete(e.proposeExhibits, key)
		}
	}
}

func (e *BFTExecutor) pruneProposeExhibitsBelow(keepMinHeight uint64) {
	if e == nil || keepMinHeight == 0 {
		return
	}
	e.proposeExhibitMu.Lock()
	defer e.proposeExhibitMu.Unlock()
	for key := range e.proposeExhibits {
		if key.height < keepMinHeight {
			delete(e.proposeExhibits, key)
		}
	}
}

// SetEvidenceManager optionally submits proposer equivocation from gossip to automatic slashing.
func (e *BFTExecutor) SetEvidenceManager(em *EvidenceManager) {
	if e == nil {
		return
	}
	e.evidence.Store(em)
}

// maybeRecordProposerEquivocation converts a detected propose conflict into
// slashable evidence only when both conflicting proposals are available as
// verified signed exhibits.
func (e *BFTExecutor) maybeRecordProposerEquivocation(err error, msg BFTWireProposeMsg) {
	if e == nil || err == nil {
		return
	}
	var pe *ProposerEquivocationError
	if !errors.As(err, &pe) {
		return
	}
	current, ok := signedProposeExhibitFromMessage(msg)
	if !ok {
		return
	}
	previous, ok := e.lookupProposeExhibit(pe.Height, pe.Round, pe.Proposer)
	if !ok || previous.BlockHash != pe.ExistingHash || current.BlockHash != pe.NewHash {
		return
	}
	em := e.evidence.Load()
	if em == nil {
		return
	}
	ev, buildErr := BuildEquivocationEvidence(pe.Proposer, previous, current)
	if buildErr != nil {
		return
	}
	ev.Details = "conflicting signed BFT propose at same height/round"
	ev.Timestamp = time.Now()
	em.SubmitEvidenceBestEffort(ev)
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
	msg := BFTWireProposeMsg{
		Height: height, Round: round, Proposer: proposer, BlockHash: blockHash, Block: body,
	}
	if signer := e.VoteSigner(); signer != nil {
		if err := SignPropose(&msg, signer); err != nil {
			return err
		}
	}
	e.recordProposeExhibit(msg)
	b, err := MarshalBFTWire(BFTWirePropose, msg)
	if err != nil {
		return err
	}
	return e.emit(b)
}

// SetVoteSigner installs the key used to authenticate outbound consensus
// messages. When nil, messages are emitted unsigned — which peers running
// with RequireSignedVotes will reject.
func (e *BFTExecutor) SetVoteSigner(s BFTSigner) {
	if e == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.voteSigner = s
}

// VoteSigner returns the configured outbound signer, if any.
func (e *BFTExecutor) VoteSigner() BFTSigner {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.voteSigner
}

// SetRequireSignedVotes controls whether inbound consensus messages must
// carry a valid authenticator.
//
// Defaults to false so a mixed-version network can roll forward: nodes
// running the signed build emit signatures immediately, and operators flip
// enforcement on once every validator is upgraded. Leaving it off preserves
// the old, forgeable behaviour, so it should be enabled as soon as the
// rollout completes.
func (e *BFTExecutor) SetRequireSignedVotes(require bool) {
	if e == nil {
		return
	}
	e.requireSignedVotes.Store(require)
}

// RequireSignedVotes reports whether inbound signature enforcement is on.
func (e *BFTExecutor) RequireSignedVotes() bool {
	if e == nil {
		return false
	}
	return e.requireSignedVotes.Load()
}

// SetSignedVoteActivationHeight sets the first height where unsigned votes
// are rejected when RequireSignedVotes is enabled. Every validator must use
// the same non-zero height in production.
func (e *BFTExecutor) SetSignedVoteActivationHeight(height uint64) {
	if e == nil {
		return
	}
	e.signedVoteActivationHeight.Store(height)
}

// SignedVoteActivationHeight reports the configured activation height.
func (e *BFTExecutor) SignedVoteActivationHeight() uint64 {
	if e == nil {
		return 0
	}
	return e.signedVoteActivationHeight.Load()
}

// BFTAuthStats reports cumulative inbound authentication outcomes.
type BFTAuthStats struct {
	SignedAccepted       uint64
	UnsignedAccepted     uint64
	UnsignedRejected     uint64
	BadSignatureRejected uint64
}

// AuthStats returns a snapshot of inbound BFT authentication outcomes.
func (e *BFTExecutor) AuthStats() BFTAuthStats {
	if e == nil {
		return BFTAuthStats{}
	}
	return BFTAuthStats{
		SignedAccepted:       e.authSignedAccepted.Load(),
		UnsignedAccepted:     e.authUnsignedAccepted.Load(),
		UnsignedRejected:     e.authUnsignedRejected.Load(),
		BadSignatureRejected: e.authBadSignatureRejected.Load(),
	}
}

func (e *BFTExecutor) signedVotesRequiredAt(height uint64) bool {
	if !e.RequireSignedVotes() {
		return false
	}
	activation := e.SignedVoteActivationHeight()
	return activation == 0 || height >= activation
}

// checkInboundAuth applies the configured signature policy. When a message
// carries an authenticator it is ALWAYS verified — a present-but-invalid
// signature is a hard error regardless of policy, because the only reason to
// send one is to be checked. The policy flag governs unsigned messages.
func (e *BFTExecutor) checkInboundAuth(height uint64, signed bool, verify func() error) error {
	if !signed {
		if e.signedVotesRequiredAt(height) {
			e.authUnsignedRejected.Add(1)
			return ErrBFTUnsigned
		}
		e.authUnsignedAccepted.Add(1)
		return nil
	}
	if err := verify(); err != nil {
		e.authBadSignatureRejected.Add(1)
		return err
	}
	e.authSignedAccepted.Add(1)
	return nil
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
	e.pruneProposeExhibitsAtHeight(height)
}

// PrunePendingBelow clears gossip caches for heights strictly below keepMinHeight (bounded retention).
func (e *BFTExecutor) PrunePendingBelow(keepMinHeight uint64) {
	if e == nil || e.pending == nil {
		return
	}
	e.pending.PruneHeightsBelow(keepMinHeight)
	e.prunePendingPeersBelow(keepMinHeight)
	e.pruneProposeExhibitsBelow(keepMinHeight)
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
	msg := BFTWirePrevoteMsg{
		Height: height, Round: round, Validator: validator, BlockHash: blockHash,
	}
	if signer := e.VoteSigner(); signer != nil {
		if err := SignPrevote(&msg, signer); err != nil {
			return err
		}
	}
	b, err := MarshalBFTWire(BFTWirePrevote, msg)
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
	msg := BFTWirePrecommitMsg{
		Height: height, Round: round, Validator: validator, BlockHash: blockHash,
	}
	if signer := e.VoteSigner(); signer != nil {
		if err := SignPrecommit(&msg, signer); err != nil {
			return err
		}
	}
	b, err := MarshalBFTWire(BFTWirePrecommit, msg)
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
		if err := e.checkInboundAuth(m.Height, m.Auth.Signed(), func() error { return VerifyPropose(m) }); err != nil {
			return err
		}
		if _, err := e.bc.Propose(m.Height, m.Round, m.Proposer, m.BlockHash); err != nil {
			e.maybeRecordProposerEquivocation(err, m)
			if isBenignBFTErr(err) {
				return nil
			}
			return err
		}
		e.recordProposeExhibit(m)
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
		if err := e.checkInboundAuth(m.Height, m.Auth.Signed(), func() error { return VerifyPrevote(m) }); err != nil {
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
		if err := e.checkInboundAuth(m.Height, m.Auth.Signed(), func() error { return VerifyPrecommit(m) }); err != nil {
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

// ValidateInboundAuthentication checks a BFT gossip payload without mutating
// consensus. Catch-up replicas use this path so they can reject and avoid
// relaying forged traffic even though they intentionally do not apply votes
// to their local singleton validator set.
func (e *BFTExecutor) ValidateInboundAuthentication(payload []byte) error {
	if e == nil {
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
		return e.checkInboundAuth(m.Height, m.Auth.Signed(), func() error { return VerifyPropose(m) })
	case BFTWirePrevote:
		var m BFTWirePrevoteMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		return e.checkInboundAuth(m.Height, m.Auth.Signed(), func() error { return VerifyPrevote(m) })
	case BFTWirePrecommit:
		var m BFTWirePrecommitMsg
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		return e.checkInboundAuth(m.Height, m.Auth.Signed(), func() error { return VerifyPrecommit(m) })
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
	// A retransmitted propose for a round this node has already retired is
	// expected on every round timeout, not an application error. Matched by
	// sentinel rather than substring: the list below is exactly the mechanism
	// that failed to notice this error when it was introduced.
	if errors.Is(err, ErrBFTRoundRetired) {
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
