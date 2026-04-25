// Package branding holds the canonical public product identity for QSDM.
//
// Background: the project was temporarily known as "QSDM" while the current
// feature enhancements (NVIDIA NGC attestation, Scylla storage, WASM contracts,
// Go/JS SDKs) were stabilised. Per the Major Update plan (see
// QSDM/docs/docs/REBRAND_NOTES.md), the product reverts to the clean name
// "QSDM" at the Cell-coin launch. Technical identifiers (Go module path
// github.com/blackbeardONE/QSDM, legacy environment variables QSDM_*,
// legacy HTTP headers X-QSDM-*, legacy JSON field qsdm_node_id,
// and legacy directories cmd/qsdm, sdk/qsdm.*) continue to work
// for one deprecation window; new code and operator docs MUST reference the
// Name / CoinName / CoinSymbol / preferred-header constants defined here.
package branding

const (
	// Name is the official product name as displayed in UIs, API responses,
	// log prefixes, and container image labels.
	Name = "QSDM"

	// LegacyName is the immediately previous product name. It is exported so
	// deprecation shims and REBRAND_NOTES tooling can emit consistent wording
	// without re-hard-coding the string in half a dozen places.
	LegacyName = "QSDM"

	// Tagline is the long-form descriptor shown with the product.
	Tagline = "Quantum-Secure Dynamic Mesh Ledger"

	// LogPrefix is prepended to CLI status lines (e.g. "QSDM: ...").
	LogPrefix = Name + ": "
)

// Native coin identity.
//
// The values below are the "Cell" coin as described in Major Update §4.
// They are treated as canonical by the API, the Go and JavaScript SDKs, and
// the operator dashboard tokenomics panel. Changing any of these constants
// changes user-visible strings; do NOT change them without a corresponding
// update to CELL_TOKENOMICS.md and REBRAND_NOTES.md.
const (
	// CoinName is the full, human-readable coin name.
	CoinName = "Cell"

	// CoinSymbol is the three-to-five-letter ticker used in wallets, block
	// explorers, and DEX listings. All-caps by convention.
	CoinSymbol = "CELL"

	// CoinDecimals is the number of fractional digits the coin supports, i.e.
	// 1 CELL = 10^CoinDecimals of the smallest unit. Eight places matches
	// Bitcoin-style UX and is the value committed in Major Update §4.1.
	CoinDecimals = 8
)

// SmallestUnitName is the human-readable name for the smallest indivisible
// coin unit (1 CELL = 100_000_000 dust). The name is intentionally short
// and phonetic so CLI output ("amount: 12345 dust") reads cleanly.
const SmallestUnitName = "dust"

// FullTitle is Tagline + parenthetical name for logs and banners.
func FullTitle() string {
	return Tagline + " (" + Name + ")"
}

// DashboardTitle is the default browser title for the operator dashboard.
func DashboardTitle() string {
	return Name + " Monitoring Dashboard"
}

// NetworkLabel is what the landing page and dashboard header pill should
// display alongside the product name, e.g. "QSDM · CELL". Keeping this in
// one place means a future renaming of the network (e.g. "QSDM Mainnet" vs
// "QSDM Testnet") only touches this function.
func NetworkLabel() string {
	return Name + " \u00b7 " + CoinSymbol
}

// HTTP header names.
//
// New nodes and new sidecars SHOULD send the "Preferred" variant. The
// "Legacy" variant is accepted for one deprecation window and must continue
// to be recognised server-side so existing deployed sidecars keep working
// through the rebrand. When Major Update Phase 6 ships, the Legacy variants
// may be removed in a breaking release (see REBRAND_NOTES.md for the
// migration window).
const (
	// NGCSecretHeaderPreferred carries the NGC-ingest shared secret used by
	// POST /api/v1/monitoring/ngc-proof.
	NGCSecretHeaderPreferred = "X-QSDM-NGC-Secret"

	// NGCSecretHeaderLegacy is the pre-rebrand name for the same header.
	NGCSecretHeaderLegacy = "X-QSDM-NGC-Secret"

	// MetricsScrapeSecretHeaderPreferred is the optional alternative to
	// Bearer for GET /api/metrics/prometheus.
	MetricsScrapeSecretHeaderPreferred = "X-QSDM-Metrics-Scrape-Secret"

	// MetricsScrapeSecretHeaderLegacy is the pre-rebrand name for the same header.
	MetricsScrapeSecretHeaderLegacy = "X-QSDM-Metrics-Scrape-Secret"
)

// MetricsScrapeSecretHeader remains exported as the PREFERRED name and is
// kept as a plain identifier so existing call sites (dashboard.go line 278,
// 483) compile unchanged. New code should use
// MetricsScrapeSecretHeaderPreferred / ...Legacy explicitly.
const MetricsScrapeSecretHeader = MetricsScrapeSecretHeaderPreferred

// ProofNodeIDFieldPreferred and ProofNodeIDFieldLegacy are the two JSON
// field names accepted inside an NGC proof bundle to bind the proof to a
// specific node. The verifier (pkg/monitoring/nvidia_lock.go) MUST try the
// Preferred name first and fall back to Legacy for backwards compatibility
// with existing sidecar deployments.
const (
	ProofNodeIDFieldPreferred = "qsdm_node_id"
	ProofNodeIDFieldLegacy    = "qsdm_node_id"

	ProofHMACFieldPreferred = "qsdm_proof_hmac"
	ProofHMACFieldLegacy    = "qsdm_proof_hmac"

	ProofIngestNonceFieldPreferred = "qsdm_ingest_nonce"
	ProofIngestNonceFieldLegacy    = "qsdm_ingest_nonce"
)
