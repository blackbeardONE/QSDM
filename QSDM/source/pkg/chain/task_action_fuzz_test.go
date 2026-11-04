package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Fuzz targets for task actions: the decoder every task transaction passes
// through at apply time, and the consensus-side signature check.

func FuzzDecodeTaskActionTx(f *testing.F) {
	genuine, _, _ := signedTaskAction(f)
	full, err := json.Marshal(genuine)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(full, genuine.ID, genuine.Sender, genuine.Nonce, TaskContractID)
	// The envelope may omit its identity and take it from the transaction.
	f.Add([]byte(`{"task_id":"task-1","action":"start","timestamp":"2026-08-14T00:00:00Z"}`),
		"act-2", genuine.Sender, uint64(4), TaskContractID)
	f.Add(full, "", "", genuine.Nonce, TaskContractID)
	f.Add(full, "other-id", genuine.Sender, genuine.Nonce, TaskContractID)
	f.Add(full, genuine.ID, "other-sender", genuine.Nonce, TaskContractID)
	f.Add(full, genuine.ID, genuine.Sender, genuine.Nonce+1, TaskContractID)
	f.Add(full, genuine.ID, genuine.Sender, genuine.Nonce, StreamContractID)
	f.Add([]byte(`{"nonce":0,"amount":-0,"payload":"{\"round\":1}"}`), "id", "s", uint64(9), TaskContractID)
	f.Add([]byte(`{"amount":1e400}`), "id", "s", uint64(0), TaskContractID)
	f.Add([]byte(`null`), "id", "s", uint64(0), TaskContractID)
	f.Add([]byte(`[]`), "", "", uint64(0), TaskContractID)

	f.Fuzz(func(t *testing.T, payload []byte, id, sender string, nonce uint64, contractID string) {
		// A transaction reaches the decoder as JSON (block gossip, the API),
		// which turns invalid UTF-8 into U+FFFD, so its identity strings are
		// always valid UTF-8; without this the re-encoding check below would
		// only be testing encoding/json.
		id, sender = fuzzWireString(id), fuzzWireString(sender)
		tx := &mempool.Tx{ID: id, Sender: sender, Nonce: nonce, Payload: payload, ContractID: contractID}
		orig := cloneTx(tx)
		action, err := DecodeTaskActionTx(tx)
		if !reflect.DeepEqual(tx, orig) {
			t.Fatal("DecodeTaskActionTx modified its input")
		}
		if err != nil {
			return
		}
		var envelope TaskAction
		if contractID != TaskContractID || json.Unmarshal(payload, &envelope) != nil {
			t.Fatalf("accepted contract %q payload %q", contractID, payload)
		}
		// The identity is the envelope's where it has one, the transaction's
		// where it does not, and the two never disagree.
		bound := func(field, fromEnvelope, fromTx, got string) {
			want := fromEnvelope
			if want == "" {
				want = fromTx
			}
			if got != want || (fromEnvelope != "" && fromTx != "" && fromEnvelope != fromTx) {
				t.Fatalf("%s: envelope %q, tx %q, decoded %q", field, fromEnvelope, fromTx, got)
			}
		}
		bound("id", envelope.ID, id, action.ID)
		bound("sender", envelope.Sender, sender, action.Sender)
		if action.Nonce != nonce || (envelope.Nonce != 0 && envelope.Nonce != nonce) {
			t.Fatalf("nonce: envelope %d, tx %d, decoded %d", envelope.Nonce, nonce, action.Nonce)
		}
		envelope.ID, envelope.Sender, envelope.Nonce = action.ID, action.Sender, action.Nonce
		if !reflect.DeepEqual(action, envelope) {
			t.Fatalf("decoded %+v, envelope says %+v", action, envelope)
		}
		// Decoding is idempotent: the decoded action, re-encoded, decodes to itself.
		again, err := json.Marshal(action)
		if err != nil {
			t.Fatalf("re-encode %+v: %v", action, err)
		}
		tx.Payload = again
		if back, err := DecodeTaskActionTx(tx); err != nil || !reflect.DeepEqual(back, action) {
			t.Fatalf("re-decoded %+v, %v; want %+v", back, err, action)
		}
	})
}

