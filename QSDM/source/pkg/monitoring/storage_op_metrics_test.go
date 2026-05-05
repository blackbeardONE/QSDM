package monitoring

import (
	"strings"
	"testing"
)

// TestStorageOpMetrics_AlwaysIncludesAllOpResultPairs verifies the
// scrape always emits all 5 ops × 2 results = 10 rows so the
// alerting expression `rate(qsdm_storage_op_total{...,result="error"}[5m]) > 0`
// evaluates against a populated time series rather than missing-
// data on cold-start nodes.
func TestStorageOpMetrics_AlwaysIncludesAllOpResultPairs(t *testing.T) {
	got := storageOpPrometheusMetrics()
	type opRes struct{ op, result string }
	seen := map[opRes]bool{}
	for _, m := range got {
		if m.Name != "qsdm_storage_op_total" {
			t.Fatalf("unexpected metric %q", m.Name)
		}
		seen[opRes{m.Labels["op"], m.Labels["result"]}] = true
	}
	wantOps := []string{
		StorageOpStoreTransaction,
		StorageOpGetBalance,
		StorageOpUpdateBalance,
		StorageOpSetBalance,
		StorageOpReady,
	}
	wantResults := []string{
		StorageOpResultSuccess,
		StorageOpResultError,
	}
	for _, op := range wantOps {
		for _, r := range wantResults {
			if !seen[opRes{op, r}] {
				t.Errorf("missing qsdm_storage_op_total{op=%q,result=%q}", op, r)
			}
		}
	}
}

// TestRecordStorageOp_ReflectsInExposition exercises a few paths
// and verifies the counters surface in /api/metrics/prometheus.
func TestRecordStorageOp_ReflectsInExposition(t *testing.T) {
	startStoreOK := storageOpStoreTransactionSuccess.Load()
	startStoreErr := storageOpStoreTransactionError.Load()
	startReadyOK := storageOpReadySuccess.Load()

	RecordStorageOp(StorageOpStoreTransaction, StorageOpResultSuccess)
	RecordStorageOp(StorageOpStoreTransaction, StorageOpResultSuccess)
	RecordStorageOp(StorageOpStoreTransaction, StorageOpResultError)
	RecordStorageOp(StorageOpReady, StorageOpResultSuccess)

	if got := storageOpStoreTransactionSuccess.Load(); got != startStoreOK+2 {
		t.Errorf("store_transaction success counter = %d; want %d", got, startStoreOK+2)
	}
	if got := storageOpStoreTransactionError.Load(); got != startStoreErr+1 {
		t.Errorf("store_transaction error counter = %d; want %d", got, startStoreErr+1)
	}
	if got := storageOpReadySuccess.Load(); got != startReadyOK+1 {
		t.Errorf("ready success counter = %d; want %d", got, startReadyOK+1)
	}

	exposition := PrometheusExposition()
	if !strings.Contains(exposition, `qsdm_storage_op_total{op="store_transaction",result="success"}`) {
		t.Error("PrometheusExposition() missing the store_transaction success row")
	}
	if !strings.Contains(exposition, `qsdm_storage_op_total{op="store_transaction",result="error"}`) {
		t.Error("PrometheusExposition() missing the store_transaction error row")
	}
}

// TestRecordStorageOp_UnknownOpOrResult is a defensive check that
// invalid (op, result) tuples no-op rather than panicking.
func TestRecordStorageOp_UnknownOpOrResult(t *testing.T) {
	startStoreOK := storageOpStoreTransactionSuccess.Load()
	RecordStorageOp("not_a_real_op", StorageOpResultSuccess)
	RecordStorageOp(StorageOpStoreTransaction, "not_a_real_result")
	if got := storageOpStoreTransactionSuccess.Load(); got != startStoreOK {
		t.Errorf("unknown tags mutated counter from %d to %d", startStoreOK, got)
	}
}
