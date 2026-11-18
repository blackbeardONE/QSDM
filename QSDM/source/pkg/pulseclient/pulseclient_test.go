package pulseclient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func newKey(t *testing.T) (*mldsa87.PrivateKey, []byte) {
	t.Helper()
	pk, sk, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return sk, pk.Bytes()
}

// The same vector pins the node (chain.PulseTxSigningBytes) and the pool
// (pulse.TxSigningBytes): a member's signature is valid exactly where the
// chain checks it.
func TestTxSigningBytesVector(t *testing.T) {
	b := TxSigningBytes("abababababababababababababababababababababababababababababababab", 7, 0.001,
		[]byte(`{"op":"join","pool":"cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"}`))
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != "809119a56e49435857355a33fc40b3ad2d84af5a18adab2a66110728f2766a95" {
		t.Fatalf("vector %s", got)
	}
}

// The join payload must serialize exactly as the vector's payload.
func TestJoinPayloadShape(t *testing.T) {
	b, _ := json.Marshal(Action{Op: "join", Pool: strings.Repeat("cd", 32)})
	if string(b) != `{"op":"join","pool":"cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"}` {
		t.Fatalf("payload %s", b)
	}
}

func TestSignActionVerifiesAndBindsEveryField(t *testing.T) {
	sk, pub := newKey(t)
	env, err := SignAction(sk, pub, 3, 0.001, Action{Op: "join", Pool: strings.Repeat("ab", 32)})
	if err != nil {
		t.Fatal(err)
	}
	if env.Sender != AddressOf(pub) {
		t.Fatal("sender is not the key's address")
	}
	if err := VerifyEnvelope(env); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(e *Envelope){
		"nonce":   func(e *Envelope) { e.Nonce++ },
		"fee":     func(e *Envelope) { e.Fee = 0.002 },
		"payload": func(e *Envelope) { e.Payload = json.RawMessage(`{"op":"leave"}`) },
		"sender":  func(e *Envelope) { e.Sender = strings.Repeat("00", 32) },
	} {
		c := *env
		mut(&c)
		if VerifyEnvelope(&c) == nil {
			t.Fatalf("a changed %s still verifies", name)
		}
	}
}

func TestValidateAction(t *testing.T) {
	pool := strings.Repeat("ab", 32)
	good := []Action{{Op: "join", Pool: pool}, {Op: "leave"}, {Op: "reclaim", Proofs: []string{"p1", "p2"}}}
	for _, a := range good {
		if err := ValidateAction(a); err != nil {
			t.Fatalf("%+v refused: %v", a, err)
		}
	}
	bad := []Action{
		{Op: "settle"}, {Op: "join"}, {Op: "join", Pool: strings.ToUpper(pool)}, {Op: "join", Pool: pool, Proofs: []string{"x"}},
		{Op: "leave", Pool: pool}, {Op: "reclaim"}, {Op: "reclaim", Proofs: []string{"a", "a"}}, {Op: "reclaim", Pool: pool, Proofs: []string{"a"}},
	}
	for _, a := range bad {
		if ValidateAction(a) == nil {
			t.Fatalf("%+v accepted", a)
		}
	}
	many := make([]string, MaxReclaimProofs+1)
	for i := range many {
		many[i] = hex.EncodeToString([]byte{byte(i >> 8), byte(i)})
	}
	if ValidateAction(Action{Op: "reclaim", Proofs: many}) == nil {
		t.Fatal("an oversized reclaim was accepted")
	}
}

func TestSignRegister(t *testing.T) {
	sk, pub := newKey(t)
	login, err := Login(AddressOf(pub), "rig1")
	if err != nil {
		t.Fatal(err)
	}
	sig, err := SignRegister(sk, pub, login)
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := hex.DecodeString(sig)
	if !verify(pub, append([]byte(RegisterDomain), login...), sb) {
		t.Fatal("register signature does not verify")
	}
	if verify(pub, append([]byte(TxDomain), login...), sb) {
		t.Fatal("a register signature verifies under the tx domain")
	}
	if _, err := SignRegister(sk, pub, strings.Repeat("00", 32)+".rig1"); err == nil {
		t.Fatal("signed a login for someone else's address")
	}
	if _, err := Login(AddressOf(pub), "bad name"); err == nil {
		t.Fatal("a worker name with a space was accepted")
	}
}

