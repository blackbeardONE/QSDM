//go:build pulsee2e

package blockdriver

// PULSE end to end, with the real pieces: this package's block driver on the
// real chain.BlockProducer, AccountStore and PULSE state (the node's
// EnrollmentAwareApplier), the node's real /api/v1/pulse handlers, the real
// pool server (qsdm-mining-bootstrap cmd/pulse-pool) and the real public miner
// (qsdmminer-console in pool mode) as two members with real ML-DSA-87 wallets.
//
// What stands in: accepted network proofs come from this package's fake proof
// ledger (proof verification is tested elsewhere), and /status and
// /mining/work are thin adapters (the real routes need the whole node; the
// work set is synthetic).
//
// Run (both binaries built first):
//
//	PULSE_POOL_BIN=.../pulse-pool.exe QSDM_MINER_BIN=.../qsdmminer-console.exe \
//	go test -tags "validator_only,dilithium_circl,pulsee2e" ./internal/blockdriver -run TestPulseEndToEnd -v -timeout 20m

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/pulse"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/keystore"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

const (
	e2eActivation  = 3
	e2eReclaim     = 15
	e2eReward      = 10.0
	e2eFloorDust   = 600_000_000 // 6 CELL: at least one member's share is carried
	e2eDustPerCell = 100_000_000
)

type e2eWallet struct {
	seed [mldsa87.SeedSize]byte
	key  *pulse.KeyPair
	addr string
}

func newE2EWallet(t *testing.T) *e2eWallet {
	t.Helper()
	var seed [mldsa87.SeedSize]byte
	if _, err := rand.Read(seed[:]); err != nil {
		t.Fatal(err)
	}
	k := pulse.KeyPairFromSeed(seed)
	return &e2eWallet{seed: seed, key: k, addr: string(k.ID())}
}

