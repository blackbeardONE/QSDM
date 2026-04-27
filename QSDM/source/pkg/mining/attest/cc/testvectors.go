package cc

// testvectors.go — deterministic in-process generator for
// nvidia-cc-v1 test bundles. Keeps reproducible happy-path
// and golden negative-case coverage with NO external
// testdata files: every vector is rebuilt from a seeded PRNG
// so a CI machine without GPU hardware can still exercise
// the full verifier flow.
//
// This file is BUILT INTO the production package (not
// behind a `_test.go` suffix) for two reasons:
//
//   1. Other packages (cmd/qsdm wiring smoke-tests, the
//      attest production_test, future replay tools) need to
//      construct valid CC bundles to exercise integration
//      paths. Hiding the helpers behind `_test.go` would
//      force every consumer to copy them.
//
//   2. The functions in this file are clearly marked as
//      test/dev tooling — they take a `*BuildOpts` rather
//      than reading any consensus-critical state, and they
//      DO NOT touch the global verifier registration. They
//      cannot be misused to produce a bundle that would
//      smuggle past a properly-configured production
//      Verifier (the leaf cert chain still has to terminate
//      in a pinned root, which production doesn't ship).
//
// If the linter ever flags these as "test code in production
// file", they CAN be moved to a separate _testhelpers package
// — but they MUST stay reachable from non-test code.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"time"
)

// TestKeyPair bundles a leaf cert + signing key suitable for
// producing AIK-style quotes against a TestRoot.
type TestKeyPair struct {
	Cert *x509.Certificate
	DER  []byte
	Key  *ecdsa.PrivateKey
}

// TestRoot bundles a pinned root cert + key (kept around so
// callers can mint additional intermediates / leaves).
type TestRoot struct {
	Cert *x509.Certificate
	DER  []byte
	Key  *ecdsa.PrivateKey
}

// BuildOpts controls test-vector generation. Zero values are
// substituted with sensible defaults so a single
// `BuildTestBundle(BuildOpts{})` produces a happy-path
// vector.
type BuildOpts struct {
	// Reader supplies randomness for cert-chain key gen and
	// quote signing. Pass a deterministic Reader to get
	// reproducible vectors. Defaults to crypto/rand.Reader.
	Reader io.Reader

	// Now is the wall-clock the cert NotBefore is anchored to.
	// Quote IssuedAt also defaults to this. Defaults to
	// time.Unix(1_700_000_000, 0).
	Now time.Time

	// CertValidity is the life of the leaf cert. Defaults to
	// 24 hours.
	CertValidity time.Duration

	// DeviceUUID is the GPU UUID embedded in the bundle. The
	// preimage's leading 16 bytes are the hex-decoded form of
	// this string. Defaults to a deterministic UUID.
	DeviceUUID string

	// Nonce is the 32-byte challenge nonce. Defaults to a
	// deterministic value when zero-valued.
	Nonce [32]byte

	// IssuedAt overrides Now for the bundle's issued_at.
	IssuedAt int64

	// MinerAddr / BatchRoot / MixDigest mirror the enclosing
	// Proof. Test code MUST pass the SAME values into
	// mining.Proof or the bundle will fail step 4 (preimage
	// reconstruction).
	MinerAddr string
	BatchRoot [32]byte
	MixDigest [32]byte

	// ChallengeSignerID / ChallengeSig are echoed verbatim
	// into the bundle preimage; they're NOT verified
	// internally by BuildTestBundle (the challenge layer is
	// orthogonal to the AIK signature).
	ChallengeSignerID string
	ChallengeSig      []byte

	// FirmwareVer / DriverVer feed PCRMeasurementsV1.
	FirmwareVer string
	DriverVer   string

	// PreSignedRoot lets a test reuse an existing root from
	// a previous BuildTestBundle call. Useful when constructing
	// a "wrong root" negative vector (build two bundles, swap
	// the chains).
	PreSignedRoot *TestRoot
}

// BuildTestBundle constructs a fully-signed, validator-
// acceptable Bundle with a freshly-minted self-signed root +
// leaf, plus the pinned root the verifier should be configured
// with. The returned (b64, root) tuple lets the caller paste
// b64 into Proof.Attestation.BundleBase64 and root into
// VerifierConfig.PinnedRoots.
//
// Determinism: when opts.Reader is a deterministic Reader and
// opts.Now is fixed, the output bytes are byte-identical
// across runs and platforms. CI uses this for golden vectors
// without committing testdata files.
func BuildTestBundle(opts BuildOpts) (string, *TestRoot, *TestKeyPair, error) {
	o := normaliseOpts(opts)
	root := o.PreSignedRoot
	if root == nil {
		r, err := mintTestRoot(o)
		if err != nil {
			return "", nil, nil, err
		}
		root = r
	}
	leaf, err := mintTestLeaf(o, root)
	if err != nil {
		return "", nil, nil, err
	}
	bundle, err := buildSignedBundle(o, root, leaf)
	if err != nil {
		return "", nil, nil, err
	}
	b64, err := EncodeBundle(bundle)
	if err != nil {
		return "", nil, nil, err
	}
	return b64, root, leaf, nil
}

