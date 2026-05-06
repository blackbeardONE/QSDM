package cc

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	mathrand "math/rand"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

// rootsHelper mints a fresh self-signed root cert with the
// supplied CommonName, returning {DER bytes, PEM bytes,
// *x509.Certificate} so the test can write to disk in either
// encoding without re-marshalling. The keypair is discarded —
// these tests only exercise the loader, not the signing path.
type rootsHelper struct {
	der  []byte
	pem  []byte
	cert *x509.Certificate
}

func mintLoaderRoot(t *testing.T, cn string) rootsHelper {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return rootsHelper{der: der, pem: pemBytes, cert: cert}
}

// ---- LoadPinnedRootsFromFile ---------------------------------------------

func TestLoadPinnedRootsFromFile_PEM(t *testing.T) {
	r := mintLoaderRoot(t, "test-root-pem")
	dir := t.TempDir()
	path := filepath.Join(dir, "root.pem")
	if err := os.WriteFile(path, r.pem, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	roots, err := LoadPinnedRootsFromFile(path)
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromFile: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(roots))
	}
	if !bytes.Equal(roots[0].DER, r.der) {
		t.Fatalf("DER round-trip mismatch")
	}
	if roots[0].Subject != "test-root-pem" {
		t.Fatalf("Subject=%q want test-root-pem", roots[0].Subject)
	}
}

func TestLoadPinnedRootsFromFile_DER(t *testing.T) {
	r := mintLoaderRoot(t, "test-root-der")
	dir := t.TempDir()
	path := filepath.Join(dir, "root.der")
	if err := os.WriteFile(path, r.der, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	roots, err := LoadPinnedRootsFromFile(path)
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromFile: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(roots))
	}
	if !bytes.Equal(roots[0].DER, r.der) {
		t.Fatalf("DER round-trip mismatch")
	}
	if roots[0].Subject != "test-root-der" {
		t.Fatalf("Subject=%q want test-root-der", roots[0].Subject)
	}
}

func TestLoadPinnedRootsFromFile_MultiBlockPEM(t *testing.T) {
	r1 := mintLoaderRoot(t, "root-a")
	r2 := mintLoaderRoot(t, "root-b")
	combined := append([]byte(nil), r1.pem...)
	combined = append(combined, r2.pem...)
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.pem")
	if err := os.WriteFile(path, combined, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	roots, err := LoadPinnedRootsFromFile(path)
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromFile: %v", err)
	}
	if len(roots) != 2 {
		t.Fatalf("want 2 roots, got %d", len(roots))
	}
	gotSubjects := []string{roots[0].Subject, roots[1].Subject}
	if gotSubjects[0] != "root-a" || gotSubjects[1] != "root-b" {
		t.Fatalf("subjects in wrong order: %v", gotSubjects)
	}
}

func TestLoadPinnedRootsFromFile_PEMSkipsNonCertBlocks(t *testing.T) {
	r := mintLoaderRoot(t, "test-root-mixed")
	keyBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: []byte("decoy bytes — should be ignored by loader"),
	})
	combined := append([]byte(nil), keyBlock...)
	combined = append(combined, r.pem...)
	dir := t.TempDir()
	path := filepath.Join(dir, "mixed.pem")
	if err := os.WriteFile(path, combined, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	roots, err := LoadPinnedRootsFromFile(path)
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromFile: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(roots))
	}
	if roots[0].Subject != "test-root-mixed" {
		t.Fatalf("subject=%q", roots[0].Subject)
	}
}

func TestLoadPinnedRootsFromFile_PEMOnlyKeyBlocks(t *testing.T) {
	keyBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: []byte("decoy"),
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "keyonly.pem")
	if err := os.WriteFile(path, keyBlock, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := LoadPinnedRootsFromFile(path)
	if !errors.Is(err, ErrPinnedRootNoCerts) {
		t.Fatalf("want ErrPinnedRootNoCerts, got %v", err)
	}
}

func TestLoadPinnedRootsFromFile_PEMLeadingWhitespace(t *testing.T) {
	r := mintLoaderRoot(t, "lead-ws-root")
	withWS := append([]byte("\n\r\n   \t"), r.pem...)
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.pem")
	if err := os.WriteFile(path, withWS, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	roots, err := LoadPinnedRootsFromFile(path)
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromFile: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(roots))
	}
}

