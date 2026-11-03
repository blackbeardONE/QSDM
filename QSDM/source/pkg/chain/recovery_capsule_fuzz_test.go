package chain

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/walletrecovery"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// Fuzz targets for the wallet recovery capsule decoder and verifier, which
// gate a public, replicated record on every node.

// checkAcceptedRecoveryAction asserts the documented rules for a recovery
// capsule registration the verifier accepted.
func checkAcceptedRecoveryAction(t *testing.T, a RecoveryCapsuleAction) {
	t.Helper()
	fail := func(rule string) {
		t.Helper()
		t.Fatalf("accepted recovery capsule action breaks the %s rule: %+v", rule, a)
	}
	if !fuzzIdentifier(a.ID, 128) {
		fail("action id")
	}
	if !fuzzLowerHex(a.Sender, sha256.Size) || !fuzzLowerHex(a.Locator, sha256.Size) {
		fail("sender and locator digest")
	}
	if a.Action != RecoveryCapsuleActionRegister {
		fail("action set")
	}
	c := a.Capsule
	if c.Locator != a.Locator || c.Address != a.Sender {
		fail("capsule binding")
	}
	if c.Version != walletrecovery.LegacyCapsuleVersion || c.Scheme != walletrecovery.LegacyCapsuleScheme ||
		c.Cipher != "aes-256-gcm" {
		fail("capsule version, scheme and cipher")
	}
	if nonce, err := base64.StdEncoding.DecodeString(c.Nonce); err != nil || len(nonce) != 12 {
		fail("capsule nonce")
	}
	// walletrecovery bounds the ciphertext to 32 bytes .. 16 KiB.
	if ct, err := base64.StdEncoding.DecodeString(c.Ciphertext); err != nil || len(ct) < 32 || len(ct) > 16*1024 {
		fail("capsule ciphertext")
	}
	created, ok := fuzzTime(c.CreatedAt)
	if !ok {
		fail("capsule created_at")
	}
	if at, ok := fuzzTime(a.Timestamp); !ok || at.Before(created) {
		fail("registration timestamp")
	}
}

func recoveryFuzzSeeds(f *testing.F) []*mempool.Tx {
	wallet := newRecoveryTestWallet(f)
	return []*mempool.Tx{
		signedRecoveryCapsuleTx(f, wallet, "recovery-register-1", 0),
		signedRecoveryCapsuleTx(f, wallet, "recovery:register.2_b", 7),
	}
}

