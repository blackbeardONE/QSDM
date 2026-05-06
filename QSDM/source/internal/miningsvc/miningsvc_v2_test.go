package miningsvc

// miningsvc_v2_test.go: regression tests for the v2 (NVIDIA-
// locked) attestation gate as wired through Service.Submit.
// Confirms that:
//
//  1. With the v2 fork active and a real *attest.Dispatcher
//     plumbed via Config.Attestation, a v1 (CPU) proof is
//     rejected before any reward sink notification fires.
//
//  2. With the same dispatcher but the fork NOT active,
//     v1 proofs continue to flow as before — the dispatcher
//     never gets called, and a successfully-mined v1 proof is
//     accepted. Protects the testnet bring-up posture from
//     accidental tightening.
//
// Why this lives in miningsvc and not in pkg/mining: the
// goal is to exercise the FULL plumbing the validator binary
// uses (Config.Attestation → mining.VerifierConfig.Attestation
// → Verifier.Verify dispatcher hook). pkg/mining tests cover
// the verifier in isolation; this file covers the wiring.

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// resetForkV2Local mirrors pkg/mining.resetForkV2 (which is
// unexported) so we can pin / restore the fork height around
// each test without leaking state into siblings.
func resetForkV2Local(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { mining.SetForkV2Height(math.MaxUint64) })
}

// buildV2Dispatcher returns a fully-wired *attest.Dispatcher
// over fresh in-memory collaborators. The Registry is empty —
// any HMAC bundle the verifier sees lookups to
// hmac.ErrNodeNotRegistered → reject. That's the right fail-
// closed posture for these tests: we are not asserting v2
// happy-path acceptance here (that lives in
// pkg/mining/attest tests against synthetic bundles); we are
// asserting v2 REJECTS unattested traffic with the dispatcher
// wired through miningsvc.
func buildV2Dispatcher(t *testing.T) *attest.Dispatcher {
	t.Helper()
	chSig, err := challenge.NewHMACSigner(
		"validator-test",
		[]byte("0123456789abcdef0123456789abcdef"), // 32-byte test key
	)
	if err != nil {
		t.Fatalf("challenge signer: %v", err)
	}
	chSV := challenge.NewHMACSignerVerifier()
	if err := chSV.Register(
		chSig.SignerID(),
		[]byte("0123456789abcdef0123456789abcdef"),
	); err != nil {
		t.Fatalf("challenge signer-verifier register: %v", err)
	}
	disp, err := attest.NewProductionDispatcher(attest.ProductionConfig{
		Registry:          enrollment.NewStateBackedRegistry(enrollment.NewInMemoryState()),
		ChallengeVerifier: chSV,
		NonceStore:        hmac.NewInMemoryNonceStore(2 * mining.FreshnessWindow),
	})
	if err != nil {
		t.Fatalf("dispatcher: %v", err)
	}
	return disp
}

// TestV2Gate_RejectsCPUProofPostFork is the regression that
// pins the user-visible promise: with QSDM_V2_ACTIVE on,
// CPU miners cannot submit a proof. The verifier rejects
// before the proof's mix_digest is even recomputed, and the
// reward sink is NOT notified.
func TestV2Gate_RejectsCPUProofPostFork(t *testing.T) {
	resetForkV2Local(t)
	mining.SetForkV2Height(0) // every height is post-fork

	cfg := validConfig(t)
	cfg.Attestation = buildV2Dispatcher(t)
	sink := &capturingSink{}
	cfg.RewardSink = sink

	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Solve a v1 (CPU) proof against the served work — same
	// flow as TestEndToEnd_SolveAndSubmit but post-fork.
	tip := cfg.Producer.TipHeight()
	work, err := svc.WorkAt(tip)
	if err != nil {
		t.Fatalf("WorkAt: %v", err)
	}
	ws, hdr, diff, _ := api.WorkToMiningCore(work)
	ws.Canonicalize()
	batchRoot, _ := ws.PrefixRoot(1)
	target, _ := mining.TargetFromDifficulty(diff)
	dag, _ := mining.NewInMemoryDAG(work.Epoch, ws.Root(), work.DAGSize)
	res, err := mining.Solve(context.Background(), mining.SolverParams{
		Epoch: work.Epoch, Height: work.Height, HeaderHash: hdr,
		MinerAddr: "qsdm1cpu-attempt", BatchRoot: batchRoot, BatchCount: 1,
		Target: target, DAG: dag,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	raw, err := res.Proof.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}

	// The proof is a fully-valid v1 (Version=1, no
	// Attestation) PoW. Post-fork it MUST reject — either at
	// the version gate ("post-fork got v1 want v2") or at the
	// attestation gate (Type==""). Both reasons live under
	// the v2-active rejection family.
	if _, err := svc.Submit(raw); err == nil {
		t.Fatal("post-fork v1 (CPU) proof was accepted; v2 gate did not engage")
	} else {
		// Sanity-check the rejection mentions the expected
		// post-fork reason.
		msg := err.Error()
		if !(strings.Contains(msg, "post-fork") ||
			strings.Contains(msg, "attestation")) {
			t.Fatalf("post-fork rejection should reference version or attestation; got: %v", err)
		}
	}

	// Reward sink MUST NOT be notified for a rejected proof.
	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("reward sink notified for rejected v1 proof: %v", got)
	}
}

// TestV2Gate_AcceptsV1ProofPreFork is the symmetric guarantee:
// with the fork NOT active (the testnet posture), wiring the
// dispatcher into Config.Attestation is a no-op for v1
// traffic. Without this, a deploy that wired the dispatcher
// "just in case" would silently break every v1 proof.
func TestV2Gate_AcceptsV1ProofPreFork(t *testing.T) {
	resetForkV2Local(t) // restores MaxUint64 on cleanup; default is also MaxUint64

	cfg := validConfig(t)
	cfg.Attestation = buildV2Dispatcher(t)
	sink := &capturingSink{}
	cfg.RewardSink = sink

	svc, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	tip := cfg.Producer.TipHeight()
	work, err := svc.WorkAt(tip)
	if err != nil {
		t.Fatalf("WorkAt: %v", err)
	}
	ws, hdr, diff, _ := api.WorkToMiningCore(work)
	ws.Canonicalize()
	batchRoot, _ := ws.PrefixRoot(1)
	target, _ := mining.TargetFromDifficulty(diff)
	dag, _ := mining.NewInMemoryDAG(work.Epoch, ws.Root(), work.DAGSize)
	res, err := mining.Solve(context.Background(), mining.SolverParams{
		Epoch: work.Epoch, Height: work.Height, HeaderHash: hdr,
		MinerAddr: "qsdm1prefork", BatchRoot: batchRoot, BatchCount: 1,
		Target: target, DAG: dag,
	}, nil, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	raw, _ := res.Proof.CanonicalJSON()

	if _, err := svc.Submit(raw); err != nil {
		t.Fatalf("pre-fork v1 proof rejected: %v", err)
	}
	if got := sink.snapshot(); len(got) != 1 || got[0] != "qsdm1prefork" {
		t.Fatalf("reward sink not notified for accepted v1 proof: %v", got)
	}
}
