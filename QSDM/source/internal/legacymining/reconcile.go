package legacymining

// Startup reconciliation (§5 S7-S14, WP8). Reconcile runs once per canary
// boot: after the restore block and S5, and before SyncFunderNonce and the
// driver start (S15). It opens legacy-mining.db, or creates it at S8, checks
// the DB against the restored chain, records the chain's LMP1 payments in the
// DB (the chain is the only record of payment, W3), derives the counters of
// the active config window, initialises the Ledger and pre-arms the guard
// markers. Section references are to the HL1 design rev 4.
//
// Every anomaly and every failure trips FREEZE and never exits. The process
// keeps sealing heartbeats, and admission never opens in this process,
// because ReconcileReport.Clean is false and is passed to Guard.Activate
// (S16). After an S7-S10 failure, S11-S13 do not run: the DB keeps its
// evidence for the operator and the Ledger stays uninitialised.

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// reconcileMaxAnomalies bounds ReconcileReport.Anomalies. AnomalyCount still
// counts every finding.
const reconcileMaxAnomalies = 32

// ReconcileConfig holds the inputs of Reconcile.
type ReconcileConfig struct {
	// Store is the proof store. It must not be open yet: Reconcile opens it
	// at DBPath (S7) and creates it there at S8.
	Store Store
	// DBPath is Env.DBPath.
	DBPath string
	// Guard is the canary guard, constructed before the Store is opened.
	// Reconcile trips it (Freeze) on every anomaly or failure, feeds it the
	// derived totals (S12) and pre-arms its markers (S14).
	Guard Guard
	// Ledger is the payout ledger. It must not be initialised yet;
	// Reconcile calls Init at S13.
	Ledger Ledger
	// Accounts is the restored account store. S13 reads the funder's nonce
	// and balance from it.
	Accounts AccountReader
	// Blocks is the restored canonical chain, the slice given to
	// RestoreChain: the blocks at heights 0..tip, in order. Reconcile only
	// reads it and does not retain it. It must not be read through a
	// ChainView (see doc.go).
	Blocks []*chain.Block
	// Release is the binary's version string. S8 writes it into Meta.
	Release string
	// RewardCell is the float-CELL block reward schedule, the same function
	// as LedgerConfig.RewardCell. nil means DefaultRewardCell.
	RewardCell func(height uint64) float64
	// Now is the clock for Meta.CreatedNS (S8), ConfigWindow.ActivatedNS
	// (S11) and the expiry check (S12). nil means time.Now.
	Now func() time.Time
}

// ReconcileReport is the outcome of Reconcile.
type ReconcileReport struct {
	// Clean is true when S7-S14 all completed without an anomaly or a
	// failure. It is the argument of Guard.Activate at S16.
	Clean bool
	// Step is the first step that failed ("S7".."S14"), or "" when Clean.
	Step string
	// StoreOpen reports that the Store was left open (S7 or S8 succeeded).
	StoreOpen bool
	// Created reports that S8 created the DB, with Meta.H0 = Tip+1.
	Created bool
	// Tip is the height of the last restored block.
	Tip uint64
	// Meta is the DB's meta row, read at S9.
	Meta Meta
	// Paid is the number of LMP1 payments found in [Meta.H0, Tip] (S10).
	Paid int
	// Counts is what S11 changed in the DB.
	Counts ReconcileCounts
	// Window is the active config window after S11.
	Window ConfigWindow
	// Totals is the S12 counter derivation for the active config window.
	Totals Totals
	// Pending is the number of records loaded into the Ledger (S13).
	Pending int
	// Anomalies lists the S9 and S10 findings, at most
	// reconcileMaxAnomalies of them. AnomalyCount is their total number.
	Anomalies    []string
	AnomalyCount int
}

