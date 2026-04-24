// Command qsdmminer is the pure-Go CPU reference miner for QSDM / Cell.
//
// Status: reference implementation. The CUDA production miner ships
// under pkg/mining/cuda after the external security review of Major
// Update Phase 6. For home-miner deployment instructions see
// QSDM/docs/docs/MINER_QUICKSTART.md.
//
// This binary intentionally runs on a single CPU core at reference-
// quality speed. Its purposes are:
//
//  1. Validate that pkg/mining and pkg/api round-trip an end-to-end mining
//     flow (proof derivation → verify) against a live validator.
//  2. Give protocol implementers a minimal, readable reference they can
//     audit against MINING_PROTOCOL.md line by line.
//  3. Satisfy Major Update Phase 4.5 acceptance gate: "reference miner
//     produces a valid proof for a test epoch accepted by pkg/mining
//     verifier".
//
// Run with --help for the full flag list. The two primary modes are:
//
//   - --self-test
//       Build a synthetic work-set, DAG, and easy difficulty entirely in
//       memory; solve a proof; verify it against the in-memory pkg/mining
//       verifier; print pass/fail. Used by the Phase 4.5 gate and by
//       downstream CI.
//
//   - (default — connect to validator)
//       GET /api/v1/mining/work from the validator, build the DAG
//       locally, call mining.Solve, POST the result to
//       /api/v1/mining/submit. Loops forever until interrupted.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/buildinfo"
	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// binaryName is the exec name we advertise via --version. Keeping it
// as a const (not os.Args[0]) means a user running `./my-renamed-bin
// --version` still sees a consistent identifier in bug reports.
const binaryName = "qsdmminer"

