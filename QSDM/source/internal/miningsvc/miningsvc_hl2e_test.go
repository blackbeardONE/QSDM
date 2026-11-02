package miningsvc

// HL2 WP-E: a version 2 service serves and enforces the config's
// difficulty_bits. /work advertises 2^bits, a miner that solves against the
// advertised target is accepted, and a proof that only meets an easier
// target is rejected at Verify step 10 (400, reason work).

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// hl2eBits keeps the test fast: 2^6 attempts per proof. The mapping from
// difficulty_bits to the served difficulty is the same for 16..48
// (legacymining.ConfigDifficulty).
const hl2eBits = 6

func hl2eService(t *testing.T) (*hl1Fixture, *fakeOwnerGuard, *Service) {
	t.Helper()
	f := newHL1Fixture(t, 2, 0)
	g := newFakeOwnerGuard(f.log)
	g.cfg.DifficultyBits = hl2eBits
	f.guard = g.fakeGuard
	f.cfg.Guard = g
	f.cfg.Difficulty = legacymining.ConfigDifficulty(g.cfg)
	return f, g, f.service(t)
}

func TestHL2NewV2DifficultyMustMatchConfig(t *testing.T) {
	withoutPinnedCompat(t)
	f := newHL1Fixture(t, 1, 0)
	g := newFakeOwnerGuard(f.log)
	g.cfg.DifficultyBits = 20
	f.cfg.Guard = g
	for _, d := range []*big.Int{big.NewInt(2), new(big.Int).Set(mining.DefaultMinDifficulty), big.NewInt(1<<20 + 1)} {
		f.cfg.Difficulty = d
		if svc, err := New(f.cfg); err == nil || svc != nil || !strings.Contains(err.Error(), "difficulty_bits") {
			t.Fatalf("New(v2 bits 20, difficulty %v) = %v, %v; want refusal", d, svc, err)
		}
	}
	f.cfg.Difficulty = big.NewInt(1 << 20)
	if _, err := New(f.cfg); err != nil {
		t.Fatalf("New(v2 bits 20, 2^20): %v", err)
	}
	// A read-only service and a v1 config are not affected.
	f.cfg.Difficulty = big.NewInt(2)
	f.cfg.ReadOnly = true
	if _, err := New(f.cfg); err != nil {
		t.Fatalf("read-only: %v", err)
	}
	f.cfg.ReadOnly = false
	f.cfg.Guard = f.guard
	if _, err := New(f.cfg); err != nil {
		t.Fatalf("v1: %v", err)
	}
}

// minerSolve does what cmd/qsdmminer-console's runLoop does with a /work
// payload: decode it with api.WorkToMiningCore, derive the target from the
// advertised difficulty, build the DAG and solve. target overrides the
// advertised target when non-nil (a miner that ignores the advertisement).
func minerSolve(t *testing.T, work *api.MiningWork, minerAddr string, target *big.Int) *mining.Proof {
	t.Helper()
	ws, hdr, diff, err := api.WorkToMiningCore(work)
	if err != nil {
		t.Fatalf("WorkToMiningCore: %v", err)
	}
	ws.Canonicalize()
	root, err := ws.PrefixRoot(1)
	if err != nil {
		t.Fatal(err)
	}
	if target == nil {
		if target, err = mining.TargetFromDifficulty(diff); err != nil {
			t.Fatal(err)
		}
	}
	dag, err := mining.NewInMemoryDAG(work.Epoch, ws.Root(), work.DAGSize)
	if err != nil {
		t.Fatal(err)
	}
	res, err := mining.Solve(context.Background(), mining.SolverParams{
		Epoch: work.Epoch, Height: work.Height, HeaderHash: hdr, MinerAddr: minerAddr,
		BatchRoot: root, BatchCount: 1, Target: target, DAG: dag,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return res.Proof
}

func TestHL2WorkAdvertisesAndVerifyEnforcesDifficulty(t *testing.T) {
	f, g, svc := hl2eService(t)
	work, err := svc.WorkAt(svc.TipHeight() + 1)
	if err != nil {
		t.Fatalf("WorkAt: %v", err)
	}
	if want := new(big.Int).Lsh(big.NewInt(1), hl2eBits).String(); work.Difficulty != want {
		t.Fatalf("/work difficulty %q, want %q (2^difficulty_bits)", work.Difficulty, want)
	}

	// The miner adapts: solving against the advertised target is accepted.
	p := minerSolve(t, work, "qsdm1owner", nil)
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Submit(raw); err != nil {
		t.Fatalf("Submit at the advertised difficulty: %v", err)
	}
	if f.store.count() != 1 {
		t.Fatalf("stored %d rows, want 1", f.store.count())
	}

	// A proof below the target: it meets difficulty 2 (the HL1 test value)
	// but not 2^bits. Verify rejects it with reason work, nothing is stored
	// and the rejection is attributed to the owner (a bad submission).
	easy, err := mining.TargetFromDifficulty(big.NewInt(2))
	if err != nil {
		t.Fatal(err)
	}
	strict, err := mining.TargetFromDifficulty(legacymining.ConfigDifficulty(g.cfg))
	if err != nil {
		t.Fatal(err)
	}
	var weak []byte
	for i := 0; i < 200 && weak == nil; i++ {
		q := minerSolve(t, work, "qsdm1owner", easy)
		if mining.MeetsTarget(mining.ProofPoWHash(q.HeaderHash, q.Nonce, q.BatchRoot, q.MixDigest), strict) {
			continue // also meets the strict target; try another nonce range
		}
		if weak, err = q.CanonicalJSON(); err != nil {
			t.Fatal(err)
		}
	}
	if weak == nil {
		t.Fatal("no proof below the strict target in 200 tries")
	}
	_, err = svc.Submit(weak)
	checkClass(t, err, 400, 0, mining.ReasonWork)
	if !strings.Contains(err.Error(), "does not meet target") {
		t.Fatalf("rejection detail: %v", err)
	}
	if f.store.count() != 1 {
		t.Fatalf("a proof below the target was stored (%d rows)", f.store.count())
	}
	if len(g.observed) != 1 || g.observed[0].Owner != "qsdm1owner" {
		t.Fatalf("observed candidates %+v", g.observed)
	}
}
