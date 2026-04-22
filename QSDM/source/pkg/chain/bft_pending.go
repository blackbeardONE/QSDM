package chain

import (
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// PendingProposalStore retains block bodies learned from gossiped proposals (keyed by height
// and vote value — typically StateRoot — so followers can resolve what validators voted on).
type PendingProposalStore struct {
	mu sync.RWMutex
	// height -> vote value (e.g. state root) -> block body
	by map[uint64]map[string]*Block
}

// NewPendingProposalStore creates an empty proposal cache.
func NewPendingProposalStore() *PendingProposalStore {
	return &PendingProposalStore{by: make(map[uint64]map[string]*Block)}
}

// Put stores a shallow copy of the block for the given height and vote value.
func (s *PendingProposalStore) Put(height uint64, voteValue string, b *Block) {
	if s == nil || b == nil || voteValue == "" {
		return
	}
	cp := *b
	if len(b.Transactions) > 0 {
		cp.Transactions = append([]*mempool.Tx(nil), b.Transactions...)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inner, ok := s.by[height]
	if !ok {
		inner = make(map[string]*Block)
		s.by[height] = inner
	}
	inner[voteValue] = &cp
}

// Get returns a stored block for this height and vote value, if any.
func (s *PendingProposalStore) Get(height uint64, voteValue string) (*Block, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	inner, ok := s.by[height]
	if !ok {
		return nil, false
	}
	b, ok := inner[voteValue]
	if !ok || b == nil {
		return nil, false
	}
	return b, true
}

// RemoveHeight drops all cached proposals at the given height.
func (s *PendingProposalStore) RemoveHeight(height uint64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.by, height)
}

// PruneHeightsBelow removes cached proposals with height strictly less than keepMinHeight.
func (s *PendingProposalStore) PruneHeightsBelow(keepMinHeight uint64) {
	if s == nil || keepMinHeight == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for h := range s.by {
		if h < keepMinHeight {
			delete(s.by, h)
		}
	}
}
