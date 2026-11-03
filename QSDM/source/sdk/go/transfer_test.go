package qsdm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// nodeTransactionData is a copy of pkg/wallet.TransactionData from the node
// (QSDM/source/pkg/wallet/wallet.go). The fake node below decodes envelopes
// into it and re-encodes them exactly as pkg/api SubmitSignedTransaction
// does, so a test passes only if the SDK signs the bytes the node verifies.
type nodeTransactionData struct {
	ID          string   `json:"id"`
	Sender      string   `json:"sender"`
	Recipient   string   `json:"recipient"`
	Amount      float64  `json:"amount"`
	Fee         float64  `json:"fee"`
	GeoTag      string   `json:"geotag"`
	ParentCells []string `json:"parent_cells"`
	Nonce       uint64   `json:"nonce,omitempty"`
	Signature   string   `json:"signature"`
	PublicKey   string   `json:"public_key,omitempty"`
	Timestamp   string   `json:"timestamp"`
}

func (tx nodeTransactionData) canonicalBytes() []byte {
	tx.Signature = ""
	tx.PublicKey = ""
	b, err := json.Marshal(tx)
	if err != nil {
		panic(err)
	}
	return b
}

// fakeSigner stands in for an ML-DSA-87 key. Its "signature" is SHA-256
// chained to the ML-DSA-87 signature size, which lets the fake node check
// exactly which bytes were signed.
type fakeSigner struct{ pub []byte }

func newFakeSigner(seed byte) *fakeSigner {
	return &fakeSigner{pub: bytes.Repeat([]byte{seed}, MLDSA87PublicKeySize)}
}

func (s *fakeSigner) PublicKey() []byte { return s.pub }

func (s *fakeSigner) Sign(_ context.Context, message []byte) ([]byte, error) {
	return fakeSignature(s.pub, message), nil
}

func fakeSignature(publicKey, message []byte) []byte {
	block := sha256.Sum256(append(append([]byte{}, publicKey...), message...))
	out := make([]byte, 0, MLDSA87SignatureSize+sha256.Size)
	for len(out) < MLDSA87SignatureSize {
		out = append(out, block[:]...)
		block = sha256.Sum256(block[:])
	}
	return out[:MLDSA87SignatureSize]
}

const (
	testParentA = "solo-heartbeat-2253090-1791611907090994535"
	testParentB = "solo-heartbeat-2253089-1791611897111559959"
	testPayee   = "1111111111111111111111111111111111111111111111111111111111111111"
)

// TestTransferEnvelope_SigningBytesMatchNode pins SigningBytes to the bytes
// the node itself produces. The expected strings were printed by
// pkg/wallet.TransactionData.CanonicalBytes for the same envelopes (the JS
// SDK's qsdm.test.js pins the same three vectors). They cover HTML and
// line-separator escaping, an exponent-form amount, a nil and an empty
// parent list, and an omitted zero nonce.
func TestTransferEnvelope_SigningBytesMatchNode(t *testing.T) {
	const sender = "5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7"
	cases := []struct {
		env  TransferEnvelope
		want string
	}{
		{
			env: TransferEnvelope{
				ID: "sdk_go_1790000000000_0123456789abcdef", Sender: sender, Recipient: testPayee,
				Amount: 1.25, ParentCells: []string{testParentA, testParentB}, Nonce: 2,
				Signature: "aa", PublicKey: "bb", Timestamp: "2026-11-03T06:20:09.07Z",
			},
			want: `{"id":"sdk_go_1790000000000_0123456789abcdef","sender":"5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7","recipient":"1111111111111111111111111111111111111111111111111111111111111111","amount":1.25,"fee":0,"geotag":"","parent_cells":["solo-heartbeat-2253090-1791611907090994535","solo-heartbeat-2253089-1791611897111559959"],"nonce":2,"signature":"","timestamp":"2026-11-03T06:20:09.07Z"}`,
		},
		{
			env: TransferEnvelope{
				ID: "tx<&>id-0000000001", Sender: sender,
				Recipient: "2222222222222222222222222222222222222222222222222222222222222222",
				Amount:    1e-7, Fee: 0.29, GeoTag: "<eu>&\u2028\u2029\u00e9\"\\",
				Timestamp: "2026-11-03T06:20:09.123456789Z",
			},
			want: `{"id":"tx\u003c\u0026\u003eid-0000000001","sender":"5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7","recipient":"2222222222222222222222222222222222222222222222222222222222222222","amount":1e-7,"fee":0.29,"geotag":"\u003ceu\u003e\u0026\u2028\u2029` + "\u00e9" + `\"\\","parent_cells":null,"signature":"","timestamp":"2026-11-03T06:20:09.123456789Z"}`,
		},
		{
			env: TransferEnvelope{
				ID: "sdk_js_1790000000000_fedcba9876543210", Sender: sender,
				Recipient: "3333333333333333333333333333333333333333333333333333333333333333",
				Amount:    123456789.12345678, Fee: 0.00000001, GeoTag: "SG",
				ParentCells: []string{}, Nonce: 9007199254740991, Timestamp: "2026-11-03T06:20:09Z",
			},
			want: `{"id":"sdk_js_1790000000000_fedcba9876543210","sender":"5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7","recipient":"3333333333333333333333333333333333333333333333333333333333333333","amount":123456789.12345678,"fee":1e-8,"geotag":"SG","parent_cells":[],"nonce":9007199254740991,"signature":"","timestamp":"2026-11-03T06:20:09Z"}`,
		},
	}
	for i, tc := range cases {
		got, err := tc.env.SigningBytes()
		if err != nil {
			t.Fatalf("case %d: SigningBytes: %v", i, err)
		}
		if string(got) != tc.want {
			t.Fatalf("case %d:\n got %s\nwant %s", i, got, tc.want)
		}
	}
}

