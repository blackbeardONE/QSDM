package chain

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// A settlement's receipt ids spelled with surrounding whitespace, or in upper
// case, are not the canonical keys of the replay maps. Below the rc1112 height
// history replays as produced; from the height such ids are refused.
func TestSettlementIDsCanonical(t *testing.T) {
	const (
		contributor = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		mother      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	r1, r2 := strings.Repeat("1", 64), strings.Repeat("2", 64)
	setup := func(t *testing.T) (*TaskStateStore, *AccountStore, time.Time, func(ids []string) *mempool.Tx) {
		now := time.Now().UTC()
		proof, relayKey := signedPooledProof(t, contributor, mother, []string{r1, r2}, now)
		store := NewTaskStateStore()
		registerPooledTask(t, store, "qsdm-edge-worker", proof.CoordinatorID, now)
		if err := store.ApplyActions([]TaskAction{
			{ID: "stake", Sender: mother, TaskID: "qsdm-edge-worker", Action: "stake", Amount: 1, Timestamp: now.Format(time.RFC3339Nano)},
			{ID: "fund", Sender: "sponsor", TaskID: "qsdm-edge-worker", Action: "fund", Amount: 1, Timestamp: now.Format(time.RFC3339Nano)},
		}); err != nil {
			t.Fatal(err)
		}
		accounts := NewAccountStore()
		accounts.Credit(mother, 1)
		if err := store.ApplyEconomicTxAtHeight(pooledSubmitTx(t, "settle-1", 0, 2, 120, proof, 0.05, now.Add(time.Second)), accounts, 121); err != nil {
			t.Fatal(err)
		}
		variant := func(ids []string) *mempool.Tx {
			p := proof
			p.ReceiptIDs = ids
			// A re-signing Relay also names a fresh receipt root: consensus checks
			// only its shape, and the per-sender duplicate-value gate keys on it.
			p.ReceiptRoot = strings.Repeat("e", 64)
			p.WindowStart = now.Add(-30 * time.Second).Format(time.RFC3339Nano)
			p = resignPooledProof(t, p, relayKey)
			return pooledSubmitTx(t, "settle-variant", 1, 3, 180, p, 0.05, now.Add(2*time.Second))
		}
		return store, accounts, now, variant
	}
	spaced := []string{r1 + " ", r2 + " "}

	t.Run("below the height the spaced variant replays as produced", func(t *testing.T) {
		withRelaySettlementScopeHeight(t, 1_000_000)
		store, accounts, _, variant := setup(t)
		if err := store.ApplyEconomicTxAtHeight(variant(spaced), accounts, 181); err != nil {
			t.Fatalf("history replays as produced: below the height the variant applies, got %v", err)
		}
		if account, _ := accounts.Get(contributor); mathAbs(account.Balance-0.07) > taskAmountEpsilon {
			t.Fatalf("contributor balance %.8f, want 0.07 (below the height, as produced)", account.Balance)
		}
	})
	t.Run("from the height the spaced variant is refused", func(t *testing.T) {
		withRelaySettlementScopeHeight(t, 100)
		store, accounts, _, variant := setup(t)
		err := store.ApplyEconomicTxAtHeight(variant(spaced), accounts, 181)
		if !errors.Is(err, ErrSettlementIDNotCanonical) {
			t.Fatalf("spaced receipt ids: got %v, want ErrSettlementIDNotCanonical", err)
		}
		if account, _ := accounts.Get(contributor); mathAbs(account.Balance-0.035) > taskAmountEpsilon {
			t.Fatalf("contributor balance %.8f, want 0.035 (paid once)", account.Balance)
		}
	})
	t.Run("from the height upper-case ids are refused", func(t *testing.T) {
		withRelaySettlementScopeHeight(t, 100)
		store, accounts, _, variant := setup(t)
		upper := []string{strings.Repeat("A", 64), strings.Repeat("B", 64)}
		if err := store.ApplyEconomicTxAtHeight(variant(upper), accounts, 181); !errors.Is(err, ErrSettlementIDNotCanonical) {
			t.Fatalf("upper-case receipt ids: got %v, want ErrSettlementIDNotCanonical", err)
		}
	})
	t.Run("from the height a canonical reuse is still a replay", func(t *testing.T) {
		withRelaySettlementScopeHeight(t, 100)
		store, accounts, _, variant := setup(t)
		if err := store.ApplyEconomicTxAtHeight(variant([]string{r1, r2}), accounts, 181); err == nil || !strings.Contains(err.Error(), "already consumed") {
			t.Fatalf("canonical receipt reuse: got %v, want already consumed", err)
		}
	})
	t.Run("admission refuses non-canonical ids at once", func(t *testing.T) {
		withRelaySettlementScopeHeight(t, 1_000_000)
		_, _, _, variant := setup(t)
		var bp *BlockProducer
		if err := bp.CheckEdgeRelayOnlyAdmission(variant(spaced)); !errors.Is(err, ErrSettlementIDNotCanonical) {
			t.Fatalf("admission: got %v, want ErrSettlementIDNotCanonical", err)
		}
		if err := bp.CheckEdgeRelayOnlyAdmission(variant([]string{r1, r2})); err != nil {
			t.Fatalf("admission of canonical ids: %v", err)
		}
	})
}

func lookalikeCatalogTx(t *testing.T, id, taskID, manager string) *mempool.Tx {
	t.Helper()
	manifest := TaskManifest{
		SchemaVersion: TaskCatalogSchemaVersion, TaskID: taskID, Version: 1, Name: "QSDM Edge Worker",
		Manager: manager, Active: true, MinimumStakeAmount: 1, RewardPerRound: 0.05, RoundTime: 60,
		Runtime: TaskRuntimeManifest{Kind: "capability", Capability: "qsdm-edge-worker-v1"},
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	action, err := json.Marshal(TaskAction{
		ID: id, Sender: manager, TaskID: taskID, Action: TaskActionCatalogRegister,
		Payload: string(payload), Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &mempool.Tx{ID: id, Sender: manager, ContractID: TaskContractID, Payload: action}
}

func TestReservedTaskCatalogLookalike(t *testing.T) {
	const someone = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	for _, id := range []string{"QSDM-Edge-Worker", "qsdm-edge-worker-GPU", "Qsdm-Hive-Local-Task"} {
		if !isReservedTaskCatalogLookalike(id) {
			t.Fatalf("%q should be a look-alike of a reserved id", id)
		}
	}
	for _, id := range []string{"qsdm-edge-worker", "my-edge-worker", "QSDM-Edge-Worker-2"} {
		if isReservedTaskCatalogLookalike(id) {
			t.Fatalf("%q should not be a look-alike", id)
		}
	}

	t.Run("below the height a look-alike registers", func(t *testing.T) {
		withRelaySettlementScopeHeight(t, 1_000_000)
		store := NewTaskStateStore()
		accounts := NewAccountStore()
		accounts.Credit(someone, 1)
		if err := store.ApplyEconomicTxAtHeight(lookalikeCatalogTx(t, "reg-1", "QSDM-Edge-Worker", someone), accounts, 500); err != nil {
			t.Fatalf("history replays as produced: %v", err)
		}
	})
	t.Run("from the height a look-alike is refused, for any sender", func(t *testing.T) {
		withRelaySettlementScopeHeight(t, 100)
		for _, sender := range []string{someone, TaskCatalogReservedManager} {
			store := NewTaskStateStore()
			accounts := NewAccountStore()
			accounts.Credit(sender, 1)
			err := store.ApplyEconomicTxAtHeight(lookalikeCatalogTx(t, "reg-"+sender[:4], "QSDM-Edge-Worker", sender), accounts, 500)
			if !errors.Is(err, ErrTaskCatalogReservedLookalike) {
				t.Fatalf("sender %s: got %v, want ErrTaskCatalogReservedLookalike", sender[:8], err)
			}
		}
	})
	t.Run("from the height the exact reserved id still works for its manager", func(t *testing.T) {
		withRelaySettlementScopeHeight(t, 100)
		store := NewTaskStateStore()
		accounts := NewAccountStore()
		accounts.Credit(TaskCatalogReservedManager, 1)
		if err := store.ApplyEconomicTxAtHeight(lookalikeCatalogTx(t, "reg-exact", "qsdm-edge-worker", TaskCatalogReservedManager), accounts, 500); err != nil {
			t.Fatalf("exact reserved id by its manager: %v", err)
		}
	})
	t.Run("admission refuses a look-alike at once", func(t *testing.T) {
		withRelaySettlementScopeHeight(t, 1_000_000)
		var bp *BlockProducer
		if err := bp.CheckEdgeRelayOnlyAdmission(lookalikeCatalogTx(t, "adm", "qsdm-edge-worker-GPU", someone)); !errors.Is(err, ErrTaskCatalogReservedLookalike) {
			t.Fatalf("admission: got %v, want ErrTaskCatalogReservedLookalike", err)
		}
	})
}
