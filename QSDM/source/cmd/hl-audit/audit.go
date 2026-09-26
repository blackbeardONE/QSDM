package main

// The offline exactly-once audit (HL1 design rev 4: §7 "Audit: hl-audit on
// copies" and oracles 1-3, 7; §8 step 5.6). Section, rule and oracle
// references are to that design.
//
// The audit reads a copy of the core state directory. It never writes to its
// inputs: legacy-mining.db is copied into a private temporary directory
// before SQLite opens it.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

const (
	// ReportFormat identifies the JSON report layout.
	ReportFormat = "hl-audit/v1"

	// JournalFile is the chain journal name in the state directory.
	JournalFile = "qsdm_chain.ndjson"

	resultPass = "PASS"
	resultFail = "FAIL"

	sevFail = "fail"
	sevWarn = "warn"

	// maxFindings bounds Report.Findings; FindingsDropped counts the rest.
	maxFindings = 1000
	// maxLineBytes bounds one journal line (the core loader allows 4 MiB).
	maxLineBytes = 8 << 20
	// maxWatermarkBytes bounds a watermark file, as core does.
	maxWatermarkBytes = 4096
)

// Finding codes. Every code except the warn-only ones fails the audit.
const (
	CodeJournalParse       = "journal-parse"        // a complete line is not a block
	CodeJournalTornTail    = "journal-torn-tail"    // bytes after the last "\n" (R-C3)
	CodeJournalGap         = "journal-gap"          // height or prev_hash discontinuity
	CodeJournalEmptyLine   = "journal-empty-line"   // warn
	CodeDoublePay          = "double-pay"           // oracle 1: a proof ID in two payloads
	CodeTagBelowH0         = "lmp1-below-h0"        // LMP1 payload below meta.h0
	CodeTagOutsideReward   = "lmp1-outside-reward"  // LMP1 payload outside the reward contract
	CodeRewardUntagged     = "reward-untagged"      // reward tx at or above h0 without LMP1
	CodeRewardBadPayload   = "reward-bad-payload"   // LMP1 payload does not decode
	CodeRewardBadSender    = "reward-bad-sender"    // sender is not the funder
	CodeRewardBadID        = "reward-bad-id"        // tx ID is not RewardIDFormat
	CodeRewardBadAmount    = "reward-bad-amount"    // amount not positive and finite, or fee != 0
	CodeRewardOverSchedule = "reward-over-schedule" // block reward sum above the schedule (I3)
	CodeDBOpen             = "db-open"              // legacy-mining.db fails the store checks
	CodeDBMissing          = "db-missing"           // LMP1 payments on the chain, no DB (S8)
	CodeDBMeta             = "db-meta-mismatch"     // S9
	CodeOrphanPayment      = "orphan-payment"       // chain-paid ID with no DB row
	CodeRecipientMismatch  = "recipient-mismatch"   // recipient != row miner_addr
	CodeDBPaidNotOnChain   = "db-paid-not-on-chain" // DB says paid, the chain does not (W3)
	CodeDBPaidMismatch     = "db-paid-mismatch"     // DB paid height or tx differs from the chain
	CodeDBUnmarked         = "db-unmarked-payment"  // warn: chain-paid, DB unpaid (S11 repairs)
	CodeWatermarkAbsent    = "watermark-absent"     // warn unless -require-watermark
	CodeWatermarkInvalid   = "watermark-invalid"    // S5 rule 2
	CodeWatermarkAboveTip  = "watermark-above-tip"  // S5 rule 3 (R-W)
	CodeWatermarkHash      = "watermark-hash-mismatch"
	CodeWatermarkBehindTip = "watermark-behind-tip" // S5 rule 5: tip > W+1
	CodeWatermarkRegress   = "watermark-regression" // W below the previously served high-water
	CodeServedHeightLost   = "served-height-lost"   // oracle 3: a served height disappeared
	CodeServedBlockChanged = "served-block-changed" // oracle 3: a served block changed
	CodeRetiredInvalid     = "retired-watermark-invalid"
	CodeCanaryConfig       = "canary-config-invalid"
	CodeCanaryWindow       = "canary-window-absent" // warn
	CodeBudgetExceeded     = "budget-exceeded"      // emitted_H > B
	CodeProofsTotal        = "proofs-total-exceeded"
	CodeNotAllowlisted     = "recipient-not-allowlisted"
)

