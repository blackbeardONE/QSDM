package chain

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Fuzz targets for the QSDM-20 payload decoder and transaction verifier,
// which gate a replicated token state on every node.

// qsdm20RefMaxUnits is 2^256 - 1, computed apart from the code under test.
var qsdm20RefMaxUnits = new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), big.NewInt(1))

// qsdm20RefUnits is a reference parser for the amount rule: digits only, no
// leading zero, 0 (when allowed) or 1 .. 2^256 - 1.
func qsdm20RefUnits(s string, allowZero bool) (*big.Int, bool) {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" || (len(s) > 1 && s[0] == '0') {
		return nil, false
	}
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v.Cmp(qsdm20RefMaxUnits) > 0 || (v.Sign() == 0 && !allowZero) {
		return nil, false
	}
	return v, true
}

func qsdm20RefHex64(s string) bool {
	return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == ""
}

// checkAcceptedQSDM20Action asserts the documented field rules for an action
// the decoder accepted, without the decoder's own helpers.
func checkAcceptedQSDM20Action(t *testing.T, a QSDM20Action) {
	t.Helper()
	fail := func(rule string) {
		t.Helper()
		t.Fatalf("accepted QSDM-20 action breaks the %s rule: %+v", rule, a)
	}
	createFields := a.Symbol != "" || a.Name != "" || a.Decimals != nil || a.MaxSupply != "" ||
		a.InitialSupply != "" || a.Mint != "" || a.MintLimit != ""
	switch a.Action {
	case QSDM20ActionCreate:
		if a.Token != "" || a.To != "" || a.Amount != "" {
			fail("create field set")
		}
		if len(a.Symbol) < 1 || len(a.Symbol) > 12 || strings.Trim(a.Symbol, "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789") != "" {
			fail("symbol")
		}
		if !utf8.ValidString(a.Name) || utf8.RuneCountInString(a.Name) < 1 || utf8.RuneCountInString(a.Name) > 48 {
			fail("name length")
		}
		for _, r := range a.Name {
			if unicode.Is(unicode.Cc, r) {
				fail("name control character")
			}
		}
		if a.Decimals == nil || *a.Decimals > 18 {
			fail("decimals")
		}
		maxSupply, ok := qsdm20RefUnits(a.MaxSupply, false)
		if !ok {
			fail("max_supply")
		}
		initial, ok := qsdm20RefUnits(a.InitialSupply, true)
		if !ok || initial.Cmp(maxSupply) > 0 {
			fail("initial_supply")
		}
		switch a.Mint {
		case QSDM20MintNone:
			if a.MintLimit != "" || initial.Cmp(maxSupply) != 0 {
				fail("mint none")
			}
		case QSDM20MintIssuer:
			if a.MintLimit != "" {
				fail("mint issuer")
			}
		case QSDM20MintOpen:
			if _, ok := qsdm20RefUnits(a.MintLimit, false); !ok {
				fail("mint open")
			}
		default:
			fail("mint mode")
		}
	case QSDM20ActionTransfer, QSDM20ActionMint, QSDM20ActionBurn, QSDM20ActionRenounce:
		if createFields {
			fail("non-create field set")
		}
		if !qsdm20RefHex64(a.Token) {
			fail("token id")
		}
		needsTo := a.Action == QSDM20ActionTransfer || a.Action == QSDM20ActionMint
		if needsTo != (a.To != "") || (needsTo && !qsdm20RefHex64(a.To)) {
			fail("to")
		}
		needsAmount := a.Action != QSDM20ActionRenounce
		if needsAmount != (a.Amount != "") {
			fail("amount presence")
		}
		if needsAmount {
			if _, ok := qsdm20RefUnits(a.Amount, false); !ok {
				fail("amount")
			}
		}
	default:
		fail("action set")
	}
}