func TestLoadPinnedRootsFromFile_MalformedDER(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.der")
	// Leading 0x30 picks the DER branch; trailing garbage breaks parse.
	if err := os.WriteFile(path, []byte{0x30, 0x82, 0x00, 0x01, 0xFF}, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadPinnedRootsFromFile(path)
	if err == nil {
		t.Fatal("expected DER parse error")
	}
	// The error message must include the path so the operator
	// knows which file is broken.
	if !strings.Contains(err.Error(), "bad.der") {
		t.Fatalf("error %q missing file path", err)
	}
}

func TestLoadPinnedRootsFromFile_UnknownFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noise.bin")
	// Neither 0x30 nor "-----BEGIN ".
	if err := os.WriteFile(path, []byte("hello world this is not a cert"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadPinnedRootsFromFile(path)
	if !errors.Is(err, ErrPinnedRootDecode) {
		t.Fatalf("want ErrPinnedRootDecode, got %v", err)
	}
}

func TestLoadPinnedRootsFromFile_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.pem")
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadPinnedRootsFromFile(path)
	if !errors.Is(err, ErrPinnedRootDecode) {
		t.Fatalf("want ErrPinnedRootDecode for empty file, got %v", err)
	}
}

func TestLoadPinnedRootsFromFile_NotFound(t *testing.T) {
	_, err := LoadPinnedRootsFromFile(filepath.Join(t.TempDir(), "missing.pem"))
	if err == nil {
		t.Fatal("expected error on missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
}

// ---- LoadPinnedRootsFromDir ----------------------------------------------

func TestLoadPinnedRootsFromDir_LexicographicOrder(t *testing.T) {
	r1 := mintLoaderRoot(t, "alpha")
	r2 := mintLoaderRoot(t, "bravo")
	r3 := mintLoaderRoot(t, "charlie")
	dir := t.TempDir()
	// Write out of alphabetical order on purpose; the loader
	// sorts so the consensus-side iteration order is stable.
	if err := os.WriteFile(filepath.Join(dir, "c.pem"), r3.pem, 0o600); err != nil {
		t.Fatalf("write c: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.pem"), r1.pem, 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.pem"), r2.pem, 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}

	roots, err := LoadPinnedRootsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromDir: %v", err)
	}
	if len(roots) != 3 {
		t.Fatalf("want 3 roots, got %d", len(roots))
	}
	gotCN := []string{roots[0].Subject, roots[1].Subject, roots[2].Subject}
	wantCN := []string{"alpha", "bravo", "charlie"}
	for i := range gotCN {
		if gotCN[i] != wantCN[i] {
			t.Fatalf("idx=%d got %q want %q", i, gotCN[i], wantCN[i])
		}
	}
}

func TestLoadPinnedRootsFromDir_IgnoresUnrecognisedExtensions(t *testing.T) {
	r := mintLoaderRoot(t, "good-cert")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.pem"), r.pem, 0o600); err != nil {
		t.Fatalf("write good: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("noise"), 0o600); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	roots, err := LoadPinnedRootsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromDir: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 root, got %d", len(roots))
	}
}

func TestLoadPinnedRootsFromDir_IgnoresSubdirectories(t *testing.T) {
	r := mintLoaderRoot(t, "top-level-root")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "top.pem"), r.pem, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	subdir := filepath.Join(dir, "nested")
	if err := os.Mkdir(subdir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Drop a cert in the subdir; it should NOT be picked up.
	r2 := mintLoaderRoot(t, "nested-root")
	if err := os.WriteFile(filepath.Join(subdir, "nested.pem"), r2.pem, 0o600); err != nil {
		t.Fatalf("write nested: %v", err)
	}

	roots, err := LoadPinnedRootsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromDir: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 root (no recursion), got %d", len(roots))
	}
	if roots[0].Subject != "top-level-root" {
		t.Fatalf("subject=%q", roots[0].Subject)
	}
}

func TestLoadPinnedRootsFromDir_EmptyDirEmptySlice(t *testing.T) {
	dir := t.TempDir()
	roots, err := LoadPinnedRootsFromDir(dir)
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromDir on empty dir: %v", err)
	}
	if len(roots) != 0 {
		t.Fatalf("want empty slice, got %d", len(roots))
	}
}

