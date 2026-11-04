package chain

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// Fuzz targets for the CELL stream decoder and stateless verifier. Both run
// on every stream transaction a peer or API client submits, before any state
// is touched.

// checkAcceptedStreamAction asserts the documented rules (the StreamAction
// field comments) for an action the verifier accepted.
func checkAcceptedStreamAction(t *testing.T, a StreamAction) {
	t.Helper()
	fail := func(rule string) {
		t.Helper()
		t.Fatalf("accepted stream action breaks the %s rule: %+v", rule, a)
	}
	if !fuzzIdentifier(a.ID, 128) || !fuzzIdentifier(a.StreamID, 128) {
		fail("identifier")
	}
	if !fuzzLowerHex(a.Sender, sha256.Size) {
		fail("sender address")
	}
	at, ok := fuzzTime(a.Timestamp)
	if !ok {
		fail("timestamp")
	}
	openFields := a.Provider != "" || a.ServiceID != "" || a.DeviceIDHash != "" ||
		a.SessionPublicKey != "" || a.PriceDust != 0 || a.PricePeriodSeconds != 0 ||
		a.BudgetDust != 0 || a.MaxActiveSeconds != 0 || a.ExpiresAt != ""
	switch a.Action {
	case StreamActionOpen:
		if !fuzzLowerHex(a.Provider, sha256.Size) || a.Provider == a.Sender {
			fail("provider")
		}
		if !fuzzIdentifier(a.ServiceID, 128) {
			fail("service id")
		}
		if !fuzzLowerHex(a.DeviceIDHash, sha256.Size) {
			fail("device_id_hash")
		}
		if !fuzzLowerHex(a.SessionPublicKey, ed25519.PublicKeySize) {
			fail("session_public_key")
		}
		if a.PriceDust == 0 || a.PricePeriodSeconds == 0 || a.BudgetDust == 0 || a.MaxActiveSeconds == 0 {
			fail("positive price, period, budget and active-time cap")
		}
		if expires, ok := fuzzTime(a.ExpiresAt); !ok || !expires.After(at) {
			fail("expiry")
		}
		if a.Receipt != nil {
			fail("open carries no receipt")
		}
	case StreamActionReceipt:
		r := a.Receipt
		if openFields || r == nil || r.StreamID != a.StreamID || r.Sequence == 0 ||
			r.CumulativeActiveSeconds == 0 || strings.TrimSpace(r.Signature) == "" {
			fail("usage receipt")
		}
		if _, ok := fuzzTime(r.ObservedAt); !ok {
			fail("receipt observed_at")
		}
	case StreamActionPause, StreamActionResume, StreamActionSettle, StreamActionClose:
		if openFields || a.Receipt != nil {
			fail("lifecycle action")
		}
	default:
		fail("action set")
	}
}

// streamFuzzSeeds returns real signed stream transactions for every action,
// built with the unit tests' helpers.
func streamFuzzSeeds(f *testing.F) []*mempool.Tx {
	payer := newStreamTestWallet(f)
	provider := newStreamTestWallet(f)
	sessionPublic, sessionPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	open := openStreamAction(f, payer, provider, sessionPublic, DustPerCell)
	receipt := StreamAction{
		ID: "stream-receipt-1", StreamID: open.StreamID, Action: StreamActionReceipt,
		Receipt: func() *StreamUsageReceipt {
			r := signedUsageReceipt(f, sessionPrivate, StreamUsageReceipt{
				StreamID: open.StreamID, Sequence: 1, CumulativeActiveSeconds: 60,
				ObservedAt: "2026-08-01T00:01:00Z",
			})
			return &r
		}(),
		Timestamp: "2026-08-01T00:01:01Z",
	}
	lifecycle := func(id, action string, nonce uint64, at string) StreamAction {
		return StreamAction{ID: id, StreamID: open.StreamID, Action: action, Nonce: nonce, Timestamp: at}
	}
	return []*mempool.Tx{
		signedStreamTx(f, payer, open),
		signedStreamTx(f, provider, receipt),
		signedStreamTx(f, payer, lifecycle("stream-pause", StreamActionPause, 1, "2026-08-01T00:02:00Z")),
		signedStreamTx(f, payer, lifecycle("stream-resume", StreamActionResume, 2, "2026-08-01T00:03:00.5Z")),
		signedStreamTx(f, provider, lifecycle("stream-settle", StreamActionSettle, 1, "2026-08-01T00:04:00Z")),
		signedStreamTx(f, payer, lifecycle("stream-close", StreamActionClose, 3, "2026-08-01T00:05:00+02:00")),
	}
}

