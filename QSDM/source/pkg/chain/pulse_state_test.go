package chain

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/pulse"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

type pulseFix struct {
	t        *testing.T
	aware    *EnrollmentAwareApplier
	accounts *AccountStore
	state    *enrollment.InMemoryState
	ps       *PulseStateStore
	height   uint64
	pool     *pulse.KeyPair
	m1, m2   *pulse.KeyPair
	fee      string
	nonces   map[string]uint64
	funderN  uint64
}

func newPulseFix(t *testing.T, params *PulseParams) *pulseFix {
	t.Helper()
	t.Cleanup(func() { pulseParams.Store(nil) })
	pool, _ := pulse.NewKeyPair()
	m1, _ := pulse.NewKeyPair()
	m2, _ := pulse.NewKeyPair()
	fee, _ := pulse.NewKeyPair()
	if params != nil {
		params.Pools = []pulse.PoolEntry{{ID: pool.ID(), PublicKey: hex.EncodeToString(pool.Pub.Bytes())}}
		if err := SetPulseParams(*params); err != nil {
			t.Fatalf("SetPulseParams: %v", err)
		}
	}
	accounts := NewAccountStore()
	accounts.Credit(MiningRewardFunderAddress, 1000)
	state := enrollment.NewInMemoryState()
	f := &pulseFix{t: t, accounts: accounts, state: state, ps: NewPulseStateStore(), height: 20,
		pool: pool, m1: m1, m2: m2, fee: string(fee.ID()), nonces: map[string]uint64{}}
	f.aware = NewEnrollmentAwareApplier(accounts, NewEnrollmentApplier(accounts, state))
	f.aware.SetHeightFn(func() uint64 { return f.height })
	f.aware.SetPulseStateStore(f.ps)
	return f
}

func activeParams() *PulseParams {
	return &PulseParams{ActivationHeight: 10, PayeeEpochCapDust: 1 << 60, ReclaimAfterBlocks: 100}
}

func (f *pulseFix) pulseTx(k *pulse.KeyPair, a PulseAction) *mempool.Tx {
	payload, _ := json.Marshal(a)
	sender := string(k.ID())
	tx := &mempool.Tx{ID: "p", Sender: sender, Nonce: f.nonces[sender], ContractID: PulseContractID,
		Payload: payload, PublicKey: hex.EncodeToString(k.Pub.Bytes())}
	sig, err := k.Sign("", PulseTxSigningBytes(tx))
	if err != nil {
		f.t.Fatal(err)
	}
	tx.Signature = hex.EncodeToString(sig)
	return tx
}

func (f *pulseFix) apply(tx *mempool.Tx) error {
	err := f.aware.ApplyTx(tx)
	if err == nil && tx.ContractID == PulseContractID {
		f.nonces[tx.Sender]++
	}
	return err
}

func (f *pulseFix) must(tx *mempool.Tx) {
	f.t.Helper()
	if err := f.apply(tx); err != nil {
		f.t.Fatalf("apply %s: %v", tx.ContractID, err)
	}
}

func lmp(ids ...string) []byte {
	out := append([]byte(lmpTag), 0, 0)
	binary.BigEndian.PutUint16(out[len(lmpTag):], uint16(len(ids)))
	for _, id := range ids {
		s := sha256.Sum256([]byte(id))
		out = append(out, s[:]...)
	}
	return out
}

func proofHex(id string) string { s := sha256.Sum256([]byte(id)); return hex.EncodeToString(s[:]) }

func (f *pulseFix) reward(recipient string, amount float64, ids ...string) *mempool.Tx {
	tx := &mempool.Tx{ID: "r", Sender: MiningRewardFunderAddress, Recipient: recipient, Amount: amount,
		Nonce: f.funderN, ContractID: MiningRewardContractID, Payload: lmp(ids...)}
	return tx
}

func (f *pulseFix) rewardOK(recipient string, amount float64, ids ...string) {
	f.t.Helper()
	if err := f.aware.ApplyTx(f.reward(recipient, amount, ids...)); err != nil {
		f.t.Fatalf("reward: %v", err)
	}
	f.funderN++
}

