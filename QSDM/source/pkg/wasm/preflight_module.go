package wasm

import (
	"fmt"
	"os"
	"strings"
	"sync"
)

var (
	preflightModMu   sync.Mutex
	preflightModPath string
	preflightModRT   *WazeroRuntime
)

// ResetModulePreflightCache closes any cached validator module (for tests).
func ResetModulePreflightCache() {
	preflightModMu.Lock()
	defer preflightModMu.Unlock()
	if preflightModRT != nil {
		_ = preflightModRT.Close()
		preflightModRT = nil
	}
	preflightModPath = ""
}

func modulePreflightFromPath(path string, msg []byte) (bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return true, nil
	}
	preflightModMu.Lock()
	defer preflightModMu.Unlock()

	if preflightModRT != nil && preflightModPath == path {
		return preflightModRT.CallValidateRaw(msg)
	}
	if preflightModRT != nil {
		_ = preflightModRT.Close()
		preflightModRT = nil
		preflightModPath = ""
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("wasm preflight module read %q: %w", path, err)
	}
	rt, err := NewWazeroRuntime(data)
	if err != nil {
		return false, fmt.Errorf("wasm preflight instantiate %q: %w", path, err)
	}
	if !rt.HasFunction("validate_raw") {
		_ = rt.Close()
		return false, fmt.Errorf("wasm preflight module %q: no export validate_raw", path)
	}
	ok, err := rt.CallValidateRaw(msg)
	if err != nil {
		_ = rt.Close()
		return false, err
	}
	preflightModRT = rt
	preflightModPath = path
	return ok, nil
}
