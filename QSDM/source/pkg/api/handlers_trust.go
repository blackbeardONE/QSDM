package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// Trust & attestation endpoints — implements MINING_PROTOCOL-adjacent
// transparency surface required by Major Update §8.5.{1,3}.
//
// Contract (copied verbatim from §8.5.2 so the endpoints cannot drift):
//
//   - Attestation data NEVER enters consensus or block-validity paths.
//   - The widget/page must always display "X of Y", never just "X".
//   - Zero-opt-in and NGC-outage states are legal responses and MUST be
//     served with 200 — not hidden, not loading-forever.
//   - node_id_prefix is always first-8 + "…" + last-4 chars of the peer
//     ID. The aggregator enforces this redaction before cache insertion.
//   - region_hint is coarse: "eu" / "us" / "apac" / "other".
//
// Both endpoints are public (see middleware.isPublicEndpoint).

// -----------------------------------------------------------------------------
// Wire types (§8.5.3)
// -----------------------------------------------------------------------------

// TrustSummary is the payload of GET /api/v1/trust/attestations/summary.
type TrustSummary struct {
	Attested         int     `json:"attested"`
	TotalPublic      int     `json:"total_public"`
	Ratio            float64 `json:"ratio"`
	FreshWithin      string  `json:"fresh_within"`
	LastAttestedAt   *string `json:"last_attested_at"` // RFC3339 or null
	LastCheckedAt    string  `json:"last_checked_at"`
	NGCServiceStatus string  `json:"ngc_service_status"`
	ScopeNote        string  `json:"scope_note"`
}

// TrustAttestation is one row in GET /api/v1/trust/attestations/recent.
type TrustAttestation struct {
	NodeIDPrefix    string `json:"node_id_prefix"`
	AttestedAt      string `json:"attested_at"` // RFC3339
	FreshAgeSeconds int64  `json:"fresh_age_seconds"`
	GPUArchitecture string `json:"gpu_architecture"`
	GPUAvailable    bool   `json:"gpu_available"`
	NGCHMACOK       bool   `json:"ngc_hmac_ok"`
	RegionHint      string `json:"region_hint"`
}

// TrustRecent is the payload of GET /api/v1/trust/attestations/recent.
type TrustRecent struct {
	FreshWithin  string             `json:"fresh_within"`
	Count        int                `json:"count"`
	Attestations []TrustAttestation `json:"attestations"`
}

// Fixed scope-note string from §8.5.2. Included verbatim in every summary
// response so scrapers cannot strip the caveat without tampering with the
// body.
const trustScopeNote = "NVIDIA-lock is an opt-in, per-operator API policy — not a consensus rule. See NVIDIA_LOCK_CONSENSUS_SCOPE.md."

// Default freshness window (§8.5.3). Humans read it in the summary; the
// aggregator parses it back into a time.Duration.
const defaultFreshWithin = 15 * time.Minute

// Minimum wall-clock time the aggregator must run before it will claim
// to have a scraped view. §8.5.3's 503 "warming up" state.
const trustAggregatorWarmup = 60 * time.Second

// -----------------------------------------------------------------------------
// Data sources (dependency-injected)
// -----------------------------------------------------------------------------

// PeerAttestation is a normalised view of a known public peer and its
// most recent attestation (if any). Cross-peer aggregation is wired by
// the validator daemon through SetTrustPeerProvider; nil provider means
// the aggregator only knows about the local node.
type PeerAttestation struct {
	NodeID          string    // full libp2p peer ID or equivalent
	AttestedAt      time.Time // zero if never
	GPUArchitecture string
	GPUAvailable    bool
	NGCHMACOK       bool
	RegionHint      string // "eu" / "us" / "apac" / "other"
}

// TrustPeerProvider returns the current public-peer snapshot with, for
// each peer, its latest attestation state (if known to this validator).
// Total count includes peers without attestations so the X/Y ratio in
// the summary is correct.
type TrustPeerProvider interface {
	PeerAttestations() []PeerAttestation
}

