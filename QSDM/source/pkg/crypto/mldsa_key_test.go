//go:build !cgo || dilithium_circl
// +build !cgo dilithium_circl

package crypto

// Tests for MLDSAKey in the circl backend, the one QSDM ships: a key
// prepared once verifies exactly as VerifyWithPublicKey does, for every
// input. (In the CGO backend MLDSAKey.Verify is VerifyWithPublicKey.)

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

// verifyOutcome is what one verification returns, as text: the boolean and
// the error message, if any.
func verifyOutcome(ok bool, err error) string {
	if err != nil {
		return fmt.Sprintf("%v / error %q", ok, err.Error())
	}
	return fmt.Sprintf("%v / no error", ok)
}

// requireSameAsVerifyWithPublicKey requires NewMLDSAKey(pk).Verify to return
// what VerifyWithPublicKey returns for message and sig, also when the
// prepared key is reused.
func requireSameAsVerifyWithPublicKey(t *testing.T, name string, key *MLDSAKey, pk, message, sig []byte) {
	t.Helper()
	d := NewDilithiumVerifyOnly()
	if d == nil {
		t.Skip("no ML-DSA verifier in this build")
	}
	defer d.Free()
	want := verifyOutcome(d.VerifyWithPublicKey(message, sig, pk))
	for round := 0; round < 2; round++ {
		if got := verifyOutcome(key.Verify(message, sig)); got != want {
			t.Fatalf("%s (round %d): MLDSAKey.Verify = %s, VerifyWithPublicKey = %s", name, round, got, want)
		}
	}
}

func newSignerForKeyTest(t *testing.T) *Dilithium {
	t.Helper()
	signer := NewDilithium()
	if signer == nil {
		t.Skip("no ML-DSA signer in this build")
	}
	t.Cleanup(signer.Free)
	return signer
}

// TestMLDSAKeyVerifiesAsVerifyWithPublicKey: valid, tampered, foreign and
// malformed signatures give the same result and the same error through a
// prepared key as through VerifyWithPublicKey.
func TestMLDSAKeyVerifiesAsVerifyWithPublicKey(t *testing.T) {
	signer, other := newSignerForKeyTest(t), newSignerForKeyTest(t)
	pk := signer.GetPublicKey()
	message := []byte("qsdm/block/v1 digest under test")
	sig, err := signer.Sign(message)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := other.Sign(message)
	if err != nil {
		t.Fatal(err)
	}
	flipped := append([]byte(nil), sig...)
	flipped[100] ^= 1
	key := NewMLDSAKey(pk)
	for _, tc := range []struct {
		name         string
		message, sig []byte
	}{
		{"valid", message, sig},
		{"another message", []byte("another digest"), sig},
		{"empty message", []byte{}, sig},
		{"a flipped bit", message, flipped},
		{"another key's signature", message, foreign},
		{"one byte short", message, sig[:len(sig)-1]},
		{"one byte long", message, append(append([]byte(nil), sig...), 0)},
		{"empty signature", message, []byte{}},
		{"nil signature", message, nil},
	} {
		requireSameAsVerifyWithPublicKey(t, tc.name, key, pk, tc.message, tc.sig)
	}
	if ok, err := key.Verify(message, sig); !ok || err != nil {
		t.Fatalf("a valid signature does not verify: %v, %v", ok, err)
	}
}

// TestMLDSAKeyMalformedKeys: keys of the wrong length, and a key of the
// right length that is not the signer's, are refused exactly as
// VerifyWithPublicKey refuses them.
func TestMLDSAKeyMalformedKeys(t *testing.T) {
	signer := newSignerForKeyTest(t)
	pk := signer.GetPublicKey()
	message := []byte("qsdm/block/v1 digest under test")
	sig, err := signer.Sign(message)
	if err != nil {
		t.Fatal(err)
	}
	garbled := append([]byte(nil), pk...)
	for i := range garbled {
		garbled[i] ^= byte(i)
	}
	for _, tc := range []struct {
		name string
		pk   []byte
	}{
		{"one byte short", pk[:len(pk)-1]},
		{"one byte long", append(append([]byte(nil), pk...), 7)},
		{"empty", []byte{}},
		{"garbled", garbled},
	} {
		requireSameAsVerifyWithPublicKey(t, tc.name, NewMLDSAKey(tc.pk), tc.pk, message, sig)
		requireSameAsVerifyWithPublicKey(t, tc.name+", short signature", NewMLDSAKey(tc.pk), tc.pk, message, sig[:10])
	}
}

// TestMLDSAKeyKeepsItsOwnCopy: changing the slice NewMLDSAKey was given does
// not change the key it prepared.
func TestMLDSAKeyKeepsItsOwnCopy(t *testing.T) {
	signer := newSignerForKeyTest(t)
	pk := signer.GetPublicKey()
	message := []byte("qsdm/block/v1 digest under test")
	sig, err := signer.Sign(message)
	if err != nil {
		t.Fatal(err)
	}
	given := append([]byte(nil), pk...)
	key := NewMLDSAKey(given)
	for i := range given {
		given[i] = 0
	}
	if bytes.Equal(given, pk) {
		t.Fatal("fixture: the slice did not change")
	}
	if ok, err := key.Verify(message, sig); !ok || err != nil {
		t.Fatalf("after the given slice changed: %v, %v", ok, err)
	}
}

// TestMLDSAKeyConcurrentVerify: one prepared key verifies on many goroutines
// at once (run with -race), each getting the right answer.
func TestMLDSAKeyConcurrentVerify(t *testing.T) {
	signer := newSignerForKeyTest(t)
	key := NewMLDSAKey(signer.GetPublicKey())
	const n = 8
	messages := make([][]byte, n)
	sigs := make([][]byte, n)
	for i := range messages {
		messages[i] = []byte(fmt.Sprintf("message %d", i))
		sig, err := signer.Sign(messages[i])
		if err != nil {
			t.Fatal(err)
		}
		sigs[i] = sig
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4*n)
	for g := 0; g < 4; g++ {
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i, g int) {
				defer wg.Done()
				// Even goroutines verify their own message, odd ones the next
				// message's signature, which must fail.
				sig, want := sigs[i], true
				if g%2 == 1 {
					sig, want = sigs[(i+1)%n], false
				}
				if ok, err := key.Verify(messages[i], sig); ok != want || err != nil {
					errs <- fmt.Errorf("goroutine %d message %d: %v, %v; want %v", g, i, ok, err, want)
				}
			}(i, g)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
