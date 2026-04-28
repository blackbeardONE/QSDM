package api

// Governance-parameters READ endpoints (v2 protocol §9.4).
// Companion to the off-chain `qsdm/gov/v1` write path that
// flows through the existing /api/v1/transactions submission
// surface:
//
//	GET /api/v1/governance/params           — full snapshot
//	GET /api/v1/governance/params/{name}    — single-param view
//
// Why two routes:
//
//   - The list endpoint is what dashboards / `qsdmcli watch
//     params` poll. They want one round-trip per cycle and a
//     deterministic ordering so diff engines can pin
//     state by name.
//   - The single-param endpoint is what `qsdmcli gov-helper
//     params --remote --param=NAME` and the off-chain
//     proposal builders hit. Same wire shape, just sliced to
//     one record + 404 on unknown names so client errors stay
//     attributable.
//
// Both endpoints are READ-only; submitting a parameter change
// goes through the same signed-tx envelope as every other
// `qsdm/...` ContractID via /api/v1/transactions, with payload
// constructed by `qsdmcli gov-helper propose-param`.
//
// The endpoint MUST behave gracefully when no provider is
// wired: 503 with a clear "v2 governance not configured on
// this node" message rather than 404 (which would imply the
// query reached the param store and the name was unknown).
// Mirrors handlers_enrollment_query.go's posture exactly.

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
)

// GovernanceParamsProvider is the narrow read-only surface
// the handler depends on. The concrete implementation is a
// thin adapter over chain.GovApplier + chainparams.ParamStore
// installed by internal/v2wiring; the indirection here keeps
// pkg/api free of pkg/chain / pkg/governance/chainparams
// imports (the same boundary discipline the EnrollmentRegistry
// + SlashReceiptStore interfaces enforce in this package).
//
// Snapshot semantics:
//
//   - Returns a self-consistent point-in-time view; reads of
//     active and pending occur under the underlying store's
//     RWMutex, so the caller cannot observe a torn state where
//     a parameter is shown as both "active old value" and
//     "pending new value" mid-promotion.
//   - The slice / map values returned are owned by the caller
//     after the call (the provider returns fresh copies).
//
// AuthorityList may be empty (governance disabled posture);
// the handler surfaces that as `governance_enabled: false` so
// dashboards can render a clear "governance is not configured"
// state rather than guessing from missing fields.
type GovernanceParamsProvider interface {
	// SnapshotGovernanceParams returns the point-in-time view
	// described above.
	SnapshotGovernanceParams() GovernanceParamsView
}

// GovernanceParamsView is the complete wire snapshot returned
// by GET /api/v1/governance/params. Field set:
//
//   - Active: param-name → currently-active value. Includes
//     every entry in the registry (so consumers can render a
//     stable table without consulting a separate registry
//     index). Numbers are uint64, encoded as JSON numbers.
//   - Pending: every staged change, sorted by EffectiveHeight
//     ASC then by Param ASC for deterministic diffability.
//   - Registry: the static registry entries (name, bounds,
//     default, unit, description). Lets a CLI render the table
//     in a single round-trip.
//   - Authorities: the configured AuthorityList, sorted ASC.
//     Empty when governance is disabled.
//   - GovernanceEnabled: false iff Authorities is empty. Kept
//     as a separate field for explicitness; clients SHOULD
//     check this before treating Pending entries as actionable.
//
// Bumping or reordering fields here is a wire-format change.
type GovernanceParamsView struct {
	Active            map[string]uint64        `json:"active"`
	Pending           []GovernancePendingView  `json:"pending"`
	Registry          []GovernanceRegistryView `json:"registry"`
	Authorities       []string                 `json:"authorities"`
	GovernanceEnabled bool                     `json:"governance_enabled"`
}

// GovernancePendingView is one staged param change. Mirrors
// chainparams.ParamChange but lives here so pkg/api stays
// import-light. JSON tags are the wire contract.
type GovernancePendingView struct {
	Param             string `json:"param"`
	Value             uint64 `json:"value"`
	EffectiveHeight   uint64 `json:"effective_height"`
	SubmittedAtHeight uint64 `json:"submitted_at_height"`
	Authority         string `json:"authority"`
	Memo              string `json:"memo,omitempty"`
}

// GovernanceRegistryView is one entry in the parameter
// registry. Mirrors chainparams.ParamSpec but kept local for
// the same import-discipline reason.
type GovernanceRegistryView struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	MinValue     uint64 `json:"min_value"`
	MaxValue     uint64 `json:"max_value"`
	DefaultValue uint64 `json:"default_value"`
	Unit         string `json:"unit"`
}

