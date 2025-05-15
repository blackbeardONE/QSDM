package wasm

import (
    "context"
    "fmt"
    "github.com/wasmerio/wasmer-go/wasmer"
    "io/ioutil"
    "log"
)

// WASMSDK provides interfaces to load and execute WASM modules for wallet and validator integration.
type WASMSDK struct {
    engine  *wasmer.Engine
    store   *wasmer.Store
    module  *wasmer.Module
    instance *wasmer.Instance
}

// NewWASMSDK creates a new WASMSDK instance.
func NewWASMSDK(wasmBytes []byte) (*WASMSDK, error) {
    engine := wasmer.NewEngine()
    store := wasmer.NewStore(engine)

    module, err := wasmer.NewModule(store, wasmBytes)
    if err != nil {
        return nil, fmt.Errorf("failed to compile WASM module: %w", err)
    }

    importObject := wasmer.NewImportObject()

    // No imports needed for non-wasm-bindgen module
    instance, err := wasmer.NewInstance(module, importObject)
    if err != nil {
        return nil, fmt.Errorf("failed to instantiate WASM module: %w", err)
    }

    return &WASMSDK{
        engine:  engine,
        store:   store,
        module:  module,
        instance: instance,
    }, nil
}

// LoadWASMFromFile loads WASM bytes from a file.
func LoadWASMFromFile(path string) ([]byte, error) {
    data, err := ioutil.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("failed to read WASM file: %w", err)
    }
    return data, nil
}

// CallFunction calls an exported function in the WASM module with given parameters.
func (sdk *WASMSDK) CallFunction(funcName string, params ...interface{}) (interface{}, error) {
    fn, err := sdk.instance.Exports.GetFunction(funcName)
    if err != nil {
        return nil, fmt.Errorf("function %s not found: %w", funcName, err)
    }

    result, err := fn(params...)
    if err != nil {
        return nil, fmt.Errorf("failed to call function %s: %w", funcName, err)
    }
    return result, nil
}

// Example usage: Load a wallet WASM module and call its 'sign' function.
func ExampleUsage(ctx context.Context, wasmBytes []byte) {
    sdk, err := NewWASMSDK(wasmBytes)
    if err != nil {
        log.Fatalf("Failed to create WASM SDK: %v", err)
    }

    // Call 'sign' function with example parameters
    result, err := sdk.CallFunction("sign", []byte("transaction data"))
    if err != nil {
        log.Fatalf("Failed to call sign function: %v", err)
    }

    log.Printf("Sign function result: %v", result)
}
