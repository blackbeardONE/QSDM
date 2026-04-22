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
		if msg.Type != "metrics" {
			t.Fatalf("expected metrics message, got %s", msg.Type)
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