// LocalAttestationSource exposes the local node's own attestation state.
// The reference implementation wires pkg/monitoring; tests inject fakes.
type LocalAttestationSource interface {
	// LocalLatest returns the most recent local NGC proof's received-at
	// timestamp (UTC) and the extracted attestation fields. ok=false
	// means this node has never received a proof.
	LocalLatest() (PeerAttestation, bool)
	// LocalNodeID returns the full (unredacted) node ID of this node.
	// Empty string means the node has no persistent identity yet; in
	// that case the aggregator substitutes "local" as a placeholder for
	// redaction purposes and never exposes any substring of the node
	// process's ephemeral state.
	LocalNodeID() string
}

// LocalDistinctAttestationSource is an optional extension of
// LocalAttestationSource that exposes every distinct attestation
// source present in the local node's ring buffer, keyed by
// `qsdmplus_node_id`, instead of collapsing all POSTs to
// /api/v1/monitoring/ngc-proof into a single "local" peer row.
//
// When an operator runs multiple CPU-fallback sidecars — e.g. one on
// the main VPS, one on a laptop, one on a secondary cloud VM — each
// with its own QSDMPLUS_NGC_PROOF_NODE_ID, implementing this
// interface lets the trust aggregator count them as distinct
// attestation sources. PeerAttestation entries returned with an
// empty NodeID are folded onto the local node's identity by the
// aggregator so the legacy behaviour is preserved for bundles that
// omit the id field.
//
// Sources that don't implement this interface keep the pre-existing
// LocalLatest() semantics (one peer row for all local proofs). This
// is an optional, duck-typed extension: TrustAggregator uses a
// type assertion and falls back cleanly.
type LocalDistinctAttestationSource interface {
	LocalAttestationSource
	LocalDistinctAttestations() []PeerAttestation
}

// TrustConfig wires dependencies into the trust subsystem. Both
// providers are optional:
//   - If LocalSource is nil, the aggregator reports no local attestation.
//   - If PeerProvider is nil, the aggregator only sees the local node.
//
// A process that wants to opt out of serving the trust API entirely
// leaves TrustDisabled = true; the handlers then return 404 per §8.5.3.
type TrustConfig struct {
	PeerProvider TrustPeerProvider
	LocalSource  LocalAttestationSource
	FreshWithin  time.Duration // zero → defaultFreshWithin
	Clock        func() time.Time
	TrustDisabled bool
}

// -----------------------------------------------------------------------------
// Aggregator (cache + recompute-on-read)
// -----------------------------------------------------------------------------

// TrustAggregator periodically rebuilds the summary and recent lists
// from the data sources. The reference validator kicks off a goroutine
// that calls Refresh every 5–15 s. Handlers always serve the last
// successful refresh so HTTP latency is bounded by cache read time.
type TrustAggregator struct {
	cfg TrustConfig

	mu            sync.RWMutex
	startedAt     time.Time
	lastCheckedAt time.Time
	cached        TrustSummary
	recent        []TrustAttestation
	warm          atomic.Bool
}

// NewTrustAggregator initialises a new aggregator. It does not start
// any goroutines — the caller is responsible for wiring Refresh into a
// ticker if they want freshness guarantees; otherwise the handlers call
// Refresh on demand.
func NewTrustAggregator(cfg TrustConfig) *TrustAggregator {
	if cfg.FreshWithin == 0 {
		cfg.FreshWithin = defaultFreshWithin
	}
	if cfg.Clock == nil {
		cfg.Clock = func() time.Time { return time.Now().UTC() }
	}
	return &TrustAggregator{cfg: cfg, startedAt: cfg.Clock()}
}

