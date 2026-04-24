package attest

// Tests for Dispatcher. Two test flavors:
//
//   1. Unit tests — lightweight, use a stubAttestationVerifier
//      that records the call and returns a canned error, so we
//      can assert on routing behaviour without pulling in any
//      real cryptographic verifier.
//
//   2. Integration test — wires a real hmac.Verifier through the
//      Dispatcher and asserts an end-to-end v2 proof passes. This
//      is the only test in this file that imports a concrete
//      verifier subpackage; it exists to prove the Dispatcher
//      does not accidentally corrupt the proof on its way through.
//
// The two flavors are split because the integration test is
// expensive to maintain (HMAC key material, bundle serialization,
// etc.) and we do not want to pay that cost in every routing
// test.

import (
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

// stubAttestationVerifier records each call it receives and
// returns the configured error (may be nil).
type stubAttestationVerifier struct {
	called     bool
	sawType    string
	sawNow     time.Time
	returnsErr error
}

func (s *stubAttestationVerifier) VerifyAttestation(p mining.Proof, now time.Time) error {
	s.called = true
	s.sawType = p.Attestation.Type
	s.sawNow = now
	return s.returnsErr
}

// ----- unit tests --------------------------------------------------

func TestDispatcher_Empty_RejectsWithUnknownType(t *testing.T) {
	d := NewDispatcher()
	p := mining.Proof{Attestation: mining.Attestation{Type: mining.AttestationTypeHMAC}}
	err := d.VerifyAttestation(p, time.Unix(0, 0))
	if !errors.Is(err, mining.ErrAttestationTypeUnknown) {
		t.Fatalf("empty dispatcher should reject with type-unknown, got %v", err)
	}
}

func TestDispatcher_EmptyType_RejectsWithRequired(t *testing.T) {
	d := NewDispatcher()
	// Empty type but some registered verifier — the empty-type
	// branch should still fire because the outer check for
	// Attestation.Type == "" happens before the map lookup.
	d.MustRegister(mining.AttestationTypeHMAC, &stubAttestationVerifier{})
	p := mining.Proof{Attestation: mining.Attestation{Type: ""}}
	err := d.VerifyAttestation(p, time.Unix(0, 0))
	if !errors.Is(err, mining.ErrAttestationRequired) {
		t.Fatalf("empty type should reject with attestation-required, got %v", err)
	}
}

func TestDispatcher_Registered_Dispatches(t *testing.T) {
	d := NewDispatcher()
	stub := &stubAttestationVerifier{returnsErr: nil}
	d.MustRegister(mining.AttestationTypeHMAC, stub)
	now := time.Unix(1_700_000_000, 0)
	p := mining.Proof{Attestation: mining.Attestation{Type: mining.AttestationTypeHMAC}}
	if err := d.VerifyAttestation(p, now); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !stub.called {
		t.Fatal("stub verifier was not called")
	}
	if stub.sawType != mining.AttestationTypeHMAC {
		t.Fatalf("stub saw type %q, want %q", stub.sawType, mining.AttestationTypeHMAC)
	}
	if !stub.sawNow.Equal(now) {
		t.Fatalf("stub saw now %v, want %v", stub.sawNow, now)
	}
}

func TestDispatcher_UnknownType_RejectsWithUnknown(t *testing.T) {
	d := NewDispatcher()
	d.MustRegister(mining.AttestationTypeHMAC, &stubAttestationVerifier{})
	p := mining.Proof{Attestation: mining.Attestation{Type: "nvidia-futuretype-v9"}}
	err := d.VerifyAttestation(p, time.Unix(0, 0))
	if !errors.Is(err, mining.ErrAttestationTypeUnknown) {
		t.Fatalf("unknown type should reject with type-unknown, got %v", err)
	}
}

func TestDispatcher_PassthroughError(t *testing.T) {
	d := NewDispatcher()
	sentinel := errors.New("inner verifier said no")
	d.MustRegister(mining.AttestationTypeHMAC, &stubAttestationVerifier{returnsErr: sentinel})
	p := mining.Proof{Attestation: mining.Attestation{Type: mining.AttestationTypeHMAC}}
	err := d.VerifyAttestation(p, time.Unix(0, 0))
	if !errors.Is(err, sentinel) {
		t.Fatalf("dispatcher must passthrough inner error unchanged, got %v", err)
	}
}

// ----- Register edge cases -----------------------------------------

func TestDispatcher_Register_EmptyType(t *testing.T) {
	d := NewDispatcher()
	if err := d.Register("", &stubAttestationVerifier{}); err == nil {
		t.Fatal("Register with empty type should error")
	}
}

func TestDispatcher_Register_NilVerifier(t *testing.T) {
	d := NewDispatcher()
	if err := d.Register(mining.AttestationTypeHMAC, nil); err == nil {
		t.Fatal("Register with nil verifier should error")
	}
}

func TestDispatcher_Register_Duplicate(t *testing.T) {
	d := NewDispatcher()
	if err := d.Register(mining.AttestationTypeHMAC, &stubAttestationVerifier{}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := d.Register(mining.AttestationTypeHMAC, &stubAttestationVerifier{}); err == nil {
		t.Fatal("duplicate register should error")
	}
}

func TestDispatcher_MustRegister_PanicsOnError(t *testing.T) {
	d := NewDispatcher()
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustRegister should panic on duplicate")
		}
	}()
	d.MustRegister(mining.AttestationTypeHMAC, &stubAttestationVerifier{})
	d.MustRegister(mining.AttestationTypeHMAC, &stubAttestationVerifier{}) // panics
}

