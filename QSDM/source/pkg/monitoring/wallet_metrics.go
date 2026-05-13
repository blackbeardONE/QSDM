package monitoring

// Wallet HTTP API counters: instrument the four state-changing
// endpoints under /api/v1/wallet/* (create, send, mint, balance).
// Per-result labels let alerts split "request error rate" from
// "downstream-system unavailable" so the on-call can tell a user
// problem from a node problem.
//
// The `qsdm_p2p_wallet_ingress_dedupe_skip_total` and
// `qsdm_submesh_api_wallet_reject_*_total` counters live in
// p2p_ingress_dedupe_metrics.go and submesh_metrics.go
// respectively — they're the *gate-side* rejects on the wallet
// surface and complement the per-endpoint counters here.
//
// Why per-endpoint and not a single qsdm_wallet_request_total
// with `endpoint` and `result` labels? Because the endpoints have
// different operational semantics — send is the hot path,
// mint is admin-only and a sustained burst is suspicious in
// itself, balance is read-only and any failure means storage
// is wedged, create is rare. Alerts fire on different
// thresholds per endpoint, so per-endpoint counters keep the
// alerting expressions simple.

import "sync/atomic"

// ---------- /api/v1/wallet/send ----------

var (
	walletSendSuccess          atomic.Uint64 // 201 created, tx stored, optional broadcast
	walletSendInvalidRequest   atomic.Uint64 // 400: validation failure (recipient/amount/fee/geotag/parents)
	walletSendUnauthenticated  atomic.Uint64 // 401: missing/invalid Claims (legacy /wallet/send) or signature invalid (/wallet/submit-signed)
	walletSendNvidiaLockBlocked atomic.Uint64 // 403: NVIDIA-lock gate denied (no qualifying NGC proof)
	walletSendNoWalletService  atomic.Uint64 // 503: wallet service not initialized
	walletSendTxCreateFailed   atomic.Uint64 // 500: WalletService.CreateTransaction error
	walletSendStoreFailed      atomic.Uint64 // 500: storage.StoreTransaction error
	// v0.4.0 (Session 95) — terminal outcomes specific to the
	// self-custody POST /api/v1/wallet/submit-signed handler. They
	// share the `qsdm_wallet_send_total` family because they're
	// semantically "a transaction got sent", just with a different
	// signer-identity model.
	walletSendSenderMismatch     atomic.Uint64 // 400: envelope.sender != hex(sha256(envelope.public_key))
	walletSendSignatureInvalid   atomic.Uint64 // 422: ML-DSA-87 signature does not verify under envelope.public_key
	walletSendInsufficientBalance atomic.Uint64 // 402: storage.GetBalance(sender) < amount + fee
	walletSendDuplicate          atomic.Uint64 // 409: tx_id already stored (idempotent retry)
	// v0.4.1 (Session 99) — replay-protection result tags. See
	// QSDM/docs/docs/V041_REPLAY_PROTECTION_DESIGN.md.
	walletSendNonceReplay        atomic.Uint64 // 409: envelope.nonce <= stored last-seen for sender
	walletSendNonceConflict      atomic.Uint64 // 409: atomic-debit CAS rejected on concurrent submit
	walletSendNonceLookupFailed  atomic.Uint64 // 500: storage.GetNonce error (DB unavailable)
)

// Result tags for qsdm_wallet_send_total.
const (
	WalletSendResultSuccess           = "success"
	WalletSendResultInvalidRequest    = "invalid_request"
	WalletSendResultUnauthenticated   = "unauthenticated"
	WalletSendResultNvidiaLockBlocked = "nvidia_lock_blocked"
	WalletSendResultNoWalletService   = "no_wallet_service"
	WalletSendResultTxCreateFailed    = "tx_create_failed"
	WalletSendResultStoreFailed       = "store_failed"
	// v0.4.0 — added for /api/v1/wallet/submit-signed (Session 95).
	WalletSendResultSenderMismatch      = "sender_mismatch"
	WalletSendResultSignatureInvalid    = "signature_invalid"
	WalletSendResultInsufficientBalance = "insufficient_balance"
	WalletSendResultDuplicate           = "duplicate"
	// v0.4.1 — added for replay protection (Session 99). See
	// QSDM/docs/docs/V041_REPLAY_PROTECTION_DESIGN.md.
	WalletSendResultNonceReplay       = "nonce_replay"
	WalletSendResultNonceConflict     = "nonce_conflict"
	WalletSendResultNonceLookupFailed = "nonce_lookup_failed"
)

