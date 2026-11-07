package qsdm

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
)

// qsdm20Vectors is testdata/qsdm20_vectors.json. The node's own test
// (pkg/chain TestQSDM20SDKVectors) computes every value in it with the
// node's code and fails when the file differs, so matching these vectors
// means signing exactly what a node verifies.
type qsdm20Vectors struct {
	TokenIDs []struct {
		Creator string `json:"creator"`
		Nonce   string `json:"nonce"`
		ID      string `json:"id"`
	} `json:"token_ids"`
	Transactions []struct {
		Name     string          `json:"name"`
		Sender   string          `json:"sender"`
		Nonce    string          `json:"nonce"`
		FeeDust  string          `json:"fee_dust"`
		Payload  json.RawMessage `json:"payload"`
		Preimage string          `json:"preimage"`
		Digest   string          `json:"digest"`
		TxID     string          `json:"tx_id"`
		Token    string          `json:"token"`
	} `json:"transactions"`
	Invalid []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
	} `json:"invalid_payloads"`
}

func loadQSDM20Vectors(t *testing.T) qsdm20Vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/qsdm20_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v qsdm20Vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.TokenIDs) == 0 || len(v.Transactions) == 0 || len(v.Invalid) == 0 {
		t.Fatal("the vector file is missing a section")
	}
	return v
}

