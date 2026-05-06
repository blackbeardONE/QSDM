package cc

// roots.go — operator-facing helpers for loading the genesis-
// pinned NVIDIA CA root set into VerifierConfig.PinnedRoots.
//
// The cryptographic verifier in verifier.go assumes its caller
// has already assembled a []PinnedRoot. In bring-up tests that
// happens via testvectors.go (BuildTestBundle returns a fresh
// in-memory root). In production a validator operator wants
// the simpler ergonomics of "point me at a directory of
// .pem / .der files and give me back a slice." This file
// ships those ergonomics.
//
// Three loaders are exposed:
//
//   LoadPinnedRootsFromFile(path) — single PEM-or-DER file.
//   LoadPinnedRootsFromDir(dir)   — every recognised cert file
//                                    in a directory, lex-sorted.
//   LoadPinnedRootsFromPaths(ps)  — fan-out across files and
//                                    directories, dedup'd by
//                                    DER bytes.
//
// All three are pure I/O helpers: no consensus state, no
// global wiring, no logging. They translate filesystem input
// into []PinnedRoot or fail with a wrapped *os.PathError /
// *x509-parse error so the operator gets a precise diagnostic
// at boot rather than a silent verifier mis-pin.
//
// Auto-detection rule (PEM vs DER): PEM blocks begin with
// "-----BEGIN ", which is bytes [0x2D, 0x2D, 0x2D, ...]. DER
// x509 certs always begin with 0x30 (ASN.1 SEQUENCE tag).
// We dispatch on the first byte. A file that's neither PEM
// nor parseable DER returns ErrPinnedRootDecode wrapped with
// the path so logs show which file is bad.

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrPinnedRootDecode is the sentinel error returned when a
// candidate cert file can be read from disk but is neither
// valid DER nor a PEM document containing at least one
// CERTIFICATE block. Callers can errors.Is against it for
// precise routing in operator-facing error reporters.
var ErrPinnedRootDecode = errors.New("cc: pinned root not decodable as PEM or DER")

// ErrPinnedRootNoCerts is returned when a PEM document parses
// successfully but contains no CERTIFICATE blocks (typical
// case: an operator pointed the loader at a key file by
// mistake). Distinguished from ErrPinnedRootDecode because
// this one says "the file is the wrong kind", not "the file
// is corrupt".
var ErrPinnedRootNoCerts = errors.New("cc: pinned root file contains no CERTIFICATE blocks")

// recognisedCertExtensions is the set of file extensions that
// LoadPinnedRootsFromDir treats as candidate cert files. The
// list mirrors openssl/Go convention: .pem and .crt for PEM-
// encoded, .der and .cer for DER-encoded (although the loader
// auto-detects regardless of suffix, so a misnamed file still
// loads correctly — the suffix only controls which files the
// directory scan picks up).
var recognisedCertExtensions = []string{".pem", ".der", ".crt", ".cer"}

// LoadPinnedRootsFromFile reads PEM- or DER-encoded x509
// certificates from the file at path and returns them as
// PinnedRoot entries.
//
// PEM files may contain multiple certificate blocks; non-
// CERTIFICATE blocks (e.g. PRIVATE KEY, EC PARAMETERS) are
// skipped silently. DER files contain exactly one cert.
//
// Each returned PinnedRoot has Subject set to the cert's
// Subject.CommonName (or the full RFC 2253 string if CN is
// empty) and DER set to the raw DER bytes. The Subject
// field is informational only — consensus is keyed on the
// DER bytes; two roots with identical DER but different
// Subject would still dedup correctly downstream.
//
// Returns:
//   - (entries, nil) on success (entries may be empty if
//     the file contained only non-CERTIFICATE PEM blocks
//     — wait, no: that case returns ErrPinnedRootNoCerts).
//   - wrapped *os.PathError if the file cannot be read.
//   - ErrPinnedRootDecode for an unrecognised first byte.
//   - ErrPinnedRootNoCerts for a PEM doc with no certs.
//   - wrapped x509-parse error for malformed DER bytes.
func LoadPinnedRootsFromFile(path string) ([]PinnedRoot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cc: read pinned root %s: %w", path, err)
	}
	return decodePinnedRoots(path, data)
}

