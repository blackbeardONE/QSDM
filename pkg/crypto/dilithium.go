package crypto

/*
#cgo CFLAGS: -IC:/liboqs/include -IC:/liboqs/build/include
#cgo LDFLAGS: -LC:/liboqs/lib -LC:/liboqs/build/lib -loqs
#include <oqs/oqs.h>
#include <stdlib.h>
*/
import "C"
import (
	"errors"
	"fmt"
	"unsafe"
)

// Dilithium represents the Dilithium signature scheme using OQS
type Dilithium struct {
	sig *C.OQS_SIG
	pk  []byte
	sk  []byte
}

// NewDilithium initializes a new Dilithium instance
func NewDilithium() *Dilithium {
	cname := C.CString("Dilithium2")
	defer C.free(unsafe.Pointer(cname))
	sig := C.OQS_SIG_new(cname)
	if sig == nil {
		fmt.Println("OQS_SIG_new returned nil")
		return nil
	}
	d := &Dilithium{sig: sig}
	// Generate key pair
	pk := make([]byte, sig.length_public_key)
	sk := make([]byte, sig.length_secret_key)
	ret := C.OQS_SIG_keypair(
		sig,
		(*C.uchar)(unsafe.Pointer(&pk[0])),
		(*C.uchar)(unsafe.Pointer(&sk[0])),
	)
	if ret != C.OQS_SUCCESS {
		fmt.Println("OQS_SIG_keypair failed")
		return nil
	}
	d.pk = pk
	d.sk = sk
	return d
}

// Sign signs the message and returns the signature
func (d *Dilithium) Sign(message []byte) ([]byte, error) {
	if d.sig == nil {
		return nil, errors.New("Dilithium not initialized")
	}
	var sigLen C.size_t
	sigBuf := make([]byte, d.sig.length_signature)
	ret := C.OQS_SIG_sign(
		d.sig,
		(*C.uchar)(unsafe.Pointer(&sigBuf[0])),
		&sigLen,
		(*C.uchar)(unsafe.Pointer(&message[0])),
		C.size_t(len(message)),
		(*C.uchar)(unsafe.Pointer(&d.sk[0])),
	)
	if ret != C.OQS_SUCCESS {
		return nil, errors.New("failed to sign message")
	}
	return sigBuf[:sigLen], nil
}

// Verify verifies the signature for the given message
func (d *Dilithium) Verify(message []byte, signature []byte) (bool, error) {
	if d.sig == nil {
		return false, errors.New("Dilithium not initialized")
	}
	ret := C.OQS_SIG_verify(
		d.sig,
		(*C.uchar)(unsafe.Pointer(&message[0])),
		C.size_t(len(message)),
		(*C.uchar)(unsafe.Pointer(&signature[0])),
		C.size_t(len(signature)),
		(*C.uchar)(unsafe.Pointer(&d.pk[0])),
	)
	if ret == C.OQS_SUCCESS {
		return true, nil
	}
	return false, nil
}

// Free releases resources associated with Dilithium
func (d *Dilithium) Free() {
	if d.sig != nil {
		C.OQS_SIG_free(d.sig)
		d.sig = nil
	}
}
