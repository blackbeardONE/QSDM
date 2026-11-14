package chain

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func withCustomTaskPayHeight(t *testing.T, h uint64) {
	t.Helper()
	prev := customTaskPayFrom.Load()
	customTaskPayFrom.Store(h)
	t.Cleanup(func() { customTaskPayFrom.Store(prev) })
}

// ctpTx wraps a task action as the economic tx the applier sees.
func ctpTx(t *testing.T, action TaskAction) *mempool.Tx {
	t.Helper()
	aj, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	return &mempool.Tx{ID: action.ID, Sender: action.Sender, Nonce: action.Nonce, ContractID: TaskContractID, Payload: aj}
}

func ctpSubmit(t *testing.T, id string, nonce, round uint64, reward float64, ts time.Time) TaskAction {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"round": round, "submission_value": "v" + strings.Repeat("0", 63) + id, "reward_amount": reward})
	if err != nil {
		t.Fatal(err)
	}
	return TaskAction{ID: id, Sender: rssStaker, TaskID: rssCustomTask, Action: "submit", Payload: string(raw),
		Nonce: nonce, Timestamp: ts.Format(time.RFC3339Nano)}
}

// ctpStore: an active custom task, funded by its manager with 1 CELL, on which
// a staker holds the minimum stake.
func ctpStore(t *testing.T) (*TaskStateStore, *AccountStore, time.Time) {
	t.Helper()
	now := time.Now().UTC()
	store := NewTaskStateStore()
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
		{ID: "stake-custom", Sender: rssStaker, TaskID: rssCustomTask, Action: "stake", Amount: 1, Timestamp: ts},
		{ID: "fund-custom", Sender: rssManager, TaskID: rssCustomTask, Action: "fund", Amount: 1, Timestamp: ts},
	}); err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(rssStaker, 1)
	return store, accounts, now
}

// Below the height a custom task books the asked reward, as before; from the
// height a submit asking for a reward is refused and changes nothing, while a
// zero-reward submit is recorded and reward booked below the height is still
// claimable.
func TestCustomTaskPayRule(t *testing.T) {
	const h = 2_000
	withCustomTaskPayHeight(t, h)
	store, accounts, now := ctpStore(t)

	if err := store.ApplyEconomicTxAtHeight(ctpTx(t, ctpSubmit(t, "below", 0, 1, 0.05, now.Add(time.Second))), accounts, h-1); err != nil {
		t.Fatalf("below the height: %v", err)
	}
	st, _ := store.GetTask(rssCustomTask)
	if taskAmountLessThan(st.PendingRewardAmount, 0.05) || taskAmountLessThan(0.95+1e-9, st.RewardPoolAmount) {
		t.Fatalf("below the height the submission should book 0.05 from the pool: %+v", st)
	}

	rootBefore, acctBefore := store.StateRoot(), accounts.StateRoot()
	err := store.ApplyEconomicTxAtHeight(ctpTx(t, ctpSubmit(t, "at", 1, 2, 0.05, now.Add(2*time.Second))), accounts, h)
	if !errors.Is(err, ErrCustomTaskUnverifiedPay) {
		t.Fatalf("at the height: %v, want ErrCustomTaskUnverifiedPay", err)
	}
	if store.StateRoot() != rootBefore || accounts.StateRoot() != acctBefore {
		t.Fatal("a refused submit changed state")
	}
	tiny := ctpSubmit(t, "tiny", 1, 3, 0.00000001, now.Add(3*time.Second))
	if err := store.ApplyEconomicTxAtHeight(ctpTx(t, tiny), accounts, h+500); !errors.Is(err, ErrCustomTaskUnverifiedPay) {
		t.Fatalf("one dust of reward above the height: %v", err)
	}

	if err := store.ApplyEconomicTxAtHeight(ctpTx(t, ctpSubmit(t, "zero", 1, 4, 0, now.Add(4*time.Second))), accounts, h); err != nil {
		t.Fatalf("a zero-reward submit at the height: %v", err)
	}
	st, _ = store.GetTask(rssCustomTask)
	if math.Abs(st.PendingRewardAmount-0.05) > 1e-9 {
		t.Fatalf("the zero-reward submit moved reward: pending %.8f, want 0.05", st.PendingRewardAmount)
	}

	balBefore := ctpBalance(accounts, rssStaker)
	claim := TaskAction{ID: "claim", Sender: rssStaker, TaskID: rssCustomTask, Action: "claim", Payload: `{"round":0}`,
		Nonce: 2, Timestamp: now.Add(5 * time.Second).Format(time.RFC3339Nano)}
	if err := store.ApplyEconomicTxAtHeight(ctpTx(t, claim), accounts, h+1); err != nil {
		t.Fatalf("claiming the reward booked below the height: %v", err)
	}
	if got := ctpBalance(accounts, rssStaker) - balBefore; got < 0.05-1e-6 {
		t.Fatalf("claim paid %.8f, want about 0.05 (less the fee)", got)
	}
}