// Options are the audit inputs. Paths refer to copies, never to live state.
type Options struct {
	// JournalPath is the chain journal (required).
	JournalPath string
	// WatermarkPath is the served watermark W; "" skips the W checks.
	WatermarkPath string
	// RetiredDir is searched for WatermarkRetiredPrefix files; "" skips them.
	RetiredDir string
	// DBPath is legacy-mining.db; "" means no DB (Stage A, or not captured).
	DBPath string
	// Prev is an earlier report; its ServedHighWater must still be served.
	Prev *Report
	// CanaryConfig is the canary config file bytes; nil skips the budget checks.
	CanaryConfig []byte
	// RequireWatermark turns an absent W into a failure.
	RequireWatermark bool
	// RewardCell is the float-CELL schedule; nil means legacymining.DefaultRewardCell.
	RewardCell func(uint64) float64
	// TempDir is where the DB copy is made; "" means os.TempDir.
	TempDir string
}

// Finding is one audit result.
type Finding struct {
	Code     string  `json:"code"`
	Severity string  `json:"severity"`
	Height   *uint64 `json:"height,omitempty"`
	Detail   string  `json:"detail"`
}

// Point is a (height, hash) pair that was served: a watermark.
type Point struct {
	Height uint64 `json:"height"`
	Hash   string `json:"hash"`
	Source string `json:"source,omitempty"`
	Origin string `json:"origin,omitempty"` // where the audit found it
}

// ChainSummary describes the journal.
type ChainSummary struct {
	JournalBytes  int64  `json:"journal_bytes"`
	Blocks        uint64 `json:"blocks"`
	FirstHeight   uint64 `json:"first_height"`
	Tip           uint64 `json:"tip"`
	TipHash       string `json:"tip_hash"`
	GenesisHash   string `json:"genesis_hash,omitempty"`
	TornTailBytes int    `json:"torn_tail_bytes,omitempty"`
}

// DBSummary describes legacy-mining.db.
type DBSummary struct {
	H0          uint64 `json:"h0"`
	GenesisHash string `json:"genesis_hash"`
	Funder      string `json:"funder"`
	Release     string `json:"release"`
	Rows        int    `json:"rows"`
	Paid        int    `json:"paid"`
	Unpaid      int    `json:"unpaid"`
	Windows     int    `json:"config_windows"`
}

// PaymentSummary describes the LMP1 payments found on the chain.
type PaymentSummary struct {
	RewardTxs       int     `json:"reward_txs"`
	PayloadIDs      int     `json:"payload_ids"`
	DistinctIDs     int     `json:"distinct_ids"`
	FirstPaidHeight uint64  `json:"first_paid_height,omitempty"`
	LastPaidHeight  uint64  `json:"last_paid_height,omitempty"`
	Emitted         float64 `json:"emitted_cell"`
}

// CanarySummary describes the active canary window.
type CanarySummary struct {
	ConfigSHA256   string  `json:"config_sha256"`
	FirstHeight    *uint64 `json:"window_first_height,omitempty"`
	Emitted        float64 `json:"emitted_cell"`
	BudgetCell     uint64  `json:"budget_cell"`
	Proofs         uint64  `json:"proofs"`
	MaxProofsTotal uint64  `json:"max_proofs_total"`
}

// Report is the audit result. Its JSON form is the -out file and the -prev
// input of the next audit.
type Report struct {
	Format          string          `json:"format"`
	Result          string          `json:"result"`
	Chain           ChainSummary    `json:"chain"`
	Watermark       *Point          `json:"watermark,omitempty"`
	Retired         []Point         `json:"retired_watermarks,omitempty"`
	DB              *DBSummary      `json:"db,omitempty"`
	Payments        PaymentSummary  `json:"payments"`
	Canary          *CanarySummary  `json:"canary,omitempty"`
	ServedHighWater *Point          `json:"served_high_water,omitempty"`
	Findings        []Finding       `json:"findings"`
	FindingsDropped int             `json:"findings_dropped,omitempty"`
	failed          bool            // any fail finding, including dropped ones
	counts          map[string]bool // codes seen
}

// Failed reports whether any fail-severity finding was recorded.
func (r *Report) Failed() bool { return r.failed }

// Has reports whether a finding with code was recorded.
func (r *Report) Has(code string) bool { return r.counts[code] }

func (r *Report) add(code, sev string, height *uint64, format string, a ...any) {
	if sev == sevFail {
		r.failed = true
	}
	if r.counts == nil {
		r.counts = make(map[string]bool)
	}
	r.counts[code] = true
	if len(r.Findings) >= maxFindings {
		r.FindingsDropped++
		return
	}
	r.Findings = append(r.Findings, Finding{Code: code, Severity: sev, Height: height, Detail: fmt.Sprintf(format, a...)})
}

