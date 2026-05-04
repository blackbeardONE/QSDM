package monitoring

import (
	"strings"
	"testing"
)

// TestWalletPrometheusMetrics_AlwaysIncludesAllResultLabels verifies
// the bridge from the per-result counters → qsdm_wallet_*_total
// emits a row per (endpoint, result) pair. Critical for alerting
// — a query like `rate(qsdm_wallet_send_total{result="store_failed"}[5m]) > 0`
// would otherwise have a bootstrap missing-data problem on a
// cold-start node that hasn't yet received any /api/v1/wallet/send
// traffic.
func TestWalletPrometheusMetrics_AlwaysIncludesAllResultLabels(t *testing.T) {
	got := walletPrometheusMetrics()
	bySendResult := map[string]bool{}
	byBalanceResult := map[string]bool{}
	byMintResult := map[string]bool{}
	byCreateResult := map[string]bool{}
	for _, m := range got {
		switch m.Name {
		case "qsdm_wallet_send_total":
			bySendResult[m.Labels["result"]] = true
		case "qsdm_wallet_balance_query_total":
			byBalanceResult[m.Labels["result"]] = true
		case "qsdm_wallet_mint_total":
			byMintResult[m.Labels["result"]] = true
		case "qsdm_wallet_create_total":
			byCreateResult[m.Labels["result"]] = true
		default:
			t.Errorf("unexpected metric %q", m.Name)
		}
	}

	wantSend := []string{
		WalletSendResultSuccess,
		WalletSendResultInvalidRequest,
		WalletSendResultUnauthenticated,
		WalletSendResultNvidiaLockBlocked,
		WalletSendResultNoWalletService,
		WalletSendResultTxCreateFailed,
		WalletSendResultStoreFailed,
	}
	for _, r := range wantSend {
		if !bySendResult[r] {
			t.Errorf("qsdm_wallet_send_total missing result=%q", r)
		}
	}

	wantBalance := []string{
		WalletBalanceResultSuccess,
		WalletBalanceResultStorageError,
		WalletBalanceResultNoWalletService,
	}
	for _, r := range wantBalance {
		if !byBalanceResult[r] {
			t.Errorf("qsdm_wallet_balance_query_total missing result=%q", r)
		}
	}

	wantMint := []string{
		WalletMintResultSuccess,
		WalletMintResultAdminRejected,
		WalletMintResultInvalidRequest,
		WalletMintResultStoreFailed,
		WalletMintResultNoWalletService,
	}
	for _, r := range wantMint {
		if !byMintResult[r] {
			t.Errorf("qsdm_wallet_mint_total missing result=%q", r)
		}
	}

	wantCreate := []string{
		WalletCreateResultSuccess,
		WalletCreateResultFailed,
	}
	for _, r := range wantCreate {
		if !byCreateResult[r] {
			t.Errorf("qsdm_wallet_create_total missing result=%q", r)
		}
	}
}

// TestRecordWalletSend_ReflectsInExposition records a few
// terminal outcomes and verifies they surface in the actual
// /api/metrics/prometheus output.
func TestRecordWalletSend_ReflectsInExposition(t *testing.T) {
	// Capture starting values so the test is robust to other
	// tests in the package having recorded sends already.
	startStore := walletSendStoreFailed.Load()
	startSuccess := walletSendSuccess.Load()

	RecordWalletSend(WalletSendResultStoreFailed)
	RecordWalletSend(WalletSendResultStoreFailed)
	RecordWalletSend(WalletSendResultSuccess)

	// Direct counter check (no reset between tests in this package).
	if got := walletSendStoreFailed.Load(); got != startStore+2 {
		t.Errorf("walletSendStoreFailed=%d; want %d", got, startStore+2)
	}
	if got := walletSendSuccess.Load(); got != startSuccess+1 {
		t.Errorf("walletSendSuccess=%d; want %d", got, startSuccess+1)
	}

	// Exposition smoke check: the per-result rows are present.
	exposition := PrometheusExposition()
	if !strings.Contains(exposition, `qsdm_wallet_send_total{result="store_failed"}`) {
		t.Error("PrometheusExposition() missing qsdm_wallet_send_total{result=\"store_failed\"} row")
	}
	if !strings.Contains(exposition, `qsdm_wallet_send_total{result="success"}`) {
		t.Error("PrometheusExposition() missing qsdm_wallet_send_total{result=\"success\"} row")
	}
}

// TestRecordWalletXxx_UnknownResult is a no-op (defensive: an
// invalid result tag must not panic and must not corrupt
// existing counters).
func TestRecordWalletXxx_UnknownResult(t *testing.T) {
	startSendSuccess := walletSendSuccess.Load()
	RecordWalletSend("definitely_not_a_known_tag")
	if got := walletSendSuccess.Load(); got != startSendSuccess {
		t.Errorf("unknown tag mutated walletSendSuccess from %d to %d", startSendSuccess, got)
	}

	// Same for balance / mint / create.
	startBal := walletBalanceQuerySuccess.Load()
	RecordWalletBalanceQuery("nope")
	if got := walletBalanceQuerySuccess.Load(); got != startBal {
		t.Errorf("unknown tag mutated walletBalanceQuerySuccess")
	}

	startMint := walletMintSuccess.Load()
	RecordWalletMint("nope")
	if got := walletMintSuccess.Load(); got != startMint {
		t.Errorf("unknown tag mutated walletMintSuccess")
	}

	startCreate := walletCreateSuccess.Load()
	RecordWalletCreate("nope")
	if got := walletCreateSuccess.Load(); got != startCreate {
		t.Errorf("unknown tag mutated walletCreateSuccess")
	}
}