func FuzzDecodeStreamActionTx(f *testing.F) {
	add := func(tx *mempool.Tx) {
		f.Add(tx.Payload, tx.ID, tx.Sender, tx.Nonce, tx.ContractID, tx.PublicKey, tx.Signature,
			tx.Amount, tx.Fee, tx.GasLimit, tx.Recipient)
	}
	seeds := streamFuzzSeeds(f)
	for _, tx := range seeds {
		if err := VerifyStreamActionTx(tx); err != nil {
			f.Fatalf("genuine seed rejected: %v", err)
		}
		add(tx)
	}
	// Edge cases derived from the valid open transaction.
	open := seeds[0]
	variant := func(mutate func(*mempool.Tx)) {
		tx := cloneTx(open)
		mutate(tx)
		add(tx)
	}
	variant(func(tx *mempool.Tx) { tx.Payload = append([]byte(" "), tx.Payload...) })
	variant(func(tx *mempool.Tx) {
		tx.Payload = bytes.Replace(tx.Payload, []byte(`{"id"`), []byte(`{"id":"x","id"`), 1)
	})
	variant(func(tx *mempool.Tx) { tx.Payload = bytes.Replace(tx.Payload, []byte(`"open"`), []byte(`"OPEN"`), 1) })
	variant(func(tx *mempool.Tx) { tx.Payload = bytes.Replace(tx.Payload, []byte(`}`), []byte(`,"extra":1}`), 1) })
	variant(func(tx *mempool.Tx) { tx.Payload = []byte("null") })
	variant(func(tx *mempool.Tx) { tx.Payload = []byte("{}") })
	variant(func(tx *mempool.Tx) { tx.Payload = nil })
	variant(func(tx *mempool.Tx) { tx.ID = "another-id" })
	variant(func(tx *mempool.Tx) { tx.Sender = strings.ToUpper(tx.Sender) })
	variant(func(tx *mempool.Tx) { tx.Nonce++ })
	variant(func(tx *mempool.Tx) { tx.ContractID = TaskContractID })
	variant(func(tx *mempool.Tx) { tx.Amount = 1 })
	variant(func(tx *mempool.Tx) { tx.Fee = 0.5 })
	variant(func(tx *mempool.Tx) { tx.Recipient = tx.Sender })
	variant(func(tx *mempool.Tx) { tx.PublicKey = strings.ToUpper(tx.PublicKey) })
	variant(func(tx *mempool.Tx) { tx.Signature = " " + tx.Signature + "\n" })
	variant(func(tx *mempool.Tx) { tx.Signature = tx.Signature[:len(tx.Signature)-2] })
	variant(func(tx *mempool.Tx) { tx.PublicKey = "zz" })

	f.Fuzz(func(t *testing.T, payload []byte, id, sender string, nonce uint64, contractID, publicKey, signature string,
		amount, fee float64, gasLimit int64, recipient string) {
		tx := &mempool.Tx{
			ID: id, Sender: sender, Recipient: recipient, Amount: amount, Fee: fee, GasLimit: gasLimit,
			Nonce: nonce, Payload: payload, ContractID: contractID, Signature: signature, PublicKey: publicKey,
		}
		orig := cloneTx(tx)

		action, decodeErr := DecodeStreamActionTx(tx)
		if !reflect.DeepEqual(tx, orig) {
			t.Fatal("DecodeStreamActionTx modified its input")
		}
		if decodeErr == nil {
			// Canonical: the payload is the only encoding of what it decodes to,
			// and the decoded identity is exactly the transaction's.
			canonical, err := json.Marshal(action)
			if err != nil || !bytes.Equal(canonical, payload) {
				t.Fatalf("accepted a non-canonical payload %q (canonical %q, %v)", payload, canonical, err)
			}
			if contractID != StreamContractID || action.ID == "" || action.ID != id ||
				action.Sender == "" || action.Sender != sender || action.Nonce != nonce {
				t.Fatalf("decoded identity %q/%q/%d is not bound to tx %q/%q/%d (contract %q)",
					action.ID, action.Sender, action.Nonce, id, sender, nonce, contractID)
			}
		}

		verifyErr := VerifyStreamActionTx(tx)
		if !reflect.DeepEqual(tx, orig) {
			t.Fatal("VerifyStreamActionTx modified its input")
		}
		if verifyErr != nil {
			return
		}
		if decodeErr != nil {
			t.Fatalf("verifier accepted a transaction the decoder rejects: %v", decodeErr)
		}
		checkAcceptedStreamAction(t, action)
		if amount != 0 || fee != 0 || gasLimit != 0 || recipient != "" {
			t.Fatalf("accepted outer amount %v fee %v gas %d recipient %q", amount, fee, gasLimit, recipient)
		}
		pub, pubErr := hex.DecodeString(strings.TrimSpace(publicKey))
		sig, sigErr := hex.DecodeString(strings.TrimSpace(signature))
		sum := sha256.Sum256(pub)
		if pubErr != nil || sigErr != nil || hex.EncodeToString(sum[:]) != action.Sender {
			t.Fatalf("accepted a public key that does not derive sender %q", action.Sender)
		}
		if !fuzzVerifyMLDSA(pub, payload, sig) {
			t.Fatal("accepted a signature the reference ML-DSA-87 verifier rejects")
		}
	})
}

