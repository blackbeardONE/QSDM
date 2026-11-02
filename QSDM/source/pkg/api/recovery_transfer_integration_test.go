//go:build cgo

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// These tests use disposable keys, synthetic balances and t.TempDir only.
// They exercise a real loopback HTTP server and production transaction, block,
// account, receipt and journal implementations. They do not launch the daemon,
// exercise its middleware, or connect to a live chain.
type recoveryTransferAccountProbe struct{ accounts *chain.AccountStore }

func (p recoveryTransferAccountProbe) BalanceOf(address string) (float64, uint64, bool) {
	account, ok := p.accounts.Get(address)
	if !ok {
		return 0, 0, false
	}
	return account.Balance, account.Nonce, true
}

type recoveryTransferReceiptProbe struct{ receipts *chain.ReceiptStore }

func (p recoveryTransferReceiptProbe) GetReceipt(id string) (TxReceiptView, bool) {
	r, ok := p.receipts.Get(id)
	if !ok {
		return TxReceiptView{}, false
	}
	return TxReceiptView{TxID: r.TxID, BlockHeight: r.BlockHeight, BlockHash: r.BlockHash, Status: uint8(r.Status), Fee: r.Fee}, true
}

type recoveryTransferFixture struct {
	dir                                       string
	wallet, otherWallet, oldSigner, newSigner *wallet.WalletService
	accounts                                  *chain.AccountStore
	aware                                     *chain.EnrollmentAwareApplier
	pool                                      *mempool.Mempool
	producer                                  *chain.BlockProducer
	receipts                                  *chain.ReceiptStore
	secondary                                 *storage.Storage
	server                                    *httptest.Server
	journal                                   *chain.ChainJournal
	policy                                    *producerpolicy.Transition
	recipient                                 string
}

func recoveryTestWallet(t *testing.T) *wallet.WalletService {
	t.Helper()
	w, err := wallet.NewWalletService()
	if err != nil {
		t.Fatalf("real ML-DSA wallet required: %v", err)
	}
	t.Cleanup(func() { w.ConsensusSigner().Free() })
	return w
}