// Refresh recomputes the cached summary and recent lists. Safe for
// concurrent call — the last writer wins.
func (a *TrustAggregator) Refresh() {
	now := a.cfg.Clock()
	freshCutoff := now.Add(-a.cfg.FreshWithin)

	peers := []PeerAttestation{}
	if a.cfg.PeerProvider != nil {
		peers = append(peers, a.cfg.PeerProvider.PeerAttestations()...)
	}
	if a.cfg.LocalSource != nil {
		// Prefer the distinct-by-node-id view when the source supports
		// it. Each CPU-fallback sidecar that stamped its own
		// QSDMPLUS_NGC_PROOF_NODE_ID surfaces as a separate peer row
		// instead of all of them collapsing onto the local node's
		// identity (the old LocalLatest() behaviour).
		if ds, ok := a.cfg.LocalSource.(LocalDistinctAttestationSource); ok {
			distinct := ds.LocalDistinctAttestations()
			if len(distinct) > 0 {
				localID := a.cfg.LocalSource.LocalNodeID()
				for _, att := range distinct {
					// Empty-id rows fold onto the local node's identity.
					// This keeps the legacy behaviour for bundles that
					// did not include qsdmplus_node_id.
					if att.NodeID == "" {
						att.NodeID = localID
					}
					if att.NodeID == "" {
						att.NodeID = "local"
					}
					peers = mergePeer(peers, att)
				}
			} else if a.cfg.PeerProvider == nil {
				// No proofs yet and no peer provider — still render
				// "0 of 1" so the widget never shows a bare "0".
				peers = mergePeer(peers, PeerAttestation{NodeID: a.cfg.LocalSource.LocalNodeID()})
			}
		} else if local, ok := a.cfg.LocalSource.LocalLatest(); ok {
			// Legacy single-row path for LocalSources that don't yet
			// implement LocalDistinctAttestationSource.
			if local.NodeID == "" {
				local.NodeID = a.cfg.LocalSource.LocalNodeID()
			}
			if local.NodeID == "" {
				local.NodeID = "local"
			}
			peers = mergePeer(peers, local)
		} else if a.cfg.PeerProvider == nil {
			// If the only source is the (missing) local one, still
			// count this node as a public peer with no attestation so
			// the widget renders "0 of 1".
			peers = mergePeer(peers, PeerAttestation{NodeID: a.cfg.LocalSource.LocalNodeID()})
		}
	}

	totalPublic := len(peers)
	attested := 0
	var lastAttested time.Time
	var lastAttestedAny time.Time // includes stale, for outage detection
	freshRows := make([]TrustAttestation, 0, len(peers))
	for _, p := range peers {
		if !p.AttestedAt.IsZero() && p.AttestedAt.After(lastAttestedAny) {
			lastAttestedAny = p.AttestedAt
		}
		if p.AttestedAt.IsZero() || p.AttestedAt.Before(freshCutoff) {
			continue
		}
		attested++
		if p.AttestedAt.After(lastAttested) {
			lastAttested = p.AttestedAt
		}
		freshRows = append(freshRows, TrustAttestation{
			NodeIDPrefix:    redactNodeID(p.NodeID),
			AttestedAt:      p.AttestedAt.UTC().Format(time.RFC3339),
			FreshAgeSeconds: int64(now.Sub(p.AttestedAt).Seconds()),
			GPUArchitecture: p.GPUArchitecture,
			GPUAvailable:    p.GPUAvailable,
			NGCHMACOK:       p.NGCHMACOK,
			RegionHint:      normaliseRegion(p.RegionHint),
		})
	}
	sort.Slice(freshRows, func(i, j int) bool {
		return freshRows[i].FreshAgeSeconds < freshRows[j].FreshAgeSeconds
	})

	status := classifyNGCStatus(attested, lastAttestedAny, now, a.cfg.FreshWithin)

	// last_attested_at reflects the most recent successful attestation
	// seen by this node, whether or not it is still within the freshness
	// window. This lets dashboards render "last attested 2 hours ago"
	// rather than "never" during a 15-minute outage.
	var lastStr *string
	if !lastAttestedAny.IsZero() {
		s := lastAttestedAny.UTC().Format(time.RFC3339)
		lastStr = &s
	}

	ratio := 0.0
	if totalPublic > 0 {
		ratio = float64(attested) / float64(totalPublic)
	}

	summary := TrustSummary{
		Attested:         attested,
		TotalPublic:      totalPublic,
		Ratio:            ratio,
		FreshWithin:      a.cfg.FreshWithin.String(),
		LastAttestedAt:   lastStr,
		LastCheckedAt:    now.Format(time.RFC3339),
		NGCServiceStatus: status,
		ScopeNote:        trustScopeNote,
	}

	a.mu.Lock()
	a.cached = summary
	a.recent = freshRows
	a.lastCheckedAt = now
	a.mu.Unlock()

	if now.Sub(a.startedAt) >= trustAggregatorWarmup {
		a.warm.Store(true)
	}
}

