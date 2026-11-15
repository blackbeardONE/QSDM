package chain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const (
	rssContributor = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rssMother      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	rssManager     = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	rssStaker      = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	rssCustomTask  = "rt-custom-pool"
)

func withRelaySettlementScopeHeight(t *testing.T, h uint64) {
	t.Helper()
	prev := relaySettlementScopeFrom.Load()
	relaySettlementScopeFrom.Store(h)
	t.Cleanup(func() { relaySettlementScopeFrom.Store(prev) })
}

// rssCustomSubmit is a submit to the custom task, by the staker, whose
// payload is the given map (a settlement envelope or anything else).
func rssCustomSubmit(t *testing.T, id string, nonce uint64, payload map[string]any, ts time.Time) *mempool.Tx {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	action := TaskAction{ID: id, Sender: rssStaker, TaskID: rssCustomTask, Action: "submit", Payload: string(raw),
		Nonce: nonce, Timestamp: ts.Format(time.RFC3339Nano)}
	aj, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	return &mempool.Tx{ID: id, Sender: rssStaker, Nonce: nonce, ContractID: TaskContractID, Payload: aj}
}

// rssSettledStore settles one real Relay proof on qsdm-edge-worker at height
// 121 and sets up an active, third-party-funded custom task the staker has
// staked on.
func rssSettledStore(t *testing.T) (*TaskStateStore, *AccountStore, edgepool.PoolProof, time.Time) {
	t.Helper()
	now := time.Now().UTC()
	proof, _ := signedPooledProof(t, rssContributor, rssMother, []string{strings.Repeat("1", 64), strings.Repeat("2", 64)}, now)
	store := NewTaskStateStore()
	registerPooledTask(t, store, "qsdm-edge-worker", proof.CoordinatorID, now)
	manifest := TaskManifest{SchemaVersion: TaskCatalogSchemaVersion, TaskID: rssCustomTask, Version: 1, Name: "Custom pool",
		Manager: rssManager, Active: true, MinimumStakeAmount: 1, RewardPerRound: 0.05, RoundTime: 60,
		Runtime: TaskRuntimeManifest{Kind: "capability", Capability: "generic-proof-v1"}}
	mj, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	ts := now.Format(time.RFC3339Nano)
	if err := store.ApplyActions([]TaskAction{
		{ID: "register-custom", Sender: rssManager, TaskID: rssCustomTask, Action: TaskActionCatalogRegister, Payload: string(mj), Timestamp: ts},
		{ID: "stake-edge", Sender: rssMother, TaskID: "qsdm-edge-worker", Action: "stake", Amount: 1, Timestamp: ts},
		{ID: "fund-edge", Sender: "sponsor", TaskID: "qsdm-edge-worker", Action: "fund", Amount: 1, Timestamp: ts},
		{ID: "stake-custom", Sender: rssStaker, TaskID: rssCustomTask, Action: "stake", Amount: 1, Timestamp: ts},
		{ID: "fund-custom", Sender: rssManager, TaskID: rssCustomTask, Action: "fund", Amount: 1, Timestamp: ts},
	}); err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(rssMother, 1)
	accounts.Credit(rssStaker, 1)
	if err := store.ApplyEconomicTxAtHeight(pooledSubmitTx(t, "settle-1", 0, 2, 120, proof, 0.05, now.Add(time.Second)), accounts, 121); err != nil {
		t.Fatal(err)
	}
	return store, accounts, proof, now
}

func rssEnvelope(round uint64, source string, proof any) map[string]any {
	return map[string]any{"round": round, "slot": round * 60, "source": source, "resource": "cpu",
		"submission_value": "x" + strings.Repeat("0", 63), "reward_amount": 0.05, "proof": proof}
}

// Below the height a settled proof applies on a custom task as produced;
// from the height it is refused there, under its own source and under any
// other, while ordinary custom submissions and fresh settlements still pay.
func TestRelaySettlementScope(t *testing.T) {
	const h = 1_000
	withRelaySettlementScopeHeight(t, h)
	store, accounts, proof, now := rssSettledStore(t)

	// Below the height: unchanged behaviour (a plain submission on the custom task).
	below := rssCustomSubmit(t, "below", 0, rssEnvelope(10, edgepool.SettlementProofSource, proof), now.Add(2*time.Second))
	if err := store.ApplyEconomicTxAtHeight(below, accounts, h-1); err != nil {
		t.Fatalf("below the height: %v", err)
	}
	if st, _ := store.GetTask(rssCustomTask); taskAmountLessThan(st.PendingRewardAmount, 0.05) {
		t.Fatalf("below the height the submission should reserve the reward: %+v", st)
	}

	onlyReceipt := map[string]any{"algorithm": "x", "units": 1, "digest": strings.Repeat("a", 64), "receipt_ids": []string{strings.Repeat("2", 64)}}
	onlyProofID := map[string]any{"algorithm": "x", "units": 1, "digest": strings.Repeat("b", 64), "proof_id": strings.ToUpper(proof.ProofID)}
	for i, tc := range []struct {
		name    string
		payload map[string]any
		want    error
	}{
		{"settlement envelope", rssEnvelope(11, edgepool.SettlementProofSource, proof), ErrRelaySettlementWrongTask},
		{"settlement envelope, other case", rssEnvelope(12, "QSDM-EDGE-RELAY-V2", proof), ErrRelaySettlementWrongTask},
		{"settled proof under another source", rssEnvelope(13, "anything", proof), ErrRelaySettlementConsumed},
		{"settled proof, empty source", rssEnvelope(14, "", proof), ErrRelaySettlementConsumed},
		{"a settled receipt only", rssEnvelope(15, "local", onlyReceipt), ErrRelaySettlementConsumed},
		{"a settled proof ID only", rssEnvelope(16, "local", onlyProofID), ErrRelaySettlementConsumed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rootBefore, acctBefore := store.StateRoot(), accounts.StateRoot()
			tx := rssCustomSubmit(t, "at-"+tc.name, 1, tc.payload, now.Add(time.Duration(3+i)*time.Second))
			if err := store.ApplyEconomicTxAtHeight(tx, accounts, h); !errors.Is(err, tc.want) {
				t.Fatalf("economic apply at the height returned %v, want %v", err, tc.want)
			}
			if err := store.ChainReplayClone().(*TaskStateStore).ApplyHistoricalTx(tx, h+5); !errors.Is(err, tc.want) {
				t.Fatalf("replay returned %v, want %v", err, tc.want)
			}
			if store.StateRoot() != rootBefore || accounts.StateRoot() != acctBefore {
				t.Fatal("a refused submit changed state")
			}
		})
	}

	// An ordinary custom submission still pays (the custom task's own rules).
	plain := rssCustomSubmit(t, "plain", 1, map[string]any{"round": 20, "submission_value": "bafy-result", "reward_amount": 0.05}, now.Add(20*time.Second))
	if err := store.ApplyEconomicTxAtHeight(plain, accounts, h+1); err != nil {
		t.Fatalf("an ordinary custom submission was refused: %v", err)
	}
}

