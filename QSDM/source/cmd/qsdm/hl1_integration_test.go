package main

// Integration tests of the cmd/qsdm HL1 wiring with the real collaborators:
// chain.BlockProducer, the block driver, the persistence hook, the canary
// Guard, SQLite Store and Ledger, and the mining service.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/blockdriver"
	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/internal/miningsvc"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	hmacattest "github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

func hl1BaseMiningConfig() miningsvc.Config {
	return miningsvc.Config{
		WorkSet:        bringUpWorkSet(),
		DAGSize:        1024,
		Difficulty:     new(big.Int).Set(mining.DefaultMinDifficulty),
		BlocksPerEpoch: mining.DefaultBlocksPerMiningEpoch,
	}
}

// Stage A (and a canary whose Store did not open) serves a read-only mining
// service: loopback GET /work and POST /submit get 503 although the chain has
// a durable tip.
func TestHL1LoopbackWorkUnavailableWhenNotWritable(t *testing.T) {
	bp, _ := hl1ProducerWithChain(t, 4)
	durable := &hl1DurableTip{}
	durable.Store(3)
	view := &durableChainView{tip: durable, producer: bp}
	parts := &hl1CanaryParts{
		store:  legacymining.NewSQLiteStore(),
		guard:  &legacymining.CanaryGuard{},
		ledger: &legacymining.PayoutLedger{},
	}
	for name, tc := range map[string]struct {
		parts     *hl1CanaryParts
		storeOpen bool
	}{
		"Stage A":                {&hl1CanaryParts{reason: "off"}, false},
		"canary, store not open": {parts, false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := hl1MiningServiceConfig(hl1BaseMiningConfig(), view, tc.parts, tc.storeOpen)
			if !cfg.ReadOnly || cfg.Store != nil || cfg.Guard != nil || cfg.Sink != nil || cfg.RewardSink != nil {
				t.Fatalf("config not read-only: %+v", cfg)
			}
			if cfg.Producer != view {
				t.Fatal("the mining service does not use the durable chain view")
			}
			svc, err := miningsvc.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.WorkAt(4); !errors.Is(err, api.ErrMiningUnavailable) {
				t.Fatalf("WorkAt err = %v, want ErrMiningUnavailable (503)", err)
			}
			if _, err := svc.Submit([]byte(`{}`)); !errors.Is(err, api.ErrMiningUnavailable) {
				t.Fatalf("Submit err = %v, want ErrMiningUnavailable (503)", err)
			}
		})
	}
	cfg := hl1MiningServiceConfig(hl1BaseMiningConfig(), view, parts, true)
	if cfg.ReadOnly || cfg.Store == nil || cfg.Guard != legacymining.Guard(parts.guard) || cfg.Sink != legacymining.Sink(parts.ledger) {
		t.Fatalf("canary with an open store is not writable: %+v", cfg)
	}
}

// -----------------------------------------------------------------------------
// Lock watchdog: submit, tick, wallet admission and the hook together
// -----------------------------------------------------------------------------

type hl1FakeClock struct{ ns atomic.Int64 }

func (c *hl1FakeClock) Now() time.Time          { return time.Unix(0, c.ns.Load()) }
func (c *hl1FakeClock) Advance(d time.Duration) { c.ns.Add(int64(d)) }

func hl1WaitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func hl1RandBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

