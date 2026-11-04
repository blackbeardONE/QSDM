package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// Fuzz targets for the wallet-signed envelopes two public POST routes verify
// in this package before anything is stored: /api/v1/tasks/actions/submit-signed
// and /api/v1/referrals/register-signed. Each harness decodes the body as its
// handler does and runs the handler's validate and verify steps. In "harness
// signs" mode the harness signs the decoded envelope with a fixed ML-DSA-87
// key (and, with "stamp", dates it now), so the fuzzer reaches acceptance.

type fuzzWalletKey struct {
	private *mldsa87.PrivateKey
	public  *mldsa87.PublicKey
	pubHex  string
	address string
}

func newFuzzWalletKey(tag string) fuzzWalletKey {
	var seed [mldsa87.SeedSize]byte
	copy(seed[:], tag)
	public, private := mldsa87.NewKeyFromSeed(&seed)
	pub := public.Bytes()
	sum := sha256.Sum256(pub)
	return fuzzWalletKey{private: private, public: public, pubHex: hex.EncodeToString(pub), address: hex.EncodeToString(sum[:])}
}

// signJSON signs the JSON encoding of v and returns the signature as hex.
func (k fuzzWalletKey) signJSON(v any) string {
	msg, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(k.private, msg, nil, false, sig); err != nil {
		panic(err)
	}
	return hex.EncodeToString(sig)
}

// fuzzSignedBy asserts that sigHex verifies over the JSON encoding of
// unsigned under pubHex, with the reference ML-DSA-87 verifier.
func fuzzSignedBy(t *testing.T, unsigned any, sigHex, pubHex string) {
	t.Helper()
	msg, _ := json.Marshal(unsigned)
	pub, perr := hex.DecodeString(pubHex)
	sig, serr := hex.DecodeString(sigHex)
	var pk mldsa87.PublicKey
	if perr != nil || serr != nil || pk.UnmarshalBinary(pub) != nil || !mldsa87.Verify(&pk, msg, nil, sig) {
		t.Fatal("accepted a signature the reference ML-DSA-87 verifier rejects")
	}
}

// fuzzWalletAddress reports whether address is hex(sha256(public key)).
func fuzzWalletAddress(address, pubHex string) bool {
	pub, err := hex.DecodeString(pubHex)
	sum := sha256.Sum256(pub)
	return err == nil && address == hex.EncodeToString(sum[:])
}

// fuzzFresh reports whether ts is absent (ValidateTimestamp permits that) or
// inside the accepted timestamp window.
func fuzzFresh(ts string, before, after time.Time) bool {
	if ts == "" {
		return true
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		at, err = time.Parse(time.RFC3339, ts)
	}
	return err == nil && !at.After(after.Add(MaxClockSkew)) && !at.Before(before.Add(-MaxTransactionAge))
}

func fuzzHandlers(f *testing.F) *Handlers {
	ws, err := wallet.NewWalletService()
	if err != nil {
		f.Skipf("wallet requires an ML-DSA backend: %v", err)
	}
	return &Handlers{walletService: ws}
}

var fuzzTaskActions = map[string]bool{
	"start": true, "stop": true, "stake": true, "fund": true, "unstake": true, "submit": true, "claim": true,
	"withdraw": true, "migrate": true, chain.TaskActionCatalogRegister: true, chain.TaskActionCatalogUpdate: true,
	chain.TaskActionCatalogPause: true, chain.TaskActionCatalogResume: true,
}