func h(v uint64) *uint64 { return &v }

// ParseReport decodes a report written by an earlier audit.
func ParseReport(data []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("previous report: %w", err)
	}
	if r.Format != ReportFormat {
		return nil, fmt.Errorf("previous report: format %q, want %q", r.Format, ReportFormat)
	}
	if hw := r.ServedHighWater; hw != nil && !isLowerHex(hw.Hash, 64) {
		return nil, fmt.Errorf("previous report: served_high_water hash %q", hw.Hash)
	}
	return &r, nil
}

// payment is the first LMP1 payment of a proof ID.
type payment struct {
	height    uint64
	txID      string
	recipient string
}

// reward is one well-formed LMP1 reward, for the budget sums.
type reward struct {
	height    uint64
	amount    float64
	recipient string
}

type auditor struct {
	o       Options
	rep     *Report
	cell    func(uint64) float64
	db      *dbData
	h0      uint64 // meta.h0; math.MaxUint64 without a DB
	wanted  map[uint64]string
	paid    map[legacymining.ProofID]payment
	rewards []reward
	haveTip bool
}

// Audit runs the offline audit. It returns an error only when an input cannot
// be read at all; every consistency problem is a Finding.
func Audit(o Options) (*Report, error) {
	a := &auditor{
		o:      o,
		rep:    &Report{Format: ReportFormat, Findings: []Finding{}},
		cell:   o.RewardCell,
		h0:     math.MaxUint64,
		wanted: make(map[uint64]string),
		paid:   make(map[legacymining.ProofID]payment),
	}
	if a.cell == nil {
		a.cell = legacymining.DefaultRewardCell
	}
	if o.JournalPath == "" {
		return nil, errors.New("no journal path")
	}

	// Inputs that decide what the journal scan must record.
	w, err := a.readWatermark()
	if err != nil {
		return nil, err
	}
	retired, err := a.readRetired()
	if err != nil {
		return nil, err
	}
	if err := a.readDB(); err != nil {
		return nil, err
	}
	cfg, cfgHash := a.readCanaryConfig()

	var points []Point
	if w != nil {
		points = append(points, *w)
	}
	points = append(points, retired...)
	if o.Prev != nil && o.Prev.ServedHighWater != nil {
		points = append(points, *o.Prev.ServedHighWater)
	}
	for _, p := range points {
		a.wanted[p.Height] = ""
	}

	if err := a.scan(); err != nil {
		return nil, err
	}
	a.checkServed(w, retired)
	a.checkDB()
	a.checkCanary(cfg, cfgHash)

	if a.rep.failed {
		a.rep.Result = resultFail
	} else {
		a.rep.Result = resultPass
	}
	return a.rep, nil
}

// -----------------------------------------------------------------------------
// Watermarks
// -----------------------------------------------------------------------------

func (a *auditor) readWatermark() (*Point, error) {
	if a.o.WatermarkPath == "" {
		return nil, nil
	}
	data, err := readSmallFile(a.o.WatermarkPath)
	if errors.Is(err, fs.ErrNotExist) {
		sev := sevWarn
		if a.o.RequireWatermark {
			sev = sevFail
		}
		a.rep.add(CodeWatermarkAbsent, sev, nil, "%s is absent", a.o.WatermarkPath)
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("watermark: %w", err)
	}
	wm, err := parseWatermark(data)
	if err != nil {
		a.rep.add(CodeWatermarkInvalid, sevFail, nil, "%s: %v", a.o.WatermarkPath, err)
		return nil, nil
	}
	p := &Point{Height: wm.Height, Hash: wm.Hash, Source: wm.Source, Origin: filepath.Base(a.o.WatermarkPath)}
	a.rep.Watermark = p
	return p, nil
}

