//go:build !cgo
// +build !cgo

package crypto

// Helper file separated from dilithium_circl_test.go for the
// stubactive registry probe used by TestCircl_StubFlagNotMarked.
// Under Stage B the dilithium kind is no longer marked active in
// any !cgo build path, so this is the test that proves the
// invariant remained true after the build-tag flip.

import (
	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

func stubActiveDilithiumIsActive() bool {
	return stubactive.Active(stubactive.KindDilithium)
}