func FuzzTaskActionEnvelope(f *testing.F) {
	h := fuzzHandlers(f)
	key := newFuzzWalletKey("api task action fuzz key")
	sign := func(env QSDMTaskActionEnvelope) QSDMTaskActionEnvelope {
		env.Sender, env.PublicKey, env.Signature = key.address, "", ""
		env.Signature, env.PublicKey = key.signJSON(env), key.pubHex
		return env
	}
	now := time.Now().UTC().Format(time.RFC3339)
	genuine := sign(QSDMTaskActionEnvelope{ID: "task-action-0000000000000001", TaskID: "qsdm.cpu.hash-chain",
		Action: "stake", Amount: 25, Payload: `{"note":"fuzz"}`, Nonce: 4, Timestamp: now})
	if validateQSDMTaskActionEnvelope(genuine) != nil || h.verifyQSDMTaskActionEnvelope(genuine) != nil {
		f.Fatal("genuine seed rejected")
	}
	raw := func(env QSDMTaskActionEnvelope) []byte {
		b, _ := json.Marshal(env)
		return b
	}
	catalog := genuine
	catalog.Action, catalog.Amount = chain.TaskActionCatalogRegister, 0
	for _, s := range []struct {
		body  []byte
		signs bool
		stamp bool
	}{
		{raw(genuine), false, false},
		{raw(genuine), true, true},
		{raw(catalog), true, true},
		{bytes.Replace(raw(genuine), []byte(`"stake"`), []byte(`" STAKE "`), 1), true, true},
		{bytes.Replace(raw(genuine), []byte(`"stake"`), []byte(`"mint"`), 1), true, true},
		{bytes.Replace(raw(genuine), []byte(`"amount":25`), []byte(`"amount":-1`), 1), true, true},
		{bytes.Replace(raw(genuine), []byte(`"amount":25`), []byte(`"amount":26`), 1), false, false},
		{bytes.Replace(raw(genuine), []byte(`"timestamp":"`), []byte(`"timestamp":"1999`), 1), true, false},
		{[]byte(`{"id":"short","sender":"S","task_id":"","action":"start","timestamp":"x"}`), true, false},
		{[]byte(`{"id":"task-action-0000000000000002"} trailing`), true, true},
	} {
		f.Add(s.body, s.signs, s.stamp)
	}

	f.Fuzz(func(t *testing.T, body []byte, harnessSigns, stamp bool) {
		var env QSDMTaskActionEnvelope
		if json.NewDecoder(io.LimitReader(bytes.NewReader(body), 1<<20)).Decode(&env) != nil {
			return
		}
		// The handler's normalisation, then its two checks.
		env.Action = strings.ToLower(strings.TrimSpace(env.Action))
		env.Sender = strings.TrimSpace(strings.ToLower(env.Sender))
		env.TaskID = strings.TrimSpace(env.TaskID)
		if stamp {
			env.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}
		if harnessSigns {
			env = sign(env)
		}
		before := time.Now()
		if validateQSDMTaskActionEnvelope(env) != nil || h.verifyQSDMTaskActionEnvelope(env) != nil {
			return
		}
		after := time.Now()
		if !fuzzValidTxID(env.ID) || !fuzzTaskActions[env.Action] || env.TaskID == "" || len(env.TaskID) > 256 ||
			len(env.Payload) > 100000 || !fuzzFresh(env.Timestamp, before, after) {
			t.Fatalf("accepted task action %+v", env)
		}
		if env.Amount != 0 && !(env.Amount >= MinAmount && env.Amount <= MaxAmount) {
			t.Fatalf("accepted amount %v", env.Amount)
		}
		if !fuzzWalletAddress(env.Sender, env.PublicKey) {
			t.Fatalf("accepted sender %q, which is not hex(sha256(public_key))", env.Sender)
		}
		unsigned := env
		unsigned.Signature, unsigned.PublicKey = "", ""
		fuzzSignedBy(t, unsigned, env.Signature, env.PublicKey)
		// Consensus re-verifies the same proof when the action is applied.
		if err := chain.VerifyTaskActionSignature(qsdmTaskActionToChain(env), env.Signature, env.PublicKey, 0); err != nil {
			t.Fatalf("the API accepted a task action consensus would refuse: %v", err)
		}
		for _, change := range []func(*QSDMTaskActionEnvelope){
			func(e *QSDMTaskActionEnvelope) { e.Nonce++ },
			func(e *QSDMTaskActionEnvelope) { e.TaskID += "x" },
			func(e *QSDMTaskActionEnvelope) { e.Payload += " " },
		} {
			changed := env
			change(&changed)
			if h.verifyQSDMTaskActionEnvelope(changed) == nil {
				t.Fatalf("a signature verified after a signed field changed: %+v", changed)
			}
		}
	})
}