func newRecoveryTransferFixture(t *testing.T) *recoveryTransferFixture {
	t.Helper()
	f := &recoveryTransferFixture{dir: t.TempDir(), wallet: recoveryTestWallet(t), otherWallet: recoveryTestWallet(t), oldSigner: recoveryTestWallet(t), newSigner: recoveryTestWallet(t), recipient: recoveryTestWallet(t).GetAddress()}
	f.accounts = chain.NewAccountStore()
	f.accounts.Credit(f.wallet.GetAddress(), 100)
	f.accounts.Credit(f.otherWallet.GetAddress(), 25)
	f.accounts.Credit(f.oldSigner.GetAddress(), 2)
	f.aware = chain.NewEnrollmentAwareApplier(f.accounts, nil)
	oldPool := mempool.New(mempool.DefaultConfig())
	oldProducer := chain.NewBlockProducer(oldPool, f.aware, chain.DefaultProducerConfig())
	oldProducer.SetBlockSigner(f.oldSigner.ConsensusSigner())
	var historical []*chain.Block
	var prefix []byte
	for i := 0; i < 2; i++ {
		env := recoveryResign(t, f.oldSigner, wallet.TransactionData{ID: fmt.Sprintf("historical-%d", i), Sender: f.oldSigner.GetAddress(), Recipient: f.oldSigner.GetAddress(), Amount: 1, Nonce: uint64(i + 1)})
		tx, err := walletTransferMempoolTx(env)
		if err != nil {
			t.Fatal(err)
		}
		if err := oldPool.Add(tx); err != nil {
			t.Fatal(err)
		}
		b, err := oldProducer.ProduceBlock()
		if err != nil {
			t.Fatal(err)
		}
		historical = append(historical, b)
		raw, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		prefix = append(prefix, append(raw, '\n')...)
	}
	f.policy = &producerpolicy.Transition{Version: 1, CheckpointHeight: 1, CheckpointHash: historical[1].Hash, HistoricalSignatureHeight: 1, HistoricalProducer: f.oldSigner.GetAddress(), ReplacementProducer: f.newSigner.GetAddress(), EffectiveHeight: 2, HistoricalPrefixBytes: int64(len(prefix)), HistoricalPrefixSHA256: fmt.Sprintf("%x", sha256.Sum256(prefix))}
	if err := os.WriteFile(filepath.Join(f.dir, "chain.ndjson"), prefix, 0600); err != nil {
		t.Fatal(err)
	}
	f.pool = mempool.New(mempool.DefaultConfig())
	f.producer = chain.NewBlockProducer(f.pool, f.aware, chain.DefaultProducerConfig())
	if err := f.producer.SetProducerTransition(f.policy); err != nil {
		t.Fatal(err)
	}
	if err := f.producer.RestoreChain(historical); err != nil {
		t.Fatal(err)
	}
	f.producer.SetBlockSigner(f.newSigner.ConsensusSigner())
	f.receipts = chain.NewReceiptStore()
	f.producer.SetAppendReceiptStore(f.receipts)
	var err error
	f.journal, err = chain.OpenChainJournal(filepath.Join(f.dir, "chain.ndjson"), historical[1])
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.journal.Close() })
	f.producer.OnSealedBlock = func(b *chain.Block) {
		if err := f.journal.Append(b); err != nil {
			t.Fatal(err)
		}
		if err := f.accounts.Save(filepath.Join(f.dir, "accounts.json")); err != nil {
			t.Fatal(err)
		}
		if err := f.receipts.Save(filepath.Join(f.dir, "receipts.json")); err != nil {
			t.Fatal(err)
		}
	}
	f.secondary, err = storage.NewStorage(filepath.Join(f.dir, "secondary.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.secondary.Close() })
	logger := logging.NewLogger(filepath.Join(f.dir, "http.log"), false)
	t.Cleanup(func() { _ = logger.Close() })
	h := NewHandlers(nil, nil, f.wallet, f.secondary, logger, "", false, 0, "", "", false, 0, false, nil)
	SetLocalWalletTransferLedger(nil)
	SetMiningAccountProbe(recoveryTransferAccountProbe{f.accounts})
	SetWalletTransferMempool(f.producer.WalletTransferSubmitter())
	SetMiningReceiptProbe(recoveryTransferReceiptProbe{f.receipts})
	t.Cleanup(func() {
		SetLocalWalletTransferLedger(nil)
		SetMiningAccountProbe(nil)
		SetWalletTransferMempool(nil)
		SetMiningReceiptProbe(nil)
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/wallet/submit-signed", h.SubmitSignedTransaction)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *recoveryTransferFixture) envelope(t *testing.T, id string, nonce uint64, amount, fee float64) wallet.TransactionData {
	t.Helper()
	env := wallet.TransactionData{ID: id, Sender: f.wallet.GetAddress(), Recipient: f.recipient, Amount: amount, Fee: fee, GeoTag: "US", ParentCells: []string{strings.Repeat("a", 32), strings.Repeat("b", 32)}, Nonce: nonce, Timestamp: time.Now().UTC().Format(time.RFC3339)}
	return recoveryResign(t, f.wallet, env)
}
func recoveryResign(t *testing.T, w *wallet.WalletService, env wallet.TransactionData) wallet.TransactionData {
	t.Helper()
	env.Signature = ""
	env.PublicKey = ""
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := w.SignData(raw)
	if err != nil {
		t.Fatal(err)
	}
	env.Signature = hex.EncodeToString(sig)
	env.PublicKey = hex.EncodeToString(w.GetPublicKey())
	return env
}
func (f *recoveryTransferFixture) submit(t *testing.T, env wallet.TransactionData, want int) {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	response, err := f.server.Client().Post(f.server.URL+"/api/v1/wallet/submit-signed", "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 65536))
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("HTTP status=%d want=%d body=%s", response.StatusCode, want, body)
	}
}
func recoveryAssertAccount(t *testing.T, accounts *chain.AccountStore, address string, wantBalance float64, wantNonce uint64) {
	t.Helper()
	a, ok := accounts.Get(address)
	if !ok || a.Balance != wantBalance || a.Nonce != wantNonce {
		t.Fatalf("account mismatch: present=%v account=%+v want balance=%g nonce=%d", ok, a, wantBalance, wantNonce)
	}
}
func recoveryAssertReceipt(t *testing.T, receipts *chain.ReceiptStore, id string, b *chain.Block) {
	t.Helper()
	r, ok := receipts.Get(id)
	if !ok || r.Status != chain.ReceiptSuccess || r.BlockHeight != b.Height || r.BlockHash != b.Hash {
		t.Fatalf("committed receipt mismatch: %+v", r)
	}
}

func TestRecoveryTransferHTTPCommitFollowerAndRestart(t *testing.T) {
	f := newRecoveryTransferFixture(t)
	// Activate content commitments for the synthetic replacement suffix too.
	oldContentHeight := chain.TxContentRootActivationHeight()
	chain.SetTxContentRootActivationHeight(2)
	t.Cleanup(func() { chain.SetTxContentRootActivationHeight(oldContentHeight) })
	initialRoot := f.aware.StateRoot()
	good := f.envelope(t, "recovery-transfer-1", 1, 10, 0.25)
	t.Run("bad signature changes nothing", func(t *testing.T) { bad := good; bad.Amount = 11; f.submit(t, bad, http.StatusUnprocessableEntity) })
	t.Run("wrong sender changes nothing", func(t *testing.T) { bad := good; bad.Sender = f.recipient; f.submit(t, bad, http.StatusBadRequest) })
	t.Run("amount plus fee over balance rejected", func(t *testing.T) { f.submit(t, f.envelope(t, "overdraw", 1, 100, 0.25), http.StatusPaymentRequired) })
	t.Run("nonce gap rejected", func(t *testing.T) { f.submit(t, f.envelope(t, "gap", 2, 10, 0.25), http.StatusConflict) })
	t.Run("legacy nonce zero rejected", func(t *testing.T) { f.submit(t, f.envelope(t, "legacy", 0, 10, 0.25), http.StatusBadRequest) })
	if f.aware.StateRoot() != initialRoot || f.pool.Size() != 0 {
		t.Fatal("rejected requests changed canonical state or queue")
	}
	t.Run("accepted only into queue", func(t *testing.T) {
		f.submit(t, good, http.StatusAccepted)
		if f.aware.StateRoot() != initialRoot || f.pool.Size() != 1 {
			t.Fatal("admission mutated balances or failed to queue")
		}
		if _, ok := f.receipts.Get(good.ID); ok {
			t.Fatal("receipt exists before commit")
		}
		balance, err := f.secondary.GetBalance(f.wallet.GetAddress())
		if err != nil {
			t.Fatal(err)
		}
		if balance != 0 {
			t.Fatal("secondary SQLite was used as spend authority")
		}
	})
	t.Run("duplicate pending ID rejected", func(t *testing.T) { f.submit(t, good, http.StatusConflict) })
	t.Run("competing same nonce rejected", func(t *testing.T) {
		f.submit(t, f.envelope(t, "different-id-same-nonce", 1, 3, 0.25), http.StatusConflict)
	})
	block, err := f.producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if block.Height != 2 || len(block.Transactions) != 1 || block.Transactions[0].ID != good.ID || block.ProducerID != f.newSigner.GetAddress() {
		t.Fatal("transfer not committed by replacement producer")
	}
	if err := chain.VerifyBlockSignature(block); err != nil {
		t.Fatal(err)
	}
	if f.pool.Size() != 0 {
		t.Fatal("committed transfer left in mempool")
	}
	recoveryAssertAccount(t, f.accounts, f.wallet.GetAddress(), 89.75, 1)
	recoveryAssertAccount(t, f.accounts, f.recipient, 10, 0)
	recoveryAssertReceipt(t, f.receipts, good.ID, block)
	t.Run("committed envelope replay rejected", func(t *testing.T) { f.submit(t, good, http.StatusConflict) })
	if err := f.policy.ValidateJournalPrefix(filepath.Join(f.dir, "chain.ndjson")); err != nil {
		t.Fatal(err)
	}
	if err := f.journal.Close(); err != nil {
		t.Fatal(err)
	}
	blocks, err := chain.LoadChainNDJSON(filepath.Join(f.dir, "chain.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("journal blocks=%d want=3", len(blocks))
	}
	t.Run("independent follower replays serialized blocks", func(t *testing.T) {
		accounts := chain.NewAccountStore()
		accounts.Credit(f.wallet.GetAddress(), 100)
		accounts.Credit(f.otherWallet.GetAddress(), 25)
		accounts.Credit(f.oldSigner.GetAddress(), 2)
		aware := chain.NewEnrollmentAwareApplier(accounts, nil)
		follower := chain.NewBlockProducer(mempool.New(mempool.DefaultConfig()), aware, chain.DefaultProducerConfig())
		if err := follower.SetProducerTransition(f.policy); err != nil {
			t.Fatal(err)
		}
		receipts := chain.NewReceiptStore()
		follower.SetAppendReceiptStore(receipts)
		followerDir := t.TempDir()
		j, err := chain.OpenChainJournal(filepath.Join(followerDir, "chain.ndjson"), nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = j.Close() })
		follower.OnSealedBlock = func(b *chain.Block) {
			if err := j.Append(b); err != nil {
				t.Fatal(err)
			}
			if err := accounts.Save(filepath.Join(followerDir, "accounts.json")); err != nil {
				t.Fatal(err)
			}
			if err := receipts.Save(filepath.Join(followerDir, "receipts.json")); err != nil {
				t.Fatal(err)
			}
		}
		for _, b := range blocks {
			if err := follower.TryAppendExternalBlock(b); err != nil {
				t.Fatalf("follower height=%d: %v", b.Height, err)
			}
		}
		if aware.StateRoot() != block.StateRoot || aware.StateRoot() != f.aware.StateRoot() {
			t.Fatal("follower state root diverged")
		}
		recoveryAssertAccount(t, accounts, f.wallet.GetAddress(), 89.75, 1)
		recoveryAssertAccount(t, accounts, f.recipient, 10, 0)
		recoveryAssertReceipt(t, receipts, good.ID, block)
		if err := follower.TryAppendExternalBlock(blocks[2]); err != nil {
			t.Fatal(err)
		}
		recoveryAssertAccount(t, accounts, f.wallet.GetAddress(), 89.75, 1)
		if len(receipts.GetByBlock(2)) != 1 {
			t.Fatal("duplicate block produced duplicate receipts")
		}
		if err := j.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.policy.ValidateJournalPrefix(filepath.Join(followerDir, "chain.ndjson")); err != nil {
			t.Fatal(err)
		}
		replayed, err := chain.LoadChainNDJSON(filepath.Join(followerDir, "chain.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		if len(replayed) != 3 || replayed[2].Hash != block.Hash {
			t.Fatal("follower durable journal diverged")
		}
		restoredAccounts := chain.NewAccountStore()
		if _, err := restoredAccounts.Load(filepath.Join(followerDir, "accounts.json")); err != nil {
			t.Fatal(err)
		}
		restoredReceipts := chain.NewReceiptStore()
		if _, err := restoredReceipts.Load(filepath.Join(followerDir, "receipts.json")); err != nil {
			t.Fatal(err)
		}
		restoredAware := chain.NewEnrollmentAwareApplier(restoredAccounts, nil)
		restoredAware.SetStateRootHeight(block.Height)
		restoredFollower := chain.NewBlockProducer(mempool.New(mempool.DefaultConfig()), restoredAware, chain.DefaultProducerConfig())
		if err := restoredFollower.SetProducerTransition(f.policy); err != nil {
			t.Fatal(err)
		}
		if err := restoredFollower.RestoreChain(replayed); err != nil {
			t.Fatal(err)
		}
		if restoredAware.StateRoot() != block.StateRoot {
			t.Fatal("follower persisted state root diverged")
		}
		recoveryAssertAccount(t, restoredAccounts, f.wallet.GetAddress(), 89.75, 1)
		recoveryAssertAccount(t, restoredAccounts, f.recipient, 10, 0)
		recoveryAssertAccount(t, restoredAccounts, f.otherWallet.GetAddress(), 25, 0)
		recoveryAssertReceipt(t, restoredReceipts, good.ID, block)
	})
	t.Run("restart reloads committed balances receipts and nonce", func(t *testing.T) {
		accounts := chain.NewAccountStore()
		if _, err := accounts.Load(filepath.Join(f.dir, "accounts.json")); err != nil {
			t.Fatal(err)
		}
		receipts := chain.NewReceiptStore()
		if _, err := receipts.Load(filepath.Join(f.dir, "receipts.json")); err != nil {
			t.Fatal(err)
		}
		aware := chain.NewEnrollmentAwareApplier(accounts, nil)
		restartedPool := mempool.New(mempool.DefaultConfig())
		restarted := chain.NewBlockProducer(restartedPool, aware, chain.DefaultProducerConfig())
		if err := restarted.SetProducerTransition(f.policy); err != nil {
			t.Fatal(err)
		}
		if err := restarted.RestoreChain(blocks); err != nil {
			t.Fatal(err)
		}
		aware.SetStateRootHeight(block.Height)
		if aware.StateRoot() != block.StateRoot {
			t.Fatal("persisted account root differs from journal tip")
		}
		recoveryAssertAccount(t, accounts, f.wallet.GetAddress(), 89.75, 1)
		recoveryAssertAccount(t, accounts, f.recipient, 10, 0)
		recoveryAssertReceipt(t, receipts, good.ID, block)
		restarted.SetBlockSigner(f.newSigner.ConsensusSigner())
		restarted.SetAppendReceiptStore(receipts)
		SetWalletTransferMempool(restarted.WalletTransferSubmitter())
		SetMiningAccountProbe(recoveryTransferAccountProbe{accounts})
		SetMiningReceiptProbe(recoveryTransferReceiptProbe{receipts})
		f.submit(t, good, http.StatusConflict)
		f.submit(t, f.envelope(t, good.ID, 2, 1, 0.25), http.StatusConflict)
		crossSender := f.envelope(t, good.ID, 1, 1, 0.25)
		crossSender.Sender = f.otherWallet.GetAddress()
		f.submit(t, recoveryResign(t, f.otherWallet, crossSender), http.StatusConflict)
		if restartedPool.Size() != 0 {
			t.Fatal("restart accepted an envelope or committed ID replay")
		}
		recoveryAssertReceipt(t, receipts, good.ID, block)
		if accounts.StateRoot() != f.accounts.StateRoot() {
			t.Fatal("restart replay changed balances")
		}
		// Fresh admission after restoring both chain and side-state proves the
		// restart guard rejects replays without disabling legitimate transfers.
		next := f.envelope(t, "recovery-transfer-after-restart", 2, 5, 0.25)
		f.submit(t, next, http.StatusAccepted)
		restartJournal, err := chain.OpenChainJournal(filepath.Join(f.dir, "chain.ndjson"), block)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = restartJournal.Close() })
		restarted.OnSealedBlock = func(b *chain.Block) {
			if err := restartJournal.Append(b); err != nil {
				t.Fatal(err)
			}
			if err := accounts.Save(filepath.Join(f.dir, "accounts.json")); err != nil {
				t.Fatal(err)
			}
			if err := receipts.Save(filepath.Join(f.dir, "receipts.json")); err != nil {
				t.Fatal(err)
			}
		}
		nextBlock, err := restarted.ProduceBlock()
		if err != nil {
			t.Fatal(err)
		}
		if nextBlock.Height != 3 || nextBlock.PrevHash != block.Hash || len(nextBlock.Transactions) != 1 || nextBlock.Transactions[0].ID != next.ID {
			t.Fatal("restart did not extend the durable transfer block")
		}
		if err := chain.VerifyBlockSignature(nextBlock); err != nil {
			t.Fatal(err)
		}
		recoveryAssertAccount(t, accounts, f.wallet.GetAddress(), 84.5, 2)
		recoveryAssertAccount(t, accounts, f.recipient, 15, 0)
		recoveryAssertAccount(t, accounts, f.otherWallet.GetAddress(), 25, 0)
		recoveryAssertReceipt(t, receipts, good.ID, block)
		recoveryAssertReceipt(t, receipts, next.ID, nextBlock)
		if err := restartJournal.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.policy.ValidateJournalPrefix(filepath.Join(f.dir, "chain.ndjson")); err != nil {
			t.Fatal(err)
		}
		durable, err := chain.LoadChainNDJSON(filepath.Join(f.dir, "chain.ndjson"))
		if err != nil {
			t.Fatal(err)
		}
		if len(durable) != 4 || durable[3].Hash != nextBlock.Hash {
			t.Fatal("post-restart journal diverged")
		}
		restoredAccounts := chain.NewAccountStore()
		if _, err := restoredAccounts.Load(filepath.Join(f.dir, "accounts.json")); err != nil {
			t.Fatal(err)
		}
		restoredAware := chain.NewEnrollmentAwareApplier(restoredAccounts, nil)
		restoredAware.SetStateRootHeight(nextBlock.Height)
		if restoredAware.StateRoot() != nextBlock.StateRoot {
			t.Fatal("post-restart persisted state root diverged")
		}
	})
}

// Re-signing with a new nonce cannot reuse a committed transaction's ID:
// receipt lookup is globally indexed by that ID and must remain unambiguous.
func TestRecoveryTransferRejectsCommittedIDReuse(t *testing.T) {
	f := newRecoveryTransferFixture(t)
	first := f.envelope(t, "committed-id", 1, 10, 0.25)
	f.submit(t, first, http.StatusAccepted)
	block, err := f.producer.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	recoveryAssertReceipt(t, f.receipts, first.ID, block)
	root := f.aware.StateRoot()
	originalReceipt, ok := f.receipts.Get(first.ID)
	if !ok {
		t.Fatal("original receipt absent")
	}
	originalReceiptJSON, err := json.Marshal(originalReceipt)
	if err != nil {
		t.Fatal(err)
	}
	t.Run("same sender fresh nonce", func(t *testing.T) {
		f.submit(t, f.envelope(t, first.ID, 2, 1, 0.25), http.StatusConflict)
	})
	t.Run("different funded sender fresh nonce", func(t *testing.T) {
		env := f.envelope(t, first.ID, 1, 1, 0.25)
		env.Sender = f.otherWallet.GetAddress()
		f.submit(t, recoveryResign(t, f.otherWallet, env), http.StatusConflict)
	})
	if f.aware.StateRoot() != root || f.pool.Size() != 0 {
		t.Fatal("committed ID reuse mutated state or queue")
	}
	retained, ok := f.receipts.Get(first.ID)
	if !ok {
		t.Fatal("rejected reuse removed original receipt")
	}
	retainedJSON, err := json.Marshal(retained)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalReceiptJSON, retainedJSON) || len(f.receipts.GetByBlock(block.Height)) != 1 {
		t.Fatal("rejected reuse overwrote or duplicated original receipt")
	}
	recoveryAssertAccount(t, f.accounts, f.wallet.GetAddress(), 89.75, 1)
	recoveryAssertAccount(t, f.accounts, f.otherWallet.GetAddress(), 25, 0)
}