// RecordWalletSend increments qsdm_wallet_send_total{result=...} by 1.
// The submesh-policy and dedupe rejects are NOT counted here —
// they live in their own dedicated counters
// (qsdm_submesh_api_wallet_reject_*_total,
// qsdm_p2p_wallet_ingress_dedupe_skip_total) so an operator can
// disambiguate "wallet handler logic rejected" from "policy
// gates rejected" at a glance.
func RecordWalletSend(result string) {
	switch result {
	case WalletSendResultSuccess:
		walletSendSuccess.Add(1)
	case WalletSendResultInvalidRequest:
		walletSendInvalidRequest.Add(1)
	case WalletSendResultUnauthenticated:
		walletSendUnauthenticated.Add(1)
	case WalletSendResultNvidiaLockBlocked:
		walletSendNvidiaLockBlocked.Add(1)
	case WalletSendResultNoWalletService:
		walletSendNoWalletService.Add(1)
	case WalletSendResultTxCreateFailed:
		walletSendTxCreateFailed.Add(1)
	case WalletSendResultStoreFailed:
		walletSendStoreFailed.Add(1)
	case WalletSendResultSenderMismatch:
		walletSendSenderMismatch.Add(1)
	case WalletSendResultSignatureInvalid:
		walletSendSignatureInvalid.Add(1)
	case WalletSendResultInsufficientBalance:
		walletSendInsufficientBalance.Add(1)
	case WalletSendResultDuplicate:
		walletSendDuplicate.Add(1)
	case WalletSendResultNonceReplay:
		walletSendNonceReplay.Add(1)
	case WalletSendResultNonceConflict:
		walletSendNonceConflict.Add(1)
	case WalletSendResultNonceLookupFailed:
		walletSendNonceLookupFailed.Add(1)
	}
}

// WalletSendCounts returns (result, count) tuples for prometheus exposition.
func WalletSendCounts() []struct {
	Result string
	Count  uint64
} {
	return []struct {
		Result string
		Count  uint64
	}{
		{WalletSendResultSuccess, walletSendSuccess.Load()},
		{WalletSendResultInvalidRequest, walletSendInvalidRequest.Load()},
		{WalletSendResultUnauthenticated, walletSendUnauthenticated.Load()},
		{WalletSendResultNvidiaLockBlocked, walletSendNvidiaLockBlocked.Load()},
		{WalletSendResultNoWalletService, walletSendNoWalletService.Load()},
		{WalletSendResultTxCreateFailed, walletSendTxCreateFailed.Load()},
		{WalletSendResultStoreFailed, walletSendStoreFailed.Load()},
		{WalletSendResultSenderMismatch, walletSendSenderMismatch.Load()},
		{WalletSendResultSignatureInvalid, walletSendSignatureInvalid.Load()},
		{WalletSendResultInsufficientBalance, walletSendInsufficientBalance.Load()},
		{WalletSendResultDuplicate, walletSendDuplicate.Load()},
		{WalletSendResultNonceReplay, walletSendNonceReplay.Load()},
		{WalletSendResultNonceConflict, walletSendNonceConflict.Load()},
		{WalletSendResultNonceLookupFailed, walletSendNonceLookupFailed.Load()},
	}
}

// ---------- /api/v1/wallet/balance ----------

var (
	walletBalanceQuerySuccess        atomic.Uint64 // 200: balance returned
	walletBalanceQueryStorageError   atomic.Uint64 // 500: storage read error
	walletBalanceQueryNoWalletService atomic.Uint64 // 503: wallet service not initialized
)

// Result tags for qsdm_wallet_balance_query_total.
const (
	WalletBalanceResultSuccess         = "success"
	WalletBalanceResultStorageError    = "storage_error"
	WalletBalanceResultNoWalletService = "no_wallet_service"
)