// GovernanceParamView is the single-record wire shape returned
// by GET /api/v1/governance/params/{name}. Carries the active
// value, the pending change (if any), and the registry entry
// in one round-trip.
type GovernanceParamView struct {
	Name         string                  `json:"name"`
	ActiveValue  uint64                  `json:"active_value"`
	Pending      *GovernancePendingView  `json:"pending,omitempty"`
	RegistryInfo GovernanceRegistryView  `json:"registry"`
}

// process-wide installer + accessor, parallel to the
// EnrollmentRegistry / SlashReceiptStore holders elsewhere in
// this package.
type governanceProviderHolder struct {
	mu       sync.RWMutex
	provider GovernanceParamsProvider
}

var govProviderHolder = &governanceProviderHolder{}

// SetGovernanceProvider installs (or removes, when p==nil) the
// process-wide governance provider the GET handlers use.
// Validators call this once at startup. Calling again replaces
// the prior provider — process restarts get clean state.
func SetGovernanceProvider(p GovernanceParamsProvider) {
	govProviderHolder.mu.Lock()
	defer govProviderHolder.mu.Unlock()
	govProviderHolder.provider = p
}

func currentGovernanceProvider() GovernanceParamsProvider {
	govProviderHolder.mu.RLock()
	defer govProviderHolder.mu.RUnlock()
	return govProviderHolder.provider
}

// GovernanceParamsHandler serves
// GET /api/v1/governance/params.
//
// 503 when no provider is wired (v1-only nodes, or the
// operator has not yet upgraded to a binary that calls
// SetGovernanceProvider). 200 with a `GovernanceParamsView`
// otherwise. Other methods → 405.
func (h *Handlers) GovernanceParamsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	prov := currentGovernanceProvider()
	if prov == nil {
		writeMiningUnavailable(w, "v2 governance not configured on this node")
		return
	}
	view := prov.SnapshotGovernanceParams()
	// Defensive: ensure deterministic field-presence even if
	// a provider returns nil maps / slices. Empty values are
	// preferable to nil JSON for diff-driven consumers.
	if view.Active == nil {
		view.Active = map[string]uint64{}
	}
	if view.Pending == nil {
		view.Pending = []GovernancePendingView{}
	}
	if view.Registry == nil {
		view.Registry = []GovernanceRegistryView{}
	}
	if view.Authorities == nil {
		view.Authorities = []string{}
	}
	// Pin deterministic ordering at the API boundary even
	// when the provider's ordering convention drifts. Pending
	// is sorted by (EffectiveHeight ASC, Param ASC); Registry
	// + Authorities are sorted ascending.
	sort.SliceStable(view.Pending, func(i, j int) bool {
		if view.Pending[i].EffectiveHeight != view.Pending[j].EffectiveHeight {
			return view.Pending[i].EffectiveHeight < view.Pending[j].EffectiveHeight
		}
		return view.Pending[i].Param < view.Pending[j].Param
	})
	sort.SliceStable(view.Registry, func(i, j int) bool {
		return view.Registry[i].Name < view.Registry[j].Name
	})
	sort.Strings(view.Authorities)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(view)
}

// GovernanceParamHandler serves
// GET /api/v1/governance/params/{name}.
//
// Mounted on the trailing-slash route prefix; we extract the
// name by stripping the prefix off r.URL.Path. Same shape as
// EnrollmentQueryHandler.
//
// 503 when no provider; 400 on missing / over-long name; 404
// when the name is not in the registry; 200 with a
// `GovernanceParamView` otherwise.
func (h *Handlers) GovernanceParamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	prov := currentGovernanceProvider()
	if prov == nil {
		writeMiningUnavailable(w, "v2 governance not configured on this node")
		return
	}

	const prefix = "/api/v1/governance/params/"
	rawName := strings.TrimPrefix(r.URL.Path, prefix)
	rawName = strings.TrimSuffix(rawName, "/")
	if rawName == "" || strings.Contains(rawName, "/") {
		http.Error(w, "param name required as path component", http.StatusBadRequest)
		return
	}
	// 64-byte cap is a safe upper bound for chainparams names
	// (ASCII snake_case, ≤32 bytes by registry rule). Going
	// above the registry cap means the request is junk; reject
	// fast without a registry walk.
	if len(rawName) > 64 {
		http.Error(w, "param name too long", http.StatusBadRequest)
		return
	}

	view := prov.SnapshotGovernanceParams()
	var spec *GovernanceRegistryView
	for i := range view.Registry {
		if view.Registry[i].Name == rawName {
			spec = &view.Registry[i]
			break
		}
	}
	if spec == nil {
		http.Error(w, "no governance parameter with that name", http.StatusNotFound)
		return
	}
	out := GovernanceParamView{
		Name:         rawName,
		ActiveValue:  view.Active[rawName],
		RegistryInfo: *spec,
	}
	for i := range view.Pending {
		if view.Pending[i].Param == rawName {
			pending := view.Pending[i]
			out.Pending = &pending
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}
