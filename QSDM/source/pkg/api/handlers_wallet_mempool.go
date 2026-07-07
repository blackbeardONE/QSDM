package api

import (
	"encoding/json"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

type walletTransferSubmitterHolder struct {
	mu   sync.RWMutex
	pool MempoolSubmitter
}

var walletTransferPoolHolder = &walletTransferSubmitterHolder{}

// SetWalletTransferMempool installs the canonical block-admission path used
// by signed CELL transfers. A nil submitter retains the legacy storage path
// for isolated API tests and non-validator tools.
func SetWalletTransferMempool(pool MempoolSubmitter) {
	walletTransferPoolHolder.mu.Lock()
	defer walletTransferPoolHolder.mu.Unlock()
	walletTransferPoolHolder.pool = pool
}

func currentWalletTransferMempool() MempoolSubmitter {
	walletTransferPoolHolder.mu.RLock()
	defer walletTransferPoolHolder.mu.RUnlock()
	return walletTransferPoolHolder.pool
}

func walletTransferMempoolTx(env wallet.TransactionData) (*mempool.Tx, error) {
	payload, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	return &mempool.Tx{
		ID:         env.ID,
		Sender:     env.Sender,
		Recipient:  env.Recipient,
		Amount:     env.Amount,
		Fee:        env.Fee,
		Nonce:      env.Nonce - 1,
		Payload:    payload,
		ContractID: chain.WalletTransferContractID,
		Signature:  env.Signature,
		PublicKey:  env.PublicKey,
	}, nil
}
