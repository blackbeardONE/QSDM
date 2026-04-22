package chain

import (
	"fmt"
	"sort"
	"sync"
)

const defaultPolFollowerRetention = uint64(4096)

// PolFollower applies verified inbound POL gossip: prevote-lock proofs and round certificates.
// It uses the node's validator set for quorum checks on prevotes; certificates are stored
// after structural and membership checks (commit digest is not re-derived from commits here).
type PolFollower struct {
	mu           sync.RWMutex
	vs           *ValidatorSet
	quorumFrac   float64
	lockByHeight map[uint64]*PrevoteLockProof
	certByHeight map[uint64]*RoundCertificate
	maxObserved  uint64
	retention    uint64

	// Fork-choice / finality anchoring for heights this node sealed (see RecordLocalSealedBlock).
	anchorFinality      bool
	localStateRoot      map[uint64]string
	localPolPublished   map[uint64]bool
	polConflicts        map[uint64]bool
}

// NewPolFollower creates a follower view backed by vs. quorumFrac should match BFT (e.g. 2/3).
func NewPolFollower(vs *ValidatorSet, quorumFrac float64) *PolFollower {
	if vs == nil {
		return nil
	}
	if quorumFrac <= 0 || quorumFrac > 1 {
		quorumFrac = 2.0 / 3.0
	}
	return &PolFollower{
		vs:                vs,
		quorumFrac:        quorumFrac,
		lockByHeight:      make(map[uint64]*PrevoteLockProof),
		certByHeight:      make(map[uint64]*RoundCertificate),
		retention:         defaultPolFollowerRetention,
		localStateRoot:    make(map[uint64]string),
		localPolPublished: make(map[uint64]bool),
		polConflicts:      make(map[uint64]bool),
	}
}

// SetAnchorFinality when true makes AllowFinalize require POL evidence for heights this node sealed locally.
func (f *PolFollower) SetAnchorFinality(enabled bool) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.anchorFinality = enabled
}

// RecordLocalSealedBlock records the state root this node committed at height before POL gossip is applied.
func (f *PolFollower) RecordLocalSealedBlock(height uint64, stateRoot string) {
	if f == nil || stateRoot == "" {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.localStateRoot[height] = stateRoot
}

// MarkLocalRoundCertificatePublished records that the local POL round certificate was published for height.
func (f *PolFollower) MarkLocalRoundCertificatePublished(height uint64) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.localPolPublished[height] = true
}

// allowFinalizeCoreLocked implements POL gating when anchorFinality is already known true (caller holds rlock).
func (f *PolFollower) allowFinalizeCoreLocked(height uint64, blockStateRoot string) bool {
	if f.polConflicts[height] {
		return false
	}
	localSR, hasLocal := f.localStateRoot[height]
	if !hasLocal || localSR == "" {
		return true
	}
	if blockStateRoot != "" && blockStateRoot != localSR {
		return false
	}
	if cert, ok := f.certByHeight[height]; ok && cert.BlockHash == localSR {
		return true
	}
	return f.localPolPublished[height]
}

