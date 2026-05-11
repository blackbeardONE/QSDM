// Package preflight implements the startup-time check that the
// QSDM CPU reference miners (cmd/qsdmminer and cmd/qsdmminer-console)
// run before entering their mining loop. The check fetches the
// validator's public /api/v1/status endpoint, parses the `mining`
// block (pkg/api.MiningInfo), and compares the validator's stated
// posture against the miner's chosen protocol version.
//
// The motivation is a real, observed failure mode: an operator who
// follows pre-v2 documentation will launch `qsdmminer --validator=…`
// against api.qsdm.tech (which advertises FORK_V2_HEIGHT=0), spend
// CPU cycles solving valid v1 proofs, and get every single submission
// rejected at the verifier with ReasonBadVersion — with no obvious
// signal at the miner side that the chain has moved on. The preflight
// is the explicit refusal that prevents that wasted-cycle scenario.
//
// The check is implemented as an HTTP GET with a short timeout. It is
// purposefully fail-OPEN on probe failure (network error, malformed
// JSON, missing `mining` block) — the v1 binaries were shipped before
// /api/v1/status had a `mining` block, so blocking on the field's
// absence would lock out anybody running against an older validator
// or against a strict-firewall environment that blocks /status. The
// caller is encouraged to surface the probe error to the operator so
// the degraded path is at least visible in stderr.
//
// The function returns a *Result whose Decision field is one of:
//
//   DecisionProceedV1   — validator is v1-only OR posture unknown
//   DecisionProceedV2   — validator is v2-active and the caller
//                          requested v2 (consistent with the chain)
//   DecisionRefuseV1    — validator is v2-active but the caller
//                          requested v1; submitting would burn cycles
//                          on guaranteed-reject proofs
//
// Callers MUST treat DecisionRefuseV1 as fatal unless the operator
// passed an explicit override flag (--allow-v1 / etc). The override
// is intentionally not part of this package — it is a CLI concern.
package preflight

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// statusBlob is the minimal shape this package needs from
// /api/v1/status. We do NOT depend on pkg/api.StatusResponse because:
//
//  1. It would create an import cycle (pkg/mining/preflight →
//     pkg/api → pkg/mining).
//  2. The miner only needs three fields out of the much-larger
//     /api/v1/status response; depending on the full shape would
//     make the miner brittle to any future status-schema change.
type statusBlob struct {
	ChainTip uint64       `json:"chain_tip"`
	Network  string       `json:"network"`
	Mining   *miningBlock `json:"mining"`
}

type miningBlock struct {
	ProtocolVersionsAccepted []uint32 `json:"protocol_versions_accepted"`
	ForkV2Height             uint64   `json:"fork_v2_height"`
	ForkV2Active             bool     `json:"fork_v2_active"`
	ForkV2TCActive           bool     `json:"fork_v2_tc_active"`
	AttestationTypesRequired []string `json:"attestation_types_required"`
	MinEnrollStakeDust       uint64   `json:"min_enroll_stake_dust"`
}

// Decision is the recommendation the caller (a miner binary) acts on.
type Decision int

const (
	// DecisionProceedV1 means the validator does not require v2
	// proofs at this height. A v1 miner may safely submit.
	DecisionProceedV1 Decision = iota

	// DecisionProceedV2 means the validator requires v2 proofs and
	// the caller is configured for v2. Submission is on-protocol.
	DecisionProceedV2

	// DecisionRefuseV1 means the validator requires v2 proofs but
	// the caller is configured for v1. Submitting would burn CPU
	// solving proofs that the verifier rejects with
	// ReasonBadVersion. The miner SHOULD refuse to enter its loop.
	DecisionRefuseV1
)

func (d Decision) String() string {
	switch d {
	case DecisionProceedV1:
		return "proceed-v1"
	case DecisionProceedV2:
		return "proceed-v2"
	case DecisionRefuseV1:
		return "refuse-v1"
	}
	return fmt.Sprintf("decision(%d)", int(d))
}