func main() {
	var (
		validatorURL = flag.String("validator", "http://127.0.0.1:8080", "base URL of the QSDM validator HTTP API")
		minerAddr    = flag.String("address", "", "reward address to credit when a proof is accepted")
		batchCount   = flag.Uint("batch-count", 1, "number of workset batches to claim per proof (must be <= work.batch_count_maximum)")
		selfTest     = flag.Bool("self-test", false, "run a fully in-memory solve+verify cycle and exit 0 on success")
		selfTestEasy = flag.Int("self-test-difficulty", 2, "difficulty scalar for --self-test (1=trivial, higher=slower); default 2 matches the pkg/mining unit tests")
		pollInterval = flag.Duration("poll", 2*time.Second, "how often to re-fetch /api/v1/mining/work between rounds")
		httpTimeout  = flag.Duration("http-timeout", 30*time.Second, "per-request HTTP timeout")
		progress     = flag.Bool("progress", true, "periodically print hashrate on stderr")
		showVersion  = flag.Bool("version", false, "print build metadata (release tag, git SHA, build date, runtime) and exit")
	)
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"%s — reference CPU miner (MINING_PROTOCOL.md v%d)\n\n",
			branding.FullTitle(), mining.ProtocolVersion)
		fmt.Fprintf(flag.CommandLine.Output(),
			"Usage:\n  %s [flags]\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	// --version is intentionally checked before any other side-effect
	// (no HTTP, no self-test, no config touch) so it stays usable on a
	// half-provisioned host. Exit 0 so scripts can detect the binary
	// with `./qsdmminer --version >/dev/null`.
	if *showVersion {
		fmt.Println(buildinfo.String(binaryName))
		return
	}

	if *selfTest {
		if err := runSelfTest(*selfTestEasy); err != nil {
			fmt.Fprintf(os.Stderr, "self-test FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("self-test OK: proof solved and verified end-to-end via pkg/mining")
		return
	}

	// NVIDIA-lock pivot notice. See cmd/qsdmminer-console/main.go for
	// the rationale — the banner is deliberately factored as a local
	// function in both miners rather than a shared helper, because
	// cmd/qsdmminer stays intentionally dependency-minimal for
	// protocol-audit purposes (see the package doc comment).
	fmt.Fprintln(os.Stderr, "┌─────────────────────────────────────────────────────────────────────┐")
	fmt.Fprintln(os.Stderr, "│  qsdmminer: NVIDIA-lock pivot in progress                           │")
	fmt.Fprintln(os.Stderr, "│  Once the v2 protocol activates (see                                │")
	fmt.Fprintln(os.Stderr, "│  nvidia_locked_qsdmplus_blockchain_architecture.md), CPU proofs     │")
	fmt.Fprintln(os.Stderr, "│  will NOT be accepted on mainnet. Binary kept for testnet/replay.   │")
	fmt.Fprintln(os.Stderr, "└─────────────────────────────────────────────────────────────────────┘")

	if *minerAddr == "" {
		fmt.Fprintln(os.Stderr, "--address is required when not running --self-test")
		os.Exit(2)
	}
	if *batchCount == 0 {
		fmt.Fprintln(os.Stderr, "--batch-count must be >= 1")
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client := &http.Client{Timeout: *httpTimeout}
	runLoop(ctx, client, *validatorURL, *minerAddr, uint32(*batchCount), *pollInterval, *progress)
}

// -----------------------------------------------------------------------------
// Self-test (Phase 4.5 acceptance gate)
// -----------------------------------------------------------------------------

// runSelfTest is the deterministic end-to-end smoke test required by
// Major Update Phase 4.5. It exercises the entire mining pipeline using
// only pkg/mining primitives:
//
//  1. Build a synthetic 4-batch work-set.
//  2. Materialise a small in-memory DAG for mining-epoch 0.
//  3. Solve a nonce under easy difficulty via mining.Solve.
//  4. Verify the solved proof via mining.Verifier (injected fakes for
//     chain, address, batch validators).
//
// Returns nil on a full accept.
func runSelfTest(difficultyScalar int) error {
	// 1. Work-set.
	ws := syntheticWorkSet(4)
	const dagN = 128

	epoch := uint64(0)
	dag, err := mining.NewInMemoryDAG(epoch, ws.Root(), dagN)
	if err != nil {
		return fmt.Errorf("dag: %w", err)
	}
	difficulty := big.NewInt(int64(difficultyScalar))
	if difficulty.Sign() <= 0 {
		return errors.New("self-test difficulty must be > 0")
	}
	target, err := mining.TargetFromDifficulty(difficulty)
	if err != nil {
		return err
	}
	headerHash := [32]byte{0x5E, 0x1F, 0x7E, 0x57} // "SELFTEST" pattern
	batchRoot, err := ws.PrefixRoot(1)
	if err != nil {
		return err
	}

	// 2. Solve.
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := mining.Solve(ctx, mining.SolverParams{
		Epoch:      epoch,
		Height:     60_480 * 0, // epoch 0, first block
		HeaderHash: headerHash,
		MinerAddr:  "qsdm1selftest",
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Target:     target,
		DAG:        dag,
	}, nil, nil)
	if err != nil {
		return fmt.Errorf("solve: %w", err)
	}

	// 3. Verify.
	verifier, err := mining.NewVerifier(mining.VerifierConfig{
		EpochParams:      mining.NewEpochParams(),
		DifficultyParams: mining.NewDifficultyAdjusterParams(),
		Chain: &selftestChain{
			tip:    0,
			header: headerHash,
		},
		Addresses:       selftestAddr{},
		Batches:         selftestBatch{},
		Dedup:           mining.NewProofIDSet(1024),
		Quarantine:      mining.NewQuarantineSet(),
		DAGProvider:     func(_ uint64) (mining.DAG, error) { return dag, nil },
		WorkSetProvider: func(_ uint64) (mining.WorkSet, error) { return ws, nil },
		DifficultyAt:    func(_ uint64) (*big.Int, error) { return difficulty, nil },
	})
	if err != nil {
		return fmt.Errorf("verifier: %w", err)
	}
	raw, err := res.Proof.CanonicalJSON()
	if err != nil {
		return err
	}
	id, err := verifier.Verify(raw, 0)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	fmt.Printf("self-test: solved in %d attempts, proof_id=%s\n", res.Attempts, hex.EncodeToString(id[:8])+"…")
	return nil
}

func syntheticWorkSet(n int) mining.WorkSet {
	ws := mining.WorkSet{Batches: make([]mining.Batch, n)}
	for i := 0; i < n; i++ {
		cells := make([]mining.ParentCellRef, 3)
		for j := 0; j < 3; j++ {
			var ch [32]byte
			ch[0] = byte(i)
			ch[1] = byte(j)
			cells[j] = mining.ParentCellRef{
				ID:          []byte{byte(i), byte(j), 0xAB},
				ContentHash: ch,
			}
		}
		ws.Batches[i] = mining.Batch{Cells: cells}
	}
	ws.Canonicalize()
	return ws
}

// Self-test stubs: real fakes, not placeholders. Each implements exactly
// the behaviour the verifier interface demands, using in-memory state.

type selftestChain struct {
	tip    uint64
	header [32]byte
}

func (c *selftestChain) TipHeight() uint64 { return c.tip }
func (c *selftestChain) HeaderHashAt(h uint64) ([32]byte, bool) {
	if h == c.tip {
		return c.header, true
	}
	return [32]byte{}, false
}

type selftestAddr struct{}

func (selftestAddr) ValidateAddress(a string) error {
	if a == "" {
		return errors.New("empty address")
	}
	return nil
}

type selftestBatch struct{}

func (selftestBatch) ValidateBatch(_ mining.Batch) error { return nil }

// -----------------------------------------------------------------------------
// Remote mining loop
// -----------------------------------------------------------------------------

// runLoop drives the real mining loop against a remote validator. It
// fetches work, builds the DAG lazily (caching by epoch), calls the
// solver, and POSTs the proof. On any transport error it backs off and
// retries rather than exiting — miners run unattended.
func runLoop(ctx context.Context, client *http.Client, baseURL, minerAddr string, batchCount uint32, poll time.Duration, showProgress bool) {
	var (
		currentEpoch uint64 = ^uint64(0) // sentinel: we have no DAG yet
		currentDAG   mining.DAG
		attempts     uint64
	)
	if showProgress {
		go hashrateReporter(ctx, &attempts)
	}
	fmt.Printf("%s miner starting: validator=%s address=%s batch_count=%d GOMAXPROCS=%d\n",
		branding.LogPrefix, baseURL, minerAddr, batchCount, runtime.GOMAXPROCS(0))
	for {
		if ctx.Err() != nil {
			return
		}
		work, err := fetchWork(ctx, client, baseURL)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch work: %v (retry in %s)\n", err, poll)
			sleepOrCancel(ctx, poll)
			continue
		}
		if work.BatchCountMaximum > 0 && batchCount > work.BatchCountMaximum {
			fmt.Fprintf(os.Stderr, "--batch-count %d > server maximum %d; clamping\n", batchCount, work.BatchCountMaximum)
			batchCount = work.BatchCountMaximum
		}
		ws, hdr, diff, err := api.WorkToMiningCore(work)
		if err != nil {
			fmt.Fprintf(os.Stderr, "decode work: %v\n", err)
			sleepOrCancel(ctx, poll)
			continue
		}
		ws.Canonicalize()
		batchRoot, err := ws.PrefixRoot(batchCount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "prefix root: %v\n", err)
			sleepOrCancel(ctx, poll)
			continue
		}
		target, err := mining.TargetFromDifficulty(diff)
		if err != nil {
			fmt.Fprintf(os.Stderr, "target: %v\n", err)
			sleepOrCancel(ctx, poll)
			continue
		}
		if work.Epoch != currentEpoch {
			fmt.Printf("%s new mining epoch %d (building DAG, N=%d)\n",
				branding.LogPrefix, work.Epoch, work.DAGSize)
			start := time.Now()
			dag, err := mining.NewInMemoryDAG(work.Epoch, ws.Root(), work.DAGSize)
			if err != nil {
				fmt.Fprintf(os.Stderr, "build DAG: %v\n", err)
				sleepOrCancel(ctx, poll)
				continue
			}
			fmt.Printf("%s DAG built in %s\n", branding.LogPrefix, time.Since(start))
			currentDAG = dag
			currentEpoch = work.Epoch
		}
		sctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		res, err := mining.Solve(sctx, mining.SolverParams{
			Epoch:      work.Epoch,
			Height:     work.Height,
			HeaderHash: hdr,
			MinerAddr:  minerAddr,
			BatchRoot:  batchRoot,
			BatchCount: batchCount,
			Target:     target,
			DAG:        currentDAG,
		}, nil, &attempts)
		cancel()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				continue
			}
			fmt.Fprintf(os.Stderr, "solve: %v\n", err)
			sleepOrCancel(ctx, poll)
			continue
		}
		raw, err := res.Proof.CanonicalJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "encode proof: %v\n", err)
			continue
		}
		resp, err := submitProof(ctx, client, baseURL, raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "submit: %v\n", err)
			sleepOrCancel(ctx, poll)
			continue
		}
		if resp.Accepted {
			fmt.Printf("%s proof ACCEPTED height=%d epoch=%d attempts=%d id=%s\n",
				branding.LogPrefix, work.Height, work.Epoch, res.Attempts, resp.ProofID)
		} else {
			fmt.Printf("%s proof REJECTED reason=%s detail=%q\n",
				branding.LogPrefix, resp.RejectReason, resp.Detail)
		}
	}
}

func fetchWork(ctx context.Context, client *http.Client, baseURL string) (*api.MiningWork, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/v1/mining/work", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	var work api.MiningWork
	if err := json.Unmarshal(body, &work); err != nil {
		return nil, fmt.Errorf("decode work: %w", err)
	}
	return &work, nil
}

func submitProof(ctx context.Context, client *http.Client, baseURL string, raw []byte) (*api.MiningSubmitResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/v1/mining/submit", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out api.MiningSubmitResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("decode submit (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return &out, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return &out, nil
}

func hashrateReporter(ctx context.Context, attempts *uint64) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	last := atomic.LoadUint64(attempts)
	lastAt := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			cur := atomic.LoadUint64(attempts)
			dt := now.Sub(lastAt).Seconds()
			if dt > 0 {
				rate := float64(cur-last) / dt
				fmt.Fprintf(os.Stderr, "%s hashrate: %.2f H/s (%d attempts total)\n",
					branding.LogPrefix, rate, cur)
			}
			last = cur
			lastAt = now
		}
	}
}

func sleepOrCancel(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
