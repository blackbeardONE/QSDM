package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// FuzzProducerTransitionBlockRule gives the producer transition block rule
// one block around the boundaries of a version 1 policy and of a three-epoch
// version 2 schedule built from the fuzz input: any height near a boundary
// (or any height at all), a real ML-DSA-87 signature by one of the three
// producers, a forged signature or none, a block that was changed after it
// was signed, and any block and parent hash. The checkpoints may be pinned
// to the block itself, so that a hand-over block can be accepted too.
//
// Version 1 must not change: the rule as it was written before epochs
// (legacyValidateTransitionBlockV1), version 1 enforced as its schedule and
// the same table written as version 2 return the same verdict and the same
// error text for every block. For both schedules a block is accepted exactly
// when an independent reading of the documented rule allows it: every
// hand-over pin holds (block from_height-1 has the checkpoint hash, block
// from_height names it as its parent); from the first epoch on, the block
// carries a valid signature (checked with circl directly) by the producer of
// the epoch that contains it; below the first epoch an unsigned block is
// allowed and a signed one must come validly from the first producer.
func FuzzProducerTransitionBlockRule(f *testing.F) {
	keys := [3]fuzzKey{newFuzzKey(0x71), newFuzzKey(0x72), newFuzzKey(0x73)}
	hexOf := func(s string) string {
		sum := sha256.Sum256([]byte(s))
		return hex.EncodeToString(sum[:])
	}
	cp1, cp2, other := hexOf("checkpoint-1"), hexOf("checkpoint-2"), hexOf("other")
	prefix := hexOf("prefix")

	// The public network's version 1 heights, with each signer and each
	// height candidate, plus pinned hand-over blocks.
	for heightSel := uint8(0); heightSel < 13; heightSel++ {
		for signSel := uint8(0); signSel < 5; signSel++ {
			f.Add(uint64(482565), uint64(659975), uint64(7), heightSel, signSel, uint8(4), uint8(0), uint8(0), int64(0))
		}
	}
	f.Add(uint64(0), uint64(2), uint64(0), uint8(6), uint8(1), uint8(4), uint8(0), uint8(1), int64(5))  // pinned first hand-over
	f.Add(uint64(0), uint64(2), uint64(0), uint8(9), uint8(2), uint8(4), uint8(1), uint8(4), int64(6))  // pinned second hand-over
	f.Add(uint64(9), uint64(99), uint64(3), uint8(7), uint8(2), uint8(4), uint8(0), uint8(0), int64(7)) // first block of epoch 1
	f.Add(uint64(9), uint64(99), uint64(3), uint8(4), uint8(1), uint8(4), uint8(2), uint8(8), int64(8)) // changed after signing

	f.Fuzz(func(t *testing.T, sigSeed, cpSeed, gapSeed uint64, heightSel, signSel, hashSel, prevSel, pinSel uint8, ts int64) {
		cp := 1 + cpSeed%(1<<40)       // version 1 checkpoint_height
		hs := 1 + sigSeed%cp           // historical_signature_height, in 1..cp
		third := cp + 2 + gapSeed%1000 // first height of the third epoch

		candidates := []uint64{0, 1, hs - 1, hs, hs + 1, cp - 1, cp, cp + 1, cp + 2, third - 1, third, third + 1, sigSeed ^ cpSeed}
		hashes := []string{cp1, cp2, other, ""}
		producers := []string{keys[0].address, keys[1].address, keys[2].address, "", "12D3KooWlegacypeer"}
		if ts < 0 {
			ts = -ts
		}
		b := &Block{
			Height:    candidates[int(heightSel)%len(candidates)],
			PrevHash:  hashes[int(prevSel)%len(hashes)],
			Timestamp: time.Unix(1_700_000_000+ts%1_000_000_000, 0).UTC(),
		}
		switch mode := signSel % 5; mode {
		case 0: // unsigned
			b.ProducerID = producers[int(hashSel>>3)%len(producers)]
		case 1, 2, 3: // signed by producer mode-1
			b.Hash = computeBlockHash(b)
			if err := SignBlock(b, keys[mode-1]); err != nil {
				t.Fatalf("sign: %v", err)
			}
			if pinSel&8 != 0 { // changed after signing
				b.PrevHash = hashes[(int(prevSel)+1)%len(hashes)]
			}
		case 4: // forged: a real public key, a zero signature
			k := keys[int(hashSel>>3)%len(keys)]
			b.ProducerID = k.address
			b.ProducerAuth = BFTWireAuth{PublicKey: k.GetPublicKey(), Signature: make([]byte, mldsa87.SignatureSize)}
		}
		if signSel%5 == 0 || signSel%5 == 4 {
			if sel := int(hashSel) % 5; sel < len(hashes) {
				b.Hash = hashes[sel]
			} else {
				b.Hash = computeBlockHash(b)
			}
		}

		// Pin a checkpoint to the block itself when asked, so a block at a
		// hand-over height can be accepted.
		pin1, pin2 := cp1, cp2
		if pinSel&1 != 0 && fuzzHex64(b.Hash) {
			pin1 = b.Hash
		}
		if pinSel&4 != 0 && fuzzHex64(b.Hash) {
			pin2 = b.Hash
		}

		v1 := &producerpolicy.Transition{Version: 1, CheckpointHeight: cp, CheckpointHash: pin1,
			HistoricalSignatureHeight: hs, HistoricalProducer: keys[0].address,
			ReplacementProducer: keys[1].address, EffectiveHeight: cp + 1,
			HistoricalPrefixBytes: 1, HistoricalPrefixSHA256: prefix}
		v3 := &producerpolicy.Transition{Version: 2, HistoricalPrefixBytes: 1, HistoricalPrefixSHA256: prefix,
			Epochs: []producerpolicy.Epoch{
				{FromHeight: hs, ProducerID: keys[0].address},
				{FromHeight: cp + 1, ProducerID: keys[1].address, CheckpointHash: pin1},
				{FromHeight: third, ProducerID: keys[2].address, CheckpointHash: pin2},
			}}
		for _, p := range []*producerpolicy.Transition{v1, v3} {
			if err := p.Validate(); err != nil {
				t.Fatalf("harness built an invalid policy: %v", err)
			}
		}

		// Version 1 is unchanged.
		want := errString(legacyValidateTransitionBlockV1(v1, b))
		if got := errString(validateTransitionBlock(v1.Schedule(), b)); got != want {
			t.Fatalf("version 1 schedule: %s\nrule before epochs: %s", got, want)
		}
		if got := errString(validateTransitionBlock(asV2(v1).Schedule(), b)); got != want {
			t.Fatalf("version 1 written as version 2: %s\nrule before epochs: %s", got, want)
		}

		// Both schedules accept exactly the authorized blocks.
		for _, p := range []*producerpolicy.Transition{v1, v3} {
			epochs := p.Schedule()
			err := validateTransitionBlock(epochs, b)
			if ok := fuzzTransitionAuthorized(epochs, b); (err == nil) != ok {
				t.Fatalf("version %d, %d epochs, height %d producer %q signed %v: rule says %v, documented rule says accepted=%v",
					p.Version, len(epochs), b.Height, b.ProducerID, b.ProducerAuth.Signed(), err, ok)
			}
		}
	})
}

