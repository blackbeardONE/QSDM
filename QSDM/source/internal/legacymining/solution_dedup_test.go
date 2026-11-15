package legacymining

import (
	"encoding/binary"
	"strings"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// sdRecord is stRecord with the owner and node chosen: the same seq gives the
// same solution (height, header_hash, nonce), whoever submits it.
func sdRecord(t *testing.T, seq uint64, miner, node string, attNonce [32]byte, acceptTip uint64) Record {
	t.Helper()
	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Height:     200 + seq%7,
		MinerAddr:  miner,
		BatchCount: 1,
		Attestation: mining.Attestation{
			Type:               mining.AttestationTypeHMAC,
			BundleBase64:       "YnVuZGxl",
			GPUArch:            "ada",
			ClaimedHashrateHPS: 1,
			Nonce:              attNonce,
			IssuedAt:           1_790_000_000,
		},
	}
	binary.BigEndian.PutUint64(p.Nonce[:8], seq)
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	id, err := p.ID()
	if err != nil {
		t.Fatal(err)
	}
	if acceptTip == 0 {
		acceptTip = p.Height + 1
	}
	return Record{
		ProofID:      id,
		MinerAddr:    miner,
		NodeID:       node,
		AttNonce:     attNonce,
		WorkHeight:   p.Height,
		AcceptTip:    acceptTip,
		AcceptedNS:   int64(1_000 + seq),
		ConfigSHA256: stCfg1,
		ProofJSON:    raw,
	}
}

var sdOtherOwner = strings.Repeat("cd", 32)

func sdStore(t *testing.T) (*SQLiteStore, *SolutionDedupStore) {
	t.Helper()
	requireHLSQLite(t)
	inner := stCreate(t, stDir(t))
	stWindow(t, inner, stCfg1, 100)
	return inner, NewSolutionDedupStore(inner)
}

// Wrapping never fails (the node wires it before every store is open); a
// store that cannot list its rows leaves the index unseeded and the wrapped
// store's own Accept error is returned unchanged.
func TestSolutionDedupWrapsAnUnopenedStore(t *testing.T) {
	s := NewSolutionDedupStore(stNewStore(t))
	err := s.Accept(sdRecord(t, 1, stMiner, stNode, stNonce(1), 0))
	if err == nil || RejectKindOf(err) != 0 {
		t.Fatalf("Accept on a closed store = %v, want the store's plain error", err)
	}
	if s.SeedErr() == nil {
		t.Fatal("the failed seed was not recorded")
	}
}

// One solution, two owners: the second proof has its own ID (the ID covers
// miner_addr) and passes the table's UNIQUE checks, but the index refuses it.
func TestSolutionDedupRefusesTheSameSolutionUnderAnotherOwner(t *testing.T) {
	inner, s := sdStore(t)
	a := sdRecord(t, 1, stMiner, stNode, stNonce(1), 0)
	b := sdRecord(t, 1, sdOtherOwner, "sybil-node-2", stNonce(2), 0)
	if a.ProofID == b.ProofID {
		t.Fatal("test setup: the two proofs must have different IDs")
	}
	if err := s.Accept(a); err != nil {
		t.Fatalf("first: %v", err)
	}
	before := SolutionDuplicatesRefused()
	stWantReject(t, s.Accept(b), KindDuplicate, mining.ReasonDuplicate)
	if SolutionDuplicatesRefused() != before+1 {
		t.Fatal("the refusal was not counted")
	}
	// Without the index the table alone would store it.
	if err := inner.Accept(b); err != nil {
		t.Fatalf("control: the bare store refused %v", err)
	}
	if got := len(stPendingIDs(t, inner)); got != 2 {
		t.Fatalf("control: %d rows, want 2", got)
	}
}

// A different nonce is different work and is accepted for any owner; the
// same proof again is the store's own duplicate.
func TestSolutionDedupKeepsOtherSolutionsAndResubmissions(t *testing.T) {
	_, s := sdStore(t)
	if err := s.Accept(sdRecord(t, 1, stMiner, stNode, stNonce(1), 0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Accept(sdRecord(t, 8, sdOtherOwner, "node-2", stNonce(2), 0)); err != nil {
		t.Fatalf("another solution of the same height: %v", err)
	}
	if err := s.Accept(sdRecord(t, 2, stMiner, stNode, stNonce(3), 0)); err != nil {
		t.Fatalf("the first owner's next solution: %v", err)
	}
	stWantReject(t, s.Accept(sdRecord(t, 1, stMiner, stNode, stNonce(1), 0)), KindDuplicate, mining.ReasonDuplicate)
}

// The index is seeded from the unpaid rows, so a wrapped store refuses a
// solution stored before it was wrapped (a restart).
func TestSolutionDedupSeedsFromPendingRows(t *testing.T) {
	requireHLSQLite(t)
	inner := stCreate(t, stDir(t))
	stWindow(t, inner, stCfg1, 100)
	if err := inner.Accept(sdRecord(t, 3, stMiner, stNode, stNonce(1), 0)); err != nil {
		t.Fatal(err)
	}
	s := NewSolutionDedupStore(inner)
	stWantReject(t, s.Accept(sdRecord(t, 3, sdOtherOwner, "node-2", stNonce(2), 0)), KindDuplicate, mining.ReasonDuplicate)
	if s.SeedErr() != nil {
		t.Fatalf("seed: %v", s.SeedErr())
	}
}

// Solutions of heights that can no longer be accepted are dropped.
func TestSolutionDedupEvictsStaleHeights(t *testing.T) {
	_, s := sdStore(t)
	for seq := uint64(0); seq < 7; seq++ {
		if err := s.Accept(sdRecord(t, seq, stMiner, stNode, stNonce(byte(seq)), 0)); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.solutionIndexLen(); got != 7 {
		t.Fatalf("index %d, want 7", got)
	}
	// A proof accepted far later moves the floor past every earlier height.
	late := sdRecord(t, 7, stMiner, stNode, stNonce(99), 200+SolutionKeepHeights+50)
	if err := s.Accept(late); err != nil {
		t.Fatal(err)
	}
	if got := s.solutionIndexLen(); got != 0 {
		t.Fatalf("index %d after the floor moved, want 0 (the late proof's own height is stale too)", got)
	}
}

// Two owners racing with one solution: exactly one is stored.
func TestSolutionDedupConcurrentSubmissions(t *testing.T) {
	inner, s := sdStore(t)
	owners := []string{stMiner, sdOtherOwner, strings.Repeat("ef", 32), strings.Repeat("01", 32)}
	recs := make([]Record, len(owners))
	for i, o := range owners {
		recs[i] = sdRecord(t, 5, o, "node-"+o[:4], stNonce(byte(10+i)), 0)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(owners))
	for i := range recs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = s.Accept(recs[i])
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if RejectKindOf(err) != KindDuplicate {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || len(stPendingIDs(t, inner)) != 1 {
		t.Fatalf("%d accepted, %d rows; want exactly one", ok, len(stPendingIDs(t, inner)))
	}
}