// LoadPinnedRootsFromDir reads every *.pem, *.der, *.crt, and
// *.cer file in dir (non-recursive) and concatenates the
// results in lexicographic filename order so the returned
// slice is deterministic across runs and platforms.
//
// Subdirectories are silently ignored — operators wanting a
// recursive scan should pre-flatten with a cp -r or pass each
// subdir explicitly via LoadPinnedRootsFromPaths.
//
// An empty directory returns ([], nil) — the caller decides
// whether that's a mis-config (CCConfig left nil → fall back
// to stub) or a legitimate test posture.
func LoadPinnedRootsFromDir(dir string) ([]PinnedRoot, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("cc: read pinned-roots dir %s: %w", dir, err)
	}
	// Collect candidate filenames first so we can sort before
	// reading — the sort key is the basename, identical across
	// case-sensitive (Linux) and case-insensitive (macOS / NTFS)
	// filesystems modulo locale, which is fine for a deploy-tree
	// scan whose contents the operator controls.
	candidates := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if !isRecognisedCertExt(ext) {
			continue
		}
		candidates = append(candidates, e.Name())
	}
	sort.Strings(candidates)

	out := make([]PinnedRoot, 0, len(candidates))
	for _, name := range candidates {
		full := filepath.Join(dir, name)
		roots, err := LoadPinnedRootsFromFile(full)
		if err != nil {
			return nil, err
		}
		out = append(out, roots...)
	}
	return out, nil
}

// LoadPinnedRootsFromPaths fans out across paths: each entry
// may be a file or a directory. File entries are passed to
// LoadPinnedRootsFromFile; directory entries to
// LoadPinnedRootsFromDir. Symlinks are followed.
//
// The aggregated result is dedup'd by DER bytes so a root
// that appears in two configured sources counts once. The
// dedup is order-preserving: the FIRST occurrence wins, so
// the Subject label on the surviving entry comes from
// whichever source listed it first.
//
// Returns the aggregate slice and any error from the first
// failing source. We surface the failure rather than skipping
// it because a bad cert file is exactly the kind of
// mis-configuration consensus operators must NOT silently
// drop — a missing trust anchor at boot would otherwise
// reduce the verifier's reach without any visible signal.
func LoadPinnedRootsFromPaths(paths []string) ([]PinnedRoot, error) {
	all := make([]PinnedRoot, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("cc: stat pinned root path %s: %w", p, err)
		}
		var roots []PinnedRoot
		if info.IsDir() {
			roots, err = LoadPinnedRootsFromDir(p)
		} else {
			roots, err = LoadPinnedRootsFromFile(p)
		}
		if err != nil {
			return nil, err
		}
		all = append(all, roots...)
	}
	return dedupPinnedRoots(all), nil
}

// decodePinnedRoots is the format-agnostic core that
// LoadPinnedRootsFromFile delegates to once the bytes are in
// hand. Split out so future loaders (e.g. an in-memory test
// helper that already has the bytes, or a Kubernetes secret
// reader) can reuse the parsing without re-implementing the
// PEM-vs-DER detection.
//
// path is informational only — used to attach context to
// returned errors. Pass an empty string when there is no
// meaningful filesystem path.
func decodePinnedRoots(path string, data []byte) ([]PinnedRoot, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("cc: pinned root %s empty: %w", path, ErrPinnedRootDecode)
	}

	// Heuristic: PEM always opens with the literal "-----BEGIN ".
	// DER x509 certs always open with 0x30 (ASN.1 SEQUENCE).
	// Anything else is malformed and surfaces as
	// ErrPinnedRootDecode.
	switch {
	case bytes.HasPrefix(data, []byte("-----BEGIN ")):
		return decodePinnedRootsPEM(path, data)
	case data[0] == 0x30:
		return decodePinnedRootsDER(path, data)
	default:
		// One last fallback: some CAs distribute their roots in
		// PEM with a leading BOM or a leading whitespace line.
		// Trim leading ASCII whitespace and re-test.
		trimmed := bytes.TrimLeft(data, " \t\r\n")
		if bytes.HasPrefix(trimmed, []byte("-----BEGIN ")) {
			return decodePinnedRootsPEM(path, trimmed)
		}
		return nil, fmt.Errorf("cc: pinned root %s first byte 0x%02x: %w",
			path, data[0], ErrPinnedRootDecode)
	}
}