// Reconcile runs S7-S14 of §5 in canary mode. The caller then runs
// SyncFunderNonce, starts the driver (S15) and calls
// Guard.Activate(report.Clean) (S16).
//
//   - S7: Store.Open(DBPath, nil). A failure other than ErrDBMissing trips
//     CauseDBOpen.
//   - S8: if the DB is missing and any restored block carries an LMP1
//     payload, it trips CauseDBMissing and creates nothing. Otherwise it
//     creates the DB with H0 = tip+1, the genesis hash (block 0) and the
//     funder; a create failure trips CauseDBOpen.
//   - S9: the meta row must match the chain: genesis_hash, funder, and
//     H0 <= tip+1.
//   - S10: in [H0, tip], every reward-contract tx must carry a valid LMP1
//     payload, have the funder as Sender and the derived RewardIDFormat ID,
//     a positive finite Amount and a zero Fee, and every block's reward sum
//     must be within the schedule (RewardCell(h)*(1+RewardSumSlack), as I3).
//     Every paid proof ID must be unique on the chain and stored with
//     miner_addr == Recipient. An LMP1 payload outside the reward contract,
//     or in a block below H0, is also an anomaly.
//   - S11: Store.ApplyReconcile with every payment and the window (config
//     hash, tip+1, now).
//   - S12: proofs_H from Store.Counters, and emitted_H as the float64 sum, in
//     block and tx order, of the LMP1 reward amounts in [FirstHeight, tip].
//     The window must start at or below tip+1. Guard.ObserveTotals applies
//     the graceful thresholds, and a reached expiry stops admission
//     (CauseExpired).
//   - S13: Ledger.Init with Store.Pending, the totals and the funder's
//     balance and nonce from Accounts.
//   - S14: Guard.PreArm, after every outcome. A failure trips CausePreArm.
//
// Missing dependencies and a malformed Blocks slice (reported as S7, before
// the Store is opened) and every S9-S13 anomaly or failure trip
// CauseReconcile. The Freeze cause is "<cause>:<step>: <detail>". A threshold
// or expiry reached at S12 latches ADMISSION_STOPPED but leaves the result
// clean: payouts continue. The returned error is nil exactly when
// report.Clean; otherwise it wraps ErrReconcile and any underlying error, and
// is for logging only: FREEZE has been tripped and the process continues. A
// nil Guard is a wiring error that trips nothing.
func Reconcile(c ReconcileConfig) (ReconcileReport, error) {
	if c.Guard == nil {
		return ReconcileReport{}, errors.New("legacymining: Reconcile requires a Guard")
	}
	r := &reconciler{c: c, rewardCell: c.RewardCell, now: c.Now}
	if r.rewardCell == nil {
		r.rewardCell = DefaultRewardCell
	}
	if r.now == nil {
		r.now = time.Now
	}
	failure := r.run()
	if failure != nil {
		c.Guard.Freeze(failure.tripCause())
	}
	// S14 runs after every outcome, so that a later trip finds its marker
	// armed.
	if err := c.Guard.PreArm(); err != nil {
		f := &reconcileFailure{step: "S14", cause: CausePreArm, err: err}
		c.Guard.Freeze(f.tripCause())
		if failure == nil {
			failure = f
		}
	}
	if failure != nil {
		r.rep.Step = failure.step
		return r.rep, failure
	}
	r.rep.Clean = true
	return r.rep, nil
}

// reconcileFailure is the first failed step of Reconcile. It is also the
// returned error.
type reconcileFailure struct {
	step   string // "S7".."S14"
	cause  string // CauseDBOpen, CauseDBMissing, CauseReconcile or CausePreArm
	detail string
	err    error // the underlying error, if any
}

// tripCause is the Guard.Freeze cause: "<cause>:<step>: <detail>: <err>".
func (f *reconcileFailure) tripCause() string {
	s := f.cause + ":" + f.step
	if f.detail != "" {
		s += ": " + f.detail
	}
	if f.err != nil {
		s += ": " + f.err.Error()
	}
	return s
}

func (f *reconcileFailure) Error() string { return ErrReconcile.Error() + ": " + f.tripCause() }

