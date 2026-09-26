package miningsvc

// Test doubles for the HL1 admission collaborators (legacymining.Store,
// Guard and Sink), a durable-tip ChainView, and the compatibility hook that
// keeps the byte-pinned pre-HL1 tests running (design rev 4 §1, §2).

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// The pinned tests (miningsvc_test.go, miningsvc_v2_test.go) predate
// Config.{Store, Guard, Sink}. They build writable Configs without them, and
// some set RewardSink. For such a Config the hook gives New permissive
// doubles: an always-open guard whose Precheck only parses, an in-memory
// store, and a sink whose Enqueue reports the miner address to the Config's
// RewardSink. The pinned tests therefore exercise the real §4.1 Submit path.
// Tests of New's refusals call withoutPinnedCompat.
func init() {
	pinnedTestCollaborators = func(cfg *Config) {
		rs := cfg.RewardSink
		cfg.RewardSink = nil
		sink := newFakeSink(nil)
		if rs != nil {
			sink.onEnqueue = func(rec legacymining.Record) { rs.OnAcceptedProof(rec.MinerAddr) }
		}
		cfg.Store, cfg.Guard, cfg.Sink = newFakeStore(nil), newFakeGuard(nil), sink
	}
}

// withoutPinnedCompat disables the pinned-test hook for one test, so New
// behaves exactly as in a binary.
func withoutPinnedCompat(t *testing.T) {
	t.Helper()
	saved := pinnedTestCollaborators
	pinnedTestCollaborators = nil
	t.Cleanup(func() { pinnedTestCollaborators = saved })
}

// ---- call log ---------------------------------------------------------------

type call struct {
	name string
	held bool // submitMu was held when the call was made
}

type callLog struct {
	mu    sync.Mutex
	calls []call
	held  func() bool
}

func (l *callLog) add(name string) {
	if l == nil {
		return
	}
	held := false
	l.mu.Lock()
	h := l.held
	l.mu.Unlock()
	if h != nil {
		held = h()
	}
	l.mu.Lock()
	l.calls = append(l.calls, call{name: name, held: held})
	l.mu.Unlock()
}

func (l *callLog) reset() {
	l.mu.Lock()
	l.calls = nil
	l.mu.Unlock()
}

func (l *callLog) snapshot() []call {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]call(nil), l.calls...)
}

// names returns the logged names, dropping consecutive repeats.
func (l *callLog) names() []string {
	var out []string
	for _, c := range l.snapshot() {
		if len(out) == 0 || out[len(out)-1] != c.name {
			out = append(out, c.name)
		}
	}
	return out
}

// trackSubmitMu makes every later log entry record whether svc.submitMu is
// held. Use it only in single-goroutine tests.
func (l *callLog) trackSubmitMu(svc *Service) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held = func() bool {
		if svc.submitMu.TryLock() {
			svc.submitMu.Unlock()
			return false
		}
		return true
	}
}

// ---- guard ------------------------------------------------------------------

type fakeGuard struct {
	log  *callLog
	cfg  legacymining.Config
	hash legacymining.ConfigHash

	closed      atomic.Bool // AdmissionOpen is !closed
	rateErr     error
	precheckErr error
	nilProof    bool   // Precheck succeeds with a nil Candidate.Proof
	nodeID      string // Candidate.NodeID
	fixedNonce  *[32]byte

	mu       sync.Mutex
	observed []error
	freezes  []string
}

func newFakeGuard(log *callLog) *fakeGuard {
	g := &fakeGuard{log: log, nodeID: "node-A"}
	g.cfg = legacymining.Config{
		Version:         legacymining.ConfigVersion,
		Allowed:         []legacymining.AllowEntry{{MinerAddr: "any", NodeID: "node-A"}},
		MaxProofsPerMin: 1 << 20,
		MaxProofsTotal:  1 << 40,
		MaxPending:      legacymining.MaxPendingLimit,
		BudgetCell:      1 << 40,
		ExpiresUnix:     1 << 40,
	}
	g.hash = sha256.Sum256([]byte("fake canary config"))
	return g
}

