package v2client

// Unit tests for FetchChallenge + BuildHMACAttestation + friends.
// End-to-end tests (everything wired together) live in
// integration_test.go to keep the individual-function coverage
// fast and the full-stack coverage isolated.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// TestChallengeWireMatchesAPI is the build-time guard against
// the package-private challengeWire drifting from
// api.ChallengeWire. If this test fails, someone changed one
// but not both — miners will silently stop being able to decode
// validator responses.
func TestChallengeWireMatchesAPI(t *testing.T) {
	a := api.ChallengeWire{
		Nonce:     hex.EncodeToString(bytes.Repeat([]byte{0xAA}, 32)),
		IssuedAt:  1_700_000_000,
		SignerID:  "validator-01",
		Signature: "cafebabe",
	}
	aBytes, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal api: %v", err)
	}

	var b challengeWire
	if err := json.Unmarshal(aBytes, &b); err != nil {
		t.Fatalf("unmarshal into v2client: %v", err)
	}
	bBytes, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal v2client: %v", err)
	}
	if !bytes.Equal(aBytes, bBytes) {
		t.Fatalf("wire schema drift:\n  api: %s\n  v2c: %s", aBytes, bBytes)
	}
}

// ----- FetchChallenge -----------------------------------------------------

func TestFetchChallenge_HappyPath(t *testing.T) {
	want := api.ChallengeWire{
		Nonce:     hex.EncodeToString(bytes.Repeat([]byte{0x7E}, 32)),
		IssuedAt:  1_700_000_000,
		SignerID:  "validator-01",
		Signature: hex.EncodeToString([]byte("sig-bytes-here-32bytes-goes-here!")),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/mining/challenge" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %q", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))
	defer srv.Close()

	c, err := FetchChallenge(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("FetchChallenge: %v", err)
	}
	if c.IssuedAt != want.IssuedAt {
		t.Fatalf("IssuedAt: got %d want %d", c.IssuedAt, want.IssuedAt)
	}
	if c.SignerID != want.SignerID {
		t.Fatalf("SignerID: got %q want %q", c.SignerID, want.SignerID)
	}
	if hex.EncodeToString(c.Nonce[:]) != want.Nonce {
		t.Fatalf("Nonce: got %x want %s", c.Nonce, want.Nonce)
	}
	if hex.EncodeToString(c.Signature) != want.Signature {
		t.Fatalf("Signature: got %x want %s", c.Signature, want.Signature)
	}
}

func TestFetchChallenge_Returns503(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"mining_unavailable"}`))
	}))
	defer srv.Close()
	_, err := FetchChallenge(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error on 503")
	}
}

func TestFetchChallenge_BadNonceHex(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nonce":"not-hex!","issued_at":1,"signer_id":"v","signature":"cafe"}`))
	}))
	defer srv.Close()
	_, err := FetchChallenge(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error on non-hex nonce")
	}
}

func TestFetchChallenge_ShortNonce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nonce":"cafe","issued_at":1,"signer_id":"v","signature":"cafe"}`))
	}))
	defer srv.Close()
	_, err := FetchChallenge(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error on short nonce")
	}
}

func TestFetchChallenge_EmptySignerID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nonce":"` +
			hex.EncodeToString(bytes.Repeat([]byte{1}, 32)) +
			`","issued_at":1,"signer_id":"","signature":"cafe"}`))
	}))
	defer srv.Close()
	_, err := FetchChallenge(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("expected error on empty signer_id")
	}
}

func TestFetchChallenge_UnknownFieldRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"nonce":"` +
			hex.EncodeToString(bytes.Repeat([]byte{1}, 32)) +
			`","issued_at":1,"signer_id":"v","signature":"cafe","extra_field":42}`))
	}))
	defer srv.Close()
	_, err := FetchChallenge(context.Background(), srv.Client(), srv.URL)
	if err == nil {
		t.Fatal("DisallowUnknownFields should reject extra_field")
	}
}

func TestFetchChallenge_NilClient(t *testing.T) {
	_, err := FetchChallenge(context.Background(), nil, "http://x")
	if err == nil {
		t.Fatal("nil client should error")
	}
}

// ----- BundleInputs.Validate --------------------------------------------

