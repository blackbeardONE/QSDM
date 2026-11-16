package pulse

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Params are the consensus parameters of the rule. The zero value is off:
// ActivationHeight 0 means never, and every call refuses with ErrInactive.
type Params struct {
	ActivationHeight uint64 // first height whose reward may be settled by a batch
	MaxFeeBPS        uint16 // network-wide ceiling on any pool's fee
}

// PoolEntry is one authorized pool in the consensus manifest.
type PoolEntry struct {
	ID        Address // hex(sha256(public key))
	PublicKey string  // hex ML-DSA-87 public key
	MaxFeeBPS uint16  // this pool's ceiling (at most Params.MaxFeeBPS)
}

// Errors. A refused batch never changes state; the block's reward is then
// paid exactly as without PULSE (each proof to its own owner).
var (
	ErrInactive     = errors.New("pulse: the rule is not active at this height")
	ErrUnknownPool  = errors.New("pulse: pool not in the manifest")
	ErrBadSignature = errors.New("pulse: batch signature does not verify")
	ErrReplay       = errors.New("pulse: batch sequence or proof already used")
	ErrMismatch     = errors.New("pulse: batch does not match the block")
)

// Claim is one accepted proof the producer pays in this block.
type Claim struct {
	ProofID string
	Owner   Address
	Amount  uint64 // dust
}

// State is the chain state the rule reads and writes. Implementations must
// make Commit atomic with the block.
type State interface {
	LastSeq(pool Address) uint64
	ProofPooled(proofID string) bool
	Owed(pool Address) map[Address]uint64
	Commit(pool Address, seq uint64, proofs []string, owed map[Address]uint64) error
}

// Metrics are counters for the node's metrics endpoint.
type Metrics struct {
	Applied, Refused, PaidDust, DustToFee atomic.Uint64
}

// Rule verifies and applies settlement batches.
type Rule struct {
	Params   Params
	Manifest []PoolEntry
	Metrics  *Metrics
}

// Active reports whether height is settled by the rule.
func (r *Rule) Active(height uint64) bool {
	return r.Params.ActivationHeight > 0 && height >= r.Params.ActivationHeight
}

func (r *Rule) entry(id Address) (*PoolEntry, error) {
	for i := range r.Manifest {
		if r.Manifest[i].ID == id {
			return &r.Manifest[i], nil
		}
	}
	return nil, ErrUnknownPool
}

// Verify decodes a signed batch and checks it against the manifest, the
// height, the fee ceilings and the replay state. It changes nothing.
func (r *Rule) Verify(body, sig []byte, height uint64, st State) (*Batch, error) {
	if !r.Active(height) {
		return nil, ErrInactive
	}
	b, err := DecodeBatch(body)
	if err != nil {
		return nil, err
	}
	e, err := r.entry(b.PoolID)
	if err != nil {
		return nil, err
	}
	pkb, err := hex.DecodeString(e.PublicKey)
	if err != nil {
		return nil, ErrUnknownPool
	}
	pk, err := ParsePublicKey(pkb)
	if err != nil || AddressOf(pk) != b.PoolID {
		return nil, ErrUnknownPool
	}
	if !Verify(pk, BatchDomain, body, sig) {
		return nil, ErrBadSignature
	}
	if b.Height != height {
		return nil, fmt.Errorf("%w: batch height %d, block %d", ErrMismatch, b.Height, height)
	}
	maxFee := r.Params.MaxFeeBPS
	if e.MaxFeeBPS < maxFee {
		maxFee = e.MaxFeeBPS
	}
	if b.FeeBPS > maxFee {
		return nil, fmt.Errorf("%w: fee %d bps above the ceiling %d", ErrMismatch, b.FeeBPS, maxFee)
	}
	if b.Seq <= st.LastSeq(b.PoolID) {
		return nil, ErrReplay
	}
	for _, id := range b.ProofIDs {
		if st.ProofPooled(id) {
			return nil, ErrReplay
		}
	}
	return b, nil
}

