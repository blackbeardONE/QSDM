package api

import (
	"encoding/json"
	"math"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// nodeStartTime is captured once per process so /api/v1/status can report an
// uptime. It is initialised by init() and never reset — reboots start a new
// process which gets a new startTime.
var nodeStartTime = time.Now()

// StatusResponse is the public shape returned by GET /api/v1/status.
//
// Fields match the SDK type qsdm.NodeStatus (see sdk/go/qsdm.go): node_id,
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
	GitSHA     string         `json:"git_sha,omitempty"`
	BuildDate  string         `json:"build_date,omitempty"`
	Uptime     string         `json:"uptime,omitempty"`
	ChainTip   uint64         `json:"chain_tip"`
	Peers      int            `json:"peers"`
	NodeRole   string         `json:"node_role"`
	Network    string         `json:"network"`
	Coin       CoinInfo       `json:"coin"`
	Branding   BrandInfo      `json:"branding"`
	Tokenomics TokenomicsInfo `json:"tokenomics"`
	// TaskActionsReady is true only when signed task actions have a live
	// validator mempool submitter. Clients can distinguish maintenance from a
	// partially wired validator without attempting a state-changing action.
	TaskActionsReady bool `json:"task_actions_ready"`

	// Mining is the consensus-visible mining-protocol state. Miners
	// MUST inspect this block at startup to decide which protocol to
	// submit proofs under — submitting v1 against a validator whose
	// v2 fork has activated will be rejected by the verifier with
	// ReasonBadVersion (pkg/mining/verifier.go §Step 1). The field
	// is `omitempty` only on the outer pointer, never on the inner
	// scalars, so SDK callers can rely on `mining.fork_v2_active`
	// being present whenever `mining` itself is.
	Mining *MiningInfo `json:"mining,omitempty"`
}

