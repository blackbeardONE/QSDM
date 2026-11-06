package chain

// Tests for producer_keys.go: verifying with producer keys prepared once
// gives exactly the result and the error of verifyAuth, for every input,
// whether the key was prepared already or not, and keeps only the keys of
// the producers that blocks name.

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

// authCase is one authenticator to check, against the producer it claims.
type authCase struct {
	name    string
	auth    BFTWireAuth
	digest  []byte
	claimed string
}

// authCases are the authenticators of a signed fixture block b and of
// tampered copies of it, signed by the producer at address addr; otherAddr
// is another producer's.
func authCases(t *testing.T, b, stripped *Block, otherAddr string) []authCase {
	t.Helper()
	auth := b.ProducerAuth
	digest := blockSigDigest(b)
	flipped := auth
	flipped.Signature = append([]byte(nil), auth.Signature...)
	flipped.Signature[77] ^= 1
	short := auth
	short.Signature = auth.Signature[:len(auth.Signature)-1]
	shortKey := BFTWireAuth{PublicKey: auth.PublicKey[:100], Signature: auth.Signature}
	if !stripped.ProducerAuth.Stripped() {
		t.Fatal("fixture: block is not stripped")
	}
	return []authCase{
		{"valid", auth, digest, b.ProducerID},
		{"valid, producer in capitals", auth, digest, strings.ToUpper(b.ProducerID)},
		{"another digest", auth, blockSigDigest(stripped), b.ProducerID},
		{"a flipped signature bit", flipped, digest, b.ProducerID},
		{"a signature one byte short", short, digest, b.ProducerID},
		{"the key of another producer", auth, digest, otherAddr},
		{"a short key that derives the claimed producer", shortKey, digest, BFTValidatorAddress(shortKey.PublicKey)},
		{"a short key", shortKey, digest, b.ProducerID},
		{"no signature", BFTWireAuth{PublicKey: auth.PublicKey}, digest, b.ProducerID},
		{"no key", BFTWireAuth{Signature: auth.Signature}, digest, b.ProducerID},
		{"nothing", BFTWireAuth{}, digest, b.ProducerID},
		{"stripped", stripped.ProducerAuth, blockSigDigest(stripped), stripped.ProducerID},
	}
}

// requireSameError requires got to be want: both nil, or the same message
// and the same sentinel errors.
func requireSameError(t *testing.T, name string, got, want error) {
	t.Helper()
	if (got == nil) != (want == nil) || (got != nil && got.Error() != want.Error()) {
		t.Fatalf("%s: got %v, want %v", name, got, want)
	}
	for _, sentinel := range []error{ErrBFTBadSignature, ErrBFTUnsigned, ErrBlockSignatureStripped, ErrBlockBadSignature, ErrBlockUnsigned} {
		if errors.Is(got, sentinel) != errors.Is(want, sentinel) {
			t.Fatalf("%s: errors.Is(%v) = %v, want %v", name, sentinel, errors.Is(got, sentinel), errors.Is(want, sentinel))
		}
	}
}

// TestProducerKeysVerifyAsVerifyAuth: valid, tampered, foreign, malformed,
// unsigned and stripped authenticators give verifyAuth's result and error,
// through a fresh table, a table that has prepared the key already, and a
// nil table.
func TestProducerKeysVerifyAsVerifyAuth(t *testing.T) {
	f := newJournalFixture(t, 12)
	_, _, loaded := f.loadAndRestore(t)
	b := f.blocks[jfCheckpoint+2]
	cases := authCases(t, b, loaded[jfCheckpoint+1], f.policy.HistoricalProducer)
	warm := newProducerKeys()
	if err := warm.verifyAuth(b.ProducerAuth, blockSigDigest(b), b.ProducerID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		want := verifyAuth(tc.auth, tc.digest, tc.claimed)
		for _, table := range []struct {
			name string
			keys *producerKeys
		}{{"fresh", newProducerKeys()}, {"warm", warm}, {"nil", nil}} {
			for round := 0; round < 2; round++ {
				got := table.keys.verifyAuth(tc.auth, tc.digest, tc.claimed)
				requireSameError(t, tc.name+" ("+table.name+" table)", got, want)
			}
		}
	}
	if err := verifyAuth(b.ProducerAuth, blockSigDigest(b), b.ProducerID); err != nil {
		t.Fatalf("fixture: the valid case does not verify: %v", err)
	}
}