// Apply settles a verified batch against the block's claims. Fail-closed:
//   - every listed proof must be a claim of this block whose owner has
//     weight in the batch (a member's own proof), else the batch is refused;
//   - weights of owners that are not enrolled are dropped; none left = refused;
//   - each member's credit counts against its own cap (capLeft);
//   - the claims NOT listed are returned untouched, to be paid as without PULSE.
//
// It returns the payouts for the pooled part, the unlisted claims, and commits
// the sequence, the pooled proof IDs and the carried balances to st.
func (r *Rule) Apply(b *Batch, claims []Claim, enrolled func(Address) bool, capLeft func(Address) uint64, st State) ([]Payout, []Claim, error) {
	refuse := func(err error) ([]Payout, []Claim, error) {
		if r.Metrics != nil {
			r.Metrics.Refused.Add(1)
		}
		return nil, claims, err
	}
	members := map[Address]bool{}
	var weights []Weight
	for _, w := range b.Weights {
		members[w.Owner] = true
		if enrolled(w.Owner) {
			weights = append(weights, w)
		}
	}
	if len(weights) == 0 {
		return refuse(fmt.Errorf("%w: no enrolled member in the window", ErrMismatch))
	}
	listed := map[string]bool{}
	for _, id := range b.ProofIDs {
		listed[id] = true
	}
	var pooled uint64
	var rest []Claim
	found := 0
	for _, c := range claims {
		if !listed[c.ProofID] {
			rest = append(rest, c)
			continue
		}
		if !members[c.Owner] {
			return refuse(fmt.Errorf("%w: proof %s is not a member's", ErrMismatch, c.ProofID))
		}
		if pooled+c.Amount < pooled {
			return refuse(errors.New("pulse: pooled amount overflows"))
		}
		pooled += c.Amount
		found++
	}
	if found != len(b.ProofIDs) {
		return refuse(fmt.Errorf("%w: a listed proof is not paid in this block", ErrMismatch))
	}
	in := SplitInput{Pooled: pooled, FeeBPS: b.FeeBPS, FeeTo: b.FeeTo, FloorDust: b.FloorDust,
		Weights: weights, Owed: st.Owed(b.PoolID), CapLeft: capLeft}
	res, err := Split(in)
	if err != nil {
		return refuse(err)
	}
	if !Conserved(in, res) {
		return refuse(errors.New("pulse: split does not conserve value"))
	}
	ids := append([]string(nil), b.ProofIDs...)
	sort.Strings(ids)
	if err := st.Commit(b.PoolID, b.Seq, ids, res.Owed); err != nil {
		return refuse(err)
	}
	if r.Metrics != nil {
		r.Metrics.Applied.Add(1)
		for _, p := range res.Payouts {
			r.Metrics.PaidDust.Add(p.Amount)
		}
		r.Metrics.DustToFee.Add(res.Dust)
	}
	return res.Payouts, rest, nil
}

// MemState is an in-memory State (tests, and the reference for a durable one).
type MemState struct {
	mu     sync.Mutex
	seq    map[Address]uint64
	pooled map[string]bool
	owed   map[Address]map[Address]uint64
}

// NewMemState returns an empty state.
func NewMemState() *MemState {
	return &MemState{seq: map[Address]uint64{}, pooled: map[string]bool{}, owed: map[Address]map[Address]uint64{}}
}

func (m *MemState) LastSeq(p Address) uint64 { m.mu.Lock(); defer m.mu.Unlock(); return m.seq[p] }

func (m *MemState) ProofPooled(id string) bool { m.mu.Lock(); defer m.mu.Unlock(); return m.pooled[id] }

func (m *MemState) Owed(p Address) map[Address]uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[Address]uint64{}
	for k, v := range m.owed[p] {
		out[k] = v
	}
	return out
}

func (m *MemState) Commit(p Address, seq uint64, proofs []string, owed map[Address]uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if seq <= m.seq[p] {
		return ErrReplay
	}
	for _, id := range proofs {
		if m.pooled[id] {
			return ErrReplay
		}
	}
	m.seq[p] = seq
	for _, id := range proofs {
		m.pooled[id] = true
	}
	cp := map[Address]uint64{}
	for k, v := range owed {
		cp[k] = v
	}
	m.owed[p] = cp
	return nil
}
