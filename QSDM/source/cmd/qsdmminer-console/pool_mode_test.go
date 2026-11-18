package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining/v2client"
	"github.com/blackbeardONE/QSDM/pkg/pulseclient"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

func testSigner(t *testing.T) *operatorProofSigner {
	t.Helper()
	pk, sk, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &operatorProofSigner{Address: pulseclient.AddressOf(pk.Bytes()), PublicKeyHex: hex.EncodeToString(pk.Bytes()), privateKey: sk}
}

// fakePool is a stand-in PULSE pool: it checks the login signature like the
// real pool, hands out the given work, counts shares and signs receipts with
// its own ML-DSA-87 key.
type fakePool struct {
	t        *testing.T
	pub      []byte
	sk       *mldsa87.PrivateKey
	work     api.MiningWork
	shareDif string
	forge    bool // sign receipts with another key

	mu     sync.Mutex
	logins []string
	shares int
	proofs []string
	down   atomic.Bool
}

func newFakePool(t *testing.T, work api.MiningWork, shareDiff string) *fakePool {
	pk, sk, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &fakePool{t: t, pub: pk.Bytes(), sk: sk, work: work, shareDif: shareDiff}
}

func (f *fakePool) handler() http.Handler {
	mux := http.NewServeMux()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/pool/api/v1/info", func(w http.ResponseWriter, r *http.Request) {
		write(w, map[string]any{"pool_id": pulseclient.AddressOf(f.pub), "public_key": hex.EncodeToString(f.pub), "mode": "active"})
	})
	mux.HandleFunc("/pool/api/v1/register", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Login, Password, PublicKey, Signature string }
		_ = json.NewDecoder(r.Body).Decode(&struct {
			Login     *string `json:"login"`
			PublicKey *string `json:"public_key"`
			Signature *string `json:"signature"`
		}{&in.Login, &in.PublicKey, &in.Signature})
		pub, _ := hex.DecodeString(in.PublicKey)
		sig, _ := hex.DecodeString(in.Signature)
		addr, _, _ := strings.Cut(in.Login, ".")
		var buf [mldsa87.PublicKeySize]byte
		copy(buf[:], pub)
		pk := new(mldsa87.PublicKey)
		pk.Unpack(&buf)
		if len(pub) != mldsa87.PublicKeySize || pulseclient.AddressOf(pub) != addr ||
			!mldsa87.Verify(pk, append([]byte(pulseclient.RegisterDomain), in.Login...), nil, sig) {
			http.Error(w, `{"error":"bad login signature"}`, http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		f.logins = append(f.logins, in.Login)
		f.mu.Unlock()
		write(w, map[string]string{"session": "s-" + in.Login})
	})
	mux.HandleFunc("/pool/api/v1/work", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer s-") {
			http.Error(w, "no session", http.StatusUnauthorized)
			return
		}
		raw, _ := json.Marshal(f.work)
		write(w, map[string]any{"job_id": "job-1", "share_difficulty": f.shareDif, "network_difficulty": f.work.Difficulty,
			"window_target": 1, "work": json.RawMessage(raw)})
	})
	mux.HandleFunc("/pool/api/v1/shares", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			JobID      string `json:"job_id"`
			Nonce      string `json:"nonce"`
			BatchCount uint32 `json:"batch_count"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		login := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer s-")
		addr, worker, _ := strings.Cut(login, ".")
		f.mu.Lock()
		f.shares++
		idx := uint64(f.shares)
		f.mu.Unlock()
		rc := pulseclient.Receipt{Pool: pulseclient.AddressOf(f.pub), Index: idx, Owner: addr, Worker: worker, JobID: in.JobID,
			Nonce: in.Nonce, BatchCount: in.BatchCount, Difficulty: 2, AtUnixMs: time.Now().UnixMilli()}
		b, _ := json.Marshal(rc)
		sk := f.sk
		if f.forge {
			_, sk, _ = mldsa87.GenerateKey(rand.Reader)
		}
		sig := make([]byte, mldsa87.SignatureSize)
		_ = mldsa87.SignTo(sk, append([]byte(pulseclient.ReceiptDomain), b...), nil, true, sig)
		write(w, map[string]any{"accepted": true, "difficulty": 2, "receipt": rc, "receipt_sig": hex.EncodeToString(sig)})
	})
	mux.HandleFunc("/pool/api/v1/proofs", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ProofID string `json:"proof_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.proofs = append(f.proofs, in.ProofID)
		f.mu.Unlock()
		write(w, map[string]bool{"recorded": true})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.down.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// The whole member path: log in with a wallet signature, mine shares at the
// pool's target, keep each verified receipt, submit a share that also meets
// the network target to the validator, and report the accepted proof.
func TestPoolMode_SharesReceiptsAndNetworkProof(t *testing.T) {
	work := buildFixtureWork(t, 1, 1)
	work.Difficulty = "8" // network: about 1 share in 4 is also a network solution
	pool := newFakePool(t, work, "2")
	psrv := httptest.NewServer(pool.handler())
	defer psrv.Close()

	var submits atomic.Int32
	vmux := http.NewServeMux()
	vmux.HandleFunc("/api/v1/mining/submit", func(w http.ResponseWriter, r *http.Request) {
		n := submits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{Accepted: true, ProofID: "proof-" + string(rune('a'+n))})
	})
	vsrv := httptest.NewServer(vmux)
	defer vsrv.Close()

	signer := testSigner(t)
	receipts := filepath.Join(t.TempDir(), "receipts.jsonl")
	cfg := Config{ValidatorURL: vsrv.URL, RewardAddr: signer.Address, BatchCount: 1, PollInterval: "20ms",
		PoolURL: psrv.URL + "/pool/api", PoolWorker: "rig1", PoolReceiptsFile: receipts}
	client := &http.Client{Timeout: 5 * time.Second}
	pm, err := newPoolMiner(cfg, signer, client)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan Event, 1024)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fetcher, _ := v2client.NewMultiFetcher(client, []string{cfg.ValidatorURL})
		runLoopPool(ctx, client, fetcher, cfg, &V2Context{}, nil, nil, events, new(uint64), pm)
	}()
	go func() { // drain
		for range events {
		}
	}()
	deadline := time.Now().Add(55 * time.Second)
	for time.Now().Before(deadline) {
		pool.mu.Lock()
		s, p := pool.shares, len(pool.proofs)
		pool.mu.Unlock()
		// The pool counts a share before the miner has written its receipt; wait for the receipts on disk too, or a
		// slow runner can stop the miner between the two (CI on #240: "5 receipts kept for 6 shares").
		if s >= 6 && p >= 1 && countLines(receipts) >= 6 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-done

	pool.mu.Lock()
	defer pool.mu.Unlock()
	if len(pool.logins) == 0 || pool.logins[0] != signer.Address+".rig1" {
		t.Fatalf("logins %v", pool.logins)
	}
	if pool.shares < 6 || len(pool.proofs) < 1 {
		t.Fatalf("shares %d, reported proofs %d", pool.shares, len(pool.proofs))
	}
	if int(submits.Load()) >= pool.shares {
		t.Fatalf("every share went to the network (%d submits, %d shares): the network target was not checked", submits.Load(), pool.shares)
	}
	if len(pool.proofs) > int(submits.Load()) {
		t.Fatalf("%d proofs reported but only %d submitted", len(pool.proofs), submits.Load())
	}
	f, err := os.Open(receipts)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<10)
	for sc.Scan() {
		var line struct {
			Key     string              `json:"pool_public_key"`
			Receipt pulseclient.Receipt `json:"receipt"`
			Sig     string              `json:"sig"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if !pulseclient.VerifyReceipt(line.Key, line.Receipt, line.Sig) || line.Receipt.Owner != signer.Address {
			t.Fatalf("kept receipt %d does not verify", n)
		}
		n++
	}
	if n < 6 {
		t.Fatalf("%d receipts kept for %d shares", n, pool.shares)
	}
}

// The pool is down: after the fallback time the miner mines solo, tries the
// pool again once a minute, and goes back to it when it answers.
func TestPoolMode_FallbackToSoloAndBack(t *testing.T) {
	pool := newFakePool(t, buildFixtureWork(t, 1, 1), "2")
	psrv := httptest.NewServer(pool.handler())
	defer psrv.Close()
	pool.down.Store(true)

	now := time.Unix(1_800_000_000, 0)
	pm, err := newPoolMiner(Config{PoolURL: psrv.URL + "/pool/api", PoolFallbackMinutes: 5}, testSigner(t), &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pm.now = func() time.Time { return now }
	ctx := context.Background()

	if j, err := pm.Job(ctx); err == nil || j != nil {
		t.Fatalf("down pool: job %v err %v, want an error before the fallback time", j, err)
	}
	now = now.Add(4 * time.Minute)
	if _, err := pm.Job(ctx); err == nil || pm.Solo() {
		t.Fatal("went solo before 5 minutes")
	}
	now = now.Add(time.Minute)
	if j, err := pm.Job(ctx); err != nil || j != nil || !pm.Solo() {
		t.Fatalf("after 5 minutes: job %v err %v solo %v, want solo", j, err, pm.Solo())
	}
	pool.down.Store(false)
	now = now.Add(30 * time.Second)
	if j, _ := pm.Job(ctx); j != nil || !pm.Solo() {
		t.Fatal("tried the pool again before a minute had passed")
	}
	now = now.Add(31 * time.Second)
	j, err := pm.Job(ctx)
	if err != nil || j == nil || pm.Solo() {
		t.Fatalf("pool back: job %v err %v solo %v", j, err, pm.Solo())
	}
}

// A dead primary: the backup endpoint serves the work.
func TestPoolMode_BackupEndpoint(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer dead.Close()
	pool := newFakePool(t, buildFixtureWork(t, 1, 1), "2")
	psrv := httptest.NewServer(pool.handler())
	defer psrv.Close()
	pm, err := newPoolMiner(Config{PoolURL: dead.URL + "/pool/api", PoolBackupURL: psrv.URL + "/pool/api"}, testSigner(t), &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	j, err := pm.Job(context.Background())
	if err != nil || j == nil || j.ID != "job-1" {
		t.Fatalf("backup: job %v err %v", j, err)
	}
}

// A receipt not signed by the pool's published key is reported, not kept.
func TestPoolMode_ForgedReceiptIsRefused(t *testing.T) {
	pool := newFakePool(t, buildFixtureWork(t, 1, 1), "2")
	pool.forge = true
	psrv := httptest.NewServer(pool.handler())
	defer psrv.Close()
	receipts := filepath.Join(t.TempDir(), "r.jsonl")
	pm, err := newPoolMiner(Config{PoolURL: psrv.URL + "/pool/api", PoolReceiptsFile: receipts}, testSigner(t), &http.Client{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pm.Job(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := pm.Share(context.Background(), "job-1", [16]byte{1}, 1); err == nil {
		t.Fatal("a forged receipt was accepted")
	}
	if _, err := os.Stat(receipts); !os.IsNotExist(err) {
		t.Fatal("a forged receipt was kept")
	}
}

// Pool mode refuses to start without the wallet key.
func TestPoolMode_NeedsTheWalletKey(t *testing.T) {
	if _, err := newPoolMiner(Config{PoolURL: "https://qsdm.tech/pool/api"}, nil, &http.Client{}); err == nil {
		t.Fatal("pool mode started without a wallet key")
	}
	if pm, err := newPoolMiner(Config{}, nil, &http.Client{}); pm != nil || err != nil {
		t.Fatal("solo mode (no pool_url) must not build a pool miner")
	}
}

// countLines returns the number of lines in a file, 0 when it does not exist yet.
func countLines(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return bytes.Count(b, []byte("\n"))
}