func TestBundleInputs_Validate_Accept(t *testing.T) {
	in := BundleInputs{
		NodeID:    "a",
		GPUUUID:   "GPU-abc",
		HMACKey:   bytes.Repeat([]byte{1}, 16),
		MinerAddr: "q1",
		Challenge: challenge.Challenge{
			SignerID:  "v",
			Signature: []byte{1, 2, 3},
			IssuedAt:  1,
		},
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestBundleInputs_Validate_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*BundleInputs)
	}{
		{"empty node id", func(in *BundleInputs) { in.NodeID = "" }},
		{"empty gpu uuid", func(in *BundleInputs) { in.GPUUUID = "" }},
		{"short key", func(in *BundleInputs) { in.HMACKey = []byte{1} }},
		{"empty miner addr", func(in *BundleInputs) { in.MinerAddr = "" }},
		{"empty signer id", func(in *BundleInputs) { in.Challenge.SignerID = "" }},
		{"empty sig", func(in *BundleInputs) { in.Challenge.Signature = nil }},
		{"non-positive issued_at", func(in *BundleInputs) { in.Challenge.IssuedAt = 0 }},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			in := BundleInputs{
				NodeID:    "a",
				GPUUUID:   "GPU-abc",
				HMACKey:   bytes.Repeat([]byte{1}, 16),
				MinerAddr: "q1",
				Challenge: challenge.Challenge{SignerID: "v", Signature: []byte{1}, IssuedAt: 1},
			}
			tc.mutate(&in)
			if err := in.Validate(); err == nil {
				t.Fatalf("%s: Validate should have rejected", tc.name)
			}
		})
	}
}

// ----- BuildHMACAttestation / AttachToProof -----------------------------

func TestBuildHMACAttestation_StableAcrossInputs(t *testing.T) {
	var nonce [32]byte
	for i := range nonce {
		nonce[i] = byte(i)
	}
	in := BundleInputs{
		NodeID:      "alice-rtx4090-01",
		GPUUUID:     "GPU-01234567-89ab-cdef-0123-456789abcdef",
		GPUName:     "NVIDIA GeForce RTX 4090",
		ComputeCap:  "8.9",
		CUDAVersion: "12.8",
		DriverVer:   "572.16",
		HMACKey:     bytes.Repeat([]byte{0x42}, 32),
		MinerAddr:   "qsdm1test",
		BatchRoot:   [32]byte{0xAA},
		MixDigest:   [32]byte{0xBB},
		Challenge: challenge.Challenge{
			Nonce:     nonce,
			IssuedAt:  1_700_000_000,
			SignerID:  "validator-01",
			Signature: []byte{0xDE, 0xAD, 0xBE, 0xEF},
		},
	}
	att, err := BuildHMACAttestation(in, "ada")
	if err != nil {
		t.Fatalf("BuildHMACAttestation: %v", err)
	}
	if att.Type != mining.AttestationTypeHMAC {
		t.Fatalf("Type: got %q want %q", att.Type, mining.AttestationTypeHMAC)
	}
	if att.GPUArch != "ada" {
		t.Fatalf("GPUArch: got %q", att.GPUArch)
	}
	if att.Nonce != nonce {
		t.Fatalf("Nonce not plumbed through")
	}
	if att.IssuedAt != 1_700_000_000 {
		t.Fatalf("IssuedAt not plumbed through")
	}
	if att.BundleBase64 == "" {
		t.Fatal("BundleBase64 empty")
	}
	// Call twice with identical inputs; output must be identical.
	att2, err := BuildHMACAttestation(in, "ada")
	if err != nil {
		t.Fatalf("BuildHMACAttestation (2nd): %v", err)
	}
	if att.BundleBase64 != att2.BundleBase64 {
		t.Fatal("BuildHMACAttestation not deterministic")
	}
}

func TestBuildHMACAttestation_Rejects_BadInputs(t *testing.T) {
	_, err := BuildHMACAttestation(BundleInputs{}, "")
	if err == nil {
		t.Fatal("empty inputs should error")
	}
}

func TestAttachToProof_SetsVersionAndAttestation(t *testing.T) {
	p := &mining.Proof{Version: mining.ProtocolVersion}
	att := mining.Attestation{Type: mining.AttestationTypeHMAC, GPUArch: "ada"}
	if err := AttachToProof(p, att); err != nil {
		t.Fatalf("AttachToProof: %v", err)
	}
	if p.Version != mining.ProtocolVersionV2 {
		t.Fatalf("Version: got %d want %d", p.Version, mining.ProtocolVersionV2)
	}
	if p.Attestation.Type != mining.AttestationTypeHMAC {
		t.Fatalf("Attestation not attached")
	}
}

func TestAttachToProof_NilProof(t *testing.T) {
	if err := AttachToProof(nil, mining.Attestation{}); err == nil {
		t.Fatal("nil proof should error")
	}
}