// AllowFinalize returns whether a block at height may be marked finalized under POL fork-choice rules.
// When anchorFinality is false, always true. When true, heights without a local seal are not gated;
// for sealed heights, require matching gossip round certificate or successful local POL publish.
func (f *PolFollower) AllowFinalize(height uint64, blockStateRoot string) bool {
	if f == nil {
		return true
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.anchorFinality {
		return true
	}
	return f.allowFinalizeCoreLocked(height, blockStateRoot)
}

// CanExtendFromTip reports whether a new block may be sealed on top of this chain tip (same POL rules as finality).
func (f *PolFollower) CanExtendFromTip(tipHeight uint64, tipStateRoot string) bool {
	if f == nil {
		return true
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	if !f.anchorFinality {
		return true
	}
	return f.allowFinalizeCoreLocked(tipHeight, tipStateRoot)
}

// AnchorFinalityEnabled returns whether POL anchoring is active for finality and production gates.
func (f *PolFollower) AnchorFinalityEnabled() bool {
	if f == nil {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.anchorFinality
}

// HasConflict reports whether a POL fork was detected at height.
func (f *PolFollower) HasConflict(height uint64) bool {
	if f == nil {
		return false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.polConflicts[height]
}

func (f *PolFollower) totalActiveStake() float64 {
	if f.vs == nil {
		return 0
	}
	var t float64
	for _, v := range f.vs.ActiveValidators() {
		if v.Status == ValidatorActive {
			t += v.Stake
		}
	}
	return t
}

func (f *PolFollower) prevoteStakeForLocked(p *PrevoteLockProof) float64 {
	if f.vs == nil || p == nil {
		return 0
	}
	var s float64
	for _, vote := range p.Prevotes {
		if vote.Height != p.Height || vote.Round != p.Round {
			continue
		}
		if vote.Type != VotePreVote && vote.Type != "" {
			continue
		}
		if vote.BlockHash != p.LockedBlockHash {
			continue
		}
		v, ok := f.vs.GetValidator(vote.Validator)
		if !ok || v.Status != ValidatorActive {
			continue
		}
		s += v.Stake
	}
	return s
}

// IngestPrevoteLockProof verifies prevote quorum for LockedBlockHash and stores the latest proof per height.
func (f *PolFollower) IngestPrevoteLockProof(p *PrevoteLockProof) error {
	if f == nil || f.vs == nil || p == nil {
		return fmt.Errorf("pol follower: nil input")
	}
	if p.Height == 0 || p.LockedBlockHash == "" {
		return fmt.Errorf("pol follower: invalid proof fields")
	}
	if len(p.Prevotes) == 0 {
		return fmt.Errorf("pol follower: empty prevotes")
	}
	for _, vote := range p.Prevotes {
		if vote.Height != p.Height || vote.Round != p.Round {
			return fmt.Errorf("pol follower: prevote height/round mismatch")
		}
		if vote.Type != VotePreVote && vote.Type != "" {
			return fmt.Errorf("pol follower: vote is not prevote")
		}
	}
	total := f.totalActiveStake()
	if total <= 0 {
		return fmt.Errorf("pol follower: no active stake")
	}
	stake := f.prevoteStakeForLocked(p)
	if stake < total*f.quorumFrac {
		return fmt.Errorf("pol follower: insufficient prevote stake for lock (got %.6f need %.6f of %.6f)", stake, total*f.quorumFrac, total)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if prev, ok := f.lockByHeight[p.Height]; ok && prev != nil && prev.Round > p.Round {
		return fmt.Errorf("pol follower: stale round %d < stored %d", p.Round, prev.Round)
	}
	cp := *p
	if len(p.Prevotes) > 0 {
		cp.Prevotes = make([]BlockVote, len(p.Prevotes))
		copy(cp.Prevotes, p.Prevotes)
	}
	f.lockByHeight[p.Height] = &cp
	if p.Height > f.maxObserved {
		f.maxObserved = p.Height
	}
	f.pruneLocked()
	return nil
}

// IngestRoundCertificate checks certificate fields and validator membership, then stores by height.
func (f *PolFollower) IngestRoundCertificate(c *RoundCertificate) error {
	if f == nil || f.vs == nil || c == nil {
		return fmt.Errorf("pol follower: nil input")
	}
	if c.Height == 0 || c.CommitDigest == "" {
		return fmt.Errorf("pol follower: invalid certificate fields")
	}
	if c.CommitCount < 1 || len(c.ValidatorSet) == 0 {
		return fmt.Errorf("pol follower: certificate missing validators or commits")
	}
	for _, addr := range c.ValidatorSet {
		v, ok := f.vs.GetValidator(addr)
		if !ok {
			return fmt.Errorf("pol follower: unknown validator %q in certificate", addr)
		}
		if v.Status == ValidatorExited {
			return fmt.Errorf("pol follower: validator %q exited in certificate", addr)
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if lr := f.localStateRoot[c.Height]; lr != "" && c.BlockHash != lr {
		f.polConflicts[c.Height] = true
		return fmt.Errorf("pol follower: round certificate forks local sealed state")
	}
	if ex := f.certByHeight[c.Height]; ex != nil && ex.BlockHash != c.BlockHash {
		f.polConflicts[c.Height] = true
		return fmt.Errorf("pol follower: conflicting round certificates at height %d", c.Height)
	}
	cp := *c
	if len(c.ValidatorSet) > 0 {
		cp.ValidatorSet = append([]string(nil), c.ValidatorSet...)
		sort.Strings(cp.ValidatorSet)
	}
	f.certByHeight[c.Height] = &cp
	if c.Height > f.maxObserved {
		f.maxObserved = c.Height
	}
	f.pruneLocked()
	return nil
}

func (f *PolFollower) pruneLocked() {
	if f.retention == 0 {
		return
	}
	if f.maxObserved <= f.retention {
		return
	}
	cutoff := f.maxObserved - f.retention
	for h := range f.lockByHeight {
		if h < cutoff {
			delete(f.lockByHeight, h)
		}
	}
	for h := range f.certByHeight {
		if h < cutoff {
			delete(f.certByHeight, h)
		}
	}
	for h := range f.localStateRoot {
		if h < cutoff {
			delete(f.localStateRoot, h)
			delete(f.localPolPublished, h)
			delete(f.polConflicts, h)
		}
	}
}

// GetPrevoteLockProof returns the latest stored lock proof for height.
func (f *PolFollower) GetPrevoteLockProof(height uint64) (*PrevoteLockProof, bool) {
	if f == nil {
		return nil, false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	p, ok := f.lockByHeight[height]
	return p, ok
}

// GetRoundCertificate returns the stored round certificate for height.
func (f *PolFollower) GetRoundCertificate(height uint64) (*RoundCertificate, bool) {
	if f == nil {
		return nil, false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	c, ok := f.certByHeight[height]
	return c, ok
}

// Summary returns compact stats for admin / metrics (no full payloads).
func (f *PolFollower) Summary() map[string]interface{} {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	conf := 0
	for _, v := range f.polConflicts {
		if v {
			conf++
		}
	}
	return map[string]interface{}{
		"prevote_lock_heights": len(f.lockByHeight),
		"round_cert_heights":   len(f.certByHeight),
		"max_height_observed":  f.maxObserved,
		"anchor_finality":      f.anchorFinality,
		"pol_conflicts":        conf,
	}
}

// LockHeights returns stored lock proof heights sorted ascending (capped).
func (f *PolFollower) LockHeights(max int) []uint64 {
	if f == nil {
		return nil
	}
	if max <= 0 {
		max = 256
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]uint64, 0, len(f.lockByHeight))
	for h := range f.lockByHeight {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

// CertHeights returns stored certificate heights sorted ascending (capped).
func (f *PolFollower) CertHeights(max int) []uint64 {
	if f == nil {
		return nil
	}
	if max <= 0 {
		max = 256
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]uint64, 0, len(f.certByHeight))
	for h := range f.certByHeight {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}
