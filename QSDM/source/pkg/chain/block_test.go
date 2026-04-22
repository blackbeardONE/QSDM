package chain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// testApplier is a simple state applier for testing.
type testApplier struct {
	balances map[string]float64
}

func newTestApplier() *testApplier {
	return &testApplier{balances: map[string]float64{"alice": 10000}}
}

func (ta *testApplier) ApplyTx(tx *mempool.Tx) error {
	if ta.balances[tx.Sender] < tx.Amount+tx.Fee {
		return fmt.Errorf("insufficient balance")
	}
	ta.balances[tx.Sender] -= tx.Amount + tx.Fee
	ta.balances[tx.Recipient] += tx.Amount
	return nil
}

func (ta *testApplier) StateRoot() string {
	h := sha256.Sum256([]byte(fmt.Sprint(ta.balances)))
	return hex.EncodeToString(h[:])
}

func (ta *testApplier) ChainReplayClone() ChainReplayApplier {
	out := &testApplier{balances: make(map[string]float64)}
	for k, v := range ta.balances {
		out.balances[k] = v
	}
	return out
}

func (ta *testApplier) RestoreFromChainReplay(from ChainReplayApplier) error {
	other, ok := from.(*testApplier)
	if !ok || other == nil {
		return fmt.Errorf("replay restore: expected *testApplier")
	}
	ta.balances = make(map[string]float64)
	for k, v := range other.balances {
		ta.balances[k] = v
	}
	return nil
}

func makeTx(id string, fee float64) *mempool.Tx {
	return &mempool.Tx{ID: id, Sender: "alice", Recipient: "bob", Amount: 1, Fee: fee}
}

func TestBlockProducer_ProduceBlock(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	pool.Add(makeTx("tx1", 5.0))
	pool.Add(makeTx("tx2", 3.0))
	pool.Add(makeTx("tx3", 1.0))

	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())

	block, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if block.Height != 0 {
		t.Fatalf("expected height 0, got %d", block.Height)
	}
	if len(block.Transactions) != 3 {
		t.Fatalf("expected 3 txs, got %d", len(block.Transactions))
	}
	if block.Hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if block.StateRoot == "" {
		t.Fatal("expected non-empty state root")
	}
	if block.TotalFees != 9.0 {
		t.Fatalf("expected fees 9.0, got %f", block.TotalFees)
	}
}

func TestBlockProducer_ChainLinking(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())

	pool.Add(makeTx("tx1", 1.0))
	b1, _ := bp.ProduceBlock()

	pool.Add(makeTx("tx2", 2.0))
	b2, _ := bp.ProduceBlock()

	if b2.PrevHash != b1.Hash {
		t.Fatal("block 2 should reference block 1's hash")
	}
	if b2.Height != 1 {
		t.Fatalf("expected height 1, got %d", b2.Height)
	}
}

func TestBlockProducer_TryAppendExternalBlockGenesis(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())
	sr := as.StateRoot()
	blk := &Block{
		Height: 0, PrevHash: "", Timestamp: time.Unix(1700000100, 0),
		Transactions: nil, StateRoot: sr, ProducerID: "peer",
	}
	blk.Hash = computeBlockHash(blk)
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatal(err)
	}
	if h := bp.ChainHeight(); h != 0 {
		t.Fatalf("expected tip height 0, got %d", h)
	}
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatalf("idempotent second append: %v", err)
	}
}

func TestBlockProducer_TryAppendExternalBlockReplayApplier(t *testing.T) {
	ta := newTestApplier()
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, ta, DefaultProducerConfig())
	sr := ta.StateRoot()
	blk := &Block{
		Height: 0, PrevHash: "", Timestamp: time.Unix(1700000400, 0),
		Transactions: nil, StateRoot: sr, ProducerID: "peer",
	}
	blk.Hash = computeBlockHash(blk)
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatal(err)
	}
	if ta.StateRoot() != sr {
		t.Fatalf("state root changed unexpectedly")
	}
}

func TestBlockProducer_TryAppendExternalBlockConflict(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 100)
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())
	sr := as.StateRoot()
	blkA := &Block{
		Height: 0, PrevHash: "", Timestamp: time.Unix(1700000100, 0),
		Transactions: nil, StateRoot: sr, ProducerID: "peer-a",
	}
	blkA.Hash = computeBlockHash(blkA)
	if err := bp.TryAppendExternalBlock(blkA); err != nil {
		t.Fatal(err)
	}
	blkB := &Block{
		Height: 0, PrevHash: "", Timestamp: time.Unix(1700000199, 0),
		Transactions: nil, StateRoot: sr, ProducerID: "peer-b",
	}
	blkB.Hash = computeBlockHash(blkB)
	err := bp.TryAppendExternalBlock(blkB)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	if !errors.Is(err, ErrExternalAppendConflict) {
		t.Fatalf("expected ErrExternalAppendConflict, got %v", err)
	}
	var ace *ExternalAppendConflictError
	if !errors.As(err, &ace) || ace.Height != 0 {
		t.Fatalf("expected ExternalAppendConflictError, got %#v", err)
	}
}

