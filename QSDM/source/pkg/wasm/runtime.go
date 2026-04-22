package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	wazeroapi "github.com/tetratelabs/wazero/api"
)

// WazeroRuntime is a pure-Go WASM runtime that requires no CGO or DLLs.
type WazeroRuntime struct {
	rt      wazero.Runtime
	module  wazeroapi.Module
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
}

// NewWazeroRuntime compiles and instantiates a WASM module from bytecode.
// Pass nil code to create a runtime-only handle (useful for deferred module loading).
func NewWazeroRuntime(code []byte) (*WazeroRuntime, error) {
	ctx, cancel := context.WithCancel(context.Background())

	rt := wazero.NewRuntime(ctx)

	wr := &WazeroRuntime{
		rt:     rt,
		ctx:    ctx,
		cancel: cancel,
	}

	if len(code) > 0 {
		if err := wr.LoadModule(code); err != nil {
			rt.Close(ctx)
			cancel()
			return nil, err
		}
	}

	return wr, nil
}

// LoadModule compiles and instantiates a WASM module.
func (wr *WazeroRuntime) LoadModule(code []byte) error {
	wr.mu.Lock()
	defer wr.mu.Unlock()

	compiled, err := wr.rt.CompileModule(wr.ctx, code)
	if err != nil {
		return fmt.Errorf("wazero compile: %w", err)
	}

	mod, err := wr.rt.InstantiateModule(wr.ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		return fmt.Errorf("wazero instantiate: %w", err)
	}

	wr.module = mod
	return nil
}

// Call invokes an exported WASM function by name with JSON-encoded args.
// Returns the raw result values from the WASM function.
func (wr *WazeroRuntime) Call(funcName string, argsJSON []byte) (interface{}, error) {
	wr.mu.Lock()
	defer wr.mu.Unlock()

	if wr.module == nil {
		return nil, fmt.Errorf("no WASM module loaded")
	}

	fn := wr.module.ExportedFunction(funcName)
	if fn == nil {
		return nil, fmt.Errorf("function %q not exported by WASM module", funcName)
	}

	// Determine how many parameters the function expects
	paramTypes := fn.Definition().ParamTypes()
	var callArgs []uint64

	if len(paramTypes) > 0 && len(argsJSON) > 0 {
		var nums []float64
		if err := json.Unmarshal(argsJSON, &nums); err == nil {
			for i, n := range nums {
				if i >= len(paramTypes) {
					break
				}
				switch paramTypes[i] {
				case wazeroapi.ValueTypeI32:
					callArgs = append(callArgs, wazeroapi.EncodeI32(int32(n)))
				case wazeroapi.ValueTypeI64:
					callArgs = append(callArgs, uint64(int64(n)))
				case wazeroapi.ValueTypeF32:
					callArgs = append(callArgs, wazeroapi.EncodeF32(float32(n)))
				case wazeroapi.ValueTypeF64:
					callArgs = append(callArgs, wazeroapi.EncodeF64(n))
				default:
					callArgs = append(callArgs, uint64(n))
				}
			}
		}
	}

	results, err := fn.Call(wr.ctx, callArgs...)
	if err != nil {
		return nil, fmt.Errorf("wazero call %s: %w", funcName, err)
	}

	if len(results) == 0 {
		return nil, nil
	}
	if len(results) == 1 {
		return results[0], nil
	}
	return results, nil
}

// CallValidateRaw invokes export `validate_raw(tx_ptr, tx_len) -> i32` (1 = valid, 0 = invalid).
// Payload bytes are written into guest linear memory at a fixed base offset; the module must accept that layout
// (matches `wasm_module` / `#[no_mangle] pub extern "C" fn validate_raw`).
// If `validate_raw` is not exported, returns (true, nil) so callers can treat validation as optional.
func (wr *WazeroRuntime) CallValidateRaw(payload []byte) (bool, error) {
	wr.mu.Lock()
	defer wr.mu.Unlock()

	if wr.module == nil {
		return false, fmt.Errorf("no WASM module loaded")
	}
	fn := wr.module.ExportedFunction("validate_raw")
	if fn == nil {
		return true, nil
	}
	mem := wr.module.Memory()
	if mem == nil {
		return false, fmt.Errorf("no linear memory")
	}
	const base uint32 = 65536
	need := uint64(base) + uint64(len(payload))
	for uint64(mem.Size()) < need {
		_, ok := mem.Grow(1)
		if !ok {
			return false, fmt.Errorf("linear memory grow failed")
		}
	}
	if !mem.Write(base, payload) {
		return false, fmt.Errorf("linear memory write failed")
	}
	results, err := fn.Call(wr.ctx, uint64(base), uint64(len(payload)))
	if err != nil {
		return false, fmt.Errorf("validate_raw: %w", err)
	}
	if len(results) == 0 {
		return false, fmt.Errorf("validate_raw: empty result")
	}
	return results[0] != 0, nil
}

// HasFunction checks whether the module exports a function with the given name.
func (wr *WazeroRuntime) HasFunction(funcName string) bool {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	if wr.module == nil {
		return false
	}
	return wr.module.ExportedFunction(funcName) != nil
}

// Close releases all resources.
func (wr *WazeroRuntime) Close() error {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	err := wr.rt.Close(wr.ctx)
	wr.cancel()
	return err
}