func TestTransferEnvelope_SigningBytesRejectsInvalidUTF8(t *testing.T) {
	env := TransferEnvelope{ID: "id-\xff-0000000000", Sender: "s", Recipient: "r", Timestamp: "t"}
	if _, err := env.SigningBytes(); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("SigningBytes error = %v, want an invalid UTF-8 error", err)
	}
}

// fakeNode serves the routes a signed transfer needs and checks every
// submitted envelope the way pkg/api SubmitSignedTransaction does.
type fakeNode struct {
	t       *testing.T
	mu      sync.Mutex
	parents []string
	nonce   uint64
	seen    []string
	// submitStatus and submitBody, when set, replace the success answer.
	submitStatus int
	submitBody   string
	submitted    []nodeTransactionData
}

func (n *fakeNode) handler(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.seen = append(n.seen, r.Method+" "+r.URL.RequestURI())
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/wallet/nonce":
		sender := r.URL.Query().Get("sender")
		_ = json.NewEncoder(w).Encode(WalletNonceResponse{Sender: sender, Nonce: n.nonce, Next: n.nonce + 1})
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/chain/parents":
		_ = json.NewEncoder(w).Encode(ChainParentsResponse{
			Tip: 900000, PoEActivationHeight: 816600, PoEActive: true,
			MinParents: 2, MaxParents: 10, WindowBlocks: 8640, Parents: n.parents,
		})
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/wallet/submit-signed":
		var env nodeTransactionData
		if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
			n.t.Errorf("decode envelope: %v", err)
			http.Error(w, "bad envelope", http.StatusBadRequest)
			return
		}
		n.submitted = append(n.submitted, env)
		if n.submitStatus != 0 {
			w.WriteHeader(n.submitStatus)
			_, _ = w.Write([]byte(n.submitBody))
			return
		}
		pub, err := hex.DecodeString(env.PublicKey)
		if err != nil || WalletAddress(pub) != env.Sender {
			n.t.Errorf("sender %s is not hex(sha256(public_key))", env.Sender)
		}
		if env.Signature != hex.EncodeToString(fakeSignature(pub, env.canonicalBytes())) {
			n.t.Errorf("signature does not cover the node's canonical bytes %s", env.canonicalBytes())
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(SubmitTransferResponse{
			TransactionID: env.ID, Status: "pending", Broadcast: "block-pending",
		})
	default:
		n.t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		http.NotFound(w, r)
	}
}

