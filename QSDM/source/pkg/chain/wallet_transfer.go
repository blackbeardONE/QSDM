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
//
// The signer binding is checked here at every height: the envelope's sender
// must be hex(sha256(public_key)) and its ML-DSA-87 signature must verify
// under that public key (wallet.VerifyTransactionData). The Proof-of-
// Entanglement parent rules need the committed chain, so the block producer
// applies them before calling this (see poe.go).
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

	env, err := decodeWalletTransferEnvelope(tx.Payload)
	if err != nil {
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

// decodeWalletTransferEnvelope strictly decodes the signed envelope a wallet
// transfer commits in tx.Payload: unknown fields and trailing JSON are
// refused. Block application and the PoE parent rules share it, so both read
// exactly the parent_cells the signature covers.
func decodeWalletTransferEnvelope(payload []byte) (wallet.TransactionData, error) {
	var env wallet.TransactionData
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return wallet.TransactionData{}, fmt.Errorf("chain: decode wallet transfer envelope: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return wallet.TransactionData{}, err
	}
	return env, nil
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