func TestBlockProducer_TryAppendExternalBlockStoresReceipts(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 10000)
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())
	bp.SetAppendReceiptStore(rs)
	tx := &mempool.Tx{ID: "ext1", Sender: "alice", Recipient: "bob", Amount: 2, Fee: 1, GasLimit: 0, Nonce: 0}
	spec := as.Clone()
	if err := spec.ApplyTx(tx); err != nil {
		t.Fatal(err)
	}
	sr := spec.StateRoot()
	blk := &Block{
		Height: 0, PrevHash: "", Timestamp: time.Unix(1700000500, 0),
		Transactions: []*mempool.Tx{tx}, StateRoot: sr, TotalFees: 1, GasUsed: 0, ProducerID: "peer",
	}
	blk.Hash = computeBlockHash(blk)
	pool.Add(tx)
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatal(err)
	}
	got, ok := rs.Get("ext1")
	if !ok || got.Status != ReceiptSuccess || got.BlockHash != blk.Hash {
		t.Fatalf("receipt: ok=%v %#v", ok, got)
	}
	if len(got.Logs) != 1 || got.Logs[0].Topic != "TxApplied" {
		t.Fatalf("expected one TxApplied log like ProduceBlockWithReceipts, got %#v", got.Logs)
	}
	if got.Logs[0].Data["sender"] != "alice" || got.Logs[0].Data["recipient"] != "bob" {
		t.Fatalf("expected sender/recipient on receipt, got %#v", got.Logs[0].Data)
	}
}

func TestBlockProducer_TryAppendExternalBlockStoresReceiptsContractID(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 10000)
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())
	bp.SetAppendReceiptStore(rs)
	tx := &mempool.Tx{ID: "ext-cid", Sender: "alice", Recipient: "bob", Amount: 2, Fee: 1, GasLimit: 0, Nonce: 0, ContractID: "token-v1"}
	spec := as.Clone()
	if err := spec.ApplyTx(tx); err != nil {
		t.Fatal(err)
	}
	sr := spec.StateRoot()
	blk := &Block{
		Height: 0, PrevHash: "", Timestamp: time.Unix(1700000700, 0),
		Transactions: []*mempool.Tx{tx}, StateRoot: sr, TotalFees: 1, GasUsed: 0, ProducerID: "peer",
	}
	blk.Hash = computeBlockHash(blk)
	pool.Add(tx)
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatal(err)
	}
	got, ok := rs.Get("ext-cid")
	if !ok || got.ContractID != "token-v1" {
		t.Fatalf("receipt contract_id: ok=%v %#v", ok, got)
	}
	byC := rs.GetByContract("token-v1")
	if len(byC) != 1 || byC[0].TxID != "ext-cid" {
		t.Fatalf("GetByContract: %#v", byC)
	}
	if got.Logs[0].Data["contract_id"] != "token-v1" {
		t.Fatalf("expected contract_id in TxApplied data, got %#v", got.Logs[0].Data)
	}
}

func TestBlockProducer_TryAppendExternalBlockStoresReceiptsTwoTxs(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 10000)
	pool := mempool.New(mempool.DefaultConfig())
	rs := NewReceiptStore()
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())
	bp.SetAppendReceiptStore(rs)
	tx0 := &mempool.Tx{ID: "e0", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 0, GasLimit: 0, Nonce: 0}
	tx1 := &mempool.Tx{ID: "e1", Sender: "alice", Recipient: "carol", Amount: 2, Fee: 0, GasLimit: 0, Nonce: 1}
	spec := as.Clone()
	if err := spec.ApplyTx(tx0); err != nil {
		t.Fatal(err)
	}
	if err := spec.ApplyTx(tx1); err != nil {
		t.Fatal(err)
	}
	sr := spec.StateRoot()
	blk := &Block{
		Height: 0, PrevHash: "", Timestamp: time.Unix(1700000600, 0),
		Transactions: []*mempool.Tx{tx0, tx1}, StateRoot: sr, ProducerID: "peer",
	}
	blk.Hash = computeBlockHash(blk)
	_ = pool.Add(tx0)
	_ = pool.Add(tx1)
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatal(err)
	}
	r0, ok := rs.Get("e0")
	if !ok || r0.Status != ReceiptSuccess || r0.BlockHash != blk.Hash || r0.IndexInBlock != 0 {
		t.Fatalf("receipt e0: ok=%v %#v", ok, r0)
	}
	r1, ok := rs.Get("e1")
	if !ok || r1.Status != ReceiptSuccess || r1.BlockHash != blk.Hash || r1.IndexInBlock != 1 {
		t.Fatalf("receipt e1: ok=%v %#v", ok, r1)
	}
}