// RecordWalletBalanceQuery increments qsdm_wallet_balance_query_total{result=...}.
func RecordWalletBalanceQuery(result string) {
	switch result {
	case WalletBalanceResultSuccess:
		walletBalanceQuerySuccess.Add(1)
	case WalletBalanceResultStorageError:
		walletBalanceQueryStorageError.Add(1)
	case WalletBalanceResultNoWalletService:
		walletBalanceQueryNoWalletService.Add(1)
	}
}

// WalletBalanceCounts returns (result, count) for prometheus exposition.
func WalletBalanceCounts() []struct {
	Result string
	Count  uint64
} {
	return []struct {
		Result string
		Count  uint64
	}{
		{WalletBalanceResultSuccess, walletBalanceQuerySuccess.Load()},
		{WalletBalanceResultStorageError, walletBalanceQueryStorageError.Load()},
		{WalletBalanceResultNoWalletService, walletBalanceQueryNoWalletService.Load()},
	}
}

// ---------- /api/v1/wallet/mint ----------
//
// HISTORY: in v0.3.2 and earlier the endpoint had four real
// outcomes (success / admin_rejected / invalid_request / store_failed
// / no_wallet_service). Removed in v0.3.3 (Session 91) — the
// endpoint always returns 410 Gone, so only `gone` increments on a
// live operator node. The other tags are retained on the exposition
// surface so dashboards / alerts that watch them keep evaluating
// against present time-series instead of missing-data (legacy
// nodes that haven't upgraded to v0.3.3 yet still emit them).

var (
	walletMintSuccess           atomic.Uint64 // PRE-v0.3.3: 201: mint admitted (now never increments)
	walletMintAdminRejected     atomic.Uint64 // PRE-v0.3.3: 403: NVIDIA-lock or submesh policy reject (now never increments)
	walletMintInvalidRequest    atomic.Uint64 // PRE-v0.3.3: 400: validation failure (now never increments)
	walletMintStoreFailed       atomic.Uint64 // PRE-v0.3.3: 500: storage write error (now never increments)
	walletMintNoWalletService   atomic.Uint64 // PRE-v0.3.3: 503: wallet service not initialized (now never increments)
	walletMintGone              atomic.Uint64 // v0.3.3+: 410: endpoint removed; every call lands here
)

// Result tags for qsdm_wallet_mint_total.
const (
	WalletMintResultSuccess          = "success"
	WalletMintResultAdminRejected    = "admin_rejected"
	WalletMintResultInvalidRequest   = "invalid_request"
	WalletMintResultStoreFailed      = "store_failed"
	WalletMintResultNoWalletService  = "no_wallet_service"
	// WalletMintResultGone is the only result that increments on a
	// v0.3.3+ node — every POST /api/v1/wallet/mint returns 410
	// Gone with a structured migration message. Operators that see
	// this counter rising have callers (game servers, scripts) that
	// still target the removed endpoint and need to be migrated to
	// `/api/v1/wallet/send` (peer transfer) or `/api/v1/tokens/mint`
	// (named token mint).
	WalletMintResultGone             = "gone"
)

// RecordWalletMint increments qsdm_wallet_mint_total{result=...}.
func RecordWalletMint(result string) {
	switch result {
	case WalletMintResultSuccess:
		walletMintSuccess.Add(1)
	case WalletMintResultAdminRejected:
		walletMintAdminRejected.Add(1)
	case WalletMintResultInvalidRequest:
		walletMintInvalidRequest.Add(1)
	case WalletMintResultStoreFailed:
		walletMintStoreFailed.Add(1)
	case WalletMintResultNoWalletService:
		walletMintNoWalletService.Add(1)
	case WalletMintResultGone:
		walletMintGone.Add(1)
	}
}

// WalletMintCounts returns (result, count) for prometheus exposition.
func WalletMintCounts() []struct {
	Result string
	Count  uint64
} {
	return []struct {
		Result string
		Count  uint64
	}{
		{WalletMintResultSuccess, walletMintSuccess.Load()},
		{WalletMintResultAdminRejected, walletMintAdminRejected.Load()},
		{WalletMintResultInvalidRequest, walletMintInvalidRequest.Load()},
		{WalletMintResultStoreFailed, walletMintStoreFailed.Load()},
		{WalletMintResultNoWalletService, walletMintNoWalletService.Load()},
		{WalletMintResultGone, walletMintGone.Load()},
	}
}

