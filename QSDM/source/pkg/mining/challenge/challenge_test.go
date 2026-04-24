package challenge

// Tests for Challenge.SigningBytes, Issuer mint/verify flow, and
// HMACSigner/HMACSignerVerifier. These are the crypto-adjacent
// primitives so we exhaustively lock down their behaviour; the
// HTTP handler in pkg/api gets its own test file.

import (
	"bytes"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"
)

// -----------------------------------------------------------------------------
// SigningBytes: golden-vector lock + collision resistance
// -----------------------------------------------------------------------------

func TestSigningBytes_Golden(t *testing.T) {
	// The exact output here is part of the v2 protocol wire
	// format. If this test fails, you have changed the hashable
	// payload and every existing validator + every signed
	// challenge in flight is now incompatible. Bump a protocol
	// field and explain yourself in the commit message before
	// "fixing" this test.
	var n [32]byte
	for i := range n {
		n[i] = byte(i)
	}
	c := Challenge{
		Nonce:    n,
		IssuedAt: 1_700_000_000,
		SignerID: "validator-01",
	}
	got := c.SigningBytes()

	// Reconstruct the expected bytes explicitly so the reader can
	// see the layout.
	want := []byte{}
	want = append(want, "qsdm.v2.challenge\x00"...)
	// uvarint(12) — len("validator-01")
	want = append(want, 0x0c)
	want = append(want, "validator-01"...)
	// uvarint(zigzag(1_700_000_000)) = uvarint(3_400_000_000).
	// Hand-derivation: 3.4e9 = 26562500*128+0 → byte0=0x80;
	// 26562500 = 207519*128+68 → byte1=0xC4; 207519 = 1621*128+31
	// → byte2=0x9F; 1621 = 12*128+85 → byte3=0xD5; 12 → byte4=0x0C.
	want = append(want, 0x80, 0xc4, 0x9f, 0xd5, 0x0c)
	want = append(want, n[:]...)

	if !bytes.Equal(got, want) {
		t.Fatalf("SigningBytes mismatch:\n  got=%x\n want=%x", got, want)
	}
}

func TestSigningBytes_PrefixSeparatesFields(t *testing.T) {
	// Two tuples that would collide under naive concatenation
	// must NOT collide here because the length prefix disambiguates
	// signer_id boundaries.
	a := Challenge{SignerID: "ab", IssuedAt: 1, Nonce: [32]byte{0x01}}
	b := Challenge{SignerID: "a", IssuedAt: 98, Nonce: [32]byte{0x01}}
	// In a naive concat the two would share a prefix; the length
	// prefix means the SignerID byte-range is unambiguous.
	if bytes.Equal(a.SigningBytes(), b.SigningBytes()) {
		t.Fatal("length prefix failed to disambiguate signer_id boundaries")
	}
}

// -----------------------------------------------------------------------------
// HMACSigner / HMACSignerVerifier
// -----------------------------------------------------------------------------

func TestHMACSigner_SignVerifyRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0xAA}, 32)
	signer, err := NewHMACSigner("validator-01", key)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	verifier := NewHMACSignerVerifier()
	if err := verifier.Register("validator-01", key); err != nil {
		t.Fatalf("Register: %v", err)
	}
	msg := []byte("example challenge payload")
	sig, err := signer.Sign(msg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(sig) != 32 {
		t.Fatalf("HMAC-SHA256 output should be 32 bytes, got %d", len(sig))
	}
	if err := verifier.VerifySignature("validator-01", msg, sig); err != nil {
		t.Fatalf("VerifySignature round-trip: %v", err)
	}
}

func TestHMACSigner_TamperedSignatureRejected(t *testing.T) {
	key := bytes.Repeat([]byte{0xAA}, 32)
	signer, _ := NewHMACSigner("validator-01", key)
	verifier := NewHMACSignerVerifier()
	_ = verifier.Register("validator-01", key)
	msg := []byte("example")
	sig, _ := signer.Sign(msg)
	sig[0] ^= 0x01
	err := verifier.VerifySignature("validator-01", msg, sig)
	if !errors.Is(err, ErrChallengeSignatureBad) {
		t.Fatalf("expected ErrChallengeSignatureBad, got %v", err)
	}
}

