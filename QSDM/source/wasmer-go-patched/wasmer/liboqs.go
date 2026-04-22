package wasmer

/*
#include <oqs/oqs.h>
#include <stdlib.h>
*/
import "C"
import (
	"errors"
	"unsafe"
)

// Algorithm represents a liboqs algorithm name
type Algorithm string

// List of supported algorithms (example subset)
const (
	AlgDefault Algorithm = "DEFAULT"
	AlgFrodoKEM640AES Algorithm = "FrodoKEM-640-AES"
	AlgKyber512 Algorithm = "Kyber512"
)

// KeyEncapsulation represents a KEM object from liboqs
type KeyEncapsulation struct {
	ptr *C.OQS_KEM
}

// NewKEM creates a new KEM object for the given algorithm
func NewKEM(alg Algorithm) (*KeyEncapsulation, error) {
	cAlg := C.CString(string(alg))
	defer C.free(unsafe.Pointer(cAlg))

	kem := C.OQS_KEM_new(cAlg)
	if kem == nil {
		return nil, errors.New("failed to create KEM object")
	}
	return &KeyEncapsulation{ptr: kem}, nil
}

// Free releases the KEM object
func (k *KeyEncapsulation) Free() {
	if k.ptr != nil {
		C.OQS_KEM_free(k.ptr)
		k.ptr = nil
	}
}

// KeyPair generates a public/private key pair
func (k *KeyEncapsulation) KeyPair() (publicKey, secretKey []byte, err error) {
	if k.ptr == nil {
		return nil, nil, errors.New("KEM object is nil")
	}
	pubKey := make([]byte, k.ptr.length_public_key)
	secKey := make([]byte, k.ptr.length_secret_key)

	ret := C.OQS_KEM_keypair(k.ptr,
		(*C.uint8_t)(unsafe.Pointer(&pubKey[0])),
		(*C.uint8_t)(unsafe.Pointer(&secKey[0])),
	)
	if ret != C.OQS_SUCCESS {
		return nil, nil, errors.New("keypair generation failed")
	}
	return pubKey, secKey, nil
}

// Encapsulate generates a ciphertext and shared secret using the public key
func (k *KeyEncapsulation) Encapsulate(publicKey []byte) (ciphertext, sharedSecret []byte, err error) {
	if k.ptr == nil {
		return nil, nil, errors.New("KEM object is nil")
	}
	ct := make([]byte, k.ptr.length_ciphertext)
	ss := make([]byte, k.ptr.length_shared_secret)

	ret := C.OQS_KEM_encaps(k.ptr,
		(*C.uint8_t)(unsafe.Pointer(&ct[0])),
		(*C.uint8_t)(unsafe.Pointer(&ss[0])),
		(*C.uint8_t)(unsafe.Pointer(&publicKey[0])),
	)
	if ret != C.OQS_SUCCESS {
		return nil, nil, errors.New("encapsulation failed")
	}
	return ct, ss, nil
}

// Decapsulate recovers the shared secret from the ciphertext and secret key
func (k *KeyEncapsulation) Decapsulate(ciphertext, secretKey []byte) (sharedSecret []byte, err error) {
	if k.ptr == nil {
		return nil, errors.New("KEM object is nil")
	}
	ss := make([]byte, k.ptr.length_shared_secret)

	ret := C.OQS_KEM_decaps(k.ptr,
		(*C.uint8_t)(unsafe.Pointer(&ss[0])),
		(*C.uint8_t)(unsafe.Pointer(&ciphertext[0])),
		(*C.uint8_t)(unsafe.Pointer(&secretKey[0])),
	)
	if ret != C.OQS_SUCCESS {
		return nil, errors.New("decapsulation failed")
	}
	return ss, nil
}
