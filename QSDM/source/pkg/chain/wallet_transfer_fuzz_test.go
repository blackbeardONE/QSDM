package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// FuzzApplyWalletTransferTx drives the consensus check every validator runs
// on a signed wallet transfer: the producer when it seals a transfer that
// POST /api/v1/wallet/submit-signed queued, and every follower when it
// replays the block. In "from envelope" mode the transaction is built from
// the decoded envelope the way the API builds it; otherwise the payload and
// the transaction fields come from the input separately, as they may in a
// block. In "harness signs" mode the harness signs the decoded envelope with
// a funded key, so the fuzzer reaches the balance update.

// walletTransferFromEnvelope mirrors pkg/api walletTransferMempoolTx.
func walletTransferFromEnvelope(env wallet.TransactionData) *mempool.Tx {
	payload, _ := json.Marshal(env)
	return &mempool.Tx{
		ID: env.ID, Sender: env.Sender, Recipient: env.Recipient, Amount: env.Amount, Fee: env.Fee,
		Nonce: env.Nonce - 1, Payload: payload, ContractID: WalletTransferContractID,
		Signature: env.Signature, PublicKey: env.PublicKey,
	}
}

func signWalletEnvelope(k fuzzKey, env wallet.TransactionData) wallet.TransactionData {
	env.Sender, env.PublicKey = k.address, hex.EncodeToString(k.pub)
	canonical, err := env.CanonicalBytes()
	if err != nil {
		panic(err)
	}
	sig, err := k.Sign(canonical)
	if err != nil {
		panic(err)
	}
	env.Signature = hex.EncodeToString(sig)
	return env
}

