package chain

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/crypto"
)

// Producer keys prepared once.
//
// Under a producer transition a node authenticates every block of its chain
// before it restores it: LoadVerifiedJournal at every start, and CheckJournal
// for the snapshot import and the tail replay. verifyAuth unpacked the
// producer's public key for every signature, and in the circl backend
// unpacking derives the key's 8x7 matrix (56 SHAKE-128 samplings) into a new
// 57 KB allocation: about two thirds of a
// verification. Every block of an epoch carries the same key, so at height
// 880,376 of the public chain a start unpacked the two producer keys 397,811
// times: 26% of the start's CPU, and 21 GiB of its 33 GiB of allocations.
//
// producerKeys verifies the blocks of one such run with each producer key
// prepared once (crypto.MLDSAKey) and its consensus address computed once.
// The checks, their order and their errors are verifyAuth's. A key is kept
// only once it derives the producer that the block names, and every caller
// has checked that producer against the transition's schedule first
// (checkTransitionSchedule), so a run keeps at most one key per producer of
// its schedule.
type producerKeys struct {
	mu   sync.RWMutex
	keys map[string]*producerKey // by the packed public key
}

// producerKey is one prepared producer key.
type producerKey struct {
	address string           // BFTValidatorAddress of the key
	key     *crypto.MLDSAKey // the key, prepared for verification
}

func newProducerKeys() *producerKeys {
	return &producerKeys{keys: map[string]*producerKey{}}
}

// verifyAuth is the package's verifyAuth, with each key of k prepared once.
// A nil k is verifyAuth itself. It is safe for concurrent use.
func (k *producerKeys) verifyAuth(auth BFTWireAuth, digest []byte, claimedSigner string) error {
	if k == nil {
		return verifyAuth(auth, digest, claimedSigner)
	}
	if auth.stripped != nil {
		return ErrBlockSignatureStripped
	}
	if !auth.Signed() {
		return ErrBFTUnsigned
	}
	key, err := k.key(auth.PublicKey, claimedSigner)
	if err != nil {
		return err
	}
	ok, err := key.Verify(digest, auth.Signature)
	if errors.Is(err, crypto.ErrMLDSAVerifierUnavailable) {
		return errors.New("chain: ML-DSA verifier unavailable for BFT signature check")
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBFTBadSignature, err)
	}
	if !ok {
		return ErrBFTBadSignature
	}
	return nil
}

// key returns publicKey prepared for verification if it derives
// claimedSigner, and otherwise verifyAuth's error for a key that does not.
func (k *producerKeys) key(publicKey []byte, claimedSigner string) (*crypto.MLDSAKey, error) {
	k.mu.RLock()
	p := k.keys[string(publicKey)]
	k.mu.RUnlock()
	var got string
	if p != nil {
		got = p.address
	} else {
		got = BFTValidatorAddress(publicKey)
	}
	if !strings.EqualFold(got, claimedSigner) {
		return nil, fmt.Errorf("%w: public key derives %s not %s", ErrBFTBadSignature, got, claimedSigner)
	}
	if p == nil {
		p = &producerKey{address: got, key: crypto.NewMLDSAKey(publicKey)}
		k.mu.Lock()
		if q := k.keys[string(publicKey)]; q != nil {
			p = q
		} else {
			k.keys[string(publicKey)] = p
		}
		k.mu.Unlock()
	}
	return p.key, nil
}
