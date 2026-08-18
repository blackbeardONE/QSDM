package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/storage"
)

// parseWalletTxID pulls the transaction id out of a wallet-built payload.
//
// Returns "" when absent rather than falling back to anything. StoreTransaction
// used to fall back to the SENDER ADDRESS as the id (sqlite.go:120-121), which
// makes every id-less transaction from one sender share a key: the first is
// stored and every later one is silently skipped as a duplicate. Refusing to
// settle is the honest response to a payload we cannot identify.
func parseWalletTxID(txBytes []byte) string {
	var m map[string]interface{}
	if err := json.Unmarshal(txBytes, &m); err != nil {
		return ""
	}
	id, _ := m["id"].(string)
	return id
}

// writeWalletSendApplyError maps the ApplyTransferAtomic sentinels for
// /wallet/send. Mirrors writeSubmitSignedApplyError, but this endpoint has no
// signed envelope to echo, so it reports the id it settled under.
//
// ErrNonceConflict is handled even though this path passes envelopeNonce == 0
// and therefore cannot raise it today: if a future change gives the wallet
// service a real nonce, the mapping is already correct rather than falling into
// the 500 default.
func (h *Handlers) writeWalletSendApplyError(w http.ResponseWriter, txID string, err error) {
	switch {
	case errors.Is(err, storage.ErrTxAlreadyExists):
		monitoring.RecordWalletSend(monitoring.WalletSendResultDuplicate)
		writeJSONResponse(w, http.StatusConflict, SendTransactionResponse{
			TransactionID: txID,
			Status:        "duplicate",
		})
	case errors.Is(err, storage.ErrInsufficientBalance):
		monitoring.RecordWalletSend(monitoring.WalletSendResultInsufficientBalance)
		writeErrorResponse(w, http.StatusPaymentRequired, "insufficient balance for amount + fee")
	case errors.Is(err, storage.ErrNonceConflict):
		monitoring.RecordWalletSend(monitoring.WalletSendResultNonceConflict)
		writeErrorResponse(w, http.StatusConflict,
			"nonce conflict: concurrent submit raced; retry after re-reading nonce")
	case errors.Is(err, storage.ErrAtomicTransferUnsupported):
		// The backend cannot settle a transfer at all -- FileStorage refuses by
		// construction, ScyllaStorage is a stub. Retrying will never help, so
		// this is not a 500: it is a capability gap in the deployment.
		//
		// Reported explicitly because moving this endpoint onto
		// ApplyTransferAtomic newly broke it on those two backends. It used to
		// settle through StoreTransaction, which they both implement -- while
		// under-charging the fee. Failing closed is the right trade on a money
		// path, but an operator seeing a generic 500 has no way to learn that
		// the endpoint can never work on their backend. A reviewer caught that
		// I had made this regression without disclosing or testing it.
		monitoring.RecordWalletSend(monitoring.WalletSendResultStoreFailed)
		h.logger.Error("wallet send refused: storage backend cannot settle transfers",
			"error", err, "tx_id", txID)
		writeErrorResponse(w, http.StatusNotImplemented,
			"this node's storage backend cannot settle transfers; only the SQLite backend "+
				"implements atomic transfer settlement (see startup log)")
	default:
		monitoring.RecordWalletSend(monitoring.WalletSendResultStoreFailed)
		h.logger.Error("wallet send ApplyTransferAtomic failed", "error", err, "tx_id", txID)
		writeErrorResponse(w, http.StatusInternalServerError, "failed to apply transfer")
	}
}