func TestLoadPinnedRootsFromDir_NoSuchDir(t *testing.T) {
	_, err := LoadPinnedRootsFromDir(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected error on missing dir")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
}

func TestLoadPinnedRootsFromDir_PropagatesFileError(t *testing.T) {
	r := mintLoaderRoot(t, "good")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.pem"), r.pem, 0o600); err != nil {
		t.Fatalf("write good: %v", err)
	}
	// A .pem file that's actually noise. The dir scan picks
	// it up by extension and the per-file decode should error.
	if err := os.WriteFile(filepath.Join(dir, "bad.pem"), []byte("not a pem"), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}

	_, err := LoadPinnedRootsFromDir(dir)
	if err == nil {
		t.Fatal("expected error from malformed .pem in dir")
	}
	if !errors.Is(err, ErrPinnedRootDecode) {
		t.Fatalf("want ErrPinnedRootDecode, got %v", err)
	}
}

// ---- LoadPinnedRootsFromPaths --------------------------------------------

func TestLoadPinnedRootsFromPaths_FilesAndDirs(t *testing.T) {
	r1 := mintLoaderRoot(t, "from-file")
	r2 := mintLoaderRoot(t, "from-dir-a")
	r3 := mintLoaderRoot(t, "from-dir-b")

	// Single file path.
	fileDir := t.TempDir()
	filePath := filepath.Join(fileDir, "single.pem")
	if err := os.WriteFile(filePath, r1.pem, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Directory path with two certs.
	dirPath := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirPath, "a.pem"), r2.pem, 0o600); err != nil {
		t.Fatalf("write a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "b.pem"), r3.pem, 0o600); err != nil {
		t.Fatalf("write b: %v", err)
	}

	roots, err := LoadPinnedRootsFromPaths([]string{filePath, dirPath})
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromPaths: %v", err)
	}
	if len(roots) != 3 {
		t.Fatalf("want 3 roots, got %d", len(roots))
	}
}

func TestLoadPinnedRootsFromPaths_DedupOrderPreserving(t *testing.T) {
	r1 := mintLoaderRoot(t, "first-occurrence")
	// Two paths that contain the SAME root cert. Loader should
	// dedup; the first occurrence (with its Subject label)
	// should win.
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir1, "x.pem"), r1.pem, 0o600); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir2, "y.pem"), r1.pem, 0o600); err != nil {
		t.Fatalf("write 2: %v", err)
	}

	roots, err := LoadPinnedRootsFromPaths([]string{dir1, dir2})
	if err != nil {
		t.Fatalf("LoadPinnedRootsFromPaths: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("want 1 deduped root, got %d", len(roots))
	}
	if !bytes.Equal(roots[0].DER, r1.der) {
		t.Fatal("DER mismatch after dedup")
	}
}

func TestLoadPinnedRootsFromPaths_StatErrorPropagates(t *testing.T) {
	_, err := LoadPinnedRootsFromPaths([]string{filepath.Join(t.TempDir(), "nope")})
	if err == nil {
		t.Fatal("expected stat error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want os.ErrNotExist, got %v", err)
	}
}

// ---- LoadVerifierConfig --------------------------------------------------

func TestLoadVerifierConfig_NoRootsReturnsNil(t *testing.T) {
	cfg, err := LoadVerifierConfig(VerifierConfigOptions{})
	if err != nil {
		t.Fatalf("LoadVerifierConfig with empty opts: %v", err)
	}
	if cfg != nil {
		t.Fatalf("want nil cfg (caller falls back to stub), got %+v", cfg)
	}
}

