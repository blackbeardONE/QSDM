package chain

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/blackbeardONE/QSDM/pkg/fileutil"
)

const stakingPersistVersion = 1

type stakingPersistBond struct {
	Delegator string  `json:"d"`
	Validator string  `json:"v"`
	Amount    float64 `json:"a"`
}

type stakingPersistDoc struct {
	V      int                  `json:"v"`
	Bonds  []stakingPersistBond `json:"bonds"`
	Unbond []unbondEntry        `json:"unbond"`
}

// LoadOrNewStakingLedger loads staking state from path, or returns an empty ledger when the file is missing.
func LoadOrNewStakingLedger(path string) (*StakingLedger, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewStakingLedger(), nil
		}
		return nil, err
	}
	doc, decodeErr := decodeStakingPersistDoc(b)
	if decodeErr != nil {
		backup, backupErr := os.ReadFile(path + ".last-good")
		if backupErr != nil {
			return nil, fmt.Errorf("%w (backup unavailable: %v)", decodeErr, backupErr)
		}
		doc, backupErr = decodeStakingPersistDoc(backup)
		if backupErr != nil {
			return nil, fmt.Errorf("%w (backup invalid: %v)", decodeErr, backupErr)
		}
		if repairErr := fileutil.WriteFileAtomic(path, backup, 0o600); repairErr != nil {
			return nil, fmt.Errorf("recover staking ledger from backup: %w", repairErr)
		}
	}
	sl := NewStakingLedger()
	sl.mu.Lock()
	for _, b := range doc.Bonds {
		if b.Amount <= 0 || b.Delegator == "" || b.Validator == "" {
			continue
		}
		if sl.delegatorIndex[b.Delegator] == nil {
			sl.delegatorIndex[b.Delegator] = make(map[string]float64)
		}
		sl.delegatorIndex[b.Delegator][b.Validator] += b.Amount
		sl.delegated[b.Validator] += b.Amount
	}
	sl.unbond = append([]unbondEntry(nil), doc.Unbond...)
	sl.mu.Unlock()
	return sl, nil
}

// SaveStakingLedger writes the ledger to path (atomic replace via temp file).
func SaveStakingLedger(sl *StakingLedger, path string) error {
	if sl == nil || path == "" {
		return nil
	}
	sl.mu.RLock()
	bonds := make([]stakingPersistBond, 0)
	for d, m := range sl.delegatorIndex {
		for v, a := range m {
			if a <= 0 {
				continue
			}
			bonds = append(bonds, stakingPersistBond{Delegator: d, Validator: v, Amount: a})
		}
	}
	unbondCopy := append([]unbondEntry(nil), sl.unbond...)
	sl.mu.RUnlock()

	doc := stakingPersistDoc{V: stakingPersistVersion, Bonds: bonds, Unbond: unbondCopy}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := fileutil.WriteFileAtomic(path+".last-good", out, 0o600); err != nil {
		return fmt.Errorf("write staking ledger backup: %w", err)
	}
	if err := fileutil.WriteFileAtomic(path, out, 0o600); err != nil {
		return fmt.Errorf("write staking ledger: %w", err)
	}
	return nil
}

func decodeStakingPersistDoc(data []byte) (stakingPersistDoc, error) {
	var doc stakingPersistDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return stakingPersistDoc{}, fmt.Errorf("staking ledger json: %w", err)
	}
	if doc.V != stakingPersistVersion {
		return stakingPersistDoc{}, fmt.Errorf("unsupported staking ledger version %d", doc.V)
	}
	return doc, nil
}
