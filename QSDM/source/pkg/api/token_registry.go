package api

import (
	"encoding/json"
	"fmt"
	"os"
)

type tokenRegistryFile struct {
	Tokens []TokenInfo `json:"tokens"`
}

// SaveTokenRegistry writes the user-created token list to path as JSON.
func (h *Handlers) SaveTokenRegistry(path string) error {
	h.tokenRegistryMu.RLock()
	reg := tokenRegistryFile{Tokens: make([]TokenInfo, len(h.tokenRegistry))}
	copy(reg.Tokens, h.tokenRegistry)
	h.tokenRegistryMu.RUnlock()

	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal token registry: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write token registry: %w", err)
	}
	return os.Rename(tmp, path)
}

// LoadTokenRegistry reads previously created tokens from path.
// Missing file is not an error (returns 0, nil).
func (h *Handlers) LoadTokenRegistry(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read token registry: %w", err)
	}

	var reg tokenRegistryFile
	if err := json.Unmarshal(data, &reg); err != nil {
		return 0, fmt.Errorf("unmarshal token registry: %w", err)
	}

	h.tokenRegistryMu.Lock()
	h.tokenRegistry = append(h.tokenRegistry, reg.Tokens...)
	h.tokenRegistryMu.Unlock()

	return len(reg.Tokens), nil
}