func (f *reconcileFailure) Unwrap() []error {
	if f.err == nil {
		return []error{ErrReconcile}
	}
	return []error{ErrReconcile, f.err}
}

// reconcileReward is one LMP1 reward found by S10, for the S12 sum.
type reconcileReward struct {
	height uint64
	amount float64
}

type reconciler struct {
	c          ReconcileConfig
	rewardCell func(uint64) float64
	now        func() time.Time
	rep        ReconcileReport
	tip        uint64
	rewards    []reconcileReward // S10: every LMP1 reward in [H0, tip], in chain order
}

func (r *reconciler) fail(step, cause, detail string, err error) *reconcileFailure {
	return &reconcileFailure{step: step, cause: cause, detail: detail, err: err}
}

// note records one S9 or S10 finding.
func (r *reconciler) note(format string, a ...any) {
	r.rep.AnomalyCount++
	if len(r.rep.Anomalies) < reconcileMaxAnomalies {
		r.rep.Anomalies = append(r.rep.Anomalies, fmt.Sprintf(format, a...))
	}
}

// anomalies returns a CauseReconcile failure for the recorded findings, or
// nil if there are none. The detail is the first finding.
func (r *reconciler) anomalies(step string) *reconcileFailure {
	if r.rep.AnomalyCount == 0 {
		return nil
	}
	d := r.rep.Anomalies[0]
	if n := r.rep.AnomalyCount - 1; n > 0 {
		d += fmt.Sprintf(" (and %d more)", n)
	}
	return r.fail(step, CauseReconcile, d, nil)
}

func (r *reconciler) run() *reconcileFailure {
	c := r.c
	if c.Store == nil || c.Ledger == nil || c.Accounts == nil {
		return r.fail("S7", CauseReconcile, "Store, Ledger and Accounts are required", nil)
	}
	if f := r.checkChain(); f != nil {
		return f
	}
	if f := r.openStore(); f != nil {
		return f
	}
	meta, f := r.checkMeta()
	if f != nil {
		return f
	}
	pays, f := r.scan(meta.H0)
	if f != nil {
		return f
	}
	hash := c.Guard.ConfigHash()

	// S11.
	counts, err := c.Store.ApplyReconcile(Reconciliation{
		Paid:   pays,
		Window: ConfigWindow{ConfigSHA256: hash, FirstHeight: r.tip + 1, ActivatedNS: r.now().UnixNano()},
	})
	if err != nil {
		return r.fail("S11", CauseReconcile, "apply", err)
	}
	r.rep.Counts = counts

	// S12.
	ctr, err := c.Store.Counters(hash)
	switch {
	case err != nil:
		return r.fail("S12", CauseReconcile, "counters", err)
	case !ctr.Known:
		return r.fail("S12", CauseReconcile, fmt.Sprintf("config window %x is missing after S11", hash[:]), nil)
	case ctr.Window.FirstHeight > r.tip+1:
		return r.fail("S12", CauseReconcile, fmt.Sprintf("config window %x starts at height %d, above tip+1 %d",
			hash[:], ctr.Window.FirstHeight, r.tip+1), nil)
	}
	var emitted float64
	for _, rw := range r.rewards {
		if rw.height >= ctr.Window.FirstHeight {
			emitted += rw.amount
		}
	}
	tot := Totals{ConfigSHA256: hash, Proofs: ctr.Proofs, Emitted: emitted}
	r.rep.Window, r.rep.Totals = ctr.Window, tot
	c.Guard.ObserveTotals(tot, r.rewardCell(r.tip+1))
	if cfg := c.Guard.Config(); r.now().Unix() >= cfg.ExpiresUnix {
		c.Guard.StopAdmission(fmt.Sprintf("%s:expires_unix=%d", CauseExpired, cfg.ExpiresUnix))
	}

	// S13.
	pending, err := c.Store.Pending()
	if err != nil {
		return r.fail("S13", CauseReconcile, "pending", err)
	}
	acc, ok := c.Accounts.Get(chain.MiningRewardFunderAddress)
	if !ok || acc == nil {
		return r.fail("S13", CauseReconcile, fmt.Sprintf("funder account %s is missing from the restored account store",
			chain.MiningRewardFunderAddress), nil)
	}
	if err := c.Ledger.Init(LedgerInit{Pending: pending, Totals: tot, FunderBalance: acc.Balance, FunderNonce: acc.Nonce}); err != nil {
		return r.fail("S13", CauseReconcile, "ledger init", err)
	}
	r.rep.Pending = len(pending)
	return nil
}

