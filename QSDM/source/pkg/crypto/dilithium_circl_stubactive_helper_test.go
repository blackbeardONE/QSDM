//go:build !cgo && dilithium_circl
// +build !cgo,dilithium_circl

package crypto

// Helper file separated from dilithium_circl_test.go so the
// stubactive import only appears under the dilithium_circl tag.
// dilithium_stub.go imports stubactive on the !dilithium_circl
// path, so the import graph is mutually exclusive — exactly one
// of the two files supplies the dependency at any given build.

import (
	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

func stubActiveDilithiumIsActive() bool {
	return stubactive.Active(stubactive.KindDilithium)
}