// Summary returns a copy of the last-computed summary and whether the
// aggregator has completed its warm-up window.
func (a *TrustAggregator) Summary() (TrustSummary, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cached, a.warm.Load()
}

// Recent returns up to `limit` of the freshest attestations sorted newest
// first. `limit` is clamped to [1, 200] per §8.5.3.
func (a *TrustAggregator) Recent(limit int) (TrustRecent, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if limit < 1 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows := a.recent
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return TrustRecent{
		FreshWithin:  a.cfg.FreshWithin.String(),
		Count:        len(rows),
		Attestations: append([]TrustAttestation(nil), rows...),
	}, a.warm.Load()
}

func classifyNGCStatus(attested int, lastAttested time.Time, now time.Time, window time.Duration) string {
	if attested == 0 {
		if lastAttested.IsZero() {
			return "healthy" // zero opt-in; still "healthy" per §8.5.4
		}
		if now.Sub(lastAttested) > window {
			return "outage"
		}
		return "degraded"
	}
	// Has attested peers: pick "healthy" unless the newest is older
	// than half the window (lagging NGC) — maps to the §8.5.4 "degraded" state.
	if now.Sub(lastAttested) > window/2 {
		return "degraded"
	}
	return "healthy"
}

// redactNodeID applies the §8.5.3 redaction rule: first 8 + "…" + last 4
// characters of the node ID. IDs shorter than 12 characters are
// right-padded with "*" to reach 12 so the shape never leaks length.
func redactNodeID(nodeID string) string {
	const prefix = 8
	const suffix = 4
	if len(nodeID) == 0 {
		return "local***…****"
	}
	if len(nodeID) < prefix+suffix {
		padded := nodeID
		for len(padded) < prefix+suffix {
			padded += "*"
		}
		return padded[:prefix] + "…" + padded[prefix:prefix+suffix]
	}
	return nodeID[:prefix] + "…" + nodeID[len(nodeID)-suffix:]
}

// normaliseRegion collapses any caller-provided region string to the
// four-bucket taxonomy from §8.5.3.
func normaliseRegion(r string) string {
	switch r {
	case "eu", "us", "apac":
		return r
	default:
		return "other"
	}
}

// mergePeer appends local into peers unless there's already a record
// with the same NodeID, in which case the newer AttestedAt wins.
func mergePeer(peers []PeerAttestation, p PeerAttestation) []PeerAttestation {
	for i := range peers {
		if peers[i].NodeID == p.NodeID && peers[i].NodeID != "" {
			if p.AttestedAt.After(peers[i].AttestedAt) {
				peers[i] = p
			}
			return peers
		}
	}
	return append(peers, p)
}

// -----------------------------------------------------------------------------
// Handlers and registration
// -----------------------------------------------------------------------------

// trustHolder owns the process-wide aggregator instance.
type trustHolder struct {
	mu        sync.RWMutex
	agg       *TrustAggregator
	disabled  bool
}

var trustSingleton = &trustHolder{}

// SetTrustAggregator installs (or removes) the aggregator. Call with
// disabled=true to expose the opt-out 404 behaviour per §8.5.3.
func SetTrustAggregator(a *TrustAggregator, disabled bool) {
	trustSingleton.mu.Lock()
	defer trustSingleton.mu.Unlock()
	trustSingleton.agg = a
	trustSingleton.disabled = disabled
}

func currentTrustAggregator() (*TrustAggregator, bool) {
	trustSingleton.mu.RLock()
	defer trustSingleton.mu.RUnlock()
	return trustSingleton.agg, trustSingleton.disabled
}

// TrustSummaryHandler serves GET /api/v1/trust/attestations/summary.
func (h *Handlers) TrustSummaryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	agg, disabled := currentTrustAggregator()
	if disabled {
		writeTrustDisabled(w)
		return
	}
	if agg == nil {
		writeTrustWarmup(w)
		return
	}
	agg.Refresh()
	summary, warm := agg.Summary()
	if !warm {
		writeTrustWarmup(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")
	_ = json.NewEncoder(w).Encode(summary)
}

// TrustRecentHandler serves GET /api/v1/trust/attestations/recent?limit=N.
func (h *Handlers) TrustRecentHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	agg, disabled := currentTrustAggregator()
	if disabled {
		writeTrustDisabled(w)
		return
	}
	if agg == nil {
		writeTrustWarmup(w)
		return
	}
	limit := 50
	if ls := r.URL.Query().Get("limit"); ls != "" {
		v, err := strconv.Atoi(ls)
		if err != nil {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		limit = v
	}
	agg.Refresh()
	recent, warm := agg.Recent(limit)
	if !warm {
		writeTrustWarmup(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=5")
	_ = json.NewEncoder(w).Encode(recent)
}

func writeTrustWarmup(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "5")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "trust aggregator warming up"})
}

