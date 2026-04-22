package transaction

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/walletp2p"
	"github.com/blackbeardONE/QSDM/pkg/wasm"
	"github.com/blackbeardONE/QSDM/pkg/api"
)

// Storage interface for storing transactions
type Storage interface {
	StoreTransaction(tx []byte) error
	Close() error
}

func HandleTransaction(logger *logging.Logger, msg []byte, dynamicManager *submesh.DynamicSubmeshManager, wasmSdk *wasm.WASMSDK, consensus *consensus.ProofOfEntanglement, storage Storage, nvidiaP2PGate *monitoring.NvidiaLockP2PGate) {
	tx, err := ParseTransaction(msg)
	if err != nil {
		logger.Warn("Failed to parse transaction", "error", err, "hint", "Check transaction format and required fields (id, sender, recipient, amount, signature)")
		return
	}

	dedupeCommitted := false
	if tx.ID != "" {
		if !walletp2p.Reserve(tx.ID) {
			monitoring.RecordP2PWalletIngressDedupeSkip()
			logger.Debug("P2P wallet ingress dedupe: same tx id already ingested (mesh companion, gossip, or duplicate publish)", "tx_id", tx.ID)
			return
		}
		defer func() {
			if !dedupeCommitted {
				walletp2p.Release(tx.ID)
			}
		}()
	}

	if ok, perr := wasm.TryPreflightP2PTransactionJSON(wasmSdk, msg); perr != nil {
		logger.Warn("WASM preflight failed", "error", perr)
		monitoring.GetMetrics().IncrementTransactionsInvalid()
		return
	} else if !ok {
		logger.Warn("WASM preflight rejected transaction", "tx_id", tx.ID)
		monitoring.GetMetrics().IncrementTransactionsInvalid()
		return
	}

	logger.Info("Processing P2P transaction", "sender", tx.Sender, "recipient", tx.Recipient, "amount", tx.Amount, "fee", tx.Fee)

	ds, subErr := dynamicManager.MatchP2POrReject(tx.Fee, tx.GeoTag, msg)
	if subErr != nil {
		logger.Warn("P2P transaction rejected by submesh policy", "error", subErr)
		switch {
		case errors.Is(subErr, submesh.ErrSubmeshNoRoute):
			monitoring.RecordSubmeshP2PRejectRoute()
		case errors.Is(subErr, submesh.ErrSubmeshPayloadTooLarge):
			monitoring.RecordSubmeshP2PRejectSize()
		}
		monitoring.GetMetrics().IncrementTransactionsInvalid()
		return
	}
	if ds != nil {
		logger.Info("Routing transaction to dynamic submesh:", ds.Name, "with priority", ds.PriorityLevel)
	} else {
		_, err := dynamicManager.RouteTransaction(tx.Fee, tx.GeoTag)
		if err != nil {
			logger.Warn("No dynamic submesh matched for transaction:", err)
		}
	}

	// Transaction is already signed, we need to verify it
	// Extract signature from transaction
	if tx.Signature == "" {
		logger.Warn("Transaction has no signature, discarding", "tx_id", tx.ID, "hint", "All transactions must be signed with ML-DSA-87 before submission")
		return
	}

	// Decode hex signature
	signatureBytes, err := hex.DecodeString(tx.Signature)
	if err != nil {
		logger.Warn("Failed to decode signature", "tx_id", tx.ID, "error", err, "hint", "Signature must be valid hex-encoded ML-DSA-87 signature")
		return
	}

	// Create transaction data without signature for verification
	txForVerification := Transaction{
		ID:          tx.ID,
		Sender:      tx.Sender,
		Recipient:   tx.Recipient,
		Amount:      tx.Amount,
		Fee:         tx.Fee,
		GeoTag:      tx.GeoTag,
		ParentCells: tx.ParentCells,
		Signature:   "", // Empty for verification
		PublicKey:   "", // Excluded from signing bytes (same as wallet first-pass marshal)
		Timestamp:   tx.Timestamp,
	}
	txBytesForVerification, err := json.Marshal(txForVerification)
	if err != nil {
		logger.Warn("Failed to marshal transaction for verification:", err)
		return
	}

	// Validate transaction signature using consensus
	parentCells := make([][]byte, len(tx.ParentCells))
	for i, pc := range tx.ParentCells {
		parentCells[i] = []byte(pc)
	}
	signatures := [][]byte{signatureBytes}

	valid, err := consensus.ValidateTransaction(txBytesForVerification, parentCells, signatures, logger)
	if err != nil || !valid {
		logger.Warn("Received invalid transaction, discarding", "error", err)
		monitoring.GetMetrics().IncrementTransactionsInvalid()
		monitoring.GetMetrics().RecordError("Transaction validation failed: " + err.Error())
		return
	}

	if !nvidiaP2PGate.Allows() {
		logger.Warn("Discarding P2P transaction: NVIDIA-lock P2P gate not satisfied", "hint", "Ingest GPU NGC proofs or disable QSDMPLUS_NVIDIA_LOCK_GATE_P2P")
		monitoring.RecordNvidiaLockP2PReject()
		monitoring.GetMetrics().IncrementTransactionsInvalid()
		monitoring.GetMetrics().RecordError("NVIDIA-lock P2P gate blocked transaction after consensus validation")
		return
	}

	monitoring.GetMetrics().IncrementTransactionsValid()

	// Store the transaction
	err = storage.StoreTransaction(msg)
	if err != nil {
		logger.Error("Failed to store transaction", "error", err, "hint", "Check database connection and disk space")
		monitoring.GetMetrics().RecordError(fmt.Sprintf("Storage failed: %v", err))
	} else {
		dedupeCommitted = true
		logger.Info("Transaction stored successfully")
		monitoring.GetMetrics().IncrementTransactionsStored()
	}
}

