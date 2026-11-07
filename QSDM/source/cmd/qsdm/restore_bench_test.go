package main

// Memory and time harness for the restore a node runs at every start, and for
// the checks of qsdm --snapshot-import (QSDM/docs/docs/RUN_A_FULL_NODE.md,
// "What you need"). It is skipped unless QSDM_RESTORE_BENCH names a phase. It
// reads an existing state directory, such as an unpacked snapshot or a copy
// of a stopped node's state directory, and never writes to it:
//
//	QSDM_RESTORE_BENCH=boot|import
//	QSDM_RESTORE_BENCH_DIR=<state dir>     (required: qsdm_chain.ndjson,
//	                                        qsdm_accounts.json, qsdm_enrollment.json;
//	                                        import also reads qsdm_staking.json)
//	QSDM_RESTORE_BENCH_CONFIG=<node .toml> (the node's configuration, loaded as
//	                                        main() loads it; its [consensus] table
//	                                        is what matters. Unset: the defaults)
//	QSDM_RESTORE_BENCH_HEAPPROFILE=<file>  (boot, optional: a heap profile taken
//	                                        after the restore and a GC)
//	QSDM_RESTORE_BENCH_REMARSHAL=1         (boot, optional: after the measurement,
//	                                        re-encode every restored block as the
//	                                        node relays it (RelayBlocks, which reads
//	                                        stripped signatures back from the
//	                                        journal), compare it with its journal
//	                                        line and hash the re-encoded journal,
//	                                        to compare builds)
//
// boot    main()'s restore block: the consensus settings, the transition
//         prefix check, twice as main() runs it (before the state lock and
//         under it; prefix_seconds), the journal load (with a producer
//         transition, chain.LoadVerifiedJournal: every block's hash and
//         signature verified once, and the verified signatures stripped from
//         memory; load_seconds includes that verification), the snapshot
//         pair's state root check and the producer hydrate
//         (hl1ReplayStack.restore, which does not verify the blocks again),
//         then the receipts load when the directory has a
//         qsdm_receipts.ndjson (receipts_seconds; a producer's holds a
//         receipt for every transaction of the chain, a snapshot none).
// import  the checks of qsdm --snapshot-import (snapshotVerifyDir), without
//         the staging copy and the checkpoint pin: the journal read once,
//         every block checked as it is read (chain.CheckJournal: each hash
//         computed and, with a producer transition, each signature verified
//         once, every signature but the tip's stripped; load_seconds
//         includes that verification), then the snapshot pair's state root
//         check and the producer hydrate (restore_seconds). stripped_blocks
//         counts the blocks it held without their signature.
//
// Build the test binary once, then run each phase in its own process so that
// the peak resident memory belongs to that phase alone:
//
//	go test -c -o qsdm.test -tags dilithium_circl ./cmd/qsdm
//	export QSDM_RESTORE_BENCH_DIR=/srv/qsdm-snapshot/qsdm-snapshot-<M>
//	export QSDM_RESTORE_BENCH_CONFIG=/etc/qsdm-follower/core.toml
//	for phase in boot import; do
//	  QSDM_RESTORE_BENCH=$phase ./qsdm.test -test.run '^TestRestoreBench$' -test.count=1 -test.timeout=6h
//	done
//
// Each phase prints one line: RESTORE_BENCH {json}. The memory figures are
// MiB: heap_* and sys_mb come from runtime.MemStats after the restore and a
// runtime.GC() (boot only), peak_heap_inuse_mb is sampled every 100 ms,
// peak_rss_mb is the process's peak resident memory (VmHWM on Linux, the peak
// working set on Windows; -1 elsewhere) and peak_commit_mb its peak private
// commit (Windows only, where a trimmed working set can hide the peak).

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