// MiningInfo advertises the validator's mining-consensus posture so
// that clients (miners, explorers, dashboards) can self-configure
// without out-of-band knowledge of the network's fork schedule.
//
// All fork-height fields are absolute chain heights. A value of
// `18446744073709551615` (math.MaxUint64) MUST be interpreted as
// "the fork is not yet scheduled on this network"; the JSON field is
// elided in that case (`omitempty`) so the wire form stays compact
// for v1-only deployments.
//
// Mainnet (`api.qsdm.tech`) currently advertises:
//
//	{
//	  "protocol_versions_accepted": [2],
//	  "fork_v2_height": 0,
//	  "fork_v2_active":  true,
//	  "fork_v2_tc_height": <varies>,
//	  "fork_v2_tc_active": false,
//	  "attestation_types_required": ["nvidia-cc-v1","nvidia-hmac-v1"],
//	  "min_enroll_stake_dust": 1000000000
//	}
//
// because pkg/mining.ForkV2Height() == 0 and the live chain tip is
// already > 0. Any v1 proof submitted against this validator is
// rejected at the consensus layer.
type MiningInfo struct {
	// ProtocolVersionsAccepted lists the Proof.Version values that
	// the verifier will accept at the CURRENT chain tip. On a
	// post-fork chain this is `[2]` only; on a pre-fork chain
	// (ForkV2Height() > ChainTip) it is `[1]`. Boundary heights
	// are inclusive-on-v2 — at p.Height == ForkV2Height() the
	// verifier requires v2 (see verifier.go IsV2 helper).
	ProtocolVersionsAccepted []uint32 `json:"protocol_versions_accepted"`

	// ForkV2Height is the absolute block height at which the v2
	// NVIDIA-locked attestation gate activates. Elided when the
	// fork is not scheduled (math.MaxUint64 sentinel — see
	// pkg/mining/fork.go).
	ForkV2Height uint64 `json:"fork_v2_height,omitempty"`

	// ForkV2Active is true iff the current chain tip is at or above
	// ForkV2Height. This is the field miners SHOULD branch on; it
	// folds the "fork not scheduled" case (height = MaxUint64) and
	// the "fork scheduled but not yet reached" case into a single
	// false, and the "fork active" case into a single true.
	ForkV2Active bool `json:"fork_v2_active"`

	// ForkV2TCHeight is the Tensor-Core PoW mixin fork height. Same
	// semantics as ForkV2Height: elided when not scheduled.
	ForkV2TCHeight uint64 `json:"fork_v2_tc_height,omitempty"`

	// ForkV2TCActive folds the "scheduled and reached" semantics
	// for the TC fork the same way ForkV2Active does for the
	// attestation fork.
	ForkV2TCActive bool `json:"fork_v2_tc_active"`

	// AttestationTypesRequired is the whitelist a v2 proof's
	// Attestation.Type field must match. Empty in a v1-only
	// posture; ["nvidia-cc-v1","nvidia-hmac-v1"] post-v2.
	AttestationTypesRequired []string `json:"attestation_types_required,omitempty"`

	// MinEnrollStakeDust is the bonded stake (in dust, 1 CELL = 1e8
	// dust) required for an nvidia-hmac-v1 operator enrollment. 0
	// when v2 is not active.
	MinEnrollStakeDust uint64 `json:"min_enroll_stake_dust,omitempty"`

	// EnrollmentContract identifies the signed envelope generation clients
	// must submit. Legacy unsigned v1 is never advertised to new clients.
	EnrollmentContract string `json:"enrollment_contract"`

	// SignedEnrollmentRequired is explicit for older clients that do not
	// understand contract version strings.
	SignedEnrollmentRequired bool `json:"signed_enrollment_required"`

	// SignedEnrollmentActivationHeight is the consensus height after which
	// unsigned v1 enrollment is invalid even when injected directly in a block.
	SignedEnrollmentActivationHeight uint64 `json:"signed_enrollment_activation_height"`

	// DeferredBondFromRewards advertises whether zero-balance miners may build
	// their enrollment bond from protocol mining rewards at the current tip.
	DeferredBondFromRewards bool `json:"deferred_bond_from_rewards"`

	DeferredBondActivationHeight uint64 `json:"deferred_bond_activation_height"`

	DeferredBondWorkDifficulty uint8 `json:"deferred_bond_work_difficulty"`
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
	Name         string `json:"name"`
	Symbol       string `json:"symbol"`
	Decimals     int    `json:"decimals"`
	SmallestUnit string `json:"smallest_unit"`
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
		NodeID:           h.nodeID,
		Version:          statusVersion(),
		GitSHA:           buildinfo.GitSHA,
		BuildDate:        buildinfo.BuildDate,
		Uptime:           time.Since(nodeStartTime).Truncate(time.Second).String(),
		ChainTip:         chainTip,
		Peers:            peers,
		NodeRole:         role.String(),
		Network:          branding.NetworkLabel(),
		TaskActionsReady: TaskActionSubmissionReady(),
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
		Mining: buildMiningInfo(chainTip),
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
// `cmd/qsdm/main.go` is the authoritative check).
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

// buildMiningInfo snapshots the in-process v2 fork state into a
// MiningInfo struct that the public /api/v1/status handler embeds.
//
// The function reads pkg/mining.ForkV2Height() / ForkV2TCHeight()
// once each — these are atomic loads, so the snapshot is sequentially
// consistent with whichever value SetForkV2Height most recently
// committed. The "active" booleans are computed against the supplied
// chainTip so /api/v1/status reports the same answer the verifier
// would return for a proof at that height — there is no separate
// truth.
//
// math.MaxUint64 is the in-process sentinel for "fork not yet
// scheduled" (see fork.go init()). The MiningInfo struct does NOT
// emit the sentinel on the wire: a height of 18446744073709551615
// would mislead naive clients into computing "chain_tip > fork_height
// → active", which is exactly backwards. Instead, the height field
// is `omitempty` and elided in that case, and `fork_v*_active` stays
// false. SDK callers that want to detect "v2 will eventually
// activate" should branch on the presence of `fork_v2_height` in the
// JSON object, not on a comparison against MaxUint64.
func buildMiningInfo(chainTip uint64) *MiningInfo {
	v2Height := mining.ForkV2Height()
	tcHeight := mining.ForkV2TCHeight()

	v2Active := v2Height != math.MaxUint64 && chainTip >= v2Height
	tcActive := tcHeight != math.MaxUint64 && chainTip >= tcHeight

	info := &MiningInfo{
		ForkV2Active:                     v2Active,
		ForkV2TCActive:                   tcActive,
		EnrollmentContract:               enrollment.SignedContractID,
		SignedEnrollmentRequired:         true,
		SignedEnrollmentActivationHeight: enrollment.SignedContractActivationHeight,
		DeferredBondFromRewards:          v2Active && chainTip >= enrollment.DeferredBondActivationHeight,
		DeferredBondActivationHeight:     enrollment.DeferredBondActivationHeight,
		DeferredBondWorkDifficulty:       enrollment.DeferredBondWorkDifficulty,
	}
	if v2Height != math.MaxUint64 {
		info.ForkV2Height = v2Height
	}
	if tcHeight != math.MaxUint64 {
		info.ForkV2TCHeight = tcHeight
	}

	// The accepted-versions list is what the verifier ACTUALLY
	// accepts at chainTip. Pre-fork validators take v1 only;
	// post-fork take v2 only. The list is intentionally not
	// "[1, 2] both ok" — there is no version-tolerant window in
	// the verifier, and exposing one here would tempt clients
	// to keep submitting v1.
	if v2Active {
		info.ProtocolVersionsAccepted = []uint32{mining.ProtocolVersionV2}
		info.AttestationTypesRequired = []string{mining.AttestationTypeCC, mining.AttestationTypeHMAC}
		info.MinEnrollStakeDust = mining.MinEnrollStakeDust
	} else {
		info.ProtocolVersionsAccepted = []uint32{mining.ProtocolVersion}
	}
	return info
}

// statusVersion returns the build version string reported by
// GET /api/v1/status's `version` field. Resolution order:
//
//  1. `pkg/buildinfo.Version` if it has been injected at build time
//     (i.e. != the documented "dev" sentinel). This is the canonical
//     source of truth and matches what GET /api/v1/health reports;
//     keeping these two endpoints byte-equivalent on a -X-injected
//     binary is what prevents the "status says v0.4.2, health says
//     v0.4.3" cross-endpoint drift class observed on BLR1 between
//     the v0.4.2 release-cut (which set the systemd `version.conf`
//     env var) and the v0.4.3 binary swap (which injected
//     `buildinfo.Version=v0.4.3` but left the env var stale).
//
//  2. `QSDM_BUILD_VERSION` environment variable. Operator escape
//     hatch retained for backwards compatibility with the systemd
//     `version.conf` drop-in that pre-dated buildinfo's adoption
//     in /api/v1/*, and for dev builds where the operator wants a
//     labelled version without rebuilding with `-X`.
//
//  3. `QSDMPLUS_BUILD_VERSION` environment variable. Legacy alias
//     from the Major Update §6 dual-emit secret-rebrand convention.
//
//  4. `runtime.Version()` (e.g. "go1.25.12"). Last-resort fallback
//     reached only when neither `-X` nor any env var was set. This
//     value is the Go toolchain version, NOT the QSDM release
//     version — it is a deliberately ugly fallback so operators
//     reading the response can tell at a glance that the build
//     pipeline forgot to inject a real version.
func statusVersion() string {
	if buildinfo.Version != "dev" {
		return buildinfo.Version
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_BUILD_VERSION")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("QSDMPLUS_BUILD_VERSION")); v != "" {
		return v
	}
	return runtime.Version()
}
