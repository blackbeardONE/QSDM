package main

// PULSE pool mode (qsdm-mining-bootstrap PROTOCOL.md). With pool_url set the
// miner takes its work from the pool, sends every share that meets the pool's
// share target, and still submits a share that also meets the network target
// to the producer as a normal, attested, operator-signed proof (the pool
// cannot submit it: it needs this GPU's attestation and this wallet's
// signature). The chain then pays the reward into this member's escrow and the
// pool's settlement spreads it by share window.
//
// Fallback: if no pool URL (primary, then backup) answers for
// pool_fallback_minutes, the miner mines solo against the validator, so no
// mining time is lost, and tries the pool again every minute.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/pulseclient"
)

// DefaultPoolFallbackMinutes is how long the pool may be unreachable before
// the miner mines solo (recommended in the PULSE design doc: long enough to
// ride out a pool restart, short enough that a real outage costs little).
const DefaultPoolFallbackMinutes = 5

// poolRetryEvery is how often a miner in solo fallback tries the pool again.
const poolRetryEvery = time.Minute

// poolMiner keeps the pool session and the fallback state.
type poolMiner struct {
	urls        []string // primary first, then backup
	cur         int
	worker      string
	fallbackFor time.Duration
	signer      *operatorProofSigner
	receipts    string // file receipts are appended to ("" = none)
	now         func() time.Time

	mu        sync.Mutex
	client    *pulseclient.Pool
	poolKey   string // the pool's public key (hex), from /info, to check receipts
	failSince time.Time
	solo      bool
	lastTry   time.Time
}

func newPoolMiner(cfg Config, signer *operatorProofSigner, hc *http.Client) (*poolMiner, error) {
	if cfg.PoolURL == "" {
		return nil, nil
	}
	if signer == nil || signer.privateKey == nil {
		return nil, errors.New("pool mode needs the wallet key (operator keystore and passphrase file): " +
			"the pool login and the network proofs are signed with it")
	}
	if _, err := pulseclient.Login(signer.Address, cfg.PoolWorker); err != nil {
		return nil, err
	}
	urls := []string{cfg.PoolURL}
	if cfg.PoolBackupURL != "" && cfg.PoolBackupURL != cfg.PoolURL {
		urls = append(urls, cfg.PoolBackupURL)
	}
	mins := cfg.PoolFallbackMinutes
	if mins <= 0 {
		mins = DefaultPoolFallbackMinutes
	}
	pm := &poolMiner{urls: urls, worker: cfg.PoolWorker, fallbackFor: time.Duration(mins) * time.Minute,
		signer: signer, receipts: cfg.PoolReceiptsFile, now: time.Now}
	pm.client = &pulseclient.Pool{Base: urls[0], HTTP: hc}
	return pm, nil
}

// poolJob is one unit of pool work, decoded.
type poolJob struct {
	ID          string
	Work        *api.MiningWork
	ShareTarget *big.Int
}