func mustUint64(t *testing.T, s string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// decodeQSDM20Payload decodes a payload the way the node's submit route
// does: unknown fields refused.
func decodeQSDM20Payload(raw []byte) (QSDM20Action, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a QSDM20Action
	err := dec.Decode(&a)
	return a, err
}

func TestQSDM20_TokenIDsMatchNode(t *testing.T) {
	for _, v := range loadQSDM20Vectors(t).TokenIDs {
		if got := QSDM20TokenID(v.Creator, mustUint64(t, v.Nonce)); got != v.ID {
			t.Errorf("QSDM20TokenID(%s, %s) = %s, want %s", v.Creator[:8], v.Nonce, got, v.ID)
		}
	}
}

func TestQSDM20_SigningDigestsMatchNode(t *testing.T) {
	for _, v := range loadQSDM20Vectors(t).Transactions {
		t.Run(v.Name, func(t *testing.T) {
			a, err := decodeQSDM20Payload(v.Payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateQSDM20Action(a); err != nil {
				t.Fatalf("ValidateQSDM20Action refuses a payload the node accepts: %v", err)
			}
			// The payload re-encodes to exactly what the node signs and stores.
			if back, _ := json.Marshal(a); !bytes.Equal(back, compactJSON(t, v.Payload)) {
				t.Fatalf("payload re-encodes as %s", back)
			}
			tx := QSDM20Transaction{Sender: v.Sender, Nonce: mustUint64(t, v.Nonce), FeeDust: mustUint64(t, v.FeeDust), Payload: a}
			if got := QSDM20SigningPreimage(tx.Sender, tx.Nonce, tx.FeeDust, a); got != v.Preimage {
				t.Fatalf("preimage\n got %q\nwant %q", got, v.Preimage)
			}
			if got := hex.EncodeToString(tx.SigningDigest()); got != v.Digest {
				t.Fatalf("digest %s, want %s", got, v.Digest)
			}
			if got := tx.TxID(); got != v.TxID {
				t.Fatalf("tx id %s, want %s", got, v.TxID)
			}
			if got := tx.TokenID(); got != v.Token {
				t.Fatalf("token %s, want %s", got, v.Token)
			}
		})
	}
}

func compactJSON(t *testing.T, raw []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		t.Fatal(err)
	}
	// json.Compact keeps escapes as written; the file was written by
	// encoding/json, so it already escapes as Marshal does.
	return out.Bytes()
}

func TestQSDM20_ValidateRefusesWhatTheNodeRefuses(t *testing.T) {
	for _, v := range loadQSDM20Vectors(t).Invalid {
		a, err := decodeQSDM20Payload(v.Payload)
		if err != nil {
			continue // the node's decoder refuses it too
		}
		if err := ValidateQSDM20Action(a); err == nil {
			t.Errorf("%s: ValidateQSDM20Action accepts %s", v.Name, v.Payload)
		} else if !errors.Is(err, ErrQSDM20Field) {
			t.Errorf("%s: error %v does not wrap ErrQSDM20Field", v.Name, err)
		}
	}
}

const (
	qsdm20TestToken = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
)

func qsdm20TestTransfer() QSDM20Action {
	return QSDM20Action{Action: QSDM20ActionTransfer, Token: qsdm20TestToken, To: testPayee, Amount: "1000"}
}

func TestSignQSDM20_Checks(t *testing.T) {
	signer := newFakeSigner(7)
	sender := WalletAddress(signer.PublicKey())
	tx := QSDM20Transaction{Sender: sender, Nonce: 3, FeeDust: 1500, Payload: qsdm20TestTransfer()}

	signed, err := SignQSDM20(context.Background(), tx, signer)
	if err != nil {
		t.Fatal(err)
	}
	if !signed.IsSigned() || signed.PublicKey != hex.EncodeToString(signer.PublicKey()) {
		t.Fatalf("signed = %+v", signed)
	}
	if want := hex.EncodeToString(fakeSignature(signer.PublicKey(), tx.SigningDigest())); signed.Signature != want {
		t.Fatal("the signature does not cover SigningDigest")
	}

	if _, err := SignQSDM20(context.Background(), tx, nil); err == nil {
		t.Error("nil signer accepted")
	}
	other := tx
	other.Sender = testPayee
	if _, err := SignQSDM20(context.Background(), other, signer); err == nil || !strings.Contains(err.Error(), "not the signer's wallet address") {
		t.Errorf("foreign sender: %v", err)
	}
	bad := tx
	bad.Payload.Amount = "0"
	if _, err := SignQSDM20(context.Background(), bad, signer); !errors.Is(err, ErrQSDM20Field) {
		t.Errorf("invalid action: %v", err)
	}
	if _, err := SignQSDM20(context.Background(), tx, badSigner{pub: []byte{1}, sig: make([]byte, MLDSA87SignatureSize)}); err == nil {
		t.Error("short public key accepted")
	}
	pub := bytes.Repeat([]byte{9}, MLDSA87PublicKeySize)
	short := QSDM20Transaction{Sender: WalletAddress(pub), Payload: qsdm20TestTransfer()}
	if _, err := SignQSDM20(context.Background(), short, badSigner{pub: pub, sig: []byte{1}}); err == nil {
		t.Error("short signature accepted")
	}
}

// qsdm20SubmitBody mirrors pkg/api.QSDM20SubmitRequest in the node.
type qsdm20SubmitBody struct {
	Sender    string       `json:"sender"`
	Nonce     uint64       `json:"nonce"`
	FeeDust   uint64       `json:"fee_dust"`
	Payload   QSDM20Action `json:"payload"`
	PublicKey string       `json:"public_key"`
	Signature string       `json:"signature"`
}

func TestClient_SendQSDM20_RoundTrip(t *testing.T) {
	signer := newFakeSigner(5)
	sender := WalletAddress(signer.PublicKey())
	create := QSDM20Action{Action: QSDM20ActionCreate, Symbol: "GROOT", Name: "Groot <Coin> & co", Decimals: QSDM20Decimals(8),
		MaxSupply: "1000000", InitialSupply: "250", Mint: QSDM20MintIssuer}
	var submitted qsdm20SubmitBody
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/mining/account":
			if r.URL.Query().Get("address") != sender {
				t.Errorf("account address = %q", r.URL.Query().Get("address"))
			}
			_, _ = w.Write([]byte(`{"address":"` + sender + `","balance":12.5,"nonce":41,"present":true}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/qsdm20/submit":
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&submitted); err != nil {
				t.Errorf("submit body: %v", err)
			}
			digest := QSDM20SigningDigest(submitted.Sender, submitted.Nonce, submitted.FeeDust, submitted.Payload)
			pub, _ := hex.DecodeString(submitted.PublicKey)
			if hex.EncodeToString(fakeSignature(pub, digest)) != submitted.Signature {
				t.Error("the submitted signature does not cover the node's digest")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tx_id": QSDM20TxID(digest), "action": submitted.Payload.Action, "sender": submitted.Sender,
				"nonce": submitted.Nonce, "token": QSDM20TokenID(submitted.Sender, submitted.Nonce),
				"status": "accepted", "mempool_status": "submitted",
			})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	})
	defer srv.Close()

	resp, err := c.SendQSDM20(context.Background(), create, 2500, signer)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.Nonce != 41 || submitted.FeeDust != 2500 || submitted.Sender != sender || submitted.Payload.Name != create.Name {
		t.Fatalf("submitted %+v", submitted)
	}
	if resp.Token != QSDM20TokenID(sender, 41) || resp.MempoolStatus != "submitted" {
		t.Fatalf("response %+v", resp)
	}
}

func TestClient_PrepareQSDM20_Refusals(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"address":"x","balance":0,"nonce":0,"present":false}`))
	})
	defer srv.Close()
	ctx := context.Background()
	if _, err := c.PrepareQSDM20(ctx, testPayee, qsdm20TestTransfer(), 0); err == nil || !strings.Contains(err.Error(), "no CELL account") {
		t.Errorf("accountless sender: %v", err)
	}
	if _, err := c.PrepareQSDM20(ctx, "ABC", qsdm20TestTransfer(), 0); err == nil {
		t.Error("bad sender accepted")
	}
	bad := qsdm20TestTransfer()
	bad.To = strings.ToUpper(qsdm20TestToken) // a valid address, but in uppercase
	if _, err := c.PrepareQSDM20(ctx, testPayee, bad, 0); !errors.Is(err, ErrQSDM20Field) {
		t.Errorf("bad action: %v", err)
	}
}