func TestBlockProducer_TryAppendExternalBlockWithTx(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 10000)
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())
	tx := &mempool.Tx{ID: "x1", Sender: "alice", Recipient: "bob", Amount: 2, Fee: 1, GasLimit: 0, Nonce: 0}
	spec := as.Clone()
	if err := spec.ApplyTx(tx); err != nil {
		t.Fatal(err)
	}
	sr := spec.StateRoot()
	blk := &Block{
		Height: 0, PrevHash: "", Timestamp: time.Unix(1700000200, 0),
		Transactions: []*mempool.Tx{tx}, StateRoot: sr, TotalFees: 1, GasUsed: 0, ProducerID: "peer",
	}
	blk.Hash = computeBlockHash(blk)
	pool.Add(tx)
	if err := bp.TryAppendExternalBlock(blk); err != nil {
		t.Fatal(err)
	}
	if pool.Size() != 0 {
		t.Fatalf("expected tx removed from pool, size=%d", pool.Size())
	}
}

func TestBlockProducer_PreSealRequiresAccountStore(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	bp.SetPreSealBFTRound(func(*Block) error { return nil })
	pool.Add(makeTx("tx1", 1.0))
	_, err := bp.ProduceBlock()
	if !errors.Is(err, ErrPreSealRequiresAccountStore) {
		t.Fatalf("expected ErrPreSealRequiresAccountStore, got %v", err)
	}
}

func TestBlockProducer_PreSealRestoresPoolOnHookError(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 10000)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	_ = vs.Register("v3", 100)
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())
	bp.SetPreSealBFTRound(func(*Block) error {
		return fmt.Errorf("hook failed")
	})
	pool.Add(&mempool.Tx{ID: "t1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 1, GasLimit: 0, Nonce: 0})
	if _, err := bp.ProduceBlock(); err == nil {
		t.Fatal("expected error")
	}
	if pool.Size() != 1 {
		t.Fatalf("expected tx restored, pool size %d", pool.Size())
	}
}

func TestBlockProducer_PreSealCommitsBeforeAppend(t *testing.T) {
	as := NewAccountStore()
	as.Credit("alice", 10000)
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	_ = vs.Register("v2", 100)
	_ = vs.Register("v3", 100)
	bc := NewBFTConsensus(vs, DefaultConsensusConfig())
	ex := NewBFTExecutor(bc)
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, as, DefaultProducerConfig())
	bp.SetPreSealBFTRound(func(tent *Block) error {
		return RunSyntheticBFTRoundWithExecutor(ex, vs, tent)
	})
	pool.Add(&mempool.Tx{ID: "t1", Sender: "alice", Recipient: "bob", Amount: 1, Fee: 1, GasLimit: 0, Nonce: 0})
	blk, err := bp.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	if !bc.IsCommitted(blk.Height) {
		t.Fatal("BFT should already be committed for sealed height")
	}
}

func TestBlockProducer_BFTSealGate(t *testing.T) {
	bc, vs := setupBFT(t)
	_ = vs

	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	bp.SetBFTSealGate(bc)

	pool.Add(makeTx("tx1", 1.0))
	b1, err := bp.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}

	pool.Add(makeTx("tx2", 1.0))
	_, err = bp.ProduceBlock()
	if !errors.Is(err, ErrBFTExtensionBlocked) {
		t.Fatalf("expected ErrBFTExtensionBlocked, got %v", err)
	}

	prop, err := bc.ProposerForRound(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bc.Propose(b1.Height, 0, prop, b1.StateRoot); err != nil {
		t.Fatal(err)
	}
	for _, v := range vs.ActiveValidators() {
		if v.Status != ValidatorActive {
			continue
		}
		if err := bc.PreVote(b1.Height, v.Address, b1.StateRoot); err != nil {
			t.Fatal(err)
		}
	}
	for _, v := range vs.ActiveValidators() {
		if v.Status != ValidatorActive {
			continue
		}
		if err := bc.PreCommit(b1.Height, v.Address, b1.StateRoot); err != nil {
			t.Fatal(err)
		}
	}
	if !bc.IsCommitted(b1.Height) {
		t.Fatal("expected BFT committed tip height")
	}

	pool.Add(makeTx("tx3", 1.0))
	if _, err := bp.ProduceBlock(); err != nil {
		t.Fatalf("after BFT commit: %v", err)
	}
}

