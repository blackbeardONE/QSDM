//go:build cgo && !wasm_wazero
// +build cgo,!wasm_wazero

package wasm

import (
	"fmt"
	"io/ioutil"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

// This version is used when CGO is enabled, the operator is NOT
// opting in to the wazero backend (sdk_wazero.go via the
// wasm_wazero tag), and wasmtime DLLs are not available. It
// allows the application to use liboqs (for consensus) without
// wasmtime. With wasm_wazero on, the wazero pure-Go SDK takes
// over the WASMSDK type entirely.
//
// Like sdk_stub.go (the non-CGO counterpart), we flip
// qsdm_stub_active{kind="wasm_sdk"} only inside NewWASMSDK, not in
// package init() — a CGO build that never asks for WASM execution
// is operating correctly and should not page on-call.

func LoadWASMFromFile(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

type WASMSDK struct{}

func NewWASMSDK(wasmBytes []byte) (*WASMSDK, error) {
	stubactive.MarkActive(stubactive.KindWasmSDK)
	return nil, fmt.Errorf("wasmtime not available: DLLs required but not found. Install wasmtime DLLs or build with 'wasmtime_available' tag")
}

func (sdk *WASMSDK) CallFunction(funcName string, params ...interface{}) (interface{}, error) {
	return nil, fmt.Errorf("wasmtime not available")
}

func (sdk *WASMSDK) preflightP2PTransactionJSON(msg []byte) (bool, error) {
	return true, nil
}