func (g *fakeGuard) Config() legacymining.Config         { return g.cfg }
func (g *fakeGuard) ConfigHash() legacymining.ConfigHash { return g.hash }

func (g *fakeGuard) State() legacymining.State {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.freezes) > 0 {
		return legacymining.StateFrozen
	}
	return legacymining.StateOpen
}

func (g *fakeGuard) AdmissionOpen() bool { return !g.closed.Load() }
func (g *fakeGuard) PreArm() error       { return nil }
func (g *fakeGuard) Activate(bool)       {}

func (g *fakeGuard) Admit() error {
	g.log.add("Admit")
	if g.closed.Load() {
		return &legacymining.Rejection{Kind: legacymining.KindAdmissionClosed}
	}
	return nil
}

func (g *fakeGuard) Precheck(raw []byte) (legacymining.Candidate, error) {
	g.log.add("Precheck")
	if g.precheckErr != nil {
		return legacymining.Candidate{}, g.precheckErr
	}
	p, err := mining.ParseProof(raw)
	if err != nil {
		return legacymining.Candidate{}, &legacymining.Rejection{Kind: legacymining.KindMalformed, Detail: err.Error()}
	}
	c := legacymining.Candidate{Proof: p, NodeID: g.nodeID, AttNonce: sha256.Sum256(raw)}
	if g.fixedNonce != nil {
		c.AttNonce = *g.fixedNonce
	}
	if g.nilProof {
		c.Proof = nil
	}
	return c, nil
}

func (g *fakeGuard) TakeRate() error {
	g.log.add("TakeRate")
	return g.rateErr
}

func (g *fakeGuard) ObserveRejection(err error) {
	g.log.add("ObserveRejection")
	g.mu.Lock()
	g.observed = append(g.observed, err)
	g.mu.Unlock()
}

func (g *fakeGuard) ObserveSeal(uint64, bool)                   {}
func (g *fakeGuard) ObserveTotals(legacymining.Totals, float64) {}
func (g *fakeGuard) StopAdmission(cause string)                 { g.Freeze("stop:" + cause) }
func (g *fakeGuard) setClosed(closed bool)                      { g.closed.Store(closed) }
func (g *fakeGuard) withMaxPending(n int)                       { g.cfg.MaxPending = n }
func (g *fakeGuard) withNonce(n [32]byte)                       { g.fixedNonce = &n }

func (g *fakeGuard) observedErrs() []error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]error(nil), g.observed...)
}

func (g *fakeGuard) freezeCauses() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.freezes...)
}

// Freeze latches FROZEN: admission closes, as in the real guard.
func (g *fakeGuard) Freeze(cause string) {
	g.log.add("Freeze")
	g.mu.Lock()
	g.freezes = append(g.freezes, cause)
	g.mu.Unlock()
	g.closed.Store(true)
}

var _ legacymining.Guard = (*fakeGuard)(nil)

// ---- store ------------------------------------------------------------------

type nodeNonce struct {
	node  string
	nonce [32]byte
}

type fakeStore struct {
	log *callLog

	mu        sync.Mutex
	rows      map[legacymining.ProofID]legacymining.Record
	nonces    map[nodeNonce]legacymining.ProofID
	order     []legacymining.ProofID
	acceptErr error
	delay     func() // runs inside Accept, before the insert
}

func newFakeStore(log *callLog) *fakeStore {
	return &fakeStore{
		log:    log,
		rows:   map[legacymining.ProofID]legacymining.Record{},
		nonces: map[nodeNonce]legacymining.ProofID{},
	}
}

var errFakeUnused = errors.New("fakeStore: method not used by miningsvc")

func (s *fakeStore) Open(string, *legacymining.Meta) error { return errFakeUnused }
func (s *fakeStore) Close() error                          { return nil }
func (s *fakeStore) Meta() (legacymining.Meta, error)      { return legacymining.Meta{}, errFakeUnused }
func (s *fakeStore) Pending() ([]legacymining.Record, error) {
	return nil, errFakeUnused
}
func (s *fakeStore) Lookup([]legacymining.ProofID) (map[legacymining.ProofID]legacymining.Record, error) {
	return nil, errFakeUnused
}
func (s *fakeStore) MarkPaid([]legacymining.Payment) error { return errFakeUnused }
func (s *fakeStore) ApplyReconcile(legacymining.Reconciliation) (legacymining.ReconcileCounts, error) {
	return legacymining.ReconcileCounts{}, errFakeUnused
}
func (s *fakeStore) Counters(legacymining.ConfigHash) (legacymining.Counters, error) {
	return legacymining.Counters{}, errFakeUnused
}
func (s *fakeStore) Event(legacymining.Event) error { return nil }