func TestClient_SendTransfer_ParentsRoundTrip(t *testing.T) {
	signer := newFakeSigner(7)
	sender := WalletAddress(signer.PublicKey())
	node := &fakeNode{t: t, parents: []string{testParentA, testParentB}, nonce: 4}
	srv, client := newTestServer(node.handler)
	defer srv.Close()

	out, err := client.SendTransfer(context.Background(), TransferRequest{
		Recipient: testPayee, Amount: 2.5, Fee: 0.01,
	}, signer)
	if err != nil {
		t.Fatalf("SendTransfer: %v", err)
	}
	if out.Status != "pending" || out.HTTPStatus != http.StatusAccepted || out.Broadcast != "block-pending" {
		t.Fatalf("unexpected response: %+v", out)
	}
	if len(node.submitted) != 1 {
		t.Fatalf("submitted %d envelopes, want 1", len(node.submitted))
	}
	env := node.submitted[0]
	if out.TransactionID != env.ID {
		t.Fatalf("transaction id %q, want %q", out.TransactionID, env.ID)
	}
	if strings.Join(env.ParentCells, ",") != testParentA+","+testParentB {
		t.Fatalf("parent_cells = %v, want the node's parents", env.ParentCells)
	}
	if env.Sender != sender || env.Recipient != testPayee || env.Amount != 2.5 || env.Fee != 0.01 || env.Nonce != 5 {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if !regexp.MustCompile(`^sdk_go_[0-9]+_[0-9a-f]{16}$`).MatchString(env.ID) {
		t.Fatalf("generated id %q is not a valid parent-shaped id", env.ID)
	}
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?Z$`).MatchString(env.Timestamp) {
		t.Fatalf("timestamp %q is not RFC 3339 UTC", env.Timestamp)
	}
	want := []string{
		"GET /api/v1/wallet/nonce?sender=" + sender,
		"GET /api/v1/chain/parents",
		"POST /api/v1/wallet/submit-signed",
	}
	if strings.Join(node.seen, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests:\n%s\nwant:\n%s", strings.Join(node.seen, "\n"), strings.Join(want, "\n"))
	}
}

// A 422 Proof-of-Entanglement refusal comes back as a *TransferError that
// says what to do, and is still an *ErrAPI.
func TestClient_SubmitSignedTransfer_PoERejection(t *testing.T) {
	node := &fakeNode{
		t: t, parents: []string{testParentA, testParentB}, nonce: 0,
		submitStatus: http.StatusUnprocessableEntity,
		submitBody: `{"error":"Unprocessable Entity","message":"proof-of-entanglement: proof-of-entanglement violation: ` +
			`parent is not a committed transaction in the reference window: parent 0; sign again with parents from ` +
			`GET /api/v1/chain/parents","status":422}`,
	}
	srv, client := newTestServer(node.handler)
	defer srv.Close()

	_, err := client.SendTransfer(context.Background(), TransferRequest{Recipient: testPayee, Amount: 1}, newFakeSigner(3))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !IsPoERejection(err) {
		t.Fatalf("IsPoERejection(%v) = false", err)
	}
	var te *TransferError
	if !errors.As(err, &te) || te.Reason != RejectPoEParents || te.API.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("unexpected error: %#v", err)
	}
	if !strings.HasPrefix(te.Message, "proof-of-entanglement:") {
		t.Fatalf("message %q lost the node's explanation", te.Message)
	}
	if !strings.Contains(err.Error(), "fetch fresh parents") {
		t.Fatalf("error %q does not say how to recover", err)
	}
	var apiErr *ErrAPI
	if !errors.As(err, &apiErr) || IsNotFound(err) || IsUnauthorized(err) {
		t.Fatalf("TransferError must unwrap to *ErrAPI: %v", err)
	}
	if got := node.submitted[0].ParentCells; len(got) != 2 || got[0] != testParentA {
		t.Fatalf("the refused envelope did not carry the node's parents: %v", got)
	}
}