func (f *pulseFix) batch(seq uint64, feeBPS uint16, proofs []string, weights []pulse.Weight) (string, string) {
	b := &pulse.Batch{Version: pulse.BatchVersion, PoolID: f.pool.ID(), Seq: seq, Height: f.height, FeeBPS: feeBPS,
		FeeTo: pulse.Address(f.fee), FloorDust: 10_000_000, ProofIDs: proofs, Weights: weights}
	pulse.SortWeights(b.Weights)
	sb, err := pulse.SignBatch(f.pool, b)
	if err != nil {
		f.t.Fatalf("SignBatch: %v", err)
	}
	return hex.EncodeToString(sb.Body), hex.EncodeToString(sb.Sig)
}

func (f *pulseFix) bal(addr string) float64 {
	if a, ok := f.accounts.Get(addr); ok && a != nil {
		return a.Balance
	}
	return 0
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestPulseOffByDefault(t *testing.T) {
	f := newPulseFix(t, nil)
	if err := f.apply(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: string(f.pool.ID())})); !errors.Is(err, ErrPulseInactive) {
		t.Fatalf("join without params: %v", err)
	}
	if err := f.aware.ApplyTx(f.reward(PulseEscrowAddress(string(f.pool.ID()), string(f.m1.ID())), 1, "a")); !errors.Is(err, ErrPulseInactive) {
		t.Fatalf("escrow reward without params: %v", err)
	}
	// An empty store leaves the state root exactly as without PULSE.
	plain := NewEnrollmentAwareApplier(f.accounts, NewEnrollmentApplier(f.accounts, f.state))
	if plain.StateRoot() != f.aware.StateRoot() {
		t.Fatal("an empty PULSE store changed the state root")
	}
	// Below the activation height the rule is off too.
	f2 := newPulseFix(t, activeParams())
	f2.height = 9
	if err := f2.apply(f2.pulseTx(f2.m1, PulseAction{Op: "join", Pool: string(f2.pool.ID())})); !errors.Is(err, ErrPulseInactive) {
		t.Fatalf("join below the height: %v", err)
	}
}

func TestPulseSetParamsRefusesIncomplete(t *testing.T) {
	t.Cleanup(func() { pulseParams.Store(nil) })
	if err := SetPulseParams(PulseParams{ActivationHeight: 5}); err == nil {
		t.Fatal("an activation height without pools was accepted")
	}
	k, _ := pulse.NewKeyPair()
	good := pulse.PoolEntry{ID: k.ID(), PublicKey: hex.EncodeToString(k.Pub.Bytes())}
	if err := SetPulseParams(PulseParams{ActivationHeight: 5, Pools: []pulse.PoolEntry{good}, ReclaimAfterBlocks: 1}); err == nil {
		t.Fatal("no payee cap was accepted")
	}
	bad := good
	bad.ID = pulse.Address(string(good.ID)[:63] + "0")
	if string(bad.ID) == string(good.ID) {
		bad.ID = pulse.Address(string(good.ID)[:63] + "1")
	}
	if err := SetPulseParams(PulseParams{ActivationHeight: 5, Pools: []pulse.PoolEntry{bad}, PayeeEpochCapDust: 1, ReclaimAfterBlocks: 1}); err == nil {
		t.Fatal("a pool id that is not its key's address was accepted")
	}
	if err := SetPulseParams(PulseParams{ActivationHeight: 5, Pools: []pulse.PoolEntry{good}, PayeeEpochCapDust: 1, ReclaimAfterBlocks: 1}); err != nil {
		t.Fatalf("valid params refused: %v", err)
	}
	if PulseParamsSnapshot().MaxFeeBPS != 300 {
		t.Fatalf("default fee ceiling = %d, want 300", PulseParamsSnapshot().MaxFeeBPS)
	}
}

