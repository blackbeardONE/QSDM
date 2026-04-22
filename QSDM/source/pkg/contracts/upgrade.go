package contracts

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// ContractVersion records a historical version of a contract's code + ABI.
type ContractVersion struct {
	Version    int       `json:"version"`
	Code       []byte    `json:"code"`
	ABI        *ABI      `json:"abi"`
	UpgradedAt time.Time `json:"upgraded_at"`
	UpgradedBy string    `json:"upgraded_by"`
	Reason     string    `json:"reason"`
}

// UpgradePolicy defines who can upgrade a contract and under what conditions.
type UpgradePolicy struct {
	AllowOwnerUpgrade bool     `json:"allow_owner_upgrade"`
	AllowedUpgraders  []string `json:"allowed_upgraders"` // addresses allowed to upgrade (besides owner)
	RequireMultiSig   bool     `json:"require_multi_sig"`
	FreezeAfterV      int      `json:"freeze_after_v"` // no upgrades after this version (0 = unlimited)
}

// DefaultUpgradePolicy allows the contract owner to upgrade with no limits.
func DefaultUpgradePolicy() UpgradePolicy {
	return UpgradePolicy{AllowOwnerUpgrade: true}
}

// UpgradeManager tracks contract version history and enforces upgrade policies.
type UpgradeManager struct {
	mu       sync.RWMutex
	engine   *ContractEngine
	history  map[string][]ContractVersion // contractID -> version history
	policies map[string]UpgradePolicy     // contractID -> policy
}

// NewUpgradeManager creates an upgrade manager for a contract engine.
func NewUpgradeManager(engine *ContractEngine) *UpgradeManager {
	return &UpgradeManager{
		engine:   engine,
		history:  make(map[string][]ContractVersion),
		policies: make(map[string]UpgradePolicy),
	}
}

// SetPolicy sets the upgrade policy for a contract.
func (um *UpgradeManager) SetPolicy(contractID string, policy UpgradePolicy) {
	um.mu.Lock()
	defer um.mu.Unlock()
	um.policies[contractID] = policy
}

// GetPolicy returns the upgrade policy for a contract.
func (um *UpgradeManager) GetPolicy(contractID string) UpgradePolicy {
	um.mu.RLock()
	defer um.mu.RUnlock()
	if p, ok := um.policies[contractID]; ok {
		return p
	}
	return DefaultUpgradePolicy()
}

// Upgrade replaces a contract's code and ABI while preserving state.
// Records the previous version in history.
func (um *UpgradeManager) Upgrade(ctx context.Context, contractID string, newCode []byte, newABI *ABI, upgraderAddr string, reason string) (*Contract, error) {
	um.mu.Lock()
	defer um.mu.Unlock()

	policy := um.policies[contractID]
	if _, ok := um.policies[contractID]; !ok {
		policy = DefaultUpgradePolicy()
	}

	if !um.isAuthorised(policy, contractID, upgraderAddr) {
		return nil, fmt.Errorf("address %s is not authorised to upgrade contract %s", upgraderAddr, contractID)
	}

	versions := um.history[contractID]
	nextVersion := len(versions) + 2 // version 1 is original deploy, so first upgrade = v2

	if policy.FreezeAfterV > 0 && nextVersion > policy.FreezeAfterV {
		return nil, fmt.Errorf("contract %s is frozen at version %d", contractID, policy.FreezeAfterV)
	}

	um.engine.mu.Lock()
	contract, exists := um.engine.contracts[contractID]
	if !exists {
		um.engine.mu.Unlock()
		return nil, fmt.Errorf("contract %s not found", contractID)
	}

	// Archive current version
	archiveV := ContractVersion{
		Version:    nextVersion - 1,
		Code:       contract.Code,
		ABI:        contract.ABI,
		UpgradedAt: time.Now(),
		UpgradedBy: upgraderAddr,
		Reason:     reason,
	}
	um.history[contractID] = append(um.history[contractID], archiveV)

	// Swap code and ABI, preserve state
	contract.Code = newCode
	contract.ABI = newABI

	// Re-initialise wazero runtime if code is valid WASM
	if rt, ok := um.engine.contractRTs[contractID]; ok {
		rt.Close()
		delete(um.engine.contractRTs, contractID)
	}
	if len(newCode) > 4 && newCode[0] == 0x00 && newCode[1] == 0x61 && newCode[2] == 0x73 && newCode[3] == 0x6d {
		if rt, err := newWazeroRuntimeSafe(newCode); err == nil {
			um.engine.contractRTs[contractID] = rt
		}
	}

	um.engine.mu.Unlock()

	return contract, nil
}

// Rollback reverts a contract to a specific version, preserving current state.
func (um *UpgradeManager) Rollback(ctx context.Context, contractID string, targetVersion int, rollerAddr string) (*Contract, error) {
	um.mu.Lock()
	defer um.mu.Unlock()

	versions := um.history[contractID]
	var target *ContractVersion
	for i := range versions {
		if versions[i].Version == targetVersion {
			target = &versions[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("version %d not found for contract %s", targetVersion, contractID)
	}

	policy := um.policies[contractID]
	if _, ok := um.policies[contractID]; !ok {
		policy = DefaultUpgradePolicy()
	}
	if !um.isAuthorised(policy, contractID, rollerAddr) {
		return nil, fmt.Errorf("address %s is not authorised to rollback contract %s", rollerAddr, contractID)
	}

	um.engine.mu.Lock()
	contract, exists := um.engine.contracts[contractID]
	if !exists {
		um.engine.mu.Unlock()
		return nil, fmt.Errorf("contract %s not found", contractID)
	}

	contract.Code = target.Code
	contract.ABI = target.ABI

	if rt, ok := um.engine.contractRTs[contractID]; ok {
		rt.Close()
		delete(um.engine.contractRTs, contractID)
	}
	if len(target.Code) > 4 && target.Code[0] == 0x00 && target.Code[1] == 0x61 && target.Code[2] == 0x73 && target.Code[3] == 0x6d {
		if rt, err := newWazeroRuntimeSafe(target.Code); err == nil {
			um.engine.contractRTs[contractID] = rt
		}
	}

	um.engine.mu.Unlock()

	return contract, nil
}

// VersionHistory returns the upgrade history for a contract.
func (um *UpgradeManager) VersionHistory(contractID string) []ContractVersion {
	um.mu.RLock()
	defer um.mu.RUnlock()
	h := um.history[contractID]
	out := make([]ContractVersion, len(h))
	copy(out, h)
	return out
}

// CurrentVersion returns the current version number of a contract.
func (um *UpgradeManager) CurrentVersion(contractID string) int {
	um.mu.RLock()
	defer um.mu.RUnlock()
	return len(um.history[contractID]) + 1
}

func (um *UpgradeManager) isAuthorised(policy UpgradePolicy, contractID, addr string) bool {
	um.engine.mu.RLock()
	contract, exists := um.engine.contracts[contractID]
	um.engine.mu.RUnlock()

	if !exists {
		return false
	}

	if policy.AllowOwnerUpgrade && contract.Owner == addr {
		return true
	}
	for _, a := range policy.AllowedUpgraders {
		if a == addr {
			return true
		}
	}
	return false
}