// ---------- /api/v1/wallet/create ----------

var (
	walletCreateSuccess atomic.Uint64
	walletCreateFailed  atomic.Uint64 // CGO/liboqs init error, OS RNG read error
)

// Result tags for qsdm_wallet_create_total.
const (
	WalletCreateResultSuccess = "success"
	WalletCreateResultFailed  = "failed"
)

// RecordWalletCreate increments qsdm_wallet_create_total{result=...}.
func RecordWalletCreate(result string) {
	switch result {
	case WalletCreateResultSuccess:
		walletCreateSuccess.Add(1)
	case WalletCreateResultFailed:
		walletCreateFailed.Add(1)
	}
}

// WalletCreateCounts returns (result, count) for prometheus exposition.
func WalletCreateCounts() []struct {
	Result string
	Count  uint64
} {
	return []struct {
		Result string
		Count  uint64
	}{
		{WalletCreateResultSuccess, walletCreateSuccess.Load()},
		{WalletCreateResultFailed, walletCreateFailed.Load()},
	}
}

// walletPrometheusMetrics is the collector hook registered with
// the global scrape exporter. Emits qsdm_wallet_*_total counters
// with `result` labels, one row per (endpoint, result) pair.
//
// Exposition contract: per-result rows always exist (so an alert
// expression like `rate(qsdm_wallet_send_total{result="store_failed"}[5m]) > 0`
// evaluates against a populated time series rather than missing-
// data on cold-start nodes that haven't received traffic yet).
func walletPrometheusMetrics() []Metric {
	out := make([]Metric, 0, 18)

	for _, p := range WalletSendCounts() {
		out = append(out, Metric{
			Name: "qsdm_wallet_send_total",
			Help: "Terminal outcomes for POST /api/v1/wallet/send (validator-signed) and POST /api/v1/wallet/submit-signed (self-custody, v0.4.0). Per-result tags: success / invalid_request / unauthenticated / nvidia_lock_blocked / no_wallet_service / tx_create_failed / store_failed / sender_mismatch / signature_invalid / insufficient_balance / duplicate. The sender_mismatch + signature_invalid + insufficient_balance + duplicate tags increment only on the submit-signed path. Submesh-policy and dedupe rejects live in their own dedicated counters (qsdm_submesh_api_wallet_reject_*_total and qsdm_p2p_wallet_ingress_dedupe_skip_total).",
			Type: MetricCounter,
			Value: float64(p.Count),
			Labels: map[string]string{"result": p.Result},
		})
	}

	for _, p := range WalletBalanceCounts() {
		out = append(out, Metric{
			Name: "qsdm_wallet_balance_query_total",
			Help: "GET /api/v1/wallet/balance terminal outcomes by per-result tag (success / storage_error / no_wallet_service). storage_error climbing is a strong storage-backend health signal.",
			Type: MetricCounter,
			Value: float64(p.Count),
			Labels: map[string]string{"result": p.Result},
		})
	}

	for _, p := range WalletMintCounts() {
		out = append(out, Metric{
			Name: "qsdm_wallet_mint_total",
			Help: "POST /api/v1/wallet/mint terminal outcomes by per-result tag. Removed in v0.3.3 (Session 91): the endpoint now always returns 410 Gone, so on a v0.3.3+ node only the `gone` tag increments. Legacy tags (success / admin_rejected / invalid_request / store_failed / no_wallet_service) are retained on the exposition surface so older WALLET_INCIDENT.md alerts (Mode C: QSDMWalletMintBurst) keep evaluating against present time-series. A rising `gone` count indicates a misconfigured caller (game server, script) that should be migrated to /api/v1/wallet/send or /api/v1/tokens/mint.",
			Type: MetricCounter,
			Value: float64(p.Count),
			Labels: map[string]string{"result": p.Result},
		})
	}

	for _, p := range WalletCreateCounts() {
		out = append(out, Metric{
			Name: "qsdm_wallet_create_total",
			Help: "POST /api/v1/wallet/create terminal outcomes by per-result tag (success / failed). failed climbing on a CGO build is a liboqs/OpenSSL init failure signal.",
			Type: MetricCounter,
			Value: float64(p.Count),
			Labels: map[string]string{"result": p.Result},
		})
	}

	return out
}
