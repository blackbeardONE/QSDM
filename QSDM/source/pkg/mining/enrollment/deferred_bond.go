package enrollment

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

// DeferredBondWorkDigest returns the enrollment Hashcash digest for nonce.
// WorkNonce itself is zeroed in the canonical payload prefix so miners can
// search the nonce without changing any other signed field.
func DeferredBondWorkDigest(p EnrollPayload, nonce uint64) ([32]byte, error) {
	p.WorkNonce = 0
	raw, err := marshalCanonical(p)
	if err != nil {
		return [32]byte{}, fmt.Errorf("canonicalize deferred-bond work: %w", err)
	}
	buf := make([]byte, 0, len(raw)+8+32)
	buf = append(buf, []byte("qsdm-deferred-bond-work-v1\n")...)
	buf = append(buf, raw...)
	var suffix [8]byte
	binary.BigEndian.PutUint64(suffix[:], nonce)
	buf = append(buf, suffix[:]...)
	return sha256.Sum256(buf), nil
}

func hasLeadingZeroBits(digest [32]byte, bits uint8) bool {
	whole := int(bits / 8)
	partial := bits % 8
	for i := 0; i < whole; i++ {
		if digest[i] != 0 {
			return false
		}
	}
	if partial == 0 {
		return true
	}
	mask := byte(0xff << (8 - partial))
	return digest[whole]&mask == 0
}

// ValidateDeferredBondWork verifies the fixed-cost enrollment postage.
func ValidateDeferredBondWork(p EnrollPayload) error {
	digest, err := DeferredBondWorkDigest(p, p.WorkNonce)
	if err != nil {
		return err
	}
	if !hasLeadingZeroBits(digest, DeferredBondWorkDifficulty) {
		return fmt.Errorf("%w: deferred bond work does not satisfy %d leading zero bits",
			ErrPayloadInvalid, DeferredBondWorkDifficulty)
	}
	return nil
}

// FindDeferredBondWork searches a valid WorkNonce for a deferred enrollment.
// It is intended for operator tooling, never for validator request handlers.
func FindDeferredBondWork(p EnrollPayload) (nonce uint64, attempts uint64, err error) {
	for nonce = 0; nonce < math.MaxUint64; nonce++ {
		digest, digestErr := DeferredBondWorkDigest(p, nonce)
		if digestErr != nil {
			return 0, attempts, digestErr
		}
		attempts++
		if hasLeadingZeroBits(digest, DeferredBondWorkDifficulty) {
			return nonce, attempts, nil
		}
	}
	return 0, attempts, fmt.Errorf("deferred bond work search exhausted")
}
