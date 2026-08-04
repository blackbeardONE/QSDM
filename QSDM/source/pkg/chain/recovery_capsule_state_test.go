package chain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/walletrecovery"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

type recoveryTestWallet struct {
	public  *mldsa87.PublicKey
	private *mldsa87.PrivateKey
	address string
}

func newRecoveryTestWallet(t *testing.T) recoveryTestWallet {
	t.Helper()
	public, private, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicBytes, _ := public.MarshalBinary()
	sum := sha256.Sum256(publicBytes)
	return recoveryTestWallet{public: public, private: private, address: hex.EncodeToString(sum[:])}
}

func signedRecoveryCapsuleTx(t *testing.T, wallet recoveryTestWallet, id string, nonce uint64) *mempool.Tx {
	t.Helper()
	publicBytes, _ := wallet.public.MarshalBinary()
	privateBytes, _ := wallet.private.MarshalBinary()
	material, err := walletrecovery.GenerateLegacyCapsule(wallet.address, publicBytes, privateBytes)
	if err != nil {
		t.Fatal(err)
	}
	defer material.ZeroSecrets()
	action := RecoveryCapsuleAction{
		ID:        id,
		Sender:    wallet.address,
		Action:    RecoveryCapsuleActionRegister,
		Locator:   material.Capsule.Locator,
		Capsule:   material.Capsule,
		Nonce:     nonce,
		Timestamp: time.Now().UTC().Add(time.Second).Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(wallet.private, payload, nil, true, signature); err != nil {
		t.Fatal(err)
	}
	return &mempool.Tx{
		ID:         id,
		Sender:     wallet.address,
		Nonce:      nonce,
		Payload:    payload,
		ContractID: RecoveryCapsuleContractID,
		Signature:  hex.EncodeToString(signature),
		PublicKey:  hex.EncodeToString(publicBytes),
	}
}

func TestRecoveryCapsuleStateRegistersAndReplacesOneRecordPerWallet(t *testing.T) {
	wallet := newRecoveryTestWallet(t)
	accounts := NewAccountStore()
	accounts.Credit(wallet.address, 1)
	store := NewRecoveryCapsuleStateStore()

	first := signedRecoveryCapsuleTx(t, wallet, "recovery-register-1", 0)
	if err := store.ApplyEconomicTx(first, accounts); err != nil {
		t.Fatal(err)
	}
	firstAction, _ := DecodeRecoveryCapsuleTx(first)
	if got, ok := store.GetByLocator(firstAction.Locator); !ok || got.Owner != wallet.address {
		t.Fatalf("registered capsule missing: %+v %v", got, ok)
	}
	second := signedRecoveryCapsuleTx(t, wallet, "recovery-register-2", 1)
	if err := store.ApplyEconomicTx(second, accounts); err != nil {
		t.Fatal(err)
	}
	secondAction, _ := DecodeRecoveryCapsuleTx(second)
	if store.Count() != 1 {
		t.Fatalf("capsule count = %d, want 1", store.Count())
	}
	if _, ok := store.GetByLocator(firstAction.Locator); ok {
		t.Fatal("superseded locator remains in current state")
	}
	if got, ok := store.GetByOwner(wallet.address); !ok || got.Locator != secondAction.Locator {
		t.Fatalf("owner index did not move: %+v %v", got, ok)
	}
	account, _ := accounts.Get(wallet.address)
	if account.Nonce != 2 || account.Balance != 1 {
		t.Fatalf("account changed unexpectedly: %+v", account)
	}
}

func TestRecoveryCapsuleRejectsTamperingAndUnknownAccount(t *testing.T) {
	wallet := newRecoveryTestWallet(t)
	tx := signedRecoveryCapsuleTx(t, wallet, "recovery-tamper", 0)
	var action RecoveryCapsuleAction
	_ = json.Unmarshal(tx.Payload, &action)
	action.Capsule.Ciphertext = action.Capsule.Ciphertext[:len(action.Capsule.Ciphertext)-4] + "AAAA"
	tx.Payload, _ = json.Marshal(action)
	if err := VerifyRecoveryCapsuleTx(tx); err == nil {
		t.Fatal("tampered capsule signature was accepted")
	}

	valid := signedRecoveryCapsuleTx(t, wallet, "recovery-no-account", 0)
	if err := NewRecoveryCapsuleStateStore().ApplyEconomicTx(valid, NewAccountStore()); err == nil {
		t.Fatal("wallet without an existing chain account registered a capsule")
	}
}

func TestRecoveryCapsuleCloneAndStateRoot(t *testing.T) {
	wallet := newRecoveryTestWallet(t)
	tx := signedRecoveryCapsuleTx(t, wallet, "recovery-clone", 0)
	store := NewRecoveryCapsuleStateStore()
	if err := store.ApplyHistoricalTx(tx, 1); err != nil {
		t.Fatal(err)
	}
	root := store.StateRoot()
	clone := store.ChainReplayClone().(*RecoveryCapsuleStateStore)
	if clone.StateRoot() != root {
		t.Fatal("clone state root changed")
	}
}
