package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

// MiningAccountProbe is the read-only contract the
// /api/v1/mining/account endpoint uses to surface CELL
// balances from the live AccountStore. Wired by the validator
// in solo-validator mode so operators can see mining rewards
// land. Without a probe, the endpoint returns 503 with
// "balance probe not configured" — distinct from "service not
// configured" so debugging is unambiguous.
//
// The contract is intentionally thin (one method) so the
// implementation in cmd/qsdm/main.go is a one-line closure
// that calls adminAccounts.Get.
type MiningAccountProbe interface {
	// BalanceOf returns the CELL balance and nonce of an
	// address, plus a `present` flag distinguishing
	// "address has 0 balance because it never received any
	// txs" (false) from "address received and spent
	// everything" (true, balance=0). Lookups for unknown
	// addresses return present=false, balance=0, nonce=0.
	BalanceOf(address string) (balance float64, nonce uint64, present bool)
}

type miningAccountProbeHolder struct {
	mu    sync.RWMutex
	probe MiningAccountProbe
}

var miningAccountProbeRegistry = &miningAccountProbeHolder{}

// SetMiningAccountProbe installs (or removes, when probe==nil)
// the process-wide balance probe. Outside solo mode, the
// validator does not wire one and the endpoint stays at 503.
func SetMiningAccountProbe(probe MiningAccountProbe) {
	miningAccountProbeRegistry.mu.Lock()
	defer miningAccountProbeRegistry.mu.Unlock()
	miningAccountProbeRegistry.probe = probe
}

func currentMiningAccountProbe() MiningAccountProbe {
	miningAccountProbeRegistry.mu.RLock()
	defer miningAccountProbeRegistry.mu.RUnlock()
	return miningAccountProbeRegistry.probe
}

// MiningAccountResponse is the wire payload for
// GET /api/v1/mining/account?address=<addr>.
type MiningAccountResponse struct {
	Address string  `json:"address"`
	Balance float64 `json:"balance"`
	Nonce   uint64  `json:"nonce"`
	Present bool    `json:"present"`
}

// MiningEmissionProbe is the read-only contract the
// /api/v1/mining/emission endpoint uses to surface the §8
// emission schedule, current per-block reward, cumulative
// emission, and remaining supply. Wired by the validator
// regardless of solo / peer mode because the data is pure
// schedule state — no AccountStore peek.
type MiningEmissionProbe interface {
	// Snapshot returns a single-call view of the current
	// emission state at the live chain tip. The struct shape
	// mirrors MiningEmissionResponse so implementations can
	// fill it directly.
	Snapshot() MiningEmissionSnapshot
}

// MiningEmissionSnapshot bundles the read-only schedule
// state returned by MiningEmissionProbe.Snapshot. Field
// units match the on-wire MiningEmissionResponse.
type MiningEmissionSnapshot struct {
	ChainTip               uint64
	MiningCapDust          uint64
	BlocksPerEpoch         uint64
	TargetBlockTimeSeconds uint64
	CurrentEpoch           uint32
	BlockRewardDust        uint64
	BlockRewardCell        string
	EmittedDust            uint64
	EmittedCell            string
	RemainingDust          uint64
	NextHalvingHeight      uint64
	NextHalvingETASeconds  uint64
}

// MiningEmissionResponse is the wire payload for
// GET /api/v1/mining/emission.
type MiningEmissionResponse struct {
	ChainTip               uint64 `json:"chain_tip"`
	MiningCapDust          uint64 `json:"mining_cap_dust"`
	BlocksPerEpoch         uint64 `json:"blocks_per_epoch"`
	TargetBlockTimeSeconds uint64 `json:"target_block_time_seconds"`
	CurrentEpoch           uint32 `json:"current_epoch"`
	BlockRewardDust        uint64 `json:"block_reward_dust"`
	BlockRewardCell        string `json:"block_reward_cell"`
	EmittedDust            uint64 `json:"emitted_dust"`
	EmittedCell            string `json:"emitted_cell"`
	RemainingDust          uint64 `json:"remaining_dust"`
	NextHalvingHeight      uint64 `json:"next_halving_height"`
	NextHalvingETASeconds  uint64 `json:"next_halving_eta_seconds"`
}