// A fresh settlement on the built-in resource task is unaffected by the rule.
func TestRelaySettlementScopeLeavesResourceTasksAlone(t *testing.T) {
	withRelaySettlementScopeHeight(t, 100)
	now := time.Now().UTC()
	proof, _ := signedPooledProof(t, rssContributor, rssMother, []string{strings.Repeat("3", 64)}, now)
	store := NewTaskStateStore()
	registerPooledTask(t, store, "qsdm-edge-worker", proof.CoordinatorID, now)
	ts := now.Format(time.RFC3339Nano)
	if err := store.ApplyActions([]TaskAction{
		{ID: "stake", Sender: rssMother, TaskID: "qsdm-edge-worker", Action: "stake", Amount: 1, Timestamp: ts},
		{ID: "fund", Sender: "sponsor", TaskID: "qsdm-edge-worker", Action: "fund", Amount: 1, Timestamp: ts},
	}); err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(rssMother, 1)
	if err := store.ApplyEconomicTxAtHeight(pooledSubmitTx(t, "settle", 0, 2, 120, proof, 0.05, now.Add(time.Second)), accounts, 121); err != nil {
		t.Fatalf("a settlement on the resource task was refused: %v", err)
	}
}

// With no activation height (0) the rule never applies.
func TestRelaySettlementScopeInertAtZero(t *testing.T) {
	withRelaySettlementScopeHeight(t, 0)
	withCustomTaskPayHeight(t, 0) // this test isolates the scope rule (custom_task_pay.go has its own)
	store, accounts, proof, now := rssSettledStore(t)
	tx := rssCustomSubmit(t, "zero", 0, rssEnvelope(30, edgepool.SettlementProofSource, proof), now.Add(2*time.Second))
	if err := store.ApplyEconomicTxAtHeight(tx, accounts, 1<<40); err != nil {
		t.Fatalf("at height 0 nothing changes: %v", err)
	}
	if RelaySettlementScopeActiveAt(1 << 62) {
		t.Fatal("active with no height")
	}
}

// The producer's admission refuses a settlement envelope for another task at
// once and leaves everything else to the other checks.
func TestRelaySettlementScopeAdmission(t *testing.T) {
	withRelaySettlementScopeHeight(t, RelaySettlementScopeActivationHeight)
	withCustomTaskPayHeight(t, 0) // this test isolates the scope rule (custom_task_pay.go has its own)
	now := time.Now().UTC()
	proof, _ := signedPooledProof(t, rssContributor, rssMother, []string{strings.Repeat("4", 64)}, now)
	var bp *BlockProducer
	before := RelaySettlementScopeAdmissionRejected()
	if err := bp.CheckEdgeRelayOnlyAdmission(rssCustomSubmit(t, "adm-1", 0, rssEnvelope(1, edgepool.SettlementProofSource, proof), now)); !errors.Is(err, ErrRelaySettlementWrongTask) {
		t.Fatalf("admission returned %v", err)
	}
	if RelaySettlementScopeAdmissionRejected() != before+1 {
		t.Fatal("the admission refusal was not counted")
	}
	if err := bp.CheckEdgeRelayOnlyAdmission(rssCustomSubmit(t, "adm-2", 0, map[string]any{"round": 1, "submission_value": "v"}, now)); err != nil {
		t.Fatalf("an ordinary custom submission was refused at admission: %v", err)
	}
	if err := bp.CheckEdgeRelayOnlyAdmission(pooledSubmitTx(t, "adm-3", 0, 2, 120, proof, 0.05, now)); err != nil {
		t.Fatalf("a settlement for the resource task was refused at admission: %v", err)
	}
	withRelaySettlementScopeHeight(t, 0)
	if err := bp.CheckEdgeRelayOnlyAdmission(rssCustomSubmit(t, "adm-4", 0, rssEnvelope(1, edgepool.SettlementProofSource, proof), now)); err != nil {
		t.Fatalf("with no height admission must not refuse: %v", err)
	}
}