// checkChain requires Blocks to hold the blocks at heights 0..tip in order,
// with a genesis hash. RestoreChain has already checked hashes and links.
func (r *reconciler) checkChain() *reconcileFailure {
	bs := r.c.Blocks
	if len(bs) == 0 {
		return r.fail("S7", CauseReconcile, "the restored chain is empty", nil)
	}
	for i, b := range bs {
		if b == nil || b.Height != uint64(i) {
			return r.fail("S7", CauseReconcile, fmt.Sprintf("restored chain: index %d is not the block at height %d", i, i), nil)
		}
	}
	if bs[0].Hash == "" {
		return r.fail("S7", CauseReconcile, "restored chain: the genesis block has no hash", nil)
	}
	r.tip = bs[len(bs)-1].Height
	r.rep.Tip = r.tip
	return nil
}

// openStore is S7 and S8.
func (r *reconciler) openStore() *reconcileFailure {
	c := r.c
	err := c.Store.Open(c.DBPath, nil)
	switch {
	case err == nil:
	case errors.Is(err, ErrDBMissing):
		if h, id, ok := r.findTag(); ok {
			return r.fail("S8", CauseDBMissing, fmt.Sprintf("the DB is missing but block %d tx %q carries an LMP1 payload", h, id), nil)
		}
		m := Meta{
			H0:          r.tip + 1,
			GenesisHash: c.Blocks[0].Hash,
			Funder:      chain.MiningRewardFunderAddress,
			Release:     c.Release,
			CreatedNS:   r.now().UnixNano(),
		}
		if err := c.Store.Open(c.DBPath, &m); err != nil {
			return r.fail("S8", CauseDBOpen, "create", err)
		}
		r.rep.Created = true
	default:
		return r.fail("S7", CauseDBOpen, "open", err)
	}
	r.rep.StoreOpen = true
	return nil
}

// findTag returns the first restored tx whose payload starts with
// PayloadTag (S8).
func (r *reconciler) findTag() (uint64, string, bool) {
	for _, b := range r.c.Blocks {
		for _, tx := range b.Transactions {
			if tx != nil && reconcileTagged(tx) {
				return b.Height, tx.ID, true
			}
		}
	}
	return 0, "", false
}

func reconcileTagged(tx *mempool.Tx) bool { return bytes.HasPrefix(tx.Payload, []byte(PayloadTag)) }

// checkMeta is S9.
func (r *reconciler) checkMeta() (Meta, *reconcileFailure) {
	m, err := r.c.Store.Meta()
	if err != nil {
		return Meta{}, r.fail("S9", CauseReconcile, "read meta", err)
	}
	r.rep.Meta = m
	if g := r.c.Blocks[0].Hash; m.GenesisHash != g {
		r.note("meta genesis_hash %q, chain genesis %q", m.GenesisHash, g)
	}
	if m.Funder != chain.MiningRewardFunderAddress {
		r.note("meta funder %q, chain funder %q", m.Funder, chain.MiningRewardFunderAddress)
	}
	if m.H0 > r.tip+1 {
		r.note("meta h0 %d is above tip+1 %d", m.H0, r.tip+1)
	}
	return m, r.anomalies("S9")
}

