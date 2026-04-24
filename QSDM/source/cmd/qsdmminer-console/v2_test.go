package main

// v2_test.go covers the v2-path plumbing that v2.go introduces:
//
//   - LoadV2Context: disabled / enabled / missing field / bad key
//     scenarios. This is a *startup* gate so we want coverage
//     that any misconfiguration aborts before the mining loop
//     starts, never silently.
//
//   - loadHMACKeyFromFile: file-shape rejections (empty, multi
//     line, bad hex). The hex-only contract is load-bearing —
//     if miners accidentally point at a binary-format file and
//     the loader papers over it, their HMACs silently never
//     validate.
//
//   - V2PrepareAttestation: happy-path against a fake
//     challenge endpoint, verifying that the resulting
//     Attestation has the correct Type, GPUArch, and a
//     non-empty Blob (the signed HMAC bundle). The round-trip
//     correctness of the bundle itself is owned by
//     pkg/mining/v2client tests — here we only prove the glue.
//
// We intentionally do NOT spin up a full runLoop here: that's
// covered by TestIntegration_RunLoop_EndToEnd in integration_test.go
// and doubling it into a v2 variant doesn't add regression
// coverage — if runLoop breaks, the v1 variant already catches
// it, and the v2-specific glue is exercised piece-wise.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// -----------------------------------------------------------------------------
// loadHMACKeyFromFile
// -----------------------------------------------------------------------------

