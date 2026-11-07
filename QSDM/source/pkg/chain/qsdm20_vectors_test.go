package chain

// The QSDM-20 test vectors that the Go and JavaScript SDKs and qsdmcli are
// checked against (sdk/go/testdata/qsdm20_vectors.json). This test computes
// every expected value with the node's own code and requires the committed
// file to match it byte for byte, so a client that passes the vectors signs
// exactly what a node verifies. After a deliberate change, regenerate with:
//
//	QSDM20_WRITE_VECTORS=1 go test -tags dilithium_circl -run TestQSDM20SDKVectors ./pkg/chain

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const qsdm20VectorsPath = "../../sdk/go/testdata/qsdm20_vectors.json"

type qsdm20VectorFile struct {
	Comment      []string              `json:"comment"`
	TokenIDs     []qsdm20TokenIDVector `json:"token_ids"`
	Transactions []qsdm20TxVector      `json:"transactions"`
	Invalid      []qsdm20InvalidVector `json:"invalid_payloads"`
}

type qsdm20TokenIDVector struct {
	Creator string `json:"creator"`
	Nonce   uint64 `json:"nonce,string"`
	ID      string `json:"id"`
}

type qsdm20TxVector struct {
	Name     string          `json:"name"`
	Sender   string          `json:"sender"`
	Nonce    uint64          `json:"nonce,string"`
	FeeDust  uint64          `json:"fee_dust,string"`
	Payload  json.RawMessage `json:"payload"`
	Preimage string          `json:"preimage"`
	Digest   string          `json:"digest"`
	TxID     string          `json:"tx_id"`
	Token    string          `json:"token"`
}

type qsdm20InvalidVector struct {
	Name    string          `json:"name"`
	Payload json.RawMessage `json:"payload"`
}

func qsdm20VectorHex(b byte) string { return strings.Repeat(hex.EncodeToString([]byte{b}), 32) }

