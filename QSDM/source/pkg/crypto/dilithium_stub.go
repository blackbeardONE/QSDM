//go:build !cgo && !dilithium_circl
// +build !cgo,!dilithium_circl

package crypto

import (
	"errors"

	"github.com/blackbeardONE/QSDM/pkg/monitoring/stubactive"
)

// init flips qsdm_stub_active{kind="dilithium"} to 1 in non-CGO
// builds *without* the dilithium_circl tag. ML-DSA-87 quantum-
// safe signatures require either liboqs (CGO) or the cloudflare/
// circl pure-Go backend (selected by `go build -tags dilithium_circl`,
// see dilithium_circl.go); with neither available, every
// Sign/Verify call here returns an error rather than silently
// producing weaker output.
//
// Stage A note: the dilithium_circl backend lands behind an
// opt-in build tag so default non-CGO builds stay byte-identical
// to the prior stub behaviour. Stage B will flip the tag and
// retire this file.
func init() {
	stubactive.MarkActive(stubactive.KindDilithium)
}

// Dilithium represents the Dilithium signature scheme (stub for non-CGO builds)
type Dilithium struct{}

// NewDilithium returns nil for non-CGO builds
func NewDilithium() *Dilithium {
	return nil
}

// NewDilithiumVerifyOnly returns nil for non-CGO builds
func NewDilithiumVerifyOnly() *Dilithium {
	return nil
}

// Sign returns an error for non-CGO builds
func (d *Dilithium) Sign(message []byte) ([]byte, error) {
	if d == nil {
		return nil, errors.New("Dilithium not available: CGO and liboqs required")
	}
	return nil, errors.New("Dilithium not available: CGO and liboqs required")
}

// Verify returns false and an error for non-CGO builds
func (d *Dilithium) Verify(message []byte, signature []byte) (bool, error) {
	if d == nil {
		return false, errors.New("Dilithium not available: CGO and liboqs required")
	}
	return false, errors.New("Dilithium not available: CGO and liboqs required")
}

// VerifyWithPublicKey is a stub for non-CGO builds
func (d *Dilithium) VerifyWithPublicKey(message []byte, signature []byte, publicKey []byte) (bool, error) {
	if d == nil {
		return false, errors.New("Dilithium not available: CGO and liboqs required")
	}
	return false, errors.New("Dilithium not available: CGO and liboqs required")
}

// Free is a no-op for stub
func (d *Dilithium) Free() {
	// No-op
}

