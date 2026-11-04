package hmac

import (
	"bytes"
	stdhmac "crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/archcheck"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// FuzzVerifyAttestation drives the nvidia-hmac-v1 verifier that every
// post-fork proof submitted to POST /api/v1/mining/submit reaches through the
// attestation dispatcher. The bundle bytes, the proof fields the bundle binds
// to and the verifier options come from the input. In "harness signs" mode
// the harness decodes the bundle, binds it to the proof and signs it with the
// enrolled node's HMAC key, the validator's challenge key and the operator's
// ML-DSA-87 key, so the fuzzer reaches acceptance; otherwise the bundle is
// verified as sent.

const (
	fuzzOperatorNode = "fuzz-operator-node"
	fuzzPlainNode    = "fuzz-plain-node"
	fuzzGPUUUID      = "GPU-0f0e0d0c-0b0a-0908-0706-050403020100"
	fuzzSignerID     = "validator-fuzz"
	fuzzNowUnix      = 1_785_585_600
)

var (
	fuzzNodeKeys = map[string][]byte{
		fuzzOperatorNode: []byte("fuzz-node-key-test-only-32-bytes"),
		fuzzPlainNode:    []byte("fuzz-plain-key-test-only-32-byte"),
	}
	fuzzChallengeKey = []byte("fuzz-challenge-key-test-only")
	fuzzBatchRoot    = [32]byte{0x0b, 0xa7}
	fuzzMixDigest    = [32]byte{0x31, 0x3c}
)

// Option bits of the flags input.
const (
	fuzzRequireOwner = 1 << iota
	fuzzRequireOperatorSig
	fuzzWithChallenge
	fuzzRawBase64 // the bundle input is the base64 text itself
)

type fuzzOperator struct {
	private *mldsa87.PrivateKey
	pub     []byte
	owner   string
}

func newFuzzOperator() fuzzOperator {
	var seed [mldsa87.SeedSize]byte
	copy(seed[:], "hmac attestation fuzz operator")
	public, private := mldsa87.NewKeyFromSeed(&seed)
	pub := public.Bytes()
	sum := sha256.Sum256(pub)
	return fuzzOperator{private: private, pub: pub, owner: hex.EncodeToString(sum[:])}
}

// fuzzProof is the proof an attestation input is checked against.
func fuzzProof(minerAddr, gpuArch string, attNonce []byte, issuedAt int64, bundle string) mining.Proof {
	p := mining.Proof{
		Version: mining.ProtocolVersionV2, Height: 100, HeaderHash: [32]byte{0xaa},
		MinerAddr: minerAddr, BatchRoot: fuzzBatchRoot, BatchCount: 1,
		Nonce: [16]byte{3}, MixDigest: fuzzMixDigest,
		Attestation: mining.Attestation{
			Type: mining.AttestationTypeHMAC, BundleBase64: bundle, GPUArch: gpuArch, IssuedAt: issuedAt,
		},
	}
	copy(p.Attestation.Nonce[:], attNonce)
	return p
}

// fuzzMACInput is the MAC-covered bundle content serialised independently of
// CanonicalForMAC: encoding/json emits map keys in sorted order, which is the
// canonical form's alphabetical field order.
func fuzzMACInput(b Bundle) []byte {
	out, err := json.Marshal(map[string]any{
		"challenge_bind": b.ChallengeBind, "challenge_sig": b.ChallengeSig,
		"challenge_signer_id": b.ChallengeSignerID, "compute_cap": b.ComputeCap,
		"cuda_version": b.CUDAVersion, "driver_ver": b.DriverVer, "gpu_name": b.GPUName,
		"gpu_uuid": b.GPUUUID, "issued_at": b.IssuedAt, "node_id": b.NodeID, "nonce": b.Nonce,
	})
	if err != nil {
		panic(err)
	}
	return out
}

func fuzzMAC(key, msg []byte) []byte {
	m := stdhmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

// signForFuzz binds b to p and signs it the way an honest miner does.
func signForFuzz(b Bundle, p mining.Proof, op fuzzOperator) Bundle {
	b.ChallengeBind = HexChallengeBind(p.MinerAddr, p.BatchRoot, p.MixDigest)
	b.Nonce = hex.EncodeToString(p.Attestation.Nonce[:])
	b.IssuedAt = p.Attestation.IssuedAt
	if b.ChallengeSignerID == fuzzSignerID {
		c := challenge.Challenge{Nonce: p.Attestation.Nonce, IssuedAt: b.IssuedAt, SignerID: b.ChallengeSignerID}
		b.ChallengeSig = hex.EncodeToString(fuzzMAC(fuzzChallengeKey, c.SigningBytes()))
	}
	if b.NodeID == fuzzOperatorNode {
		if msg, err := b.CanonicalForOperatorSignature(p); err == nil {
			sig := make([]byte, mldsa87.SignatureSize)
			if mldsa87.SignTo(op.private, msg, nil, false, sig) == nil {
				b.OperatorSig = hex.EncodeToString(sig)
			}
		}
	}
	if key, ok := fuzzNodeKeys[b.NodeID]; ok {
		b.HMAC = hex.EncodeToString(fuzzMAC(key, fuzzMACInput(b)))
	}
	return b
}

func FuzzVerifyAttestation(f *testing.F) {
	op := newFuzzOperator()
	now := time.Unix(fuzzNowUnix, 0)
	newVerifier := func(flags uint8) *Verifier {
		reg := NewInMemoryRegistry()
		if err := reg.EnrollWithOperatorPublicKey(fuzzOperatorNode, op.owner, hex.EncodeToString(op.pub),
			fuzzGPUUUID, fuzzNodeKeys[fuzzOperatorNode]); err != nil {
			f.Fatal(err)
		}
		if err := reg.Enroll(fuzzPlainNode, fuzzGPUUUID, fuzzNodeKeys[fuzzPlainNode]); err != nil {
			f.Fatal(err)
		}
		v := NewVerifier(reg)
		v.NonceStore = NewInMemoryNonceStore(2 * mining.FreshnessWindow)
		v.DenyList = SubstringDenyList{Substrings: []string{"Denied"}}
		v.RequireOwnerBinding = flags&fuzzRequireOwner != 0
		v.RequireOperatorSignature = flags&fuzzRequireOperatorSig != 0
		if flags&fuzzWithChallenge != 0 {
			sv := challenge.NewHMACSignerVerifier()
			if err := sv.Register(fuzzSignerID, fuzzChallengeKey); err != nil {
				f.Fatal(err)
			}
			v.ChallengeVerifier = sv
		}
		return v
	}

	attNonce := bytes.Repeat([]byte{0x5a}, 32)
	unsigned := Bundle{
		ChallengeSignerID: fuzzSignerID, ComputeCap: "8.9", CUDAVersion: "12.8", DriverVer: "572.16",
		GPUName: "NVIDIA GeForce RTX 4090", GPUUUID: fuzzGPUUUID, NodeID: fuzzOperatorNode,
	}
	genuine := signForFuzz(unsigned, fuzzProof(op.owner, "ada", attNonce, fuzzNowUnix-5, ""), op)
	genuineJSON, err := json.Marshal(genuine)
	if err != nil {
		f.Fatal(err)
	}
	all := uint8(fuzzRequireOwner | fuzzRequireOperatorSig | fuzzWithChallenge)
	p := fuzzProof(op.owner, "ada", attNonce, fuzzNowUnix-5, base64.StdEncoding.EncodeToString(genuineJSON))
	if err := newVerifier(all).VerifyAttestation(p, now); err != nil {
		f.Fatalf("genuine seed rejected: %v", err)
	}
	plain := unsigned
	plain.NodeID, plain.ChallengeSignerID, plain.GPUName = fuzzPlainNode, "", "NVIDIA RTX 6000 Ada Generation"
	plainJSON, _ := json.Marshal(plain)
	for _, seed := range []struct {
		bundle   []byte
		miner    string
		arch     string
		issuedAt int64
		flags    uint8
		signs    bool
	}{
		{genuineJSON, op.owner, "ada", fuzzNowUnix - 5, all, false},
		{genuineJSON, op.owner, "Ada Lovelace", fuzzNowUnix - 5, all, false},
		{genuineJSON, "qsdm1someoneelse", "ada", fuzzNowUnix - 5, all, true},
		{genuineJSON, op.owner, "hopper", fuzzNowUnix - 5, all, true},
		{genuineJSON, op.owner, "ada", fuzzNowUnix - 61, 0, true},
		{genuineJSON, op.owner, "ada", fuzzNowUnix + 6, 0, true},
		{bytes.Replace(genuineJSON, []byte(`"nonce":"`), []byte(`"nonce":"00`), 1), op.owner, "ada", fuzzNowUnix, 0, false},
		{plainJSON, "qsdm1plain", "ada", fuzzNowUnix, 0, true},
		{plainJSON, "qsdm1plain", "ada", fuzzNowUnix, fuzzRequireOwner, true},
		{bytes.Replace(plainJSON, []byte("RTX 6000"), []byte("Denied RTX"), 1), "qsdm1plain", "ada", fuzzNowUnix, 0, true},
		{append(bytes.Clone(genuineJSON), ' ', '{', '}'), op.owner, "ada", fuzzNowUnix - 5, all, true},
		{[]byte(`{"node_id":"fuzz-plain-node","extra":1}`), "m", "ada", fuzzNowUnix, 0, true},
		{[]byte(base64.StdEncoding.EncodeToString(genuineJSON)), op.owner, "ada", fuzzNowUnix - 5, all | fuzzRawBase64, false},
		{[]byte("%%%not-base64"), "m", "ada", 0, fuzzRawBase64, false},
	} {
		f.Add(seed.bundle, seed.miner, seed.arch, attNonce, seed.issuedAt, seed.flags, seed.signs)
	}

	f.Fuzz(func(t *testing.T, bundleIn []byte, minerAddr, gpuArch string, attNonce []byte, issuedAt int64,
		flags uint8, harnessSigns bool) {
		p := fuzzProof(minerAddr, gpuArch, attNonce, issuedAt, "")
		if flags&fuzzRawBase64 != 0 {
			p.Attestation.BundleBase64 = string(bundleIn)
		} else {
			p.Attestation.BundleBase64 = base64.StdEncoding.EncodeToString(bundleIn)
		}
		if harnessSigns {
			var b Bundle
			if json.Unmarshal(bundleIn, &b) != nil {
				return
			}
			raw, err := json.Marshal(signForFuzz(b, p, op))
			if err != nil {
				return
			}
			p.Attestation.BundleBase64 = base64.StdEncoding.EncodeToString(raw)
		}
		v := newVerifier(flags)
		err := v.VerifyAttestation(p, now)
		if err != nil {
			for _, sentinel := range []error{mining.ErrAttestationBundleMalformed, mining.ErrAttestationSignatureInvalid,
				mining.ErrAttestationNonceMismatch, mining.ErrAttestationStale} {
				if errors.Is(err, sentinel) {
					return
				}
			}
			t.Fatalf("rejection wraps no attestation sentinel: %v", err)
		}

		raw, derr := base64.StdEncoding.DecodeString(p.Attestation.BundleBase64)
		var b Bundle
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if derr != nil || dec.Decode(&b) != nil || dec.More() {
			t.Fatalf("accepted a bundle that is not one strict JSON object: %q", raw)
		}
		bind := sha256.Sum256(append([]byte(p.MinerAddr), append(p.BatchRoot[:], p.MixDigest[:]...)...))
		if !strings.EqualFold(b.ChallengeBind, hex.EncodeToString(bind[:])) {
			t.Fatal("accepted a bundle bound to another proof")
		}
		key, enrolled := fuzzNodeKeys[b.NodeID]
		mac, herr := hex.DecodeString(b.HMAC)
		if !enrolled || herr != nil || !stdhmac.Equal(mac, fuzzMAC(key, fuzzMACInput(b))) {
			t.Fatalf("accepted a bundle without the enrolled node's MAC: node %q", b.NodeID)
		}
		if !strings.EqualFold(b.GPUUUID, fuzzGPUUUID) {
			t.Fatalf("accepted gpu_uuid %q", b.GPUUUID)
		}
		nonce, nerr := hex.DecodeString(b.Nonce)
		if nerr != nil || !bytes.Equal(nonce, p.Attestation.Nonce[:]) || b.IssuedAt != p.Attestation.IssuedAt {
			t.Fatal("accepted a bundle whose nonce or issue time differs from the proof's")
		}
		if age := now.Sub(time.Unix(b.IssuedAt, 0)); age > mining.FreshnessWindow || age < -5*time.Second {
			t.Fatalf("accepted an attestation issued %v before now", age)
		}
		if v.DenyList.Denied(b.GPUName) {
			t.Fatalf("accepted deny-listed gpu_name %q", b.GPUName)
		}
		arch, ok := archcheck.Canonicalise(p.Attestation.GPUArch)
		if !ok || archcheck.ValidateBundleArchConsistencyHMAC(arch, b.GPUName) != nil {
			t.Fatalf("accepted gpu_arch %q for gpu_name %q", p.Attestation.GPUArch, b.GPUName)
		}
		if v.RequireOwnerBinding && (b.NodeID != fuzzOperatorNode || p.MinerAddr != op.owner) {
			t.Fatalf("accepted miner %q for node %q despite owner binding", p.MinerAddr, b.NodeID)
		}
		if v.ChallengeVerifier != nil {
			sig, serr := hex.DecodeString(b.ChallengeSig)
			c := challenge.Challenge{Nonce: p.Attestation.Nonce, IssuedAt: b.IssuedAt, SignerID: b.ChallengeSignerID}
			if serr != nil || b.ChallengeSignerID != fuzzSignerID || !stdhmac.Equal(sig, fuzzMAC(fuzzChallengeKey, c.SigningBytes())) {
				t.Fatal("accepted a challenge the validator did not sign")
			}
		}
		if v.RequireOperatorSignature {
			sig, serr := hex.DecodeString(b.OperatorSig)
			msg, merr := b.CanonicalForOperatorSignature(p)
			var pk mldsa87.PublicKey
			if b.NodeID != fuzzOperatorNode || serr != nil || merr != nil || pk.UnmarshalBinary(op.pub) != nil ||
				!mldsa87.Verify(&pk, msg, nil, sig) {
				t.Fatal("accepted an operator signature the reference ML-DSA-87 verifier rejects")
			}
		}
		if err := v.VerifyAttestation(p, now); !errors.Is(err, mining.ErrAttestationNonceMismatch) {
			t.Fatalf("a replayed attestation was not refused as a nonce reuse: %v", err)
		}
	})
}
