package chain

import (
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

func signedWalletTransfer(t *testing.T) (wallet.TransactionData, *mempool.Tx) {
	t.Helper()
	ws, err := wallet.NewWalletService()
	if err != nil {
		t.Fatalf("NewWalletService: %v", err)
	}
	env := wallet.TransactionData{
		ID:          "wallet-transfer-1",
		Sender:      ws.GetAddress(),
		Recipient:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Amount:      3,
		Fee:         0.25,
		GeoTag:      "",
		ParentCells: []string{"parent-a", "parent-b"},
		Nonce:       1,
		PublicKey:   hex.EncodeToString(ws.GetPublicKey()),
		Timestamp:   time.Unix(1_700_000_000, 0).UTC().Format(time.RFC3339),
	}
	canonical, err := env.CanonicalBytes()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ws.SignData(canonical)
	if err != nil {
		t.Fatal(err)
	}
	env.Signature = hex.EncodeToString(sig)
	payload, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	tx := &mempool.Tx{
		ID: env.ID, Sender: env.Sender, Recipient: env.Recipient,
		Amount: env.Amount, Fee: env.Fee, Nonce: env.Nonce - 1,
		ContractID: WalletTransferContractID, Payload: payload,
		Signature: env.Signature, PublicKey: env.PublicKey,
	}
	return env, tx
}

func TestApplyWalletTransferTx_VerifiesAndApplies(t *testing.T) {
	env, tx := signedWalletTransfer(t)
	accounts := NewAccountStore()
	accounts.Credit(env.Sender, 10)
	if err := ApplyWalletTransferTx(accounts, tx); err != nil {
		t.Fatalf("ApplyWalletTransferTx: %v", err)
	}
	sender, _ := accounts.Get(env.Sender)
	recipient, _ := accounts.Get(env.Recipient)
	if sender.Balance != 6.75 || sender.Nonce != 1 || recipient.Balance != 3 {
		t.Fatalf("unexpected account state: sender=%+v recipient=%+v", sender, recipient)
	}
}

func TestApplyWalletTransferTx_RejectsTamperedFields(t *testing.T) {
	env, tx := signedWalletTransfer(t)
	accounts := NewAccountStore()
	accounts.Credit(env.Sender, 10)
	tx.Amount++
	if err := ApplyWalletTransferTx(accounts, tx); err == nil {
		t.Fatal("tampered transfer was accepted")
	}
	sender, _ := accounts.Get(env.Sender)
	if sender.Balance != 10 || sender.Nonce != 0 {
		t.Fatalf("tampered transfer mutated state: %+v", sender)
	}
}
