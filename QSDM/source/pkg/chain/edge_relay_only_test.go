package chain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const (
	eroWorker      = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	eroContributor = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	eroMother      = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var eroResources = map[string]string{
	"qsdm-edge-worker":     "cpu",
	"qsdm-edge-worker-gpu": "gpu",
	"qsdm-edge-worker-ram": "ram",
}

func withEdgeRelayOnlyHeight(t *testing.T, h uint64) {
	t.Helper()
	prev := EdgeRelayOnlyActivationHeight()
	SetEdgeRelayOnlyActivationHeight(h)
	t.Cleanup(func() { SetEdgeRelayOnlyActivationHeight(prev) })
}

// eroLocalPayload is the Hive "local" resource proof shape (no Relay).
func eroLocalPayload(resource, source string, round uint64, seed int, reward float64) string {
	digest := fmt.Sprintf("%064x", seed)
	return fmt.Sprintf(`{"round":%d,"slot":%d,"source":%q,"resource":%q,"submission_value":%q,"reward_amount":%v,"proof":{"algorithm":"sha256-iterated","units":50000,"digest":%q}}`,
		round, round*60, source, resource, digest, reward, digest)
}

func eroTx(id, sender, taskID, action, payload string, amount float64, nonce uint64) *mempool.Tx {
	body, _ := json.Marshal(TaskAction{ID: id, Sender: sender, TaskID: taskID, Action: action,
		Amount: amount, Payload: payload, Nonce: nonce, Timestamp: "2026-10-12T00:00:00Z"})
	return &mempool.Tx{ID: id, Sender: sender, Nonce: nonce, Amount: amount, ContractID: TaskContractID, Payload: body}
}

// eroFundedStore stakes eroWorker and funds every resource task with 1 CELL.
func eroFundedStore(t *testing.T) (*TaskStateStore, *AccountStore) {
	t.Helper()
	store := NewTaskStateStore()
	for taskID := range eroResources {
		if err := store.ApplyActions([]TaskAction{
			{ID: "stake-" + taskID, Sender: eroWorker, TaskID: taskID, Action: "stake", Amount: 1, Timestamp: "2026-10-12T00:00:00Z"},
			{ID: "fund-" + taskID, Sender: "sponsor", TaskID: taskID, Action: "fund", Amount: 1, Timestamp: "2026-10-12T00:00:00Z"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	accounts := NewAccountStore()
	accounts.Credit(eroWorker, 1)
	return store, accounts
}

// Below H a local resource proof moves reward from pool to pending exactly as
// before; at and above H it is refused on the consensus paths (economic apply
// and historical replay) with no account, nonce or task-state change, for all
// three resource tasks and every reward (zero included) and non-Relay source.
func TestEdgeRelayOnlyLocalProofBelowAtAbove(t *testing.T) {
	const h = 1_000
	withEdgeRelayOnlyHeight(t, h)
	for taskID, resource := range eroResources {
		t.Run(taskID, func(t *testing.T) {
			store, accounts := eroFundedStore(t)
			below := eroTx("below", eroWorker, taskID, "submit", eroLocalPayload(resource, "qsdm-edge-worker-"+resource, 1, 1, 0.05), 0, 0)
			if err := store.ApplyEconomicTxAtHeight(below, accounts, h-1); err != nil {
				t.Fatalf("local proof below H: %v", err)
			}
			st, _ := store.GetTask(taskID)
			if mathAbs(st.RewardPoolAmount-0.95) > taskAmountEpsilon || mathAbs(st.PendingRewardAmount-0.05) > taskAmountEpsilon {
				t.Fatalf("below H accounting: %+v", st)
			}
			for i, tc := range []struct {
				height uint64
				source string
				reward float64
			}{
				{h, "qsdm-edge-worker-" + resource, 0.05},
				{h + 1, "qsdm-edge-worker-" + resource, 0.05},
				{h + 1, "qsdm-edge-worker-" + resource, 0},
				{h + 1_000_000, "", 0.01},
				{h, "qsdm-edge-pool", 0.05},
				{h, "qsdm-edge-relay", 0.05},
				{h, "QSDM-EDGE-RELAY-V1", 0.05},
			} {
				rootBefore, acctBefore := store.StateRoot(), accounts.StateRoot()
				tx := eroTx(fmt.Sprintf("at-%d", i), eroWorker, taskID, "submit", eroLocalPayload(resource, tc.source, uint64(10+i), 10+i, tc.reward), 0, 1)
				if err := store.ApplyEconomicTxAtHeight(tx, accounts, tc.height); !errors.Is(err, ErrEdgeRelayOnly) {
					t.Fatalf("case %d (%+v): economic apply returned %v", i, tc, err)
				}
				if err := store.ChainReplayClone().(*TaskStateStore).ApplyHistoricalTx(tx, tc.height); !errors.Is(err, ErrEdgeRelayOnly) {
					t.Fatalf("case %d (%+v): replay returned %v", i, tc, err)
				}
				if store.StateRoot() != rootBefore || accounts.StateRoot() != acctBefore {
					t.Fatalf("case %d: a refused submit changed state", i)
				}
			}
			// The heightless ApplyAction projection is not a consensus path
			// and is unchanged.
			action, _ := DecodeTaskActionTx(eroTx("projection", eroWorker, taskID, "submit", eroLocalPayload(resource, "x", 50, 50, 0.01), 0, 0))
			if err := store.ChainReplayClone().(*TaskStateStore).ApplyAction(action); err != nil {
				t.Fatalf("heightless projection: %v", err)
			}
		})
	}
}

// With no activation height (0) the rule never applies.
func TestEdgeRelayOnlyInertAtZero(t *testing.T) {
	withEdgeRelayOnlyHeight(t, 0)
	store, accounts := eroFundedStore(t)
	tx := eroTx("local", eroWorker, "qsdm-edge-worker", "submit", eroLocalPayload("cpu", "qsdm-edge-worker-cpu", 1, 1, 0.05), 0, 0)
	if err := store.ApplyEconomicTxAtHeight(tx, accounts, 1<<40); err != nil {
		t.Fatalf("local proof with no activation height: %v", err)
	}
	if EdgeRelayOnlyActiveAt(1 << 62) {
		t.Fatal("active with height 0")
	}
}

// At and above H the pooled Relay settlement still pays 70/15/15 from the
// pool, and replay still rejects a reused proof.
func TestEdgeRelayOnlyPooledSettlementStillPays(t *testing.T) {
	withEdgeRelayOnlyHeight(t, 100)
	now := time.Now().UTC()
	proof, _ := signedPooledProof(t, eroContributor, eroMother, []string{strings.Repeat("7", 64)}, now)
	store := NewTaskStateStore()
	registerPooledTask(t, store, "qsdm-edge-worker", proof.CoordinatorID, now)
	if err := store.ApplyActions([]TaskAction{
		{ID: "stake", Sender: eroMother, TaskID: "qsdm-edge-worker", Action: "stake", Amount: 1, Timestamp: now.Format(time.RFC3339Nano)},
		{ID: "fund", Sender: "sponsor", TaskID: "qsdm-edge-worker", Action: "fund", Amount: 1, Timestamp: now.Format(time.RFC3339Nano)},
	}); err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountStore()
	accounts.Credit(eroMother, 1)
	tx := pooledSubmitTx(t, "settle", 0, 2, 120, proof, 0.05, now.Add(time.Second))
	if !EdgeRelayOnlyActiveAt(121) {
		t.Fatal("rule not active at 121")
	}
	if err := store.ApplyEconomicTxAtHeight(tx, accounts, 121); err != nil {
		t.Fatalf("pooled settlement at H: %v", err)
	}
	for addr, want := range map[string]float64{eroContributor: 0.035, eroMother: 1.0075, edgepool.ProductionEcosystemWallet: 0.0075} {
		if acc, ok := accounts.Get(addr); !ok || mathAbs(acc.Balance-want) > taskAmountEpsilon {
			t.Fatalf("balance %s = %+v, want %v", addr, acc, want)
		}
	}
	st, _ := store.GetTask("qsdm-edge-worker")
	if mathAbs(st.RewardPoolAmount-0.95) > taskAmountEpsilon || st.PendingRewardAmount != 0 {
		t.Fatalf("settled accounting: %+v", st)
	}
	replay := pooledSubmitTx(t, "settle-replay", 1, 3, 180, proof, 0.05, now.Add(2*time.Second))
	if err := store.ApplyEconomicTxAtHeight(replay, accounts, 181); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("proof replay at H returned %v", err)
	}
}

// Other tasks, other actions, and claim of reward that was already pending
// below H are unaffected at and above H.
func TestEdgeRelayOnlyOtherTasksAndClaimUnaffected(t *testing.T) {
	const h = 500
	withEdgeRelayOnlyHeight(t, h)
	store, accounts := eroFundedStore(t)
	accrued := eroTx("accrue", eroWorker, "qsdm-edge-worker", "submit", eroLocalPayload("cpu", "qsdm-edge-worker-cpu", 1, 1, 0.05), 0, 0)
	if err := store.ApplyEconomicTxAtHeight(accrued, accounts, h-1); err != nil {
		t.Fatal(err)
	}
	claim := eroTx("claim", eroWorker, "qsdm-edge-worker", "claim", "", 0, 1)
	if err := store.ApplyEconomicTxAtHeight(claim, accounts, h+5); err != nil {
		t.Fatalf("claim at H of reward pending since below H: %v", err)
	}
	if acc, _ := accounts.Get(eroWorker); mathAbs(acc.Balance-1.05) > taskAmountEpsilon {
		t.Fatalf("claim paid %+v", acc)
	}
	// A second claim has nothing left: claim cannot move pool to pending.
	again := eroTx("claim-2", eroWorker, "qsdm-edge-worker", "claim", "", 0, 2)
	if err := store.ApplyEconomicTxAtHeight(again, accounts, h+6); !errors.Is(err, ErrNoTaskReward) {
		t.Fatalf("second claim returned %v", err)
	}
	for i, tx := range []*mempool.Tx{
		eroTx("generic-stake", eroWorker, "community-task", "stake", "", 0.1, 2),
		eroTx("generic-submit", eroWorker, "community-task", "submit", `{"round":7,"submission_value":"anything"}`, 0, 3),
		eroTx("cased-id", eroWorker, "QSDM-EDGE-WORKER", "submit", `{"round":7,"submission_value":"x"}`, 0, 4),
		eroTx("start", eroWorker, "qsdm-edge-worker", "start", "", 0, 5),
		eroTx("fund", eroWorker, "qsdm-edge-worker-gpu", "fund", "", 0.01, 6),
		eroTx("stop", eroWorker, "qsdm-edge-worker", "stop", "", 0, 7),
	} {
		if err := store.ApplyEconomicTxAtHeight(tx, accounts, h+10+uint64(i)); err != nil {
			t.Fatalf("%s at H: %v", tx.ID, err)
		}
	}
}

// Replay determinism: a history committed below H replays to the same task
// state root whether or not H is configured, and a history that crosses H
// replays identically to its live application.
func TestEdgeRelayOnlyReplayDeterminism(t *testing.T) {
	type committed struct {
		tx     *mempool.Tx
		height uint64
	}
	build := func() []committed {
		var out []committed
		nonce := uint64(0)
		for i := 0; i < 6; i++ {
			out = append(out, committed{eroTx(fmt.Sprintf("hist-%d", i), eroWorker, "qsdm-edge-worker", "submit",
				eroLocalPayload("cpu", "qsdm-edge-worker-cpu", uint64(i+1), i+1, 0.05), 0, nonce), uint64(90 + i)})
			nonce++
		}
		out = append(out, committed{eroTx("hist-claim", eroWorker, "qsdm-edge-worker", "claim", "", 0, nonce), 98})
		return out
	}
	apply := func(h uint64, history []committed, historical bool) (string, []error) {
		withEdgeRelayOnlyHeight(t, h)
		store, accounts := eroFundedStore(t)
		var errs []error
		for _, c := range history {
			var err error
			if historical {
				err = store.ApplyHistoricalTx(c.tx, c.height)
			} else {
				err = store.ApplyEconomicTxAtHeight(c.tx, accounts, c.height)
			}
			errs = append(errs, err)
		}
		return store.StateRoot(), errs
	}
	history := build()
	ref, refErrs := apply(0, history, false)
	for _, h := range []uint64{0, 99, 1_000, 1 << 40} {
		for _, historical := range []bool{false, true} {
			root, errs := apply(h, history, historical)
			if root != ref {
				t.Fatalf("H=%d historical=%v: root %s, want %s", h, historical, root, ref)
			}
			for i := range errs {
				if (errs[i] == nil) != (refErrs[i] == nil) {
					t.Fatalf("H=%d historical=%v tx %d: %v vs %v", h, historical, i, errs[i], refErrs[i])
				}
			}
		}
	}
	// H inside the history: a live chain commits only what the rule allows
	// (nonces follow the account), and replaying the committed blocks gives
	// the same task state root, with H set or (since nothing committed
	// breaks the rule) with H unset.
	const hMid = 93
	withEdgeRelayOnlyHeight(t, hMid)
	store, accounts := eroFundedStore(t)
	var commits []committed
	for i := 0; i < 8; i++ {
		height := uint64(90 + i)
		acc, _ := accounts.Get(eroWorker)
		var tx *mempool.Tx
		if i == 7 {
			tx = eroTx("mid-claim", eroWorker, "qsdm-edge-worker", "claim", "", 0, acc.Nonce)
		} else {
			tx = eroTx(fmt.Sprintf("mid-%d", i), eroWorker, "qsdm-edge-worker", "submit",
				eroLocalPayload("cpu", "qsdm-edge-worker-cpu", uint64(i+1), 100+i, 0.05), 0, acc.Nonce)
		}
		err := store.ApplyEconomicTxAtHeight(tx, accounts, height)
		if wantRefused := height >= hMid && i != 7; errors.Is(err, ErrEdgeRelayOnly) != wantRefused || (!wantRefused && err != nil) {
			t.Fatalf("tx %s at %d: %v", tx.ID, height, err)
		}
		if err == nil {
			commits = append(commits, committed{tx, height})
		}
	}
	liveRoot := store.StateRoot()
	for _, h := range []uint64{hMid, 0} {
		withEdgeRelayOnlyHeight(t, h)
		replay, _ := eroFundedStore(t)
		for _, c := range commits {
			if err := replay.ApplyHistoricalTx(c.tx, c.height); err != nil {
				t.Fatalf("replay H=%d %s: %v", h, c.tx.ID, err)
			}
		}
		if replay.StateRoot() != liveRoot {
			t.Fatalf("replay H=%d root differs from live", h)
		}
	}
}

// Admission refuses non-Relay resource submits from the lead window before H
// (and fail-closed while the tip is unknown), and passes everything else.
func TestEdgeRelayOnlyAdmission(t *testing.T) {
	local := eroTx("adm-local", eroWorker, "qsdm-edge-worker-ram", "submit", eroLocalPayload("ram", "qsdm-edge-worker-ram", 1, 1, 0.05), 0, 0)
	relay := eroTx("adm-relay", eroMother, "qsdm-edge-worker", "submit", `{"source":" QSDM-Edge-Relay-V2 ","resource":"cpu"}`, 0, 0)
	others := []*mempool.Tx{
		relay,
		eroTx("adm-claim", eroWorker, "qsdm-edge-worker", "claim", "", 0, 0),
		eroTx("adm-generic", eroWorker, "community-task", "submit", `{"round":1}`, 0, 0),
		{ID: "adm-transfer", Sender: eroWorker, ContractID: WalletTransferContractID},
		{ID: "adm-stream", Sender: eroWorker, ContractID: StreamContractID, Payload: local.Payload},
		nil,
	}
	bp := &BlockProducer{}
	check := func(tip uint64, known bool, tx *mempool.Tx) error {
		bp.tipHeight.Store(tip)
		bp.tipHeightSet.Store(known)
		return bp.EdgeRelayOnlyAdmission(nil)(tx)
	}
	withEdgeRelayOnlyHeight(t, 0)
	if err := check(10, true, local); err != nil {
		t.Fatalf("H=0: %v", err)
	}
	const h = 10_000
	withEdgeRelayOnlyHeight(t, h)
	before := EdgeRelayOnlyAdmissionRejected()
	for _, tc := range []struct {
		tip    uint64
		known  bool
		refuse bool
	}{
		{h - EdgeRelayOnlyAdmissionLeadBlocks - 2, true, false},
		{h - EdgeRelayOnlyAdmissionLeadBlocks - 1, true, true},
		{h - 2, true, true},
		{h - 1, true, true},
		{h + 50, true, true},
		{0, false, true},
	} {
		err := check(tc.tip, tc.known, local)
		if refused := errors.Is(err, ErrEdgeRelayOnly); refused != tc.refuse {
			t.Fatalf("tip %d known %v: %v, want refuse=%v", tc.tip, tc.known, err, tc.refuse)
		}
		for _, o := range others {
			if err := check(tc.tip, tc.known, o); err != nil {
				t.Fatalf("tip %d: %v refused: %v", tc.tip, o, err)
			}
		}
	}
	if got := EdgeRelayOnlyAdmissionRejected() - before; got != 5 {
		t.Fatalf("admission counter advanced by %d, want 5", got)
	}
	// next is still consulted.
	sentinel := errors.New("next")
	if err := bp.EdgeRelayOnlyAdmission(func(*mempool.Tx) error { return sentinel })(relay); !errors.Is(err, sentinel) {
		t.Fatalf("next not called: %v", err)
	}
}