func FuzzDecodeRecoveryCapsuleTx(f *testing.F) {
	add := func(tx *mempool.Tx) {
		f.Add(tx.Payload, tx.ID, tx.Sender, tx.Nonce, tx.ContractID, tx.PublicKey, tx.Signature,
			tx.Amount, tx.Fee, tx.GasLimit, tx.Recipient)
	}
	seeds := recoveryFuzzSeeds(f)
	for _, tx := range seeds {
		if err := VerifyRecoveryCapsuleTx(tx); err != nil {
			f.Fatalf("genuine seed rejected: %v", err)
		}
		add(tx)
	}
	variant := func(mutate func(*mempool.Tx)) {
		tx := cloneTx(seeds[0])
		mutate(tx)
		add(tx)
	}
	replace := func(old, new string) func(*mempool.Tx) {
		return func(tx *mempool.Tx) { tx.Payload = bytes.Replace(tx.Payload, []byte(old), []byte(new), 1) }
	}
	variant(func(tx *mempool.Tx) { tx.Payload = append(tx.Payload, '\n') })
	variant(func(tx *mempool.Tx) {
		tx.Payload = append(tx.Payload, bytes.Repeat([]byte(" "), RecoveryCapsuleMaxPayloadBytes)...)
	})
	variant(replace(`"register"`, `"Register"`))
	variant(replace(`"version":1`, `"version":2`))
	variant(replace(`"cipher":"aes-256-gcm"`, `"cipher":"aes-128-gcm"`))
	variant(replace(`"nonce":"`, `"nonce":"AAAA`))
	variant(func(tx *mempool.Tx) { tx.Payload = nil })
	variant(func(tx *mempool.Tx) { tx.Payload = []byte(`{"id":"x"}`) })
	variant(func(tx *mempool.Tx) { tx.ID = "recovery-register-2" })
	variant(func(tx *mempool.Tx) { tx.Sender = strings.ToUpper(tx.Sender) })
	variant(func(tx *mempool.Tx) { tx.Nonce = 1 })
	variant(func(tx *mempool.Tx) { tx.ContractID = StreamContractID })
	variant(func(tx *mempool.Tx) { tx.GasLimit = 1 })
	variant(func(tx *mempool.Tx) { tx.PublicKey = "\t" + tx.PublicKey })
	variant(func(tx *mempool.Tx) { tx.Signature = strings.Repeat("00", mldsa87.SignatureSize) })

	f.Fuzz(func(t *testing.T, payload []byte, id, sender string, nonce uint64, contractID, publicKey, signature string,
		amount, fee float64, gasLimit int64, recipient string) {
		tx := &mempool.Tx{
			ID: id, Sender: sender, Recipient: recipient, Amount: amount, Fee: fee, GasLimit: gasLimit,
			Nonce: nonce, Payload: payload, ContractID: contractID, Signature: signature, PublicKey: publicKey,
		}
		orig := cloneTx(tx)

		action, decodeErr := DecodeRecoveryCapsuleTx(tx)
		if !reflect.DeepEqual(tx, orig) {
			t.Fatal("DecodeRecoveryCapsuleTx modified its input")
		}
		if decodeErr == nil {
			if len(payload) == 0 || len(payload) > RecoveryCapsuleMaxPayloadBytes {
				t.Fatalf("accepted a %d-byte payload", len(payload))
			}
			canonical, err := json.Marshal(action)
			if err != nil || !bytes.Equal(canonical, payload) {
				t.Fatalf("accepted a non-canonical payload %q (canonical %q, %v)", payload, canonical, err)
			}
			if contractID != RecoveryCapsuleContractID || action.ID == "" || action.ID != id ||
				action.Sender == "" || action.Sender != sender || action.Nonce != nonce {
				t.Fatalf("decoded identity %q/%q/%d is not bound to tx %q/%q/%d (contract %q)",
					action.ID, action.Sender, action.Nonce, id, sender, nonce, contractID)
			}
		}

		verifyErr := VerifyRecoveryCapsuleTx(tx)
		if !reflect.DeepEqual(tx, orig) {
			t.Fatal("VerifyRecoveryCapsuleTx modified its input")
		}
		if verifyErr != nil {
			return
		}
		if decodeErr != nil {
			t.Fatalf("verifier accepted a transaction the decoder rejects: %v", decodeErr)
		}
		checkAcceptedRecoveryAction(t, action)
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

// FuzzVerifyRecoveryCapsuleTx builds the registration from its fields, binds
// the outer transaction to it and signs it, so the fuzzer reaches the capsule
// shape and signature checks.
func FuzzVerifyRecoveryCapsuleTx(f *testing.F) {
	add := func(a RecoveryCapsuleAction, sender, capsuleAddress string) {
		c := a.Capsule
		f.Add(a.ID, a.Action, a.Locator, c.Version, c.Scheme, c.Locator, capsuleAddress, c.Cipher, c.Nonce,
			c.Ciphertext, c.CreatedAt, a.Nonce, a.Timestamp, sender)
	}
	var actions []RecoveryCapsuleAction
	for _, tx := range recoveryFuzzSeeds(f) {
		var a RecoveryCapsuleAction
		if err := json.Unmarshal(tx.Payload, &a); err != nil {
			f.Fatal(err)
		}
		actions = append(actions, a)
		add(a, "", "")                      // re-bound to and signed by the harness key: accepted
		add(a, a.Sender, a.Capsule.Address) // the helper's wallet, which the harness cannot sign for
	}
	bad := actions[0]
	bad.Timestamp = "2000-01-01T00:00:00Z" // before the capsule was created
	add(bad, "", "")
	bad = actions[0]
	bad.Capsule.Ciphertext = base64.StdEncoding.EncodeToString(make([]byte, 31))
	add(bad, "", "")
	bad = actions[0]
	bad.Locator = strings.ToUpper(bad.Locator)
	add(bad, "", "")

	key := newFuzzKey(2)
	publicKey := hex.EncodeToString(key.pub)
	zeroSignature := strings.Repeat("00", mldsa87.SignatureSize)

	f.Fuzz(func(t *testing.T, id, action, locator string, version int, scheme, capsuleLocator, capsuleAddress,
		cipher, capsuleNonce, ciphertext, createdAt string, nonce uint64, timestamp, sender string) {
		a := RecoveryCapsuleAction{
			ID: id, Sender: key.address, Action: action, Locator: locator,
			Capsule: walletrecovery.LegacyCapsule{
				Version: version, Scheme: scheme, Locator: capsuleLocator, Address: capsuleAddress,
				Cipher: cipher, Nonce: capsuleNonce, Ciphertext: ciphertext, CreatedAt: createdAt,
			},
			Nonce: nonce, Timestamp: timestamp,
		}
		if sender != "" {
			a.Sender = sender
		}
		if capsuleAddress == "" {
			a.Capsule.Address = a.Sender
		}
		raw, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		var wire RecoveryCapsuleAction
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		payload, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		tx := &mempool.Tx{
			ID: wire.ID, Sender: wire.Sender, Nonce: wire.Nonce, Payload: payload,
			ContractID: RecoveryCapsuleContractID, PublicKey: publicKey, Signature: zeroSignature,
		}
		shapeErr := validateRecoveryCapsuleAction(wire)
		if shapeErr == nil {
			tx.Signature = hex.EncodeToString(key.mustSign(t, payload))
		}
		oversized := len(payload) > RecoveryCapsuleMaxPayloadBytes

		decoded, err := DecodeRecoveryCapsuleTx(tx)
		if wire.ID == "" || wire.Sender == "" || oversized {
			if err == nil {
				t.Fatal("decoded a registration with no id or sender, or an oversized payload")
			}
		} else if err != nil || !reflect.DeepEqual(decoded, wire) {
			t.Fatalf("canonical bound payload did not decode back: %v", err)
		}

		err = VerifyRecoveryCapsuleTx(tx)
		switch {
		case shapeErr != nil || oversized:
			if err == nil {
				t.Fatalf("accepted a malformed registration (%v)", shapeErr)
			}
			return
		case wire.Sender != key.address:
			if !errors.Is(err, ErrRecoveryCapsuleSignature) {
				t.Fatalf("sender %q is not the signing key, got %v", wire.Sender, err)
			}
			return
		case err != nil:
			t.Fatalf("rejected a well-formed registration signed by its sender: %v", err)
		}
		checkAcceptedRecoveryAction(t, wire)

		moved := cloneTx(tx)
		moved.Nonce++
		paid := cloneTx(tx)
		paid.Fee = 1
		if VerifyRecoveryCapsuleTx(moved) == nil || VerifyRecoveryCapsuleTx(paid) == nil {
			t.Fatal("a signed registration verified under a different nonce or with an outer fee")
		}
	})
}