func TestLoadVerifierConfig_RequiresNonceStore(t *testing.T) {
	r := mintLoaderRoot(t, "nonce-required")
	dir := t.TempDir()
	path := filepath.Join(dir, "root.pem")
	if err := os.WriteFile(path, r.pem, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadVerifierConfig(VerifierConfigOptions{
		RootPaths: []string{path},
	})
	if err == nil {
		t.Fatal("expected error when NonceStore is missing")
	}
	if !strings.Contains(err.Error(), "NonceStore is required") {
		t.Fatalf("error %q missing NonceStore mention", err)
	}
}

func TestLoadVerifierConfig_HappyPath(t *testing.T) {
	r := mintLoaderRoot(t, "happy-root")
	dir := t.TempDir()
	path := filepath.Join(dir, "root.pem")
	if err := os.WriteFile(path, r.pem, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	store := hmac.NewInMemoryNonceStore(2 * mining.FreshnessWindow)
	cfg, err := LoadVerifierConfig(VerifierConfigOptions{
		RootPaths:         []string{path},
		MinFirmware:       "535.0.0",
		MinDriver:         "550.0.0",
		FreshnessWindow:   45 * time.Second,
		AllowedFutureSkew: 7 * time.Second,
		NonceStore:        store,
	})
	if err != nil {
		t.Fatalf("LoadVerifierConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("want non-nil cfg")
	}
	if len(cfg.PinnedRoots) != 1 {
		t.Fatalf("want 1 pinned root, got %d", len(cfg.PinnedRoots))
	}
	if cfg.MinFirmware.Firmware != "535.0.0" || cfg.MinFirmware.Driver != "550.0.0" {
		t.Fatalf("MinFirmware mismatch: %+v", cfg.MinFirmware)
	}
	if cfg.FreshnessWindow != 45*time.Second {
		t.Fatalf("FreshnessWindow=%v", cfg.FreshnessWindow)
	}
	if cfg.AllowedFutureSkew != 7*time.Second {
		t.Fatalf("AllowedFutureSkew=%v", cfg.AllowedFutureSkew)
	}
	if cfg.NonceStore == nil {
		t.Fatal("NonceStore not propagated")
	}

	// Round-trip: feed the assembled config to NewVerifier and
	// confirm it constructs cleanly. Boot-time mis-pin (e.g.
	// PinnedRoots empty) would surface here.
	if _, err := NewVerifier(*cfg); err != nil {
		t.Fatalf("NewVerifier from loaded config: %v", err)
	}
}

func TestLoadVerifierConfig_EndToEnd_VerifiesRealBundle(t *testing.T) {
	// Full integration: BuildTestBundle mints a fresh root and
	// signs a bundle against it; we write the root to disk,
	// load it through LoadVerifierConfig, and confirm the
	// verifier accepts the bundle. This is the end-to-end
	// proof that the operator's "drop a .pem file in /etc"
	// experience produces a working CC verifier.
	opts := BuildOpts{
		Reader: mathrand.New(mathrand.NewSource(testSeed)),
		Now:    time.Unix(1_700_000_000, 0),
	}
	b64, root, _, err := BuildTestBundle(opts)
	if err != nil {
		t.Fatalf("BuildTestBundle: %v", err)
	}
	o := normaliseOpts(opts)

	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: root.DER,
	})
	dir := t.TempDir()
	rootPath := filepath.Join(dir, "nvidia-test-root.pem")
	if err := os.WriteFile(rootPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write root: %v", err)
	}

	store := hmac.NewInMemoryNonceStore(2 * mining.FreshnessWindow)
	cfg, err := LoadVerifierConfig(VerifierConfigOptions{
		RootPaths:   []string{rootPath},
		MinFirmware: "535.0.0",
		MinDriver:   "550.0.0",
		NonceStore:  store,
	})
	if err != nil {
		t.Fatalf("LoadVerifierConfig: %v", err)
	}
	v, err := NewVerifier(*cfg)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	p := mining.Proof{
		MinerAddr: o.MinerAddr,
		BatchRoot: o.BatchRoot,
		MixDigest: o.MixDigest,
		Attestation: mining.Attestation{
			Type:         mining.AttestationTypeCC,
			BundleBase64: b64,
			Nonce:        o.Nonce,
			IssuedAt:     o.IssuedAt,
		},
	}
	if err := v.VerifyAttestation(p, o.Now); err != nil {
		t.Fatalf("VerifyAttestation against loaded root: %v", err)
	}
}

// ---- dedupPinnedRoots unit smoke -----------------------------------------

func TestDedupPinnedRoots_PreservesFirstOccurrence(t *testing.T) {
	in := []PinnedRoot{
		{Subject: "first", DER: []byte("aaa")},
		{Subject: "second", DER: []byte("bbb")},
		{Subject: "duplicate-of-first", DER: []byte("aaa")},
	}
	out := dedupPinnedRoots(in)
	if len(out) != 2 {
		t.Fatalf("want 2 entries, got %d", len(out))
	}
	if out[0].Subject != "first" {
		t.Fatalf("first entry subject=%q want first", out[0].Subject)
	}
	if out[1].Subject != "second" {
		t.Fatalf("second entry subject=%q want second", out[1].Subject)
	}
}

func TestDedupPinnedRoots_EmptyAndSingleAreNoOps(t *testing.T) {
	if got := dedupPinnedRoots(nil); got != nil {
		t.Fatalf("nil input dedup -> %v", got)
	}
	in := []PinnedRoot{{Subject: "only", DER: []byte("zzz")}}
	out := dedupPinnedRoots(in)
	if len(out) != 1 || out[0].Subject != "only" {
		t.Fatalf("single-entry dedup mangled: %+v", out)
	}
}
