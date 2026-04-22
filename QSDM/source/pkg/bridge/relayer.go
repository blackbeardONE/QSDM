package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// ChainAdapter is the interface each supported chain must implement.
type ChainAdapter interface {
	ChainID() string
	SubmitLock(ctx context.Context, lock *Lock) (txHash string, err error)
	SubmitRedeem(ctx context.Context, lockID, secret string) (txHash string, err error)
	SubmitRefund(ctx context.Context, lockID string) (txHash string, err error)
	ConfirmTransaction(ctx context.Context, txHash string) (confirmed bool, err error)
}

// RelayTask represents a pending relay operation.
type RelayTask struct {
	ID         string      `json:"id"`
	Kind       string      `json:"kind"` // "lock", "redeem", "refund"
	ChainID    string      `json:"chain_id"`
	LockID     string      `json:"lock_id"`
	Secret     string      `json:"secret,omitempty"`
	TxHash     string      `json:"tx_hash,omitempty"`
	Attempts   int         `json:"attempts"`
	MaxRetries int         `json:"max_retries"`
	Status     RelayStatus `json:"status"`
	CreatedAt  time.Time   `json:"created_at"`
	LastTryAt  time.Time   `json:"last_try_at"`
	Error      string      `json:"error,omitempty"`
	Nonce      uint64      `json:"nonce"`
}

// RelayStatus represents task lifecycle.
type RelayStatus string

const (
	RelayPending   RelayStatus = "pending"
	RelaySubmitted RelayStatus = "submitted"
	RelayConfirmed RelayStatus = "confirmed"
	RelayFailed    RelayStatus = "failed"
	RelayRetrying  RelayStatus = "retrying"
)

// Relayer is a production-grade daemon that relays bridge operations across chains.
type Relayer struct {
	adapters    map[string]ChainAdapter
	queue       []*RelayTask
	mu          sync.Mutex
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	nonceTrack  map[string]uint64 // chain_id -> next nonce
	maxRetries  int
	retryDelay  time.Duration
	pollInterval time.Duration
}

// RelayerConfig configures the relayer.
type RelayerConfig struct {
	MaxRetries   int
	RetryDelay   time.Duration
	PollInterval time.Duration
}

// DefaultRelayerConfig returns sensible defaults.
func DefaultRelayerConfig() RelayerConfig {
	return RelayerConfig{
		MaxRetries:   5,
		RetryDelay:   10 * time.Second,
		PollInterval: 5 * time.Second,
	}
}

// NewRelayer creates a new multi-chain bridge relayer.
func NewRelayer(cfg RelayerConfig) *Relayer {
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 10 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Relayer{
		adapters:     make(map[string]ChainAdapter),
		queue:        make([]*RelayTask, 0),
		ctx:          ctx,
		cancel:       cancel,
		nonceTrack:   make(map[string]uint64),
		maxRetries:   cfg.MaxRetries,
		retryDelay:   cfg.RetryDelay,
		pollInterval: cfg.PollInterval,
	}
}

// RegisterAdapter adds a chain adapter to the relayer.
func (r *Relayer) RegisterAdapter(adapter ChainAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[adapter.ChainID()] = adapter
}

// Enqueue adds a relay task to the queue.
func (r *Relayer) Enqueue(task *RelayTask) {
	r.mu.Lock()
	defer r.mu.Unlock()

	task.Status = RelayPending
	task.CreatedAt = time.Now()
	task.MaxRetries = r.maxRetries
	task.Nonce = r.nonceTrack[task.ChainID]
	r.nonceTrack[task.ChainID]++
	r.queue = append(r.queue, task)
}

// EnqueueLock creates and enqueues a lock relay task.
func (r *Relayer) EnqueueLock(lock *Lock, targetChain string) string {
	task := &RelayTask{
		ID:      fmt.Sprintf("relay_%s_%d", lock.ID, time.Now().UnixNano()),
		Kind:    "lock",
		ChainID: targetChain,
		LockID:  lock.ID,
	}
	r.Enqueue(task)
	return task.ID
}

// EnqueueRedeem creates and enqueues a redeem relay task.
func (r *Relayer) EnqueueRedeem(lockID, secret, chainID string) string {
	task := &RelayTask{
		ID:      fmt.Sprintf("relay_redeem_%s_%d", lockID, time.Now().UnixNano()),
		Kind:    "redeem",
		ChainID: chainID,
		LockID:  lockID,
		Secret:  secret,
	}
	r.Enqueue(task)
	return task.ID
}

// Start begins the relay processing loop.
func (r *Relayer) Start() {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.processLoop()
	}()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.confirmLoop()
	}()
}

// Stop halts the relayer gracefully.
func (r *Relayer) Stop() {
	r.cancel()
	r.wg.Wait()
}