func (a *auditor) readRetired() ([]Point, error) {
	if a.o.RetiredDir == "" {
		return nil, nil
	}
	ents, err := os.ReadDir(a.o.RetiredDir)
	if err != nil {
		return nil, fmt.Errorf("retired watermarks: %w", err)
	}
	var out []Point
	for _, e := range ents { // ReadDir sorts by name
		if !strings.HasPrefix(e.Name(), legacymining.WatermarkRetiredPrefix) {
			continue
		}
		data, err := readSmallFile(filepath.Join(a.o.RetiredDir, e.Name()))
		if err != nil {
			a.rep.add(CodeRetiredInvalid, sevWarn, nil, "%s: %v", e.Name(), err)
			continue
		}
		wm, err := parseWatermark(data)
		if err != nil {
			a.rep.add(CodeRetiredInvalid, sevWarn, nil, "%s: %v", e.Name(), err)
			continue
		}
		out = append(out, Point{Height: wm.Height, Hash: wm.Hash, Source: wm.Source, Origin: e.Name()})
	}
	a.rep.Retired = out
	return out, nil
}

// parseWatermark mirrors core's strict S5 decoding (cmd/qsdm hl1ParseWatermark).
func parseWatermark(data []byte) (legacymining.Watermark, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w legacymining.Watermark
	if err := dec.Decode(&w); err != nil {
		return w, fmt.Errorf("decode: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return w, errors.New("trailing data")
	}
	if w.Version != legacymining.WatermarkVersion {
		return w, fmt.Errorf("version %d", w.Version)
	}
	if !isLowerHex(w.Hash, 64) {
		return w, fmt.Errorf("hash %q", w.Hash)
	}
	switch w.Source {
	case legacymining.WatermarkSourceSeal, legacymining.WatermarkSourceBoot,
		legacymining.WatermarkSourceReplay, legacymining.WatermarkSourceSeed:
	default:
		return w, fmt.Errorf("source %q", w.Source)
	}
	return w, nil
}

// checkServed applies S5 rules 3-5 to W and oracle 3 to every earlier served
// point, then records the new served high-water mark.
func (a *auditor) checkServed(w *Point, retired []Point) {
	tip, have := a.rep.Chain.Tip, a.haveTip
	tipDesc := fmt.Sprintf("the journal tip %d", tip)
	if !have {
		tipDesc = "an empty journal"
	}
	if w != nil {
		switch got := a.wanted[w.Height]; {
		case !have || w.Height > tip:
			a.rep.add(CodeWatermarkAboveTip, sevFail, h(w.Height), "W is %d, above %s (R-W)", w.Height, tipDesc)
		case got != w.Hash:
			a.rep.add(CodeWatermarkHash, sevFail, h(w.Height), "W hash %s, journal block %d hash %q (R-W)", w.Hash, w.Height, got)
		case tip > w.Height+1:
			a.rep.add(CodeWatermarkBehindTip, sevFail, h(w.Height), "journal tip %d is above W+1 = %d: blocks not sealed by HL1 (S5 rule 5)", tip, w.Height+1)
		}
	}
	check := func(p Point, what string) {
		switch got := a.wanted[p.Height]; {
		case !have || p.Height > tip:
			a.rep.add(CodeServedHeightLost, sevFail, h(p.Height), "%s: served height %d is above %s", what, p.Height, tipDesc)
		case got != p.Hash:
			a.rep.add(CodeServedBlockChanged, sevFail, h(p.Height), "%s: served block %d had hash %s, the journal has %s", what, p.Height, p.Hash, got)
		}
	}
	for _, p := range retired {
		check(p, "retired watermark "+p.Origin)
	}
	var prev *Point
	if a.o.Prev != nil && a.o.Prev.ServedHighWater != nil {
		p := *a.o.Prev.ServedHighWater
		prev = &p
		check(p, "previous audit high-water")
		if w != nil && w.Height < p.Height {
			a.rep.add(CodeWatermarkRegress, sevFail, h(w.Height), "W is %d, below the previously audited served high-water %d", w.Height, p.Height)
		}
	}

	// The next audit must still find the highest height ever served.
	best := prev
	cands := append([]Point(nil), retired...)
	if w != nil {
		cands = append(cands, *w)
	}
	for i := range cands {
		if best == nil || cands[i].Height > best.Height {
			c := cands[i]
			best = &c
		}
	}
	if best != nil {
		hw := Point{Height: best.Height, Hash: best.Hash, Origin: best.Origin}
		a.rep.ServedHighWater = &hw
	}
}

// -----------------------------------------------------------------------------
// Journal scan
// -----------------------------------------------------------------------------

// lightBlock is the part of a block every line is decoded into.
type lightBlock struct {
	Height   uint64 `json:"height"`
	PrevHash string `json:"prev_hash"`
	Hash     string `json:"hash"`
}

var (
	// tagB64 is base64(PayloadTag). The tag is 9 bytes, so every payload
	// that starts with it has a JSON string that starts with these 12 bytes.
	tagB64 = []byte("UVNETS1MTVAx")
	// rewardContractJSON appears in every line holding a reward tx.
	rewardContractJSON = []byte(chain.MiningRewardContractID)
)

func (a *auditor) scan() error {
	f, err := os.Open(a.o.JournalPath)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	var (
		lineNo  int
		prev    lightBlock
		n       uint64
		total   int64
		tornLen int
	)
	for {
		line, err := r.ReadBytes('\n')
		total += int64(len(line))
		if err == io.EOF {
			if len(line) > 0 {
				tornLen = len(line)
			}
			break
		}
		if err != nil {
			return fmt.Errorf("journal: read: %w", err)
		}
		lineNo++
		line = line[:len(line)-1]
		if len(line) == 0 {
			a.rep.add(CodeJournalEmptyLine, sevWarn, nil, "line %d is empty", lineNo)
			continue
		}
		if len(line) > maxLineBytes {
			a.rep.add(CodeJournalParse, sevFail, nil, "line %d is %d bytes; the audit stops here", lineNo, len(line))
			break
		}
		var lb lightBlock
		if err := json.Unmarshal(line, &lb); err != nil {
			a.rep.add(CodeJournalParse, sevFail, nil, "line %d: %v; the audit stops here", lineNo, err)
			break
		}
		if n > 0 && (lb.Height != prev.Height+1 || lb.PrevHash != prev.Hash) {
			a.rep.add(CodeJournalGap, sevFail, h(lb.Height), "line %d: height %d prev_hash %s follows height %d hash %s",
				lineNo, lb.Height, lb.PrevHash, prev.Height, prev.Hash)
		}
		if n == 0 {
			a.rep.Chain.FirstHeight = lb.Height
			if lb.Height == 0 {
				a.rep.Chain.GenesisHash = lb.Hash
			}
		}
		if _, ok := a.wanted[lb.Height]; ok {
			a.wanted[lb.Height] = lb.Hash
		}
		if lb.Height >= a.h0 || bytes.Contains(line, tagB64) || bytes.Contains(line, rewardContractJSON) {
			var blk chain.Block
			if err := json.Unmarshal(line, &blk); err != nil {
				a.rep.add(CodeJournalParse, sevFail, h(lb.Height), "line %d: %v; the audit stops here", lineNo, err)
				break
			}
			a.scanBlock(&blk)
		}
		prev = lb
		n++
	}
	a.rep.Chain.JournalBytes = total
	a.rep.Chain.Blocks = n
	if n > 0 {
		a.haveTip = true
		a.rep.Chain.Tip = prev.Height
		a.rep.Chain.TipHash = prev.Hash
	}
	if tornLen > 0 {
		a.rep.Chain.TornTailBytes = tornLen
		a.rep.add(CodeJournalTornTail, sevFail, nil, "%d bytes after the last newline (R-C3)", tornLen)
	}
	pay := &a.rep.Payments
	pay.DistinctIDs = len(a.paid)
	return nil
}

func tagged(tx *mempool.Tx) bool { return bytes.HasPrefix(tx.Payload, []byte(legacymining.PayloadTag)) }

// scanBlock checks the LMP1 payments of one block (S10 rules, oracle 1).
func (a *auditor) scanBlock(b *chain.Block) {
	var sum float64
	rewards := 0
	for _, tx := range b.Transactions {
		if tx == nil {
			continue
		}
		isReward := tx.ContractID == chain.MiningRewardContractID
		if tagged(tx) {
			switch {
			case b.Height < a.h0 && a.db != nil:
				a.rep.add(CodeTagBelowH0, sevFail, h(b.Height), "tx %q carries an LMP1 payload below h0 %d", tx.ID, a.h0)
			case !isReward:
				a.rep.add(CodeTagOutsideReward, sevFail, h(b.Height), "tx %q carries an LMP1 payload in contract %q", tx.ID, tx.ContractID)
			}
			// Oracle 1 counts every decodable payload, wherever it is.
			if ids, err := legacymining.DecodePayload(tx.Payload); err == nil {
				a.record(b.Height, tx, ids)
			}
		}
		if !isReward || b.Height < a.h0 {
			continue
		}
		if bad := a.checkReward(b.Height, tx); bad {
			continue
		}
		rewards++
		sum += tx.Amount
		a.rewards = append(a.rewards, reward{height: b.Height, amount: tx.Amount, recipient: tx.Recipient})
		a.rep.Payments.RewardTxs++
		a.rep.Payments.Emitted += tx.Amount
	}
	if rewards > 0 {
		if limit := a.cell(b.Height) * (1 + legacymining.RewardSumSlack); !(sum <= limit) {
			a.rep.add(CodeRewardOverSchedule, sevFail, h(b.Height), "reward amounts sum to %v, above the schedule %v", sum, limit)
		}
	}
}

// checkReward applies the S10 per-tx rules to a reward tx at or above h0 and
// reports whether it is malformed.
func (a *auditor) checkReward(height uint64, tx *mempool.Tx) bool {
	if !tagged(tx) {
		a.rep.add(CodeRewardUntagged, sevFail, h(height), "reward tx %q has no LMP1 payload", tx.ID)
		return true
	}
	if _, err := legacymining.DecodePayload(tx.Payload); err != nil {
		a.rep.add(CodeRewardBadPayload, sevFail, h(height), "reward tx %q: %v", tx.ID, err)
		return true
	}
	bad := false
	if tx.Sender != chain.MiningRewardFunderAddress {
		a.rep.add(CodeRewardBadSender, sevFail, h(height), "reward tx %q: sender %q is not the funder", tx.ID, tx.Sender)
		bad = true
	}
	if want := rewardID(tx.Nonce, tx.Recipient, tx.Payload); tx.ID != want {
		a.rep.add(CodeRewardBadID, sevFail, h(height), "reward tx %q: the derived ID is %q", tx.ID, want)
		bad = true
	}
	if !(tx.Amount > 0) || math.IsInf(tx.Amount, 0) || math.Float64bits(tx.Fee) != 0 {
		a.rep.add(CodeRewardBadAmount, sevFail, h(height), "reward tx %q: amount %v fee %v", tx.ID, tx.Amount, tx.Fee)
		bad = true
	}
	return bad
}

// record notes one payload's IDs; a second payment of an ID is a double pay.
func (a *auditor) record(height uint64, tx *mempool.Tx, ids []legacymining.ProofID) {
	pay := &a.rep.Payments
	for _, id := range ids {
		pay.PayloadIDs++
		if first, dup := a.paid[id]; dup {
			a.rep.add(CodeDoublePay, sevFail, h(height), "proof %x is paid again by tx %q (first paid at height %d by tx %q)",
				id[:], tx.ID, first.height, first.txID)
			continue
		}
		a.paid[id] = payment{height: height, txID: tx.ID, recipient: tx.Recipient}
		if pay.FirstPaidHeight == 0 || height < pay.FirstPaidHeight {
			pay.FirstPaidHeight = height
		}
		if height > pay.LastPaidHeight {
			pay.LastPaidHeight = height
		}
	}
}

// rewardID is legacymining.RewardIDFormat applied as the driver does.
func rewardID(nonce uint64, addr string, payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf(legacymining.RewardIDFormat, nonce, addr, hex.EncodeToString(sum[:8]))
}

// -----------------------------------------------------------------------------
// legacy-mining.db
// -----------------------------------------------------------------------------

type dbRow struct {
	id         legacymining.ProofID
	minerAddr  string
	config     legacymining.ConfigHash
	paid       bool
	paidHeight uint64
	paidTxID   string
}

type dbData struct {
	meta    legacymining.Meta
	rows    []dbRow // ordered by proof_id
	windows map[legacymining.ConfigHash]uint64
}

func (a *auditor) readDB() error {
	if a.o.DBPath == "" {
		return nil
	}
	if _, err := os.Lstat(a.o.DBPath); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("db: %w", err)
	}
	d, err := loadDBCopy(a.o.DBPath, a.o.TempDir)
	var fe *dbFinding
	if errors.As(err, &fe) {
		a.rep.add(CodeDBOpen, sevFail, nil, "%v", fe.err)
		return nil
	}
	if err != nil {
		return err
	}
	a.db = d
	a.h0 = d.meta.H0
	s := &DBSummary{H0: d.meta.H0, GenesisHash: d.meta.GenesisHash, Funder: d.meta.Funder, Release: d.meta.Release,
		Rows: len(d.rows), Windows: len(d.windows)}
	for _, r := range d.rows {
		if r.paid {
			s.Paid++
		} else {
			s.Unpaid++
		}
	}
	a.rep.DB = s
	return nil
}