// HandlePhase3Transaction decodes a mesh3d wire envelope and runs the phase-3 pipeline. No-op if msg is not wire format.
func HandlePhase3Transaction(logger *logging.Logger, msg []byte, mesh3dValidator *mesh3d.Mesh3DValidator, quarantineManager *quarantine.QuarantineManager, reputationManager *quarantine.ReputationManager, consensus *consensus.ProofOfEntanglement, storage Storage, nvidiaP2PGate *monitoring.NvidiaLockP2PGate) {
	tx, submeshKey, err := ParsePhase3Wire(msg)
	if err != nil {
		return
	}
	if mesh3dValidator == nil {
		logger.Warn("mesh3d wire message ignored: validator nil")
		return
	}
	HandlePhase3MeshTx(logger, tx, submeshKey, mesh3dValidator, quarantineManager, reputationManager, consensus, storage, nvidiaP2PGate)
}

func meshReputationPeer(txID string) string {
	if txID == "" {
		return "mesh3d:unknown"
	}
	if len(txID) > 48 {
		return "mesh3d:" + txID[:48]
	}
	return "mesh3d:" + txID
}

// HandlePhase3MeshTx validates and stores a decoded mesh3d transaction (quarantine + reputation + consensus + storage).
func HandlePhase3MeshTx(logger *logging.Logger, tx *mesh3d.Transaction, submeshKey string, mesh3dValidator *mesh3d.Mesh3DValidator, quarantineManager *quarantine.QuarantineManager, reputationManager *quarantine.ReputationManager, consensus *consensus.ProofOfEntanglement, storage Storage, nvidiaP2PGate *monitoring.NvidiaLockP2PGate) {
	if logger == nil || tx == nil || mesh3dValidator == nil || quarantineManager == nil || reputationManager == nil || storage == nil {
		return
	}

	walletDedupeID := walletp2p.WalletJSONIDFromBytes(tx.Data)
	dedupeCommitted := false
	if walletDedupeID != "" {
		if !walletp2p.Reserve(walletDedupeID) {
			monitoring.RecordP2PWalletIngressDedupeSkip()
			logger.Debug("P2P mesh ingress dedupe: wallet payload id already ingested", "wallet_tx_id", walletDedupeID)
			return
		}
		defer func() {
			if !dedupeCommitted {
				walletp2p.Release(walletDedupeID)
			}
		}()
	}

	peerKey := meshReputationPeer(tx.ID)

	valid, err := mesh3dValidator.ValidateTransaction(tx)
	if err != nil {
		logger.Error("3D mesh validation error", "error", err)
		quarantineManager.RecordTransaction(submeshKey, false)
		reputationManager.Penalize(peerKey)
		monitoring.GetMetrics().IncrementTransactionsInvalid()
		monitoring.GetMetrics().RecordError("3D mesh validation error: " + err.Error())
		return
	}
	quarantineManager.RecordTransaction(submeshKey, valid)
	if valid {
		reputationManager.Reward(peerKey)
		monitoring.GetMetrics().IncrementTransactionsValid()
		monitoring.GetMetrics().IncrementReputationUpdates()
	} else {
		reputationManager.Penalize(peerKey)
		monitoring.GetMetrics().IncrementTransactionsInvalid()
		monitoring.GetMetrics().IncrementReputationUpdates()
	}

	if !valid {
		logger.Warn("Transaction failed 3D mesh validation, discarding", "tx_id", tx.ID)
		return
	}

	parentBytes := make([][]byte, len(tx.ParentCells))
	for i := range tx.ParentCells {
		parentBytes[i] = tx.ParentCells[i].Data
	}

	if consensus != nil {
		signature, err := consensus.Sign(tx.Data)
		if err != nil {
			logger.Error("Failed to sign transaction", "error", err)
			return
		}
		signatures := [][]byte{signature}
		validConsensus, err := consensus.ValidateTransaction(tx.Data, parentBytes, signatures, logger)
		if err != nil || !validConsensus {
			logger.Warn("Received invalid transaction by consensus, discarding", "tx_id", tx.ID, "error", err)
			return
		}
	} else {
		logger.Debug("Phase3: consensus unavailable, storing after mesh validation only", "tx_id", tx.ID)
	}

	if nvidiaP2PGate != nil && !nvidiaP2PGate.Allows() {
		logger.Warn("Discarding Phase3 P2P transaction: NVIDIA-lock P2P gate not satisfied")
		monitoring.RecordNvidiaLockP2PReject()
		monitoring.GetMetrics().RecordError("NVIDIA-lock P2P gate blocked Phase3 transaction")
		return
	}

	err = storage.StoreTransaction(tx.Data)
	if err != nil {
		logger.Error("Failed to store transaction", "error", err)
		monitoring.GetMetrics().RecordError("Storage failed: " + err.Error())
	} else {
		dedupeCommitted = true
		logger.Info("Phase3 transaction stored successfully", "tx_id", tx.ID, "submesh", submeshKey)
		monitoring.GetMetrics().IncrementTransactionsStored()
	}
}