func FuzzApplyWalletTransferTx(f *testing.F) {
	payer := newFuzzKey(0x57)
	const funded = 1000.0
	recipient := hex.EncodeToString(bytes.Repeat([]byte{0xab}, 32))
	newStore := func(tb testing.TB, dust bool) *AccountStore {
		as := NewAccountStore()
		as.Credit(payer.address, funded)
		if dust {
			as.SetHeightFn(func() uint64 { return 1 })
			if _, err := as.MigrateToDust(); err != nil {
				tb.Fatal(err)
			}
		}
		return as
	}
	genuine := signWalletEnvelope(payer, wallet.TransactionData{
		ID: "a3f1c0de9b8e7d6c5b4a39281706f5e4", Recipient: recipient, Amount: 12.5, Fee: 0.01,
		GeoTag: "PH", ParentCells: []string{"solo-heartbeat-7-1791611907090994535", "hive_wallet_1784876606130_1a4983ed8ce85b54"},
		Nonce: 1, Timestamp: "2026-08-01T12:00:00Z",
	})
	if err := ApplyWalletTransferTx(newStore(f, false), walletTransferFromEnvelope(genuine)); err != nil {
		f.Fatalf("genuine seed rejected: %v", err)
	}
	add := func(env wallet.TransactionData, fromEnvelope, signs, dust bool) {
		tx := walletTransferFromEnvelope(env)
		f.Add(tx.Payload, tx.ID, tx.Sender, tx.Recipient, tx.Amount, tx.Fee, tx.Nonce, tx.Signature, tx.PublicKey,
			tx.ContractID, fromEnvelope, signs, dust)
	}
	add(genuine, true, false, false)
	add(genuine, false, false, false)
	add(genuine, true, false, true)
	self := genuine
	self.Recipient = payer.address
	add(self, true, true, false)
	overdraw := genuine
	overdraw.Amount = funded
	add(overdraw, true, true, true)
	legacy := genuine
	legacy.Nonce = 0
	add(legacy, true, true, false)
	tamper := genuine
	tamper.Amount = 13
	add(tamper, true, false, false)
	f.Add([]byte(`{"id":"x","sender":"s","recipient":"r","amount":1,"nonce":1,"extra":true}`), "x", "s", "r", 1.0, 0.0,
		uint64(0), "", "", WalletTransferContractID, false, true, false)
	f.Add([]byte(`{"id":"x"} {"id":"y"}`), "x", "", "", 0.0, 0.0, uint64(0), "", "", WalletTransferContractID, false, false, false)

	f.Fuzz(func(t *testing.T, payload []byte, id, sender, recipient string, amount, fee float64, nonce uint64,
		signature, publicKey, contractID string, fromEnvelope, harnessSigns, dust bool) {
		tx := &mempool.Tx{ID: id, Sender: sender, Recipient: recipient, Amount: amount, Fee: fee, Nonce: nonce,
			Payload: payload, ContractID: contractID, Signature: signature, PublicKey: publicKey}
		if fromEnvelope || harnessSigns {
			var env wallet.TransactionData
			if json.Unmarshal(payload, &env) != nil {
				return
			}
			if harnessSigns {
				env = signWalletEnvelope(payer, env)
			}
			if fromEnvelope {
				tx = walletTransferFromEnvelope(env)
			} else {
				tx.Payload, _ = json.Marshal(env)
			}
		}
		if dust {
			prev := ForkDustHeight()
			SetForkDustHeight(0)
			defer SetForkDustHeight(prev)
		}
		as := newStore(t, dust)
		rootBefore := as.StateRoot()
		payerBefore, _ := as.Get(payer.address)
		recipientBefore, _ := as.Get(tx.Recipient)
		orig := cloneTx(tx)

		err := ApplyWalletTransferTx(as, tx)
		if !reflect.DeepEqual(tx, orig) {
			t.Fatal("ApplyWalletTransferTx modified the transaction")
		}
		if err != nil {
			if as.StateRoot() != rootBefore {
				t.Fatalf("a refused transfer changed the account state: %v", err)
			}
			return
		}

		var env wallet.TransactionData
		dec := json.NewDecoder(bytes.NewReader(tx.Payload))
		dec.DisallowUnknownFields()
		var extra json.RawMessage
		if dec.Decode(&env) != nil || dec.Decode(&extra) == nil {
			t.Fatalf("applied a payload that is not exactly one strict envelope: %q", tx.Payload)
		}
		if tx.ContractID != WalletTransferContractID || env.ID != tx.ID || env.Sender != tx.Sender ||
			env.Recipient != tx.Recipient || env.Amount != tx.Amount || env.Fee != tx.Fee || env.Nonce != tx.Nonce+1 ||
			env.Signature != tx.Signature || env.PublicKey != tx.PublicKey {
			t.Fatalf("applied a transaction whose fields differ from its signed envelope: %+v vs %+v", tx, env)
		}
		pub, perr := hex.DecodeString(env.PublicKey)
		sum := sha256.Sum256(pub)
		if perr != nil || env.Sender != hex.EncodeToString(sum[:]) || env.ID == "" || env.Recipient == "" ||
			env.Nonce == 0 || !(env.Amount > 0) || env.Fee < 0 {
			t.Fatalf("applied envelope %+v", env)
		}
		unsigned := env
		unsigned.Signature, unsigned.PublicKey = "", ""
		msg, _ := json.Marshal(unsigned)
		sig, serr := hex.DecodeString(env.Signature)
		if serr != nil || !fuzzVerifyMLDSA(pub, msg, sig) {
			t.Fatal("applied a transfer whose signature the reference ML-DSA-87 verifier rejects")
		}
		if env.Sender != payer.address {
			t.Fatalf("applied a transfer from unfunded sender %s", env.Sender)
		}
		payerAfter, _ := as.Get(payer.address)
		recipientAfter, _ := as.Get(env.Recipient)
		if payerAfter.Nonce != payerBefore.Nonce+1 {
			t.Fatalf("payer nonce %d -> %d", payerBefore.Nonce, payerAfter.Nonce)
		}
		if dust {
			amountDust, feeDust := floorToDust(env.Amount), floorToDust(env.Fee)
			wantPayer := payerBefore.BalanceDust - amountDust - feeDust
			if env.Recipient == payer.address {
				wantPayer += amountDust
			} else if recipientBefore == nil && recipientAfter.BalanceDust != amountDust ||
				recipientBefore != nil && recipientAfter.BalanceDust != recipientBefore.BalanceDust+amountDust {
				t.Fatalf("recipient credited %d dust for an amount of %d dust", recipientAfter.BalanceDust, amountDust)
			}
			if amountDust+feeDust > payerBefore.BalanceDust || payerAfter.BalanceDust != wantPayer {
				t.Fatalf("payer %d -> %d dust for %d + %d", payerBefore.BalanceDust, payerAfter.BalanceDust, amountDust, feeDust)
			}
		} else {
			wantPayer := payerBefore.Balance - (env.Amount + env.Fee)
			if env.Recipient == payer.address {
				wantPayer += env.Amount
			} else if recipientBefore == nil && recipientAfter.Balance != env.Amount {
				t.Fatalf("recipient credited %v for an amount of %v", recipientAfter.Balance, env.Amount)
			}
			if env.Amount+env.Fee > payerBefore.Balance || payerAfter.Balance != wantPayer {
				t.Fatalf("payer %v -> %v for %v + %v", payerBefore.Balance, payerAfter.Balance, env.Amount, env.Fee)
			}
		}
		if ApplyWalletTransferTx(as, tx) == nil {
			t.Fatal("the same transfer applied twice")
		}
	})
}