func TestHMACSignerVerifier_UnknownSigner(t *testing.T) {
	v := NewHMACSignerVerifier()
	err := v.VerifySignature("nobody", []byte("x"), []byte("x"))
	if !errors.Is(err, ErrChallengeUnknownSigner) {
		t.Fatalf("expected ErrChallengeUnknownSigner, got %v", err)
	}
}

func TestHMACSignerVerifier_DuplicateRegisterErrors(t *testing.T) {
	v := NewHMACSignerVerifier()
	key := bytes.Repeat([]byte{0xAA}, 16)
	if err := v.Register("a", key); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := v.Register("a", key); err == nil {
		t.Fatal("duplicate register should error")
	}
}

func TestHMACSignerVerifier_Rotate(t *testing.T) {
	v := NewHMACSignerVerifier()
	k1 := bytes.Repeat([]byte{0xAA}, 16)
	k2 := bytes.Repeat([]byte{0xBB}, 16)
	_ = v.Register("a", k1)
	if err := v.Rotate("a", k2); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	// Signature made with k1 must no longer verify.
	oldSigner, _ := NewHMACSigner("a", k1)
	sig, _ := oldSigner.Sign([]byte("msg"))
	if err := v.VerifySignature("a", []byte("msg"), sig); !errors.Is(err, ErrChallengeSignatureBad) {
		t.Fatalf("after rotate, old signature should not verify; got %v", err)
	}
}

func TestNewHMACSigner_Validates(t *testing.T) {
	_, err := NewHMACSigner("", bytes.Repeat([]byte{1}, 16))
	if err == nil {
		t.Fatal("empty signerID should error")
	}
	_, err = NewHMACSigner("a", bytes.Repeat([]byte{1}, 15))
	if err == nil {
		t.Fatal("short key should error")
	}
}

// -----------------------------------------------------------------------------
// Issuer: mint + verify end-to-end + retention
// -----------------------------------------------------------------------------

// determRand returns a rand-like function whose output is
// guaranteed-distinct across calls. Each call writes a
// per-call-incrementing uint64 in big-endian at the start of b
// and leaves the remainder as it was (the slice comes from the
// caller, which zero-inits [32]byte fields). Distinct per-call
// integers therefore yield distinct output slices.
//
// NOTE: do NOT naively truncate a counter to bytes — that cycles
// with period 256 on a 32-byte fill and causes Issue #9 to equal
// Issue #1.
func determRand() func([]byte) error {
	var mu sync.Mutex
	var counter uint64
	return func(b []byte) error {
		mu.Lock()
		counter++
		c := counter
		mu.Unlock()
		if len(b) >= 8 {
			b[0] = byte(c >> 56)
			b[1] = byte(c >> 48)
			b[2] = byte(c >> 40)
			b[3] = byte(c >> 32)
			b[4] = byte(c >> 24)
			b[5] = byte(c >> 16)
			b[6] = byte(c >> 8)
			b[7] = byte(c)
		}
		return nil
	}
}

func newTestIssuer(t *testing.T, clock func() time.Time) (*Issuer, *HMACSignerVerifier, []byte) {
	t.Helper()
	key := bytes.Repeat([]byte{0x42}, 32)
	signer, err := NewHMACSigner("validator-01", key)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	iss, err := NewIssuer(signer, WithClock(clock), WithRand(determRand()))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	v := NewHMACSignerVerifier()
	if err := v.Register("validator-01", key); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return iss, v, key
}

