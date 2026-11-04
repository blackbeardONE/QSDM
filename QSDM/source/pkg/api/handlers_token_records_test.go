package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
)

// The token routes store a JSON record through StoreTransaction, which reads
// "id", "sender", "recipient" and "amount" from it and applies them to the
// balances table. These tests send request fields that contain quotes and
// check that every one of them stays inside its own string: the stored record
// has exactly the documented keys, with the request's values unchanged.

func tokenRecordHandlers(t *testing.T) (*Handlers, *mockStorage) {
	t.Helper()
	authManager, err := NewAuthManager()
	if err != nil {
		t.Fatalf("NewAuthManager: %v", err)
	}
	ms := newMockStorage()
	h := NewHandlers(authManager, NewUserStore(), nil, ms, logging.NewLogger("test.log", false),
		"", false, 0, "", "", false, 0, false, nil)
	return h, ms
}

func storedRecord(t *testing.T, ms *mockStorage) map[string]interface{} {
	t.Helper()
	raw, ok := ms.transactions["test"]
	if !ok {
		t.Fatalf("nothing was stored under the record key; stored keys: %v", storedKeys(ms))
	}
	var rec map[string]interface{}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("stored record is not JSON: %v\n%s", err, raw)
	}
	return rec
}

func storedKeys(ms *mockStorage) []string {
	keys := make([]string, 0, len(ms.transactions))
	for k := range ms.transactions {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func recordKeys(rec map[string]interface{}) string {
	keys := make([]string, 0, len(rec))
	for k := range rec {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func TestMintTokenRecordKeepsQuotedFieldsInside(t *testing.T) {
	cases := []struct {
		name      string
		symbol    string
		recipient string
	}{
		{
			name:      "symbol adds sender, amount and recipient",
			symbol:    `JOLLY","sender":"victim","amount":1000000,"recipient":"attacker`,
			recipient: "alice",
		},
		{
			name:      "recipient adds sender and id",
			symbol:    "JOLLY",
			recipient: `alice","sender":"victim","id":"forged`,
		},
		{
			name:      "backslashes and control characters",
			symbol:    "JO\\LLY\n\t\"",
			recipient: "al\\ice\u0000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, ms := tokenRecordHandlers(t)
			body, _ := json.Marshal(MintTokenRequest{TokenSymbol: tc.symbol, Recipient: tc.recipient, Amount: 5})
			w := httptest.NewRecorder()
			h.MintToken(w, httptest.NewRequest(http.MethodPost, "/api/v1/tokens/mint", bytes.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", w.Code, w.Body.String())
			}
			rec := storedRecord(t, ms)
			if got := recordKeys(rec); got != "amount,recipient,token,tx_id,type" {
				t.Fatalf("stored keys %q, want amount,recipient,token,tx_id,type; record %v", got, rec)
			}
			if rec["type"] != "mint" || rec["token"] != tc.symbol || rec["recipient"] != tc.recipient || rec["amount"] != float64(5) {
				t.Fatalf("stored record %v does not carry the request unchanged", rec)
			}
			if txID, _ := rec["tx_id"].(string); !strings.HasPrefix(txID, "mint_"+tc.symbol+"_") {
				t.Fatalf("tx_id %q does not start with mint_<symbol>_", txID)
			}
		})
	}
}

func TestCreateTokenRecordKeepsQuotedFieldsInside(t *testing.T) {
	h, ms := tokenRecordHandlers(t)
	req := CreateTokenRequest{
		Name:        `Jolly","sender":"victim","amount":1000000,"recipient":"attacker`,
		Symbol:      `JOLLY","id":"forged`,
		Decimals:    6,
		TotalSupply: 1000,
		Description: `a "quoted" description","sender":"victim`,
	}
	body, _ := json.Marshal(req)
	w := httptest.NewRecorder()
	h.CreateToken(w, httptest.NewRequest(http.MethodPost, "/api/v1/tokens/create", bytes.NewReader(body)))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d, want 201: %s", w.Code, w.Body.String())
	}
	rec := storedRecord(t, ms)
	if got := recordKeys(rec); got != "decimals,description,name,symbol,token_id,total_supply,type" {
		t.Fatalf("stored keys %q, want decimals,description,name,symbol,token_id,total_supply,type; record %v", got, rec)
	}
	if rec["type"] != "token_creation" || rec["name"] != req.Name || rec["symbol"] != req.Symbol ||
		rec["description"] != req.Description || rec["decimals"] != float64(6) || rec["total_supply"] != float64(1000) {
		t.Fatalf("stored record %v does not carry the request unchanged", rec)
	}
}
