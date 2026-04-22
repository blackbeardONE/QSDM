//go:build cgo
// +build cgo

package wasm

import (
	"fmt"
	"io/ioutil"
)

// This version is used when CGO is enabled but wasmtime DLLs are not available
// It allows the application to use liboqs (for consensus) without wasmtime

func LoadWASMFromFile(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

type WASMSDK struct{}

func NewWASMSDK(wasmBytes []byte) (*WASMSDK, error) {
	return nil, fmt.Errorf("wasmtime not available: DLLs required but not found. Install wasmtime DLLs or build with 'wasmtime_available' tag")
}

func (sdk *WASMSDK) CallFunction(funcName string, params ...interface{}) (interface{}, error) {
	return nil, fmt.Errorf("wasmtime not available")
}

func (sdk *WASMSDK) preflightP2PTransactionJSON(msg []byte) (bool, error) {
	return true, nil
}