func TestIssuer_MintAndVerify_HappyPath(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	iss, v, _ := newTestIssuer(t, func() time.Time { return fixed })

	c, err := iss.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if c.IssuedAt != fixed.Unix() {
		t.Fatalf("IssuedAt = %d, want %d", c.IssuedAt, fixed.Unix())
	}
	if c.SignerID != "validator-01" {
		t.Fatalf("SignerID = %q, want validator-01", c.SignerID)
	}
	if len(c.Signature) != 32 {
		t.Fatalf("Signature len = %d, want 32", len(c.Signature))
	}
	if !iss.RecentlyIssued(c.Nonce) {
		t.Fatal("RecentlyIssued should report true immediately after Issue")
	}

	// Verify succeeds with fresh now.
	if err := c.Verify(fixed.Unix(), c.IssuedAt, 60, 5, v); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestIssuer_Mint_ProducesDistinctNonces(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	iss, _, _ := newTestIssuer(t, func() time.Time { return fixed })

	seen := make(map[[32]byte]struct{})
	for i := 0; i < 16; i++ {
		c, err := iss.Issue()
		if err != nil {
			t.Fatalf("Issue %d: %v", i, err)
		}
		if _, dup := seen[c.Nonce]; dup {
			t.Fatalf("Issue #%d produced duplicate nonce %x", i, c.Nonce)
		}
		seen[c.Nonce] = struct{}{}
	}
}

func TestChallenge_Verify_StaleRejected(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	iss, v, _ := newTestIssuer(t, func() time.Time { return fixed })
	c, err := iss.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// 61 seconds later — past the 60s freshness window.
	err = c.Verify(fixed.Unix()+61, c.IssuedAt, 60, 5, v)
	if !errors.Is(err, ErrChallengeStale) {
		t.Fatalf("expected ErrChallengeStale, got %v", err)
	}
}

func TestChallenge_Verify_FutureSkewRejected(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	iss, v, _ := newTestIssuer(t, func() time.Time { return fixed })
	c, err := iss.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	// now is 10s BEFORE issuance — outside the 5s skew tolerance.
	err = c.Verify(fixed.Unix()-10, c.IssuedAt, 60, 5, v)
	if !errors.Is(err, ErrChallengeStale) {
		t.Fatalf("expected ErrChallengeStale for future-issued, got %v", err)
	}
}

func TestChallenge_Verify_UnknownSigner(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	iss, _, _ := newTestIssuer(t, func() time.Time { return fixed })
	c, _ := iss.Issue()
	// Verifier with a DIFFERENT registry.
	other := NewHMACSignerVerifier()
	_ = other.Register("some-other-validator", bytes.Repeat([]byte{1}, 16))
	err := c.Verify(fixed.Unix(), c.IssuedAt, 60, 5, other)
	if !errors.Is(err, ErrChallengeUnknownSigner) {
		t.Fatalf("expected ErrChallengeUnknownSigner, got %v", err)
	}
}

func TestChallenge_Verify_TamperedSignature(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	iss, v, _ := newTestIssuer(t, func() time.Time { return fixed })
	c, _ := iss.Issue()
	// Tamper with the nonce so the signature no longer covers
	// the claimed payload.
	c.Nonce[0] ^= 0x01
	err := c.Verify(fixed.Unix(), c.IssuedAt, 60, 5, v)
	if !errors.Is(err, ErrChallengeSignatureBad) {
		t.Fatalf("expected ErrChallengeSignatureBad after nonce tamper, got %v", err)
	}
}

func TestChallenge_Verify_NilSignerVerifier(t *testing.T) {
	c := Challenge{IssuedAt: 1_700_000_000}
	err := c.Verify(c.IssuedAt, c.IssuedAt, 60, 5, nil)
	if err == nil {
		t.Fatal("nil SignerVerifier should error")
	}
}

// -----------------------------------------------------------------------------
// Issuer retention / eviction
// -----------------------------------------------------------------------------

func TestIssuer_RecentlyIssued_EvictsOutsideRetention(t *testing.T) {
	var now time.Time
	clock := func() time.Time { return now }
	key := bytes.Repeat([]byte{0x42}, 32)
	signer, _ := NewHMACSigner("validator-01", key)
	iss, err := NewIssuer(signer,
		WithClock(clock),
		WithRand(determRand()),
		WithRetention(30*time.Second),
	)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	now = time.Unix(1_700_000_000, 0)
	c, err := iss.Issue()
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if !iss.RecentlyIssued(c.Nonce) {
		t.Fatal("nonce should be in the seen-set immediately after issue")
	}

	// Advance the clock past retention + 1 second.
	now = now.Add(31 * time.Second)
	if iss.RecentlyIssued(c.Nonce) {
		t.Fatal("nonce should have been evicted past retention")
	}
}

func TestIssuer_NewIssuer_Validates(t *testing.T) {
	if _, err := NewIssuer(nil); err == nil {
		t.Fatal("nil signer should error")
	}
	// Empty SignerID.
	badSigner := &HMACSigner{id: "", key: bytes.Repeat([]byte{1}, 16)}
	if _, err := NewIssuer(badSigner); err == nil {
		t.Fatal("empty SignerID should error")
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func TestNonceHex_Deterministic(t *testing.T) {
	var n [32]byte
	for i := range n {
		n[i] = byte(i)
	}
	got := NonceHex(n)
	want := hex.EncodeToString(n[:])
	if got != want {
		t.Fatalf("NonceHex = %q, want %q", got, want)
	}
}