// BuildSignedBundleFromPair lets tests construct a bundle
// using a pre-existing leaf key/cert (e.g. for reuse vectors).
// The leaf MUST have been issued under root.
func BuildSignedBundleFromPair(opts BuildOpts, root *TestRoot, leaf *TestKeyPair) (string, error) {
	o := normaliseOpts(opts)
	bundle, err := buildSignedBundle(o, root, leaf)
	if err != nil {
		return "", err
	}
	return EncodeBundle(bundle)
}

func normaliseOpts(o BuildOpts) BuildOpts {
	if o.Reader == nil {
		o.Reader = rand.Reader
	}
	if o.Now.IsZero() {
		o.Now = time.Unix(1_700_000_000, 0)
	}
	if o.CertValidity == 0 {
		o.CertValidity = 24 * time.Hour
	}
	if o.DeviceUUID == "" {
		o.DeviceUUID = "0123456789abcdef0123456789abcdef"
	}
	if o.IssuedAt == 0 {
		o.IssuedAt = o.Now.Unix()
	}
	if o.MinerAddr == "" {
		o.MinerAddr = "qsdm1testminer000000000000000000000000000000"
	}
	if isAllZero(o.Nonce[:]) {
		copy(o.Nonce[:], []byte("nonce-test-vector-32-bytes-fixed"))
	}
	if isAllZero(o.BatchRoot[:]) {
		copy(o.BatchRoot[:], []byte("batch-root-test-vector-32-bytes-fixed"))
	}
	if isAllZero(o.MixDigest[:]) {
		copy(o.MixDigest[:], []byte("mix-digest-test-vector-32-bytes-fixed"))
	}
	if o.ChallengeSignerID == "" {
		o.ChallengeSignerID = "validator-test-1"
	}
	if len(o.ChallengeSig) == 0 {
		o.ChallengeSig = []byte("challenge-sig-test-vector-bytes")
	}
	if o.FirmwareVer == "" {
		o.FirmwareVer = "535.86.10"
	}
	if o.DriverVer == "" {
		o.DriverVer = "550.54.14"
	}
	return o
}

func isAllZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

func mintTestRoot(o BuildOpts) (*TestRoot, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), o.Reader)
	if err != nil {
		return nil, fmt.Errorf("cc: testroot key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "qsdm-test-nvidia-root"},
		NotBefore:    o.Now.Add(-time.Hour),
		NotAfter:     o.Now.Add(o.CertValidity * 2),
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		IsCA:         true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(o.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("cc: testroot cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("cc: testroot parse: %w", err)
	}
	return &TestRoot{Cert: cert, DER: der, Key: key}, nil
}

func mintTestLeaf(o BuildOpts, root *TestRoot) (*TestKeyPair, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), o.Reader)
	if err != nil {
		return nil, fmt.Errorf("cc: testleaf key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "qsdm-test-nvidia-aik"},
		NotBefore:    o.Now.Add(-time.Minute),
		NotAfter:     o.Now.Add(o.CertValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(o.Reader, tmpl, root.Cert, &leafKey.PublicKey, root.Key)
	if err != nil {
		return nil, fmt.Errorf("cc: testleaf cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("cc: testleaf parse: %w", err)
	}
	return &TestKeyPair{Cert: cert, DER: der, Key: leafKey}, nil
}

func buildSignedBundle(o BuildOpts, root *TestRoot, leaf *TestKeyPair) (Bundle, error) {
	preimage, err := canonicalPreimage(PreimageInputs{
		DeviceUUID:        o.DeviceUUID,
		ChallengeNonce:    o.Nonce,
		IssuedAt:          o.IssuedAt,
		MinerAddr:         o.MinerAddr,
		BatchRoot:         o.BatchRoot,
		MixDigest:         o.MixDigest,
		ChallengeSignerID: o.ChallengeSignerID,
		ChallengeSig:      o.ChallengeSig,
	})
	if err != nil {
		return Bundle{}, fmt.Errorf("cc: build preimage: %w", err)
	}
	digest := sha256.Sum256(preimage)
	sig, err := ecdsa.SignASN1(o.Reader, leaf.Key, digest[:])
	if err != nil {
		return Bundle{}, fmt.Errorf("cc: sign quote: %w", err)
	}
	return Bundle{
		DeviceUUID: o.DeviceUUID,
		CertChain: []string{
			base64.StdEncoding.EncodeToString(leaf.DER),
			base64.StdEncoding.EncodeToString(root.DER),
		},
		Quote: QuoteV1{
			ChallengeNonce:    hex.EncodeToString(o.Nonce[:]),
			IssuedAt:          o.IssuedAt,
			ChallengeSignerID: o.ChallengeSignerID,
			ChallengeSig:      hex.EncodeToString(o.ChallengeSig),
			Signature:         base64.StdEncoding.EncodeToString(sig),
		},
		PCR: PCRMeasurementsV1{
			FirmwareVer: o.FirmwareVer,
			DriverVer:   o.DriverVer,
		},
	}, nil
}