type restoreBenchReport struct {
	Phase              string  `json:"phase"`
	Blocks             int     `json:"blocks"`
	SignedBlocks       int     `json:"signed_blocks,omitempty"`
	StrippedBlocks     int     `json:"stripped_blocks,omitempty"`
	Transactions       int     `json:"transactions,omitempty"`
	ProducerKeys       int     `json:"producer_keys,omitempty"`
	ProducerKeyArrays  int     `json:"producer_key_arrays,omitempty"`
	Receipts           int     `json:"receipts,omitempty"`
	JournalBytes       int64   `json:"journal_bytes"`
	Transition         bool    `json:"producer_transition"`
	PrefixSeconds      float64 `json:"prefix_seconds,omitempty"`
	LoadSeconds        float64 `json:"load_seconds"`
	RestoreSeconds     float64 `json:"restore_seconds"`
	ReceiptsSeconds    float64 `json:"receipts_seconds,omitempty"`
	Seconds            float64 `json:"seconds"`
	HeapInuseMB        float64 `json:"heap_inuse_mb,omitempty"`
	HeapAllocMB        float64 `json:"heap_alloc_mb,omitempty"`
	SysMB              float64 `json:"sys_mb,omitempty"`
	PeakHeapInuseMB    float64 `json:"peak_heap_inuse_mb"`
	PeakRSSMB          float64 `json:"peak_rss_mb"`
	PeakCommitMB       float64 `json:"peak_commit_mb,omitempty"`
	GOMAXPROCS         int     `json:"gomaxprocs"`
	GOGC               string  `json:"gogc,omitempty"`
	GOMEMLIMIT         string  `json:"gomemlimit,omitempty"`
	RemarshalMismatch  *int    `json:"remarshal_mismatched_lines,omitempty"`
	RemarshalLines     int     `json:"remarshal_lines,omitempty"`
	RemarshalSHA256    string  `json:"remarshal_sha256,omitempty"`
	RemarshalSeconds   float64 `json:"remarshal_seconds,omitempty"`
	HeapProfileWritten bool    `json:"heap_profile_written,omitempty"`
}

// restoreBenchConsensus is the consensus configuration of the measured node.
type restoreBenchConsensus struct {
	settings   func() // hl1ReplayConsensusSettings(cfg); nil: the defaults
	transition *producerpolicy.Transition
	authorized []string
	governance []string
}

func restoreBenchLoadConsensus(t *testing.T) restoreBenchConsensus {
	t.Helper()
	path := os.Getenv("QSDM_RESTORE_BENCH_CONFIG")
	if path == "" {
		return restoreBenchConsensus{}
	}
	t.Setenv("CONFIG_FILE", path)
	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("QSDM_RESTORE_BENCH_CONFIG=%s: %v", path, err)
	}
	return restoreBenchConsensus{
		settings:   func() { hl1ReplayConsensusSettings(cfg) },
		transition: cfg.ProducerTransition,
		authorized: cfg.AuthorizedBlockProducers,
		governance: cfg.GovernanceAuthorities,
	}
}

func restoreBenchMB(b uint64) float64 { return float64(b) / (1 << 20) }

