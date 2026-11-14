package chain

import (
	"encoding/json"
	"testing"
	"time"
)

// A claim sums its rounds in ascending order, so the computed amount is the same
// every time (Go map order is random, and 0.1+0.2+0.3 depends on the order).
func TestClaimSumsRoundsInOrder(t *testing.T) {
	const worker = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	now := time.Now().UTC().Format(time.RFC3339Nano)
	store := NewTaskStateStore()
	actions := []TaskAction{
		{ID: "stake", Sender: worker, TaskID: "custom", Action: "stake", Amount: 1, Timestamp: now},
		{ID: "fund", Sender: "sponsor", TaskID: "custom", Action: "fund", Amount: 10, Timestamp: now},
	}
	for i, reward := range []float64{0.1, 0.2, 0.3} {
		payload, _ := json.Marshal(map[string]any{"round": i + 1, "submission_value": "v" + string(rune('a'+i)), "reward_amount": reward})
		actions = append(actions, TaskAction{ID: "sub-" + string(rune('a'+i)), Sender: worker, TaskID: "custom", Action: "submit", Payload: string(payload), Timestamp: now})
	}
	if err := store.ApplyActions(actions); err != nil {
		t.Fatal(err)
	}
	a, b, c := 0.1, 0.2, 0.3 // float64 variables: a constant expression would be summed exactly
	want := (a + b) + c      // rounds 1, 2, 3 in order: 0.6000000000000001, not 0.6
	for i := 0; i < 300; i++ {
		if got := store.ClaimableReward(TaskAction{TaskID: "custom", Sender: worker}); got != want {
			t.Fatalf("iteration %d: claimable %v, want %v exactly", i, got, want)
		}
	}
	if err := store.ApplyAction(TaskAction{ID: "claim", Sender: worker, TaskID: "custom", Action: "claim", Timestamp: now}); err != nil {
		t.Fatal(err)
	}
	state, _ := store.GetTask("custom")
	if got := state.Participants[worker].TotalRewardClaimedAmount; got != want {
		t.Fatalf("claimed %v, want %v exactly", got, want)
	}
}

func TestClaimRoundKeysOrder(t *testing.T) {
	got := claimRoundKeys(map[string]map[string]TaskSubmissionState{"10": nil, "2": nil, "1": nil, "x": nil, "0": nil})
	want := []string{"0", "1", "2", "10", "x"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}
