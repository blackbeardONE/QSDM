package contracts

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEventIndex_EmitAndQuery(t *testing.T) {
	idx := NewEventIndex(100)

	idx.Emit("tok1", "Transfer", map[string]interface{}{"to": "alice", "amount": 50.0}, 100)
	idx.Emit("tok1", "Transfer", map[string]interface{}{"to": "bob", "amount": 25.0}, 100)
	idx.Emit("tok2", "VoteCast", map[string]interface{}{"proposal": "p1", "choice": true}, 50)

	if idx.Count() != 3 {
		t.Fatalf("expected 3 events, got %d", idx.Count())
	}

	all := idx.Query("", "", 100, 0)
	if len(all) != 3 {
		t.Fatalf("expected 3 events from unfiltered query, got %d", len(all))
	}
	// Most recent first
	if all[0].EventName != "VoteCast" {
		t.Fatalf("expected most recent event first, got %s", all[0].EventName)
	}

	byContract := idx.Query("tok1", "", 100, 0)
	if len(byContract) != 2 {
		t.Fatalf("expected 2 events for tok1, got %d", len(byContract))
	}

	byEvent := idx.Query("", "VoteCast", 100, 0)
	if len(byEvent) != 1 {
		t.Fatalf("expected 1 VoteCast event, got %d", len(byEvent))
	}
}

func TestEventIndex_Retention(t *testing.T) {
	idx := NewEventIndex(5)
	for i := 0; i < 10; i++ {
		idx.Emit("c1", "Tick", map[string]interface{}{"i": i}, 0)
	}
	if idx.Count() != 5 {
		t.Fatalf("expected 5 events (retention limit), got %d", idx.Count())
	}
	events := idx.Query("", "", 100, 0)
	// The oldest remaining event should have i=5
	last := events[len(events)-1]
	if v, ok := last.Data["i"].(int); !ok || v != 5 {
		t.Fatalf("expected oldest retained event i=5, got %v", last.Data["i"])
	}
}

func TestEventIndex_QueryOffsetLimit(t *testing.T) {
	idx := NewEventIndex(100)
	for i := 0; i < 10; i++ {
		idx.Emit("c1", "Tick", map[string]interface{}{"i": i}, 0)
	}

	page := idx.Query("", "", 3, 2)
	if len(page) != 3 {
		t.Fatalf("expected 3 events with offset, got %d", len(page))
	}
	// Skipping 2 most recent (i=9, i=8), next is i=7
	if v, ok := page[0].Data["i"].(int); !ok || v != 7 {
		t.Fatalf("expected first event in page to be i=7, got %v", page[0].Data["i"])
	}
}

func TestEventIndex_Subscribe(t *testing.T) {
	idx := NewEventIndex(100)

	var received []ContractEvent
	var mu sync.Mutex
	idx.Subscribe("tok1", "Transfer", func(evt ContractEvent) {
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
	})

	idx.Emit("tok1", "Transfer", map[string]interface{}{"to": "alice"}, 0)
	idx.Emit("tok2", "Transfer", map[string]interface{}{"to": "bob"}, 0)   // wrong contract
	idx.Emit("tok1", "VoteCast", map[string]interface{}{"p": "1"}, 0)      // wrong event

	mu.Lock()
	if len(received) != 1 {
		t.Fatalf("expected 1 subscriber call, got %d", len(received))
	}
	if received[0].Data["to"] != "alice" {
		t.Fatalf("unexpected event data: %v", received[0].Data)
	}
	mu.Unlock()
}

func TestEventIndex_SubscribeAll(t *testing.T) {
	idx := NewEventIndex(100)

	var count int32
	idx.Subscribe("", "", func(evt ContractEvent) {
		atomic.AddInt32(&count, 1)
	})

	idx.Emit("c1", "A", nil, 0)
	idx.Emit("c2", "B", nil, 0)
	idx.Emit("c3", "C", nil, 0)

	if atomic.LoadInt32(&count) != 3 {
		t.Fatalf("expected 3 subscriber calls, got %d", count)
	}
}

func TestEngine_EmitsTransferEvent(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{
		Functions: []Function{{Name: "transfer", Inputs: []Param{{Name: "to", Type: "string"}, {Name: "amount", Type: "uint64"}}, StateMutating: true}},
		Events:    []Event{{Name: "Transfer", Params: []Param{{Name: "to", Type: "string"}, {Name: "amount", Type: "uint64"}}}},
	}
	engine.DeployContract(ctx, "tok_evt", []byte{0x01}, abi, "owner")

	var captured []ContractEvent
	var mu sync.Mutex
	engine.Events.Subscribe("tok_evt", "Transfer", func(evt ContractEvent) {
		mu.Lock()
		captured = append(captured, evt)
		mu.Unlock()
	})

	engine.ExecuteContract(ctx, "tok_evt", "transfer", map[string]interface{}{"to": "alice", "amount": 100})

	mu.Lock()
	if len(captured) != 1 {
		t.Fatalf("expected 1 Transfer event, got %d", len(captured))
	}
	if captured[0].Data["to"] != "alice" {
		t.Fatalf("unexpected transfer target: %v", captured[0].Data["to"])
	}
	mu.Unlock()

	if engine.Events.CountByContract("tok_evt") != 1 {
		t.Fatalf("expected 1 event for tok_evt, got %d", engine.Events.CountByContract("tok_evt"))
	}
}

func TestEngine_EmitsVoteEvent(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{
		Functions: []Function{{Name: "vote", Inputs: []Param{{Name: "proposal", Type: "string"}, {Name: "choice", Type: "bool"}}, StateMutating: true}},
	}
	engine.DeployContract(ctx, "voting_evt", []byte{0x01}, abi, "owner")

	engine.ExecuteContract(ctx, "voting_evt", "vote", map[string]interface{}{"proposal": "p1", "choice": true})

	evts := engine.Events.Query("voting_evt", "VoteCast", 10, 0)
	if len(evts) != 1 {
		t.Fatalf("expected 1 VoteCast event, got %d", len(evts))
	}
}

func TestEngine_EmitsEscrowEvents(t *testing.T) {
	engine := NewContractEngine(nil)
	ctx := context.Background()
	abi := &ABI{
		Functions: []Function{
			{Name: "deposit", Inputs: []Param{{Name: "amount", Type: "uint64"}}, StateMutating: true},
			{Name: "release", Inputs: []Param{{Name: "escrowId", Type: "string"}}, StateMutating: true},
		},
	}
	engine.DeployContract(ctx, "escrow_evt", []byte{0x01}, abi, "owner")

	result, _ := engine.ExecuteContract(ctx, "escrow_evt", "deposit", map[string]interface{}{"amount": 500})
	output := result.Output.(map[string]interface{})
	escrowID := output["escrowId"].(string)

	engine.ExecuteContract(ctx, "escrow_evt", "release", map[string]interface{}{"escrowId": escrowID})

	evts := engine.Events.Query("escrow_evt", "", 10, 0)
	if len(evts) != 2 {
		t.Fatalf("expected 2 escrow events, got %d", len(evts))
	}
	if evts[0].EventName != "EscrowReleased" {
		t.Fatalf("expected EscrowReleased, got %s", evts[0].EventName)
	}
	if evts[1].EventName != "EscrowCreated" {
		t.Fatalf("expected EscrowCreated, got %s", evts[1].EventName)
	}
}