func TestClient_SubmitSignedTransfer_Rejections(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   TransferRejection
		txID   string
	}{
		{"validation", 400, `{"error":"Bad Request","message":"envelope.sender does not match hex(sha256(public_key))","status":400}`, RejectInvalid, ""},
		{"balance", 402, `{"error":"Payment Required","message":"insufficient canonical CELL balance for amount + fee","status":402}`, RejectInsufficientBalance, ""},
		{"duplicate", 409, `{"transaction_id":"tx-dup-000000000001","status":"duplicate","broadcast":"block-pending"}`, RejectDuplicate, "tx-dup-000000000001"},
		{"nonce gap", 409, `{"error":"Conflict","message":"nonce gap: envelope nonce 7; next required nonce is 5","status":409}`, RejectNonce, ""},
		{"signature", 422, `{"error":"Unprocessable Entity","message":"signature does not verify under envelope.public_key","status":422}`, RejectSignature, ""},
		{"rate limit", 429, `{"error":"Too Many Requests","message":"rate limit exceeded","status":429}`, RejectRateLimited, ""},
		{"read-only follower", 503, `{"error":"Service Unavailable","message":"network follower is read-only; send state-changing requests to the authoritative QSDM Core","status":503}`, RejectUnavailable, ""},
		{"plain text", 500, "internal error\n", RejectOther, ""},
	}
	signed, err := SignTransfer(context.Background(), TransferEnvelope{
		ID: "sdk_go_1_0000000000000000", Sender: WalletAddress(newFakeSigner(1).PublicKey()),
		Recipient: testPayee, Amount: 1, ParentCells: []string{testParentA, testParentB}, Nonce: 1,
		Timestamp: "2026-11-03T00:00:00Z",
	}, newFakeSigner(1))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &fakeNode{t: t, submitStatus: tc.status, submitBody: tc.body}
			srv, client := newTestServer(node.handler)
			defer srv.Close()
			_, err := client.SubmitSignedTransfer(context.Background(), signed)
			var te *TransferError
			if !errors.As(err, &te) {
				t.Fatalf("error %v is not a *TransferError", err)
			}
			if te.Reason != tc.want || te.TransactionID != tc.txID || te.API.StatusCode != tc.status {
				t.Fatalf("got reason %q tx %q status %d, want %q %q %d", te.Reason, te.TransactionID, te.API.StatusCode, tc.want, tc.txID, tc.status)
			}
			if te.Message == "" || strings.Contains(te.Message, `"error"`) {
				t.Fatalf("message %q should be the node's explanation, not raw JSON", te.Message)
			}
		})
	}
}

func TestTransferError_WithoutAPI(t *testing.T) {
	err := error(&TransferError{Reason: RejectPoEParents, Message: "proof-of-entanglement: stale"})
	if !IsPoERejection(err) || IsNotFound(err) || IsUnauthorized(err) {
		t.Fatalf("classification of a TransferError without API failed: %v", err)
	}
	if !strings.Contains(err.Error(), "poe_parents") {
		t.Fatalf("Error() = %q", err.Error())
	}
}

func TestClient_SubmitSignedTransfer_RequiresSignature(t *testing.T) {
	node := &fakeNode{t: t}
	srv, client := newTestServer(node.handler)
	defer srv.Close()
	_, err := client.SubmitSignedTransfer(context.Background(), TransferEnvelope{ID: "x", Sender: "s"})
	if err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("error = %v, want a not-signed error", err)
	}
	if len(node.seen) != 0 {
		t.Fatalf("an unsigned envelope reached the node: %v", node.seen)
	}
}

// Nodes released before /api/v1/chain/parents still list receipts: the
// newest successful, well-formed, distinct IDs are used, as in pkg/poe.
func TestClient_GetPoEParents_FallsBackToReceipts(t *testing.T) {
	var seen []string
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.RequestURI())
		switch r.URL.Path {
		case "/api/v1/chain/parents":
			http.NotFound(w, r)
		case "/api/v1/receipts":
			_, _ = w.Write([]byte(`{"tip":9,"receipts":[
				{"tx_id":"failed-transfer-000000001","status":0},
				{"tx_id":"short","status":1},
				{"tx_id":"solo-heartbeat-9-1791611927090994535","status":1},
				{"tx_id":"solo-heartbeat-9-1791611927090994535","status":1},
				{"tx_id":"solo-reward-9-abcdef0123456789","status":1},
				{"tx_id":"solo-heartbeat-8-1791611917090994535","status":1}]}`))
		default:
			http.NotFound(w, r)
		}
	})
	defer srv.Close()
	got, err := client.GetPoEParents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "solo-heartbeat-9-1791611927090994535,solo-reward-9-abcdef0123456789" {
		t.Fatalf("parents = %v", got)
	}
	if strings.Join(seen, " ") != "/api/v1/chain/parents /api/v1/receipts?limit=32" {
		t.Fatalf("requests = %v", seen)
	}
}

