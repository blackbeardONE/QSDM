package mempool

import (
	"container/heap"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Tx represents a transaction in the mempool.
type Tx struct {
	ID        string
	Sender    string
	Recipient string
	Amount    float64
	Fee       float64
	GasLimit  int64
	Nonce     uint64
	Payload   []byte
	// ContractID is an optional hint for contract-scoped transfers or traces; it does not affect
	// AccountStore balance logic but is included in TxSigningHash when non-empty and copied to TxReceipt.
	ContractID string `json:"contract_id,omitempty"`
	AddedAt    time.Time
}

// priority returns the ordering key (higher = processed first).
func (t *Tx) priority() float64 { return t.Fee }

var (
	ErrMempoolFull   = errors.New("mempool is full")
	ErrDuplicateTx   = errors.New("transaction already in mempool")
	ErrTxNotFound    = errors.New("transaction not found")
)

// Config for the mempool.
type Config struct {
	MaxSize       int           // maximum number of pending transactions
	MaxTxAge      time.Duration // evict transactions older than this
	EvictInterval time.Duration // how often to run eviction sweep
}

// DefaultConfig returns a sensible default config.
func DefaultConfig() Config {
	return Config{
		MaxSize:       10000,
		MaxTxAge:      30 * time.Minute,
		EvictInterval: 15 * time.Second,
	}
}

// Mempool is a fee-ordered priority queue of pending transactions.
type Mempool struct {
	mu       sync.RWMutex
	pq       txPQ
	index    map[string]int // tx ID -> position in pq (managed by heap interface)
	lookup   map[string]*Tx // tx ID -> *Tx for O(1) retrieval
	cfg      Config
	admitErr func(*Tx) error // optional admission gate (e.g. POL); if non-nil, Add returns its error
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// New creates a mempool with the given config.
func New(cfg Config) *Mempool {
	if cfg.MaxSize <= 0 {
		cfg.MaxSize = 10000
	}
	if cfg.MaxTxAge <= 0 {
		cfg.MaxTxAge = 30 * time.Minute
	}
	if cfg.EvictInterval <= 0 {
		cfg.EvictInterval = 15 * time.Second
	}
	m := &Mempool{
		pq:     make(txPQ, 0, 256),
		index:  make(map[string]int),
		lookup: make(map[string]*Tx),
		cfg:    cfg,
		stopCh: make(chan struct{}),
	}
	heap.Init(&m.pq)
	return m
}

// SetAdmissionChecker sets an optional callback invoked at the start of Add before the tx is accepted.
// Returning a non-nil error rejects the transaction (tx is not stored).
func (m *Mempool) SetAdmissionChecker(fn func(*Tx) error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.admitErr = fn
}

// Start begins background eviction of stale transactions.
func (m *Mempool) Start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ticker := time.NewTicker(m.cfg.EvictInterval)
		defer ticker.Stop()
		for {
			select {
			case <-m.stopCh:
				return
			case <-ticker.C:
				m.EvictStale()
			}
		}
	}()
}

// Stop halts background eviction.
func (m *Mempool) Stop() {
	close(m.stopCh)
	m.wg.Wait()
}

// Add inserts a transaction. If the pool is full, evicts the lowest-fee tx if the new one pays more.
func (m *Mempool) Add(tx *Tx) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.lookup[tx.ID]; exists {
		return ErrDuplicateTx
	}

	if m.admitErr != nil {
		if err := m.admitErr(tx); err != nil {
			return err
		}
	}

	if tx.AddedAt.IsZero() {
		tx.AddedAt = time.Now()
	}

	if len(m.pq) >= m.cfg.MaxSize {
		// In a max-heap the minimum is among the leaf nodes (second half)
		minIdx := len(m.pq) / 2
		for i := minIdx + 1; i < len(m.pq); i++ {
			if m.pq[i].tx.priority() < m.pq[minIdx].tx.priority() {
				minIdx = i
			}
		}
		lowest := m.pq[minIdx].tx
		if tx.priority() <= lowest.priority() {
			return fmt.Errorf("%w: minimum fee is %.8f", ErrMempoolFull, lowest.priority())
		}
		m.removeLocked(lowest.ID)
	}

	entry := &txEntry{tx: tx, index: -1}
	heap.Push(&m.pq, entry)
	m.lookup[tx.ID] = tx

	return nil
}

