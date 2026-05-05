//go:build cgo
// +build cgo

package wasm

import (
	"fmt"
	"io/ioutil"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

// This version is used when CGO is enabled but wasmtime DLLs are not available
// It allows the application to use liboqs (for consensus) without wasmtime.
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
