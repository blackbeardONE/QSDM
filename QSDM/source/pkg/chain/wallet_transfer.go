package chain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// WalletTransferContractID identifies a self-custody CELL transfer whose
// complete ML-DSA-signed wallet envelope is committed in tx.Payload.
const WalletTransferContractID = "qsdm/wallet-transfer/v1"

// ApplyWalletTransferTx verifies the embedded signer envelope and applies its
// transfer to the canonical account store. Verification is repeated by every
// validator during block replay; API admission alone is not a consensus rule.
func ApplyWalletTransferTx(accounts *AccountStore, tx *mempool.Tx) error {
	if accounts == nil {
		return errors.New("chain: wallet transfer account store is not wired")
	}
	if tx == nil {
		return errors.New("chain: nil wallet transfer")
	}
	if tx.ContractID != WalletTransferContractID {
		return fmt.Errorf("chain: wallet transfer contract_id must be %q", WalletTransferContractID)
	}

	var env wallet.TransactionData
	dec := json.NewDecoder(bytes.NewReader(tx.Payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return fmt.Errorf("chain: decode wallet transfer envelope: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return err
	}
	if err := wallet.VerifyTransactionData(env); err != nil {
		return fmt.Errorf("chain: verify wallet transfer envelope: %w", err)
	}
	if env.ID != tx.ID || env.Sender != tx.Sender || env.Recipient != tx.Recipient ||
		env.Amount != tx.Amount || env.Fee != tx.Fee || env.Nonce-1 != tx.Nonce ||
		env.Signature != tx.Signature || env.PublicKey != tx.PublicKey {
		return errors.New("chain: wallet transfer envelope does not match transaction fields")
	}
	return accounts.ApplyTx(tx)
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra interface{}
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("chain: wallet transfer envelope has trailing JSON")
		}
		return fmt.Errorf("chain: decode wallet transfer trailing data: %w", err)
	}
	return nil
}