func TestVerifyReceipt(t *testing.T) {
	sk, pub := newKey(t)
	r := Receipt{Pool: AddressOf(pub), Index: 9, Owner: strings.Repeat("ab", 32), Worker: "rig1", JobID: "j1",
		Nonce: strings.Repeat("11", 32), BatchCount: 4, Difficulty: 65536, AtUnixMs: 1}
	b, _ := json.Marshal(r)
	sig, _ := sign(sk, append([]byte(ReceiptDomain), b...))
	if !VerifyReceipt(hex.EncodeToString(pub), r, hex.EncodeToString(sig)) {
		t.Fatal("receipt does not verify")
	}
	r2 := r
	r2.Difficulty = 1 << 30
	if VerifyReceipt(hex.EncodeToString(pub), r2, hex.EncodeToString(sig)) {
		t.Fatal("an inflated receipt verifies")
	}
	_, other := newKey(t)
	if VerifyReceipt(hex.EncodeToString(other), r, hex.EncodeToString(sig)) {
		t.Fatal("a receipt verifies under another pool's key")
	}
	// A pool may not issue receipts in another pool's name: signed by key B,
	// naming pool A, checked with B's key.
	skB, pubB := newKey(t)
	rA := r // names the first pool
	bA, _ := json.Marshal(rA)
	sigB, _ := sign(skB, append([]byte(ReceiptDomain), bA...))
	if VerifyReceipt(hex.EncodeToString(pubB), rA, hex.EncodeToString(sigB)) {
		t.Fatal("a receipt naming another pool verifies")
	}
}

func TestReclaimable(t *testing.T) {
	me := strings.Repeat("ab", 32)
	s := &State{ReclaimAfterBlocks: 100, Tip: 199}
	s.Pool = &struct {
		ID           string            `json:"id"`
		Escrowed     []Escrowed        `json:"escrowed"`
		EscrowedDust uint64            `json:"escrowed_dust"`
		Owed         map[string]uint64 `json:"owed"`
	}{Escrowed: []Escrowed{
		{ProofID: "due", Owner: me, Height: 100},            // next block 200: 100 blocks later
		{ProofID: "early", Owner: me, Height: 101},          // 99 blocks
		{ProofID: "other", Owner: "cd" + me[2:], Height: 1}, // not mine
	}}
	got := s.Reclaimable(me)
	if len(got) != 1 || got[0].ProofID != "due" {
		t.Fatalf("reclaimable %+v", got)
	}
}

func TestPoolClientRoundTrip(t *testing.T) {
	var gotAuth, gotShare string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pool/api/v1/register":
			_, _ = w.Write([]byte(`{"session":"tok"}`))
		case "/pool/api/v1/work":
			gotAuth = r.Header.Get("Authorization")
			_, _ = w.Write([]byte(`{"job_id":"j1","share_difficulty":"65536","network_difficulty":"16777216","window_target":1,"work":{"height":5}}`))
		case "/pool/api/v1/shares":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			gotShare, _ = in["nonce"].(string)
			_, _ = w.Write([]byte(`{"accepted":true,"difficulty":65536}`))
		default:
			http.Error(w, "down", http.StatusBadGateway)
		}
	}))
	defer srv.Close()
	p := &Pool{Base: srv.URL + "/pool/api"}
	ctx := context.Background()
	if err := p.Register(ctx, "x", "", "", ""); err != nil {
		t.Fatal(err)
	}
	j, err := p.Work(ctx)
	if err != nil || j.JobID != "j1" || j.ShareDifficulty != "65536" || gotAuth != "Bearer tok" {
		t.Fatalf("work %+v %v auth %q", j, err, gotAuth)
	}
	res, err := p.Share(ctx, "j1", "ff", 4)
	if err != nil || !res.Accepted || gotShare != "ff" {
		t.Fatalf("share %+v %v", res, err)
	}
	if _, err := p.Info(ctx); !Unreachable(err) {
		t.Fatalf("a 502 is not reported as unreachable: %v", err)
	}
	var four = &PoolAPIError{Status: 401}
	if Unreachable(four) {
		t.Fatal("a 401 counted as unreachable")
	}
}

// testdata/pool-receipt-vector.json was signed by the pool's own code
// (qsdm-mining-bootstrap pulse.SignReceipt): a receipt the pool really issues
// must verify here, and a changed one must not.
func TestPoolSignedReceiptVector(t *testing.T) {
	raw, err := os.ReadFile("testdata/pool-receipt-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		PublicKey string  `json:"public_key"`
		Receipt   Receipt `json:"receipt"`
		Sig       string  `json:"sig"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if !VerifyReceipt(v.PublicKey, v.Receipt, v.Sig) {
		t.Fatal("a receipt signed by the pool does not verify")
	}
	v.Receipt.Index++
	if VerifyReceipt(v.PublicKey, v.Receipt, v.Sig) {
		t.Fatal("a changed pool receipt verifies")
	}
}