func ctpBalance(a *AccountStore, addr string) float64 {
	acc, ok := a.Get(addr)
	if !ok || acc == nil {
		return 0
	}
	return acc.Balance
}

// The producer's admission refuses a paid custom submit at once, whatever the
// tip; zero-reward submits, other actions and the built-in resource tasks
// (which have their own rules) are not this rule's business.
func TestCustomTaskPayAdmission(t *testing.T) {
	withCustomTaskPayHeight(t, 1_190_000)
	now := time.Now().UTC()
	var bp *BlockProducer
	before := CustomTaskPayAdmissionRejected()
	if err := bp.CheckEdgeRelayOnlyAdmission(ctpTx(t, ctpSubmit(t, "paid", 0, 1, 0.05, now))); !errors.Is(err, ErrCustomTaskUnverifiedPay) {
		t.Fatalf("paid custom submit: %v", err)
	}
	if CustomTaskPayAdmissionRejected() != before+1 {
		t.Fatal("admission refusal not counted")
	}
	if err := bp.CheckEdgeRelayOnlyAdmission(ctpTx(t, ctpSubmit(t, "free", 0, 2, 0, now))); err != nil {
		t.Fatalf("zero-reward custom submit: %v", err)
	}
	stake := TaskAction{ID: "stake", Sender: rssStaker, TaskID: rssCustomTask, Action: "stake", Amount: 1, Timestamp: now.Format(time.RFC3339Nano)}
	if err := bp.CheckEdgeRelayOnlyAdmission(ctpTx(t, stake)); err != nil {
		t.Fatalf("stake on a custom task: %v", err)
	}
	if err := checkCustomTaskPaySubmit("qsdm-edge-worker", 0.05); err != nil {
		t.Fatalf("built-in resource task: %v", err)
	}
	if err := checkCustomTaskPaySubmit("QSDM-Edge-Worker", 0.05); !errors.Is(err, ErrCustomTaskUnverifiedPay) {
		t.Fatalf("a look-alike of a built-in task ID is a custom task: %v", err)
	}
	withCustomTaskPayHeight(t, 0)
	if err := bp.CheckEdgeRelayOnlyAdmission(ctpTx(t, ctpSubmit(t, "unscheduled", 0, 3, 0.05, now))); err != nil {
		t.Fatalf("with the rule unscheduled admission must not refuse: %v", err)
	}
}

// The non-resource built-ins follow the rule too, and while inactive they
// refuse every submit anyway.
func TestCustomTaskPayNonResourceBuiltins(t *testing.T) {
	for _, id := range []string{"qsdm-hive-local-task", "qsdm-skyfang-wallet-link"} {
		if IsSystemResourceTask(id) {
			t.Fatalf("%s is not a resource task", id)
		}
		if err := checkCustomTaskPaySubmit(id, 0.01); !errors.Is(err, ErrCustomTaskUnverifiedPay) {
			t.Fatalf("%s: %v", id, err)
		}
	}
}