// The whole path: join, escrowed rewards, a settlement that pays every member
// and the fee directly, then replay protection.
func TestPulseSettlementPaysMembersDirectly(t *testing.T) {
	f := newPulseFix(t, activeParams())
	pool := string(f.pool.ID())
	m1, m2 := string(f.m1.ID()), string(f.m2.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	f.must(f.pulseTx(f.m2, PulseAction{Op: "join", Pool: pool}))
	f.rewardOK(PulseEscrowAddress(pool, m1), 3, "p1", "p2")
	if !near(f.bal(PulseEscrowAddress(pool, m1)), 3) || f.bal(m1) != 0 {
		t.Fatalf("escrow %v, member %v", f.bal(PulseEscrowAddress(pool, m1)), f.bal(m1))
	}
	// A proof can be escrowed once.
	if err := f.aware.ApplyTx(f.reward(PulseEscrowAddress(pool, m1), 1, "p1")); !errors.Is(err, ErrPulseReplay) {
		t.Fatalf("second escrow of p1: %v", err)
	}
	body, sig := f.batch(1, 100, []string{proofHex("p1"), proofHex("p2")},
		[]pulse.Weight{{Owner: f.m1.ID(), Weight: 1}, {Owner: f.m2.ID(), Weight: 3}})
	settle := f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: body, BatchSig: sig})
	f.must(settle)
	// 3 CELL pooled: 1 % fee = 0.03; 2.97 split 1:3.
	if !near(f.bal(m1), 0.7425) || !near(f.bal(m2), 2.2275) || !near(f.bal(f.fee), 0.03) {
		t.Fatalf("m1 %.8f m2 %.8f fee %.8f", f.bal(m1), f.bal(m2), f.bal(f.fee))
	}
	if f.bal(PulseEscrowAddress(pool, m1)) > 1e-9 || f.bal(pulseCarry(pool)) > 1e-9 {
		t.Fatalf("escrow %v carry %v left", f.bal(PulseEscrowAddress(pool, m1)), f.bal(pulseCarry(pool)))
	}
	if f.ps.LastSeq(pool) != 1 || len(f.ps.EscrowedProofs(pool)) != 0 {
		t.Fatalf("seq %d escrow %v", f.ps.LastSeq(pool), f.ps.EscrowedProofs(pool))
	}
	// The same batch again (new tx nonce): replay.
	if err := f.apply(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: body, BatchSig: sig})); !errors.Is(err, ErrPulseReplay) {
		t.Fatalf("replayed batch: %v", err)
	}
	// Settled proofs cannot be escrowed again either.
	if err := f.aware.ApplyTx(f.reward(PulseEscrowAddress(pool, m1), 1, "p2")); !errors.Is(err, ErrPulseReplay) {
		t.Fatalf("re-escrow of a settled proof: %v", err)
	}
}

func TestPulseSettlementRefusals(t *testing.T) {
	f := newPulseFix(t, activeParams())
	pool := string(f.pool.ID())
	m1 := string(f.m1.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	f.rewardOK(PulseEscrowAddress(pool, m1), 2, "q1")
	w := []pulse.Weight{{Owner: f.m1.ID(), Weight: 5}, {Owner: f.m2.ID(), Weight: 5}}
	root := f.aware.StateRoot()
	m1Bal := f.bal(m1)
	cases := map[string]func() *mempool.Tx{
		"fee above the 3 % ceiling": func() *mempool.Tx {
			b, s := f.batch(1, 301, []string{proofHex("q1")}, w)
			return f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})
		},
		"sent by someone other than the pool": func() *mempool.Tx {
			b, s := f.batch(1, 100, []string{proofHex("q1")}, w)
			return f.pulseTx(f.m2, PulseAction{Op: "settle", Batch: b, BatchSig: s})
		},
		"batch signature by another key": func() *mempool.Tx {
			b, _ := f.batch(1, 100, []string{proofHex("q1")}, w)
			raw, _ := hex.DecodeString(b)
			other, _ := f.m2.Sign(pulse.BatchDomain, raw)
			return f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: hex.EncodeToString(other)})
		},
		"proof not escrowed": func() *mempool.Tx {
			b, s := f.batch(1, 100, []string{proofHex("nope")}, w)
			return f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})
		},
		"proof owner without weight": func() *mempool.Tx {
			b, s := f.batch(1, 100, []string{proofHex("q1")}, []pulse.Weight{{Owner: f.m2.ID(), Weight: 5}})
			return f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})
		},
		"batch height in the future": func() *mempool.Tx {
			f.height++
			b, s := f.batch(1, 100, []string{proofHex("q1")}, w)
			f.height--
			return f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})
		},
		"tampered tx signature": func() *mempool.Tx {
			b, s := f.batch(1, 100, []string{proofHex("q1")}, w)
			tx := f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})
			tx.Fee = 1
			return tx
		},
	}
	for name, mk := range cases {
		if err := f.apply(mk()); err == nil {
			t.Fatalf("%s: accepted", name)
		}
		if f.aware.StateRoot() != root || f.bal(m1) != m1Bal {
			t.Fatalf("%s: a refused settlement changed state", name)
		}
	}
	// A non-member's escrow reward is refused.
	if err := f.aware.ApplyTx(f.reward(PulseEscrowAddress(pool, string(f.m2.ID())), 1, "q9")); !errors.Is(err, ErrPulseNotMember) {
		t.Fatalf("non-member escrow: %v", err)
	}
	// An unauthorized pool's escrow is refused.
	other, _ := pulse.NewKeyPair()
	if err := f.aware.ApplyTx(f.reward(PulseEscrowAddress(string(other.ID()), m1), 1, "q8")); !errors.Is(err, ErrPulseUnknown) {
		t.Fatalf("unknown pool escrow: %v", err)
	}
}

