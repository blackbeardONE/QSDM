package contracts

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/wasm"
)

// persistedContract is the JSON-serializable representation of a deployed contract.
type persistedContract struct {
	ID            string                 `json:"id"`
	Code          []byte                 `json:"code"`
	ABI           *ABI                   `json:"abi"`
	DeployedAt    time.Time              `json:"deployed_at"`
	Owner         string                 `json:"owner"`
	State         map[string]interface{} `json:"state"`
	GasUsedDeploy int64                  `json:"gas_used_deploy"`
	TotalGasUsed  int64                  `json:"total_gas_used"`
}

type persistedStore struct {
	Contracts []persistedContract `json:"contracts"`
	SavedAt   time.Time           `json:"saved_at"`
}

// SaveContracts persists all deployed contracts to a JSON file.
func (ce *ContractEngine) SaveContracts(path string) error {
	ce.mu.RLock()
	defer ce.mu.RUnlock()

	store := persistedStore{
		Contracts: make([]persistedContract, 0, len(ce.contracts)),
		SavedAt:   time.Now(),
	}
	for _, c := range ce.contracts {
		store.Contracts = append(store.Contracts, persistedContract{
			ID:            c.ID,
			Code:          c.Code,
			ABI:           c.ABI,
			DeployedAt:    c.DeployedAt,
			Owner:         c.Owner,
			State:         c.State,
			GasUsedDeploy: c.GasUsedDeploy,
			TotalGasUsed:  c.TotalGasUsed,
		})
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal contracts: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// LoadContracts restores contracts from a JSON file. Returns the number loaded.
func (ce *ContractEngine) LoadContracts(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read contracts file: %w", err)
	}

	var store persistedStore
	if err := json.Unmarshal(data, &store); err != nil {
		return 0, fmt.Errorf("unmarshal contracts: %w", err)
	}

	ce.mu.Lock()
	defer ce.mu.Unlock()

	loaded := 0
	for _, pc := range store.Contracts {
		if _, exists := ce.contracts[pc.ID]; exists {
			continue
		}
		c := &Contract{
			ID:            pc.ID,
			Code:          pc.Code,
			ABI:           pc.ABI,
			DeployedAt:    pc.DeployedAt,
			Owner:         pc.Owner,
			State:         pc.State,
			GasUsedDeploy: pc.GasUsedDeploy,
			TotalGasUsed:  pc.TotalGasUsed,
		}
		if c.State == nil {
			c.State = make(map[string]interface{})
		}
		ce.contracts[pc.ID] = c

		// Restore per-contract wazero runtime if code is valid WASM
		if len(c.Code) > 4 && c.Code[0] == 0x00 && c.Code[1] == 0x61 && c.Code[2] == 0x73 && c.Code[3] == 0x6d {
			if rt, rtErr := newWazeroRuntimeSafe(c.Code); rtErr == nil {
				ce.contractRTs[pc.ID] = rt
			}
		}
		loaded++
	}
	return loaded, nil
}

func newWazeroRuntimeSafe(code []byte) (*wasm.WazeroRuntime, error) {
	return wasm.NewWazeroRuntime(code)
}

// ContractAutoSaver periodically saves contract state.
type ContractAutoSaver struct {
	engine   *ContractEngine
	path     string
	interval time.Duration
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// NewContractAutoSaver creates an auto-saver that flushes contracts on the given interval.
func NewContractAutoSaver(engine *ContractEngine, path string, interval time.Duration) *ContractAutoSaver {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &ContractAutoSaver{
		engine:   engine,
		path:     path,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start begins the auto-save loop in a goroutine.
func (cas *ContractAutoSaver) Start() {
	cas.wg.Add(1)
	go func() {
		defer cas.wg.Done()
		ticker := time.NewTicker(cas.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				cas.engine.SaveContracts(cas.path)
			case <-cas.stopCh:
				cas.engine.SaveContracts(cas.path)
				return
			}
		}
	}()
}

// Stop halts auto-saving and does a final flush.
func (cas *ContractAutoSaver) Stop() {
	close(cas.stopCh)
	cas.wg.Wait()
}
