package legacymining

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	hmacattest "github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// FuzzOperatorSigAuth drives the operator-signature check the version 2 Guard
// runs in Precheck on every POST /api/v1/mining/submit body (OperatorSigAuth,
// installed when the config sets require_operator_sig). The proof is decoded
// the way Precheck decodes it. In "harness signs" mode the harness signs the
// decoded proof with the indexed owner's key, so the fuzzer reaches
// acceptance; otherwise the operator_sig is checked as sent.
func FuzzOperatorSigAuth(f *testing.F) {
	var seed [mldsa87.SeedSize]byte
	copy(seed[:], "legacymining operator sig fuzz")
	public, private := mldsa87.NewKeyFromSeed(&seed)
	pub := public.Bytes()
	sum := sha256.Sum256(pub)
	op := &opSigner{priv: private, pub: pub, owner: hex.EncodeToString(sum[:])}
	keys := NewOperatorKeys()
	if _, err := keys.Add(op.owner, op.pub); err != nil {
		f.Fatal(err)
	}
	auth := OperatorSigAuth(keys)

	other := newOpSigner(f)
	genuine := opProof(f, gtStart, op, op.owner, "fuzz-node-1", nil)
	if p, err := mining.ParseProof(genuine); err != nil || auth(p, "fuzz-node-1", op.owner) != nil {
		f.Fatalf("genuine seed rejected: %v", err)
	}
	for _, s := range []struct {
		raw      []byte
		owner    string
		bundleID bool
		signs    bool
	}{
		{genuine, op.owner, true, false},
		{genuine, op.owner, false, false},
		{genuine, other.owner, true, false},
		{opProof(f, gtStart, nil, op.owner, "fuzz-node-2", nil), op.owner, true, false},
		{opProof(f, gtStart, nil, op.owner, "fuzz-node-2", nil), op.owner, true, true},
		{opProof(f, gtStart, other, op.owner, "fuzz-node-3", nil), op.owner, true, false},
		{opProof(f, gtStart, op, op.owner, "fuzz-node-4", func(p *mining.Proof) { p.Height++ }), op.owner, true, false},
		{[]byte(`{"version":2,"attestation":{"type":"nvidia-hmac-v1","bundle":"e30="}}`), op.owner, true, true},
	} {
		f.Add(s.raw, s.owner, "fuzz-node-1", s.bundleID, s.signs)
	}

	f.Fuzz(func(t *testing.T, raw []byte, owner, nodeID string, useBundleNode, harnessSigns bool) {
		p, err := mining.ParseProof(raw)
		if err != nil {
			return // Precheck refuses it before any owner is consulted
		}
		b, berr := hmacattest.ParseBundle(p.Attestation.BundleBase64)
		if useBundleNode && berr == nil {
			nodeID = b.NodeID // what Precheck passes
		}
		if harnessSigns {
			if berr != nil {
				return
			}
			sig, err := op.sign(*p, b)
			if err != nil {
				return
			}
			b.OperatorSig = sig
			if p.Attestation.BundleBase64, err = b.MarshalBase64(); err != nil {
				return
			}
		}
		before := *p
		err = auth(p, nodeID, owner)
		if !reflect.DeepEqual(before, *p) {
			t.Fatal("OperatorSigAuth modified the proof")
		}
		if err != nil {
			var r *Rejection
			if !errors.As(err, &r) || r.Kind != KindBadOperatorSig {
				t.Fatalf("refusal is not a KindBadOperatorSig rejection: %v", err)
			}
			return
		}
		if owner != op.owner {
			t.Fatalf("accepted owner %q, which has no indexed key", owner)
		}
		b, err = hmacattest.ParseBundle(p.Attestation.BundleBase64)
		if err != nil || b.NodeID != nodeID {
			t.Fatalf("accepted a bundle for node %q that does not name node %q (%v)", b.NodeID, nodeID, err)
		}
		msg, merr := b.CanonicalForOperatorSignature(*p)
		sig, serr := hex.DecodeString(b.OperatorSig)
		if merr != nil || serr != nil || !mldsa87.Verify(public, msg, nil, sig) {
			t.Fatal("accepted an operator_sig the reference ML-DSA-87 verifier rejects")
		}
		// The signature binds the proof identity: the same bundle on another
		// proof must be refused.
		for _, move := range []func(*mining.Proof){
			func(q *mining.Proof) { q.Height++ },
			func(q *mining.Proof) { q.MinerAddr += "x" },
			func(q *mining.Proof) { q.Nonce[0] ^= 1 },
			func(q *mining.Proof) { q.Attestation.Nonce[31] ^= 1 },
		} {
			moved := *p
			move(&moved)
			if auth(&moved, nodeID, owner) == nil {
				t.Fatalf("an operator_sig verified on a different proof: %+v", moved)
			}
		}
	})
}