// Job returns pool work, or nil (and no error) when the miner should mine solo
// this round. It registers on the first call and after a pool restart.
func (pm *poolMiner) Job(ctx context.Context) (*poolJob, error) {
	pm.mu.Lock()
	solo, lastTry := pm.solo, pm.lastTry
	pm.mu.Unlock()
	if solo && pm.now().Sub(lastTry) < poolRetryEvery {
		return nil, nil
	}
	for attempt := 0; attempt < len(pm.urls); attempt++ {
		job, err := pm.tryJob(ctx)
		if err == nil {
			pm.mu.Lock()
			pm.failSince, pm.solo = time.Time{}, false
			pm.mu.Unlock()
			return job, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !pulseclient.Unreachable(err) {
			return nil, err // the pool answered: a real refusal, not an outage
		}
		pm.rotate()
	}
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.lastTry = pm.now()
	if pm.failSince.IsZero() {
		pm.failSince = pm.now()
	}
	if pm.now().Sub(pm.failSince) >= pm.fallbackFor {
		pm.solo = true
		return nil, nil
	}
	return nil, fmt.Errorf("pool unreachable since %s (solo after %s)", pm.failSince.Format(time.RFC3339), pm.fallbackFor)
}

// Solo reports whether the miner is in solo fallback.
func (pm *poolMiner) Solo() bool {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	return pm.solo
}

func (pm *poolMiner) rotate() {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.cur = (pm.cur + 1) % len(pm.urls)
	pm.client = &pulseclient.Pool{Base: pm.urls[pm.cur], HTTP: pm.client.HTTP}
	pm.poolKey = ""
}

func (pm *poolMiner) tryJob(ctx context.Context) (*poolJob, error) {
	pm.mu.Lock()
	c := pm.client
	needLogin := c.Token == ""
	pm.mu.Unlock()
	if needLogin {
		if err := pm.register(ctx, c); err != nil {
			return nil, err
		}
	}
	j, err := c.Work(ctx)
	var ae *pulseclient.PoolAPIError
	if errors.As(err, &ae) && ae.Status == 401 { // the pool restarted: log in again
		if err := pm.register(ctx, c); err != nil {
			return nil, err
		}
		j, err = c.Work(ctx)
	}
	if err != nil {
		return nil, err
	}
	var work api.MiningWork
	if err := json.Unmarshal(j.Work, &work); err != nil {
		return nil, fmt.Errorf("decode pool work: %w", err)
	}
	d, ok := new(big.Int).SetString(j.ShareDifficulty, 10)
	if !ok || d.Sign() <= 0 {
		return nil, fmt.Errorf("pool share difficulty %q", j.ShareDifficulty)
	}
	t, err := mining.TargetFromDifficulty(d)
	if err != nil {
		return nil, err
	}
	return &poolJob{ID: j.JobID, Work: &work, ShareTarget: t}, nil
}

func (pm *poolMiner) register(ctx context.Context, c *pulseclient.Pool) error {
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	login, err := pulseclient.Login(pm.signer.Address, pm.worker)
	if err != nil {
		return err
	}
	pub, err := hex.DecodeString(pm.signer.PublicKeyHex)
	if err != nil {
		return err
	}
	sig, err := pulseclient.SignRegister(pm.signer.privateKey, pub, login)
	if err != nil {
		return err
	}
	if err := c.Register(ctx, login, "", pm.signer.PublicKeyHex, sig); err != nil {
		return err
	}
	pm.mu.Lock()
	pm.poolKey = info.PublicKey
	pm.mu.Unlock()
	return nil
}

// Share sends one share and keeps its signed receipt. It returns the pool's
// answer; a receipt that does not verify against the pool's key is reported
// as an error (the share may not have been counted).
func (pm *poolMiner) Share(ctx context.Context, jobID string, nonce [16]byte, batchCount uint32) (*pulseclient.ShareResult, error) {
	pm.mu.Lock()
	c, key := pm.client, pm.poolKey
	pm.mu.Unlock()
	res, err := c.Share(ctx, jobID, hex.EncodeToString(nonce[:]), batchCount)
	if err != nil {
		return nil, err
	}
	if res.Accepted && res.Receipt != nil {
		if !pulseclient.VerifyReceipt(key, *res.Receipt, res.ReceiptSig) {
			return res, errors.New("the pool's share receipt does not verify against its published key")
		}
		pm.keepReceipt(*res.Receipt, res.ReceiptSig)
	}
	return res, nil
}

// ReportProof tells the pool about a network proof the producer accepted
// (optional for settlement, which reads the chain's escrow; kept for the
// pool's statistics).
func (pm *poolMiner) ReportProof(ctx context.Context, proofID string) error {
	pm.mu.Lock()
	c := pm.client
	pm.mu.Unlock()
	return c.ReportProof(ctx, proofID)
}

// keepReceipt appends a receipt to the receipts file (JSON lines, 0o600), so
// the member can later prove the share was counted.
func (pm *poolMiner) keepReceipt(r pulseclient.Receipt, sig string) {
	if pm.receipts == "" {
		return
	}
	line, _ := json.Marshal(map[string]any{"pool_public_key": pm.poolKey, "receipt": r, "sig": sig})
	f, err := os.OpenFile(pm.receipts, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// poolSummary is a one-line status for the event log.
func (pm *poolMiner) poolSummary() string {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if pm.solo {
		return "pool unreachable: mining solo, retrying every minute"
	}
	return "pool " + pm.urls[pm.cur] + " (" + strconv.Itoa(len(pm.urls)) + " endpoint(s))"
}

// job is Job for a possibly nil miner (solo mode).
func (pm *poolMiner) job(ctx context.Context) (*poolJob, error) {
	if pm == nil {
		return nil, nil
	}
	return pm.Job(ctx)
}