func TestPulseReclaimAfterTimeout(t *testing.T) {
	f := newPulseFix(t, activeParams())
	pool := string(f.pool.ID())
	m1 := string(f.m1.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	f.rewardOK(PulseEscrowAddress(pool, m1), 2, "r1")
	if err := f.apply(f.pulseTx(f.m1, PulseAction{Op: "reclaim", Proofs: []string{proofHex("r1")}})); !errors.Is(err, ErrPulseNotDue) {
		t.Fatalf("early reclaim: %v", err)
	}
	if err := f.apply(f.pulseTx(f.m2, PulseAction{Op: "reclaim", Proofs: []string{proofHex("r1")}})); !errors.Is(err, ErrPulseMismatch) {
		t.Fatalf("reclaim of someone else's proof: %v", err)
	}
	f.height += 100
	f.must(f.pulseTx(f.m1, PulseAction{Op: "reclaim", Proofs: []string{proofHex("r1")}}))
	if !near(f.bal(m1), 2) || f.bal(PulseEscrowAddress(pool, m1)) > 1e-9 {
		t.Fatalf("reclaim paid %v, escrow left %v", f.bal(m1), f.bal(PulseEscrowAddress(pool, m1)))
	}
	b, s := f.batch(1, 0, []string{proofHex("r1")}, []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}})
	if err := f.apply(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})); !errors.Is(err, ErrPulseReplay) {
		t.Fatalf("settling a reclaimed proof: %v", err)
	}
}

func TestPulsePayeeEpochCapCarriesTheRest(t *testing.T) {
	p := activeParams()
	p.PayeeEpochCapDust = 50_000_000 // 0.5 CELL per payee per epoch
	f := newPulseFix(t, p)
	pool := string(f.pool.ID())
	m1 := string(f.m1.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	f.rewardOK(PulseEscrowAddress(pool, m1), 2, "c1")
	b, s := f.batch(1, 0, []string{proofHex("c1")}, []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}})
	f.must(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s}))
	if !near(f.bal(m1), 0.5) {
		t.Fatalf("capped payout %v, want 0.5", f.bal(m1))
	}
	if f.ps.Owed(pool)[m1] != 150_000_000 || !near(f.bal(pulseCarry(pool)), 1.5) {
		t.Fatalf("owed %v carry %v", f.ps.Owed(pool), f.bal(pulseCarry(pool)))
	}
	// Next epoch: the carried balance is paid (up to the cap again).
	f.height += pulseEpochBlocks
	f.rewardOK(PulseEscrowAddress(pool, m1), 0.1, "c2")
	b, s = f.batch(2, 0, []string{proofHex("c2")}, []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}})
	f.must(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s}))
	if !near(f.bal(m1), 1.0) {
		t.Fatalf("second epoch total %v, want 1.0", f.bal(m1))
	}
}