// qsdm20VectorInputs lists the cases. Every valid case must pass the node's
// field rules and every invalid one must be refused (checked below), so the
// file can only describe what a node really does.
func qsdm20VectorInputs() (ids []qsdm20TokenIDVector, txs []qsdm20TxVector, invalid []qsdm20InvalidVector) {
	ab, cd, ef, oo, ff := qsdm20VectorHex(0xab), qsdm20VectorHex(0xcd), qsdm20VectorHex(0xef), qsdm20VectorHex(0x00), qsdm20VectorHex(0xff)
	const max = "115792089237316195423570985008687907853269984665640564039457584007913129639935"
	const overMax = "115792089237316195423570985008687907853269984665640564039457584007913129639936"
	ids = []qsdm20TokenIDVector{
		{Creator: ab, Nonce: 7},
		{Creator: oo, Nonce: 0},
		{Creator: ff, Nonce: 18446744073709551615},
		{Creator: cd, Nonce: 1000000007},
	}
	action := func(a QSDM20Action) json.RawMessage {
		raw, err := json.Marshal(a)
		if err != nil {
			panic(err)
		}
		return raw
	}
	d := QSDM20Decimals
	txs = []qsdm20TxVector{
		{Name: "spec transfer", Sender: ab, Nonce: 7, FeeDust: 1500,
			Payload: action(QSDM20Action{Action: QSDM20ActionTransfer, Token: cd, To: ef, Amount: "1000"})},
		{Name: "spec create", Sender: ab, Nonce: 0, FeeDust: 0,
			Payload: action(QSDM20Action{Action: QSDM20ActionCreate, Symbol: "GROOT", Name: "Groot Coin", Decimals: d(8),
				MaxSupply: "1000000", InitialSupply: "250", Mint: QSDM20MintIssuer})},
		{Name: "create, open mint with a limit, 0 decimals, nothing up front", Sender: cd, Nonce: 1, FeeDust: 1,
			Payload: action(QSDM20Action{Action: QSDM20ActionCreate, Symbol: "FAIR1", Name: "Fair launch", Decimals: d(0),
				MaxSupply: "21000000", InitialSupply: "0", Mint: QSDM20MintOpen, MintLimit: "1000"})},
		{Name: "create, fixed supply of 2^256 - 1, 18 decimals, a name that needs escaping", Sender: ef, Nonce: 42, FeeDust: 100000000,
			Payload: action(QSDM20Action{Action: QSDM20ActionCreate, Symbol: "ABCDEFGHIJ12", Name: "Ünïcødé <Tøken> & \"Co\" \\ 🚀\u2028end", Decimals: d(18),
				MaxSupply: max, InitialSupply: max, Mint: QSDM20MintNone})},
		{Name: "create, a name of exactly 48 characters", Sender: ff, Nonce: 3, FeeDust: 250,
			Payload: action(QSDM20Action{Action: QSDM20ActionCreate, Symbol: "A", Name: "🚀" + strings.Repeat("a", 47), Decimals: d(2),
				MaxSupply: "5", InitialSupply: "5", Mint: QSDM20MintNone})},
		{Name: "mint at a nonce above 2^32", Sender: ab, Nonce: 12345678901, FeeDust: 99999999,
			Payload: action(QSDM20Action{Action: QSDM20ActionMint, Token: cd, To: ab, Amount: "1"})},
		{Name: "burn", Sender: ef, Nonce: 9, FeeDust: 10,
			Payload: action(QSDM20Action{Action: QSDM20ActionBurn, Token: ab, Amount: max})},
		{Name: "renounce", Sender: cd, Nonce: 18446744073709551615, FeeDust: 0,
			Payload: action(QSDM20Action{Action: QSDM20ActionRenounce, Token: ef})},
	}
	invalid = []qsdm20InvalidVector{
		{Name: "unknown action", Payload: json.RawMessage(`{"action":"approve","token":"` + cd + `","to":"` + ef + `","amount":"1"}`)},
		{Name: "lowercase symbol", Payload: json.RawMessage(`{"action":"create","symbol":"groot","name":"G","decimals":8,"max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "symbol of 13 characters", Payload: json.RawMessage(`{"action":"create","symbol":"ABCDEFGHIJ123","name":"G","decimals":8,"max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "empty name", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"","decimals":8,"max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "name of 49 characters", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"🚀` + strings.Repeat("a", 48) + `","decimals":8,"max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "name with a control character", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"a\u0007b","decimals":8,"max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "name with a C1 control character", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"a\u0085b","decimals":8,"max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "19 decimals", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":19,"max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "no decimals", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "decimals as a string", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":"8","max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "fractional decimals", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":1.5,"max_supply":"1","initial_supply":"1","mint":"none"}`)},
		{Name: "max_supply 0", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"0","initial_supply":"0","mint":"issuer"}`)},
		{Name: "max_supply with a leading zero", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"01","initial_supply":"0","mint":"issuer"}`)},
		{Name: "max_supply of 2^256", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"` + overMax + `","initial_supply":"0","mint":"issuer"}`)},
		{Name: "max_supply of 79 digits", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"1` + strings.Repeat("0", 78) + `","initial_supply":"0","mint":"issuer"}`)},
		{Name: "max_supply in exponent form", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"1e5","initial_supply":"0","mint":"issuer"}`)},
		{Name: "max_supply as a number", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":100,"initial_supply":"0","mint":"issuer"}`)},
		{Name: "initial_supply over max_supply", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"10","initial_supply":"11","mint":"issuer"}`)},
		{Name: "mint none with initial_supply below max_supply", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"10","initial_supply":"9","mint":"none"}`)},
		{Name: "mint issuer with a mint_limit", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"10","initial_supply":"0","mint":"issuer","mint_limit":"1"}`)},
		{Name: "mint open without a mint_limit", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"10","initial_supply":"0","mint":"open"}`)},
		{Name: "mint open with mint_limit 0", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"10","initial_supply":"0","mint":"open","mint_limit":"0"}`)},
		{Name: "unknown mint mode", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"10","initial_supply":"10","mint":"fixed"}`)},
		{Name: "create with a token field", Payload: json.RawMessage(`{"action":"create","symbol":"G","name":"G","decimals":0,"max_supply":"10","initial_supply":"10","mint":"none","token":"` + cd + `"}`)},
		{Name: "transfer to an uppercase address", Payload: json.RawMessage(`{"action":"transfer","token":"` + cd + `","to":"` + strings.ToUpper(ef) + `","amount":"1"}`)},
		{Name: "transfer of an uppercase token id", Payload: json.RawMessage(`{"action":"transfer","token":"` + strings.ToUpper(cd) + `","to":"` + ef + `","amount":"1"}`)},
		{Name: "transfer to a 63-character address", Payload: json.RawMessage(`{"action":"transfer","token":"` + cd + `","to":"` + ef[:63] + `","amount":"1"}`)},
		{Name: "transfer of 0", Payload: json.RawMessage(`{"action":"transfer","token":"` + cd + `","to":"` + ef + `","amount":"0"}`)},
		{Name: "transfer with a negative amount", Payload: json.RawMessage(`{"action":"transfer","token":"` + cd + `","to":"` + ef + `","amount":"-1"}`)},
		{Name: "transfer with a symbol", Payload: json.RawMessage(`{"action":"transfer","symbol":"G","token":"` + cd + `","to":"` + ef + `","amount":"1"}`)},
		{Name: "transfer with no to", Payload: json.RawMessage(`{"action":"transfer","token":"` + cd + `","amount":"1"}`)},
		{Name: "mint with no amount", Payload: json.RawMessage(`{"action":"mint","token":"` + cd + `","to":"` + ef + `"}`)},
		{Name: "burn with a to", Payload: json.RawMessage(`{"action":"burn","token":"` + cd + `","to":"` + ef + `","amount":"1"}`)},
		{Name: "renounce with an amount", Payload: json.RawMessage(`{"action":"renounce","token":"` + cd + `","amount":"1"}`)},
		{Name: "an unknown field", Payload: json.RawMessage(`{"action":"burn","token":"` + cd + `","amount":"1","memo":"x"}`)},
	}
	return ids, txs, invalid
}