// keystore writes the wallet as a QSDM keystore with a passphrase file.
func (w *e2eWallet) keystore(t *testing.T, dir string) (string, string) {
	t.Helper()
	pk, sk := mldsa87.NewKeyFromSeed(&w.seed)
	priv, err := sk.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	ks, err := keystore.Encrypt(pk.Bytes(), priv, []byte("e2e-pass"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := keystore.Marshal(ks)
	if err != nil {
		t.Fatal(err)
	}
	kp, pp := filepath.Join(dir, "wallet.json"), filepath.Join(dir, "pass.txt")
	if err := os.WriteFile(kp, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pp, []byte("e2e-pass"), 0o600); err != nil {
		t.Fatal(err)
	}
	return kp, pp
}

// envelope signs a PULSE action from this wallet as the node takes it.
func (w *e2eWallet) envelope(t *testing.T, nonce uint64, action any) []byte {
	t.Helper()
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	tx := &mempool.Tx{Sender: w.addr, Nonce: nonce, Payload: payload}
	sig, err := w.key.Sign("", chain.PulseTxSigningBytes(tx))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(api.QSDMPulseEnvelope{Sender: w.addr, Nonce: nonce, Payload: payload,
		PublicKey: hex.EncodeToString(w.key.Pub.Bytes()), Signature: hex.EncodeToString(sig)})
	return b
}

type accountProbe struct{ accounts *chain.AccountStore }

func (p accountProbe) BalanceOf(address string) (float64, uint64, bool) {
	a, ok := p.accounts.Get(address)
	if !ok || a == nil {
		return 0, 0, false
	}
	return a.Balance, a.Nonce, true
}

func e2eWork(t *testing.T, height uint64) []byte {
	t.Helper()
	ws := mining.WorkSet{Batches: make([]mining.Batch, 4)}
	for i := range ws.Batches {
		cells := make([]mining.ParentCellRef, 3)
		for j := range cells {
			var ch [32]byte
			ch[0], ch[1] = byte(i), byte(j)
			cells[j] = mining.ParentCellRef{ID: []byte{byte(i), byte(j), 0xAB}, ContentHash: ch}
		}
		ws.Batches[i] = mining.Batch{Cells: cells}
	}
	ws.Canonicalize()
	batches := make([]api.MiningWorkBatch, len(ws.Batches))
	for i, b := range ws.Batches {
		cs := make([]api.MiningWorkCell, len(b.Cells))
		for j, c := range b.Cells {
			cs[j] = api.MiningWorkCell{ID: hex.EncodeToString(c.ID), ContentHash: hex.EncodeToString(c.ContentHash[:])}
		}
		batches[i] = api.MiningWorkBatch{Cells: cs}
	}
	root := ws.Root()
	hdr := [32]byte{0x50, 0x55, 0x4C, 0x53, 0x45}
	b, err := json.Marshal(api.MiningWork{Epoch: 1, Height: height, HeaderHash: hex.EncodeToString(hdr[:]),
		Difficulty: "1000000000000", DAGSize: 128, WorkSetRoot: hex.EncodeToString(root[:]), WorkSet: batches,
		BatchCountMaximum: 4, BlocksPerEpoch: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitFor(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func getJSON(url string, out any) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %d %s", url, resp.StatusCode, b)
	}
	return json.Unmarshal(b, out)
}

func post(url string, body []byte) (int, string) {
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return 0, err.Error()
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, strings.TrimSpace(string(b))
}

type e2eEscrow struct {
	ProofID string `json:"proof_id"`
	Owner   string `json:"owner"`
	Dust    uint64 `json:"dust"`
	Height  uint64 `json:"height"`
}

type e2eState struct {
	Active bool   `json:"active"`
	Tip    uint64 `json:"tip"`
	Pool   *struct {
		LastSeq      uint64            `json:"last_seq"`
		Escrowed     []e2eEscrow       `json:"escrowed"`
		EscrowedDust uint64            `json:"escrowed_dust"`
		Owed         map[string]uint64 `json:"owed"`
		AccountNonce uint64            `json:"account_nonce"`
	} `json:"pool"`
}

func TestPulseEndToEnd(t *testing.T) {
	poolBin, minerBin := os.Getenv("PULSE_POOL_BIN"), os.Getenv("QSDM_MINER_BIN")
	if poolBin == "" || minerBin == "" {
		t.Skip("set PULSE_POOL_BIN and QSDM_MINER_BIN")
	}
	dir := t.TempDir()
	poolW, alice, bob, feeTo := newE2EWallet(t), newE2EWallet(t), newE2EWallet(t), newE2EWallet(t)

	// 1. The chain rule, as every node would load it.
	if err := chain.SetPulseParams(chain.PulseParams{ActivationHeight: e2eActivation, MaxFeeBPS: 300,
		Pools:             []pulse.PoolEntry{{ID: poolW.key.ID(), PublicKey: hex.EncodeToString(poolW.key.Pub.Bytes())}},
		PayeeEpochCapDust: 1 << 60, ReclaimAfterBlocks: e2eReclaim}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = chain.SetPulseParams(chain.PulseParams{}) })

	// 2. A real producer: driver, BlockProducer, accounts and PULSE state.
	mp := mempool.New(mempool.DefaultConfig())
	accounts := chain.NewAccountStore()
	ps := chain.NewPulseStateStore()
	enroll := enrollment.NewInMemoryState() // the node always wires enrollment state
	aware := chain.NewEnrollmentAwareApplier(accounts, chain.NewEnrollmentApplier(accounts, enroll))
	aware.SetPulseStateStore(ps)
	bp := chain.NewBlockProducer(mp, aware, chain.DefaultProducerConfig())
	aware.SetHeightFn(func() uint64 { return bp.TipHeight() + 1 }) // the node's canonical wiring
	ledger := newFakeLedger(newFakeGuard())
	var tip atomic.Uint64
	bp.OnSealedBlock = func(blk *chain.Block) {
		tip.Store(blk.Height)
		if err := ledger.OnDurableBlock(blk, true); err != nil {
			t.Errorf("OnDurableBlock: %v", err)
		}
	}
	h := &harness{failStop: &failStopRecorder{}, localSeal: new(atomic.Bool)}
	h.localSeal.Store(true)
	d, err := New(Config{Producer: bp, Pool: mp, Accounts: accounts, Logger: quietLogger(t), FailStop: h.failStop.fn,
		LocalSeal: h.localSeal, Period: 5 * time.Millisecond, FlatRewardPerBlock: e2eReward,
		FunderInitialBalance: 1_000_000, Ledger: ledger, Guard: ledger.guard,
		PulsePayee: func(owner string, height uint64) string {
			if !chain.PulseActiveAt(height) {
				return owner
			}
			if pool, ok := ps.MemberPool(owner); ok && !enroll.OwesDeferredBond(owner) {
				return chain.PulseEscrowAddress(pool, owner)
			}
			return owner
		}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var tickMu sync.Mutex
	go func() {
		tk := time.NewTicker(250 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				tickMu.Lock()
				d.tick()
				tickMu.Unlock()
			}
		}
	}()

	// 3. The node's HTTP API: the real PULSE handlers, plus /status and /mining/work adapters.
	api.SetPulseMempool(mp)
	api.SetPulseStateProvider(ps, func() uint64 { return tip.Load() })
	api.SetMiningAccountProbe(accountProbe{accounts})
	hd := &api.Handlers{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/pulse/submit-signed", hd.QSDMPulseSubmitSignedHandler)
	mux.HandleFunc("/api/v1/pulse/state", hd.QSDMPulseStateHandler)
	mux.HandleFunc("/api/v1/pulse/member", hd.QSDMPulseMemberHandler)
	mux.HandleFunc("/api/v1/status", func(w http.ResponseWriter, r *http.Request) {
		n := tip.Load()
		_ = json.NewEncoder(w).Encode(map[string]any{"chain_tip": n, "pulse_activation_height": e2eActivation,
			"pulse_active": chain.PulseActiveAt(n + 1)})
	})
	mux.HandleFunc("/api/v1/mining/work", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(e2eWork(t, tip.Load()+1))
	})
	mux.HandleFunc("/api/v1/mining/submit", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"accepted":false,"reject_reason":"e2e: network proofs come from the proof ledger"}`))
	})
	node := httptest.NewServer(mux)
	defer node.Close()
	apiBase := node.URL + "/api/v1"

	waitFor(t, "the activation height", 30*time.Second, func() bool { return tip.Load() >= e2eActivation })

	// 4. Members join on the chain (signed txs through the real handler).
	for _, m := range []*e2eWallet{alice, bob} {
		code, body := post(apiBase+"/pulse/submit-signed", m.envelope(t, 0, map[string]string{"op": "join", "pool": poolW.addr}))
		if code != 200 {
			t.Fatalf("join %s: %d %s", m.addr[:8], code, body)
		}
	}
	for _, m := range []*e2eWallet{alice, bob} {
		m := m
		waitFor(t, "membership", 30*time.Second, func() bool {
			var out struct{ Member bool }
			return getJSON(apiBase+"/pulse/member?owner="+m.addr, &out) == nil && out.Member
		})
	}
	t.Log("both members joined on the chain")

	// 5. The real pool server.
	poolPort := freePort(t)
	keyFile := filepath.Join(dir, "pool.key")
	if err := os.WriteFile(keyFile, []byte(hex.EncodeToString(poolW.seed[:])+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	poolCfg, _ := json.Marshal(map[string]any{"listen": fmt.Sprintf("127.0.0.1:%d", poolPort), "producer_api": apiBase,
		"data_dir": filepath.Join(dir, "pool-data"), "key_file": keyFile, "fee_bps": 100, "fee_to": feeTo.addr,
		"window_factor": 8, "floor_dust": e2eFloorDust, "share_difficulty": 16, "min_share_difficulty": 1,
		"preview_accept_shares": false, "poll_seconds": 1})
	cfgPath := filepath.Join(dir, "pool.json")
	if err := os.WriteFile(cfgPath, poolCfg, 0o600); err != nil {
		t.Fatal(err)
	}
	poolLog := &bytes.Buffer{}
	poolCmd := exec.Command(poolBin, "serve", "--config", cfgPath)
	poolCmd.Stdout, poolCmd.Stderr = poolLog, poolLog
	if err := poolCmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopPool := func() {
		if poolCmd.Process != nil {
			_ = poolCmd.Process.Kill()
			_, _ = poolCmd.Process.Wait()
		}
	}
	defer stopPool()
	poolAPI := fmt.Sprintf("http://127.0.0.1:%d/pool/api", poolPort)
	waitFor(t, "the pool", 30*time.Second, func() bool {
		var info map[string]any
		return getJSON(poolAPI+"/v1/info", &info) == nil && info["mode"] == "active"
	})
	t.Log("pool is up and active")

	// 6. The real miner, in pool mode, for each member.
	var miners []*exec.Cmd
	minerLogs := map[string]*bytes.Buffer{}
	for i, m := range []*e2eWallet{alice, bob} {
		md := filepath.Join(dir, fmt.Sprintf("miner%d", i))
		_ = os.MkdirAll(md, 0o700)
		kp, pp := m.keystore(t, md)
		toml := fmt.Sprintf("validator_url = %q\nreward_address = %q\nbatch_count = 1\npoll_interval = \"200ms\"\nplain = true\n",
			node.URL, m.addr)
		cfg := filepath.Join(md, "miner.toml")
		_ = os.WriteFile(cfg, []byte(toml), 0o600)
		lb := &bytes.Buffer{}
		minerLogs[m.addr[:8]] = lb
		c := exec.Command(minerBin, "--config="+cfg, "--plain", "--compute-backend=cpu", "--allow-v1",
			"--pool-url="+poolAPI, fmt.Sprintf("--pool-worker=rig%d", i), "--pool-receipts-file="+filepath.Join(md, "receipts.jsonl"),
			"--operator-keystore="+kp, "--operator-passphrase-file="+pp)
		c.Stdout, c.Stderr = lb, lb
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		miners = append(miners, c)
	}
	defer func() {
		for _, c := range miners {
			_ = c.Process.Kill()
			_, _ = c.Process.Wait()
		}
	}()
	dumpLogs := func() {
		t.Logf("pool log:\n%s", tail(poolLog.String(), 40))
		for k, v := range minerLogs {
			t.Logf("miner %s log:\n%s", k, tail(v.String(), 25))
		}
	}
	waitFor(t, "shares from both members", 3*time.Minute, func() bool {
		var win struct {
			Weights []struct {
				Owner string `json:"Owner"`
			} `json:"weights"`
		}
		if getJSON(poolAPI+"/v1/window", &win) != nil {
			return false
		}
		seen := map[string]bool{}
		for _, w := range win.Weights {
			seen[strings.ToLower(w.Owner)] = true
		}
		return seen[alice.addr] && seen[bob.addr]
	})
	t.Log("the pool counts shares from both members")

	// 7. Network proofs for both members: their rewards go to escrow; the pool settles them.
	balance := func(a string) uint64 {
		acc, ok := accounts.Get(a)
		if !ok || acc == nil {
			return 0
		}
		return uint64(math.Round(acc.Balance * e2eDustPerCell))
	}
	before := map[string]uint64{alice.addr: balance(alice.addr), bob.addr: balance(bob.addr), feeTo.addr: balance(feeTo.addr)}
	ledger.add(t, alice.addr, 2)
	ledger.add(t, bob.addr, 1)
	var st e2eState
	func() {
		defer func() {
			if !t.Failed() {
				return
			}
			h.failStop.mu.Lock()
			t.Logf("fail-stops: %+v", h.failStop.calls)
			h.failStop.mu.Unlock()
			t.Logf("ledger outstanding %d, tip %d, state %+v", ledger.Outstanding(), tip.Load(), st.Pool)
			if blk, ok := bp.LatestBlock(); ok {
				for _, tx := range blk.Transactions {
					t.Logf("block %d tx %s %s -> %s %.8f", blk.Height, tx.ContractID, tx.Sender[:8], tx.Recipient, tx.Amount)
				}
			}
		}()
		waitFor(t, "escrowed rewards", 30*time.Second, func() bool {
			return loadState(apiBase+"/pulse/state?pool="+poolW.addr, &st) == nil && st.Pool != nil && len(st.Pool.Escrowed) >= 3
		})
	}()
	escrowed := st.Pool.EscrowedDust
	if balance(alice.addr) != before[alice.addr] || balance(bob.addr) != before[bob.addr] {
		t.Fatal("a pooled reward reached a member directly instead of the escrow")
	}
	t.Logf("escrowed %d dust for 3 proofs", escrowed)
	func() {
		defer func() {
			if t.Failed() {
				dumpLogs()
			}
		}()
		waitFor(t, "the pool's settlement on the chain", 2*time.Minute, func() bool {
			return loadState(apiBase+"/pulse/state?pool="+poolW.addr, &st) == nil && st.Pool != nil &&
				st.Pool.LastSeq >= 1 && len(st.Pool.Escrowed) == 0
		})
	}()
	owed := st.Pool.Owed
	delta := func(a string) uint64 { return balance(a) - before[a] + owed[a] }
	da, db, df := delta(alice.addr), delta(bob.addr), balance(feeTo.addr)-before[feeTo.addr]
	if da+db+df != escrowed {
		t.Fatalf("not conserved: alice %d + bob %d + fee %d != escrowed %d", da, db, df, escrowed)
	}
	wantFee := escrowed / 100
	if df < wantFee || df > wantFee+uint64(2) {
		t.Fatalf("fee %d dust, want 1%% of %d (%d) plus rounding dust", df, escrowed, wantFee)
	}
	if da == 0 || db == 0 {
		t.Fatalf("a member got nothing: alice %d bob %d", da, db)
	}
	carried := 0
	for _, a := range []string{alice.addr, bob.addr} {
		if owed[a] > 0 {
			carried++
			if balance(a) != before[a] {
				t.Fatalf("%s was both paid and carried", a[:8])
			}
		}
	}
	if carried == 0 {
		t.Fatalf("with a 6 CELL floor and %d dust pooled, someone's share should be carried (owed %v)", escrowed, owed)
	}
	t.Logf("settled: alice %d, bob %d, fee %d dust (1%% + dust); carried %v", da, db, df, owed)

	// 7b. A second round: carried balances plus new rewards pass the floor and are paid.
	seq1 := st.Pool.LastSeq
	before2 := map[string]uint64{alice.addr: balance(alice.addr), bob.addr: balance(bob.addr), feeTo.addr: balance(feeTo.addr)}
	owed1 := map[string]uint64{alice.addr: owed[alice.addr], bob.addr: owed[bob.addr]}
	ledger.add(t, alice.addr, 2)
	ledger.add(t, bob.addr, 1)
	waitFor(t, "the second escrow", 30*time.Second, func() bool {
		return loadState(apiBase+"/pulse/state?pool="+poolW.addr, &st) == nil && len(st.Pool.Escrowed) >= 3
	})
	escrowed2 := st.Pool.EscrowedDust
	waitFor(t, "the second settlement", 2*time.Minute, func() bool {
		return loadState(apiBase+"/pulse/state?pool="+poolW.addr, &st) == nil && st.Pool.LastSeq > seq1 && len(st.Pool.Escrowed) == 0
	})
	paid := 0
	var in2 uint64
	for _, a := range []string{alice.addr, bob.addr} {
		got := balance(a) - before2[a]
		if got > 0 {
			paid++
			if got < e2eFloorDust {
				t.Fatalf("%s was paid %d dust, below the floor", a[:8], got)
			}
		}
		in2 += got + st.Pool.Owed[a] - owed1[a]
	}
	fee2 := balance(feeTo.addr) - before2[feeTo.addr]
	for _, a := range []string{alice.addr, bob.addr} {
		t.Logf("round 2 %s: paid %d, owed before %d, owed now %d", a[:8], balance(a)-before2[a], owed1[a], st.Pool.Owed[a])
	}
	carryAcct, _ := accounts.Get(chain.PulseEscrowAddress(poolW.addr, poolW.addr))
	t.Logf("round 2: escrowed %d, fee %d, carry account %+v", escrowed2, fee2, carryAcct)
	if in2+fee2 != escrowed2 {
		t.Fatalf("second round not conserved: members %d + fee %d != escrowed %d", in2, fee2, escrowed2)
	}
	if paid == 0 {
		t.Fatalf("no member was paid in the second round (owed %v)", st.Pool.Owed)
	}
	t.Logf("second round: %d member(s) paid above the floor, owed now %v", paid, st.Pool.Owed)

	// 8. The network refuses a settlement above the 3% cap, even signed by the pool.
	stopPool()
	_ = loadState(apiBase+"/pulse/state?pool="+poolW.addr, &st)
	seqBefore := st.Pool.LastSeq
	ledger.add(t, alice.addr, 1)
	waitFor(t, "a new escrowed reward", 30*time.Second, func() bool {
		return loadState(apiBase+"/pulse/state?pool="+poolW.addr, &st) == nil && len(st.Pool.Escrowed) == 1
	})
	esc := st.Pool.Escrowed[0]
	over := &pulse.Batch{Version: 1, PoolID: poolW.key.ID(), Seq: seqBefore + 1, Height: tip.Load(), FeeBPS: 400,
		FeeTo: pulse.Address(feeTo.addr), FloorDust: 1, ProofIDs: []string{esc.ProofID},
		Weights: []pulse.Weight{{Owner: pulse.Address(alice.addr), Weight: 1}}}
	sb, err := pulse.SignBatch(poolW.key, over)
	if err != nil {
		t.Fatal(err)
	}
	code, body := post(apiBase+"/pulse/submit-signed", poolW.envelope(t, st.Pool.AccountNonce,
		map[string]string{"op": "settle", "batch": hex.EncodeToString(sb.Body), "batch_sig": hex.EncodeToString(sb.Sig)}))
	t.Logf("over-cap settlement: HTTP %d %s", code, body)
	target := tip.Load() + 4
	waitFor(t, "four more blocks", 30*time.Second, func() bool { return tip.Load() >= target })
	_ = loadState(apiBase+"/pulse/state?pool="+poolW.addr, &st)
	if st.Pool.LastSeq != seqBefore || len(st.Pool.Escrowed) != 1 {
		t.Fatalf("a 4%% settlement was applied: last_seq %d (was %d), escrowed %d", st.Pool.LastSeq, seqBefore, len(st.Pool.Escrowed))
	}

	// 9. Leave, a too-early reclaim refused, then the reclaim after the timeout.
	aliceBefore := balance(alice.addr)
	if code, body := post(apiBase+"/pulse/submit-signed", alice.envelope(t, 1, map[string]string{"op": "leave"})); code != 200 {
		t.Fatalf("leave: %d %s", code, body)
	}
	waitFor(t, "alice left", 30*time.Second, func() bool {
		var out struct{ Member bool }
		return getJSON(apiBase+"/pulse/member?owner="+alice.addr, &out) == nil && !out.Member
	})
	reclaim := alice.envelope(t, 2, map[string]any{"op": "reclaim", "proofs": []string{esc.ProofID}})
	if tip.Load()+1-esc.Height < e2eReclaim {
		code, body := post(apiBase+"/pulse/submit-signed", reclaim)
		t.Logf("early reclaim: HTTP %d %s", code, body)
		target := tip.Load() + 3
		waitFor(t, "three more blocks", 30*time.Second, func() bool { return tip.Load() >= target })
		if balance(alice.addr) != aliceBefore {
			t.Fatal("a reclaim before the timeout paid out")
		}
	}
	waitFor(t, "the reclaim timeout", 60*time.Second, func() bool { return tip.Load()+1-esc.Height >= e2eReclaim+1 })
	acc, _ := accounts.Get(alice.addr)
	if code, body := post(apiBase+"/pulse/submit-signed", alice.envelope(t, acc.Nonce, map[string]any{"op": "reclaim", "proofs": []string{esc.ProofID}})); code != 200 {
		t.Fatalf("reclaim: %d %s", code, body)
	}
	waitFor(t, "the reclaim", 30*time.Second, func() bool { return balance(alice.addr) == aliceBefore+esc.Dust })
	_ = loadState(apiBase+"/pulse/state?pool="+poolW.addr, &st)
	if len(st.Pool.Escrowed) != 0 {
		t.Fatal("the reclaimed reward is still in escrow")
	}
	t.Logf("alice reclaimed %d dust after %d blocks", esc.Dust, e2eReclaim)
	h.failStop.mu.Lock()
	stops := len(h.failStop.calls)
	h.failStop.mu.Unlock()
	if stops != 0 {
		t.Fatalf("the driver fail-stopped %d times", stops)
	}
}

// loadState reads the node's PULSE state into a fresh value: json.Unmarshal
// merges into an existing map, which would keep owners the chain no longer owes.
func loadState(url string, st *e2eState) error {
	*st = e2eState{}
	return getJSON(url, st)
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