// Result is the parsed-and-evaluated outcome of a single probe.
type Result struct {
	Decision Decision

	// ValidatorReachable is true iff the probe got back HTTP 200
	// with a parseable JSON body. False on every network / decode
	// failure path.
	ValidatorReachable bool

	// HasMiningBlock is true iff the validator's /api/v1/status
	// response included the `mining` field added in v0.3.2. Older
	// validators return false here; the caller should print an
	// "older validator, proceeding without a posture check" line
	// rather than blocking.
	HasMiningBlock bool

	// ChainTip is the height the validator reported, useful for
	// logging.
	ChainTip uint64

	// Network is the network label (e.g. "QSDM · CELL") for
	// log lines.
	Network string

	// ForkV2Active is the validator's self-report of whether v2 is
	// consensus-active at the current tip. Source of truth for the
	// Decision.
	ForkV2Active bool

	// AttestationTypesRequired is the whitelist the validator
	// returns when v2 is active. Empty otherwise. Surfaced to the
	// operator so they can tell which path (CC / HMAC) they need
	// to enroll under.
	AttestationTypesRequired []string

	// MinEnrollStakeDust is the bonded stake the validator requires
	// for an nvidia-hmac-v1 enrollment. 0 on v1-only validators.
	MinEnrollStakeDust uint64

	// ProbeErr carries the underlying error from the HTTP / JSON
	// step, or nil on a clean probe. Surfaced rather than wrapped
	// so callers can `errors.Is` against context.Canceled etc.
	ProbeErr error
}

// Check fetches /api/v1/status from validatorURL and evaluates the
// posture against `claimingV2` (true if the caller intends to submit
// v2 proofs, false for v1 / qsdmminer).
//
// The function NEVER returns an error. The (*Result).Decision +
// (*Result).ProbeErr fields convey both the recommendation and any
// underlying probe failure; the caller is expected to format an
// operator-readable summary using FormatDecision below.
//
// validatorURL may include or omit a trailing slash; the function
// trims it once. http://host:port/api/v1 is also accepted (the
// trailing /api/v1 segment is preserved, then /status is appended).
//
// The HTTP client is the caller's; tests inject a stub.
func Check(ctx context.Context, client *http.Client, validatorURL string, claimingV2 bool) *Result {
	res := &Result{}
	url := buildStatusURL(validatorURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		// Malformed URL is the caller's bug; surface it but allow
		// the run to proceed if the operator insists (the runLoop
		// will fail on its own /api/v1/mining/work GET anyway).
		res.ProbeErr = fmt.Errorf("preflight: build request: %w", err)
		res.Decision = DecisionProceedV1
		return res
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		res.ProbeErr = fmt.Errorf("preflight: GET %s: %w", url, err)
		res.Decision = decisionWhenProbeFailed(claimingV2)
		return res
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		res.ProbeErr = fmt.Errorf("preflight: GET %s: HTTP %d %s", url, resp.StatusCode, resp.Status)
		res.Decision = decisionWhenProbeFailed(claimingV2)
		return res
	}

	var blob statusBlob
	if err := json.NewDecoder(resp.Body).Decode(&blob); err != nil {
		res.ProbeErr = fmt.Errorf("preflight: decode JSON from %s: %w", url, err)
		res.Decision = decisionWhenProbeFailed(claimingV2)
		return res
	}

	res.ValidatorReachable = true
	res.ChainTip = blob.ChainTip
	res.Network = blob.Network

	if blob.Mining == nil {
		// Older validator (pre-v0.3.2) — no posture available.
		// Fall back to proceed-v1 with a probe warning; the
		// caller surfaces this to stderr so the operator
		// understands they're running against an old node.
		res.HasMiningBlock = false
		res.Decision = decisionWhenPostureUnknown(claimingV2)
		return res
	}

	res.HasMiningBlock = true
	res.ForkV2Active = blob.Mining.ForkV2Active
	res.AttestationTypesRequired = blob.Mining.AttestationTypesRequired
	res.MinEnrollStakeDust = blob.Mining.MinEnrollStakeDust

	switch {
	case blob.Mining.ForkV2Active && claimingV2:
		res.Decision = DecisionProceedV2
	case blob.Mining.ForkV2Active && !claimingV2:
		res.Decision = DecisionRefuseV1
	case !blob.Mining.ForkV2Active && claimingV2:
		// Caller wants v2 but the validator is still on v1.
		// We still allow it through — the v2 protocol code in
		// pkg/mining/v2client will check its own preconditions
		// (a v2 proof against a v1 validator is just a proof
		// the v1 verifier rejects with ReasonBadVersion for
		// the OPPOSITE reason — not our problem to short-
		// circuit at this layer).
		res.Decision = DecisionProceedV1 // proceed; v2 caller will see its own rejections
	default:
		res.Decision = DecisionProceedV1
	}
	return res
}

// decisionWhenProbeFailed picks a safe default when the network probe
// itself failed (TCP, TLS, HTTP, JSON). For a v1 caller we proceed —
// the most common cause is a misconfigured --validator URL, and the
// runLoop will fail more informatively when it tries to GET
// /api/v1/mining/work. For a v2 caller we also proceed because the
// v2 client has its own enrollment / freshness checks that will
// surface validator misconfiguration with much more context than
// /api/v1/status would.
func decisionWhenProbeFailed(claimingV2 bool) Decision {
	if claimingV2 {
		return DecisionProceedV2
	}
	return DecisionProceedV1
}