func FuzzReferralRegistrationEnvelope(f *testing.F) {
	h := fuzzHandlers(f)
	key := newFuzzWalletKey("api referral fuzz key")
	referrer := strings.Repeat("ab", 32)
	sign := func(env ReferralRegistrationEnvelope) ReferralRegistrationEnvelope {
		env.Referred, env.PublicKey, env.Signature = key.address, "", ""
		env.Signature, env.PublicKey = key.signJSON(env), key.pubHex
		return env
	}
	genuine := sign(ReferralRegistrationEnvelope{ID: "referral-0000000000000001", Referrer: referrer,
		ReferralCode: referralCodeForAddress(referrer), InstallID: "install-fuzz-1",
		Timestamp: time.Now().UTC().Format(time.RFC3339)})
	if h.validateReferralRegistrationEnvelope(genuine) != nil || h.verifyReferralRegistrationEnvelope(genuine) != nil {
		f.Fatal("genuine seed rejected")
	}
	raw := func(env ReferralRegistrationEnvelope) []byte {
		b, _ := json.Marshal(env)
		return b
	}
	lower := genuine
	lower.ReferralCode = strings.ToLower(lower.ReferralCode)
	self := genuine
	self.Referrer = key.address
	for _, s := range []struct {
		body  []byte
		signs bool
		stamp bool
	}{
		{raw(genuine), false, false},
		{raw(lower), true, true},
		{raw(self), true, true},
		{bytes.Replace(raw(genuine), []byte(`"install-fuzz-1"`), []byte(`""`), 1), true, true},
		{bytes.Replace(raw(genuine), []byte(referrer), []byte(strings.ToUpper(referrer)), 1), true, true},
		{bytes.Replace(raw(genuine), []byte(`"install-fuzz-1"`), []byte(`"install-fuzz-2"`), 1), false, false},
		{[]byte(`{"id":"referral-0000000000000002","referrer":"00","referred":"00","referral_code":"ABCDEF"}`), true, true},
	} {
		f.Add(s.body, s.signs, s.stamp)
	}

	f.Fuzz(func(t *testing.T, body []byte, harnessSigns, stamp bool) {
		var env ReferralRegistrationEnvelope
		if json.NewDecoder(io.LimitReader(bytes.NewReader(body), 1<<20)).Decode(&env) != nil {
			return
		}
		if stamp {
			env.Timestamp = time.Now().UTC().Format(time.RFC3339)
		}
		if harnessSigns {
			env = sign(env)
		}
		before := time.Now()
		if h.validateReferralRegistrationEnvelope(env) != nil || h.verifyReferralRegistrationEnvelope(env) != nil {
			return
		}
		after := time.Now()
		hexAddr := func(s string) bool {
			return len(s) >= MinAddressLength && len(s) <= MaxAddressLength && fuzzAllBytes(s, fuzzIsHex)
		}
		code := sha256.Sum256([]byte(env.Referrer))
		if !fuzzValidTxID(env.ID) || !hexAddr(env.Referrer) || !hexAddr(env.Referred) || env.Referrer == env.Referred ||
			!strings.EqualFold(env.ReferralCode, hex.EncodeToString(code[:])[:12]) || len(env.InstallID) > 128 ||
			!fuzzFresh(env.Timestamp, before, after) {
			t.Fatalf("accepted referral registration %+v", env)
		}
		if !fuzzWalletAddress(env.Referred, env.PublicKey) {
			t.Fatalf("accepted referred wallet %q, which is not hex(sha256(public_key))", env.Referred)
		}
		unsigned := env
		unsigned.Signature, unsigned.PublicKey = "", ""
		fuzzSignedBy(t, unsigned, env.Signature, env.PublicKey)
		changed := env
		changed.Referrer = strings.Repeat("cd", 32)
		if changed.Referrer != env.Referrer && h.verifyReferralRegistrationEnvelope(changed) == nil {
			t.Fatal("a referral signature verified for another referrer")
		}
	})
}
