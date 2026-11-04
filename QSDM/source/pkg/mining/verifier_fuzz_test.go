package mining

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/attest/archcheck"
)

// FuzzVerifyProof drives the acceptance pipeline (MINING_PROTOCOL.md §7) that
// every POST /api/v1/mining/submit body reaches through
// internal/miningsvc.Submit, against an in-memory chain, work set and DAG.
//
// In "harness solves" mode the harness parses the input, rewrites the fields
// a miner derives from the chain (epoch, header hash, batch root, mix digest,
// nonce until the target is met) and re-encodes it canonically, so the
// fuzzer reaches acceptance with arbitrary heights, versions, addresses and
// attestations. Otherwise the bytes reach the verifier exactly as sent.

// fuzzAttestOK is the only bundle the fake attestation verifier accepts.
const fuzzAttestOK = "fuzz-attestation-ok"

// fuzzAttestation records what the verifier handed to the attestation hook.
type fuzzAttestation struct {
	calls int
	last  Proof
}

func (a *fuzzAttestation) VerifyAttestation(p Proof, _ time.Time) error {
	a.calls++
	a.last = p
	if p.Attestation.BundleBase64 != fuzzAttestOK {
		return ErrAttestationSignatureInvalid
	}
	return nil
}

// fuzzBatches rejects exactly one batch of the work set, so the spot check
// can fail.
type fuzzBatches struct{ fraud [32]byte }

func (b fuzzBatches) ValidateBatch(batch Batch) error {
	if BatchHash(batch) == b.fraud {
		return errors.New("forged batch")
	}
	return nil
}

// fuzzMiningEnv is the fixed chain state every input is verified against.
type fuzzMiningEnv struct {
	ws         WorkSet
	dag        DAG
	headers    map[uint64][32]byte
	difficulty *big.Int
	target     *big.Int
	maxBatches uint32
	fraudIdx   int
}

const fuzzBatchCount = 48 // so a proof may claim 1..3 batches

func newFuzzMiningEnv(tb testing.TB) *fuzzMiningEnv {
	tb.Helper()
	ws := WorkSet{Batches: make([]Batch, fuzzBatchCount)}
	for i := range ws.Batches {
		cells := make([]ParentCellRef, 3)
		for j := range cells {
			var ch [32]byte
			ch[0], ch[1] = byte(i), byte(j)
			cells[j] = ParentCellRef{ID: []byte{byte(i), byte(j), 0xAB}, ContentHash: ch}
		}
		ws.Batches[i] = Batch{Cells: cells}
	}
	ws.Canonicalize()
	if err := ws.Validate(); err != nil {
		tb.Fatal(err)
	}
	dag, err := NewInMemoryDAG(0, ws.Root(), 64)
	if err != nil {
		tb.Fatal(err)
	}
	difficulty := big.NewInt(2)
	target, err := TargetFromDifficulty(difficulty)
	if err != nil {
		tb.Fatal(err)
	}
	headers := map[uint64][32]byte{}
	for h := uint64(40); h < 48; h++ {
		headers[h] = sha256.Sum256([]byte{byte(h)})
	}
	return &fuzzMiningEnv{
		ws: ws, dag: dag, headers: headers, difficulty: difficulty, target: target,
		maxBatches: (fuzzBatchCount + 15) / 16, fraudIdx: 1,
	}
}

// verifier returns a fresh verifier (empty dedup and quarantine sets) whose
// chain tip is tip.
func (e *fuzzMiningEnv) verifier(tb testing.TB, tip uint64, att AttestationVerifier) *Verifier {
	tb.Helper()
	v, err := NewVerifier(VerifierConfig{
		EpochParams:      NewEpochParams(),
		DifficultyParams: NewDifficultyAdjusterParams(),
		Chain:            &fakeChain{tip: tip, headers: e.headers},
		Addresses:        permissiveAddr{},
		Batches:          fuzzBatches{fraud: BatchHash(e.ws.Batches[e.fraudIdx])},
		Dedup:            NewProofIDSet(1024),
		Quarantine:       NewQuarantineSet(),
		DAGProvider: func(epoch uint64) (DAG, error) {
			if epoch != 0 {
				return nil, errors.New("no DAG for epoch")
			}
			return e.dag, nil
		},
		WorkSetProvider: func(epoch uint64) (WorkSet, error) {
			if epoch != 0 {
				return WorkSet{}, errors.New("no work set for epoch")
			}
			return e.ws, nil
		},
		DifficultyAt: func(uint64) (*big.Int, error) { return e.difficulty, nil },
		Attestation:  att,
		Now:          func() time.Time { return time.Unix(1_785_585_600, 0) },
	})
	if err != nil {
		tb.Fatal(err)
	}
	return v
}

