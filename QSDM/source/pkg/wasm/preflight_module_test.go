package wasm

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTryPreflightP2PTransactionJSON_missingModuleFile(t *testing.T) {
	t.Setenv("QSDM_WASM_PREFLIGHT_MODULE", filepath.Join(t.TempDir(), "nonexistent.wasm"))
	t.Cleanup(ResetModulePreflightCache)

	_, err := TryPreflightP2PTransactionJSON(nil, []byte(`{"x":1}`))
	if err == nil || !strings.Contains(err.Error(), "wasm preflight") {
		t.Fatalf("expected wasm preflight read error, got %v", err)
	}
}

func TestTryPreflightP2PTransactionJSON_noEnvNoSDK(t *testing.T) {
	t.Setenv("QSDM_WASM_PREFLIGHT_MODULE", "")
	t.Cleanup(ResetModulePreflightCache)

	ok, err := TryPreflightP2PTransactionJSON(nil, []byte(`{"id":"`+strings.Repeat("a", 32)+`"}`))
	if err != nil || !ok {
		t.Fatalf("want ok=true err=nil, got ok=%v err=%v", ok, err)
	}
}
