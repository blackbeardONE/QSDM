package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/poe"
)

// GET /api/v1/chain/parents: the parent source for Proof-of-Entanglement.
//
// A signed wallet transfer names parent_cells inside its signed envelope.
// Once PoE is active ([consensus] poe_activation_height), every parent must
// be a transaction already committed in the last poe.ParentWindowBlocks
// blocks. Wallets call this endpoint just before signing and copy `parents`
// into parent_cells. The IDs come from this node's durable canonical chain
// (never from the mempool or receipts), newest first. Before activation the
// same parents are accepted too, so a wallet that always uses this endpoint
// works on both sides of the activation height.

// PoEParentView is one committed transaction a wallet may name as a parent.
type PoEParentView struct {
	ID     string `json:"id"`
	Height uint64 `json:"height"`
}

// PoEParentSource supplies recent committed transaction IDs. ok is false
// while the node has no durable tip yet.
type PoEParentSource interface {
	RecentParents(n int) (tip uint64, refs []PoEParentView, ok bool)
}

// PoEParentsResponse is the body of GET /api/v1/chain/parents.
type PoEParentsResponse struct {
	// Tip is the durable chain tip the parents were read at.
	Tip uint64 `json:"tip"`
	// PoEActivationHeight is the configured activation height (0 = none).
	PoEActivationHeight uint64 `json:"poe_activation_height"`
	// PoEActive reports whether the rules apply to the next block.
	PoEActive    bool `json:"poe_active"`
	MinParents   int  `json:"min_parents"`
	MaxParents   int  `json:"max_parents"`
	WindowBlocks int  `json:"window_blocks"`
	// Parents is the suggested parent_cells value: the MinParents most
	// recently committed transaction IDs.
	Parents []string `json:"parents"`
	// Recent lists up to `limit` committed IDs with their heights, newest
	// first, for wallets that want to choose their own.
	Recent []PoEParentView `json:"recent"`
}

const (
	poeParentsDefaultLimit = 8
	poeParentsMaxLimit     = 32
)

type poeParentSourceHolder struct {
	mu  sync.RWMutex
	src PoEParentSource
}

var poeParentSourceRegistry = &poeParentSourceHolder{}

// SetPoEParentSource installs (or removes, with nil) the process-wide parent
// source. cmd/qsdm wires it to the block producer, clamped to the durable
// tip.
func SetPoEParentSource(src PoEParentSource) {
	poeParentSourceRegistry.mu.Lock()
	defer poeParentSourceRegistry.mu.Unlock()
	poeParentSourceRegistry.src = src
}

func currentPoEParentSource() PoEParentSource {
	poeParentSourceRegistry.mu.RLock()
	defer poeParentSourceRegistry.mu.RUnlock()
	return poeParentSourceRegistry.src
}

// ChainParentsHandler serves GET /api/v1/chain/parents?limit=<n>.
func (h *Handlers) ChainParentsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	src := currentPoEParentSource()
	if src == nil {
		writeMiningUnavailable(w, "chain parent source not configured")
		return
	}
	limit := poeParentsDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			http.Error(w, "limit must be a non-negative integer", http.StatusBadRequest)
			return
		}
		if v > 0 {
			limit = v
		}
	}
	if limit < poe.MinParents {
		limit = poe.MinParents
	}
	if limit > poeParentsMaxLimit {
		limit = poeParentsMaxLimit
	}
	tip, refs, ok := src.RecentParents(limit)
	if !ok || len(refs) < poe.MinParents {
		writeMiningUnavailable(w, "not enough committed transactions to suggest parents yet")
		return
	}
	resp := PoEParentsResponse{
		Tip:                 tip,
		PoEActivationHeight: chain.PoEActivationHeight(),
		PoEActive:           chain.PoEActiveAt(tip + 1),
		MinParents:          poe.MinParents,
		MaxParents:          poe.MaxParents,
		WindowBlocks:        poe.ParentWindowBlocks,
		Recent:              refs,
	}
	for _, ref := range refs[:poe.MinParents] {
		resp.Parents = append(resp.Parents, ref.ID)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(resp)
}