// scan is S10. It returns every LMP1 payment in [h0, tip] in chain order and
// records the rewards for S12.
func (r *reconciler) scan(h0 uint64) ([]Payment, *reconcileFailure) {
	var pays []Payment
	first := make(map[ProofID]Payment)
	for _, b := range r.c.Blocks {
		var sum float64
		rewards := 0
		for _, tx := range b.Transactions {
			if tx == nil {
				continue
			}
			if b.Height < h0 {
				if reconcileTagged(tx) {
					r.note("height %d is below h0 %d but tx %q carries an LMP1 payload", b.Height, h0, tx.ID)
				}
				continue
			}
			if tx.ContractID != chain.MiningRewardContractID {
				if reconcileTagged(tx) {
					r.note("height %d tx %q: LMP1 payload outside the reward contract (contract %q)", b.Height, tx.ID, tx.ContractID)
				}
				continue
			}
			ids, bad := reconcileCheckReward(tx)
			if bad != "" {
				r.note("height %d tx %q: %s", b.Height, tx.ID, bad)
				continue
			}
			rewards++
			sum += tx.Amount
			r.rewards = append(r.rewards, reconcileReward{height: b.Height, amount: tx.Amount})
			for _, id := range ids {
				if p, dup := first[id]; dup {
					r.note("height %d tx %q: proof %x is paid again (first paid at height %d by tx %q)", b.Height, tx.ID, id[:], p.Height, p.TxID)
					continue
				}
				p := Payment{ProofID: id, MinerAddr: tx.Recipient, Height: b.Height, TxID: tx.ID}
				first[id] = p
				pays = append(pays, p)
			}
		}
		if rewards > 0 {
			if limit := r.rewardCell(b.Height) * (1 + RewardSumSlack); !(sum <= limit) {
				r.note("height %d: reward amounts sum to %v, above the schedule %v", b.Height, sum, limit)
			}
		}
	}
	if f := r.anomalies("S10"); f != nil {
		return nil, f
	}

	ids := make([]ProofID, len(pays))
	for i, p := range pays {
		ids[i] = p.ProofID
	}
	rows, err := r.c.Store.Lookup(ids)
	if err != nil {
		return nil, r.fail("S10", CauseReconcile, "lookup", err)
	}
	for _, p := range pays {
		switch row, ok := rows[p.ProofID]; {
		case !ok:
			r.note("height %d tx %q: proof %x is not stored (orphan)", p.Height, p.TxID, p.ProofID[:])
		case row.MinerAddr != p.MinerAddr:
			r.note("height %d tx %q: proof %x belongs to %s, paid to %s", p.Height, p.TxID, p.ProofID[:], row.MinerAddr, p.MinerAddr)
		}
	}
	if f := r.anomalies("S10"); f != nil {
		return nil, f
	}
	r.rep.Paid = len(pays)
	return pays, nil
}

// reconcileCheckReward checks one reward-contract tx at or above h0 (S10). It
// returns the payload's IDs, or a description of the first problem.
func reconcileCheckReward(tx *mempool.Tx) ([]ProofID, string) {
	if !reconcileTagged(tx) {
		return nil, "untagged reward (no LMP1 payload)"
	}
	ids, err := DecodePayload(tx.Payload)
	switch {
	case err != nil:
		return nil, err.Error()
	case tx.Sender != chain.MiningRewardFunderAddress:
		return nil, fmt.Sprintf("sender %q is not the funder", tx.Sender)
	case tx.ID != ledgerRewardID(tx.Nonce, tx.Recipient, tx.Payload):
		return nil, fmt.Sprintf("tx ID is not the derived ID %q", ledgerRewardID(tx.Nonce, tx.Recipient, tx.Payload))
	case !(tx.Amount > 0) || math.IsInf(tx.Amount, 0):
		return nil, fmt.Sprintf("amount %v is not positive and finite", tx.Amount)
	case math.Float64bits(tx.Fee) != 0:
		return nil, fmt.Sprintf("fee %v is not zero", tx.Fee)
	}
	return ids, ""
}
