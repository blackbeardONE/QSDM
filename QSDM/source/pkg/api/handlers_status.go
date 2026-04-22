package api

import (
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

// nodeStartTime is captured once per process so /api/v1/status can report an
// uptime. It is initialised by init() and never reset — reboots start a new
// process which gets a new startTime.
var nodeStartTime = time.Now()

// StatusResponse is the public shape returned by GET /api/v1/status.
//
// Fields match the SDK type qsdmplus.NodeStatus (see sdk/go/qsdmplus.go): node_id,
// version, uptime, peers, chain_tip. The Major Update extends the response
// with node_role (validator | miner), coin metadata (name, symbol, decimals,
// smallest_unit), and legacy-branding hints so SDK consumers can render the
// network badge and tokenomics widgets from a single endpoint.
//
// This handler is intentionally public and read-only: it exposes only
// non-sensitive, operator-advertised metadata. It never returns secrets, peer
// addresses, or NGC proof contents.
type StatusResponse struct {
	NodeID     string         `json:"node_id,omitempty"`
	Version    string         `json:"version,omitempty"`
	Uptime     string         `json:"uptime,omitempty"`
	ChainTip   uint64         `json:"chain_tip"`
	Peers      int            `json:"peers"`
	NodeRole   string         `json:"node_role"`
	Network    string         `json:"network"`
	Coin       CoinInfo       `json:"coin"`
	Branding   BrandInfo      `json:"branding"`
	Tokenomics TokenomicsInfo `json:"tokenomics"`
}

// TokenomicsInfo is the live emission-schedule snapshot at the current
// chain tip. All numeric fields are expressed in dust (the smallest
// indivisible unit) so callers can do lossless integer math. Human-readable
// CELL values are provided as strings for display only.
type TokenomicsInfo struct {
	CapDust                uint64 `json:"cap_dust"`
	CapCell                string `json:"cap_cell"`
	EmittedDust            uint64 `json:"emitted_dust"`
	EmittedCell            string `json:"emitted_cell"`
	RemainingDust          uint64 `json:"remaining_dust"`
	BlockRewardDust        uint64 `json:"block_reward_dust"`
	BlockRewardCell        string `json:"block_reward_cell"`
	CurrentEpoch           uint32 `json:"current_epoch"`
	NextHalvingHeight      uint64 `json:"next_halving_height"`
	NextHalvingETASeconds  uint64 `json:"next_halving_eta_seconds"`
	TargetBlockTimeSeconds uint64 `json:"target_block_time_seconds"`
	BlocksPerEpoch         uint64 `json:"blocks_per_epoch"`
}

// CoinInfo is the public coin metadata block. Values are sourced from
// pkg/branding; changing them here would desync the node's public statement of
// its own coin and the audit checklist that verifies it.
type CoinInfo struct {
	Name          string `json:"name"`
	Symbol        string `json:"symbol"`
	Decimals      int    `json:"decimals"`
	SmallestUnit  string `json:"smallest_unit"`
}

// BrandInfo advertises both the current canonical brand name and the legacy
// name retained during the deprecation window. Downstream tooling (SDKs,
// explorers, dashboards) can use the legacy field to decide whether to show
// a migration banner.
type BrandInfo struct {
	Name       string `json:"name"`
	LegacyName string `json:"legacy_name,omitempty"`
	FullTitle  string `json:"full_title,omitempty"`
}

// nodeStatusConfig is the subset of configuration the status endpoint needs.
// Kept minimal so the handler does not require a full *config.Config on every
// request (callers capture the snapshot once at startup).
type nodeStatusConfig struct {
	NodeRole config.NodeRole
}

// StatusHandler serves GET /api/v1/status.
//
// The handler is stateless: it reads from the Handlers struct (for node_id and
// peer snapshot) and from pkg/branding (for coin + brand metadata). It is
// designed to be safe to call from an unauthenticated client — the landing
// page and SDKs rely on this being reachable without a token.
func (h *Handlers) StatusHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	peers := h.snapshotPeerCount()
	chainTip := h.snapshotChainTip()

	role := config.NodeRoleValidator
	if h.nodeRole != "" {
		role = config.NodeRole(h.nodeRole)
		if !role.IsValid() {
			role = config.NodeRoleValidator
		}
	}

	schedule := chain.DefaultEmissionSchedule()
	blockReward := schedule.BlockRewardDust(chainTip + 1)
	emitted := schedule.CumulativeEmittedDust(chainTip)
	currentEpoch := schedule.EpochForHeight(chainTip)
	capCell := formatDustAsCell(schedule.MiningCapDust)
	emittedCell := formatDustAsCell(emitted)

	resp := StatusResponse{
		NodeID:   h.nodeID,
		Version:  statusVersion(),
		Uptime:   time.Since(nodeStartTime).Truncate(time.Second).String(),
		ChainTip: chainTip,
		Peers:    peers,
		NodeRole: role.String(),
		Network:  branding.NetworkLabel(),
		Coin: CoinInfo{
			Name:         branding.CoinName,
			Symbol:       branding.CoinSymbol,
			Decimals:     branding.CoinDecimals,
			SmallestUnit: branding.SmallestUnitName,
		},
		Branding: BrandInfo{
			Name:       branding.Name,
			LegacyName: branding.LegacyName,
			FullTitle:  branding.FullTitle(),
		},
		Tokenomics: TokenomicsInfo{
			CapDust:                schedule.MiningCapDust,
			CapCell:                capCell,
			EmittedDust:            emitted,
			EmittedCell:            emittedCell,
			RemainingDust:          schedule.RemainingSupplyDust(chainTip),
			BlockRewardDust:        blockReward,
			BlockRewardCell:        schedule.BlockRewardCell(chainTip + 1),
			CurrentEpoch:           currentEpoch,
			NextHalvingHeight:      schedule.NextHalvingHeight(chainTip),
			NextHalvingETASeconds:  schedule.NextHalvingETA(chainTip),
			TargetBlockTimeSeconds: schedule.TargetBlockTimeSeconds,
			BlocksPerEpoch:         schedule.BlocksPerEpoch,
		},
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// snapshotPeerCount returns the current peer count if the server wired one in.
// When no source is available (tests, early startup) it returns 0 rather than
// failing — /api/v1/status must always respond.
func (h *Handlers) snapshotPeerCount() int {
	if h.peerCountSource == nil {
		return 0
	}
	n := h.peerCountSource()
	if n < 0 {
		return 0
	}
	return n
}

// snapshotChainTip returns the current chain tip height if the server wired
// one in, otherwise 0.
func (h *Handlers) snapshotChainTip() uint64 {
	if h.chainTipSource == nil {
		return 0
	}
	return h.chainTipSource()
}

// SetNodeRole records the operator-declared node role. Called once at server
// startup from registerRoutes. The role string is validated and normalised;
// an unknown value is silently coerced to "validator" so the endpoint never
// reports an invalid role to SDK consumers (the startup guard in
// `cmd/qsdmplus/main.go` is the authoritative check).
func (h *Handlers) SetNodeRole(role config.NodeRole) {
	if !role.IsValid() {
		role = config.NodeRoleValidator
	}
	h.nodeRole = string(role)
}

// SetPeerCountSource wires a live peer-count callback into the status handler.
// The callback must be safe for concurrent use and should return quickly.
func (h *Handlers) SetPeerCountSource(fn func() int) {
	h.peerCountSource = fn
}

// SetChainTipSource wires a live chain-tip callback into the status handler.
// The callback must be safe for concurrent use and should return quickly.
func (h *Handlers) SetChainTipSource(fn func() uint64) {
	h.chainTipSource = fn
}

// formatDustAsCell converts a dust amount into a CELL-denominated decimal
// string with exactly branding.CoinDecimals fractional digits. This is
// display-only; never use the string for equality or arithmetic.
func formatDustAsCell(dust uint64) string {
	const dustPerCell uint64 = 100_000_000
	whole := dust / dustPerCell
	frac := dust % dustPerCell
	// Manual formatting to avoid pulling in strconv.Uitoa.
	fracStr := []byte("00000000")
	for i := 7; i >= 0 && frac > 0; i-- {
		fracStr[i] = byte('0' + frac%10)
		frac /= 10
	}
	whStr := []byte("0")
	if whole > 0 {
		var buf [20]byte
		pos := len(buf)
		for whole > 0 {
			pos--
			buf[pos] = byte('0' + whole%10)
			whole /= 10
		}
		whStr = buf[pos:]
	}
	return string(whStr) + "." + string(fracStr)
}

// statusVersion returns the build version string, preferring the Go version
// plus any `QSDM_BUILD_VERSION` (or legacy `QSDMPLUS_BUILD_VERSION`) set at
// build time.
func statusVersion() string {
	if v := strings.TrimSpace(os.Getenv("QSDM_BUILD_VERSION")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("QSDMPLUS_BUILD_VERSION")); v != "" {
		return v
	}
	return runtime.Version()
}
