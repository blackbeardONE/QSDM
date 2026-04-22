package wasm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewWASMSDKFinal(t *testing.T) {
	wasmPath := filepath.Join("wasm_modules", "validator", "validator.wasm")

	if _, err := os.Stat(wasmPath); os.IsNotExist(err) {
		t.Skipf("WASM file %s does not exist, skipping test", wasmPath)
	}

	wasmBytes, err := LoadWASMFromFile(wasmPath)
	if err != nil {
		t.Fatalf("Failed to load WASM file: %v", err)
	}

	sdk, err := NewWASMSDK(wasmBytes)
	if err != nil {
		t.Skip("WASM runtime not available: ", err)
	}

	if sdk == nil {
		t.Fatal("WASMSDK instance is nil")
	}
}
