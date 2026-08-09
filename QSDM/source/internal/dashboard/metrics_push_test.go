package dashboard

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/networking"
)

func TestMetricsPusher_PushNowIncludesSnapshot(t *testing.T) {
	hub := NewWSHub()

	accounts := chain.NewAccountStore()
	accounts.Credit("alice", 100)
	accounts.Credit("bob", 50)

	validators := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	_ = validators.Register("val-1", 100)
	_ = validators.Register("val-2", 100)

	finality := chain.NewFinalityGadget(chain.FinalityConfig{
		ConfirmationDepth: 1,
		FinalityDepth:     2,
		ReorgLimit:        10,
		FinalizeInterval:  time.Second,
	})
	finality.TrackBlock(1, "h1")
	finality.UpdateTip(3)

	pool := mempool.New(mempool.DefaultConfig())
	_ = pool.Add(&mempool.Tx{ID: "tx1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 1, Nonce: 0})

	receipts := chain.NewReceiptStore()
	receipts.Store(&chain.TxReceipt{TxID: "tx1", BlockHeight: 1, Status: chain.ReceiptSuccess, Timestamp: time.Now()})

	peers := networking.NewReputationTracker(networking.DefaultReputationConfig())
	peers.RecordEvent("peer-1", networking.EventValidBlock, 0)

	pe := monitoring.NewPrometheusExporter()
	pe.SetGauge("qsdm_test_metric", "test metric", 42, nil)

	pusher := NewMetricsPusher(hub, MetricsSource{
		Prometheus: pe,
		Accounts:   accounts,
		Validators: validators,
		Finality:   finality,
		Mempool:    pool,
		Receipts:   receipts,
		Peers:      peers,
	}, time.Second)

	pusher.PushNow()

	select {
	case raw := <-hub.broadcast:
		var msg WSMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("unmarshal ws message: %v", err)
		}
		// MUST NOT be "metrics": that type is already used by
		// dashboard.go for Metrics.GetStats(), and the page's
		// applyMetrics() reads GetStats' field names. Broadcasting this
		// differently-shaped snapshot under the same name made every
		// counter fall back to `|| 0`, zeroing Uptime, Processed,
		// Messages Sent, Proposals Created and the rest on each push.
		if msg.Type != WSTypeChainSnapshot {
			t.Fatalf("expected %s message, got %s", WSTypeChainSnapshot, msg.Type)
		}
		if msg.Type == "metrics" {
			t.Fatal("the chain snapshot must not reuse the counter payload's message type")
		}
		data, ok := msg.Data.(map[string]interface{})
		if !ok {
			t.Fatalf("expected data map, got %T", msg.Data)
		}
		if data["account_count"].(float64) != 2 {
			t.Fatalf("expected account_count=2, got %v", data["account_count"])
		}
		if data["validators_active"].(float64) != 2 {
			t.Fatalf("expected validators_active=2, got %v", data["validators_active"])
		}
		if data["peer_count"].(float64) != 1 {
			t.Fatalf("expected peer_count=1, got %v", data["peer_count"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broadcast")
	}
}

func TestDashboard_StartWSPush_UsesMetricsPusherWhenConfigured(t *testing.T) {
	m := monitoring.GetMetrics()
	hc := monitoring.NewHealthChecker(m)
	d := NewDashboard(m, hc, "0", false, DashboardNvidiaLock{}, "", "", false, "", nil)
	defer d.wsHub.Stop()

	accounts := chain.NewAccountStore()
	accounts.Credit("alice", 10)

	d.SetRealtimeMetricsSource(MetricsSource{
		Accounts: accounts,
	})

	d.StartWSPush(20 * time.Millisecond)
	time.Sleep(90 * time.Millisecond)

	if d.wsMetricsPusher == nil {
		t.Fatal("expected wsMetricsPusher to be initialized")
	}
	if d.wsMetricsPusher.PushCount() == 0 {
		t.Fatal("expected wsMetricsPusher to push at least once")
	}

	d.wsMetricsPusher.Stop()
}


// TestWSMetricsTypes_doNotCollide pins the invariant that broke the
// dashboard's Transaction / Network / Governance / System panels.
//
// dashboard.go broadcasts Metrics.GetStats() as "metrics", and the page's
// applyMetrics() reads that struct's field names (uptime_seconds,
// transactions_processed, network_messages_sent, proposals_created,
// quarantines_triggered, reputation_updates). MetricsPusher was broadcasting
// a completely different struct under the SAME type, so applyMetrics found
// none of its fields, fell back to `|| 0` on every one, and each push wiped
// the values the HTTP poll had just rendered.
//
// The symptom was "Uptime 0s" on a node that had been running for over an
// hour — impossible for a real payload, since GetStats derives uptime from
// Metrics.StartTime which GetMetrics always sets.
func TestWSMetricsTypes_doNotCollide(t *testing.T) {
	if WSTypeChainSnapshot == "metrics" {
		t.Fatal("chain snapshot must not share the counter payload's WS type")
	}

	// The counter payload must actually carry what applyMetrics reads, so a
	// future refactor cannot silently drop these field names.
	stats := monitoring.GetMetrics().GetStats()
	for _, field := range []string{
		"uptime_seconds",
		"transactions_processed",
		"network_messages_sent",
		"proposals_created",
		"quarantines_triggered",
		"reputation_updates",
	} {
		if _, ok := stats[field]; !ok {
			t.Fatalf("GetStats must expose %q — the dashboard reads it and renders 0 when absent", field)
		}
	}

	// And the snapshot must NOT carry them, which is exactly why it needs a
	// separate type rather than being merged into the counter payload.
	snap := MetricsSnapshot{}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	var asMap map[string]interface{}
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatal(err)
	}
	if _, ok := asMap["uptime_seconds"]; ok {
		t.Fatal("snapshot unexpectedly carries uptime_seconds; the two payloads would be mergeable")
	}
}
