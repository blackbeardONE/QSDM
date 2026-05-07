package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

func writePeerFile(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "peer_signers.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write peer file: %v", err)
	}
	return path
}

func randHexKey(t *testing.T) string {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(k)
}

func TestLoadPeerSignersFile_MissingPathReturnsEmpty(t *testing.T) {
	peers, err := LoadPeerSignersFile(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("expected empty peers, got %d", len(peers))
	}
}

func TestLoadPeerSignersFile_EmptyPathReturnsEmpty(t *testing.T) {
	peers, err := LoadPeerSignersFile("")
	if err != nil {
		t.Fatalf("empty path should not error: %v", err)
	}
	if peers != nil {
		t.Fatalf("expected nil peers, got %v", peers)
	}
}

func TestLoadPeerSignersFile_ParsesValidTOML(t *testing.T) {
	keyHex := randHexKey(t)
	body := `
[[peer]]
signer_id = "attester-aaa111bbb222ccc3"
key_hex   = "` + keyHex + `"
note      = "blackbeard's home 3050"

[[peer]]
signer_id = "attester-second"
key_hex   = "` + randHexKey(t) + `"
note      = ""
`
	path := writePeerFile(t, body)
	peers, err := LoadPeerSignersFile(path)
	if err != nil {
		t.Fatalf("LoadPeerSignersFile: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}
	if peers[0].SignerID != "attester-aaa111bbb222ccc3" {
		t.Fatalf("first signer_id = %q", peers[0].SignerID)
	}
	if peers[0].KeyHex != keyHex {
		t.Fatalf("first key_hex mismatch")
	}
	if peers[0].Note != "blackbeard's home 3050" {
		t.Fatalf("first note = %q", peers[0].Note)
	}
}

func TestLoadPeerSignersFile_MalformedTOML(t *testing.T) {
	path := writePeerFile(t, "this is not toml [[")
	_, err := LoadPeerSignersFile(path)
	if err == nil {
		t.Fatalf("expected decode error on malformed TOML")
	}
}

func TestLoadPeerSignersFile_EmptyFileIsOK(t *testing.T) {
	path := writePeerFile(t, "# only a comment\n")
	peers, err := LoadPeerSignersFile(path)
	if err != nil {
		t.Fatalf("comment-only file should not error: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("expected 0 peers, got %d", len(peers))
	}
}

func TestRegisterPeerSigners_HappyPath(t *testing.T) {
	v := challenge.NewHMACSignerVerifier()
	keyHex := randHexKey(t)
	peers := []PeerSigner{{
		SignerID: "attester-test-001",
		KeyHex:   keyHex,
		Note:     "test",
	}}
	reg, errs := RegisterPeerSigners(v, peers)
	if reg != 1 {
		t.Fatalf("expected 1 registered, got %d", reg)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	// Sanity: the verifier now resolves the signer_id.
	keyBytes, _ := hex.DecodeString(keyHex)
	signer, err := challenge.NewHMACSigner("attester-test-001", keyBytes)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	c := challenge.Challenge{
		IssuedAt: time.Now().Unix(),
		SignerID: signer.SignerID(),
	}
	for i := range c.Nonce {
		c.Nonce[i] = byte(i)
	}
	sig, err := signer.Sign(c.SigningBytes())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	c.Signature = sig
	if vErr := v.VerifySignature(c.SignerID, c.SigningBytes(), c.Signature); vErr != nil {
		t.Fatalf("verifier rejected freshly-registered signature: %v", vErr)
	}
}

func TestRegisterPeerSigners_RejectsMalformedKeyHex(t *testing.T) {
	v := challenge.NewHMACSignerVerifier()
	peers := []PeerSigner{{
		SignerID: "attester-bad",
		KeyHex:   "not-hex-AT-ALL",
	}}
	reg, errs := RegisterPeerSigners(v, peers)
	if reg != 0 {
		t.Fatalf("expected 0 registered, got %d", reg)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Err.Error(), "decode key_hex") {
		t.Fatalf("error %q missing decode hint", errs[0].Err)
	}
}

func TestRegisterPeerSigners_RejectsEmptySignerID(t *testing.T) {
	v := challenge.NewHMACSignerVerifier()
	peers := []PeerSigner{{
		SignerID: "",
		KeyHex:   randHexKey(t),
	}}
	reg, errs := RegisterPeerSigners(v, peers)
	if reg != 0 {
		t.Fatalf("expected 0 registered, got %d", reg)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Err.Error(), "empty signer_id") {
		t.Fatalf("error %q missing empty hint", errs[0].Err)
	}
}

func TestRegisterPeerSigners_RejectsEmptyKey(t *testing.T) {
	v := challenge.NewHMACSignerVerifier()
	peers := []PeerSigner{{
		SignerID: "attester-empty-key",
		KeyHex:   "   ",
	}}
	reg, errs := RegisterPeerSigners(v, peers)
	if reg != 0 {
		t.Fatalf("expected 0 registered, got %d", reg)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Err.Error(), "empty key_hex") {
		t.Fatalf("error %q missing empty hint", errs[0].Err)
	}
}

func TestRegisterPeerSigners_RejectsDuplicateSignerID(t *testing.T) {
	v := challenge.NewHMACSignerVerifier()
	keyHex := randHexKey(t)
	peers := []PeerSigner{
		{SignerID: "attester-dup", KeyHex: keyHex},
		{SignerID: "attester-dup", KeyHex: randHexKey(t)},
	}
	reg, errs := RegisterPeerSigners(v, peers)
	if reg != 1 {
		t.Fatalf("expected 1 registered, got %d", reg)
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if !strings.Contains(errs[0].Err.Error(), "already registered") {
		t.Fatalf("error %q missing duplicate hint", errs[0].Err)
	}
}

func TestRegisterPeerSigners_NilVerifier(t *testing.T) {
	reg, errs := RegisterPeerSigners(nil, []PeerSigner{{SignerID: "x", KeyHex: randHexKey(t)}})
	if reg != 0 {
		t.Fatalf("nil verifier should register zero peers")
	}
	if len(errs) != 1 {
		t.Fatalf("expected 1 error for nil verifier, got %d", len(errs))
	}
}

func TestPeerSignerError_UnwrapsInnerError(t *testing.T) {
	inner := errors.New("inner failure")
	pse := PeerSignerError{
		PeerSigner: PeerSigner{SignerID: "s"},
		Err:        inner,
	}
	if !errors.Is(pse, inner) {
		t.Fatalf("PeerSignerError did not unwrap to inner")
	}
	if !strings.Contains(pse.Error(), "s") {
		t.Fatalf("Error() missing signer id: %q", pse.Error())
	}
}
