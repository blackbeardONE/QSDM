package main

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

func mustKeyN(t *testing.T, n int) []byte {
	t.Helper()
	k := make([]byte, n)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func mustHex32(t *testing.T) (string, []byte) {
	t.Helper()
	k := mustKeyN(t, 32)
	return hex.EncodeToString(k), k
}

// validProfile returns a signed ReferenceProfile that
// passes profile.Validate(). Tests build them with the
// signer they expect the registry to pin.
func validProfile(t *testing.T, signerID string, key []byte) *telemetry.ReferenceProfile {
	t.Helper()
	p := &telemetry.ReferenceProfile{
		SchemaVersion: telemetry.SchemaVersion,
		SignerID:      signerID,
		IssuedAt:      time.Now().Unix(),
		HostNote:      "unit-test",
		CollectorKind: "nvidia-smi",
		GPUs: []telemetry.GPUObservation{{
			UUID:               "GPU-test-0001",
			Name:               "NVIDIA GeForce RTX 3050",
			Vendor:             "NVIDIA",
			Architecture:       "ampere",
			ComputeCapability:  "8.6",
			MemoryTotalMB:      8192,
			PowerMaxW:          130,
			DriverVersionsSeen: []string{"576.28"},
			CUDAVersionsSeen:   []string{"12.9"},
		}},
	}
	if err := p.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	return p
}

// ---- registry: Add ----------------------------------------------------------

func TestPeerKeyRegistry_Add_Valid(t *testing.T) {
	r := NewPeerKeyRegistry()
	key := mustKeyN(t, 32)
	if err := r.Add("attester-deadbeefcafebabe", key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !r.HasPins() {
		t.Fatalf("HasPins=false after Add")
	}
}

func TestPeerKeyRegistry_Add_RejectsEmpty(t *testing.T) {
	r := NewPeerKeyRegistry()
	if err := r.Add("", mustKeyN(t, 32)); err == nil {
		t.Fatalf("Add accepted empty signer_id")
	}
}

func TestPeerKeyRegistry_Add_RejectsBadPrefix(t *testing.T) {
	r := NewPeerKeyRegistry()
	if err := r.Add("baseline", mustKeyN(t, 32)); err == nil {
		t.Fatalf("Add accepted signer_id without 'attester-' prefix")
	}
}

func TestPeerKeyRegistry_Add_RejectsShortKey(t *testing.T) {
	r := NewPeerKeyRegistry()
	if err := r.Add("attester-x", mustKeyN(t, 8)); err == nil {
		t.Fatalf("Add accepted 8-byte key")
	}
}

// ---- registry: VerifyAndAccept ---------------------------------------------

func TestVerifyAndAccept_NoPins_AcceptsAnything(t *testing.T) {
	r := NewPeerKeyRegistry()
	_, key := mustHex32(t)
	p := validProfile(t, "attester-foo", key)
	if err := r.VerifyAndAccept(p); err != nil {
		t.Fatalf("expected accept when no pins, got %v", err)
	}
	_, unpinned, _, _, _ := r.Counters()
	if unpinned != 1 {
		t.Errorf("acceptedUnpinned = %d want 1", unpinned)
	}
}

func TestVerifyAndAccept_PinnedAndSigned_Accepts(t *testing.T) {
	r := NewPeerKeyRegistry()
	_, key := mustHex32(t)
	if err := r.Add("attester-pinned1", key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	r.SetStrict(true)
	p := validProfile(t, "attester-pinned1", key)
	if err := r.VerifyAndAccept(p); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
	signed, _, _, _, _ := r.Counters()
	if signed != 1 {
		t.Errorf("acceptedSigned = %d want 1", signed)
	}
}

func TestVerifyAndAccept_PinnedAndForged_Rejects(t *testing.T) {
	r := NewPeerKeyRegistry()
	_, goodKey := mustHex32(t)
	_, attackerKey := mustHex32(t)
	if err := r.Add("attester-victim", goodKey); err != nil {
		t.Fatalf("Add: %v", err)
	}
	r.SetStrict(true)
	// Attacker signs WITH their own key, claiming the
	// victim's signer_id.
	p := validProfile(t, "attester-victim", attackerKey)
	if err := r.VerifyAndAccept(p); err == nil {
		t.Fatalf("expected reject on forged signature, got accept")
	}
	_, _, _, _, badSig := r.Counters()
	if badSig != 1 {
		t.Errorf("rejectedBadSig = %d want 1", badSig)
	}
}

func TestVerifyAndAccept_PinnedButUnsigned_Rejects(t *testing.T) {
	r := NewPeerKeyRegistry()
	_, key := mustHex32(t)
	if err := r.Add("attester-x", key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	r.SetStrict(true)
	p := validProfile(t, "attester-x", key)
	p.Signature = "" // strip signature
	if err := r.VerifyAndAccept(p); err == nil {
		t.Fatalf("expected reject on missing signature")
	}
	_, _, _, unsigned, _ := r.Counters()
	if unsigned != 1 {
		t.Errorf("rejectedUnsigned = %d want 1", unsigned)
	}
}

func TestVerifyAndAccept_StrictUnknownSigner_Rejects(t *testing.T) {
	r := NewPeerKeyRegistry()
	_, key := mustHex32(t)
	if err := r.Add("attester-pinned1", key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	r.SetStrict(true)
	_, otherKey := mustHex32(t)
	p := validProfile(t, "attester-stranger", otherKey)
	if err := r.VerifyAndAccept(p); err == nil {
		t.Fatalf("expected reject on unknown signer in strict mode")
	}
	_, _, unknown, _, _ := r.Counters()
	if unknown != 1 {
		t.Errorf("rejectedUnknown = %d want 1", unknown)
	}
}

func TestVerifyAndAccept_NonStrictUnknownSigner_Accepts(t *testing.T) {
	r := NewPeerKeyRegistry()
	_, key := mustHex32(t)
	if err := r.Add("attester-pinned1", key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	r.SetStrict(false)
	_, otherKey := mustHex32(t)
	p := validProfile(t, "attester-stranger", otherKey)
	if err := r.VerifyAndAccept(p); err != nil {
		t.Fatalf("expected accept (non-strict), got %v", err)
	}
	_, unpinned, _, _, _ := r.Counters()
	if unpinned != 1 {
		t.Errorf("acceptedUnpinned = %d want 1", unpinned)
	}
}

func TestVerifyAndAccept_NilProfile_Rejects(t *testing.T) {
	r := NewPeerKeyRegistry()
	if err := r.VerifyAndAccept(nil); err == nil {
		t.Fatalf("expected reject on nil profile")
	}
}

// ---- env loading ------------------------------------------------------------

func TestLoadPeerKeysFromEnv_NoneSet(t *testing.T) {
	t.Setenv("QSDM_PEER_ATTESTER_KEYS", "")
	t.Setenv("QSDM_PEER_ATTESTER_KEYS_FILE", "")
	t.Setenv("QSDM_PEER_ATTESTER_STRICT", "")
	reg, n, err := LoadPeerKeysFromEnv()
	if err != nil {
		t.Fatalf("LoadPeerKeysFromEnv: %v", err)
	}
	if n != 0 || reg.HasPins() {
		t.Errorf("expected empty registry, got n=%d HasPins=%v", n, reg.HasPins())
	}
}

func TestLoadPeerKeysFromEnv_FromString(t *testing.T) {
	hexKey, _ := mustHex32(t)
	hexKey2, _ := mustHex32(t)
	t.Setenv("QSDM_PEER_ATTESTER_KEYS",
		"attester-foo="+hexKey+";attester-bar="+hexKey2)
	t.Setenv("QSDM_PEER_ATTESTER_KEYS_FILE", "")
	t.Setenv("QSDM_PEER_ATTESTER_STRICT", "")
	reg, n, err := LoadPeerKeysFromEnv()
	if err != nil {
		t.Fatalf("LoadPeerKeysFromEnv: %v", err)
	}
	if n != 2 {
		t.Errorf("loaded count = %d want 2", n)
	}
	if !reg.Strict() {
		t.Errorf("Strict should default true once any pin loaded")
	}
}

func TestLoadPeerKeysFromEnv_StrictExplicitOff(t *testing.T) {
	hexKey, _ := mustHex32(t)
	t.Setenv("QSDM_PEER_ATTESTER_KEYS", "attester-x="+hexKey)
	t.Setenv("QSDM_PEER_ATTESTER_KEYS_FILE", "")
	t.Setenv("QSDM_PEER_ATTESTER_STRICT", "0")
	reg, _, err := LoadPeerKeysFromEnv()
	if err != nil {
		t.Fatalf("LoadPeerKeysFromEnv: %v", err)
	}
	if reg.Strict() {
		t.Errorf("Strict should be false with explicit '0'")
	}
}

func TestLoadPeerKeysFromEnv_BadHex(t *testing.T) {
	t.Setenv("QSDM_PEER_ATTESTER_KEYS", "attester-x=NOT_HEX")
	t.Setenv("QSDM_PEER_ATTESTER_KEYS_FILE", "")
	if _, _, err := LoadPeerKeysFromEnv(); err == nil {
		t.Fatalf("expected error on non-hex key")
	}
}

func TestLoadPeerKeysFromEnv_MissingEquals(t *testing.T) {
	t.Setenv("QSDM_PEER_ATTESTER_KEYS", "attester-x")
	t.Setenv("QSDM_PEER_ATTESTER_KEYS_FILE", "")
	if _, _, err := LoadPeerKeysFromEnv(); err == nil {
		t.Fatalf("expected error on entry without '='")
	}
}

func TestLoadPeerKeysFromEnv_FromFile(t *testing.T) {
	hexKey, _ := mustHex32(t)
	hexKey2, _ := mustHex32(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.txt")
	body := strings.Join([]string{
		"# comment line",
		"attester-foo=" + hexKey,
		"",
		"attester-bar=" + hexKey2,
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("QSDM_PEER_ATTESTER_KEYS", "")
	t.Setenv("QSDM_PEER_ATTESTER_KEYS_FILE", path)
	t.Setenv("QSDM_PEER_ATTESTER_STRICT", "")
	reg, n, err := LoadPeerKeysFromEnv()
	if err != nil {
		t.Fatalf("LoadPeerKeysFromEnv: %v", err)
	}
	if n != 2 {
		t.Errorf("loaded count = %d want 2", n)
	}
	signers := reg.PinnedSigners()
	if len(signers) != 2 {
		t.Errorf("PinnedSigners = %v", signers)
	}
}

func TestLoadPeerKeysFromEnv_BothEnvAndFile_Combine(t *testing.T) {
	hexA, _ := mustHex32(t)
	hexB, _ := mustHex32(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.txt")
	if err := os.WriteFile(path, []byte("attester-from-file="+hexB), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv("QSDM_PEER_ATTESTER_KEYS", "attester-from-env="+hexA)
	t.Setenv("QSDM_PEER_ATTESTER_KEYS_FILE", path)
	t.Setenv("QSDM_PEER_ATTESTER_STRICT", "")
	reg, n, err := LoadPeerKeysFromEnv()
	if err != nil {
		t.Fatalf("LoadPeerKeysFromEnv: %v", err)
	}
	if n != 2 {
		t.Errorf("loaded count = %d want 2", n)
	}
	if !reg.HasPins() {
		t.Errorf("HasPins=false after loading env+file")
	}
}

// Round-trip integration: a signed profile from the
// attester binary's perspective (Sign with key K) is
// accepted by a validator whose registry pins K. The
// canonical encoder is deterministic so this is the
// definitive contract test for the trust handshake.
func TestVerifyAndAccept_RoundTripWithSignerIDDerivedFromKey(t *testing.T) {
	key := mustKeyN(t, 32)
	signerID := "attester-" + hex.EncodeToString(key[:8])
	r := NewPeerKeyRegistry()
	if err := r.Add(signerID, key); err != nil {
		t.Fatalf("Add: %v", err)
	}
	r.SetStrict(true)
	p := validProfile(t, signerID, key)
	if err := r.VerifyAndAccept(p); err != nil {
		t.Fatalf("expected round-trip accept, got %v", err)
	}
}