// fuzzHex64 reports whether s can be a checkpoint hash: 64 lowercase hex
// characters, not all zero.
func fuzzHex64(s string) bool {
	if len(s) != 64 || s == strings.Repeat("0", 64) {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// fuzzTransitionAuthorized restates the documented block rule for a
// schedule, independently of validateTransitionBlock. Signatures are checked
// with circl directly (fuzzVerifyMLDSA).
func fuzzTransitionAuthorized(epochs []producerpolicy.Epoch, b *Block) bool {
	for _, e := range epochs[1:] {
		if b.Height == e.FromHeight-1 && b.Hash != e.CheckpointHash {
			return false
		}
		if b.Height == e.FromHeight && b.PrevHash != e.CheckpointHash {
			return false
		}
	}
	auth := b.ProducerAuth
	signed := len(auth.PublicKey) > 0 && len(auth.Signature) > 0
	keyID := sha256.Sum256(auth.PublicKey)
	validSignature := signed && computeBlockHash(b) == b.Hash &&
		strings.EqualFold(hex.EncodeToString(keyID[:]), b.ProducerID) &&
		fuzzVerifyMLDSA(auth.PublicKey, blockSigDigest(b), auth.Signature)
	if b.Height >= epochs[0].FromHeight {
		producer := ""
		for _, e := range epochs {
			if b.Height >= e.FromHeight {
				producer = e.ProducerID
			}
		}
		return signed && b.ProducerID == producer && validSignature
	}
	if !signed {
		return !SignedBlocksRequiredAt(b.Height)
	}
	return b.ProducerID == epochs[0].ProducerID && validSignature
}
