//go:build !cgo && !wasm_wazero
// +build !cgo,!wasm_wazero

package wasm

import (
	"fmt"
	"io/ioutil"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

// Selected when CGO is off AND the wasm_wazero opt-in tag is
// NOT in scope. The wasm_wazero variant (sdk_wazero.go) is the
// Stage-A path that lights up a real WASMSDK backed by wazero
// pure-Go; with that tag this stub never compiles. CGO builds
// without wasmtime DLLs land on sdk_wasmtime_disabled.go
// instead.
//
// Note: qsdm_stub_active{kind="wasm_sdk"} is flipped to 1 by
// NewWASMSDK below — NOT by package init() — because a non-CGO
// validator that doesn't configure any WASM module is operating
// correctly (the contracts engine prefers wazero, which is
// pure-Go, and wallet WASM loading is opt-in). Marking the stub
// active at package load would page on-call for every non-CGO
// build regardless of whether the operator ever asked for WASM
// execution; marking it on attempted construction matches the
// "stub became operationally relevant" semantics the alert is
// supposed to convey.

func LoadWASMFromFile(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

type WASMSDK struct{}

func NewWASMSDK(wasmBytes []byte) (*WASMSDK, error) {
	stubactive.MarkActive(stubactive.KindWasmSDK)
	return nil, fmt.Errorf("WASM SDK not available: CGO is disabled. Enable CGO to use WASM modules")
}

func (sdk *WASMSDK) CallFunction(funcName string, params ...interface{}) (interface{}, error) {
	return nil, fmt.Errorf("WASM SDK not available: CGO is disabled")
}

func (sdk *WASMSDK) preflightP2PTransactionJSON(msg []byte) (bool, error) {
	return true, nil
}
