// Package stubactive is the registry of which stub-shipped code
// paths are currently active in the running binary. It exists
// because QSDM intentionally ships several stubs that are
// runtime-selectable or build-tag-selectable and are NOT safe in
// production:
//
//   - "poe"          — pkg/consensus/poe_stub.go (CGO disabled).
//                      ⚠ Accepts transactions WITHOUT signature
//                      verification. Test-only path.
//   - "dilithium"    — pkg/crypto/dilithium_stub.go (CGO disabled).
//                      ML-DSA-87 quantum-safe signing not available.
//   - "wallet"       — pkg/wallet/wallet_stub.go (CGO disabled).
//                      SHA-256 signatures instead of quantum-safe.
//   - "mesh3d_cuda"  — pkg/mesh3d/cuda_stub.go (CUDA disabled).
//                      Falls back to CPU mesh validation.
//   - "wasm_sdk"     — pkg/wasm/sdk_stub.go (CGO disabled).
//                      WASM module execution unavailable.
//   - "cc"           — pkg/mining/attest/cc/stub.go (Phase 2c-iv
//                      pending). Rejects every nvidia-cc-v1 proof
//                      with ErrNotYetAvailable.
//   - "slashing"     — pkg/mining/slashing/verifier.go StubVerifier.
//                      Returns "stub (not yet implemented)" for
//                      unregistered evidence kinds.
//
// Why a separate leaf package instead of a value in pkg/monitoring?
//
// Stubs live in dependency-graph-leaves like pkg/consensus and
// pkg/crypto; pkg/monitoring depends on pkg/mining and pkg/chain,
// which would create import cycles if stubs imported it directly.
// stubactive has zero non-stdlib imports, so any stub file can
// import it freely. pkg/monitoring then reads the snapshot to
// emit the qsdm_stub_active{kind="..."} Prometheus gauge.
//
// Concurrency: the registry is a sync.Map under the hood;
// MarkActive/MarkInactive are safe to call from package init()
// or runtime constructors, and Snapshot is safe to call from
// the metrics-scrape goroutine while marks happen.
package stubactive

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Canonical stub-kind identifiers. Defined as constants so the
// stubs and the metrics scrape agree on spelling.
const (
	KindPoE         = "poe"
	KindDilithium   = "dilithium"
	KindWallet      = "wallet"
	KindMesh3DCUDA  = "mesh3d_cuda"
	KindWasmSDK     = "wasm_sdk"
	KindCC          = "cc"
	KindSlashing    = "slashing"
)

// AllKinds returns the canonical kind list, sorted. Used by the
// metrics scrape to ensure the qsdm_stub_active gauge has a row
// for every kind even when no stub is currently active (so the
// alerting expression `qsdm_stub_active == 1` evaluates against
// a populated time series rather than missing-data).
func AllKinds() []string {
	out := []string{
		KindPoE,
		KindDilithium,
		KindWallet,
		KindMesh3DCUDA,
		KindWasmSDK,
		KindCC,
		KindSlashing,
	}
	sort.Strings(out)
	return out
}

// state holds the per-kind active flag (atomic int32: 0 or 1).
// We use a sync.Map to permit registering kinds we don't know
// about at compile time (forward compatibility for new stubs)
// while keeping the hot path lock-free.
var state sync.Map // map[string]*atomic.Int32

func slot(kind string) *atomic.Int32 {
	if v, ok := state.Load(kind); ok {
		return v.(*atomic.Int32)
	}
	created := new(atomic.Int32)
	actual, _ := state.LoadOrStore(kind, created)
	return actual.(*atomic.Int32)
}

// MarkActive sets the active flag for `kind` to 1. Idempotent:
// repeated calls are a no-op. Safe to call from package init()
// or constructors.
func MarkActive(kind string) {
	slot(kind).Store(1)
}

// MarkInactive sets the active flag for `kind` to 0. Used by
// real-implementation init() (when the CGO build is selected)
// or by tests that want to reset state between cases.
func MarkInactive(kind string) {
	slot(kind).Store(0)
}

// Active reports whether the given kind is currently flagged
// active (1). Falls back to false if the kind has never been
// marked.
func Active(kind string) bool {
	if v, ok := state.Load(kind); ok {
		return v.(*atomic.Int32).Load() == 1
	}
	return false
}

// Snapshot returns the current active flag for every kind in
// AllKinds() (always populated, value is 0 or 1). Additional
// runtime-registered kinds beyond AllKinds() are also included
// so forward-compatible stubs surface in metrics automatically.
func Snapshot() map[string]int32 {
	out := make(map[string]int32, len(AllKinds()))
	for _, k := range AllKinds() {
		out[k] = 0
	}
	state.Range(func(k, v any) bool {
		out[k.(string)] = v.(*atomic.Int32).Load()
		return true
	})
	return out
}

// Reset zeroes every kind's active flag. Test-only helper.
func Reset() {
	state.Range(func(k, v any) bool {
		v.(*atomic.Int32).Store(0)
		return true
	})
}