func writeTrustDisabled(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "trust endpoints disabled on this node"})
}

// -----------------------------------------------------------------------------
// Concrete LocalAttestationSource backed by pkg/monitoring
// -----------------------------------------------------------------------------

// MonitoringLocalSource plugs the production NGC ring buffer
// (pkg/monitoring.NGCProofSummaries) into the trust aggregator. For
// tests, prefer the *InMemoryLocalSource defined in trust_test.go.
type MonitoringLocalSource struct {
	NodeID       string
	RegionHint   string
	ArchOverride string
}

// LocalNodeID returns the injected local node identifier.
func (m *MonitoringLocalSource) LocalNodeID() string { return m.NodeID }

// LocalLatest extracts the newest entry from the NGC ring buffer and
// translates it into a PeerAttestation. It tolerates missing fields in
// the bundle — the aggregator only renders what it can verify.
func (m *MonitoringLocalSource) LocalLatest() (PeerAttestation, bool) {
	rows := monitoring.NGCProofSummaries()
	if len(rows) == 0 {
		return PeerAttestation{}, false
	}
	row := rows[len(rows)-1]
	p := PeerAttestation{
		NodeID:       m.NodeID,
		RegionHint:   m.RegionHint,
		GPUAvailable: true, // presence of a proof implies a GPU was present
		NGCHMACOK:    true, // ingest path validates HMAC before storage in strict mode
	}
	if m.ArchOverride != "" {
		p.GPUArchitecture = m.ArchOverride
	}
	if v, ok := row["timestamp_utc"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			p.AttestedAt = t.UTC()
		}
	}
	if p.AttestedAt.IsZero() {
		if v, ok := row["received_at"].(string); ok {
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				p.AttestedAt = t.UTC()
			}
		}
	}
	if p.AttestedAt.IsZero() {
		return PeerAttestation{}, false
	}
	return p, true
}

// LocalDistinctAttestations returns one PeerAttestation per distinct
// `qsdmplus_node_id` present in the NGC proof ring buffer. Each entry
// reflects the newest bundle seen for that id (newest-wins by the
// bundle's `timestamp_utc`, falling back to the POST wall-clock when
// the bundle omitted the field). Rows whose bundle did not include a
// node id return with an empty NodeID so the aggregator can fold
// them onto the local node's identity.
func (m *MonitoringLocalSource) LocalDistinctAttestations() []PeerAttestation {
	rows := monitoring.NGCProofDistinctByNodeID()
	if len(rows) == 0 {
		return nil
	}
	out := make([]PeerAttestation, 0, len(rows))
	for _, r := range rows {
		at := r.TimestampUTC
		if at.IsZero() {
			at = r.ReceivedAt
		}
		if at.IsZero() {
			continue
		}
		p := PeerAttestation{
			NodeID:       r.NodeID,
			AttestedAt:   at.UTC(),
			RegionHint:   m.RegionHint,
			GPUAvailable: true, // presence of a proof implies a GPU claim
			NGCHMACOK:    true, // ingest path validates HMAC in strict mode
		}
		switch {
		case m.ArchOverride != "":
			p.GPUArchitecture = m.ArchOverride
		case r.GPUArchitecture != "":
			p.GPUArchitecture = r.GPUArchitecture
		}
		out = append(out, p)
	}
	return out
}

// compile-time assertions so interface drift is caught at build time
var (
	_ LocalAttestationSource         = (*MonitoringLocalSource)(nil)
	_ LocalDistinctAttestationSource = (*MonitoringLocalSource)(nil)
)

// humanDuration converts a time.Duration to something that both a human
// and time.ParseDuration can consume. Exposed for tests.
func humanDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int(d/time.Minute))
	}
	return d.String()
}