// Accept emulates the UNIQUE constraints of §3.3.
func (s *fakeStore) Accept(rec legacymining.Record) error {
	s.log.add("Accept")
	if s.delay != nil {
		s.delay()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.acceptErr != nil {
		return s.acceptErr
	}
	if _, dup := s.rows[rec.ProofID]; dup {
		return &legacymining.Rejection{Kind: legacymining.KindDuplicate}
	}
	key := nodeNonce{rec.NodeID, rec.AttNonce}
	if _, dup := s.nonces[key]; dup {
		return &legacymining.Rejection{Kind: legacymining.KindNonceConflict}
	}
	s.rows[rec.ProofID] = rec
	s.nonces[key] = rec.ProofID
	s.order = append(s.order, rec.ProofID)
	return nil
}

func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rows)
}

func (s *fakeStore) row(id legacymining.ProofID) (legacymining.Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rows[id]
	return r, ok
}

var _ legacymining.Store = (*fakeStore)(nil)

// ---- sink -------------------------------------------------------------------

type fakeSink struct {
	log *callLog

	mu          sync.Mutex
	outstanding int
	maxSeen     int
	enqueued    []legacymining.Record
	enqueueErr  error
	onEnqueue   func(legacymining.Record)
}

func newFakeSink(log *callLog) *fakeSink { return &fakeSink{log: log} }

func (k *fakeSink) Outstanding() int {
	k.log.add("Outstanding")
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.outstanding
}

func (k *fakeSink) Enqueue(rec legacymining.Record) error {
	k.log.add("Enqueue")
	k.mu.Lock()
	if k.enqueueErr != nil {
		k.mu.Unlock()
		return k.enqueueErr
	}
	k.outstanding++
	if k.outstanding > k.maxSeen {
		k.maxSeen = k.outstanding
	}
	k.enqueued = append(k.enqueued, rec)
	cb := k.onEnqueue
	k.mu.Unlock()
	if cb != nil {
		cb(rec)
	}
	return nil
}

func (k *fakeSink) setOutstanding(n int) {
	k.mu.Lock()
	k.outstanding = n
	k.mu.Unlock()
}

func (k *fakeSink) records() []legacymining.Record {
	k.mu.Lock()
	defer k.mu.Unlock()
	return append([]legacymining.Record(nil), k.enqueued...)
}

var _ legacymining.Sink = (*fakeSink)(nil)

// ---- durable chain view -----------------------------------------------------

// durableView clamps a ChainView to a durable tip, like cmd/qsdm's
// durableChainView: no tip until set, and nothing above it.
type durableView struct {
	inner legacymining.ChainView
	log   *callLog
	has   atomic.Bool
	tip   atomic.Uint64
}

func (v *durableView) set(tip uint64) { v.tip.Store(tip); v.has.Store(true) }

func (v *durableView) HasTip() bool { return v.has.Load() && v.inner.HasTip() }

func (v *durableView) TipHeight() uint64 {
	if !v.HasTip() {
		return 0
	}
	return min(v.tip.Load(), v.inner.TipHeight())
}

func (v *durableView) GetBlock(h uint64) (*chain.Block, bool) {
	v.log.add("GetBlock")
	if !v.HasTip() || h > v.TipHeight() {
		return nil, false
	}
	return v.inner.GetBlock(h)
}

var _ legacymining.ChainView = (*durableView)(nil)

// ---- chain and proof helpers ------------------------------------------------

