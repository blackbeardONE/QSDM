package monitoring

// Storage backend per-operation, per-result counters.
//
// The pre-existing `RecordStorageOperation` in metrics.go is kept
// for backwards compatibility (its fields drive the GetStorageStats
// JSON map exposed under /api/metrics, and the SQLite GetBalance
// path already calls it). What was missing was a Prometheus-side
// surface for those metrics, AND any instrumentation on the
// hot-write path: pkg/storage/sqlite.go's StoreTransaction did not
// call any monitoring hook at all, so a write failure was log-only.
//
// This file fills that gap with a thin per-(op, result) counter
// model that surfaces directly in the OpenMetrics scrape:
//
//   qsdm_storage_op_total{op="store_transaction", result="success|error"}
//   qsdm_storage_op_total{op="get_balance",       result="success|error"}
//   qsdm_storage_op_total{op="update_balance",    result="success|error"}
//   qsdm_storage_op_total{op="set_balance",       result="success|error"}
//   qsdm_storage_op_total{op="ready",             result="success|error"}
//
// The op-set is fixed at compile time (no map-of-counters) so the
// scrape always emits one row per (op, result) pair, populated at
// 0 from process start. Alert expressions like
// `rate(qsdm_storage_op_total{op="store_transaction",result="error"}[5m]) > 0`
// evaluate against a defined time series rather than missing-data
// on cold-start nodes.

import "sync/atomic"

const (
	StorageOpStoreTransaction = "store_transaction"
	StorageOpGetBalance       = "get_balance"
	StorageOpUpdateBalance    = "update_balance"
	StorageOpSetBalance       = "set_balance"
	StorageOpReady            = "ready"

	StorageOpResultSuccess = "success"
	StorageOpResultError   = "error"
)

var (
	storageOpStoreTransactionSuccess atomic.Uint64
	storageOpStoreTransactionError   atomic.Uint64
	storageOpGetBalanceSuccess       atomic.Uint64
	storageOpGetBalanceError         atomic.Uint64
	storageOpUpdateBalanceSuccess    atomic.Uint64
	storageOpUpdateBalanceError      atomic.Uint64
	storageOpSetBalanceSuccess       atomic.Uint64
	storageOpSetBalanceError         atomic.Uint64
	storageOpReadySuccess            atomic.Uint64
	storageOpReadyError              atomic.Uint64
)

// RecordStorageOp increments qsdm_storage_op_total{op=op,result=result}.
// Unknown (op, result) pairs are silently dropped — defensive
// guard so a future op enumeration drift doesn't panic.
func RecordStorageOp(op, result string) {
	switch op {
	case StorageOpStoreTransaction:
		switch result {
		case StorageOpResultSuccess:
			storageOpStoreTransactionSuccess.Add(1)
		case StorageOpResultError:
			storageOpStoreTransactionError.Add(1)
		}
	case StorageOpGetBalance:
		switch result {
		case StorageOpResultSuccess:
			storageOpGetBalanceSuccess.Add(1)
		case StorageOpResultError:
			storageOpGetBalanceError.Add(1)
		}
	case StorageOpUpdateBalance:
		switch result {
		case StorageOpResultSuccess:
			storageOpUpdateBalanceSuccess.Add(1)
		case StorageOpResultError:
			storageOpUpdateBalanceError.Add(1)
		}
	case StorageOpSetBalance:
		switch result {
		case StorageOpResultSuccess:
			storageOpSetBalanceSuccess.Add(1)
		case StorageOpResultError:
			storageOpSetBalanceError.Add(1)
		}
	case StorageOpReady:
		switch result {
		case StorageOpResultSuccess:
			storageOpReadySuccess.Add(1)
		case StorageOpResultError:
			storageOpReadyError.Add(1)
		}
	}
}

// StorageOpCounts returns (op, result, count) tuples for prometheus
// exposition. Always returns 10 rows (5 ops × 2 results) so the
// time series is fully populated from the first scrape onward.
func StorageOpCounts() []struct {
	Op     string
	Result string
	Count  uint64
} {
	return []struct {
		Op     string
		Result string
		Count  uint64
	}{
		{StorageOpStoreTransaction, StorageOpResultSuccess, storageOpStoreTransactionSuccess.Load()},
		{StorageOpStoreTransaction, StorageOpResultError, storageOpStoreTransactionError.Load()},
		{StorageOpGetBalance, StorageOpResultSuccess, storageOpGetBalanceSuccess.Load()},
		{StorageOpGetBalance, StorageOpResultError, storageOpGetBalanceError.Load()},
		{StorageOpUpdateBalance, StorageOpResultSuccess, storageOpUpdateBalanceSuccess.Load()},
		{StorageOpUpdateBalance, StorageOpResultError, storageOpUpdateBalanceError.Load()},
		{StorageOpSetBalance, StorageOpResultSuccess, storageOpSetBalanceSuccess.Load()},
		{StorageOpSetBalance, StorageOpResultError, storageOpSetBalanceError.Load()},
		{StorageOpReady, StorageOpResultSuccess, storageOpReadySuccess.Load()},
		{StorageOpReady, StorageOpResultError, storageOpReadyError.Load()},
	}
}

// storageOpPrometheusMetrics is the collector hook registered with
// the global scrape exporter. Emits qsdm_storage_op_total per
// (op, result) pair.
func storageOpPrometheusMetrics() []Metric {
	out := make([]Metric, 0, 10)
	for _, p := range StorageOpCounts() {
		out = append(out, Metric{
			Name: "qsdm_storage_op_total",
			Help: "Storage backend operations by op and terminal result. op ∈ {store_transaction, get_balance, update_balance, set_balance, ready}; result ∈ {success, error}. Surfaces the SQLite/file/Scylla backend's per-call outcomes; complements the wallet-side counters in qsdm_wallet_*_total. See STORAGE_INCIDENT.md for triage.",
			Type: MetricCounter,
			Value: float64(p.Count),
			Labels: map[string]string{"op": p.Op, "result": p.Result},
		})
	}
	return out
}
