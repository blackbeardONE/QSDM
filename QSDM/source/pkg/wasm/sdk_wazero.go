//go:build !js || !wasm
// +build !js !wasm

// Package wasm — wazero-backed WASMSDK.
//
// This file is the default WASMSDK backend as of Stage B
// (2026-05-06). It supplies a real *WASMSDK backed by
// github.com/tetratelabs/wazero (pure-Go, already a direct
// dependency for the QSDM_WASM_PREFLIGHT_MODULE env hook).
//
// Build-tag selection:
//
//   - This file: `!js || !wasm` — every native target the
//     QSDM binary ships on (linux, windows, darwin, freebsd
//     amd64/arm64). The inverse of wasm.go's `js && wasm`
//     guard avoids a duplicate-definition collision with the
//     legacy Go-to-browser-WASM build.
//
//   - wasm.go: `js && wasm` — Go compiled to WebAssembly for
//     a browser host. Untouched by Stage B.
//
// Stage progression:
//
//   - Stage A (commit 57ef2cf, 2026-05-06) added this file
//     behind the opt-in `wasm_wazero` build tag so parity
//     tests could soak in CI without changing operational
//     behaviour. The two stubs (`sdk_stub.go`,
//     `sdk_wasmtime_disabled.go`) carried `&& !wasm_wazero`
//     to yield to wazero when the tag was on.
//
//   - Stage B (this commit) flips the default: every native
//     build now uses this backend automatically, and both
//     stubs are deleted. The `wasm_wazero` build tag is now
//     a no-op — kept as a no-op alias for one release for
//     compatibility with any external build scripts that
//     pass it. The `qsdm_stub_active{kind="wasm_sdk"}` gauge
//     remains in the registry for forward compatibility but
//     no code path flips it on under any supported build
//     configuration any more.

package wasm

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"sync"

	"github.com/tetratelabs/wazero"
	wazeroapi "github.com/tetratelabs/wazero/api"
)

// LoadWASMFromFile reads WASM bytecode from disk. Identical
// to the same-named helper in sdk_stub.go and
// sdk_wasmtime_disabled.go — kept here so the wazero build
// has no missing symbols when those files are excluded by
// build tag.
func LoadWASMFromFile(path string) ([]byte, error) {
	return ioutil.ReadFile(path)
}

// WASMSDK is the wazero-backed implementation of the contracts
// engine's WASM execution surface. The struct intentionally
// matches the field-naming and method shape that the stub
// versions present so callers (cmd/qsdm/main.go,
// pkg/contracts/engine.go) compile under either build mode.
type WASMSDK struct {
	mu     sync.Mutex
	rt     wazero.Runtime
	module wazeroapi.Module
	ctx    context.Context
	cancel context.CancelFunc
}

// NewWASMSDK compiles and instantiates the supplied WASM
// bytecode. Empty bytecode is rejected — a SDK with no module
// has no useful surface, and the prior stub builds returned
// errors for any input including empty, so allowing it here
// would be a behaviour change beyond Stage A's scope.
func NewWASMSDK(wasmBytes []byte) (*WASMSDK, error) {
	if len(wasmBytes) == 0 {
		return nil, fmt.Errorf("wasm sdk: empty bytecode")
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt := wazero.NewRuntime(ctx)

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		_ = rt.Close(ctx)
		cancel()
		return nil, fmt.Errorf("wasm sdk (wazero): compile: %w", err)
	}
	mod, err := rt.InstantiateModule(ctx, compiled, wazero.NewModuleConfig())
	if err != nil {
		_ = rt.Close(ctx)
		cancel()
		return nil, fmt.Errorf("wasm sdk (wazero): instantiate: %w", err)
	}

	return &WASMSDK{
		rt:     rt,
		module: mod,
		ctx:    ctx,
		cancel: cancel,
	}, nil
}

// Close releases the embedded wazero runtime. Safe to call
// multiple times. The stub variants of WASMSDK do not expose
// Close (their zero-value struct has nothing to release), so
// callers that compile under either backend should use a
// type-assertion or build-tag-gated cleanup if they need to
// invoke it. Currently no in-tree caller does — the SDK is
// constructed once at process start and lives for the
// lifetime of the binary.
func (sdk *WASMSDK) Close() error {
	if sdk == nil {
		return nil
	}
	sdk.mu.Lock()
	defer sdk.mu.Unlock()
	if sdk.rt == nil {
		return nil
	}
	err := sdk.rt.Close(sdk.ctx)
	sdk.cancel()
	sdk.rt = nil
	sdk.module = nil
	return err
}

