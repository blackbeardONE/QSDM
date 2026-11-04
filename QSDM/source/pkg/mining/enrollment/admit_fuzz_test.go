package enrollment

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/bits"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// FuzzEnrollmentAdmission decodes an enrollment submission the way
// POST /api/v1/mining/enroll and /unenroll do (one strict SignedEnvelope JSON
// object; the qsdm-transactions gossip ingress decodes the same envelope),
// verifies its signature, converts it to the mempool transaction and runs the
// mempool admission gate on it. In "harness signs" mode the harness signs the
// decoded envelope with a fixed key, so the fuzzer reaches the payload
// validators; otherwise the signature is checked as sent.

type fuzzSigner struct {
	private *mldsa87.PrivateKey
	public  *mldsa87.PublicKey
	pubHex  string
	address string
}

func newFuzzSigner() fuzzSigner {
	var seed [mldsa87.SeedSize]byte
	copy(seed[:], "enrollment admission fuzz key")
	public, private := mldsa87.NewKeyFromSeed(&seed)
	pub := public.Bytes()
	sum := sha256.Sum256(pub)
	return fuzzSigner{private: private, public: public, pubHex: hex.EncodeToString(pub), address: hex.EncodeToString(sum[:])}
}

func (s fuzzSigner) sign(env SignedEnvelope) SignedEnvelope {
	env.Sender, env.PublicKey = s.address, s.pubHex
	canonical, err := env.CanonicalBytes()
	if err != nil {
		panic(err)
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(s.private, canonical, nil, false, sig); err != nil {
		panic(err)
	}
	env.Signature = hex.EncodeToString(sig)
	return env
}

// fuzzNodeID and fuzzGPUUUID are the documented field rules (validate.go)
// written out independently.
func fuzzNodeID(s string) bool {
	if s == "" || len(s) > MaxNodeIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func fuzzGPUUUID(s string) bool {
	if s == "" || len(s) > MaxGPUUUIDLen || strings.HasPrefix(s, "gpu-") {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func FuzzEnrollmentAdmission(f *testing.F) {
	signer := newFuzzSigner()
	admit := AdmissionChecker(nil)
	encode := func(v any) []byte {
		var raw []byte
		var err error
		switch p := v.(type) {
		case EnrollPayload:
			raw, err = EncodeEnrollPayload(p)
		case UnenrollPayload:
			raw, err = EncodeUnenrollPayload(p)
		}
		if err != nil {
			f.Fatal(err)
		}
		return raw
	}
	upfront := encode(EnrollPayload{NodeID: "rig-77", GPUUUID: "GPU-12345678-1234-1234-1234-123456789abc",
		HMACKey: bytes.Repeat([]byte{0x42}, MinHMACKeyLen), StakeDust: mining.MinEnrollStakeDust, Memo: "rack 4"})
	deferred := EnrollPayload{Kind: PayloadKindEnroll, NodeID: "deferred-rig-01", GPUUUID: "GPU-deferred-0001",
		HMACKey: bytes.Repeat([]byte{0x24}, MaxHMACKeyLen), BondMode: BondModeMiningRewards}
	nonce, _, err := FindDeferredBondWork(deferred)
	if err != nil {
		f.Fatal(err)
	}
	deferred.WorkNonce = nonce
	unenroll := encode(UnenrollPayload{NodeID: "rig-77", Reason: "retiring"})
	envelope := func(payload []byte, fee float64) []byte {
		env := signer.sign(SignedEnvelope{ID: "enroll-fuzz-1", Nonce: 3, Fee: fee, ContractID: SignedContractID,
			PayloadB64: base64.StdEncoding.EncodeToString(payload)})
		tx, err := env.ToTransaction()
		if err != nil || VerifySignedEnvelope(env) != nil || admit(tx) != nil {
			f.Fatalf("genuine seed rejected: %v", err)
		}
		raw, _ := json.Marshal(env)
		return raw
	}
	for _, s := range []struct {
		raw     []byte
		payload []byte
		encode  bool
		signs   bool
	}{
		{envelope(upfront, 0.01), nil, false, false},
		{envelope(encode(deferred), 0), nil, false, false},
		{envelope(unenroll, 0), nil, false, false},
		{envelope(upfront, 0.01), bytes.Replace(upfront, []byte(`"rig-77"`), []byte(`"RIG-77"`), 1), true, true},
		{envelope(upfront, 0.01), bytes.Replace(upfront, []byte(`"memo":`), []byte(`"bond_mode":"upfront","memo":`), 1), true, true},
		{envelope(upfront, 0.01), append(bytes.Clone(upfront), ' ', '{', '}'), true, true},
		{envelope(upfront, 0.01), []byte(`{"kind":"unenroll","node_id":"rig-77","extra":1}`), true, true},
		{envelope(upfront, 0.01), []byte(`{"kind":"enroll","node_id":"x","gpu_uuid":"gpu-1","hmac_key":"","stake_dust":0}`), true, true},
		{envelope(unenroll, 0), []byte(`{"kind":"slash"}`), true, true},
		{[]byte(`{"id":"x","sender":"s","contract_id":"qsdm/enroll/v1","payload_b64":"e30="}`), nil, false, true},
		{[]byte(`{"id":"x","contract_id":"qsdm/enroll/v2","payload_b64":"QR==","nonce":1}`), nil, false, true},
		{[]byte(`{"id":"x","contract_id":"qsdm/enroll/v2","payload_b64":"e30=","fee":-1}`), nil, false, true},
		{bytes.Replace(envelope(upfront, 0.01), []byte(SignedContractID), []byte("qsdm/stream/v1"), 1), nil, false, true},
	} {
		f.Add(s.raw, s.payload, s.encode, s.signs)
	}

	errDelegated := errors.New("delegated")
	f.Fuzz(func(t *testing.T, raw, payload []byte, encodePayload, harnessSigns bool) {
		var env SignedEnvelope
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if dec.Decode(&env) != nil {
			return
		}
		if encodePayload {
			env.PayloadB64 = base64.StdEncoding.EncodeToString(payload)
		}
		if harnessSigns {
			env = signer.sign(env)
		}
		verifyErr := VerifySignedEnvelope(env)
		if verifyErr == nil {
			checkSignedEnvelope(t, env)
		}
		tx, err := env.ToTransaction()
		if err != nil {
			return // the handler and the gossip ingress refuse it here
		}
		before := *tx
		before.Payload = bytes.Clone(tx.Payload)
		delegated := 0
		admitErr := AdmissionChecker(func(*mempool.Tx) error { delegated++; return errDelegated })(tx)
		if tx.ID != before.ID || tx.Sender != before.Sender || tx.Fee != before.Fee || tx.Signature != before.Signature ||
			!bytes.Equal(tx.Payload, before.Payload) {
			t.Fatal("the admission gate modified the transaction")
		}
		// The gate owns only the enrollment contracts and hands every other
		// transaction to the next gate in the chain.
		switch {
		case !IsContractID(tx.ContractID):
			if delegated != 1 || !errors.Is(admitErr, errDelegated) {
				t.Fatalf("a %q transaction was not handed to the next gate (%d calls, %v)", tx.ContractID, delegated, admitErr)
			}
			return
		case delegated != 0:
			t.Fatal("an enrollment transaction was handed to the next gate")
		case tx.ContractID == ContractID:
			if !errors.Is(admitErr, ErrLegacyContractDisabled) {
				t.Fatalf("an unsigned v1 enrollment was not refused as disabled: %v", admitErr)
			}
			return
		}
		if admitErr != nil {
			return
		}
		back, err := EnvelopeFromTransaction(tx)
		if err != nil || VerifySignedEnvelope(back) != nil {
			t.Fatalf("admitted a transaction whose envelope does not verify: %v", err)
		}
		checkSignedEnvelope(t, back)
		checkAdmittedPayload(t, tx)
	})
}

// checkSignedEnvelope asserts what a verified envelope must satisfy.
func checkSignedEnvelope(t *testing.T, env SignedEnvelope) {
	t.Helper()
	if env.ContractID != SignedContractID || strings.TrimSpace(env.ID) == "" || strings.TrimSpace(env.PayloadB64) == "" {
		t.Fatalf("verified an envelope without contract, id or payload: %+v", env)
	}
	pub, err := hex.DecodeString(env.PublicKey)
	sum := sha256.Sum256(pub)
	if err != nil || env.Sender != hex.EncodeToString(sum[:]) {
		t.Fatalf("verified sender %q, which is not hex(sha256(public_key))", env.Sender)
	}
	unsigned := env
	unsigned.Signature, unsigned.PublicKey = "", ""
	msg, _ := json.Marshal(unsigned)
	sig, err := hex.DecodeString(env.Signature)
	var pk mldsa87.PublicKey
	if err != nil || pk.UnmarshalBinary(pub) != nil || !mldsa87.Verify(&pk, msg, nil, sig) {
		t.Fatal("verified a signature the reference ML-DSA-87 verifier rejects")
	}
	for _, change := range []func(*SignedEnvelope){
		func(e *SignedEnvelope) { e.Nonce++ },
		func(e *SignedEnvelope) { e.Fee += 1 },
		func(e *SignedEnvelope) { e.ID += "x" },
		func(e *SignedEnvelope) { e.PayloadB64 = base64.StdEncoding.EncodeToString([]byte(`{"kind":"enroll"}`)) },
	} {
		changed := env
		change(&changed)
		if changed != env && VerifySignedEnvelope(changed) == nil {
			t.Fatalf("a signature verified after a signed field changed: %+v", changed)
		}
	}
}

// checkAdmittedPayload asserts the stateless enrollment rules for an
// admitted transaction.
func checkAdmittedPayload(t *testing.T, tx *mempool.Tx) {
	t.Helper()
	if tx.Fee < 0 || tx.Sender == "" {
		t.Fatalf("admitted fee %v from sender %q", tx.Fee, tx.Sender)
	}
	kind, err := PeekKind(tx.Payload)
	if err != nil {
		t.Fatalf("admitted a payload without a kind: %v", err)
	}
	switch kind {
	case PayloadKindEnroll:
		p, err := DecodeEnrollPayload(tx.Payload)
		if err != nil || !fuzzNodeID(p.NodeID) || !fuzzGPUUUID(p.GPUUUID) ||
			len(p.HMACKey) < MinHMACKeyLen || len(p.HMACKey) > MaxHMACKeyLen || len(p.Memo) > MaxMemoLen {
			t.Fatalf("admitted enroll payload %+v (%v)", p, err)
		}
		switch p.BondMode {
		case "", BondModeUpfront:
			if p.StakeDust != mining.MinEnrollStakeDust || p.WorkNonce != 0 {
				t.Fatalf("admitted an upfront enrollment staking %d dust (work nonce %d)", p.StakeDust, p.WorkNonce)
			}
		case BondModeMiningRewards:
			zeroed := p
			zeroed.WorkNonce = 0
			canonical, err := EncodeEnrollPayload(zeroed)
			if err != nil || p.StakeDust != 0 {
				t.Fatalf("admitted a deferred-bond enrollment staking %d dust (%v)", p.StakeDust, err)
			}
			var suffix [8]byte
			binary.BigEndian.PutUint64(suffix[:], p.WorkNonce)
			digest := sha256.Sum256(append(append([]byte("qsdm-deferred-bond-work-v1\n"), canonical...), suffix[:]...))
			zeros := 0
			for _, b := range digest {
				zeros += bits.LeadingZeros8(b)
				if b != 0 {
					break
				}
			}
			if zeros < int(DeferredBondWorkDifficulty) {
				t.Fatalf("admitted deferred-bond work %x with %d leading zero bits", digest[:4], zeros)
			}
		default:
			t.Fatalf("admitted bond_mode %q", p.BondMode)
		}
	case PayloadKindUnenroll:
		p, err := DecodeUnenrollPayload(tx.Payload)
		if err != nil || !fuzzNodeID(p.NodeID) || len(p.Reason) > MaxMemoLen {
			t.Fatalf("admitted unenroll payload %+v (%v)", p, err)
		}
	default:
		t.Fatalf("admitted payload kind %q", kind)
	}
}
