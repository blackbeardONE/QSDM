// +build js,wasm

package wasm

import (
    "io/ioutil"
    "log"
    "syscall/js"
    "sync"
)

type WASMSDK struct {
    wasmBytes    []byte
    wasmModule   js.Value
    wasmInstance js.Value
    mu           sync.Mutex
}

func LoadWASMFromFile(path string) ([]byte, error) {
    return ioutil.ReadFile(path)
}

func NewWASMSDK(wasmBytes []byte) (*WASMSDK, error) {
    sdk := &WASMSDK{
        wasmBytes: wasmBytes,
    }
    err := sdk.instantiate()
    if err != nil {
        return nil, err
    }
    return sdk, nil
}

func (sdk *WASMSDK) instantiate() error {
    sdk.mu.Lock()
    defer sdk.mu.Unlock()

    // Create WebAssembly.Module and Instance using JS APIs
    wasmModule, err := js.Global().Get("WebAssembly").Call("compile", js.TypedArrayOf(sdk.wasmBytes))
    if err != nil {
        return err
    }
    sdk.wasmModule = wasmModule

    imports := js.ValueOf(map[string]interface{}{})
    instance, err := js.Global().Get("WebAssembly").Call("instantiate", wasmModule, imports)
    if err != nil {
        return err
    }
    sdk.wasmInstance = instance.Get("instance")
    return nil
}

func (sdk *WASMSDK) CallFunction(funcName string, args ...[]byte) ([]byte, error) {
    sdk.mu.Lock()
    defer sdk.mu.Unlock()

    if sdk.wasmInstance.IsUndefined() {
        return nil, nil
    }

    exports := sdk.wasmInstance.Get("exports")
    fn := exports.Get(funcName)
    if fn.IsUndefined() {
        log.Printf("WASM function %s not found", funcName)
        return nil, nil
    }

    // For simplicity, assume no arguments and no return value
    fn.Invoke()

    return nil, nil
}