func qsdm20FuzzSeedActions() []QSDM20Action {
	return []QSDM20Action{
		qsdm20Create("GROOT", "Groot Coin", 8, "1000000", "1000000", QSDM20MintNone, ""),
		qsdm20Create("FAIR21", "Fair launch é <&>", 0, "21000000", "0", QSDM20MintOpen, "1000"),
		qsdm20Create("ISS", "Issuer", 18, qsdm20MaxUnitsString, "0", QSDM20MintIssuer, ""),
		qsdm20Transfer(qsdm20HexA, qsdm20HexB, "1"),
		qsdm20MintAction(qsdm20HexA, qsdm20HexC, qsdm20MaxUnitsString),
		qsdm20Burn(qsdm20HexB, "77"),
		qsdm20Renounce(qsdm20HexC),
	}
}

func FuzzDecodeQSDM20Payload(f *testing.F) {
	for _, a := range qsdm20FuzzSeedActions() {
		raw, err := json.Marshal(a)
		if err != nil {
			f.Fatal(err)
		}
		if _, err := DecodeQSDM20Payload(raw); err != nil {
			f.Fatalf("genuine seed %s rejected: %v", raw, err)
		}
		f.Add(raw)
		f.Add(append(bytes.Clone(raw), ' '))
		f.Add(bytes.Replace(raw, []byte(`":"`), []byte(`": "`), 1))
	}
	for _, s := range []string{
		``, `null`, `{}`, `[]`, `{"action":"create"}`,
		`{"action":"transfer","token":"` + qsdm20HexA + `","to":"` + qsdm20HexB + `","amount":"01"}`,
		`{"action":"transfer","token":"` + qsdm20HexA + `","to":"` + qsdm20HexB + `","amount":"` + qsdm20TooManyUnits + `"}`,
		`{"action":"create","symbol":"A","name":"\u0007","decimals":0,"max_supply":"1","initial_supply":"1","mint":"none"}`,
		`{"action":"create","symbol":"A","name":"x","decimals":19,"max_supply":"1","initial_supply":"1","mint":"none"}`,
		`{"action":"create","symbol":"A","name":"x","decimals":0,"max_supply":"2","initial_supply":"1","mint":"none"}`,
		`{"action":"burn","token":"` + qsdm20HexA + `","to":"` + qsdm20HexB + `","amount":"1"}`,
		`{"Action":"renounce","token":"` + qsdm20HexA + `"}`,
		`{"action":"renounce","token":"` + qsdm20HexA + `","token":"` + qsdm20HexB + `"}`,
	} {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, payload []byte) {
		orig := bytes.Clone(payload)
		a, err := DecodeQSDM20Payload(payload)
		if !bytes.Equal(payload, orig) {
			t.Fatal("DecodeQSDM20Payload modified its input")
		}
		if err != nil {
			if !errors.Is(err, ErrQSDM20Field) {
				t.Fatalf("refusal is not ErrQSDM20Field: %v", err)
			}
			return
		}
		canonical, err := json.Marshal(a)
		if err != nil || !bytes.Equal(canonical, payload) {
			t.Fatalf("accepted a non-canonical payload %q (canonical %q, %v)", payload, canonical, err)
		}
		checkAcceptedQSDM20Action(t, a)
		again, err := DecodeQSDM20Payload(canonical)
		if err != nil || !reflect.DeepEqual(again, a) {
			t.Fatalf("an accepted payload does not decode the same way twice: %v", err)
		}
	})
}

