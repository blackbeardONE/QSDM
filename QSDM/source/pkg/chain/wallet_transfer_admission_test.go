package chain

import (
	"errors"
	"fmt"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func admissionTestTx(id, sender string, nonce uint64) *mempool.Tx {
	return &mempool.Tx{ID: id, Sender: sender, Recipient: "recipient", Amount: 1, Nonce: nonce, ContractID: WalletTransferContractID}
}
func admissionTestProducer() (*BlockProducer, *mempool.Mempool, *ReceiptStore) {
	pool := mempool.New(mempool.DefaultConfig())
	accounts := NewAccountStore()
	accounts.Credit("alice", 10)
	bp := NewBlockProducer(pool, accounts, DefaultProducerConfig())
	receipts := NewReceiptStore()
	bp.SetAppendReceiptStore(receipts)
	return bp, pool, receipts
}
func TestWalletTransferAdmissionRejectsRecordedIDs(t *testing.T) {
	for _, status := range []ReceiptStatus{ReceiptSuccess, ReceiptFailed} {
		for _, contract := range []string{WalletTransferContractID, "historical-other-contract"} {
			for _, sender := range []string{"alice", "other-sender"} {
				t.Run(fmt.Sprintf("%d/%s/%s", status, contract, sender), func(t *testing.T) {
					bp, pool, receipts := admissionTestProducer()
					existing := &TxReceipt{TxID: "reserved-id", BlockHeight: 7, BlockHash: "old-block", Status: status, ContractID: contract}
					receipts.Store(existing)
					if err := bp.WalletTransferSubmitter().Add(admissionTestTx(existing.TxID, sender, 9)); !errors.Is(err, mempool.ErrDuplicateTx) {
						t.Fatalf("Add = %v, want ErrDuplicateTx", err)
					}
					if got, _ := receipts.Get(existing.TxID); got != existing || receipts.Count() != 1 || pool.Size() != 0 {
						t.Fatal("rejected ID reuse changed receipts or mempool")
					}
				})
			}
		}
	}
}
func TestWalletTransferAdmissionFailsClosedWhenUnwiredOrWrongContract(t *testing.T) {
	valid := admissionTestTx("new-id", "alice", 0)
	bp, pool, receipts := admissionTestProducer()
	missingPool := NewBlockProducer(nil, NewAccountStore(), DefaultProducerConfig())
	missingPool.SetAppendReceiptStore(receipts)
	for _, tc := range []struct {
		name      string
		submitter *walletTransferSubmitter
		tx        *mempool.Tx
	}{
		{"nil-adapter", nil, valid},
		{"nil-producer", (*BlockProducer)(nil).WalletTransferSubmitter(), valid},
		{"missing-receipts", NewBlockProducer(pool, NewAccountStore(), DefaultProducerConfig()).WalletTransferSubmitter(), valid},
		{"missing-pool", missingPool.WalletTransferSubmitter(), valid},
		{"nil-tx", bp.WalletTransferSubmitter(), nil},
		{"empty-id", bp.WalletTransferSubmitter(), admissionTestTx("", "alice", 0)},
		{"wrong-contract", bp.WalletTransferSubmitter(), &mempool.Tx{ID: "wrong", ContractID: "other"}},
		{"missing-contract", bp.WalletTransferSubmitter(), &mempool.Tx{ID: "legacy"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.submitter.Add(tc.tx); err == nil {
				t.Fatal("unwired or invalid admission accepted")
			}
		})
	}
	if pool.Size() != 0 {
		t.Fatal("rejected input reached pool")
	}
}
func TestWalletTransferAdmissionPreservesPoolChecks(t *testing.T) {
	bp, pool, _ := admissionTestProducer()
	s := bp.WalletTransferSubmitter()
	if err := s.Add(admissionTestTx("pending", "alice", 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(admissionTestTx("pending", "bob", 1)); !errors.Is(err, mempool.ErrDuplicateTx) {
		t.Fatalf("pending ID = %v", err)
	}
	if err := s.Add(admissionTestTx("other-id", "alice", 0)); !errors.Is(err, mempool.ErrNonceAlreadyPending) {
		t.Fatalf("pending nonce = %v", err)
	}
	gateErr := errors.New("test admission gate closed")
	pool.SetAdmissionChecker(func(*mempool.Tx) error { return gateErr })
	if err := s.Add(admissionTestTx("fresh", "bob", 1)); !errors.Is(err, gateErr) {
		t.Fatalf("admission gate = %v", err)
	}
	if pool.Size() != 1 {
		t.Fatalf("pool size = %d, want 1", pool.Size())
	}
}
func TestWalletTransferAdmissionRejectsPersistedReceiptAfterRestart(t *testing.T) {
	bp, _, receipts := admissionTestProducer()
	if err := bp.WalletTransferSubmitter().Add(admissionTestTx("durable-id", "alice", 0)); err != nil {
		t.Fatal(err)
	}
	block, err := bp.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "receipts.ndjson")
	if n, err := receipts.AppendBlockNDJSON(path, block.Height); err != nil || n != 1 {
		t.Fatalf("persist receipts = %d, %v", n, err)
	}
	restored := NewReceiptStore()
	if n, err := restored.LoadNDJSON(path); err != nil || n != 1 {
		t.Fatalf("load receipts = %d, %v", n, err)
	}
	restarted, pool, _ := admissionTestProducer()
	restarted.SetAppendReceiptStore(restored)
	if err := restarted.WalletTransferSubmitter().Add(admissionTestTx("durable-id", "another-sender", 42)); !errors.Is(err, mempool.ErrDuplicateTx) {
		t.Fatalf("restarted Add = %v", err)
	}
	if pool.Size() != 0 {
		t.Fatal("restarted producer admitted a persisted ID")
	}
}

type walletAdmissionBarrierApplier struct {
	*AccountStore
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (a *walletAdmissionBarrierApplier) ApplyTx(tx *mempool.Tx) error {
	a.once.Do(func() { close(a.entered); <-a.release })
	return a.AccountStore.ApplyTx(tx)
}
func admissionWaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for lifecycle barrier")
	}
}
func admissionWaitResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for admission/lifecycle result")
		return nil
	}
}
func admissionAssertBlocked(t *testing.T, ch <-chan error) {
	t.Helper()
	select {
	case err := <-ch:
		t.Fatalf("admission crossed active lifecycle barrier: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
}
func TestWalletTransferAdmissionWaitsFromDrainThroughPersistence(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	accounts := NewAccountStore()
	accounts.Credit("alice", 10)
	applyRelease := make(chan struct{})
	var releaseApply sync.Once
	defer releaseApply.Do(func() { close(applyRelease) })
	applier := &walletAdmissionBarrierApplier{AccountStore: accounts, entered: make(chan struct{}), release: applyRelease}
	bp := NewBlockProducer(pool, applier, DefaultProducerConfig())
	receipts := NewReceiptStore()
	bp.SetAppendReceiptStore(receipts)
	persistEntered, persistRelease := make(chan struct{}), make(chan struct{})
	var releasePersist sync.Once
	defer releasePersist.Do(func() { close(persistRelease) })
	path := filepath.Join(t.TempDir(), "receipts.ndjson")
	persistResult := make(chan error, 1)
	bp.OnSealedBlock = func(block *Block) {
		close(persistEntered)
		<-persistRelease
		_, err := receipts.AppendBlockNDJSON(path, block.Height)
		persistResult <- err
	}
	if err := bp.WalletTransferSubmitter().Add(admissionTestTx("racing-id", "alice", 0)); err != nil {
		t.Fatal(err)
	}
	sealed := make(chan error, 1)
	go func() { _, err := bp.ProduceBlock(); sealed <- err }()
	admissionWaitSignal(t, applier.entered)
	if pool.Size() != 0 {
		t.Fatal("expected transaction already drained")
	}
	reused, started := make(chan error, 1), make(chan struct{})
	go func() {
		close(started)
		reused <- bp.WalletTransferSubmitter().Add(admissionTestTx("racing-id", "other-sender", 0))
	}()
	admissionWaitSignal(t, started)
	admissionAssertBlocked(t, reused)
	releaseApply.Do(func() { close(applyRelease) })
	admissionWaitSignal(t, persistEntered)
	admissionAssertBlocked(t, reused)
	releasePersist.Do(func() { close(persistRelease) })
	if err := admissionWaitResult(t, sealed); err != nil {
		t.Fatal(err)
	}
	if err := admissionWaitResult(t, persistResult); err != nil {
		t.Fatal(err)
	}
	if err := admissionWaitResult(t, reused); !errors.Is(err, mempool.ErrDuplicateTx) {
		t.Fatalf("racing Add = %v", err)
	}
	if pool.Size() != 0 || receipts.Count() != 1 {
		t.Fatal("drain race duplicated pool entry or receipt")
	}
	reloaded := NewReceiptStore()
	if n, err := reloaded.LoadNDJSON(path); err != nil || n != 1 {
		t.Fatalf("durable receipts = %d, %v", n, err)
	}
}
func TestWalletTransferAdmissionWaitsThroughExternalAppendHooks(t *testing.T) {
	source, _, _ := admissionTestProducer()
	if err := source.WalletTransferSubmitter().Add(admissionTestTx("external-id", "alice", 0)); err != nil {
		t.Fatal(err)
	}
	block, err := source.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	destination, pool, receipts := admissionTestProducer()
	if err := destination.WalletTransferSubmitter().Add(admissionTestTx("external-id", "alice", 0)); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	destination.OnSealedBlock = func(*Block) { close(entered); <-release }
	appended := make(chan error, 1)
	go func() { appended <- destination.TryAppendExternalBlock(block) }()
	admissionWaitSignal(t, entered)
	if pool.Size() != 0 {
		t.Fatal("external append did not remove pending ID")
	}
	result, started := make(chan error, 1), make(chan struct{})
	go func() {
		close(started)
		result <- destination.WalletTransferSubmitter().Add(admissionTestTx("external-id", "other-sender", 3))
	}()
	admissionWaitSignal(t, started)
	admissionAssertBlocked(t, result)
	once.Do(func() { close(release) })
	if err := admissionWaitResult(t, appended); err != nil {
		t.Fatal(err)
	}
	if err := admissionWaitResult(t, result); !errors.Is(err, mempool.ErrDuplicateTx) {
		t.Fatalf("Add after external append = %v", err)
	}
	if pool.Size() != 0 || receipts.Count() != 1 {
		t.Fatal("external append race duplicated pool entry or receipt")
	}
}
func TestWalletTransferAdmissionCallbackCanReadProducer(t *testing.T) {
	bp, pool, _ := admissionTestProducer()
	callbackCalled := false
	pool.SetAdmissionChecker(func(*mempool.Tx) error { bp.LatestBlock(); bp.ChainHeight(); callbackCalled = true; return nil })
	result := make(chan error, 1)
	go func() { result <- bp.WalletTransferSubmitter().Add(admissionTestTx("callback", "alice", 0)) }()
	if err := admissionWaitResult(t, result); err != nil {
		t.Fatal(err)
	}
	if !callbackCalled || pool.Size() != 1 {
		t.Fatal("producer-reading callback did not complete")
	}
}