type miningEmissionProbeHolder struct {
	mu    sync.RWMutex
	probe MiningEmissionProbe
}

var miningEmissionProbeRegistry = &miningEmissionProbeHolder{}

// SetMiningEmissionProbe installs (or removes, when
// probe==nil) the process-wide emission probe.
func SetMiningEmissionProbe(probe MiningEmissionProbe) {
	miningEmissionProbeRegistry.mu.Lock()
	defer miningEmissionProbeRegistry.mu.Unlock()
	miningEmissionProbeRegistry.probe = probe
}

func currentMiningEmissionProbe() MiningEmissionProbe {
	miningEmissionProbeRegistry.mu.RLock()
	defer miningEmissionProbeRegistry.mu.RUnlock()
	return miningEmissionProbeRegistry.probe
}

// MiningEmissionHandler serves GET /api/v1/mining/emission.
// Returns 503 when no probe is wired.
func (h *Handlers) MiningEmissionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	probe := currentMiningEmissionProbe()
	if probe == nil {
		writeMiningUnavailable(w, "emission probe not configured")
		return
	}
	snap := probe.Snapshot()
	resp := MiningEmissionResponse{
		ChainTip:               snap.ChainTip,
		MiningCapDust:          snap.MiningCapDust,
		BlocksPerEpoch:         snap.BlocksPerEpoch,
		TargetBlockTimeSeconds: snap.TargetBlockTimeSeconds,
		CurrentEpoch:           snap.CurrentEpoch,
		BlockRewardDust:        snap.BlockRewardDust,
		BlockRewardCell:        snap.BlockRewardCell,
		EmittedDust:            snap.EmittedDust,
		EmittedCell:            snap.EmittedCell,
		RemainingDust:          snap.RemainingDust,
		NextHalvingHeight:      snap.NextHalvingHeight,
		NextHalvingETASeconds:  snap.NextHalvingETASeconds,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// MiningBlocksProbe is the read-only contract the
// /api/v1/mining/blocks endpoint uses to surface block-header
// metadata for the public chain dashboard.
//
// Implementations return the headers in height order, ascending,
// for the inclusive [from, to] range. The probe is responsible
// for clamping `to` to the current tip and for handling from > to
// as an empty result; the handler itself only enforces the page
// size cap (MiningBlocksMaxLimit) so a runaway query can't pin
// the validator's mu for an unbounded number of headers.
type MiningBlocksProbe interface {
	HeadersInRange(from, to uint64) []MiningBlockHeader
	Tip() uint64
}

// MiningBlockHeader is a wire-friendly subset of chain.BlockHeader
// (we deliberately don't import pkg/chain into pkg/api to keep the
// dependency arrow pointing inward). Field names mirror the JSON
// tags on chain.BlockHeader so a future chain-package wire bump
// stays consistent with what the dashboard renders.
type MiningBlockHeader struct {
	Height     uint64 `json:"height"`
	Hash       string `json:"hash"`
	PrevHash   string `json:"prev_hash"`
	StateRoot  string `json:"state_root"`
	TxRoot     string `json:"tx_root"`
	TxCount    int    `json:"tx_count"`
	Timestamp  string `json:"timestamp"`
	ProducerID string `json:"producer_id,omitempty"`
}

// MiningBlocksMaxLimit caps the number of headers returned by
// a single /api/v1/mining/blocks call. 200 is enough for the
// canonical dashboard view (last 20) and for "show me the
// last few minutes of seals" tooling, while bounding the
// per-call lock duration at the BlockProducer's bp.mu.
const MiningBlocksMaxLimit = 200

// MiningBlocksResponse wraps the headers in a small envelope so
// future fields (pagination cursor, total count) can land
// without a breaking schema change. Empty slice on no results
// (NOT null) for friendly JS consumption.
type MiningBlocksResponse struct {
	Tip     uint64              `json:"tip"`
	From    uint64              `json:"from"`
	To      uint64              `json:"to"`
	Headers []MiningBlockHeader `json:"headers"`
}

type miningBlocksProbeHolder struct {
	mu    sync.RWMutex
	probe MiningBlocksProbe
}

var miningBlocksProbeRegistry = &miningBlocksProbeHolder{}

// SetMiningBlocksProbe installs (or removes, when probe==nil)
// the process-wide block-header probe. Wired by cmd/qsdm/main.go
// against the live BlockProducer; outside the solo deploy a peer
// cluster's GossipSub-driven block flow would wire a different
// implementation that draws from the local replicated chain.
func SetMiningBlocksProbe(probe MiningBlocksProbe) {
	miningBlocksProbeRegistry.mu.Lock()
	defer miningBlocksProbeRegistry.mu.Unlock()
	miningBlocksProbeRegistry.probe = probe
}

func currentMiningBlocksProbe() MiningBlocksProbe {
	miningBlocksProbeRegistry.mu.RLock()
	defer miningBlocksProbeRegistry.mu.RUnlock()
	return miningBlocksProbeRegistry.probe
}

// MiningBlocksHandler serves GET /api/v1/mining/blocks.
//
// Query params (all optional):
//
//	?from=<height>   default = max(0, tip - default_limit + 1)
//	?to=<height>     default = tip
//	?limit=<n>       default = 20, capped at MiningBlocksMaxLimit;
//	                 used as `to - limit + 1` if `from` is unset.
//
// Returns 503 when no probe is wired (the canonical posture for a
// non-solo deploy where this endpoint isn't yet wired). Returns
// 400 when the parameters parse cleanly but are inconsistent
// (from > to with both explicit).
func (h *Handlers) MiningBlocksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	probe := currentMiningBlocksProbe()
	if probe == nil {
		writeMiningUnavailable(w, "blocks probe not configured")
		return
	}
	tip := probe.Tip()

	q := r.URL.Query()
	limit := uint64(20)
	if raw := q.Get("limit"); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			http.Error(w, "limit must be a non-negative integer", http.StatusBadRequest)
			return
		}
		limit = v
	}
	if limit == 0 {
		limit = 20
	}
	if limit > MiningBlocksMaxLimit {
		limit = MiningBlocksMaxLimit
	}

	var from, to uint64
	hasFrom := q.Get("from") != ""
	hasTo := q.Get("to") != ""
	if hasTo {
		v, err := strconv.ParseUint(q.Get("to"), 10, 64)
		if err != nil {
			http.Error(w, "to must be a non-negative integer", http.StatusBadRequest)
			return
		}
		to = v
	} else {
		to = tip
	}
	if hasFrom {
		v, err := strconv.ParseUint(q.Get("from"), 10, 64)
		if err != nil {
			http.Error(w, "from must be a non-negative integer", http.StatusBadRequest)
			return
		}
		from = v
	} else {
		// No explicit from → derive from limit + to.
		// Underflow-safe: an int64 swing past 0 wraps and would
		// blow the page size; we clamp to 0 instead.
		if to+1 > limit {
			from = to + 1 - limit
		} else {
			from = 0
		}
	}
	if from > to {
		http.Error(w, "from must be <= to", http.StatusBadRequest)
		return
	}
	if to-from+1 > MiningBlocksMaxLimit {
		http.Error(w, fmt.Sprintf("range exceeds MiningBlocksMaxLimit=%d headers", MiningBlocksMaxLimit), http.StatusBadRequest)
		return
	}

	headers := probe.HeadersInRange(from, to)
	if headers == nil {
		headers = []MiningBlockHeader{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MiningBlocksResponse{
		Tip:     tip,
		From:    from,
		To:      to,
		Headers: headers,
	})
}

// MiningAccountHandler serves GET /api/v1/mining/account?address=<addr>.
// Returns 503 when no probe is wired (canonical posture
// outside solo mode). Returns 400 when the address parameter
// is missing.
func (h *Handlers) MiningAccountHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	probe := currentMiningAccountProbe()
	if probe == nil {
		writeMiningUnavailable(w, "balance probe not configured (solo mode only)")
		return
	}
	address := r.URL.Query().Get("address")
	if address == "" {
		http.Error(w, "address parameter is required", http.StatusBadRequest)
		return
	}
	bal, nonce, present := probe.BalanceOf(address)
	resp := MiningAccountResponse{
		Address: address,
		Balance: bal,
		Nonce:   nonce,
		Present: present,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
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
