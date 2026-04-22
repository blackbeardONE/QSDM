package dashboard

import (
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/networking"
)

// MetricsSource provides access to all subsystems whose metrics get pushed
// to the dashboard via WebSocket.
type MetricsSource struct {
	Prometheus *monitoring.PrometheusExporter
	Accounts   *chain.AccountStore
	Validators *chain.ValidatorSet
	Finality   *chain.FinalityGadget
	Mempool    *mempool.Mempool
	Receipts   *chain.ReceiptStore
	Peers      *networking.ReputationTracker
	Producer   *chain.BlockProducer
}

// MetricsPusher periodically collects metrics from all subsystems and
// broadcasts them to connected WebSocket clients.
type MetricsPusher struct {
	mu       sync.Mutex
	hub      *WSHub
	source   MetricsSource
	interval time.Duration
	stopCh   chan struct{}
	started  bool
	pushCount int
}

// NewMetricsPusher creates a pusher that sends metrics to the given hub.
func NewMetricsPusher(hub *WSHub, source MetricsSource, interval time.Duration) *MetricsPusher {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &MetricsPusher{
		hub:      hub,
		source:   source,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins periodic metric broadcasting.
func (mp *MetricsPusher) Start() {
	mp.mu.Lock()
	if mp.started {
		mp.mu.Unlock()
		return
	}
	mp.started = true
	mp.mu.Unlock()

	go func() {
		ticker := time.NewTicker(mp.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				mp.push()
			case <-mp.stopCh:
				return
			}
		}
	}()
}

// Stop halts periodic broadcasting.
func (mp *MetricsPusher) Stop() {
	mp.mu.Lock()
	if !mp.started {
		mp.mu.Unlock()
		return
	}
	mp.started = false
	mp.mu.Unlock()
	close(mp.stopCh)
}

// PushNow triggers an immediate metric push (useful for tests).
func (mp *MetricsPusher) PushNow() {
	mp.push()
}

// PushCount returns how many pushes have been performed.
func (mp *MetricsPusher) PushCount() int {
	mp.mu.Lock()
	defer mp.mu.Unlock()
	return mp.pushCount
}

func (mp *MetricsPusher) push() {
	snapshot := mp.collectSnapshot()
	mp.hub.Broadcast("metrics", snapshot)

	mp.mu.Lock()
	mp.pushCount++
	mp.mu.Unlock()
}

// MetricsSnapshot is the data pushed to the dashboard on each interval.
type MetricsSnapshot struct {
	Timestamp       time.Time              `json:"timestamp"`
	ChainHeight     uint64                 `json:"chain_height"`
	AccountCount    int                    `json:"account_count"`
	MempoolSize     int                    `json:"mempool_size"`
	ValidatorsActive int                   `json:"validators_active"`
	ValidatorsTotal int                    `json:"validators_total"`
	PendingBlocks   int                    `json:"pending_blocks"`
	FinalizedBlocks int                    `json:"finalized_blocks"`
	ReceiptStats    map[string]interface{} `json:"receipt_stats,omitempty"`
	PeerCount       int                    `json:"peer_count"`
	BannedPeers     int                    `json:"banned_peers"`
	WSClients       int                    `json:"ws_clients"`
	PrometheusMetrics []monitoring.Metric  `json:"prometheus_metrics,omitempty"`
}

func (mp *MetricsPusher) collectSnapshot() MetricsSnapshot {
	s := MetricsSnapshot{
		Timestamp: time.Now().UTC(),
	}

	if mp.source.Producer != nil {
		s.ChainHeight = mp.source.Producer.ChainHeight()
	}
	if mp.source.Accounts != nil {
		s.AccountCount = mp.source.Accounts.Count()
	}
	if mp.source.Mempool != nil {
		s.MempoolSize = mp.source.Mempool.Size()
	}
	if mp.source.Validators != nil {
		s.ValidatorsActive = len(mp.source.Validators.ActiveValidators())
		s.ValidatorsTotal = mp.source.Validators.Size()
	}
	if mp.source.Finality != nil {
		s.PendingBlocks = mp.source.Finality.PendingCount()
		s.FinalizedBlocks = mp.source.Finality.FinalizedCount()
	}
	if mp.source.Receipts != nil {
		s.ReceiptStats = mp.source.Receipts.Stats()
	}
	if mp.source.Peers != nil {
		s.PeerCount = mp.source.Peers.PeerCount()
		s.BannedPeers = len(mp.source.Peers.BannedPeers())
	}
	if mp.hub != nil {
		s.WSClients = mp.hub.ClientCount()
	}
	if mp.source.Prometheus != nil {
		s.PrometheusMetrics = mp.source.Prometheus.Collect()
	}

	return s
}
