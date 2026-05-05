package monitoring

// Smart-contract execution and atomic-swap bridge observability.
//
// Until this commit, both pkg/contracts (ContractEngine) and
// pkg/bridge (BridgeProtocol) had ZERO Prometheus
// instrumentation. A wedged WASM runtime, a flood of
// gas-exhaustion failures, or a cross-chain bridge stuck on
// invalid-secret rejects were all log-only.
//
// Two metric surfaces:
//
//   qsdm_contract_executions_total{result="success|error"}
//     — counter, push-incremented from ContractEngine.ExecuteContract.
//       Surfaces system-vs-execution outcome regardless of which
//       backend served the call (per-contract wazero, shared
//       wazero, wasmSDK, or simulation fallback).
//
//   qsdm_bridge_op_total{op="lock|redeem|refund", result="success|error"}
//     — counter, push-incremented from BridgeProtocol's three
//       state-changing methods. Distinguishes the atomic-swap
//       lifecycle stage so an operator can tell a "redemption
//       flood" (invalid-secret spam) apart from a "refund
//       flood" (post-expiry recovery surge).
//
// All result rows are pre-populated at 0 so the alert
// expressions evaluate against a defined time series rather
// than missing-data on cold-start nodes.

import "sync/atomic"

// Contract execution result labels.
const (
	ContractExecResultSuccess = "success"
	ContractExecResultError   = "error"
)

var (
	contractExecSuccess atomic.Uint64
	contractExecError   atomic.Uint64
)

// RecordContractExecution increments
// qsdm_contract_executions_total{result=result}. Unknown result
// values are silently dropped.
func RecordContractExecution(result string) {
	switch result {
	case ContractExecResultSuccess:
		contractExecSuccess.Add(1)
	case ContractExecResultError:
		contractExecError.Add(1)
	}
}

// ContractExecutionCounts returns (success, error). Exposed for
// tests and admin /api/metrics introspection.
func ContractExecutionCounts() (success, errCount uint64) {
	return contractExecSuccess.Load(), contractExecError.Load()
}

// Bridge op + result labels. The op enumeration matches the
// three state-changing public methods on BridgeProtocol.
const (
	BridgeOpLock   = "lock"
	BridgeOpRedeem = "redeem"
	BridgeOpRefund = "refund"

	BridgeOpResultSuccess = "success"
	BridgeOpResultError   = "error"
)

var (
	bridgeOpLockSuccess     atomic.Uint64
	bridgeOpLockError       atomic.Uint64
	bridgeOpRedeemSuccess   atomic.Uint64
	bridgeOpRedeemError     atomic.Uint64
	bridgeOpRefundSuccess   atomic.Uint64
	bridgeOpRefundError     atomic.Uint64
)

// RecordBridgeOp increments
// qsdm_bridge_op_total{op=op,result=result}. Unknown op or
// result values are silently dropped.
func RecordBridgeOp(op, result string) {
	switch op {
	case BridgeOpLock:
		switch result {
		case BridgeOpResultSuccess:
			bridgeOpLockSuccess.Add(1)
		case BridgeOpResultError:
			bridgeOpLockError.Add(1)
		}
	case BridgeOpRedeem:
		switch result {
		case BridgeOpResultSuccess:
			bridgeOpRedeemSuccess.Add(1)
		case BridgeOpResultError:
			bridgeOpRedeemError.Add(1)
		}
	case BridgeOpRefund:
		switch result {
		case BridgeOpResultSuccess:
			bridgeOpRefundSuccess.Add(1)
		case BridgeOpResultError:
			bridgeOpRefundError.Add(1)
		}
	}
}

// BridgeOpCounts returns (op, result, count) tuples — always
// 6 rows (3 ops × 2 results) so cold-start nodes don't have
// missing-data on alert evaluation.
func BridgeOpCounts() []struct {
	Op     string
	Result string
	Count  uint64
} {
	return []struct {
		Op     string
		Result string
		Count  uint64
	}{
		{BridgeOpLock, BridgeOpResultSuccess, bridgeOpLockSuccess.Load()},
		{BridgeOpLock, BridgeOpResultError, bridgeOpLockError.Load()},
		{BridgeOpRedeem, BridgeOpResultSuccess, bridgeOpRedeemSuccess.Load()},
		{BridgeOpRedeem, BridgeOpResultError, bridgeOpRedeemError.Load()},
		{BridgeOpRefund, BridgeOpResultSuccess, bridgeOpRefundSuccess.Load()},
		{BridgeOpRefund, BridgeOpResultError, bridgeOpRefundError.Load()},
	}
}

// contractsBridgePrometheusMetrics is the collector hook
// registered with the global scrape exporter. Emits both
// surfaces (8 rows total: 2 contract result rows + 6 bridge
// op-result rows).
func contractsBridgePrometheusMetrics() []Metric {
	out := make([]Metric, 0, 8)

	cSucc, cErr := ContractExecutionCounts()
	out = append(out,
		Metric{
			Name:   "qsdm_contract_executions_total",
			Help:   "ContractEngine.ExecuteContract terminal outcomes by result. result=\"success\" includes both real WASM execution and the simulation fallback path; result=\"error\" includes function-not-found, gas exhaustion, runtime panics, and ABI mismatches. Drill by other metrics if a sub-classification is needed.",
			Type:   MetricCounter,
			Value:  float64(cSucc),
			Labels: map[string]string{"result": ContractExecResultSuccess},
		},
		Metric{
			Name:   "qsdm_contract_executions_total",
			Help:   "ContractEngine.ExecuteContract terminal outcomes by result. A high error rate sustained over 15m+ indicates either a runtime regression, a misconfigured gas envelope, or systematic ABI drift; see CONTRACTS_BRIDGE_INCIDENT.md §3.1.",
			Type:   MetricCounter,
			Value:  float64(cErr),
			Labels: map[string]string{"result": ContractExecResultError},
		},
	)

	for _, p := range BridgeOpCounts() {
		out = append(out, Metric{
			Name:   "qsdm_bridge_op_total",
			Help:   "BridgeProtocol atomic-swap operations by op and result. op ∈ {lock, redeem, refund}; result ∈ {success, error}. A redeem-error burst indicates invalid-secret spam (or a target-chain proof failure); a refund-error burst indicates post-expiry recovery is failing. See CONTRACTS_BRIDGE_INCIDENT.md §3.2.",
			Type:   MetricCounter,
			Value:  float64(p.Count),
			Labels: map[string]string{"op": p.Op, "result": p.Result},
		})
	}

	return out
}
