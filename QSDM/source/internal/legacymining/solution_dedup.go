package legacymining

// solution_dedup.go (rc1112): one proof-of-work solution is accepted once,
// whoever submits it.
//
// The work hash H(header_hash || nonce || batch_root || mix_digest) does not
// cover miner_addr (MINING_PROTOCOL.md §4.2: header_hash is the block hash at
// the work height, the same for every miner), while the proof ID does
// (mining.Proof.ID). So the solution, not the proof ID, names the work.
//
// SolutionDedupStore refuses a proof whose solution (work height, header_hash,
// nonce) is already stored under another proof ID, with KindDuplicate (400; it
// counts as the submitting owner's duplicate, like a replay). The first stored
// proof keeps the solution. A different nonce is different work, and a batch
// root changes the work hash, so (height, header_hash, nonce) names the work.
//
// This is producer admission only. Followers never verify proofs (reward
// transactions carry only proof IDs; doc.go, WP-E), so it is no consensus rule,
// needs no activation height and applies at once.
//
// Memory: a proof is accepted only while its height is within
// mining.GraceWindow blocks of the accept tip (Verifier, the stale-height
// check), so only the solutions of the newest SolutionKeepHeights work heights
// are kept. The index is in memory and seeded from the unpaid rows on the first
// Accept. A paid row of the last few heights is missing only after a
// restart, and a restart with its start-up quiet period takes far longer than
// GraceWindow blocks, after which those solutions are stale anyway.

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// SolutionKeepHeights is how many work heights below the newest accept tip the
// solution index keeps: the grace window plus a margin.
const SolutionKeepHeights = mining.GraceWindow + 2

type solutionKey struct {
	height uint64
	header [32]byte
	nonce  [16]byte
}

// solutionDuplicatesRefused counts proofs refused because their solution was
// already stored under another proof ID.
var solutionDuplicatesRefused atomic.Uint64

// SolutionDuplicatesRefused reports that counter.
func SolutionDuplicatesRefused() uint64 { return solutionDuplicatesRefused.Load() }

// SolutionDedupStore wraps a Store: Accept refuses a second proof of one
// solution (see the file comment); every other method is the wrapped store's.
type SolutionDedupStore struct {
	Store
	mu      sync.Mutex
	seen    map[solutionKey]ProofID
	maxTip  uint64
	seeded  bool
	seedErr error
}

// NewSolutionDedupStore wraps inner. The index is seeded from inner's unpaid
// rows on the first Accept, when the store is open.
func NewSolutionDedupStore(inner Store) *SolutionDedupStore {
	return &SolutionDedupStore{Store: inner, seen: make(map[solutionKey]ProofID)}
}

// seedLocked indexes the unpaid rows once. A failure leaves the index unseeded
// and is kept in seedErr: only the restart edge case in the file comment relies
// on the seed, and the wrapped store's own Accept reports a broken DB anyway.
// Call with mu held.
func (s *SolutionDedupStore) seedLocked() {
	s.seeded = true
	rows, err := s.Store.Pending()
	if err != nil {
		s.seedErr = fmt.Errorf("legacymining: seed the solution index: %w", err)
		return
	}
	for _, r := range rows {
		k, ok := solutionKeyOf(r.ProofJSON)
		if !ok {
			continue
		}
		if _, dup := s.seen[k]; !dup { // keep the first (Pending is ordered by accept time)
			s.seen[k] = r.ProofID
		}
		if r.AcceptTip > s.maxTip {
			s.maxTip = r.AcceptTip
		}
	}
	s.evictLocked()
}

// SeedErr reports why seeding failed, or nil.
func (s *SolutionDedupStore) SeedErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seedErr
}

// solutionKeyOf reads a stored proof's solution. A proof that does not parse
// has no key (the verifier accepts only canonical proofs, so Accept never sees
// one).
func solutionKeyOf(raw []byte) (solutionKey, bool) {
	p, err := mining.ParseProof(raw)
	if err != nil || p == nil {
		return solutionKey{}, false
	}
	return solutionKey{height: p.Height, header: p.HeaderHash, nonce: p.Nonce}, true
}

// Accept implements Store.Accept with the one-solution rule in front of the
// wrapped store's UNIQUE checks. Check, insert and index update are one step
// under the index lock, so two concurrent submissions of one solution cannot
// both pass.
func (s *SolutionDedupStore) Accept(rec Record) error {
	k, ok := solutionKeyOf(rec.ProofJSON)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.seeded {
		s.seedLocked()
	}
	if ok {
		if prev, dup := s.seen[k]; dup && prev != rec.ProofID {
			solutionDuplicatesRefused.Add(1)
			return &Rejection{Kind: KindDuplicate, Detail: fmt.Sprintf(
				"this solution (height %d, nonce %x) is already stored as proof %x", k.height, k.nonce[:4], prev[:8])}
		}
	}
	if err := s.Store.Accept(rec); err != nil {
		return err
	}
	if ok {
		s.seen[k] = rec.ProofID
		if rec.AcceptTip > s.maxTip {
			s.maxTip = rec.AcceptTip
			s.evictLocked()
		}
	}
	return nil
}

// evictLocked drops the solutions of work heights that can no longer be
// accepted. Call with mu held.
func (s *SolutionDedupStore) evictLocked() {
	if s.maxTip <= SolutionKeepHeights {
		return
	}
	floor := s.maxTip - SolutionKeepHeights
	for k := range s.seen {
		if k.height < floor {
			delete(s.seen, k)
		}
	}
}

// solutionIndexLen reports the index size (tests).
func (s *SolutionDedupStore) solutionIndexLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.seen)
}
