package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// MiningWork is the payload a miner fetches from
// GET /api/v1/mining/work?height=<h>. All byte fields are lowercase hex.
type MiningWork struct {
	Epoch             uint64              `json:"epoch"`
	Height            uint64              `json:"height"`
	HeaderHash        string              `json:"header_hash"`
	Difficulty        string              `json:"difficulty"`          // decimal string
	DAGSize           uint32              `json:"dag_size"`            // N entries
	WorkSetRoot       string              `json:"workset_root"`        // hex root
	WorkSet           []MiningWorkBatch   `json:"workset"`             // canonical order
	BatchCountMaximum uint32              `json:"batch_count_maximum"` // per §7 step 8
	BlocksPerEpoch    uint64              `json:"blocks_per_epoch"`
}

// MiningWorkBatch is one batch in the MiningWork.workset array. The cells
// are in canonical (ID-sorted) order per MINING_PROTOCOL.md §3.2.
type MiningWorkBatch struct {
	Cells []MiningWorkCell `json:"cells"`
}

// MiningWorkCell is the miner's view of a parent-cell reference.
type MiningWorkCell struct {
	ID          string `json:"id"`           // hex of the parent-cell ID
	ContentHash string `json:"content_hash"` // 32-byte SHA-256 hex
}

// MiningSubmitResponse is what POST /api/v1/mining/submit returns. On
// acceptance, Accepted=true and the ProofID is populated. On rejection
// the RejectReason is one of the closed set in pkg/mining.
type MiningSubmitResponse struct {
	Accepted     bool   `json:"accepted"`
	ProofID      string `json:"proof_id,omitempty"`
	RejectReason string `json:"reject_reason,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// MiningService is the narrow contract the validator provides to the HTTP
// layer. A nil service is legal — in that case the endpoints return 503
// Service Unavailable, signalling that this build/node is not configured
// to accept mining proofs. The reference validator wires a concrete
// MiningService at startup once pkg/chain exposes the required plumbing;
// miners run end-to-end in "local" mode via cmd/qsdmminer --self-test
// until that wiring lands.
type MiningService interface {
	// WorkAt returns the work payload a miner should solve for the given
	// block height. If the height is not currently mineable (e.g. the
	// header is not yet known, or the chain is idle), returns
	// ErrMiningUnavailable.
	WorkAt(height uint64) (*MiningWork, error)

	// Submit runs the full §7 acceptance algorithm on the raw JSON proof
	// against the chain's current tip. Returns the proof ID on accept or
	// a *mining.RejectError (unwrapped via errors.As) on reject.
	Submit(rawProofJSON []byte) ([32]byte, error)

	// TipHeight returns the current chain tip. Useful so the miner can
	// compare its own clock against the validator's without a round-trip
	// through /api/v1/status.
	TipHeight() uint64
}

// ErrMiningUnavailable is returned by MiningService.WorkAt when the node
// cannot currently produce a work payload. The handler maps it to 503.
var ErrMiningUnavailable = errors.New("mining: work unavailable")

// -----------------------------------------------------------------------------
// Handlers attach the mining endpoints to the existing Handlers struct.
// -----------------------------------------------------------------------------

// miningService is guarded by its own mutex so tests can swap in fakes
// without racing the hot wallet/contract paths.
type miningServiceHolder struct {
	mu  sync.RWMutex
	svc MiningService
}

var miningHolder = &miningServiceHolder{}

// SetMiningService installs (or removes, when svc==nil) the process-wide
// mining service. The reference validator calls this once at startup
// after the chain and mining subsystems are ready.
func SetMiningService(svc MiningService) {
	miningHolder.mu.Lock()
	defer miningHolder.mu.Unlock()
	miningHolder.svc = svc
}

func currentMiningService() MiningService {
	miningHolder.mu.RLock()
	defer miningHolder.mu.RUnlock()
	return miningHolder.svc
}

// MiningWorkHandler serves GET /api/v1/mining/work?height=<h>.
func (h *Handlers) MiningWorkHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	svc := currentMiningService()
	if svc == nil {
		writeMiningUnavailable(w, "mining service not configured on this node")
		return
	}
	heightStr := r.URL.Query().Get("height")
	var height uint64
	if heightStr == "" {
		height = svc.TipHeight() + 1
	} else {
		v, err := strconv.ParseUint(heightStr, 10, 64)
		if err != nil {
			http.Error(w, "invalid height", http.StatusBadRequest)
			return
		}
		height = v
	}
	work, err := svc.WorkAt(height)
	if err != nil {
		if errors.Is(err, ErrMiningUnavailable) {
			writeMiningUnavailable(w, err.Error())
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(work)
}

// MiningSubmitHandler serves POST /api/v1/mining/submit. The request body
// MUST be the canonical JSON produced by mining.Proof.CanonicalJSON; any
// deviation is rejected as non-canonical by the verifier.
func (h *Handlers) MiningSubmitHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	svc := currentMiningService()
	if svc == nil {
		writeMiningUnavailable(w, "mining service not configured on this node")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	id, err := svc.Submit(body)
	w.Header().Set("Content-Type", "application/json")
	if err == nil {
		_ = json.NewEncoder(w).Encode(MiningSubmitResponse{
			Accepted: true,
			ProofID:  hex.EncodeToString(id[:]),
		})
		return
	}
	var rej *mining.RejectError
	if errors.As(err, &rej) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(MiningSubmitResponse{
			Accepted:     false,
			RejectReason: string(rej.Reason),
			Detail:       rej.Detail,
		})
		return
	}
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(MiningSubmitResponse{
		Accepted: false,
		Detail:   err.Error(),
	})
}

func writeMiningUnavailable(w http.ResponseWriter, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "5")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":  "mining_unavailable",
		"detail": detail,
	})
}

// -----------------------------------------------------------------------------
// Conversion helpers shared with tests and the reference miner.
// -----------------------------------------------------------------------------

// WorkFromMiningCore builds a MiningWork from the pure-Go types in
// pkg/mining plus chain-side inputs. The reference MiningService uses
// this helper so HTTP wire shapes and in-process types stay in sync.
func WorkFromMiningCore(
	epoch uint64,
	height uint64,
	headerHash [32]byte,
	difficulty *big.Int,
	dagSize uint32,
	ws mining.WorkSet,
	blocksPerEpoch uint64,
) (*MiningWork, error) {
	if difficulty == nil || difficulty.Sign() <= 0 {
		return nil, errors.New("api: mining difficulty must be positive")
	}
	if err := ws.Validate(); err != nil {
		return nil, err
	}
	root := ws.Root()
	batches := make([]MiningWorkBatch, len(ws.Batches))
	for i, b := range ws.Batches {
		cells := make([]MiningWorkCell, len(b.Cells))
		for j, c := range b.Cells {
			cells[j] = MiningWorkCell{
				ID:          hex.EncodeToString(c.ID),
				ContentHash: hex.EncodeToString(c.ContentHash[:]),
			}
		}
		batches[i] = MiningWorkBatch{Cells: cells}
	}
	max := (uint64(len(ws.Batches)) + 15) / 16
	if max < 1 {
		max = 1
	}
	return &MiningWork{
		Epoch:             epoch,
		Height:            height,
		HeaderHash:        hex.EncodeToString(headerHash[:]),
		Difficulty:        difficulty.String(),
		DAGSize:           dagSize,
		WorkSetRoot:       hex.EncodeToString(root[:]),
		WorkSet:           batches,
		BatchCountMaximum: uint32(max),
		BlocksPerEpoch:    blocksPerEpoch,
	}, nil
}

// WorkToMiningCore is the inverse of WorkFromMiningCore, used by the
// reference miner to reconstruct a mining.WorkSet in memory. Round-trips
// exactly when the wire payload was produced by WorkFromMiningCore (all
// canonicalisation already happened).
func WorkToMiningCore(work *MiningWork) (mining.WorkSet, [32]byte, *big.Int, error) {
	if work == nil {
		return mining.WorkSet{}, [32]byte{}, nil, errors.New("api: nil work")
	}
	var hdr [32]byte
	if err := decodeHexBytes(hdr[:], work.HeaderHash, "header_hash"); err != nil {
		return mining.WorkSet{}, [32]byte{}, nil, err
	}
	diff, ok := new(big.Int).SetString(work.Difficulty, 10)
	if !ok || diff.Sign() <= 0 {
		return mining.WorkSet{}, [32]byte{}, nil, errors.New("api: invalid difficulty")
	}
	ws := mining.WorkSet{Batches: make([]mining.Batch, len(work.WorkSet))}
	for i, b := range work.WorkSet {
		cells := make([]mining.ParentCellRef, len(b.Cells))
		for j, c := range b.Cells {
			id, err := hex.DecodeString(c.ID)
			if err != nil {
				return mining.WorkSet{}, [32]byte{}, nil, err
			}
			var ch [32]byte
			if err := decodeHexBytes(ch[:], c.ContentHash, "content_hash"); err != nil {
				return mining.WorkSet{}, [32]byte{}, nil, err
			}
			cells[j] = mining.ParentCellRef{ID: id, ContentHash: ch}
		}
		ws.Batches[i] = mining.Batch{Cells: cells}
	}
	return ws, hdr, diff, nil
}

func decodeHexBytes(dst []byte, s, field string) error {
	b, err := hex.DecodeString(s)
	if err != nil {
		return errors.New("api: decode " + field + ": " + err.Error())
	}
	if len(b) != len(dst) {
		return errors.New("api: " + field + " wrong length")
	}
	copy(dst, b)
	return nil
}