func TestClient_GetPoEParents_Failures(t *testing.T) {
	t.Run("unavailable is not a missing route", func(t *testing.T) {
		srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/receipts" {
				t.Error("a 503 from the parents route must not fall back to receipts")
			}
			http.Error(w, "not enough committed transactions to suggest parents yet", http.StatusServiceUnavailable)
		})
		defer srv.Close()
		if _, err := client.GetPoEParents(context.Background()); err == nil || errors.Is(err, ErrNoPoEParents) {
			t.Fatalf("err = %v, want the node's 503", err)
		}
	})
	t.Run("malformed parents", func(t *testing.T) {
		srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"parents":["parent1","parent2"]}`))
		})
		defer srv.Close()
		if _, err := client.GetPoEParents(context.Background()); !errors.Is(err, ErrNoPoEParents) {
			t.Fatalf("err = %v, want ErrNoPoEParents", err)
		}
	})
}

func TestClient_GetChainParents(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/api/v1/chain/parents?limit=16" {
			t.Errorf("request = %s", r.URL.RequestURI())
		}
		_, _ = w.Write([]byte(`{"tip":805400,"poe_activation_height":816600,"poe_active":true,"min_parents":2,` +
			`"max_parents":10,"window_blocks":8640,"parents":["` + testParentA + `","` + testParentB + `"],` +
			`"recent":[{"id":"` + testParentA + `","height":805400}]}`))
	})
	defer srv.Close()
	got, err := client.GetChainParents(context.Background(), 16)
	if err != nil {
		t.Fatal(err)
	}
	if !got.PoEActive || got.PoEActivationHeight != 816600 || got.WindowBlocks != 8640 ||
		len(got.Parents) != 2 || got.Recent[0].Height != 805400 {
		t.Fatalf("unexpected response: %+v", got)
	}
}

func TestClient_PrepareTransfer(t *testing.T) {
	t.Run("validates before any request", func(t *testing.T) {
		node := &fakeNode{t: t}
		srv, client := newTestServer(node.handler)
		defer srv.Close()
		sender := WalletAddress(newFakeSigner(1).PublicKey())
		bad := []TransferRequest{
			{Sender: sender, Recipient: "QSDM_RECIPIENT", Amount: 1},
			{Sender: strings.ToUpper(sender), Recipient: testPayee, Amount: 1},
			{Sender: sender, Recipient: testPayee, Amount: 0},
			{Sender: sender, Recipient: testPayee, Amount: 1, Fee: -1},
			{Sender: sender, Recipient: testPayee, Amount: 0.123456789},
			{Sender: sender, Recipient: testPayee, Amount: 1, Fee: 1e-9},
		}
		for _, req := range bad {
			if _, err := client.PrepareTransfer(context.Background(), req); err == nil {
				t.Fatalf("PrepareTransfer(%+v) succeeded", req)
			}
		}
		if len(node.seen) != 0 {
			t.Fatalf("invalid requests reached the node: %v", node.seen)
		}
	})
	t.Run("keeps caller values", func(t *testing.T) {
		node := &fakeNode{t: t}
		srv, client := newTestServer(node.handler)
		defer srv.Close()
		env, err := client.PrepareTransfer(context.Background(), TransferRequest{
			Sender: WalletAddress(newFakeSigner(1).PublicKey()), Recipient: testPayee, Amount: 1,
			ID: "my-transfer-0000000001", Timestamp: "2026-11-03T00:00:00Z", Nonce: 12,
			ParentCells: []string{},
		})
		if err != nil {
			t.Fatal(err)
		}
		if env.ID != "my-transfer-0000000001" || env.Nonce != 12 || env.ParentCells == nil || len(env.ParentCells) != 0 {
			t.Fatalf("caller values were replaced: %+v", env)
		}
		if len(node.seen) != 0 {
			t.Fatalf("nothing should be fetched: %v", node.seen)
		}
	})
	t.Run("refuses an inconsistent nonce", func(t *testing.T) {
		srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"sender":"` + testPayee + `","nonce":3,"next":4}`))
		})
		defer srv.Close()
		_, err := client.PrepareTransfer(context.Background(), TransferRequest{
			Sender: WalletAddress(newFakeSigner(1).PublicKey()), Recipient: testPayee, Amount: 1,
		})
		if err == nil || !strings.Contains(err.Error(), "inconsistent nonce") {
			t.Fatalf("err = %v, want an inconsistent nonce error", err)
		}
	})
}

