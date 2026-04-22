//go:build !cgo
// +build !cgo

package wasm

import (
	"fmt"
	"io/ioutil"
)

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
