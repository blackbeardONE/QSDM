package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func taskReplayTx(t *testing.T, action chain.TaskAction) *mempool.Tx {
	t.Helper()
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal task action: %v", err)
	}
	return &mempool.Tx{
		ID:         action.ID,
		Sender:     action.Sender,
		Nonce:      action.Nonce,
		Amount:     action.Amount,
		Payload:    payload,
		ContractID: chain.TaskContractID,
	}
}

func TestReplayTaskStateFromBlocksLegacyResourceProofBoundary(t *testing.T) {
	legacy := chain.TaskAction{
		ID:      "legacy-edge-submit",
		Sender:  "alice",
		TaskID:  "qsdm-edge-worker",
		Action:  "submit",
		Payload: `{"source":"qsdm-edge-worker","worker_kind":"cpu-sha256-v1","round":990,"slot":59401,"submission_value":"31a800969176d6d67f4739f98b0d066e8cc7a1728bdf2818da2608ba1c9af8fe","reward_amount":0,"proof":{"algorithm":"sha256-iterated","iterations":50000,"digest":"31a800969176d6d67f4739f98b0d066e8cc7a1728bdf2818da2608ba1c9af8fe"}}`,
		Nonce:   32,
	}

	legacyStore := chain.NewTaskStateStore()
	replayed, err := replayTaskStateFromBlocks(legacyStore, []*chain.Block{{
		Height:       chain.ResourceWorkerProofActivationHeight - 1,
		Transactions: []*mempool.Tx{taskReplayTx(t, legacy)},
	}})
	if err != nil {
		t.Fatalf("legacy pre-activation replay failed: %v", err)
	}
	if replayed != 1 {
		t.Fatalf("replayed = %d, want 1", replayed)
	}

	strictStore := chain.NewTaskStateStore()
	_, err = replayTaskStateFromBlocks(strictStore, []*chain.Block{{
		Height:       chain.ResourceWorkerProofActivationHeight,
		Transactions: []*mempool.Tx{taskReplayTx(t, legacy)},
	}})
	if err == nil || !strings.Contains(err.Error(), `expected "cpu"`) {
		t.Fatalf("post-activation legacy proof was not rejected: %v", err)
	}

	liveStore := chain.NewTaskStateStore()
	if err := liveStore.ApplyAction(legacy); err == nil || !strings.Contains(err.Error(), `expected "cpu"`) {
		t.Fatalf("live legacy proof was not rejected: %v", err)
	}
}

func TestReplayTaskStateFromBlocksRestoresConfirmedTaskStake(t *testing.T) {
	store := chain.NewTaskStateStore()
	blocks := []*chain.Block{
		{
			Height: 1,
			Transactions: []*mempool.Tx{
				taskReplayTx(t, chain.TaskAction{
					ID:        "stake-1",
					Sender:    "alice",
					TaskID:    "task-1",
					Action:    "stake",
					Amount:    4,
					Nonce:     1,
					Timestamp: "2026-05-30T00:00:00Z",
				}),
				{
					ID:     "ordinary-transfer",
					Sender: "alice",
				},
			},
		},
		{
			Height: 2,
			Transactions: []*mempool.Tx{
				taskReplayTx(t, chain.TaskAction{
					ID:        "start-1",
					Sender:    "alice",
					TaskID:    "task-1",
					Action:    "start",
					Nonce:     2,
					Timestamp: "2026-05-30T00:01:00Z",
				}),
			},
		},
	}

	replayed, err := replayTaskStateFromBlocks(store, blocks)
	if err != nil {
		t.Fatalf("replayTaskStateFromBlocks: %v", err)
	}
	if replayed != 2 {
		t.Fatalf("replayed = %d, want 2", replayed)
	}
	state, ok := store.GetTask("task-1")
	if !ok {
		t.Fatal("task state missing after replay")
	}
	participant := state.Participants["alice"]
	if participant.Stake != 4 || !participant.Running || state.RunningCount != 1 {
		t.Fatalf("restored participant/state mismatch: participant=%+v state=%+v", participant, state)
	}
}

func TestCanonicalPersistedChainDropsForkedDuplicateHeights(t *testing.T) {
	blocks := []*chain.Block{
		{Height: 10, Hash: "a", PrevHash: "external"},
		{Height: 11, Hash: "b1", PrevHash: "a"},
		{Height: 11, Hash: "b2", PrevHash: "a"},
		{Height: 12, Hash: "c1", PrevHash: "b1"},
		{Height: 12, Hash: "c2", PrevHash: "b2"},
		{Height: 13, Hash: "d2", PrevHash: "c2"},
	}

	canonical, dropped := canonicalPersistedChain(blocks)
	if dropped != 2 {
		t.Fatalf("dropped = %d, want 2", dropped)
	}
	got := make([]string, 0, len(canonical))
	for _, blk := range canonical {
		got = append(got, blk.Hash)
	}
	want := []string{"a", "b2", "c2", "d2"}
	if len(got) != len(want) {
		t.Fatalf("canonical len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("canonical[%d] = %q, want %q; got=%v", i, got[i], want[i], got)
		}
	}
}

func TestReplayTaskStateFromBlocksSkipsHistoricalStartBeforeStake(t *testing.T) {
	store := chain.NewTaskStateStore()
	blocks := []*chain.Block{
		{
			Height: 1,
			Transactions: []*mempool.Tx{
				taskReplayTx(t, chain.TaskAction{
					ID:        "legacy-start",
					Sender:    "alice",
					TaskID:    "task-1",
					Action:    "start",
					Nonce:     1,
					Timestamp: "2026-05-30T00:00:00Z",
				}),
				taskReplayTx(t, chain.TaskAction{
					ID:        "stake-after-legacy-start",
					Sender:    "alice",
					TaskID:    "task-1",
					Action:    "stake",
					Amount:    2,
					Nonce:     2,
					Timestamp: "2026-05-30T00:01:00Z",
				}),
			},
		},
	}

	replayed, err := replayTaskStateFromBlocks(store, blocks)
	if err != nil {
		t.Fatalf("replayTaskStateFromBlocks: %v", err)
	}
	if replayed != 1 {
		t.Fatalf("replayed = %d, want 1", replayed)
	}
	state, ok := store.GetTask("task-1")
	if !ok {
		t.Fatal("task state missing after replay")
	}
	if got := state.Participants["alice"].Stake; got != 2 {
		t.Fatalf("restored stake = %v, want 2", got)
	}
}