// FuzzVerifyQSDM20Tx builds a token transaction from its fields, signs it
// with a fixed key and checks the verifier: it accepts exactly the actions
// the field rules allow, and only signatures that cover the transaction.
func FuzzVerifyQSDM20Tx(f *testing.F) {
	key := newFuzzKey(20)
	add := func(a QSDM20Action, nonce, feeDust uint64, sender string) {
		decimals := int16(-1)
		if a.Decimals != nil {
			decimals = int16(*a.Decimals)
		}
		f.Add(a.Action, a.Symbol, a.Name, decimals, a.MaxSupply, a.InitialSupply, a.Mint, a.MintLimit,
			a.Token, a.To, a.Amount, nonce, feeDust, sender)
	}
	for i, a := range qsdm20FuzzSeedActions() {
		add(a, uint64(i), uint64(i)*1000, "")
	}
	add(qsdm20Transfer(qsdm20HexA, qsdm20HexB, "5"), 0, 0, qsdm20HexC)         // not the signing key
	add(qsdm20Transfer(qsdm20HexA, qsdm20HexB, "0"), 0, 0, "")                 // bad amount
	add(qsdm20Create("lower", "x", 0, "1", "1", QSDM20MintNone, ""), 0, 0, "") // bad symbol

	publicKey := hex.EncodeToString(key.pub)
	f.Fuzz(func(t *testing.T, action, symbol, name string, decimals int16, maxSupply, initial, mint, limit,
		token, to, amount string, nonce, feeDust uint64, sender string) {
		a := QSDM20Action{
			Action: action, Symbol: symbol, Name: name, MaxSupply: maxSupply, InitialSupply: initial,
			Mint: mint, MintLimit: limit, Token: token, To: to, Amount: amount,
		}
		if decimals >= 0 && decimals <= 255 {
			a.Decimals = QSDM20Decimals(uint8(decimals))
		}
		// The action as it arrives: through a JSON round trip.
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		var wire QSDM20Action
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		if sender == "" {
			sender = key.address
		}
		fee, feeErr := QSDM20FeeCell(feeDust)
		if feeErr != nil {
			return
		}
		digest := QSDM20SigningDigest(sender, nonce, feeDust, wire)
		tx := &mempool.Tx{
			ID: QSDM20TxID(digest), Sender: sender, Fee: fee, Nonce: nonce, Payload: payload,
			ContractID: QSDM20ContractID, PublicKey: publicKey, Signature: hex.EncodeToString(key.mustSign(t, digest)),
		}
		orig := cloneTx(tx)
		got, err := VerifyQSDM20Tx(tx)
		if !reflect.DeepEqual(tx, orig) {
			t.Fatal("VerifyQSDM20Tx modified its input")
		}
		shapeErr := ValidateQSDM20Action(wire)
		switch {
		case shapeErr != nil:
			if !errors.Is(err, ErrQSDM20Field) {
				t.Fatalf("malformed action (%v) gave %v", shapeErr, err)
			}
			return
		case sender != key.address:
			if err == nil {
				t.Fatalf("accepted sender %q, which is not the signing key", sender)
			}
			return
		case err != nil:
			t.Fatalf("rejected a well-formed action signed by its sender: %v", err)
		}
		if !reflect.DeepEqual(got, wire) {
			t.Fatal("verified action differs from the signed one")
		}
		checkAcceptedQSDM20Action(t, got)
		if !fuzzVerifyMLDSA(key.pub, digest, mustHex(t, tx.Signature)) {
			t.Fatal("accepted a signature the reference ML-DSA-87 verifier rejects")
		}
		if tx.ID != QSDM20TxIDPrefix+hex.EncodeToString(digest) {
			t.Fatal("tx id is not bound to the digest")
		}
		// The signature does not carry over to another nonce or fee, even
		// with the id fixed up.
		for _, change := range []func(*mempool.Tx){
			func(m *mempool.Tx) {
				m.Nonce++
				m.ID = QSDM20TxID(QSDM20SigningDigest(sender, m.Nonce, feeDust, wire))
			},
			func(m *mempool.Tx) {
				fee2, err := QSDM20FeeCell(feeDust + 1)
				if err != nil {
					m.Fee = -1
					return
				}
				m.Fee = fee2
				m.ID = QSDM20TxID(QSDM20SigningDigest(sender, nonce, feeDust+1, wire))
			},
		} {
			moved := cloneTx(tx)
			change(moved)
			if _, err := VerifyQSDM20Tx(moved); err == nil {
				t.Fatal("a signature verified for a different nonce or fee")
			}
		}
	})
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