func ParseTransaction(msg []byte) (*Transaction, error) {
	var tx Transaction
	err := json.Unmarshal(msg, &tx)
	if err != nil {
		return nil, fmt.Errorf("failed to parse transaction JSON: %w (hint: ensure transaction contains valid JSON with required fields: id, sender, recipient, amount, signature)", err)
	}
	
	// Validate required fields with comprehensive validation
	if err := api.ValidateTransactionID(tx.ID); err != nil {
		return nil, fmt.Errorf("transaction ID validation failed: %w", err)
	}
	if err := api.ValidateAddress(tx.Sender); err != nil {
		return nil, fmt.Errorf("sender address validation failed: %w", err)
	}
	if err := api.ValidateAddress(tx.Recipient); err != nil {
		return nil, fmt.Errorf("recipient address validation failed: %w", err)
	}
	if err := api.ValidateAmount(tx.Amount); err != nil {
		return nil, fmt.Errorf("amount validation failed: %w", err)
	}
	if tx.Fee < 0 {
		return nil, errors.New("fee cannot be negative")
	}
	if err := api.ValidateAmount(tx.Fee); err != nil {
		return nil, fmt.Errorf("fee validation failed: %w", err)
	}
	if err := api.ValidateGeoTag(tx.GeoTag); err != nil {
		return nil, fmt.Errorf("geotag validation failed: %w", err)
	}
	if err := api.ValidateParentCells(tx.ParentCells); err != nil {
		return nil, fmt.Errorf("parent cells validation failed: %w", err)
	}
	if err := api.ValidateSignature(tx.Signature); err != nil {
		return nil, fmt.Errorf("signature validation failed: %w", err)
	}
	if err := api.ValidateOptionalMLDSAPublicKeyHex(tx.PublicKey); err != nil {
		return nil, fmt.Errorf("public_key validation failed: %w", err)
	}

	return &tx, nil
}

type Transaction struct {
	ID          string   `json:"id"`
	Sender      string   `json:"sender"`
	Recipient   string   `json:"recipient"`
	Amount      float64  `json:"amount"`
	Fee         float64  `json:"fee"`
	GeoTag      string   `json:"geotag"`
	ParentCells []string `json:"parent_cells"`
	Signature   string   `json:"signature"`
	PublicKey   string   `json:"public_key,omitempty"`
	Timestamp   string   `json:"timestamp"`
}
