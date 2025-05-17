package tests

// +build !js

import (
    "testing"
    "github.com/blackbeardONE/QSDM/pkg/wasm"
    "io/ioutil"
)

func TestWASMWalletLoad(t *testing.T) {
    wasmPath := "../wasm_modules/wallet/wallet.wasm"
    wasmBytes, err := ioutil.ReadFile(wasmPath)
    if err != nil {
        t.Fatalf("Failed to read wallet WASM file: %v", err)
    }
    sdk, err := wasm.NewWASMSDK(wasmBytes)
    if err != nil {
        t.Fatalf("Failed to create WASM SDK for wallet: %v", err)
    }
    if sdk == nil {
        t.Fatalf("WASM SDK is nil")
    }
}

func TestWASMValidatorLoad(t *testing.T) {
    wasmPath := "../wasm_modules/validator/validator.wasm"
    wasmBytes, err := ioutil.ReadFile(wasmPath)
    if err != nil {
        t.Fatalf("Failed to read validator WASM file: %v", err)
    }
    sdk, err := wasm.NewWASMSDK(wasmBytes)
    if err != nil {
        t.Fatalf("Failed to create WASM SDK for validator: %v", err)
    }
    if sdk == nil {
        t.Fatalf("WASM SDK is nil")
    }
}

// Additional tests for calling WASM functions can be added here once JS environment is available
