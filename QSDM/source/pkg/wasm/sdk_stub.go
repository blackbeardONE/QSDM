//go:build !cgo
// +build !cgo

package wasm

import (
	"fmt"
	"io/ioutil"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

// init flips qsdm_stub_active{kind="wasm_sdk"} to 1 in non-CGO
// builds where WASM module execution is not available. Operators
// who configure WASM modules will see all NewWASMSDK() calls
// return an error.
func init() {
	stubactive.MarkActive(stubactive.KindWasmSDK)
}

func LoadWASMFromFile(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

type WASMSDK struct{}

func NewWASMSDK(wasmBytes []byte) (*WASMSDK, error) {
	return nil, fmt.Errorf("WASM SDK not available: CGO is disabled. Enable CGO to use WASM modules")
}

func (sdk *WASMSDK) CallFunction(funcName string, params ...interface{}) (interface{}, error) {
	return nil, fmt.Errorf("WASM SDK not available: CGO is disabled")
}

func (sdk *WASMSDK) preflightP2PTransactionJSON(msg []byte) (bool, error) {
	return true, nil
}
