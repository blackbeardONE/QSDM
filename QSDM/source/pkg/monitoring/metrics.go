package monitoring

import (
	"fmt"
	"sync"
	"time"
)

// Metrics collects system metrics for monitoring
type Metrics struct {
	mu                    sync.RWMutex
	TransactionsProcessed int64
	TransactionsValid     int64
	TransactionsInvalid   int64
	TransactionsStored    int64
	ProposalsCreated      int64
	VotesCast             int64
	QuarantinesTriggered  int64
	ReputationUpdates     int64
	NetworkMessagesSent   int64
	NetworkMessagesRecv   int64
	StartTime             time.Time
	LastTransactionTime   time.Time
	LastErrorTime         time.Time
	LastError             string
	// Storage operation metrics
	StorageOperations     int64
	StorageGetBalanceOps  int64
	StorageUpdateBalanceOps int64
	StorageSetBalanceOps  int64
	StorageGetBalanceLatency time.Duration
	StorageUpdateBalanceLatency time.Duration
	StorageSetBalanceLatency time.Duration
	StorageErrors         int64
	DatabaseConnections   int64

	// Hot-reload observability (updated by config.HotReloader and admin dry-run handler)
	HotReloadApplySuccess int64
	HotReloadApplyFailure int64
	HotReloadDryRunTotal  int64
	LastHotReloadDryRunAt time.Time
	LastHotReloadDryRunChanged  bool
	LastHotReloadDryRunPolicyOK bool
	LastHotReloadDryRunLoadOK   bool
}

var globalMetrics *Metrics
var metricsOnce sync.Once

// GetMetrics returns the global metrics instance
func GetMetrics() *Metrics {
	metricsOnce.Do(func() {
		globalMetrics = &Metrics{
			StartTime: time.Now(),
		}
	})
	return globalMetrics
}

// IncrementTransactionsProcessed increments the transaction counter
func (m *Metrics) IncrementTransactionsProcessed() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TransactionsProcessed++
	m.LastTransactionTime = time.Now()
}

// IncrementTransactionsValid increments the valid transaction counter
func (m *Metrics) IncrementTransactionsValid() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TransactionsValid++
}

// IncrementTransactionsInvalid increments the invalid transaction counter
func (m *Metrics) IncrementTransactionsInvalid() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TransactionsInvalid++
}

// IncrementTransactionsStored increments the stored transaction counter
func (m *Metrics) IncrementTransactionsStored() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TransactionsStored++
}

// IncrementProposalsCreated increments the proposal counter
func (m *Metrics) IncrementProposalsCreated() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ProposalsCreated++
}

// IncrementVotesCast increments the vote counter
func (m *Metrics) IncrementVotesCast() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.VotesCast++
}

// IncrementQuarantinesTriggered increments the quarantine counter
func (m *Metrics) IncrementQuarantinesTriggered() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.QuarantinesTriggered++
}

// IncrementReputationUpdates increments the reputation update counter
func (m *Metrics) IncrementReputationUpdates() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ReputationUpdates++
}

// IncrementNetworkMessagesSent increments the sent message counter
func (m *Metrics) IncrementNetworkMessagesSent() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.NetworkMessagesSent++
}

// IncrementNetworkMessagesRecv increments the received message counter
func (m *Metrics) IncrementNetworkMessagesRecv() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.NetworkMessagesRecv++
}

// IncHotReloadApply records a successful or failed hot-reload apply attempt.
func (m *Metrics) IncHotReloadApply(success bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if success {
		m.HotReloadApplySuccess++
	} else {
		m.HotReloadApplyFailure++
	}
}

// RecordHotReloadDryRun records one dry-run invocation and last outcome flags.
func (m *Metrics) RecordHotReloadDryRun(fileChanged, policyOK, loadOK bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.HotReloadDryRunTotal++
	m.LastHotReloadDryRunAt = time.Now()
	m.LastHotReloadDryRunChanged = fileChanged
	m.LastHotReloadDryRunPolicyOK = policyOK
	m.LastHotReloadDryRunLoadOK = loadOK
}

// RecordError records an error with timestamp
func (m *Metrics) RecordError(err string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastError = err
	m.LastErrorTime = time.Now()
}