// dbFinding marks a DB that exists but fails the store checks.
type dbFinding struct{ err error }

func (e *dbFinding) Error() string { return e.err.Error() }

// loadDBCopy copies the DB (and its WAL, if any) into a private
// legacy-mining directory, validates it with the production store checks
// (identity, exact schema, quick_check), then reads it.
func loadDBCopy(src, tempRoot string) (*dbData, error) {
	tmp, err := os.MkdirTemp(tempRoot, "hl-audit-")
	if err != nil {
		return nil, fmt.Errorf("db copy: %w", err)
	}
	defer os.RemoveAll(tmp)
	if tmp, err = filepath.Abs(tmp); err != nil {
		return nil, fmt.Errorf("db copy: %w", err)
	}
	dir := filepath.Join(tmp, legacymining.LegacyDirName)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, fmt.Errorf("db copy: %w", err)
	}
	dst := filepath.Join(dir, legacymining.DBFile)
	if err := copyFile(src, dst); err != nil {
		return nil, fmt.Errorf("db copy: %w", err)
	}
	if err := copyFile(src+"-wal", dst+"-wal"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("db copy: %w", err)
	}

	st := legacymining.NewSQLiteStore()
	if err := st.Open(dst, nil); err != nil {
		return nil, &dbFinding{err: err}
	}
	meta, err := st.Meta()
	if cerr := st.Close(); err == nil && cerr != nil {
		err = cerr
	}
	if err != nil {
		return nil, &dbFinding{err: err}
	}

	db, err := sql.Open("sqlite3", sqliteURI(dst))
	if err != nil {
		return nil, fmt.Errorf("db read: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA query_only = 1`); err != nil {
		return nil, fmt.Errorf("db read: %w", err)
	}
	d := &dbData{meta: meta, windows: make(map[legacymining.ConfigHash]uint64)}
	rows, err := db.Query(`SELECT proof_id, miner_addr, config_sha256, paid_height, paid_tx_id FROM proofs ORDER BY proof_id`)
	if err != nil {
		return nil, fmt.Errorf("db read: %w", err)
	}
	for rows.Next() {
		var (
			id, cfg []byte
			r       dbRow
			ph      sql.NullInt64
			ptx     sql.NullString
		)
		if err := rows.Scan(&id, &r.minerAddr, &cfg, &ph, &ptx); err != nil {
			rows.Close()
			return nil, fmt.Errorf("db read: %w", err)
		}
		copy(r.id[:], id)
		copy(r.config[:], cfg)
		if ph.Valid {
			r.paid, r.paidHeight, r.paidTxID = true, uint64(ph.Int64), ptx.String
		}
		d.rows = append(d.rows, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("db read: %w", err)
	}
	rows.Close()
	wrows, err := db.Query(`SELECT config_sha256, first_height FROM config_windows`)
	if err != nil {
		return nil, fmt.Errorf("db read: %w", err)
	}
	defer wrows.Close()
	for wrows.Next() {
		var (
			cfg []byte
			fh  int64
			k   legacymining.ConfigHash
		)
		if err := wrows.Scan(&cfg, &fh); err != nil {
			return nil, fmt.Errorf("db read: %w", err)
		}
		copy(k[:], cfg)
		d.windows[k] = uint64(fh)
	}
	if err := wrows.Err(); err != nil {
		return nil, fmt.Errorf("db read: %w", err)
	}
	return d, nil
}

func sqliteURI(path string) string {
	p := filepath.ToSlash(path)
	if runtime.GOOS == "windows" {
		p = "/" + p
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=rw"}
	return u.String()
}

// checkDB cross-checks the chain payments with the DB rows (S9-S11, W3).
func (a *auditor) checkDB() {
	if a.db == nil {
		if len(a.paid) > 0 && !a.rep.Has(CodeDBOpen) {
			a.rep.add(CodeDBMissing, sevFail, nil, "the chain carries %d LMP1-paid proof IDs but no legacy-mining.db was audited", len(a.paid))
		}
		return
	}
	m := a.db.meta
	if g := a.rep.Chain.GenesisHash; g != "" && m.GenesisHash != g {
		a.rep.add(CodeDBMeta, sevFail, nil, "meta genesis_hash %q, journal genesis %q", m.GenesisHash, g)
	}
	if m.Funder != chain.MiningRewardFunderAddress {
		a.rep.add(CodeDBMeta, sevFail, nil, "meta funder %q, chain funder %q", m.Funder, chain.MiningRewardFunderAddress)
	}
	if a.haveTip && m.H0 > a.rep.Chain.Tip+1 {
		a.rep.add(CodeDBMeta, sevFail, nil, "meta h0 %d is above tip+1 %d", m.H0, a.rep.Chain.Tip+1)
	}

	rows := make(map[legacymining.ProofID]*dbRow, len(a.db.rows))
	for i := range a.db.rows {
		rows[a.db.rows[i].id] = &a.db.rows[i]
	}
	ids := make([]legacymining.ProofID, 0, len(a.paid))
	for id := range a.paid {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	for _, id := range ids {
		p := a.paid[id]
		row, ok := rows[id]
		switch {
		case !ok:
			a.rep.add(CodeOrphanPayment, sevFail, h(p.height), "proof %x paid by tx %q has no DB row", id[:], p.txID)
		case row.minerAddr != p.recipient:
			a.rep.add(CodeRecipientMismatch, sevFail, h(p.height), "proof %x belongs to %s but tx %q pays %s", id[:], row.minerAddr, p.txID, p.recipient)
		case !row.paid:
			a.rep.add(CodeDBUnmarked, sevWarn, h(p.height), "proof %x is paid by tx %q but unpaid in the DB (S11 marks it at the next boot)", id[:], p.txID)
		case row.paidHeight != p.height || row.paidTxID != p.txID:
			a.rep.add(CodeDBPaidMismatch, sevFail, h(p.height), "proof %x: DB paid at %d by %q, chain at %d by %q", id[:], row.paidHeight, row.paidTxID, p.height, p.txID)
		}
	}
	for _, r := range a.db.rows {
		if !r.paid {
			continue
		}
		if _, ok := a.paid[r.id]; !ok {
			a.rep.add(CodeDBPaidNotOnChain, sevFail, h(r.paidHeight), "proof %x: DB paid at %d by %q, the journal carries no such payment", r.id[:], r.paidHeight, r.paidTxID)
		}
	}
}

// -----------------------------------------------------------------------------
// Canary window (§6.1, §6.3, §6.6)
// -----------------------------------------------------------------------------

func (a *auditor) readCanaryConfig() (*legacymining.Config, legacymining.ConfigHash) {
	if a.o.CanaryConfig == nil {
		return nil, legacymining.ConfigHash{}
	}
	hash := legacymining.ConfigHash(sha256.Sum256(a.o.CanaryConfig))
	cfg, err := legacymining.ParseConfig(a.o.CanaryConfig, hash)
	if err != nil {
		a.rep.add(CodeCanaryConfig, sevFail, nil, "%v", err)
		return nil, hash
	}
	return &cfg, hash
}

func (a *auditor) checkCanary(cfg *legacymining.Config, hash legacymining.ConfigHash) {
	if cfg == nil {
		return
	}
	cs := &CanarySummary{ConfigSHA256: hex.EncodeToString(hash[:]), BudgetCell: cfg.BudgetCell, MaxProofsTotal: cfg.MaxProofsTotal}
	a.rep.Canary = cs
	if a.db == nil {
		a.rep.add(CodeCanaryWindow, sevWarn, nil, "no DB: the config window cannot be located")
		return
	}
	first, ok := a.db.windows[hash]
	if !ok {
		a.rep.add(CodeCanaryWindow, sevWarn, nil, "config %x has no config_windows row", hash[:])
		return
	}
	cs.FirstHeight = h(first)
	allowed := make(map[string]bool, len(cfg.Allowed))
	for _, e := range cfg.Allowed {
		allowed[e.MinerAddr] = true
	}
	for _, rw := range a.rewards {
		if rw.height < first {
			continue
		}
		cs.Emitted += rw.amount
		if !allowed[rw.recipient] {
			a.rep.add(CodeNotAllowlisted, sevFail, h(rw.height), "reward to %s, which the config does not allowlist", rw.recipient)
		}
	}
	for _, r := range a.db.rows {
		if r.config == hash {
			cs.Proofs++
		}
	}
	if cs.Emitted > float64(cfg.BudgetCell) {
		a.rep.add(CodeBudgetExceeded, sevFail, nil, "emitted %v CELL in the window, above budget_cell %d", cs.Emitted, cfg.BudgetCell)
	}
	if cs.Proofs > cfg.MaxProofsTotal {
		a.rep.add(CodeProofsTotal, sevFail, nil, "%d proofs in the window, above max_proofs_total %d", cs.Proofs, cfg.MaxProofsTotal)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func readSmallFile(path string) ([]byte, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Size() > maxWatermarkBytes {
		return nil, fmt.Errorf("%s is %d bytes", path, fi.Size())
	}
	return os.ReadFile(path)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
