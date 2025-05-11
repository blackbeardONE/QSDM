package crypto

/*
#cgo LDFLAGS: -loqs
#include <oqs/oqs.h>
#include <stdlib.h>
*/
import "C"
import (
    "errors"
    "unsafe"

    "github.com/blackbeardONE/QSDM/internal/logging"
)

type Dilithium struct {
    ctx *C.OQS_SIG
    pk  []byte
    sk  []byte
}

func NewDilithium() *Dilithium {
    ctx := C.OQS_SIG_new(C.CString("Dilithium2"))
    if ctx == nil {
        logging.Error.Println("Failed to create Dilithium context")
        return nil
    }
    d := &Dilithium{ctx: ctx}
    if err := d.keypair(); err != nil {
        logging.Error.Println("Failed to generate Dilithium keypair:", err)
        return nil
    }
    logging.Info.Println("Dilithium crypto initialized with real implementation")
    return d
}

func (d *Dilithium) keypair() error {
    pk := make([]byte, int(d.ctx.length_public_key))
    sk := make([]byte, int(d.ctx.length_secret_key))
    ret := C.OQS_SIG_keypair(
        d.ctx,
        (*C.uchar)(unsafe.Pointer(&pk[0])),
        (*C.uchar)(unsafe.Pointer(&sk[0])),
    )
    if ret != C.OQS_SUCCESS {
        return errors.New("keypair generation failed")
    }
    d.pk = pk
    d.sk = sk
    return nil
}

func (d *Dilithium) Sign(message []byte) ([]byte, error) {
    sig := make([]byte, int(d.ctx.length_signature))
    var sigLen C.size_t
    ret := C.OQS_SIG_sign(
        d.ctx,
        (*C.uchar)(unsafe.Pointer(&sig[0])),
        &sigLen,
        (*C.uchar)(unsafe.Pointer(&message[0])),
        C.size_t(len(message)),
        (*C.uchar)(unsafe.Pointer(&d.sk[0])),
    )
    if ret != C.OQS_SUCCESS {
        return nil, errors.New("signing failed")
    }
    return sig[:sigLen], nil
}

func (d *Dilithium) Verify(message []byte, signature []byte) (bool, error) {
    ret := C.OQS_SIG_verify(
        d.ctx,
        (*C.uchar)(unsafe.Pointer(&message[0])),
        C.size_t(len(message)),
        (*C.uchar)(unsafe.Pointer(&signature[0])),
        C.size_t(len(signature)),
        (*C.uchar)(unsafe.Pointer(&d.pk[0])),
    )
    if ret == C.OQS_SUCCESS {
        return true, nil
    }
    return false, errors.New("invalid signature")
}

func (d *Dilithium) Close() {
    if d.ctx != nil {
        C.OQS_SIG_free(d.ctx)
        d.ctx = nil
    }
}