func TestPulseEscrowRefusesADeferredBond(t *testing.T) {
	f := newPulseFix(t, activeParams())
	pool := string(f.pool.ID())
	m1 := string(f.m1.ID())
	if err := f.state.ApplyEnroll(enrollment.EnrollmentRecord{NodeID: "rig", Owner: m1, GPUUUID: "GPU-p",
		BondMode: enrollment.BondModeMiningRewards, RequiredStakeDust: mining.MinEnrollStakeDust}); err != nil {
		t.Fatal(err)
	}
	f.height = enrollment.DeferredBondActivationHeight + 20
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	root := f.aware.StateRoot()
	if err := f.aware.ApplyTx(f.reward(PulseEscrowAddress(pool, m1), 1, "b1")); !errors.Is(err, ErrPulseBondLocked) {
		t.Fatalf("escrow reward with a deferred bond: %v", err)
	}
	if f.aware.StateRoot() != root {
		t.Fatal("a refused escrow reward changed state")
	}
}

// Rebuilding the store from blocks (state only) gives the same state as live
// application, and clone/restore round-trips.
func TestPulseHistoricalReplayMatchesLive(t *testing.T) {
	f := newPulseFix(t, activeParams())
	pool := string(f.pool.ID())
	m1 := string(f.m1.ID())
	var txs []*mempool.Tx
	join := f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool})
	f.must(join)
	txs = append(txs, join)
	rw := f.reward(PulseEscrowAddress(pool, m1), 2, "h1", "h2")
	f.rewardOK(PulseEscrowAddress(pool, m1), 2, "h1", "h2")
	txs = append(txs, rw)
	b, s := f.batch(1, 100, []string{proofHex("h1")}, []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}, {Owner: f.m2.ID(), Weight: 1}})
	st := f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})
	f.must(st)
	txs = append(txs, st)

	re := NewPulseStateStore()
	for _, tx := range txs {
		var err error
		if tx.ContractID == PulseContractID {
			err = re.ApplyHistoricalTx(tx, f.height)
		} else {
			err = re.RecordEscrow(tx, f.height)
		}
		if err != nil {
			t.Fatalf("replay %s: %v", tx.ContractID, err)
		}
	}
	if re.StateRoot() != f.ps.StateRoot() {
		t.Fatal("historical replay differs from live state")
	}
	c := f.ps.ChainReplayClone().(*PulseStateStore)
	f.must(f.pulseTx(f.m1, PulseAction{Op: "leave"}))
	if c.StateRoot() == f.ps.StateRoot() {
		t.Fatal("clone aliases live state")
	}
	if err := f.ps.RestoreFromChainReplay(c); err != nil || c.StateRoot() != f.ps.StateRoot() {
		t.Fatalf("restore: %v", err)
	}
}

func TestParsePulseEscrow(t *testing.T) {
	a := "aa" + string(make([]byte, 0))
	_ = a
	pool := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	owner := "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	if p, o, ok := ParsePulseEscrow(PulseEscrowAddress(pool, owner)); !ok || p != pool || o != owner {
		t.Fatal("round trip failed")
	}
	for _, bad := range []string{owner, PulseEscrowPrefix + pool, PulseEscrowPrefix + pool + ":" + "XYZ",
		PulseEscrowPrefix + "0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef:" + owner} {
		if _, _, ok := ParsePulseEscrow(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	if PulseOwnerOf(PulseEscrowAddress(pool, owner)) != owner || PulseOwnerOf(owner) != owner {
		t.Fatal("PulseOwnerOf")
	}
}

// Gaps found by the sabotage run (2026-11-16): each guard below had no test
// that failed without it.

func TestPulseSequenceMustIncrease(t *testing.T) {
	f := newPulseFix(t, activeParams())
	pool := string(f.pool.ID())
	m1 := string(f.m1.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	f.rewardOK(PulseEscrowAddress(pool, m1), 1, "s1")
	f.rewardOK(PulseEscrowAddress(pool, m1), 1, "s2")
	w := []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}}
	b, s := f.batch(5, 0, []string{proofHex("s1")}, w)
	f.must(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s}))
	for _, seq := range []uint64{5, 4} {
		b, s = f.batch(seq, 0, []string{proofHex("s2")}, w)
		if err := f.apply(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})); !errors.Is(err, ErrPulseReplay) {
			t.Fatalf("seq %d after 5: %v", seq, err)
		}
	}
	b, s = f.batch(6, 0, []string{proofHex("s2")}, w)
	f.must(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s}))
}