// TestProducerKeysKeepOnlyTheNamedProducersKeys: a table keeps one prepared
// key per producer whose blocks verified, and none for a key that does not
// derive the producer the block names.
func TestProducerKeysKeepOnlyTheNamedProducersKeys(t *testing.T) {
	f := newJournalFixture(t, 14)
	keys := newProducerKeys()
	for _, b := range f.blocks[jfSigFrom:] {
		if err := keys.verifyAuth(b.ProducerAuth, blockSigDigest(b), b.ProducerID); err != nil {
			t.Fatalf("block %d: %v", b.Height, err)
		}
	}
	if len(keys.keys) != 2 {
		t.Fatalf("the table holds %d keys after two producers' blocks, want 2", len(keys.keys))
	}
	for pk, k := range keys.keys {
		if k.address != BFTValidatorAddress([]byte(pk)) {
			t.Fatal("a key is kept under another key's address")
		}
		if k.address != f.policy.HistoricalProducer && k.address != f.policy.ReplacementProducer {
			t.Fatalf("the table keeps a key of %s, not a producer of the schedule", k.address)
		}
	}
	other, _ := newBFTKey(t)
	b := *f.blocks[jfCheckpoint+1]
	if err := SignBlock(&b, other); err != nil {
		t.Fatal(err)
	}
	// The block is signed by another key but still names the replacement.
	if err := keys.verifyAuth(b.ProducerAuth, blockSigDigest(&b), f.policy.ReplacementProducer); !errors.Is(err, ErrBFTBadSignature) {
		t.Fatalf("a key that does not derive the named producer: %v", err)
	}
	if len(keys.keys) != 2 {
		t.Fatalf("the table keeps a key that does not derive the producer the block names (%d keys)", len(keys.keys))
	}
}

// TestTransitionChecksAgreeWithPreparedKeys: every check of a chain block
// (verifyTransitionChainBlock) returns the same error with the run's
// prepared keys as with a key prepared for each signature, for honest blocks
// and for blocks whose signature, key or producer was tampered with.
func TestTransitionChecksAgreeWithPreparedKeys(t *testing.T) {
	f := newJournalFixture(t, 14)
	bp := f.producer(t, nil)
	epochs := bp.producerEpochs
	oldKey := f.blocks[jfSigFrom].ProducerAuth.PublicKey
	newKey := f.blocks[jfCheckpoint+1].ProducerAuth.PublicKey
	// The edits apply to the signed blocks; the unsigned history before
	// jfSigFrom is checked unchanged in every variant.
	variants := []struct {
		name string
		edit func(b *Block)
	}{
		{"honest", func(*Block) {}},
		{"a flipped signature bit", func(b *Block) {
			b.ProducerAuth.Signature = append([]byte(nil), b.ProducerAuth.Signature...)
			b.ProducerAuth.Signature[9] ^= 1
		}},
		{"a truncated signature", func(b *Block) {
			b.ProducerAuth.Signature = b.ProducerAuth.Signature[:4000]
		}},
		{"the other producer's key", func(b *Block) {
			if b.Height > jfCheckpoint {
				b.ProducerAuth.PublicKey = oldKey
			} else {
				b.ProducerAuth.PublicKey = newKey
			}
		}},
		{"no signature", func(b *Block) { b.ProducerAuth.Signature = nil }},
		{"the other producer named", func(b *Block) {
			if b.Height > jfCheckpoint {
				b.ProducerID = f.policy.HistoricalProducer
			} else {
				b.ProducerID = f.policy.ReplacementProducer
			}
		}},
	}
	// One table across all variants, as one run shares it across all
	// blocks: what an earlier variant prepared must not change a later
	// variant's result.
	shared := newProducerKeys()
	for _, v := range variants {
		for i, sealed := range f.blocks {
			b := *sealed
			if sealed.ProducerAuth.Signed() {
				v.edit(&b)
			}
			var prev *Block
			if i > 0 {
				prev = f.blocks[i-1]
			}
			want := verifyTransitionChainBlock(epochs, nil, prev, &b, uint64(i))
			requireSameError(t, v.name+" (fresh table)", verifyTransitionChainBlock(epochs, newProducerKeys(), prev, &b, uint64(i)), want)
			requireSameError(t, v.name+" (shared table)", verifyTransitionChainBlock(epochs, shared, prev, &b, uint64(i)), want)
			if v.name == "honest" && want != nil {
				t.Fatalf("honest block %d: %v", i, want)
			}
		}
	}
}

// TestProducerKeysConcurrentVerify: one table verifies on many goroutines at
// once (run with -race), each block getting its own result.
func TestProducerKeysConcurrentVerify(t *testing.T) {
	f := newJournalFixture(t, 20)
	keys := newProducerKeys()
	var wg sync.WaitGroup
	errs := make(chan error, 4*len(f.blocks))
	for g := 0; g < 4; g++ {
		for _, sealed := range f.blocks[jfSigFrom:] {
			wg.Add(1)
			go func(b *Block, g int) {
				defer wg.Done()
				digest := blockSigDigest(b)
				if g%2 == 1 {
					digest = []byte("not this block")
				}
				err := keys.verifyAuth(b.ProducerAuth, digest, b.ProducerID)
				if (err == nil) != (g%2 == 0) {
					errs <- err
				}
			}(sealed, g)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("unexpected result: %v", err)
	}
	if len(keys.keys) != 2 {
		t.Fatalf("the table holds %d keys, want 2", len(keys.keys))
	}
}
