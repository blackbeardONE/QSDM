package monitoring

import (
	"strings"
	"testing"
)

func TestContractsBridgePrometheusMetrics_AlwaysIncludesAllRows(t *testing.T) {
	rows := contractsBridgePrometheusMetrics()

	type contractKey struct{ result string }
	type bridgeKey struct{ op, result string }
	contractSeen := map[contractKey]bool{}
	bridgeSeen := map[bridgeKey]bool{}

	for _, m := range rows {
		switch m.Name {
		case "qsdm_contract_executions_total":
			contractSeen[contractKey{m.Labels["result"]}] = true
		case "qsdm_bridge_op_total":
			bridgeSeen[bridgeKey{m.Labels["op"], m.Labels["result"]}] = true
		default:
			t.Fatalf("unexpected metric: %q", m.Name)
		}
	}

	for _, r := range []string{ContractExecResultSuccess, ContractExecResultError} {
		if !contractSeen[contractKey{r}] {
			t.Errorf("missing qsdm_contract_executions_total{result=%q}", r)
		}
	}
	wantOps := []string{BridgeOpLock, BridgeOpRedeem, BridgeOpRefund}
	wantResults := []string{BridgeOpResultSuccess, BridgeOpResultError}
	for _, op := range wantOps {
		for _, r := range wantResults {
			if !bridgeSeen[bridgeKey{op, r}] {
				t.Errorf("missing qsdm_bridge_op_total{op=%q,result=%q}", op, r)
			}
		}
	}
}

func TestRecordContractExecution_ReflectsInExposition(t *testing.T) {
	startSucc, startErr := ContractExecutionCounts()

	RecordContractExecution(ContractExecResultSuccess)
	RecordContractExecution(ContractExecResultSuccess)
	RecordContractExecution(ContractExecResultError)
	RecordContractExecution("not_a_real_result") // dropped

	gotSucc, gotErr := ContractExecutionCounts()
	if gotSucc != startSucc+2 {
		t.Errorf("contract success = %d; want %d", gotSucc, startSucc+2)
	}
	if gotErr != startErr+1 {
		t.Errorf("contract error = %d; want %d", gotErr, startErr+1)
	}

	exposition := PrometheusExposition()
	if !strings.Contains(exposition, `qsdm_contract_executions_total{result="success"}`) {
		t.Error("exposition missing contract success row")
	}
	if !strings.Contains(exposition, `qsdm_contract_executions_total{result="error"}`) {
		t.Error("exposition missing contract error row")
	}
}

func TestRecordBridgeOp_AllOps(t *testing.T) {
	startCounts := map[string]map[string]uint64{}
	for _, p := range BridgeOpCounts() {
		if _, ok := startCounts[p.Op]; !ok {
			startCounts[p.Op] = map[string]uint64{}
		}
		startCounts[p.Op][p.Result] = p.Count
	}

	RecordBridgeOp(BridgeOpLock, BridgeOpResultSuccess)
	RecordBridgeOp(BridgeOpRedeem, BridgeOpResultError)
	RecordBridgeOp(BridgeOpRefund, BridgeOpResultSuccess)
	RecordBridgeOp(BridgeOpRefund, BridgeOpResultError)
	RecordBridgeOp("not_a_real_op", BridgeOpResultSuccess) // dropped

	endCounts := map[string]map[string]uint64{}
	for _, p := range BridgeOpCounts() {
		if _, ok := endCounts[p.Op]; !ok {
			endCounts[p.Op] = map[string]uint64{}
		}
		endCounts[p.Op][p.Result] = p.Count
	}

	checks := []struct {
		op, result string
		delta      uint64
	}{
		{BridgeOpLock, BridgeOpResultSuccess, 1},
		{BridgeOpRedeem, BridgeOpResultError, 1},
		{BridgeOpRefund, BridgeOpResultSuccess, 1},
		{BridgeOpRefund, BridgeOpResultError, 1},
	}
	for _, c := range checks {
		got := endCounts[c.op][c.result] - startCounts[c.op][c.result]
		if got != c.delta {
			t.Errorf("op=%s result=%s delta = %d; want %d", c.op, c.result, got, c.delta)
		}
	}
}