func writeHexKey(t *testing.T, dir, name string, raw []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(hex.EncodeToString(raw)), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestLoadHMACKeyFromFile_OK(t *testing.T) {
	dir := t.TempDir()
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	p := writeHexKey(t, dir, "key.hex", raw)

	got, err := loadHMACKeyFromFile(p)
	if err != nil {
		t.Fatalf("loadHMACKeyFromFile: %v", err)
	}
	if !equalBytes(got, raw) {
		t.Errorf("round-trip mismatch:\n got  %x\n want %x", got, raw)
	}
}

func TestLoadHMACKeyFromFile_AcceptsTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	p := filepath.Join(dir, "key.hex")
	if err := os.WriteFile(p, []byte(hex.EncodeToString(raw)+"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := loadHMACKeyFromFile(p)
	if err != nil {
		t.Fatalf("loadHMACKeyFromFile: %v", err)
	}
	if !equalBytes(got, raw) {
		t.Errorf("round-trip mismatch")
	}
}

func TestLoadHMACKeyFromFile_EmptyRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.hex")
	if err := os.WriteFile(p, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadHMACKeyFromFile(p); err == nil {
		t.Fatal("expected error on whitespace-only file, got nil")
	}
}

func TestLoadHMACKeyFromFile_MultilineRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "multi.hex")
	if err := os.WriteFile(p, []byte("aa\nbb\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := loadHMACKeyFromFile(p)
	if err == nil {
		t.Fatal("expected error on multi-line file, got nil")
	}
	if !strings.Contains(err.Error(), "multiple lines") {
		t.Errorf("error should mention multiple lines: %v", err)
	}
}

func TestLoadHMACKeyFromFile_BadHexRejected(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.hex")
	if err := os.WriteFile(p, []byte("not-hex"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := loadHMACKeyFromFile(p); err == nil {
		t.Fatal("expected hex decode error, got nil")
	}
}

func TestLoadHMACKeyFromFile_MissingFileRejected(t *testing.T) {
	_, err := loadHMACKeyFromFile(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected open error on missing file, got nil")
	}
}

// -----------------------------------------------------------------------------
// LoadV2Context
// -----------------------------------------------------------------------------

func TestLoadV2Context_DisabledByDefault(t *testing.T) {
	ctx, err := LoadV2Context(V2Config{})
	if err != nil {
		t.Fatalf("LoadV2Context(empty): %v", err)
	}
	if ctx.IsEnabled() {
		t.Error("empty config should yield disabled V2Context")
	}
}

func TestLoadV2Context_DisabledWhenProtocolBlank(t *testing.T) {
	ctx, err := LoadV2Context(V2Config{NodeID: "alice", GPUUUID: "abc"})
	if err != nil {
		t.Fatalf("LoadV2Context: %v", err)
	}
	if ctx.IsEnabled() {
		t.Error("protocol=\"\" should not enable v2 even with other fields set")
	}
}

func TestLoadV2Context_EnabledHappyPath(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	keyPath := writeHexKey(t, dir, "op.hex", key)

	cfg := V2Config{
		Protocol:    "v2",
		NodeID:      "alice-rtx4090-01",
		GPUUUID:     "GPU-abcd1234",
		GPUName:     "NVIDIA GeForce RTX 4090",
		GPUArch:     "Ada", // should be lowercased
		ComputeCap:  "8.9",
		CUDAVersion: "12.8",
		DriverVer:   "572.16",
		HMACKeyPath: keyPath,
	}
	ctx, err := LoadV2Context(cfg)
	if err != nil {
		t.Fatalf("LoadV2Context: %v", err)
	}
	if !ctx.IsEnabled() {
		t.Fatal("expected enabled V2Context")
	}
	if ctx.NodeID != "alice-rtx4090-01" {
		t.Errorf("NodeID: got %q", ctx.NodeID)
	}
	if ctx.GPUArch != "ada" {
		t.Errorf("GPUArch should be lowercased, got %q", ctx.GPUArch)
	}
	if !equalBytes(ctx.HMACKey, key) {
		t.Error("HMACKey not loaded correctly")
	}
}

func TestLoadV2Context_EnabledCaseInsensitiveProtocol(t *testing.T) {
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	keyPath := writeHexKey(t, dir, "op.hex", key)

	for _, proto := range []string{"V2", "v2", "V2 ", "v2"} {
		proto = strings.TrimSpace(proto)
		cfg := V2Config{
			Protocol:    proto,
			NodeID:      "n",
			GPUUUID:     "g",
			HMACKeyPath: keyPath,
		}
		ctx, err := LoadV2Context(cfg)
		if err != nil {
			t.Fatalf("protocol=%q: %v", proto, err)
		}
		if !ctx.IsEnabled() {
			t.Errorf("protocol=%q should enable v2", proto)
		}
	}
}

func TestLoadV2Context_MissingNodeID(t *testing.T) {
	_, err := LoadV2Context(V2Config{Protocol: "v2"})
	if err == nil || !strings.Contains(err.Error(), "node-id") {
		t.Fatalf("want node-id error, got %v", err)
	}
}

func TestLoadV2Context_MissingGPUUUID(t *testing.T) {
	_, err := LoadV2Context(V2Config{Protocol: "v2", NodeID: "alice"})
	if err == nil || !strings.Contains(err.Error(), "gpu-uuid") {
		t.Fatalf("want gpu-uuid error, got %v", err)
	}
}

func TestLoadV2Context_MissingHMACKeyPath(t *testing.T) {
	_, err := LoadV2Context(V2Config{Protocol: "v2", NodeID: "n", GPUUUID: "g"})
	if err == nil || !strings.Contains(err.Error(), "hmac-key-path") {
		t.Fatalf("want hmac-key-path error, got %v", err)
	}
}

func TestLoadV2Context_ShortHMACKeyRejected(t *testing.T) {
	dir := t.TempDir()
	// 16 bytes: below MinHMACKeyLen (32) enforced by enrollment.
	keyPath := writeHexKey(t, dir, "short.hex", make([]byte, 16))
	_, err := LoadV2Context(V2Config{
		Protocol: "v2", NodeID: "n", GPUUUID: "g", HMACKeyPath: keyPath,
	})
	if err == nil || !strings.Contains(err.Error(), "minimum is 32") {
		t.Fatalf("want short-key error, got %v", err)
	}
}

func TestLoadV2Context_LongHMACKeyRejected(t *testing.T) {
	dir := t.TempDir()
	keyPath := writeHexKey(t, dir, "long.hex", make([]byte, 200))
	_, err := LoadV2Context(V2Config{
		Protocol: "v2", NodeID: "n", GPUUUID: "g", HMACKeyPath: keyPath,
	})
	if err == nil || !strings.Contains(err.Error(), "maximum is 128") {
		t.Fatalf("want long-key error, got %v", err)
	}
}

func TestLoadV2Context_BadKeyPathSurfacesError(t *testing.T) {
	_, err := LoadV2Context(V2Config{
		Protocol: "v2", NodeID: "n", GPUUUID: "g",
		HMACKeyPath: filepath.Join(t.TempDir(), "nope"),
	})
	if err == nil || !strings.Contains(err.Error(), "load hmac key") {
		t.Fatalf("want load hmac key error, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// V2PrepareAttestation
// -----------------------------------------------------------------------------

// newFakeChallengeServer returns an httptest server that mints a
// challenge with the given signer on every call. Every test that
// needs a v2-shaped server uses this so the wire format stays
// centralised.
func newFakeChallengeServer(t *testing.T, sig challenge.Signer) *httptest.Server {
	t.Helper()
	iss, err := challenge.NewIssuer(sig)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/challenge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		c, err := iss.Mint()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Wire schema mirrors api.ChallengeWire. Duplicated here
		// rather than imported so we don't need to pull in the
		// HTTP handler — a regression in the shape is caught by
		// v2client.TestChallengeWireMatchesAPI.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nonce":     hex.EncodeToString(c.Nonce[:]),
			"issued_at": c.IssuedAt,
			"signer_id": c.SignerID,
			"signature": hex.EncodeToString(c.Signature),
		})
	})
	return httptest.NewServer(mux)
}

// mustV2Context is a test helper that builds a valid V2Context
// from a temp HMAC key, panicking on any setup error so each
// test stays short.
func mustV2Context(t *testing.T) *V2Context {
	t.Helper()
	dir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	keyPath := writeHexKey(t, dir, "op.hex", key)
	ctx, err := LoadV2Context(V2Config{
		Protocol:    "v2",
		NodeID:      "alice-rtx4090-01",
		GPUUUID:     "GPU-abcd1234",
		GPUName:     "NVIDIA GeForce RTX 4090",
		GPUArch:     "ada",
		ComputeCap:  "8.9",
		CUDAVersion: "12.8",
		DriverVer:   "572.16",
		HMACKeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("LoadV2Context: %v", err)
	}
	return ctx
}

func TestV2PrepareAttestation_HappyPath(t *testing.T) {
	sigKey := make([]byte, 32)
	_, _ = rand.Read(sigKey)
	signer, err := challenge.NewHMACSigner("validator-1", sigKey)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	srv := newFakeChallengeServer(t, signer)
	defer srv.Close()

	v2 := mustV2Context(t)
	proof := &mining.Proof{
		Version:    mining.ProtocolVersion,
		Epoch:      7,
		Height:     42,
		HeaderHash: [32]byte{0xAA},
		MinerAddr:  "qsdm1test",
		BatchRoot:  [32]byte{0xBB},
		BatchCount: 1,
		Nonce:      [16]byte{0xCC},
		MixDigest:  [32]byte{0xDD},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := V2PrepareAttestation(ctx, &http.Client{Timeout: 2 * time.Second}, srv.URL, v2, proof); err != nil {
		t.Fatalf("V2PrepareAttestation: %v", err)
	}

	if proof.Version != mining.ProtocolVersionV2 {
		t.Errorf("proof.Version: got %d want %d", proof.Version, mining.ProtocolVersionV2)
	}
	if proof.Attestation.Type == "" {
		t.Error("Attestation.Type should be set")
	}
	if proof.Attestation.BundleBase64 == "" {
		t.Error("Attestation.BundleBase64 should be set")
	}
	if proof.Attestation.GPUArch != "ada" {
		t.Errorf("GPUArch: got %q want ada", proof.Attestation.GPUArch)
	}
	// Attestation.Nonce is a fixed-size [32]byte array — length is
	// always 32 — so we can't meaningfully len-check it. Assert
	// the issuer actually populated it instead.
	var zero [32]byte
	if proof.Attestation.Nonce == zero {
		t.Error("Attestation.Nonce should be set to the challenge nonce")
	}
	if proof.Attestation.IssuedAt == 0 {
		t.Error("Attestation.IssuedAt should be set to challenge issue time")
	}
}

func TestV2PrepareAttestation_DisabledReturnsError(t *testing.T) {
	proof := &mining.Proof{MinerAddr: "x"}
	err := V2PrepareAttestation(context.Background(), http.DefaultClient, "http://nowhere", &V2Context{}, proof)
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("want disabled error, got %v", err)
	}
}

func TestV2PrepareAttestation_NilProofRejected(t *testing.T) {
	v2 := mustV2Context(t)
	err := V2PrepareAttestation(context.Background(), http.DefaultClient, "http://nowhere", v2, nil)
	if err == nil || !strings.Contains(err.Error(), "nil proof") {
		t.Fatalf("want nil proof error, got %v", err)
	}
}

func TestV2PrepareAttestation_ChallengeFailureSurfaces(t *testing.T) {
	// Server that always 503s — simulates validator not yet
	// ready or challenge issuer misconfigured.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"no issuer"}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	v2 := mustV2Context(t)
	proof := &mining.Proof{MinerAddr: "x"}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := V2PrepareAttestation(ctx, &http.Client{Timeout: 2 * time.Second}, srv.URL, v2, proof)
	if err == nil {
		t.Fatal("expected error on 503 from challenge endpoint")
	}
	if !strings.Contains(err.Error(), "fetch challenge") {
		t.Errorf("error should mention fetch challenge: %v", err)
	}
	// On failure, proof must remain untouched at v1. Otherwise a
	// transient failure corrupts the proof and a retry loop has
	// to know to un-corrupt it.
	if proof.Version != 0 && proof.Version != mining.ProtocolVersion {
		t.Errorf("proof.Version must not advance on failure, got %d", proof.Version)
	}
	if proof.Attestation.BundleBase64 != "" {
		t.Error("Attestation must remain empty on failure")
	}
}

// -----------------------------------------------------------------------------
// Config helpers
// -----------------------------------------------------------------------------

func TestConfig_V2Config_CopiesAllFields(t *testing.T) {
	c := Config{
		Protocol:    "v2",
		NodeID:      "n",
		GPUUUID:     "g",
		GPUName:     "gn",
		GPUArch:     "a",
		ComputeCap:  "cc",
		CUDAVersion: "cv",
		DriverVer:   "dv",
		HMACKeyPath: "/tmp/k",
	}
	v := c.v2Config()
	if v.Protocol != "v2" || v.NodeID != "n" || v.GPUUUID != "g" ||
		v.GPUName != "gn" || v.GPUArch != "a" || v.ComputeCap != "cc" ||
		v.CUDAVersion != "cv" || v.DriverVer != "dv" || v.HMACKeyPath != "/tmp/k" {
		t.Errorf("v2Config did not copy all fields: %+v", v)
	}
}

// -----------------------------------------------------------------------------
// Local helpers
// -----------------------------------------------------------------------------

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