// decodePinnedRootsPEM walks every PEM block in data, picking
// up the CERTIFICATE blocks and skipping anything else (PRIVATE
// KEY, EC PARAMETERS, etc.). A document with NO certificate
// blocks is treated as "wrong kind of file" via
// ErrPinnedRootNoCerts.
func decodePinnedRootsPEM(path string, data []byte) ([]PinnedRoot, error) {
	var roots []PinnedRoot
	rest := data
	blockIdx := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		blockIdx++
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf(
				"cc: pinned root %s PEM block #%d (%s) parse: %w",
				path, blockIdx, block.Type, err)
		}
		roots = append(roots, PinnedRoot{
			Subject: subjectLabel(cert),
			DER:     append([]byte(nil), block.Bytes...),
		})
	}
	if blockIdx == 0 {
		return nil, fmt.Errorf("cc: pinned root %s: %w", path, ErrPinnedRootDecode)
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("cc: pinned root %s: %w", path, ErrPinnedRootNoCerts)
	}
	return roots, nil
}

// decodePinnedRootsDER parses data as a single DER-encoded
// x509 certificate. The verifier's NewVerifier is the
// authoritative consumer — we still parse here so a malformed
// DER surfaces at load time (with the file path attached) and
// not at first-proof-arrives time (where the path context is
// long gone).
func decodePinnedRootsDER(path string, data []byte) ([]PinnedRoot, error) {
	cert, err := x509.ParseCertificate(data)
	if err != nil {
		return nil, fmt.Errorf("cc: pinned root %s DER parse: %w", path, err)
	}
	return []PinnedRoot{{
		Subject: subjectLabel(cert),
		DER:     append([]byte(nil), data...),
	}}, nil
}

// subjectLabel produces a human-readable label for the pinned
// root's Subject field. Prefers CommonName when set; falls back
// to the full distinguished name. Both branches are
// informational — consensus is keyed on the DER bytes alone.
func subjectLabel(cert *x509.Certificate) string {
	if cn := strings.TrimSpace(cert.Subject.CommonName); cn != "" {
		return cn
	}
	return cert.Subject.String()
}

// isRecognisedCertExt reports whether ext (lower-cased,
// including the leading dot) is one of the known cert-file
// extensions LoadPinnedRootsFromDir scans for.
func isRecognisedCertExt(ext string) bool {
	for _, e := range recognisedCertExtensions {
		if e == ext {
			return true
		}
	}
	return false
}

// dedupPinnedRoots returns a copy of in with duplicate DER
// payloads collapsed to their first occurrence. Order-
// preserving: the first PinnedRoot for a given DER wins, so
// the Subject label on the surviving entry comes from
// whichever caller listed the path first.
//
// Two PinnedRoots with identical DER bytes but different
// Subject strings are considered the same root: the cert is
// the authoritative identity.
func dedupPinnedRoots(in []PinnedRoot) []PinnedRoot {
	if len(in) <= 1 {
		return in
	}
	out := make([]PinnedRoot, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, r := range in {
		// Map key is the DER bytes as a string. Go strings can
		// hold arbitrary bytes; the map's hash treats the bytes
		// as opaque. No allocation beyond the string header.
		key := string(r.DER)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, r)
	}
	return out
}