// qsdm20RefusesPayload reports whether a node refuses raw as the payload of a
// submit body: the API decodes it into QSDM20Action with unknown fields
// refused, then applies the field rules.
func qsdm20RefusesPayload(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a QSDM20Action
	if err := dec.Decode(&a); err != nil {
		return err
	}
	if err := ValidateQSDM20Action(a); err != nil {
		return err
	}
	return nil
}

func buildQSDM20Vectors(t *testing.T) []byte {
	t.Helper()
	ids, txs, invalid := qsdm20VectorInputs()
	for i := range ids {
		ids[i].ID = QSDM20TokenID(ids[i].Creator, ids[i].Nonce)
	}
	for i := range txs {
		v := &txs[i]
		a, err := DecodeQSDM20Payload(v.Payload)
		if err != nil {
			t.Fatalf("vector %q: the node refuses its payload: %v", v.Name, err)
		}
		v.Preimage = qsdm20SigningPreimage(v.Sender, v.Nonce, v.FeeDust, a)
		digest := QSDM20SigningDigest(v.Sender, v.Nonce, v.FeeDust, a)
		v.Digest = hex.EncodeToString(digest)
		v.TxID = QSDM20TxID(digest)
		v.Token = a.Token
		if a.Action == QSDM20ActionCreate {
			v.Token = QSDM20TokenID(v.Sender, v.Nonce)
		}
		if _, err := QSDM20FeeCell(v.FeeDust); err != nil {
			t.Fatalf("vector %q: fee %d dust has no CELL value: %v", v.Name, v.FeeDust, err)
		}
	}
	for _, v := range invalid {
		if err := qsdm20RefusesPayload(v.Payload); err == nil {
			t.Fatalf("invalid vector %q: the node accepts %s", v.Name, v.Payload)
		}
	}
	file := qsdm20VectorFile{
		Comment: []string{
			"QSDM-20 test vectors (QSDM/docs/docs/QSDM_20.md), generated by pkg/chain's TestQSDM20SDKVectors from the node's own code.",
			"token_ids: id = hex SHA-256 of T(domain, qsdm/token/v1/id) T(creator) T(nonce).",
			"transactions: payload is the action a client sends in POST /api/v1/qsdm20/submit; preimage is the length-prefixed encoding the signature covers; digest is its SHA-256, the 32 bytes an ML-DSA-87 signer signs; tx_id is qsdm20- plus that digest; token is the token acted on, or the id a create gives the new token.",
			"invalid_payloads: actions a node refuses at submit; a client should refuse them before signing.",
			"nonce and fee_dust are written as decimal strings here so that every language reads 64-bit values exactly; the submit API takes them as JSON numbers.",
		},
		TokenIDs:     ids,
		Transactions: txs,
		Invalid:      invalid,
	}
	out, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func TestQSDM20SDKVectors(t *testing.T) {
	want := buildQSDM20Vectors(t)
	if os.Getenv("QSDM20_WRITE_VECTORS") == "1" {
		if err := os.WriteFile(qsdm20VectorsPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (%d bytes)", qsdm20VectorsPath, len(want))
		return
	}
	got, err := os.ReadFile(qsdm20VectorsPath)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with QSDM20_WRITE_VECTORS=1)", qsdm20VectorsPath, err)
	}
	got = bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
	if bytes.Equal(got, want) {
		return
	}
	gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("%s differs from the node's values at line %d:\n file: %s\n node: %s\n(after a deliberate change, regenerate with QSDM20_WRITE_VECTORS=1)",
				qsdm20VectorsPath, i+1, g, w)
		}
	}
}

// The spec's own examples are in the file, so a reader of QSDM_20.md and a
// reader of the vectors see the same numbers.
func TestQSDM20SDKVectorsCarryTheSpecExamples(t *testing.T) {
	var file qsdm20VectorFile
	if err := json.Unmarshal(buildQSDM20Vectors(t), &file); err != nil {
		t.Fatal(err)
	}
	if file.TokenIDs[0].ID != "78d65edcdf5af01c85401a31e3ffd6a4e19e90c16a62f3a222650ef8497b768a" {
		t.Fatalf("token id example = %s", file.TokenIDs[0].ID)
	}
	if file.Transactions[0].Digest != "a38b5c1cba8af0190de2c87489608e2a2977d227626ef91117a1a1797db5b648" {
		t.Fatalf("transfer example digest = %s", file.Transactions[0].Digest)
	}
	if file.Transactions[1].Digest != "d8edcbaca25b19b26a51c2c40fd51c0712d68b5ca5422289476334015870811d" {
		t.Fatalf("create example digest = %s", file.Transactions[1].Digest)
	}
}