// CallFunction invokes an exported function by name. The
// signature `(funcName string, params ...interface{})` is
// preserved from the stub variants so existing callers in
// pkg/contracts/engine.go and pkg/contracts/readonly.go
// compile unchanged. params are interpreted as numeric scalars
// matching the WASM function's parameter types; pkg/contracts
// passes a single JSON-encoded string, which we parse as a
// numeric array per the WazeroRuntime.Call convention.
func (sdk *WASMSDK) CallFunction(funcName string, params ...interface{}) (interface{}, error) {
	sdk.mu.Lock()
	defer sdk.mu.Unlock()
	if sdk.module == nil {
		return nil, fmt.Errorf("wasm sdk (wazero): no module instantiated")
	}
	fn := sdk.module.ExportedFunction(funcName)
	if fn == nil {
		return nil, fmt.Errorf("wasm sdk (wazero): function %q not exported", funcName)
	}

	paramTypes := fn.Definition().ParamTypes()
	callArgs := make([]uint64, 0, len(paramTypes))

	// Decode params. The contracts engine passes a single
	// string-encoded JSON array of numbers (see
	// pkg/contracts/engine.go:277), which is the case we
	// optimise for. We accept either that, or direct numeric
	// arguments, to stay symmetric with WazeroRuntime.Call's
	// interface.
	switch len(params) {
	case 0:
		// no-op — function may be parameterless.
	case 1:
		if s, ok := params[0].(string); ok && len(s) > 0 {
			var nums []float64
			if err := json.Unmarshal([]byte(s), &nums); err == nil {
				callArgs = encodeWasmArgs(paramTypes, nums)
			}
		}
		if len(callArgs) == 0 {
			// Single non-string param or unparsable JSON
			// — pass through as a single uint64 if possible.
			if n, ok := numericToUint64(params[0]); ok {
				callArgs = append(callArgs, n)
			} else {
				return nil, fmt.Errorf("wasm sdk (wazero): unsupported param type %T", params[0])
			}
		}
	default:
		// Multi-arg form: each must be numeric.
		for i, p := range params {
			n, ok := numericToUint64(p)
			if !ok {
				return nil, fmt.Errorf("wasm sdk (wazero): param[%d] type %T not numeric", i, p)
			}
			callArgs = append(callArgs, n)
		}
	}

	results, err := fn.Call(sdk.ctx, callArgs...)
	if err != nil {
		return nil, fmt.Errorf("wasm sdk (wazero): call %s: %w", funcName, err)
	}
	switch len(results) {
	case 0:
		return nil, nil
	case 1:
		return results[0], nil
	default:
		return results, nil
	}
}

// preflightP2PTransactionJSON validates a raw JSON transaction
// payload against the SDK's currently-loaded module's
// `validate_raw(ptr, len) -> i32` export. Modules without that
// export are treated as having no preflight rules — return
// true so the gossip pipeline does not drop messages on a
// best-effort layer. This is the same convention
// WazeroRuntime.CallValidateRaw uses.
//
// Note: the package-level QSDM_WASM_PREFLIGHT_MODULE env hook
// (preflight.go::TryPreflightP2PTransactionJSON) is ALWAYS
// preferred when set; this method is only consulted when no
// env-pinned validator exists.
func (sdk *WASMSDK) preflightP2PTransactionJSON(msg []byte) (bool, error) {
	sdk.mu.Lock()
	defer sdk.mu.Unlock()
	if sdk.module == nil {
		return true, nil
	}
	fn := sdk.module.ExportedFunction("validate_raw")
	if fn == nil {
		return true, nil
	}
	mem := sdk.module.Memory()
	if mem == nil {
		return false, fmt.Errorf("wasm sdk (wazero): preflight: no linear memory")
	}
	const base uint32 = 65536
	need := uint64(base) + uint64(len(msg))
	for uint64(mem.Size()) < need {
		if _, ok := mem.Grow(1); !ok {
			return false, fmt.Errorf("wasm sdk (wazero): preflight: linear memory grow failed")
		}
	}
	if !mem.Write(base, msg) {
		return false, fmt.Errorf("wasm sdk (wazero): preflight: linear memory write failed")
	}
	results, err := fn.Call(sdk.ctx, uint64(base), uint64(len(msg)))
	if err != nil {
		return false, fmt.Errorf("wasm sdk (wazero): preflight: %w", err)
	}
	if len(results) == 0 {
		return false, fmt.Errorf("wasm sdk (wazero): preflight: empty result")
	}
	return results[0] != 0, nil
}

// encodeWasmArgs converts a slice of float64 (the natural Go
// JSON numeric type) into wazero's uint64-encoded calling
// convention, dispatching on each parameter's declared WASM
// value type. Falls back to a raw uint64 cast for any value
// type wazero adds in the future that we don't explicitly
// handle here.
func encodeWasmArgs(paramTypes []wazeroapi.ValueType, nums []float64) []uint64 {
	out := make([]uint64, 0, len(paramTypes))
	for i, n := range nums {
		if i >= len(paramTypes) {
			break
		}
		switch paramTypes[i] {
		case wazeroapi.ValueTypeI32:
			out = append(out, wazeroapi.EncodeI32(int32(n)))
		case wazeroapi.ValueTypeI64:
			out = append(out, uint64(int64(n)))
		case wazeroapi.ValueTypeF32:
			out = append(out, wazeroapi.EncodeF32(float32(n)))
		case wazeroapi.ValueTypeF64:
			out = append(out, wazeroapi.EncodeF64(n))
		default:
			out = append(out, uint64(n))
		}
	}
	return out
}

// numericToUint64 best-effort-converts a Go scalar into a
// uint64 suitable for handing to wazero as a generic call
// argument. Matches the value types Go's JSON decoder might
// produce (float64) plus the integer types a caller might
// pass directly. Returns (_, false) if the input is not
// numeric.
func numericToUint64(v interface{}) (uint64, bool) {
	switch x := v.(type) {
	case int:
		return uint64(x), true
	case int32:
		return uint64(x), true
	case int64:
		return uint64(x), true
	case uint:
		return uint64(x), true
	case uint32:
		return uint64(x), true
	case uint64:
		return x, true
	case float32:
		return uint64(int64(x)), true
	case float64:
		return uint64(int64(x)), true
	}
	return 0, false
}