// FuzzVerifyStreamActionTx builds each action from its fields, encodes it
// canonically, binds the outer transaction to it and signs it, so the fuzzer
// spends its time in the shape and signature checks rather than at the
// canonical-JSON gate.
func FuzzVerifyStreamActionTx(f *testing.F) {
	add := func(a StreamAction, sender string) {
		r := a.Receipt
		if r == nil {
			r = &StreamUsageReceipt{}
		}
		f.Add(a.Action, a.ID, a.StreamID, a.Provider, a.ServiceID, a.DeviceIDHash, a.SessionPublicKey,
			a.PriceDust, a.PricePeriodSeconds, a.BudgetDust, a.MaxActiveSeconds, a.ExpiresAt, a.Timestamp, a.Nonce,
			a.Receipt != nil, r.StreamID, r.Sequence, r.CumulativeActiveSeconds, r.ObservedAt, r.Signature, sender)
	}
	var actions []StreamAction
	for _, tx := range streamFuzzSeeds(f) {
		var a StreamAction
		if err := json.Unmarshal(tx.Payload, &a); err != nil {
			f.Fatal(err)
		}
		actions = append(actions, a)
		add(a, "")       // re-signed by the harness key: accepted
		add(a, a.Sender) // the helper's wallet, which the harness cannot sign for
	}
	open := actions[0]
	bad := open
	bad.SessionPublicKey = strings.ToUpper(bad.SessionPublicKey)
	add(bad, "")
	bad = open
	bad.ExpiresAt = bad.Timestamp
	add(bad, "")
	bad = open
	bad.StreamID = "vpn/device"
	add(bad, "")
	bad = open
	bad.Receipt = &StreamUsageReceipt{StreamID: open.StreamID, Sequence: 1}
	add(bad, "")

	key := newFuzzKey(1)
	publicKey := hex.EncodeToString(key.pub)
	zeroSignature := strings.Repeat("00", mldsa87.SignatureSize)

	f.Fuzz(func(t *testing.T, action, id, streamID, provider, serviceID, deviceIDHash, sessionPublicKey string,
		priceDust, pricePeriodSeconds, budgetDust, maxActiveSeconds uint64, expiresAt, timestamp string, nonce uint64,
		withReceipt bool, receiptStreamID string, receiptSequence, receiptActiveSeconds uint64,
		receiptObservedAt, receiptSignature string, sender string) {
		a := StreamAction{
			ID: id, Sender: key.address, StreamID: streamID, Action: action, Provider: provider,
			ServiceID: serviceID, DeviceIDHash: deviceIDHash, SessionPublicKey: sessionPublicKey,
			PriceDust: priceDust, PricePeriodSeconds: pricePeriodSeconds, BudgetDust: budgetDust,
			MaxActiveSeconds: maxActiveSeconds, ExpiresAt: expiresAt, Nonce: nonce, Timestamp: timestamp,
		}
		if sender != "" {
			a.Sender = sender
		}
		if withReceipt {
			a.Receipt = &StreamUsageReceipt{
				StreamID: receiptStreamID, Sequence: receiptSequence, CumulativeActiveSeconds: receiptActiveSeconds,
				ObservedAt: receiptObservedAt, Signature: receiptSignature,
			}
		}
		// Keep to what the wire can carry: JSON turns invalid UTF-8 into U+FFFD.
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		var wire StreamAction
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		tx := &mempool.Tx{
			ID: wire.ID, Sender: wire.Sender, Nonce: wire.Nonce, Payload: payload,
			ContractID: StreamContractID, PublicKey: publicKey, Signature: zeroSignature,
		}
		shapeErr := validateStreamActionShape(wire)
		if shapeErr == nil {
			tx.Signature = hex.EncodeToString(key.mustSign(t, payload))
		}

		// A canonical payload bound to its own transaction always decodes, to
		// exactly the action that was encoded.
		decoded, err := DecodeStreamActionTx(tx)
		if wire.ID == "" || wire.Sender == "" {
			if err == nil {
				t.Fatal("decoded a stream action with no id or sender")
			}
		} else if err != nil || !reflect.DeepEqual(decoded, wire) {
			t.Fatalf("canonical bound payload did not decode back: %v", err)
		}

		err = VerifyStreamActionTx(tx)
		switch {
		case shapeErr != nil:
			if err == nil {
				t.Fatalf("accepted a malformed stream action (%v)", shapeErr)
			}
			return
		case wire.Sender != key.address:
			if !errors.Is(err, ErrStreamSignatureInvalid) {
				t.Fatalf("sender %q is not the signing key, got %v", wire.Sender, err)
			}
			return
		case err != nil:
			t.Fatalf("rejected a well-formed action signed by its sender: %v", err)
		}
		checkAcceptedStreamAction(t, wire)

		// The signed action is bound to the outer transaction and carries no
		// outer value.
		moved := cloneTx(tx)
		moved.Nonce++
		paid := cloneTx(tx)
		paid.Amount = 1
		if VerifyStreamActionTx(moved) == nil || VerifyStreamActionTx(paid) == nil {
			t.Fatal("a signed stream action verified under a different nonce or with an outer amount")
		}
	})
}
