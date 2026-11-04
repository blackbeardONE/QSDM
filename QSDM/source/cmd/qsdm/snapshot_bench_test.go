package main

// Measurement harness for the snapshot bootstrap
// (QSDM/docs/docs/SNAPSHOT_BOOTSTRAP.md, "Measurements"). It is skipped
// unless QSDM_SNAPSHOT_BENCH names a phase, and each phase is meant to run in
// its own process so that the peak RSS (VmHWM, Linux only) belongs to that
// phase alone:
//
//	QSDM_SNAPSHOT_BENCH=generate|replay|export|import|boot
//	QSDM_SNAPSHOT_BENCH_DIR=<work dir>        (required)
//	QSDM_SNAPSHOT_BENCH_BLOCKS=20000          (generate)
//	QSDM_SNAPSHOT_BENCH_TXS=16                (generate: txs per block, one per sender)
//	QSDM_SNAPSHOT_BENCH_ACCOUNTS=2000         (generate: recipient accounts)
//	QSDM_SNAPSHOT_BENCH_PERSIST=1             (replay: per-block follower persistence on; 0 = in memory)
//
// Build the test binary once, then run the phases in order against one empty
// work directory, each in its own process:
//
//	go test -c -o qsdm.test -tags validator_only,dilithium_circl ./cmd/qsdm
//	export QSDM_SNAPSHOT_BENCH_DIR=/tmp/snapshot-bench
//	for phase in generate replay export import boot; do
//	  QSDM_SNAPSHOT_BENCH=$phase ./qsdm.test -test.run '^TestSnapshotBench$' -test.count=1 -test.timeout=6h
//	done
//
// To measure replay both in memory and with persistence, run it with
// QSDM_SNAPSHOT_BENCH_PERSIST=0 first, delete DIR/replay, then run it again.
//
// generate  seals a synthetic signed chain into DIR/src (journal, accounts,
//           enrollment, staking) and writes DIR/meta.json.
// replay    is today's new-follower path: a follower with the genesis
//           allocation appends every block from genesis through
//           TryAppendExternalBlock (hash, ML-DSA-87 signature, full replay,
//           state root), streaming the journal as peers would serve it, with
//           main()'s follower persistence hook writing DIR/replay.
// export    qsdm --snapshot-export of DIR/src into DIR/snap.
// import    qsdm --snapshot-import of DIR/snap into the empty DIR/imported.
// boot      main()'s restore of DIR/imported: the first start after import
//           (and the cost of every restart, snapshot or not).
//
// Each phase prints one line: SNAPSHOT_BENCH {json}.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const snapBenchSenderBalance = 1e12

type snapBenchMeta struct {
	Blocks     uint64 `json:"blocks"`
	Txs        int    `json:"txs_per_block"`
	Accounts   int    `json:"recipient_accounts"`
	TipHeight  uint64 `json:"tip_height"`
	TipHash    string `json:"tip_hash"`
	TipRoot    string `json:"tip_state_root"`
	Producer   string `json:"producer_id"`
	JournalLen int64  `json:"journal_bytes"`
}

type snapBenchReport struct {
	Phase        string  `json:"phase"`
	Blocks       uint64  `json:"blocks"`
	Txs          int     `json:"txs_per_block"`
	Accounts     int     `json:"recipient_accounts"`
	JournalBytes int64   `json:"journal_bytes"`
	Seconds      float64 `json:"seconds"`
	PeakHeapMB   float64 `json:"peak_heap_inuse_mb"`
	PeakRSSMB    float64 `json:"peak_rss_mb"`
	Persist      *bool   `json:"persist,omitempty"`
	Detail       any     `json:"detail,omitempty"`
}

func snapBenchSender(i int) string { return fmt.Sprintf("bench-s-%03d", i) }

func snapBenchEnvInt(t *testing.T, key string, def int) int {
	t.Helper()
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		t.Fatalf("%s=%q: want a positive integer", key, v)
	}
	return n
}

// snapBenchSampler records the peak HeapInuse while a phase runs.
type snapBenchSampler struct {
	peak atomic.Uint64
	stop chan struct{}
	wg   sync.WaitGroup
}

func snapBenchStartSampler() *snapBenchSampler {
	s := &snapBenchSampler{stop: make(chan struct{})}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			s.sample()
			select {
			case <-s.stop:
				return
			case <-tick.C:
			}
		}
	}()
	return s
}

func (s *snapBenchSampler) sample() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	for {
		cur := s.peak.Load()
		if ms.HeapInuse <= cur || s.peak.CompareAndSwap(cur, ms.HeapInuse) {
			return
		}
	}
}

func (s *snapBenchSampler) finish() float64 {
	s.sample()
	close(s.stop)
	s.wg.Wait()
	return float64(s.peak.Load()) / (1 << 20)
}

// snapBenchVmHWM returns the process peak RSS in MiB (Linux), or -1.
func snapBenchVmHWM() float64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmHWM:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, _ := strconv.ParseFloat(f[1], 64)
				return kb / 1024
			}
		}
	}
	return -1
}

