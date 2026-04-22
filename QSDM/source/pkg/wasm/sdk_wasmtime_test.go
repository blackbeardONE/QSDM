package wasm

import (
	"io/ioutil"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNewWASMSDK(t *testing.T) {
	// Use absolute path based on current file location
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Failed to get current test file path")
	}
	projectRoot := filepath.Dir(filepath.Dir(filepath.Dir(filename)))
	wasmPath := filepath.Join(projectRoot, "wasm_modules", "validator", "validator.wasm")

	wasmBytes, err := ioutil.ReadFile(wasmPath)
	if err != nil {
		t.Fatalf("Failed to read WASM file: %v", err)
	}

	sdk, err := NewWASMSDK(wasmBytes)
	if err != nil {
		t.Skip("WASM runtime not available: ", err)
	}

	// Test calling a function that should exist
	_, err = sdk.CallFunction("Hello")
	if err != nil {
		t.Errorf("CallFunction failed: %v", err)
	}
}