// hl1PrecheckProof is a proof that passes Guard.Precheck (allowlisted miner
// and node, HMAC attestation, parseable bundle) but fails Verify.
func hl1PrecheckProof(t *testing.T, miner, node string, height uint64) []byte {
	t.Helper()
	bundle, err := json.Marshal(hmacattest.Bundle{
		ChallengeBind: "x", ChallengeSig: "x", ChallengeSignerID: "x", ComputeCap: "8.6",
		CUDAVersion: "12.0", DriverVer: "550", GPUName: "RTX", GPUUUID: "GPU-1", HMAC: "00",
		IssuedAt: time.Now().Unix(), NodeID: node, Nonce: hex.EncodeToString(hl1RandBytes(32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	p := mining.Proof{Version: 1, Height: height, MinerAddr: miner, BatchCount: 1,
		Attestation: mining.Attestation{Type: mining.AttestationTypeHMAC, BundleBase64: base64.StdEncoding.EncodeToString(bundle), GPUArch: "ada"}}
	copy(p.HeaderHash[:], hl1RandBytes(32))
	copy(p.BatchRoot[:], hl1RandBytes(32))
	copy(p.Nonce[:], hl1RandBytes(16))
	copy(p.MixDigest[:], hl1RandBytes(32))
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func hl1GoroutineDump() string {
	buf := make([]byte, 1<<20)
	return string(buf[:runtime.Stack(buf, true)])
}

func TestHL1LockWatchdogSubmitTickWalletAdd(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	dir := t.TempDir()
	legacy := filepath.Join(dir, legacymining.LegacyDirName)
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	miner, node := strings.Repeat("ab", 32), "node-1"
	clock := &hl1FakeClock{}
	clock.ns.Store(time.Now().UnixNano())

	accounts := chain.NewAccountStore()
	pool := mempool.New(mempool.DefaultConfig())
	producer := chain.NewBlockProducer(pool, accounts, chain.DefaultProducerConfig())
	receipts := chain.NewReceiptStore()
	producer.SetAppendReceiptStore(receipts)

	var failStops atomic.Int64
	var failCauses sync.Map
	fsFn := func(code int, cause string) {
		failStops.Add(1)
		failCauses.Store(cause, code)
	}
	cfg := legacymining.Config{
		Version: 1, Allowed: []legacymining.AllowEntry{{MinerAddr: miner, NodeID: node}},
		MaxProofsPerMin: 1 << 30, MaxProofsTotal: 1 << 40, MaxPending: 600, BudgetCell: 1 << 40,
		ExpiresUnix: clock.Now().Add(24 * time.Hour).Unix(),
	}
	hash := legacymining.ConfigHash(sha256.Sum256([]byte("canary-config")))
	store := legacymining.NewSQLiteStore()
	guard, err := legacymining.NewGuard(legacymining.GuardOptions{
		Dir: legacy, Config: cfg, ConfigHash: hash, Release: "test", Store: store,
		FailStop: fsFn, EnrollmentActive: func(string, string) bool { return true },
		Now: clock.Now, Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := legacymining.NewLedger(legacymining.LedgerConfig{Store: store, Guard: guard, Accounts: accounts})
	if err != nil {
		t.Fatal(err)
	}
	var localSeal atomic.Bool
	driver, err := blockdriver.New(blockdriver.Config{
		Producer: producer, Pool: pool, Accounts: accounts, Logger: logging.NewSilentLogger(),
		FailStop: fsFn, Ledger: ledger, Guard: guard, LocalSeal: &localSeal, Period: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Genesis, sealed before the hook exists (as a restored chain would be).
	funder := chain.MiningRewardFunderAddress
	if err := pool.Add(&mempool.Tx{ID: "solo-heartbeat-genesis", Sender: funder, Recipient: funder, Nonce: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := producer.ProduceBlock(); err != nil {
		t.Fatal(err)
	}
	driver.SyncFunderNonce()

	// S7-S14 on the restored chain.
	parts := &hl1CanaryParts{store: store, guard: guard, ledger: ledger}
	rep := hl1ReconcileCanary(parts, hl1BootConfig{Env: legacymining.Env{Mode: legacymining.ModeCanary, DBPath: filepath.Join(legacy, legacymining.DBFile)}}, producer.AllBlocks(), accounts)
	if !rep.Clean || !rep.StoreOpen {
		t.Fatalf("reconcile: %+v", rep)
	}
	defer store.Close()

	// The hook, with real files.
	durable := &hl1DurableTip{}
	durable.Store(0)
	journal, err := chain.OpenChainJournal(filepath.Join(dir, "qsdm_chain.ndjson"), producer.AllBlocks()[0])
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	metricsSet := &hl1MetricSet{}
	hook := &hl1PersistHook{
		ProducerRole: true, StateDir: dir,
		JournalPath:  filepath.Join(dir, "qsdm_chain.ndjson"),
		AccountsPath: filepath.Join(dir, "qsdm_accounts.json"),
		ReceiptsPath: filepath.Join(dir, "qsdm_receipts.ndjson"),
		FS:           hl1OSFS{}, AppendJournal: journal.Append, SaveAccounts: accounts.Save,
		AppendReceipts: receipts.AppendBlockNDJSON, LocalSeal: &localSeal,
		Canary: true, Ledger: ledger, Guard: guard, Durable: durable,
		FailStop: fsFn, Log: logging.NewSilentLogger(), Metrics: metricsSet,
	}
	producer.OnSealedBlock = hook.OnSealedBlock

	svc, err := miningsvc.New(hl1MiningServiceConfig(hl1BaseMiningConfig(), &durableChainView{tip: durable, producer: producer}, parts, true))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	driver.Start(ctx)
	defer driver.Stop()
	guard.Activate(true)
	// Walk the fake clock past the quiet period in steps shorter than
	// NoSealTimeout, letting local durable seals land between steps.
	for i := 0; i < 5; i++ {
		start, _ := durable.Load()
		hl1WaitFor(t, "local durable seals", 30*time.Second, func() bool { h, _ := durable.Load(); return h >= start+2 })
		clock.Advance(30 * time.Second)
	}
	hl1WaitFor(t, "a seal after the quiet period", 30*time.Second, func() bool {
		h, _ := durable.Load()
		return h >= 12 && guard.AdmissionOpen()
	})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var submits, rejected400, adds, enqueued atomic.Int64 // rejected400: Verify 400s
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tip, _ := durable.Load()
				_, err := svc.Submit(hl1PrecheckProof(t, miner, node, tip))
				submits.Add(1)
				// A 400 from Verify (not a Guard Rejection) means the
				// submit passed Admit, Precheck and TakeRate and took
				// submitMu and ledger.mu.
				var re *mining.RejectError
				if errors.As(err, &re) && legacymining.RejectKindOf(err) == 0 {
					rejected400.Add(1)
				}
			}
		}()
	}
	wallet := producer.WalletTransferSubmitter()
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}
				_ = wallet.Add(&mempool.Tx{ID: fmt.Sprintf("wt-%d-%d", i, n), Sender: fmt.Sprintf("unfunded-%d", i),
					Recipient: "r", Amount: 1, ContractID: chain.WalletTransferContractID})
				adds.Add(1)
				time.Sleep(2 * time.Millisecond)
			}
		}(i)
	}
	// Accepted proofs: commit and enqueue as Submit steps 8-9 do, so the
	// driver pays them through PreSeal, the hook's H8 and MarkPaid.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 150; n++ {
			select {
			case <-stop:
				return
			default:
			}
			tip, _ := durable.Load()
			rec := legacymining.Record{MinerAddr: miner, NodeID: node, WorkHeight: tip, AcceptTip: tip,
				AcceptedNS: time.Now().UnixNano(), ConfigSHA256: hash, ProofJSON: []byte(`{"n":` + fmt.Sprint(n) + `}`)}
			copy(rec.ProofID[:], hl1RandBytes(32))
			copy(rec.AttNonce[:], hl1RandBytes(32))
			if err := store.Accept(rec); err != nil {
				t.Errorf("accept: %v", err)
				return
			}
			if err := ledger.Enqueue(rec); err != nil {
				t.Errorf("enqueue: %v", err)
				return
			}
			enqueued.Add(1)
			time.Sleep(5 * time.Millisecond)
		}
	}()
	// Readers of the clamped surfaces.
	wg.Add(1)
	go func() {
		defer wg.Done()
		probe := blocksProbeFromProducer(producer, durable)
		for {
			select {
			case <-stop:
				return
			default:
			}
			headers := probe.HeadersInRange(0, probe.Tip()+5)
			// The durable tip only grows, so every served height must be
			// at or below its value after the call.
			tip, _ := durable.Load()
			for _, h := range headers {
				if h.Height > tip {
					t.Errorf("header %d served above the durable tip %d", h.Height, tip)
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()

	time.Sleep(1500 * time.Millisecond)
	close(stop)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatalf("deadlock watchdog: workers did not stop within 60s\n%s", hl1GoroutineDump())
	}
	hl1WaitFor(t, "every enqueued proof to be paid", 60*time.Second, func() bool { return ledger.Outstanding() == 0 })

	stopped := make(chan struct{})
	go func() { driver.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(60 * time.Second):
		t.Fatalf("deadlock watchdog: driver did not stop within 60s\n%s", hl1GoroutineDump())
	}

	if n := failStops.Load(); n != 0 {
		var causes []string
		failCauses.Range(func(k, _ any) bool { causes = append(causes, k.(string)); return true })
		t.Fatalf("failStop called %d time(s): %q", n, causes)
	}
	if st := guard.State(); st != legacymining.StateOpen {
		cause, _ := os.ReadFile(filepath.Join(legacy, legacymining.TrippedCauseFile))
		t.Fatalf("guard state %s after the run: %s", st, cause)
	}
	if submits.Load() == 0 || rejected400.Load() == 0 || adds.Load() == 0 || enqueued.Load() == 0 {
		t.Fatalf("load did not run: submits=%d rejected400=%d adds=%d enqueued=%d", submits.Load(), rejected400.Load(), adds.Load(), enqueued.Load())
	}
	if metricsSet.unexpected.Load() != 0 {
		t.Fatalf("hl1_unexpected_tx_family_total = %d", metricsSet.unexpected.Load())
	}
	tip, _ := durable.Load()
	w, err := hl1ReadWatermark(dir)
	if err != nil || w.Height != tip || tip != producer.TipHeight() {
		t.Fatalf("W=%+v (%v), durable tip %d, producer tip %d", w, err, tip, producer.TipHeight())
	}
	var paid int
	for _, b := range producer.AllBlocks() {
		for _, tx := range b.Transactions {
			if tx.ContractID == chain.MiningRewardContractID {
				ids, err := legacymining.DecodePayload(tx.Payload)
				if err != nil {
					t.Fatalf("reward payload: %v", err)
				}
				paid += len(ids)
			}
		}
	}
	if int64(paid) != enqueued.Load() {
		t.Fatalf("chain pays %d proof IDs, enqueued %d", paid, enqueued.Load())
	}
	t.Logf("blocks=%d submits=%d (400s=%d) wallet adds=%d paid=%d hook max=%s",
		tip, submits.Load(), rejected400.Load(), adds.Load(), paid, time.Duration(metricsSet.hookMaxNS.Load()))
}