func TestBlockProducer_PolExtensionGate(t *testing.T) {
	vs := NewValidatorSet(DefaultValidatorSetConfig())
	_ = vs.Register("v1", 100)
	pol := NewPolFollower(vs, 2.0/3.0)
	pol.SetAnchorFinality(true)

	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	bp.SetPolFollower(pol)

	pool.Add(makeTx("tx1", 1.0))
	b1, err := bp.ProduceBlock()
	if err != nil {
		t.Fatal(err)
	}
	pol.RecordLocalSealedBlock(b1.Height, b1.StateRoot)

	pool.Add(makeTx("tx2", 1.0))
	_, err = bp.ProduceBlock()
	if !errors.Is(err, ErrPolExtensionBlocked) {
		t.Fatalf("expected ErrPolExtensionBlocked, got %v", err)
	}

	pol.MarkLocalRoundCertificatePublished(b1.Height)
	pool.Add(makeTx("tx3", 1.0))
	_, err = bp.ProduceBlock()
	if err != nil {
		t.Fatalf("second block after POL mark: %v", err)
	}
}

func TestBlockProducer_EmptyMempool(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())

	_, err := bp.ProduceBlock()
	if err == nil {
		t.Fatal("expected error for empty mempool")
	}
}

func TestBlockProducer_SkipsInvalidTx(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	pool.Add(&mempool.Tx{ID: "bad", Sender: "broke", Recipient: "bob", Amount: 999999, Fee: 1})
	pool.Add(makeTx("good", 2.0))

	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())
	block, err := bp.ProduceBlock()
	if err != nil {
		t.Fatalf("ProduceBlock: %v", err)
	}
	if len(block.Transactions) != 1 {
		t.Fatalf("expected 1 valid tx, got %d", len(block.Transactions))
	}
	if block.Transactions[0].ID != "good" {
		t.Fatalf("expected 'good' tx, got %s", block.Transactions[0].ID)
	}
}

func TestBlockProducer_GetBlock(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())

	pool.Add(makeTx("tx1", 1.0))
	bp.ProduceBlock()
	pool.Add(makeTx("tx2", 1.0))
	bp.ProduceBlock()

	b, ok := bp.GetBlock(0)
	if !ok || b.Height != 0 {
		t.Fatal("expected block at height 0")
	}
	_, ok = bp.GetBlock(999)
	if ok {
		t.Fatal("expected no block at height 999")
	}
}

func TestBlockProducer_LatestBlock(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())

	_, ok := bp.LatestBlock()
	if ok {
		t.Fatal("expected no latest block initially")
	}

	pool.Add(makeTx("tx1", 1.0))
	bp.ProduceBlock()
	pool.Add(makeTx("tx2", 1.0))
	bp.ProduceBlock()

	latest, ok := bp.LatestBlock()
	if !ok || latest.Height != 1 {
		t.Fatalf("expected latest at height 1, got %v", latest)
	}
}

func TestBlockProducer_Headers(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())

	for i := 0; i < 5; i++ {
		pool.Add(makeTx(fmt.Sprintf("tx%d", i), 1.0))
		bp.ProduceBlock()
	}

	headers := bp.Headers(1, 3)
	if len(headers) != 3 {
		t.Fatalf("expected 3 headers, got %d", len(headers))
	}
	if headers[0].Height != 1 || headers[2].Height != 3 {
		t.Fatalf("unexpected header range: %d-%d", headers[0].Height, headers[2].Height)
	}
}

func TestBlock_Header(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	bp := NewBlockProducer(pool, newTestApplier(), DefaultProducerConfig())

	pool.Add(makeTx("tx1", 1.0))
	block, _ := bp.ProduceBlock()

	header := block.Header()
	if header.Hash != block.Hash {
		t.Fatal("header hash should match block hash")
	}
	if header.TxCount != 1 {
		t.Fatalf("expected 1 tx in header, got %d", header.TxCount)
	}
	if header.TxRoot == "" {
		t.Fatal("expected non-empty tx root")
	}
}

func TestBlockProducer_MaxTxPerBlock(t *testing.T) {
	pool := mempool.New(mempool.DefaultConfig())
	cfg := DefaultProducerConfig()
	cfg.MaxTxPerBlock = 2
	bp := NewBlockProducer(pool, newTestApplier(), cfg)

	for i := 0; i < 5; i++ {
		pool.Add(makeTx(fmt.Sprintf("tx%d", i), float64(i+1)))
	}

	block, _ := bp.ProduceBlock()
	if len(block.Transactions) != 2 {
		t.Fatalf("expected 2 txs (max), got %d", len(block.Transactions))
	}
	if pool.Size() != 3 {
		t.Fatalf("expected 3 remaining in mempool, got %d", pool.Size())
	}
}
