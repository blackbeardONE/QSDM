package wasm

import (
	_ "embed"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

//go:embed testdata/mldsa87_preflight_golden.json
var mldsa87PreflightGoldenJSON []byte

// TestTryPreflightP2PTransactionJSON_rustWasmModule runs when QSDMPLUS_WASM_PREFLIGHT_MODULE points at
// wasm_module.wasm (see QSDM/scripts/build-wasm-module.sh). CI sets this after `cargo build`.
func TestTryPreflightP2PTransactionJSON_rustWasmModule(t *testing.T) {
	path := strings.TrimSpace(os.Getenv("QSDMPLUS_WASM_PREFLIGHT_MODULE"))
	if path == "" {
		t.Skip("QSDMPLUS_WASM_PREFLIGHT_MODULE not set")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skip("wasm module not found:", path, err)
	}
	t.Cleanup(ResetModulePreflightCache)

	sig := strings.Repeat("ab", 50) // 100 hex chars
	payload, err := json.Marshal(map[string]interface{}{
		"id":           strings.Repeat("c", 32),
		"sender":       "sender_addr_here________________",
		"recipient":    "recipient_addr_here_______________",
		"amount":       1.0,
		"fee":          0.01,
		"geotag":       "US",
		"parent_cells": []string{"aaabbbcccdddeeefffggghhhhiiiijjj", "bbbaaacccdddfffeeeggghhhhjjjjiii"},
		"signature":    sig,
	})
	if err != nil {
		t.Fatal(err)
	}

	ok, err := TryPreflightP2PTransactionJSON(nil, payload)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected validate_raw to accept well-formed wallet JSON")
	}

	bad, _ := json.Marshal(map[string]interface{}{"id": "short"})
	ok2, err2 := TryPreflightP2PTransactionJSON(nil, bad)
	if err2 != nil {
		t.Fatal(err2)
	}
	if ok2 {
		t.Fatal("expected validate_raw to reject malformed payload")
	}

	golden := strings.TrimSpace(string(mldsa87PreflightGoldenJSON))
	ok3, err3 := TryPreflightP2PTransactionJSON(nil, []byte(golden))
	if err3 != nil {
		t.Fatal(err3)
	}
	if !ok3 {
		t.Fatal("expected validate_raw to accept ML-DSA-87 golden (public_key + signature)")
	}
}