func snapBenchReadMeta(t *testing.T, dir string) snapBenchMeta {
	t.Helper()
	var m snapBenchMeta
	if err := json.Unmarshal(hl1rRead(t, filepath.Join(dir, "meta.json")), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSnapshotBench(t *testing.T) {
	phase := os.Getenv("QSDM_SNAPSHOT_BENCH")
	if phase == "" {
		t.Skip("set QSDM_SNAPSHOT_BENCH=generate|replay|export|import|boot and QSDM_SNAPSHOT_BENCH_DIR")
	}
	dir := os.Getenv("QSDM_SNAPSHOT_BENCH_DIR")
	if dir == "" {
		t.Fatal("QSDM_SNAPSHOT_BENCH_DIR is required")
	}
	runtime.GC()
	rep := snapBenchReport{Phase: phase}
	sampler := snapBenchStartSampler()
	start := time.Now()
	switch phase {
	case "generate":
		m := snapBenchGenerate(t, dir,
			uint64(snapBenchEnvInt(t, "QSDM_SNAPSHOT_BENCH_BLOCKS", 20000)),
			snapBenchEnvInt(t, "QSDM_SNAPSHOT_BENCH_TXS", 16),
			snapBenchEnvInt(t, "QSDM_SNAPSHOT_BENCH_ACCOUNTS", 2000))
		rep.Detail = m
	case "replay":
		persist := os.Getenv("QSDM_SNAPSHOT_BENCH_PERSIST") != "0"
		rep.Persist = &persist
		rep.Detail = snapBenchReplay(t, dir, persist)
	case "export":
		res, err := snapshotRunExport(snapshotExportOptions{StateDir: filepath.Join(dir, "src"), OutDir: filepath.Join(dir, "snap"), Consensus: snapConsensus()})
		if err != nil {
			t.Fatal(err)
		}
		rep.Detail = map[string]any{"journal_load_seconds": res.LoadSeconds, "restore_seconds": res.RestoreSeconds}
	case "import":
		m := snapBenchReadMeta(t, dir)
		res, err := snapshotRunImport(snapshotImportOptions{
			SnapshotDir: filepath.Join(dir, "snap"), StateDir: filepath.Join(dir, "imported"),
			Pinned: config.SnapshotBootstrapConfig{Enabled: true, CheckpointHeight: m.TipHeight, CheckpointBlockHash: m.TipHash,
				CheckpointStateRoot: m.TipRoot, CheckpointProducerID: m.Producer},
			Consensus: snapConsensus(),
		})
		if err != nil {
			t.Fatal(err)
		}
		rep.Detail = map[string]any{"journal_load_seconds": res.LoadSeconds, "restore_seconds": res.RestoreSeconds}
	case "boot":
		m := snapBenchReadMeta(t, dir)
		st, _, err := hl1rRestore(t, filepath.Join(dir, "imported"), nil)
		if err != nil {
			t.Fatal(err)
		}
		if root := st.v2.StateApplier.StateRoot(); root != m.TipRoot {
			t.Fatalf("boot root %s, want %s", root, m.TipRoot)
		}
	default:
		t.Fatalf("unknown phase %q", phase)
	}
	rep.Seconds = time.Since(start).Seconds()
	rep.PeakHeapMB = sampler.finish()
	rep.PeakRSSMB = snapBenchVmHWM()
	if phase != "generate" {
		m := snapBenchReadMeta(t, dir)
		rep.Blocks, rep.Txs, rep.Accounts, rep.JournalBytes = m.Blocks, m.Txs, m.Accounts, m.JournalLen
	} else {
		m := rep.Detail.(snapBenchMeta)
		rep.Blocks, rep.Txs, rep.Accounts, rep.JournalBytes = m.Blocks, m.Txs, m.Accounts, m.JournalLen
		rep.Detail = nil
	}
	data, _ := json.Marshal(rep)
	fmt.Printf("SNAPSHOT_BENCH %s\n", data)
}

// snapBenchGenesis credits the genesis allocation every node starts from.
func snapBenchGenesis(st *hl1ReplayStack, txs int) {
	for i := 0; i < txs; i++ {
		st.accounts.Credit(snapBenchSender(i), snapBenchSenderBalance)
	}
}

func snapBenchGenerate(t *testing.T, dir string, blocks uint64, txs, accounts int) snapBenchMeta {
	t.Helper()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o700); err != nil {
		t.Fatal(err)
	}
	signer := hl1rSigner(t)
	st, err := hl1NewReplayStack(t.TempDir(), nil, nil, nil, logging.NewSilentLogger())
	if err != nil {
		t.Fatal(err)
	}
	st.producer.SetAppendReceiptStore(nil) // receipts are not part of the snapshot; keep generation RAM flat
	st.producer.OnSealedBlock = st.v2.SealedBlockHook
	st.producer.SetBlockSigner(signer)
	snapBenchGenesis(st, txs)
	journal, err := chain.OpenChainJournal(filepath.Join(src, hl1JournalName), nil)
	if err != nil {
		t.Fatal(err)
	}
	nonces := make([]uint64, txs)
	for h := uint64(0); h < blocks; h++ {
		for i := 0; i < txs; i++ {
			tx := &mempool.Tx{
				ID:        fmt.Sprintf("bench-%d-%d", h, i),
				Sender:    snapBenchSender(i),
				Recipient: fmt.Sprintf("bench-a-%06d", (int(h)*txs+i)%accounts),
				Amount:    0.5,
				Nonce:     nonces[i],
			}
			if err := st.pool.Add(tx); err != nil {
				t.Fatal(err)
			}
			nonces[i]++
		}
		blk, err := st.producer.ProduceBlock()
		if err != nil {
			t.Fatalf("block %d: %v", h, err)
		}
		if len(blk.Transactions) != txs {
			t.Fatalf("block %d holds %d txs, want %d", h, len(blk.Transactions), txs)
		}
		if err := journal.Append(blk); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if err := st.accounts.Save(filepath.Join(src, hl1AccountsName)); err != nil {
		t.Fatal(err)
	}
	if err := st.v2.EnrollmentState.Save(filepath.Join(src, hl1EnrollmentName)); err != nil {
		t.Fatal(err)
	}
	if err := chain.SaveStakingLedger(chain.NewStakingLedger(), filepath.Join(src, hl1StakingName)); err != nil {
		t.Fatal(err)
	}
	tip, _ := st.producer.LatestBlock()
	fi, err := os.Stat(filepath.Join(src, hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	m := snapBenchMeta{Blocks: blocks, Txs: txs, Accounts: accounts, TipHeight: tip.Height, TipHash: tip.Hash,
		TipRoot: tip.StateRoot, Producer: signer.Address(), JournalLen: fi.Size()}
	data, _ := json.MarshalIndent(m, "", "  ")
	hl1WriteFile(t, filepath.Join(dir, "meta.json"), string(data)+"\n")
	return m
}

// snapBenchReplay syncs a new follower from genesis, block by block, as the
// P2P/HTTP catch-up does today.
func snapBenchReplay(t *testing.T, dir string, persist bool) map[string]any {
	t.Helper()
	m := snapBenchReadMeta(t, dir)
	dst := filepath.Join(dir, "replay")
	if err := os.MkdirAll(dst, 0o700); err != nil {
		t.Fatal(err)
	}
	st, err := hl1NewReplayStack(dst, nil, []string{m.Producer}, nil, logging.NewSilentLogger())
	if err != nil {
		t.Fatal(err)
	}
	snapBenchGenesis(st, m.Txs)
	var causes []string
	if persist {
		j, err := chain.OpenChainJournal(filepath.Join(dst, hl1JournalName), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer j.Close()
		hook := &hl1PersistHook{
			Mode:             hl1HookNormal,
			ProducerRole:     false, // follower: H1-H6, no watermark
			StateDir:         dst,
			JournalPath:      filepath.Join(dst, hl1JournalName),
			AccountsPath:     filepath.Join(dst, hl1AccountsName),
			EnrollmentPath:   filepath.Join(dst, hl1EnrollmentName),
			ReceiptsPath:     filepath.Join(dst, hl1ReceiptsName),
			FS:               hl1OSFS{},
			Prior:            st.v2.SealedBlockHook,
			AppendJournal:    j.Append,
			SaveAccounts:     st.accounts.Save,
			SaveEnrollment:   st.v2.EnrollmentState.Save,
			AppendReceipts:   st.receipts.AppendBlockNDJSON,
			FailStop:         func(_ int, cause string) { causes = append(causes, cause) },
			OnFailStopReturn: func(error) {},
			Log:              logging.NewSilentLogger(),
			Metrics:          &hl1MetricSet{},
		}
		st.producer.OnSealedBlock = hook.OnSealedBlock
	} else {
		st.producer.OnSealedBlock = st.v2.SealedBlockHook
	}
	f, err := os.Open(filepath.Join(dir, "src", hl1JournalName))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 4<<20)
	var n uint64
	var slowest time.Duration
	var lastTenth time.Duration
	tenth := m.Blocks / 10
	for sc.Scan() {
		var b chain.Block
		if err := json.Unmarshal(sc.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		if err := st.producer.TryAppendExternalBlock(&b); err != nil {
			t.Fatalf("append %d: %v", b.Height, err)
		}
		d := time.Since(t0)
		if d > slowest {
			slowest = d
		}
		if n >= m.Blocks-tenth {
			lastTenth += d
		}
		if len(causes) != 0 {
			t.Fatalf("persistence fail-stop at %d: %v", b.Height, causes)
		}
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	tip, _ := st.producer.LatestBlock()
	if n != m.Blocks || tip.Hash != m.TipHash || st.v2.StateApplier.StateRoot() != m.TipRoot {
		t.Fatalf("replay ended at %d blocks, tip %s root %s; want %d %s %s", n, tip.Hash, st.v2.StateApplier.StateRoot(), m.Blocks, m.TipHash, m.TipRoot)
	}
	out := map[string]any{"slowest_append_ms": float64(slowest.Microseconds()) / 1000}
	if tenth > 0 {
		out["last_tenth_mean_append_ms"] = float64(lastTenth.Microseconds()) / 1000 / float64(tenth)
	}
	return out
}
