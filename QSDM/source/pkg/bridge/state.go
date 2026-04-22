package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// bridgeState is the on-disk representation of all bridge + swap state.
type bridgeState struct {
	Locks   map[string]*lockRecord `json:"locks,omitempty"`
	Swaps   map[string]*swapRecord `json:"swaps,omitempty"`
	SavedAt string                 `json:"saved_at"`
}

type lockRecord struct {
	ID            string     `json:"id"`
	SourceChain   string     `json:"source_chain"`
	TargetChain   string     `json:"target_chain"`
	Asset         string     `json:"asset"`
	Amount        float64    `json:"amount"`
	Recipient     string     `json:"recipient"`
	LockedAt      time.Time  `json:"locked_at"`
	ExpiresAt     time.Time  `json:"expires_at"`
	SecretHash    string     `json:"secret_hash"`
	Secret        string     `json:"secret"`
	Status        LockStatus `json:"status"`
	TransactionID string     `json:"transaction_id,omitempty"`
}

type swapRecord struct {
	ID                    string     `json:"id"`
	InitiatorChain        string     `json:"initiator_chain"`
	ParticipantChain      string     `json:"participant_chain"`
	InitiatorAsset        string     `json:"initiator_asset"`
	ParticipantAsset      string     `json:"participant_asset"`
	InitiatorAmount       float64    `json:"initiator_amount"`
	ParticipantAmount     float64    `json:"participant_amount"`
	InitiatorAddress      string     `json:"initiator_address"`
	ParticipantAddress    string     `json:"participant_address"`
	InitiatorSecretHash   string     `json:"initiator_secret_hash"`
	ParticipantSecretHash string     `json:"participant_secret_hash"`
	InitiatorSecret       string     `json:"initiator_secret"`
	ParticipantSecret     string     `json:"participant_secret"`
	Status                SwapStatus `json:"status"`
	CreatedAt             time.Time  `json:"created_at"`
	ExpiresAt             time.Time  `json:"expires_at"`
}

// SaveState writes the current in-memory bridge+swap state to path as JSON.
func SaveState(path string, bp *BridgeProtocol, asp *AtomicSwapProtocol) error {
	st := bridgeState{
		SavedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if bp != nil {
		bp.mu.RLock()
		st.Locks = make(map[string]*lockRecord, len(bp.locks))
		for k, l := range bp.locks {
			st.Locks[k] = lockToRecord(l)
		}
		bp.mu.RUnlock()
	}

	if asp != nil {
		asp.mu.RLock()
		st.Swaps = make(map[string]*swapRecord, len(asp.swaps))
		for k, s := range asp.swaps {
			st.Swaps[k] = swapToRecord(s)
		}
		asp.mu.RUnlock()
	}

	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bridge state: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write bridge state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename bridge state: %w", err)
	}
	return nil
}

// LoadState reads bridge+swap state from path and populates bp/asp.
// Missing file is not an error (returns 0, 0, nil).
func LoadState(path string, bp *BridgeProtocol, asp *AtomicSwapProtocol) (lockCount, swapCount int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("read bridge state: %w", err)
	}

	var st bridgeState
	if err := json.Unmarshal(data, &st); err != nil {
		return 0, 0, fmt.Errorf("unmarshal bridge state: %w", err)
	}

	if bp != nil && len(st.Locks) > 0 {
		bp.mu.Lock()
		for k, r := range st.Locks {
			bp.locks[k] = recordToLock(r)
		}
		bp.mu.Unlock()
		lockCount = len(st.Locks)
	}

	if asp != nil && len(st.Swaps) > 0 {
		asp.mu.Lock()
		for k, r := range st.Swaps {
			asp.swaps[k] = recordToSwap(r)
		}
		asp.mu.Unlock()
		swapCount = len(st.Swaps)
	}

	return lockCount, swapCount, nil
}

// AutoSaver periodically persists bridge state to disk.
type AutoSaver struct {
	path   string
	bp     *BridgeProtocol
	asp    *AtomicSwapProtocol
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewAutoSaver starts a goroutine that saves bridge state every interval.
func NewAutoSaver(path string, bp *BridgeProtocol, asp *AtomicSwapProtocol, interval time.Duration) *AutoSaver {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	as := &AutoSaver{
		path:   path,
		bp:     bp,
		asp:    asp,
		stopCh: make(chan struct{}),
	}
	as.wg.Add(1)
	go as.loop(interval)
	return as
}

func (as *AutoSaver) loop(interval time.Duration) {
	defer as.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			_ = SaveState(as.path, as.bp, as.asp)
		case <-as.stopCh:
			_ = SaveState(as.path, as.bp, as.asp)
			return
		}
	}
}

// Stop signals the auto-saver to flush and exit, then waits for it.
func (as *AutoSaver) Stop() {
	close(as.stopCh)
	as.wg.Wait()
}

func lockToRecord(l *Lock) *lockRecord {
	return &lockRecord{
		ID: l.ID, SourceChain: l.SourceChain, TargetChain: l.TargetChain,
		Asset: l.Asset, Amount: l.Amount, Recipient: l.Recipient,
		LockedAt: l.LockedAt, ExpiresAt: l.ExpiresAt,
		SecretHash: l.SecretHash, Secret: l.Secret,
		Status: l.Status, TransactionID: l.TransactionID,
	}
}

func recordToLock(r *lockRecord) *Lock {
	return &Lock{
		ID: r.ID, SourceChain: r.SourceChain, TargetChain: r.TargetChain,
		Asset: r.Asset, Amount: r.Amount, Recipient: r.Recipient,
		LockedAt: r.LockedAt, ExpiresAt: r.ExpiresAt,
		SecretHash: r.SecretHash, Secret: r.Secret,
		Status: r.Status, TransactionID: r.TransactionID,
	}
}

func swapToRecord(s *Swap) *swapRecord {
	return &swapRecord{
		ID: s.ID, InitiatorChain: s.InitiatorChain, ParticipantChain: s.ParticipantChain,
		InitiatorAsset: s.InitiatorAsset, ParticipantAsset: s.ParticipantAsset,
		InitiatorAmount: s.InitiatorAmount, ParticipantAmount: s.ParticipantAmount,
		InitiatorAddress: s.InitiatorAddress, ParticipantAddress: s.ParticipantAddress,
		InitiatorSecretHash: s.InitiatorSecretHash, ParticipantSecretHash: s.ParticipantSecretHash,
		InitiatorSecret: s.InitiatorSecret, ParticipantSecret: s.ParticipantSecret,
		Status: s.Status, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt,
	}
}

func recordToSwap(r *swapRecord) *Swap {
	return &Swap{
		ID: r.ID, InitiatorChain: r.InitiatorChain, ParticipantChain: r.ParticipantChain,
		InitiatorAsset: r.InitiatorAsset, ParticipantAsset: r.ParticipantAsset,
		InitiatorAmount: r.InitiatorAmount, ParticipantAmount: r.ParticipantAmount,
		InitiatorAddress: r.InitiatorAddress, ParticipantAddress: r.ParticipantAddress,
		InitiatorSecretHash: r.InitiatorSecretHash, ParticipantSecretHash: r.ParticipantSecretHash,
		InitiatorSecret: r.InitiatorSecret, ParticipantSecret: r.ParticipantSecret,
		Status: r.Status, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
	}
}