type badSigner struct {
	pub []byte
	sig []byte
}

func (s badSigner) PublicKey() []byte                            { return s.pub }
func (s badSigner) Sign(context.Context, []byte) ([]byte, error) { return s.sig, nil }

func TestSignTransfer_Checks(t *testing.T) {
	good := newFakeSigner(5)
	env := TransferEnvelope{
		ID: "sdk_go_1_0000000000000000", Sender: WalletAddress(good.PublicKey()), Recipient: testPayee,
		Amount: 1, ParentCells: []string{testParentA, testParentB}, Nonce: 1, Timestamp: "2026-11-03T00:00:00Z",
	}
	signed, err := SignTransfer(context.Background(), env, good)
	if err != nil {
		t.Fatal(err)
	}
	if len(signed.Signature) != 2*MLDSA87SignatureSize || len(signed.PublicKey) != 2*MLDSA87PublicKeySize {
		t.Fatalf("signature/public key hex lengths %d/%d", len(signed.Signature), len(signed.PublicKey))
	}
	if env.Signature != "" {
		t.Fatal("SignTransfer modified its input")
	}
	cases := map[string]TransferSigner{
		"sender":     newFakeSigner(6),
		"public key": badSigner{pub: []byte{1, 2, 3}},
		"signature":  badSigner{pub: good.PublicKey(), sig: []byte{1}},
	}
	for name, signer := range cases {
		if _, err := SignTransfer(context.Background(), env, signer); err == nil {
			t.Fatalf("%s: SignTransfer accepted a bad signer", name)
		}
	}
	if _, err := SignTransfer(context.Background(), env, nil); err == nil {
		t.Fatal("SignTransfer accepted a nil signer")
	}
}

func TestClient_GetReceipt(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/receipts/missing-tx-00000001" {
			http.Error(w, "receipt not found", http.StatusNotFound)
			return
		}
		if r.URL.Path != "/api/v1/receipts/sdk_go_1_0000000000000000" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"tx_id":"sdk_go_1_0000000000000000","block_height":900001,"status":1,` +
			`"gas_used":0,"fee":0.01,"logs":[{"topic":"TxApplied","data":{"amount":2.5},"index":0}],` +
			`"timestamp":"2026-11-03T00:00:10Z","index_in_block":1}`))
	})
	defer srv.Close()
	receipt, err := client.GetReceipt(context.Background(), "sdk_go_1_0000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Succeeded() || receipt.BlockHeight != 900001 || receipt.Logs[0].Topic != "TxApplied" {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
	if _, err := client.GetReceipt(context.Background(), "missing-tx-00000001"); !IsNotFound(err) {
		t.Fatalf("err = %v, want IsNotFound", err)
	}
}

func TestClient_GetReceipts(t *testing.T) {
	srv, client := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RequestURI() != "/api/v1/receipts?from=10&limit=5&to=20" {
			t.Errorf("request = %s", r.URL.RequestURI())
		}
		_, _ = w.Write([]byte(`{"tip":30,"from":10,"to":20,"limit":5,"receipts":[{"tx_id":"a","status":1}]}`))
	})
	defer srv.Close()
	got, err := client.GetReceipts(context.Background(), ReceiptsQuery{From: 10, To: 20, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tip != 30 || len(got.Receipts) != 1 || !got.Receipts[0].Succeeded() {
		t.Fatalf("unexpected response: %+v", got)
	}
}