func (r *Relayer) processLoop() {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.processPendingTasks()
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *Relayer) processPendingTasks() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, task := range r.queue {
		if task.Status != RelayPending && task.Status != RelayRetrying {
			continue
		}
		adapter, ok := r.adapters[task.ChainID]
		if !ok {
			task.Status = RelayFailed
			task.Error = fmt.Sprintf("no adapter for chain %s", task.ChainID)
			continue
		}

		task.Attempts++
		task.LastTryAt = time.Now()

		var txHash string
		var err error

		ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
		switch task.Kind {
		case "lock":
			txHash, err = adapter.SubmitLock(ctx, &Lock{ID: task.LockID})
		case "redeem":
			txHash, err = adapter.SubmitRedeem(ctx, task.LockID, task.Secret)
		case "refund":
			txHash, err = adapter.SubmitRefund(ctx, task.LockID)
		default:
			err = fmt.Errorf("unknown task kind: %s", task.Kind)
		}
		cancel()

		if err != nil {
			log.Printf("[relayer] task %s attempt %d failed: %v", task.ID, task.Attempts, err)
			task.Error = err.Error()
			if task.Attempts >= task.MaxRetries {
				task.Status = RelayFailed
			} else {
				task.Status = RelayRetrying
			}
			continue
		}

		task.TxHash = txHash
		task.Status = RelaySubmitted
		task.Error = ""
		log.Printf("[relayer] task %s submitted, tx=%s", task.ID, txHash)
	}
}

func (r *Relayer) confirmLoop() {
	ticker := time.NewTicker(r.pollInterval * 2)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.checkConfirmations()
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *Relayer) checkConfirmations() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, task := range r.queue {
		if task.Status != RelaySubmitted || task.TxHash == "" {
			continue
		}
		adapter, ok := r.adapters[task.ChainID]
		if !ok {
			continue
		}

		ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
		confirmed, err := adapter.ConfirmTransaction(ctx, task.TxHash)
		cancel()

		if err != nil {
			log.Printf("[relayer] confirm check for %s failed: %v", task.ID, err)
			continue
		}
		if confirmed {
			task.Status = RelayConfirmed
			log.Printf("[relayer] task %s confirmed on chain %s", task.ID, task.ChainID)
		}
	}
}

// PendingCount returns the number of pending/retrying tasks.
func (r *Relayer) PendingCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, t := range r.queue {
		if t.Status == RelayPending || t.Status == RelayRetrying {
			count++
		}
	}
	return count
}

// ListTasks returns all relay tasks.
func (r *Relayer) ListTasks() []*RelayTask {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*RelayTask, len(r.queue))
	copy(out, r.queue)
	return out
}

// SaveQueue persists the relay queue to a JSON file.
func (r *Relayer) SaveQueue(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := json.MarshalIndent(r.queue, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data)
}

// LoadQueue restores the relay queue from a JSON file.
func (r *Relayer) LoadQueue(path string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := readFileIfExists(path)
	if err != nil || len(data) == 0 {
		return err
	}
	return json.Unmarshal(data, &r.queue)
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := writeFile(tmp, data); err != nil {
		return err
	}
	return renameFile(tmp, path)
}

// Thin wrappers to simplify testing / platform differences.
var writeFile = func(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}

var renameFile = func(src, dst string) error {
	return os.Rename(src, dst)
}

var readFileIfExists = func(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil && os.IsNotExist(err) {
		return nil, nil
	}
	return data, err
}

// ── Stub adapter for testing / in-process simulation ──

// SimulatedChainAdapter is a no-op adapter useful for testing and dev.
type SimulatedChainAdapter struct {
	chain      string
	mu         sync.Mutex
	submitted  map[string]bool
}

// NewSimulatedChainAdapter creates a test adapter for a given chain ID.
func NewSimulatedChainAdapter(chainID string) *SimulatedChainAdapter {
	return &SimulatedChainAdapter{
		chain:     chainID,
		submitted: make(map[string]bool),
	}
}

func (a *SimulatedChainAdapter) ChainID() string { return a.chain }

func (a *SimulatedChainAdapter) SubmitLock(_ context.Context, lock *Lock) (string, error) {
	txHash := fmt.Sprintf("sim_tx_lock_%s_%d", lock.ID, time.Now().UnixNano())
	a.mu.Lock()
	a.submitted[txHash] = true
	a.mu.Unlock()
	return txHash, nil
}

func (a *SimulatedChainAdapter) SubmitRedeem(_ context.Context, lockID, _ string) (string, error) {
	txHash := fmt.Sprintf("sim_tx_redeem_%s_%d", lockID, time.Now().UnixNano())
	a.mu.Lock()
	a.submitted[txHash] = true
	a.mu.Unlock()
	return txHash, nil
}

func (a *SimulatedChainAdapter) SubmitRefund(_ context.Context, lockID string) (string, error) {
	txHash := fmt.Sprintf("sim_tx_refund_%s_%d", lockID, time.Now().UnixNano())
	a.mu.Lock()
	a.submitted[txHash] = true
	a.mu.Unlock()
	return txHash, nil
}

func (a *SimulatedChainAdapter) ConfirmTransaction(_ context.Context, txHash string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.submitted[txHash], nil
}