// RestoreTransactions re-inserts transactions after a failed speculative block build.
// It bypasses the admission checker so txs can return to the pool even when extension gates apply.
func (m *Mempool) RestoreTransactions(txs []*Tx) {
	if m == nil || len(txs) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		if _, exists := m.lookup[tx.ID]; exists {
			continue
		}
		if tx.AddedAt.IsZero() {
			tx.AddedAt = time.Now()
		}
		if len(m.pq) >= m.cfg.MaxSize {
			minIdx := len(m.pq) / 2
			for i := minIdx + 1; i < len(m.pq); i++ {
				if m.pq[i].tx.priority() < m.pq[minIdx].tx.priority() {
					minIdx = i
				}
			}
			lowest := m.pq[minIdx].tx
			if tx.priority() <= lowest.priority() {
				continue
			}
			m.removeLocked(lowest.ID)
		}
		entry := &txEntry{tx: tx, index: -1}
		heap.Push(&m.pq, entry)
		m.lookup[tx.ID] = tx
	}
}

// Pop removes and returns the highest-priority transaction.
func (m *Mempool) Pop() (*Tx, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.pq) == 0 {
		return nil, false
	}
	entry := heap.Pop(&m.pq).(*txEntry)
	delete(m.lookup, entry.tx.ID)
	return entry.tx, true
}

// Peek returns the highest-priority tx without removing it.
func (m *Mempool) Peek() (*Tx, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.pq) == 0 {
		return nil, false
	}
	return m.pq[0].tx, true
}

// Get returns a transaction by ID.
func (m *Mempool) Get(id string) (*Tx, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tx, ok := m.lookup[id]
	return tx, ok
}

// Remove drops a specific transaction (e.g. after inclusion in a block).
func (m *Mempool) Remove(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.removeLocked(id)
}

func (m *Mempool) removeLocked(id string) bool {
	if _, ok := m.lookup[id]; !ok {
		return false
	}
	for i, e := range m.pq {
		if e.tx.ID == id {
			heap.Remove(&m.pq, i)
			delete(m.lookup, id)
			return true
		}
	}
	delete(m.lookup, id)
	return false
}

// Size returns the number of pending transactions.
func (m *Mempool) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pq)
}

// EvictStale removes transactions older than MaxTxAge.
func (m *Mempool) EvictStale() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := time.Now().Add(-m.cfg.MaxTxAge)
	var stale []string
	for id, tx := range m.lookup {
		if tx.AddedAt.Before(cutoff) {
			stale = append(stale, id)
		}
	}
	for _, id := range stale {
		m.removeLocked(id)
	}
	return len(stale)
}

// Drain returns up to `max` highest-priority transactions in order.
func (m *Mempool) Drain(max int) []*Tx {
	m.mu.Lock()
	defer m.mu.Unlock()
	if max <= 0 || len(m.pq) == 0 {
		return nil
	}
	n := max
	if n > len(m.pq) {
		n = len(m.pq)
	}
	result := make([]*Tx, 0, n)
	for i := 0; i < n; i++ {
		entry := heap.Pop(&m.pq).(*txEntry)
		delete(m.lookup, entry.tx.ID)
		result = append(result, entry.tx)
	}
	return result
}

// Stats returns mempool statistics.
func (m *Mempool) Stats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var topFee float64
	if len(m.pq) > 0 {
		topFee = m.pq[0].tx.Fee
	}
	return map[string]interface{}{
		"size":     len(m.pq),
		"max_size": m.cfg.MaxSize,
		"top_fee":  topFee,
	}
}

// --- priority queue internals ---

type txEntry struct {
	tx    *Tx
	index int
}

type txPQ []*txEntry

func (pq txPQ) Len() int            { return len(pq) }
func (pq txPQ) Less(i, j int) bool  { return pq[i].tx.priority() > pq[j].tx.priority() } // max-heap by fee
func (pq txPQ) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}
func (pq *txPQ) Push(x interface{}) {
	entry := x.(*txEntry)
	entry.index = len(*pq)
	*pq = append(*pq, entry)
}
func (pq *txPQ) Pop() interface{} {
	old := *pq
	n := len(old)
	entry := old[n-1]
	old[n-1] = nil
	entry.index = -1
	*pq = old[:n-1]
	return entry
}