// GetStats returns a snapshot of current metrics
func (m *Metrics) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	uptime := time.Since(m.StartTime)
	validityRate := float64(0)
	if m.TransactionsProcessed > 0 {
		validityRate = float64(m.TransactionsValid) / float64(m.TransactionsProcessed) * 100
	}

	stats := map[string]interface{}{
		"uptime_seconds":           uptime.Seconds(),
		"transactions_processed":   m.TransactionsProcessed,
		"transactions_valid":       m.TransactionsValid,
		"transactions_invalid":     m.TransactionsInvalid,
		"transactions_stored":      m.TransactionsStored,
		"validity_rate_percent":    validityRate,
		"proposals_created":        m.ProposalsCreated,
		"votes_cast":               m.VotesCast,
		"quarantines_triggered":    m.QuarantinesTriggered,
		"reputation_updates":       m.ReputationUpdates,
		"network_messages_sent":    m.NetworkMessagesSent,
		"network_messages_received": m.NetworkMessagesRecv,
		"last_transaction_time":    m.LastTransactionTime,
		"last_error_time":          m.LastErrorTime,
		"last_error":               m.LastError,
	}
	
	// Add storage stats
	storageStats := m.GetStorageStats()
	for k, v := range storageStats {
		stats[k] = v
	}

	stats["nvidia_lock_http_blocks_total"] = NvidiaLockHTTPBlockCount()
	stats["nvidia_lock_p2p_rejects_total"] = NvidiaLockP2PRejectCount()
	stats["ngc_challenge_issued_total"] = NGCChallengeIssuedCount()
	stats["ngc_challenge_rate_limited_total"] = NGCChallengeRateLimitedCount()
	stats["ngc_ingest_nonce_pool_size"] = NGCIngestNoncePoolSize()
	stats["ngc_proof_ingest_accepted_total"] = NGCIngestAcceptedTotal()
	stats["ngc_proof_ingest_rejected_total"] = NGCIngestRejectedTotal()

	stats["submesh_p2p_reject_route_total"] = SubmeshP2PRejectRouteCount()
	stats["submesh_p2p_reject_size_total"] = SubmeshP2PRejectSizeCount()
	stats["submesh_api_wallet_reject_route_total"] = SubmeshAPIWalletRejectRouteCount()
	stats["submesh_api_wallet_reject_size_total"] = SubmeshAPIWalletRejectSizeCount()
	stats["submesh_api_privileged_reject_size_total"] = SubmeshAPIPrivilegedRejectSizeCount()
	stats["mesh_companion_publish_total"] = MeshCompanionPublishCount()
	stats["p2p_wallet_ingress_dedupe_skip_total"] = P2PWalletIngressDedupeSkipCount()

	stats["hot_reload_apply_success_total"] = m.HotReloadApplySuccess
	stats["hot_reload_apply_failure_total"] = m.HotReloadApplyFailure
	stats["hot_reload_dry_run_total"] = m.HotReloadDryRunTotal
	stats["hot_reload_last_dry_run_changed"] = m.LastHotReloadDryRunChanged
	stats["hot_reload_last_dry_run_policy_ok"] = m.LastHotReloadDryRunPolicyOK
	stats["hot_reload_last_dry_run_load_ok"] = m.LastHotReloadDryRunLoadOK
	stats["hot_reload_last_dry_run_at"] = m.LastHotReloadDryRunAt

	return stats
}

// RecordStorageOperation records a storage operation with latency
func (m *Metrics) RecordStorageOperation(operation string, latency time.Duration, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StorageOperations++
	if err != nil {
		m.StorageErrors++
		m.LastError = fmt.Sprintf("Storage %s failed: %v", operation, err)
		m.LastErrorTime = time.Now()
	}
	
	switch operation {
	case "GetBalance":
		m.StorageGetBalanceOps++
		m.StorageGetBalanceLatency = latency
	case "UpdateBalance":
		m.StorageUpdateBalanceOps++
		m.StorageUpdateBalanceLatency = latency
	case "SetBalance":
		m.StorageSetBalanceOps++
		m.StorageSetBalanceLatency = latency
	}
}

// GetStorageStats returns storage-specific metrics
func (m *Metrics) GetStorageStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	
	avgGetBalance := float64(0)
	if m.StorageGetBalanceOps > 0 {
		avgGetBalance = m.StorageGetBalanceLatency.Seconds() * 1000 // Convert to ms
	}
	
	avgUpdateBalance := float64(0)
	if m.StorageUpdateBalanceOps > 0 {
		avgUpdateBalance = m.StorageUpdateBalanceLatency.Seconds() * 1000
	}
	
	avgSetBalance := float64(0)
	if m.StorageSetBalanceOps > 0 {
		avgSetBalance = m.StorageSetBalanceLatency.Seconds() * 1000
	}
	
	return map[string]interface{}{
		"storage_operations_total":     m.StorageOperations,
		"storage_get_balance_ops":      m.StorageGetBalanceOps,
		"storage_update_balance_ops":    m.StorageUpdateBalanceOps,
		"storage_set_balance_ops":       m.StorageSetBalanceOps,
		"storage_get_balance_latency_ms": avgGetBalance,
		"storage_update_balance_latency_ms": avgUpdateBalance,
		"storage_set_balance_latency_ms": avgSetBalance,
		"storage_errors":               m.StorageErrors,
		"database_connections":          m.DatabaseConnections,
	}
}

// Reset resets all metrics (useful for testing)
func (m *Metrics) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.TransactionsProcessed = 0
	m.TransactionsValid = 0
	m.TransactionsInvalid = 0
	m.TransactionsStored = 0
	m.ProposalsCreated = 0
	m.VotesCast = 0
	m.QuarantinesTriggered = 0
	m.ReputationUpdates = 0
	m.NetworkMessagesSent = 0
	m.NetworkMessagesRecv = 0
	m.StartTime = time.Now()
	m.LastError = ""
	m.StorageOperations = 0
	m.StorageGetBalanceOps = 0
	m.StorageUpdateBalanceOps = 0
	m.StorageSetBalanceOps = 0
	m.StorageErrors = 0
	m.StorageGetBalanceLatency = 0
	m.StorageUpdateBalanceLatency = 0
	m.StorageSetBalanceLatency = 0
	m.HotReloadApplySuccess = 0
	m.HotReloadApplyFailure = 0
	m.HotReloadDryRunTotal = 0
	m.LastHotReloadDryRunAt = time.Time{}
	m.LastHotReloadDryRunChanged = false
	m.LastHotReloadDryRunPolicyOK = false
	m.LastHotReloadDryRunLoadOK = false
}