func TestRestoreBench(t *testing.T) {
	phase := os.Getenv("QSDM_RESTORE_BENCH")
	if phase == "" {
		t.Skip("set QSDM_RESTORE_BENCH=boot|import and QSDM_RESTORE_BENCH_DIR (see restore_bench_test.go)")
	}
	dir := os.Getenv("QSDM_RESTORE_BENCH_DIR")
	if dir == "" {
		t.Fatal("QSDM_RESTORE_BENCH_DIR is required")
	}
	journal := filepath.Join(dir, hl1JournalName)
	fi, err := os.Stat(journal)
	if err != nil {
		t.Fatal(err)
	}
	cons := restoreBenchLoadConsensus(t)
	scratch := t.TempDir()
	rep := restoreBenchReport{
		Phase: phase, JournalBytes: fi.Size(), Transition: cons.transition != nil,
		GOMAXPROCS: runtime.GOMAXPROCS(0), GOGC: os.Getenv("GOGC"), GOMEMLIMIT: os.Getenv("GOMEMLIMIT"),
	}
	runtime.GC()
	sampler := snapBenchStartSampler()
	start := time.Now()
	switch phase {
	case "boot":
		st, blocks := restoreBenchBoot(t, dir, scratch, cons, &rep)
		rep.Seconds = time.Since(start).Seconds()
		rep.PeakHeapInuseMB = sampler.finish()
		rep.PeakRSSMB, rep.PeakCommitMB = benchPeakMemMB()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		rep.HeapInuseMB, rep.HeapAllocMB, rep.SysMB = restoreBenchMB(ms.HeapInuse), restoreBenchMB(ms.HeapAlloc), restoreBenchMB(ms.Sys)
		if path := os.Getenv("QSDM_RESTORE_BENCH_HEAPPROFILE"); path != "" {
			restoreBenchHeapProfile(t, path)
			rep.HeapProfileWritten = true
		}
		restoreBenchCount(blocks, &rep)
		if os.Getenv("QSDM_RESTORE_BENCH_REMARSHAL") == "1" {
			t0 := time.Now()
			n, bad, sum := restoreBenchRemarshal(t, journal, st.producer, blocks)
			rep.RemarshalLines, rep.RemarshalMismatch, rep.RemarshalSHA256 = n, &bad, sum
			rep.RemarshalSeconds = time.Since(t0).Seconds()
		}
		runtime.KeepAlive(st)
	case "import":
		c := snapshotConsensus{
			Transition: cons.transition, AuthorizedBlockProducers: cons.authorized,
			GovernanceAuthorities: cons.governance, ConsensusSettings: cons.settings, Log: logging.NewSilentLogger(),
		}
		res, err := snapshotVerifyDir(dir, scratch, c, nil)
		if err != nil {
			t.Fatal(err)
		}
		rep.Seconds = time.Since(start).Seconds()
		rep.PeakHeapInuseMB = sampler.finish()
		rep.PeakRSSMB, rep.PeakCommitMB = benchPeakMemMB()
		rep.Blocks, rep.StrippedBlocks = res.Blocks, res.StrippedBlocks
		rep.LoadSeconds, rep.RestoreSeconds = res.LoadDuration.Seconds(), res.RestoreDuration.Seconds()
	default:
		sampler.finish()
		t.Fatalf("unknown phase %q", phase)
	}
	data, _ := json.Marshal(rep)
	fmt.Printf("RESTORE_BENCH %s\n", data)
}

// restoreBenchBoot is main()'s restore block for a node whose state directory
// is dir (hl1rRestore with the node's consensus configuration). The replay
// stack's own files go to scratch, so dir is only read.
func restoreBenchBoot(t *testing.T, dir, scratch string, c restoreBenchConsensus, rep *restoreBenchReport) (*hl1ReplayStack, []*chain.Block) {
	t.Helper()
	if c.settings != nil {
		c.settings()
	}
	journal := filepath.Join(dir, hl1JournalName)
	// main() checks the prefix before it takes the state lock, then again
	// under it (hl1LockedTransitionPreflight).
	tp := time.Now()
	for _, step := range []string{"preflight", "locked preflight"} {
		if err := c.transition.ValidateJournalPrefix(journal); err != nil {
			t.Fatalf("producer transition %s: %v", step, err)
		}
	}
	rep.PrefixSeconds = time.Since(tp).Seconds()
	if err := hl1RequireFinalNewline(hl1OSFS{}, journal); err != nil {
		t.Fatal(err)
	}
	st, err := hl1NewReplayStack(scratch, c.governance, c.authorized, c.transition, logging.NewSilentLogger())
	if err != nil {
		t.Fatal(err)
	}
	// As main() loads the journal: with a producer transition, every block
	// verified once and its signature stripped.
	t0 := time.Now()
	var blocks []*chain.Block
	var verified *chain.VerifiedChain
	if c.transition != nil {
		if verified, err = st.producer.LoadVerifiedJournal(journal); err != nil {
			t.Fatalf("producer transition persisted chain: %v", err)
		}
		blocks = verified.Blocks()
	} else if blocks, err = chain.LoadChainNDJSON(journal); err != nil {
		t.Fatal(err)
	}
	rep.LoadSeconds = time.Since(t0).Seconds()
	if len(blocks) == 0 {
		t.Fatal("empty journal")
	}
	t1 := time.Now()
	if _, dropped := canonicalPersistedChain(blocks); dropped != 0 {
		t.Fatalf("journal contains %d forked duplicate block(s)", dropped)
	}
	if err := st.restore(blocks, verified, filepath.Join(dir, hl1AccountsName), filepath.Join(dir, hl1EnrollmentName),
		filepath.Join(scratch, hl1ReceiptsName)); err != nil {
		t.Fatalf("restore: %v", err)
	}
	rep.RestoreSeconds = time.Since(t1).Seconds()
	if tip, ok := st.producer.LatestBlock(); !ok || tip != blocks[len(blocks)-1] {
		t.Fatal("the restored tip is not the journal tip")
	}
	// The receipts, as main() loads them after the restore (read only).
	if receipts := filepath.Join(dir, hl1ReceiptsName); restoreBenchFileExists(receipts) {
		t2 := time.Now()
		if err := hl1RequireFinalNewline(hl1OSFS{}, receipts); err != nil {
			t.Fatal(err)
		}
		n, err := st.receipts.LoadNDJSON(receipts)
		if err != nil {
			t.Fatalf("receipts: %v", err)
		}
		rep.Receipts, rep.ReceiptsSeconds = n, time.Since(t2).Seconds()
	}
	return st, blocks
}

func restoreBenchFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func restoreBenchHeapProfile(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.WriteHeapProfile(f); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// restoreBenchCount counts the restored blocks, signed blocks, blocks whose
// signature was stripped and transactions, and the distinct producer public
// keys: by value and by backing array.
func restoreBenchCount(blocks []*chain.Block, rep *restoreBenchReport) {
	keys := map[string]struct{}{}
	arrays := map[*byte]struct{}{}
	rep.Blocks = len(blocks)
	for _, b := range blocks {
		rep.Transactions += len(b.Transactions)
		if b.ProducerAuth.Signed() {
			rep.SignedBlocks++
		}
		if b.ProducerAuth.Stripped() {
			rep.StrippedBlocks++
		}
		if pk := b.ProducerAuth.PublicKey; len(pk) > 0 {
			keys[string(pk)] = struct{}{}
			arrays[&pk[0]] = struct{}{}
		}
	}
	rep.ProducerKeys, rep.ProducerKeyArrays = len(keys), len(arrays)
}

// restoreBenchRemarshal re-encodes every restored block as the node relays it
// (producer.RelayBlocks, in pages of api.ChainBlocksMaxLimit blocks, as GET
// /api/v1/chain/blocks reads them: a block whose signature was stripped is
// read back from the journal) and compares it with its journal line. It
// returns the number of lines compared, how many differ (a block written
// before a field existed re-encodes with it) and the SHA-256 of the
// re-encoded journal (each block as json.Marshal encodes it, then a newline):
// a build that strips signatures must give the same figures as one that
// keeps them.
func restoreBenchRemarshal(t *testing.T, journal string, producer *chain.BlockProducer, blocks []*chain.Block) (int, int, string) {
	t.Helper()
	f, err := os.Open(journal)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // LoadChainNDJSON's limits
	n, bad := 0, 0
	sum := sha256.New()
	var page []*chain.Block
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue // LoadChainNDJSON skips empty lines
		}
		if n >= len(blocks) {
			t.Fatalf("the journal holds more blocks than were restored (%d)", len(blocks))
		}
		if len(page) == 0 {
			h := blocks[n].Height
			if page, err = producer.RelayBlocks(h, h+api.ChainBlocksMaxLimit-1, 0); err != nil {
				t.Fatalf("relay from height %d: %v", h, err)
			}
			if len(page) == 0 {
				t.Fatalf("nothing relayed at height %d", h)
			}
		}
		relayed := page[0]
		page = page[1:]
		if relayed.Height != blocks[n].Height || relayed.Hash != blocks[n].Hash {
			t.Fatalf("relayed block %d (%s) for the restored block %d (%s)", relayed.Height, relayed.Hash, blocks[n].Height, blocks[n].Hash)
		}
		data, err := json.Marshal(relayed)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, line) {
			bad++
		}
		sum.Write(data)
		sum.Write([]byte{'\n'})
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n != len(blocks) {
		t.Fatalf("compared %d journal lines with %d restored blocks", n, len(blocks))
	}
	return n, bad, hex.EncodeToString(sum.Sum(nil))
}