// ----- RegisteredTypes & AssertAllRegistered -----------------------

func TestDispatcher_RegisteredTypes_SortedAndComplete(t *testing.T) {
	d := NewDispatcher()
	d.MustRegister(mining.AttestationTypeCC, &stubAttestationVerifier{})
	d.MustRegister(mining.AttestationTypeHMAC, &stubAttestationVerifier{})
	got := d.RegisteredTypes()
	want := []string{mining.AttestationTypeCC, mining.AttestationTypeHMAC} // alphabetical
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RegisteredTypes = %v, want %v", got, want)
	}
}

func TestDispatcher_AssertAllRegistered_Pass(t *testing.T) {
	d := NewDispatcher()
	d.MustRegister(mining.AttestationTypeHMAC, &stubAttestationVerifier{})
	d.MustRegister(mining.AttestationTypeCC, &stubAttestationVerifier{})
	if err := d.AssertAllRegistered(mining.AttestationTypeHMAC, mining.AttestationTypeCC); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestDispatcher_AssertAllRegistered_ReportsMissing(t *testing.T) {
	d := NewDispatcher()
	d.MustRegister(mining.AttestationTypeHMAC, &stubAttestationVerifier{})
	err := d.AssertAllRegistered(mining.AttestationTypeHMAC, mining.AttestationTypeCC)
	if err == nil {
		t.Fatal("should report missing cc verifier")
	}
	if !contains(err.Error(), mining.AttestationTypeCC) {
		t.Fatalf("error should name missing type, got %v", err)
	}
	if contains(err.Error(), mining.AttestationTypeHMAC) {
		t.Fatalf("error should NOT name registered type, got %v", err)
	}
}

func TestDispatcher_AssertAllRegistered_NoRequired(t *testing.T) {
	d := NewDispatcher()
	if err := d.AssertAllRegistered(); err != nil {
		t.Fatalf("zero required types must be a no-op: %v", err)
	}
}

// ----- integration with hmac.Verifier ------------------------------

// TestDispatcher_Integration_HMACEndToEnd is the only place in
// this package that uses a real attestation verifier subpackage.
// It asserts the Dispatcher does not corrupt the Proof on its way
// through: a valid hmac-signed proof accepted by hmac.Verifier
// directly must ALSO be accepted when funneled through the
// Dispatcher.
func TestDispatcher_Integration_HMACEndToEnd(t *testing.T) {
	const (
		nodeID  = "alice-rtx4090-01"
		gpuUUID = "GPU-01234567-89ab-cdef-0123-456789abcdef"
	)
	hmacKey := []byte("test-key-do-not-use----32-bytes!")

	reg := hmac.NewInMemoryRegistry()
	if err := reg.Enroll(nodeID, gpuUUID, hmacKey); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	hmacV := hmac.NewVerifier(reg)

	d := NewDispatcher()
	d.MustRegister(mining.AttestationTypeHMAC, hmacV)

	// Build a fully-consistent v2 proof + bundle.
	now := time.Unix(1_700_000_000, 0)
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	var batchRoot [32]byte
	for i := range batchRoot {
		batchRoot[i] = byte(i)
	}
	var mix [32]byte
	for i := range mix {
		mix[i] = byte(0xFF - i)
	}
	const minerAddr = "qsdm1integration"

	bundle := hmac.Bundle{
		ChallengeBind: hmac.HexChallengeBind(minerAddr, batchRoot, mix),
		ComputeCap:    "8.9",
		CUDAVersion:   "12.8",
		DriverVer:     "572.16",
		GPUName:       "NVIDIA GeForce RTX 4090",
		GPUUUID:       gpuUUID,
		IssuedAt:      now.Unix(),
		NodeID:        nodeID,
		Nonce:         hex.EncodeToString(nonce[:]),
	}
	signed, err := bundle.Sign(hmacKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	b64, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Epoch:      0,
		Height:     100,
		HeaderHash: [32]byte{0xAA},
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Nonce:      [16]byte{0x03},
		MixDigest:  mix,
		MinerAddr:  minerAddr,
		Attestation: mining.Attestation{
			Type:         mining.AttestationTypeHMAC,
			BundleBase64: b64,
			GPUArch:      "ada",
			Nonce:        nonce,
			IssuedAt:     now.Unix(),
		},
	}

	if err := d.VerifyAttestation(p, now); err != nil {
		t.Fatalf("dispatcher + hmac end-to-end rejected valid proof: %v", err)
	}

	// CC-type proof must be unknown-type rejected because we only
	// registered HMAC.
	p.Attestation.Type = mining.AttestationTypeCC
	err = d.VerifyAttestation(p, now)
	if !errors.Is(err, mining.ErrAttestationTypeUnknown) {
		t.Fatalf("cc without verifier should be unknown-type, got %v", err)
	}
}

// contains is a small local helper for substring checks on error
// messages — stdlib strings.Contains would force another import
// for one-line use.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