// FuzzVerifyTaskActionSignature drives the consensus-side task action check
// with actions the harness signs itself (mode 1), with signature and key hex
// supplied by the fuzzer (mode 2), and unsigned (mode 0), at fuzzed heights
// against a fuzzed activation height.
func FuzzVerifyTaskActionSignature(f *testing.F) {
	genuine, sigHex, pubHex := signedTaskAction(f)
	if err := VerifyTaskActionSignature(genuine, sigHex, pubHex, 100); err != nil {
		f.Fatalf("genuine seed rejected: %v", err)
	}
	add := func(a TaskAction, mode uint8, tamper bool, sig, pub string, height, activation uint64) {
		f.Add(a.ID, a.Sender, a.TaskID, a.Action, a.Amount, a.Payload, a.Nonce, a.Timestamp,
			mode, tamper, sig, pub, height, activation)
	}
	add(genuine, 2, false, sigHex, pubHex, 100, 0)
	add(genuine, 2, false, sigHex, pubHex, 100, 50)
	add(genuine, 2, false, sigHex[:len(sigHex)-2], pubHex, 100, 0)
	add(genuine, 2, false, sigHex, "zz"+pubHex[2:], 100, 0)
	add(genuine, 1, false, "", "", 7, 0)
	add(genuine, 1, true, "", "", 7, 0)
	unsigned := genuine
	unsigned.Sender = ""
	add(unsigned, 1, false, "", "", 1, 2)
	add(genuine, 0, false, "", "", 499, 500)
	add(genuine, 0, false, "", "", 500, 500)
	add(genuine, 2, false, "", pubHex, 500, 500)
	add(TaskAction{Action: "claim", Payload: `{"round":2}`, Amount: 1e-9}, 1, false, "", "", 0, 0)

	key := newFuzzKey(3)
	keyHex := hex.EncodeToString(key.pub)

	f.Fuzz(func(t *testing.T, id, sender, taskID, action string, amount float64, payload string, nonce uint64,
		timestamp string, mode uint8, tamper bool, sig, pub string, height, activation uint64) {
		prev := TaskActionSignatureActivationHeight()
		defer SetTaskActionSignatureActivationHeight(prev)
		SetTaskActionSignatureActivationHeight(activation)

		a := TaskAction{ID: id, Sender: sender, TaskID: taskID, Action: action, Amount: amount,
			Payload: payload, Nonce: nonce, Timestamp: timestamp}
		harnessSigned := false
		switch mode % 3 {
		case 0:
			sig, pub = "", ""
		case 1:
			if a.Sender == "" {
				a.Sender = key.address
			}
			pub = keyHex
			sig = ""
			if canonical, err := CanonicalTaskActionSigningBytes(a); err == nil {
				sig = hex.EncodeToString(key.mustSign(t, canonical))
				harnessSigned = true
			}
		}

		err := VerifyTaskActionSignature(a, sig, pub, height)
		if sig == "" || pub == "" {
			required := activation != 0 && height >= activation
			if required != errors.Is(err, ErrTaskActionUnsigned) || (!required && err != nil) {
				t.Fatalf("unsigned action at height %d (activation %d): %v", height, activation, err)
			}
			return
		}
		// A present signature is judged on its own: the height gate plays no part.
		SetTaskActionSignatureActivationHeight(0)
		if again := VerifyTaskActionSignature(a, sig, pub, 0); (again == nil) != (err == nil) {
			t.Fatalf("signed verdict depends on the height gate: %v vs %v", err, again)
		}
		if err != nil {
			if !errors.Is(err, ErrTaskActionBadSignature) {
				t.Fatalf("rejection does not wrap ErrTaskActionBadSignature: %v", err)
			}
			if harnessSigned && a.Sender == key.address {
				t.Fatalf("rejected an action signed by its sender: %v", err)
			}
			return
		}
		pubBytes, pubErr := hex.DecodeString(pub)
		sigBytes, sigErr := hex.DecodeString(sig)
		sum := sha256.Sum256(pubBytes)
		canonical, canonErr := CanonicalTaskActionSigningBytes(a)
		if pubErr != nil || sigErr != nil || canonErr != nil || hex.EncodeToString(sum[:]) != a.Sender {
			t.Fatalf("accepted a key that does not derive sender %q", a.Sender)
		}
		if !fuzzVerifyMLDSA(pubBytes, canonical, sigBytes) {
			t.Fatal("accepted a signature the reference ML-DSA-87 verifier rejects")
		}
		if tamper {
			a.Payload += "x" // any change to the signed bytes
			if err := VerifyTaskActionSignature(a, sig, pub, height); !errors.Is(err, ErrTaskActionBadSignature) {
				t.Fatalf("tampered action: %v", err)
			}
		}
	})
}
