package wasm

import (
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/envcompat"
)

// TryPreflightP2PTransactionJSON optionally validates raw JSON transaction bytes.
//
// If QSDM_WASM_PREFLIGHT_MODULE is set to a filesystem path of a WASM module exporting
// validate_raw(tx_ptr, tx_len) -> i32 (see wasm_module), validation runs via wazero (no CGO).
// (The pre-rebrand QSDMPLUS_WASM_PREFLIGHT_MODULE env var is no longer read; pkg/envcompat
// is now a no-op trim helper after db9b590.)
// Otherwise, when sdk is non-nil, sdk.preflightP2PTransactionJSON is used (may be a no-op
// stub). When neither applies, returns (true, nil).
func TryPreflightP2PTransactionJSON(sdk *WASMSDK, msg []byte) (bool, error) {
	if len(msg) == 0 {
		return true, nil
	}
	if p := strings.TrimSpace(envcompat.Lookup("QSDM_WASM_PREFLIGHT_MODULE", "QSDM_WASM_PREFLIGHT_MODULE")); p != "" {
		return modulePreflightFromPath(p, msg)
	}
	if sdk == nil {
		return true, nil
	}
	return sdk.preflightP2PTransactionJSON(msg)
}