func TestClient_SubmitQSDM20_Refusals(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"chain: QSDM-20 token transactions are not active"}`))
	})
	defer srv.Close()
	ctx := context.Background()
	if _, err := c.SubmitQSDM20(ctx, QSDM20Transaction{Payload: qsdm20TestTransfer()}); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Errorf("unsigned: %v", err)
	}
	signer := newFakeSigner(3)
	signed, err := SignQSDM20(ctx, QSDM20Transaction{Sender: WalletAddress(signer.PublicKey()), Payload: qsdm20TestTransfer()}, signer)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SubmitQSDM20(ctx, signed)
	var apiErr *ErrAPI
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(apiErr.Body, "not active") {
		t.Fatalf("refusal: %v", err)
	}
}

func TestClient_SubmitQSDM20_ChecksTheAnsweredID(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"tx_id":"qsdm20-00","status":"accepted"}`))
	})
	defer srv.Close()
	signer := newFakeSigner(4)
	signed, err := SignQSDM20(context.Background(), QSDM20Transaction{Sender: WalletAddress(signer.PublicKey()), Payload: qsdm20TestTransfer()}, signer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SubmitQSDM20(context.Background(), signed); err == nil || !strings.Contains(err.Error(), "answered tx_id") {
		t.Fatalf("mismatched id: %v", err)
	}
}

func TestClient_QSDM20Reads(t *testing.T) {
	srv, c := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/qsdm20/tokens":
			if got := r.URL.RawQuery; got != "cursor=5%3Aab&limit=2" {
				t.Errorf("tokens query = %q", got)
			}
			_, _ = w.Write([]byte(`{"runtime":"x","state_root":"r","tokens":[{"id":"` + qsdm20TestToken + `","symbol":"GROOT","name":"Groot","decimals":8,"issuer":"i","mint":"issuer","max_supply":"10","minted":"5","burned":"1","supply":"4","holders":2,"created_height":7}],"next_cursor":"7:` + qsdm20TestToken + `","has_more":true}`))
		case "/api/v1/qsdm20/tokens/" + qsdm20TestToken:
			_, _ = w.Write([]byte(`{"state_root":"r","token":{"id":"` + qsdm20TestToken + `","symbol":"GROOT","decimals":8,"supply":"4"}}`))
		case "/api/v1/qsdm20/tokens/" + qsdm20TestToken + "/holders":
			if r.URL.RawQuery != "" {
				t.Errorf("holders query = %q", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"state_root":"r","token":"` + qsdm20TestToken + `","holders":[{"address":"a","balance":"3"},{"address":"b","balance":"1"}],"has_more":false}`))
		case "/api/v1/qsdm20/balances/" + testPayee:
			_, _ = w.Write([]byte(`{"state_root":"r","address":"` + testPayee + `","balances":[{"token":"` + qsdm20TestToken + `","symbol":"GROOT","name":"Groot","decimals":8,"balance":"3"}]}`))
		default:
			http.NotFound(w, r)
		}
	})
	defer srv.Close()
	ctx := context.Background()

	page, err := c.GetQSDM20Tokens(ctx, "5:ab", 2)
	if err != nil || len(page.Tokens) != 1 || page.Tokens[0].Supply != "4" || !page.HasMore || page.NextCursor != "7:"+qsdm20TestToken {
		t.Fatalf("tokens: %+v %v", page, err)
	}
	info, err := c.GetQSDM20Token(ctx, qsdm20TestToken)
	if err != nil || info.Token.Symbol != "GROOT" {
		t.Fatalf("token: %+v %v", info, err)
	}
	holders, err := c.GetQSDM20Holders(ctx, qsdm20TestToken, "", 0)
	if err != nil || len(holders.Holders) != 2 || holders.Holders[0].Balance != "3" {
		t.Fatalf("holders: %+v %v", holders, err)
	}
	balances, err := c.GetQSDM20Balances(ctx, testPayee)
	if err != nil || len(balances.Balances) != 1 || balances.Balances[0].Balance != "3" {
		t.Fatalf("balances: %+v %v", balances, err)
	}
	if _, err := c.GetQSDM20Token(ctx, strings.Repeat("0", 64)); !IsNotFound(err) {
		t.Fatalf("unknown token: %v", err)
	}
}