// decisionWhenPostureUnknown is invoked when the probe succeeded but
// the response predates the `mining` block. Treated identically to a
// network-level failure for now.
func decisionWhenPostureUnknown(claimingV2 bool) Decision {
	return decisionWhenProbeFailed(claimingV2)
}

// FormatDecision returns a one-paragraph operator-facing description
// of a probe result, suitable for printing to stderr at startup. The
// goal is "you can read this in 3 seconds and know whether you have
// a problem".
func FormatDecision(r *Result, override bool) string {
	if r == nil {
		return "[preflight] internal error: nil result"
	}
	var sb strings.Builder
	switch r.Decision {
	case DecisionProceedV2:
		fmt.Fprintf(&sb,
			"[preflight] validator %s is v2-active at tip=%d. Proceeding with v2 protocol.",
			r.Network, r.ChainTip)
	case DecisionProceedV1:
		if !r.ValidatorReachable {
			fmt.Fprintf(&sb,
				"[preflight] could not probe validator (%v). Proceeding anyway; the runLoop will surface a more specific error if the validator is misconfigured.",
				r.ProbeErr)
		} else if !r.HasMiningBlock {
			fmt.Fprintf(&sb,
				"[preflight] validator %s does not advertise a `mining` block in /api/v1/status (likely pre-v0.3.2). Proceeding without a posture check.",
				r.Network)
		} else {
			fmt.Fprintf(&sb,
				"[preflight] validator %s is v1 at tip=%d (v2 fork not yet active). Proceeding with v1 protocol.",
				r.Network, r.ChainTip)
		}
	case DecisionRefuseV1:
		fmt.Fprintf(&sb,
			"[preflight] REFUSING TO MINE: validator %s reports the v2 NVIDIA-locked fork is ACTIVE at tip=%d. v1 proofs are rejected at the verifier with ReasonBadVersion. Every CPU cycle you burn here is wasted.\n",
			r.Network, r.ChainTip)
		fmt.Fprintf(&sb,
			"[preflight]   accepted protocol versions: %v\n",
			[]uint32{2})
		if len(r.AttestationTypesRequired) > 0 {
			fmt.Fprintf(&sb,
				"[preflight]   required attestation types: %s\n",
				strings.Join(r.AttestationTypesRequired, ", "))
		}
		if r.MinEnrollStakeDust > 0 {
			fmt.Fprintf(&sb,
				"[preflight]   minimum enrollment stake: %d dust (%s CELL)\n",
				r.MinEnrollStakeDust, formatDustAsCell(r.MinEnrollStakeDust))
		}
		fmt.Fprintf(&sb,
			"[preflight]   To mine on this chain, switch to qsdmminer-console --protocol=v2 with an enrolled NVIDIA GPU. See QSDM/docs/docs/MINER_QUICKSTART.md.")
		if override {
			fmt.Fprintf(&sb,
				"\n[preflight] WARNING: --allow-v1 override set. Continuing with v1 anyway. All submitted proofs WILL be rejected.")
		}
	}
	return sb.String()
}

// buildStatusURL composes the /api/v1/status URL from a user-supplied
// validator base. It accepts the four shapes operators tend to type:
//
//   http://host:8080
//   http://host:8080/
//   http://host:8080/api/v1
//   http://host:8080/api/v1/
//
// and converts all of them to http://host:8080/api/v1/status.
func buildStatusURL(base string) string {
	b := strings.TrimRight(base, "/")
	if strings.HasSuffix(b, "/api/v1") {
		return b + "/status"
	}
	return b + "/api/v1/status"
}

// formatDustAsCell mirrors pkg/api.formatDustAsCell but is kept local
// to avoid the import cycle that would result from depending on
// pkg/api here.
func formatDustAsCell(dust uint64) string {
	const dustPerCell uint64 = 100_000_000
	whole := dust / dustPerCell
	frac := dust % dustPerCell
	if frac == 0 {
		return fmt.Sprintf("%d", whole)
	}
	return fmt.Sprintf("%d.%08d", whole, frac)
}

// ErrRefused is the sentinel a CLI caller MAY return from main() when
// FormatDecision printed a refuse-v1 banner, the operator did not
// pass --allow-v1, and the binary is about to exit non-zero. Provided
// so the two miners stay structurally similar; using errors.Is at the
// top of main() is cleaner than a magic string.
var ErrRefused = errors.New("preflight: refusing to mine v1 against v2-active validator")