func TestPulseTwoPoolsStayApart(t *testing.T) {
	t.Cleanup(func() { pulseParams.Store(nil) })
	f := newPulseFix(t, nil)
	other, _ := pulse.NewKeyPair()
	p := activeParams()
	p.Pools = []pulse.PoolEntry{
		{ID: f.pool.ID(), PublicKey: hex.EncodeToString(f.pool.Pub.Bytes())},
		{ID: other.ID(), PublicKey: hex.EncodeToString(other.Pub.Bytes())},
	}
	if err := SetPulseParams(*p); err != nil {
		t.Fatal(err)
	}
	poolA, poolB := string(f.pool.ID()), string(other.ID())
	m1, m2 := string(f.m1.ID()), string(f.m2.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: poolA}))
	f.must(f.pulseTx(f.m2, PulseAction{Op: "join", Pool: poolB}))
	f.rewardOK(PulseEscrowAddress(poolB, m2), 1, "b-proof")
	// Pool A lists pool B's escrowed proof (its owner given weight): refused.
	w := []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}, {Owner: f.m2.ID(), Weight: 1}}
	b, s := f.batch(1, 0, []string{proofHex("b-proof")}, w)
	if err := f.apply(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s})); !errors.Is(err, ErrPulseMismatch) {
		t.Fatalf("pool A settling pool B's escrow: %v", err)
	}
	// Pool B submitting pool A's signed batch: refused as a mismatch, before
	// any signature check against B's key.
	if err := f.apply(f.pulseTx(other, PulseAction{Op: "settle", Batch: b, BatchSig: s})); !errors.Is(err, ErrPulseMismatch) {
		t.Fatalf("pool B sending pool A's batch: %v", err)
	}
	_ = m1
}

func TestPulsePayeeCapAccumulatesWithinAnEpoch(t *testing.T) {
	p := activeParams()
	p.PayeeEpochCapDust = 50_000_000
	f := newPulseFix(t, p)
	pool := string(f.pool.ID())
	m1 := string(f.m1.ID())
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: pool}))
	w := []pulse.Weight{{Owner: f.m1.ID(), Weight: 1}}
	for i, id := range []string{"e1", "e2"} {
		f.rewardOK(PulseEscrowAddress(pool, m1), 0.4, id)
		b, s := f.batch(uint64(i+1), 0, []string{proofHex(id)}, w)
		f.must(f.pulseTx(f.pool, PulseAction{Op: "settle", Batch: b, BatchSig: s}))
	}
	// 0.4 + 0.4 found, cap 0.5 in one epoch: 0.5 paid, 0.3 carried.
	if !near(f.bal(m1), 0.5) || f.ps.Owed(pool)[m1] != 30_000_000 {
		t.Fatalf("paid %v owed %v", f.bal(m1), f.ps.Owed(pool))
	}
}

func TestPulseStateFoldsIntoTheBlockRoot(t *testing.T) {
	f := newPulseFix(t, activeParams())
	plain := NewEnrollmentAwareApplier(f.accounts, NewEnrollmentApplier(f.accounts, f.state))
	before := f.aware.StateRoot()
	if before != plain.StateRoot() {
		t.Fatal("empty PULSE state changed the root")
	}
	f.must(f.pulseTx(f.m1, PulseAction{Op: "join", Pool: string(f.pool.ID())}))
	after := f.aware.StateRoot()
	if after == plain.StateRoot() {
		t.Fatal("PULSE membership is not committed by the block state root")
	}
}