// solve rewrites the chain-derived fields of p the way an honest miner
// computes them. The miner-chosen fields stay as the fuzzer set them.
func (e *fuzzMiningEnv) solve(p *Proof) {
	p.Epoch = NewEpochParams().EpochForHeight(p.Height)
	if h, ok := e.headers[p.Height]; ok {
		p.HeaderHash = h
	}
	if p.BatchCount >= 1 && p.BatchCount <= e.maxBatches {
		p.BatchRoot, _ = e.ws.PrefixRoot(p.BatchCount)
	}
	for i := 0; i < 256; i++ {
		p.MixDigest, _ = ComputeMixDigest(p.HeaderHash, p.Nonce, e.dag)
		if MeetsTarget(ProofPoWHash(p.HeaderHash, p.Nonce, p.BatchRoot, p.MixDigest), e.target) {
			return
		}
		incrementNonce(&p.Nonce)
	}
}

func FuzzVerifyProof(f *testing.F) {
	env := newFuzzMiningEnv(f)
	if ForkV2TCHeight() != math.MaxUint64 {
		f.Fatal("the harness expects the default mix-digest schedule")
	}
	batchRoot, err := env.ws.PrefixRoot(1)
	if err != nil {
		f.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := [16]byte{7}
	res, err := Solve(ctx, SolverParams{
		Epoch: 0, Height: 42, HeaderHash: env.headers[42], MinerAddr: "qsdm1fuzzminer",
		BatchRoot: batchRoot, BatchCount: 1, Target: env.target, DAG: env.dag,
	}, &start, nil)
	if err != nil {
		f.Fatal(err)
	}
	v1, err := res.Proof.CanonicalJSON()
	if err != nil {
		f.Fatal(err)
	}
	if _, err := env.verifier(f, 42, nil).Verify(v1, 42); err != nil {
		f.Fatalf("genuine seed rejected: %v", err)
	}
	v2 := *res.Proof
	v2.Version = ProtocolVersionV2
	v2.Height = 44
	v2.Attestation = Attestation{
		Type: AttestationTypeHMAC, BundleBase64: fuzzAttestOK, GPUArch: "ada",
		Nonce: [32]byte{1, 2, 3}, IssuedAt: 1_785_585_590,
	}
	env.solve(&v2)
	v2raw, err := v2.CanonicalJSON()
	if err != nil {
		f.Fatal(err)
	}
	prevFork := ForkV2Height()
	SetForkV2Height(44)
	_, err = env.verifier(f, 45, &fuzzAttestation{}).Verify(v2raw, 45)
	SetForkV2Height(prevFork)
	if err != nil {
		f.Fatalf("genuine post-fork seed rejected: %v", err)
	}
	for _, seed := range []struct {
		raw    []byte
		tip    uint64
		forkAt uint64
		solve  bool
	}{
		{v1, 42, math.MaxUint64, false},
		{v1, 47, math.MaxUint64, false},
		{v1, 49, math.MaxUint64, false}, // too late
		{v1, 41, math.MaxUint64, false}, // from the future
		{append(bytes.Clone(v1), '\n'), 42, math.MaxUint64, false},
		{bytes.Replace(v1, []byte(`"epoch":"0"`), []byte(`"epoch":"1"`), 1), 42, math.MaxUint64, true},
		{bytes.Replace(v1, []byte(`"height":"42"`), []byte(`"height":"46"`), 1), 46, math.MaxUint64, true},
		{bytes.Replace(v1, []byte(`"batch_count":1`), []byte(`"batch_count":2`), 1), 42, math.MaxUint64, true},
		{bytes.Replace(v1, []byte(`"batch_count":1`), []byte(`"batch_count":4`), 1), 42, math.MaxUint64, true},
		{v1, 42, 0, false}, // a v1 proof after the fork
		{v2raw, 45, 0, false},
		{v2raw, 45, 44, true},
		{v2raw, 45, math.MaxUint64, false}, // a v2 proof before the fork
		{bytes.Replace(v2raw, []byte(`"gpu_arch":"ada"`), []byte(`"gpu_arch":"ASIC"`), 1), 45, 0, true},
		{bytes.Replace(v2raw, []byte(`"claimed_hashrate_hps":0`), []byte(`"claimed_hashrate_hps":1`), 1), 45, 0, true},
		{[]byte(`{"version":1,"epoch":"0","height":"42","miner_addr":"m","batch_count":1}`), 42, math.MaxUint64, true},
		{[]byte(`{"version":1,"epoch":0,"height":"18446744073709551615"}`), 42, math.MaxUint64, true},
		{[]byte(`null`), 0, 0, false},
	} {
		f.Add(seed.raw, seed.tip, seed.forkAt, seed.solve)
	}

	f.Fuzz(func(t *testing.T, raw []byte, tip, forkAt uint64, solve bool) {
		if solve {
			p, err := ParseProof(raw)
			if err != nil {
				return
			}
			env.solve(p)
			if raw, err = p.CanonicalJSON(); err != nil {
				return
			}
		}
		prevFork := ForkV2Height()
		SetForkV2Height(forkAt)
		defer SetForkV2Height(prevFork)

		att := &fuzzAttestation{}
		v := env.verifier(t, tip, att)
		orig := bytes.Clone(raw)
		id, err := v.Verify(raw, tip)
		if !bytes.Equal(raw, orig) {
			t.Fatal("Verify modified its input")
		}
		if err != nil {
			var rej *RejectError
			if !errors.As(err, &rej) {
				t.Fatalf("rejection is not a *RejectError: %v", err)
			}
			if rej.Reason == ReasonBatchFraud {
				p, _ := ParseProof(raw)
				if p == nil || !v.cfg.Quarantine.IsQuarantined(p.MinerAddr, tip) {
					t.Fatal("a batch-fraud rejection did not quarantine the miner")
				}
			}
			return
		}

		p, err := ParseProof(raw)
		if err != nil {
			t.Fatalf("accepted an unparseable proof: %v", err)
		}
		canonical, err := p.CanonicalJSON()
		if err != nil || !bytes.Equal(canonical, bytes.TrimSpace(raw)) {
			t.Fatalf("accepted a non-canonical encoding: %q", raw)
		}
		if IsV2(p.Height) {
			if p.Version != ProtocolVersionV2 || p.Attestation.Type == "" || att.calls != 1 ||
				!reflect.DeepEqual(att.last, *p) || p.Attestation.BundleBase64 != fuzzAttestOK {
				t.Fatalf("accepted a post-fork proof without its attestation: %+v (%d attestation calls)", p, att.calls)
			}
			arch, err := archcheck.ValidateOuterArch(p.Attestation.GPUArch)
			if err != nil || archcheck.ValidateClaimedHashrate(arch, p.Attestation.ClaimedHashrateHPS) != nil {
				t.Fatalf("accepted gpu_arch %q claiming %d H/s", p.Attestation.GPUArch, p.Attestation.ClaimedHashrateHPS)
			}
		} else if p.Version != ProtocolVersion || att.calls != 0 {
			t.Fatalf("accepted a pre-fork proof of version %d (%d attestation calls)", p.Version, att.calls)
		}
		if p.Height > tip || tip-p.Height > GraceWindow {
			t.Fatalf("accepted height %d at tip %d", p.Height, tip)
		}
		if header, ok := env.headers[p.Height]; !ok || header != p.HeaderHash ||
			p.Epoch != p.Height/DefaultBlocksPerMiningEpoch {
			t.Fatalf("accepted a proof for another chain position: %+v", p)
		}
		if p.BatchCount < 1 || p.BatchCount > env.maxBatches {
			t.Fatalf("accepted batch_count %d", p.BatchCount)
		}
		if root, err := env.ws.PrefixRoot(p.BatchCount); err != nil || root != p.BatchRoot {
			t.Fatal("accepted a batch root that is not the work-set prefix root")
		}
		if spot := binary.BigEndian.Uint32(id[:4]) % p.BatchCount; int(spot) == env.fraudIdx {
			t.Fatalf("accepted a proof whose spot check lands on the forged batch")
		}
		mix, err := ComputeMixDigest(p.HeaderHash, p.Nonce, env.dag)
		if err != nil || mix != p.MixDigest ||
			!MeetsTarget(ProofPoWHash(p.HeaderHash, p.Nonce, p.BatchRoot, p.MixDigest), env.target) {
			t.Fatal("accepted a proof whose work does not check out")
		}
		// proof_id is SHA-256 of the canonical JSON without the attestation
		// (§4.2), the last field of the encoding.
		cut := bytes.LastIndex(canonical, []byte(`,"attestation":`))
		if cut < 0 || id != sha256.Sum256(append(bytes.Clone(canonical[:cut]), '}')) {
			t.Fatalf("proof id %x is not the hash of the attestation-free encoding", id)
		}
		if _, err := v.Verify(raw, tip); err == nil {
			t.Fatal("the same proof was accepted twice")
		} else if rej := (*RejectError)(nil); !errors.As(err, &rej) || rej.Reason != ReasonDuplicate {
			t.Fatalf("a resubmitted proof was not rejected as a duplicate: %v", err)
		}
	})
}