// buildProducerWithBlocks seals n blocks, one seed transfer each.
func buildProducerWithBlocks(t *testing.T, n int) *chain.BlockProducer {
	t.Helper()
	pool := mempool.New(mempool.DefaultConfig())
	accounts := chain.NewAccountStore()
	accounts.Credit("alice", 1_000)
	bp := chain.NewBlockProducer(pool, accounts, chain.DefaultProducerConfig())
	for i := 0; i < n; i++ {
		if err := pool.Add(&mempool.Tx{
			ID: fmt.Sprintf("hl1-seed-%d", i), Sender: "alice", Recipient: "bob",
			Amount: 1, Nonce: uint64(i),
		}); err != nil {
			t.Fatalf("seed mempool %d: %v", i, err)
		}
		if _, err := bp.ProduceBlock(); err != nil {
			t.Fatalf("ProduceBlock %d: %v", i, err)
		}
	}
	return bp
}

// headerAt returns the hash of the block at h, read from the full producer
// (not the durable view).
func headerAt(t *testing.T, bp *chain.BlockProducer, h uint64) [32]byte {
	t.Helper()
	blk, ok := bp.GetBlock(h)
	if !ok {
		t.Fatalf("no block at %d", h)
	}
	var hdr [32]byte
	if err := decodeHexInto(hdr[:], blk.Hash); err != nil {
		t.Fatalf("decode hash at %d: %v", h, err)
	}
	return hdr
}

// proofSolver solves canonical v1 proofs against svc's work parameters.
type proofSolver struct {
	svc       *Service
	batchRoot [32]byte
	target    *big.Int
	dags      map[uint64]mining.DAG
}

func newProofSolver(t *testing.T, svc *Service) *proofSolver {
	t.Helper()
	root, err := svc.ws.PrefixRoot(1)
	if err != nil {
		t.Fatalf("PrefixRoot: %v", err)
	}
	target, err := mining.TargetFromDifficulty(new(big.Int).Set(svc.difficulty))
	if err != nil {
		t.Fatalf("TargetFromDifficulty: %v", err)
	}
	return &proofSolver{svc: svc, batchRoot: root, target: target, dags: map[uint64]mining.DAG{}}
}

func (ps *proofSolver) solve(t *testing.T, height uint64, hdr [32]byte, minerAddr string) []byte {
	t.Helper()
	epoch := height / ps.svc.blocksPerEpoch
	dag, ok := ps.dags[epoch]
	if !ok {
		d, err := mining.NewInMemoryDAG(epoch, ps.svc.ws.Root(), ps.svc.dagSize)
		if err != nil {
			t.Fatalf("NewInMemoryDAG: %v", err)
		}
		dag = d
		ps.dags[epoch] = d
	}
	res, err := mining.Solve(context.Background(), mining.SolverParams{
		Epoch: epoch, Height: height, HeaderHash: hdr, MinerAddr: minerAddr,
		BatchRoot: ps.batchRoot, BatchCount: 1, Target: ps.target, DAG: dag,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	raw, err := res.Proof.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	return raw
}

// hl1Fixture is a writable service over a durable view with recording
// doubles.
type hl1Fixture struct {
	bp    *chain.BlockProducer
	view  *durableView
	log   *callLog
	guard *fakeGuard
	store *fakeStore
	sink  *fakeSink
	cfg   Config
}

// newHL1Fixture seals blocks blocks and sets the durable tip to the
// producer's tip minus lag.
func newHL1Fixture(t *testing.T, blocks int, lag uint64) *hl1Fixture {
	t.Helper()
	bp := buildProducerWithBlocks(t, blocks)
	log := &callLog{}
	f := &hl1Fixture{
		bp:    bp,
		view:  &durableView{inner: bp, log: log},
		log:   log,
		guard: newFakeGuard(log),
		store: newFakeStore(log),
		sink:  newFakeSink(log),
	}
	f.view.set(bp.TipHeight() - lag)
	f.cfg = Config{
		Producer:       f.view,
		WorkSet:        syntheticWS(),
		DAGSize:        128,
		Difficulty:     big.NewInt(2),
		BlocksPerEpoch: 1024,
		Store:          f.store,
		Guard:          f.guard,
		Sink:           f.sink,
	}
	return f
}

func (f *hl1Fixture) service(t *testing.T) *Service {
	t.Helper()
	svc, err := New(f.cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return svc
}
