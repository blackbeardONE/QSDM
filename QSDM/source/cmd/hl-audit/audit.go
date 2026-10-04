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
	CodeCanaryHistory      = "canary-history-inconsistent" // warn: the reconcile events disagree with config_windows

	// HL2 (WP-H), version 2 configs only.
	CodeOwnerEpochCap     = "owner-epoch-cap-exceeded" // an owner's epoch emission above owner_epoch_cap_cell + 2*rewardCell
	CodeOwnerPending      = "owner-pending-exceeded"   // an owner's unpaid proofs above max_pending_per_owner
	CodePendingExceeded   = "pending-exceeded"         // unpaid proofs of the config above max_pending
	CodeOwnerRewardRepeat = "owner-reward-repeated"    // two rewards to one recipient in one block (v2 S10)
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
	// MoreCanaryConfigs are further config files (HL2 WP-H: a v1 window
	// followed by v2 windows, for example), each audited like CanaryConfig
	// and reported in Report.OtherCanaries.
	MoreCanaryConfigs [][]byte
	// RequireWatermark turns an absent W into a failure.
	RequireWatermark bool
	// RewardCell is the float-CELL schedule; nil means legacymining.DefaultRewardCell.
	RewardCell func(uint64) float64
	// TempDir is where the DB copy is made; "" means os.TempDir.
	TempDir string
	// ReceiptsPath is the receipts NDJSON; "" skips the coverage check
	// (receipts.go).
	ReceiptsPath string
	// ReceiptsFrom is the first height whose txs must have receipts; nil
	// means meta.h0 (the check is skipped with a warning without a DB).
	ReceiptsFrom *uint64
	// RequireReceipts turns an absent receipts file into a failure.
	RequireReceipts bool
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

// HeightRange is the block heights [First, End). A nil End means up to and
// including the journal tip.
type HeightRange struct {
	First uint64  `json:"first"`
	End   *uint64 `json:"end,omitempty"`
}

func (r HeightRange) String() string {
	if r.End == nil {
		return fmt.Sprintf("[%d, tip]", r.First)
	}
	return fmt.Sprintf("[%d, %d)", r.First, *r.End)
}

// CanarySummary describes the window of the audited canary config.
type CanarySummary struct {
	ConfigSHA256 string `json:"config_sha256"`
	// FirstHeight is the config's config_windows.first_height.
	FirstHeight *uint64 `json:"window_first_height,omitempty"`
	// Window holds the heights whose rewards make up Emitted. It ends where
	// the next config took over; a nil End means the config is the active
	// one and the window runs to the journal tip.
	Window *HeightRange `json:"window,omitempty"`
	// Active lists the heights sealed under the config. Every reward in
	// them must pay the config's allowlisted address.
	Active         []HeightRange `json:"active,omitempty"`
	RewardTxs      int           `json:"reward_txs"`
	Emitted        float64       `json:"emitted_cell"`
	BudgetCell     uint64        `json:"budget_cell"`
	Proofs         uint64        `json:"proofs"`
	MaxProofsTotal uint64        `json:"max_proofs_total"`
	// HL2 is the per-owner audit of a version 2 config (nil for version 1).
	HL2 *OwnerAudit `json:"hl2,omitempty"`
}

// OwnerAudit is the version 2 (HL2) part of a CanarySummary.
type OwnerAudit struct {
	ConfigVersion              int    `json:"config_version"`
	OwnerEpochCapCell          uint64 `json:"owner_epoch_cap_cell"`
	MaxPending                 int    `json:"max_pending"`
	MaxPendingPerOwner         int    `json:"max_pending_per_owner"`
	DifficultyBits             int    `json:"difficulty_bits"`
	DeferredSlotWeightPermille int    `json:"deferred_slot_weight_permille"`
	// Pending counts the config's rows that are unpaid in the DB and on the
	// chain.
	Pending int            `json:"pending"`
	Owners  []OwnerSummary `json:"owners"`
	// Notes are what the audit cannot check, or reads differently, for v2.
	Notes []string `json:"notes"`
}

// OwnerSummary is one owner (reward recipient, row miner_addr) of a version
// 2 config: its rows accepted under the config and its rewards in the
// config's window.
type OwnerSummary struct {
	Owner     string  `json:"owner"`
	Proofs    uint64  `json:"proofs"`
	Pending   int     `json:"pending"`
	RewardTxs int     `json:"reward_txs"`
	Emitted   float64 `json:"emitted_cell"`
	// PeakEpoch is the owner epoch (legacymining.OwnerEpochOf) with the
	// largest emission to the owner in the window, and PeakEpochEmitted that
	// amount (gross, as core counts it).
	PeakEpoch        uint64  `json:"peak_epoch"`
	PeakEpochEmitted float64 `json:"peak_epoch_emitted_cell"`
}

// Report is the audit result. Its JSON form is the -out file and the -prev
// input of the next audit.
type Report struct {
	Format          string           `json:"format"`
	Result          string           `json:"result"`
	Chain           ChainSummary     `json:"chain"`
	Watermark       *Point           `json:"watermark,omitempty"`
	Retired         []Point          `json:"retired_watermarks,omitempty"`
	DB              *DBSummary       `json:"db,omitempty"`
	Payments        PaymentSummary   `json:"payments"`
	Canary          *CanarySummary   `json:"canary,omitempty"`
	OtherCanaries   []*CanarySummary `json:"other_canaries,omitempty"`
	ServedHighWater *Point           `json:"served_high_water,omitempty"`
	Receipts        *ReceiptSummary  `json:"receipts,omitempty"`
	Findings        []Finding        `json:"findings"`
	FindingsDropped int              `json:"findings_dropped,omitempty"`
	failed          bool             // any fail finding, including dropped ones
	counts          map[string]bool  // codes seen
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

	rcptFrom uint64                // receipts coverage bound; math.MaxUint64: off
	chainTxs map[receiptKey]string // chain txs at or above rcptFrom -> block hash
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
	a.receiptsFrom()
	type canaryInput struct {
		cfg  *legacymining.Config
		hash legacymining.ConfigHash
	}
	var canaries []canaryInput
	for _, data := range append([][]byte{o.CanaryConfig}, o.MoreCanaryConfigs...) {
		if data == nil {
			continue
		}
		cfg, hash := a.readCanaryConfig(data)
		canaries = append(canaries, canaryInput{cfg, hash})
	}

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
	if err := a.checkReceipts(); err != nil {
		return nil, err
	}
	a.checkServed(w, retired)
	a.checkDB()
	v2 := false
	for i, c := range canaries {
		cs := a.checkCanary(c.cfg, c.hash)
		switch {
		case i == 0 && o.CanaryConfig != nil:
			a.rep.Canary = cs
		case cs != nil:
			a.rep.OtherCanaries = append(a.rep.OtherCanaries, cs)
		}
		v2 = v2 || (c.cfg != nil && c.cfg.Version == legacymining.ConfigVersion2)
	}
	if v2 {
		a.checkOwnerRewards()
	}

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
		lmp1 := lb.Height >= a.h0 || bytes.Contains(line, tagB64) || bytes.Contains(line, rewardContractJSON)
		if lmp1 || lb.Height >= a.rcptFrom {
			var blk chain.Block
			if err := json.Unmarshal(line, &blk); err != nil {
				a.rep.add(CodeJournalParse, sevFail, h(lb.Height), "line %d: %v; the audit stops here", lineNo, err)
				break
			}
			if lmp1 {
				a.scanBlock(&blk)
			}
			a.noteTxs(&blk)
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
	nodeID     string
	config     legacymining.ConfigHash
	paid       bool
	paidHeight uint64
	paidTxID   string
}

// dbWindow is one config_windows row.
type dbWindow struct {
	config      legacymining.ConfigHash
	firstHeight uint64
	activatedNS int64
}

// reconcileEvent is one S11 "reconcile" events row: the boot activated
// config at height first (its tip+1), inserting the config_windows row when
// inserted is true.
type reconcileEvent struct {
	id       int64
	config   legacymining.ConfigHash
	first    uint64
	inserted bool
}

type dbData struct {
	meta       legacymining.Meta
	rows       []dbRow // ordered by proof_id
	windows    map[legacymining.ConfigHash]dbWindow
	windowList []dbWindow       // ordered by first_height, activated_ns, config_sha256
	events     []reconcileEvent // ordered by events.id
	eventsErr  error            // the first reconcile event that does not decode
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
	d := &dbData{meta: meta, windows: make(map[legacymining.ConfigHash]dbWindow)}
	rows, err := db.Query(`SELECT proof_id, miner_addr, node_id, config_sha256, paid_height, paid_tx_id FROM proofs ORDER BY proof_id`)
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
		if err := rows.Scan(&id, &r.minerAddr, &r.nodeID, &cfg, &ph, &ptx); err != nil {
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
	wrows, err := db.Query(`SELECT config_sha256, first_height, activated_ns FROM config_windows`)
	if err != nil {
		return nil, fmt.Errorf("db read: %w", err)
	}
	for wrows.Next() {
		var (
			cfg []byte
			fh  int64
			w   dbWindow
		)
		if err := wrows.Scan(&cfg, &fh, &w.activatedNS); err != nil {
			wrows.Close()
			return nil, fmt.Errorf("db read: %w", err)
		}
		copy(w.config[:], cfg)
		w.firstHeight = uint64(fh) // CHECK(first_height>=0)
		d.windows[w.config] = w
		d.windowList = append(d.windowList, w)
	}
	if err := wrows.Err(); err != nil {
		wrows.Close()
		return nil, fmt.Errorf("db read: %w", err)
	}
	wrows.Close()
	sort.Slice(d.windowList, func(i, j int) bool {
		x, y := d.windowList[i], d.windowList[j]
		if x.firstHeight != y.firstHeight {
			return x.firstHeight < y.firstHeight
		}
		if x.activatedNS != y.activatedNS {
			return x.activatedNS < y.activatedNS
		}
		return bytes.Compare(x.config[:], y.config[:]) < 0
	})

	erows, err := db.Query(`SELECT id, detail FROM events WHERE kind = ? ORDER BY id`, reconcileEventKind)
	if err != nil {
		return nil, fmt.Errorf("db read: %w", err)
	}
	defer erows.Close()
	for erows.Next() {
		var (
			id     int64
			detail string
		)
		if err := erows.Scan(&id, &detail); err != nil {
			return nil, fmt.Errorf("db read: %w", err)
		}
		ev, err := parseReconcileEvent(id, detail)
		if err != nil {
			if d.eventsErr == nil {
				d.eventsErr = err
			}
			continue
		}
		d.events = append(d.events, ev)
	}
	if err := erows.Err(); err != nil {
		return nil, fmt.Errorf("db read: %w", err)
	}
	return d, nil
}

// reconcileEventKind is the events.kind that Store.ApplyReconcile (S11)
// writes, in the same transaction as the config_windows insert.
const reconcileEventKind = "reconcile"

// parseReconcileEvent decodes the detail of a reconcile event.
func parseReconcileEvent(id int64, detail string) (reconcileEvent, error) {
	var v struct {
		ConfigSHA256   string  `json:"config_sha256"`
		FirstHeight    *uint64 `json:"first_height"`
		WindowInserted *bool   `json:"window_inserted"`
	}
	ev := reconcileEvent{id: id}
	if err := json.Unmarshal([]byte(detail), &v); err != nil {
		return ev, fmt.Errorf("reconcile event %d: %v", id, err)
	}
	if !isLowerHex(v.ConfigSHA256, 64) || v.FirstHeight == nil || v.WindowInserted == nil {
		return ev, fmt.Errorf("reconcile event %d: detail lacks config_sha256, first_height or window_inserted", id)
	}
	if _, err := hex.Decode(ev.config[:], []byte(v.ConfigSHA256)); err != nil {
		return ev, fmt.Errorf("reconcile event %d: %v", id, err)
	}
	ev.first, ev.inserted = *v.FirstHeight, *v.WindowInserted
	return ev, nil
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
// Canary windows (§6.1, §6.3, §6.5, §6.6; S11, S12)
// -----------------------------------------------------------------------------

func (a *auditor) readCanaryConfig(data []byte) (*legacymining.Config, legacymining.ConfigHash) {
	hash := legacymining.ConfigHash(sha256.Sum256(data))
	cfg, err := legacymining.ParseConfig(data, hash)
	if err != nil {
		a.rep.add(CodeCanaryConfig, sevFail, nil, "%v", err)
		return nil, hash
	}
	return &cfg, hash
}

// The config windows. Every boot that reaches S11 activates its config hash
// H at tip+1: in one DB transaction it inserts config_windows(H, tip+1) if H
// is new and appends a reconcile event naming H and tip+1. Every journal
// block at or above that height was sealed at that boot or a later one, so
// the block at height x was sealed under the config of the last activation
// at or below x. This splits the journal into segments, one per run of a
// config (segments).
//
// For the audited config H (checkCanary):
//   - the allowlist applies to the rewards in H's own segments: I6 checks a
//     recipient against the config active at seal time;
//   - emitted_cell sums the rewards from H's config_windows first_height (the
//     start of S12's emitted_H), or from H's first segment if a rollback put
//     it lower, to the end of H's last segment: the first height sealed under
//     the next config, or the journal tip while H is active. Only a new hash
//     resets core's counters, so a re-activated H keeps its row and core's
//     emitted_H also counts the rewards sealed under the configs in between;
//     the audit counts them too;
//   - proofs counts the DB rows accepted under H (proofs.config_sha256), as
//     S12's proofs_H does.
//
// Without a re-activation or a rollback across a config change, H's window
// is [first_height, the next config_windows row's first_height), and the
// last window runs to the journal tip.

// openEnd is the end of a segment that runs to the journal tip.
const openEnd = math.MaxUint64

// activation is one boot through S11: config is active from height first.
type activation struct {
	config legacymining.ConfigHash
	first  uint64
}

// segment is the heights [from, to) sealed under config; to == openEnd runs
// to the journal tip.
type segment struct {
	config   legacymining.ConfigHash
	from, to uint64
}

func (s segment) has(height uint64) bool { return height >= s.from && height < s.to }

func (s segment) heightRange() HeightRange {
	r := HeightRange{First: s.from}
	if s.to != openEnd {
		r.End = h(s.to)
	}
	return r
}

// segments splits the heights among the activations, given in boot order.
// The segments are in height order and never empty.
func segments(acts []activation) []segment {
	var segs []segment
	for _, ac := range acts {
		// The blocks at or above ac.first were rolled back before this boot.
		for len(segs) > 0 && segs[len(segs)-1].from >= ac.first {
			segs = segs[:len(segs)-1]
		}
		if n := len(segs); n > 0 {
			if segs[n-1].config == ac.config { // a restart under the same config
				segs[n-1].to = openEnd
				continue
			}
			segs[n-1].to = ac.first
		}
		segs = append(segs, segment{config: ac.config, from: ac.first, to: openEnd})
	}
	return segs
}

// eventActivations returns the activations recorded by the reconcile
// events. They must agree with config_windows: every row is inserted, with
// its first_height, by the first event of its config, and every event names
// a config that has a row.
func (d *dbData) eventActivations() ([]activation, error) {
	if d.eventsErr != nil {
		return nil, d.eventsErr
	}
	seen := make(map[legacymining.ConfigHash]bool, len(d.windows))
	acts := make([]activation, 0, len(d.events))
	for _, ev := range d.events {
		w, ok := d.windows[ev.config]
		firstRun := !seen[ev.config]
		switch {
		case !ok:
			return nil, fmt.Errorf("reconcile event %d names config %x, which has no config_windows row", ev.id, ev.config[:])
		case firstRun && !ev.inserted:
			return nil, fmt.Errorf("reconcile event %d first activates config %x but did not insert its window", ev.id, ev.config[:])
		case !firstRun && ev.inserted:
			return nil, fmt.Errorf("reconcile event %d inserts the window of config %x again", ev.id, ev.config[:])
		case firstRun && ev.first != w.firstHeight:
			return nil, fmt.Errorf("reconcile event %d inserts config %x at height %d, config_windows says %d", ev.id, ev.config[:], ev.first, w.firstHeight)
		}
		seen[ev.config] = true
		acts = append(acts, activation{config: ev.config, first: ev.first})
	}
	for _, w := range d.windowList {
		if !seen[w.config] {
			return nil, fmt.Errorf("config %x has a config_windows row but no reconcile event", w.config[:])
		}
	}
	return acts, nil
}

// activations returns the activation history: the reconcile events or, if
// they disagree with config_windows, the config_windows rows in
// (first_height, activated_ns) order.
func (a *auditor) activations() []activation {
	acts, err := a.db.eventActivations()
	if err == nil {
		return acts
	}
	a.rep.add(CodeCanaryHistory, sevWarn, nil, "%v; the config windows follow config_windows alone, so a re-activated config is not seen", err)
	acts = make([]activation, len(a.db.windowList))
	for i, w := range a.db.windowList {
		acts[i] = activation{config: w.config, first: w.firstHeight}
	}
	return acts
}

func (a *auditor) checkCanary(cfg *legacymining.Config, hash legacymining.ConfigHash) *CanarySummary {
	if cfg == nil {
		return nil
	}
	cs := &CanarySummary{ConfigSHA256: hex.EncodeToString(hash[:]), BudgetCell: cfg.BudgetCell, MaxProofsTotal: cfg.MaxProofsTotal}
	if a.db == nil {
		a.rep.add(CodeCanaryWindow, sevWarn, nil, "no DB: the config window cannot be located")
		return cs
	}
	win, ok := a.db.windows[hash]
	if !ok {
		a.rep.add(CodeCanaryWindow, sevWarn, nil, "config %x has no config_windows row", hash[:])
		return cs
	}
	cs.FirstHeight = h(win.firstHeight)

	var own []segment
	for _, s := range segments(a.activations()) {
		if s.config == hash {
			own = append(own, s)
			cs.Active = append(cs.Active, s.heightRange())
		}
	}
	window := segment{config: hash, from: win.firstHeight, to: win.firstHeight} // empty: nothing sealed under it
	if len(own) > 0 {
		window.from = min(window.from, own[0].from)
		window.to = own[len(own)-1].to
	}
	wr := window.heightRange()
	cs.Window = &wr

	allowed := make(map[string]bool, len(cfg.Allowed))
	for _, e := range cfg.Allowed {
		allowed[e.MinerAddr] = true
	}
	// HL2 (WP-D): with a version 2 config, I6 is "the recipient is each
	// paid proof's row miner_addr" (checkDB, CodeRecipientMismatch), not
	// the allowlist, which is optional and may be empty in public mode. A
	// v2 allowlist is an admission rule, checked on the config's rows
	// (checkOwners).
	v2 := cfg.Version == legacymining.ConfigVersion2
	for _, rw := range a.rewards {
		if window.has(rw.height) {
			cs.RewardTxs++
			cs.Emitted += rw.amount
		}
		if v2 || allowed[rw.recipient] {
			continue
		}
		for _, s := range own {
			if s.has(rw.height) {
				a.rep.add(CodeNotAllowlisted, sevFail, h(rw.height), "reward to %s, sealed under the config, which does not allowlist it", rw.recipient)
				break
			}
		}
	}
	for _, r := range a.db.rows {
		if r.config == hash {
			cs.Proofs++
		}
	}
	if cs.Emitted > float64(cfg.BudgetCell) {
		a.rep.add(CodeBudgetExceeded, sevFail, nil, "emitted %v CELL in the window %s, above budget_cell %d", cs.Emitted, wr, cfg.BudgetCell)
	}
	if cs.Proofs > cfg.MaxProofsTotal {
		a.rep.add(CodeProofsTotal, sevFail, nil, "%d proofs accepted under the config, above max_proofs_total %d", cs.Proofs, cfg.MaxProofsTotal)
	}
	if v2 {
		a.checkOwners(cs, cfg, hash, window, own)
	}
	return cs
}

// -----------------------------------------------------------------------------
// HL2 per-owner checks (WP-H; version 2 configs)
// -----------------------------------------------------------------------------

// The version 2 (HL2) rules the audit adds for a v2 config, on top of the
// checks every version gets (oracle 1 double pay by proof ID, which also
// covers a proof paid to two owners; I6 by row: every paid ID's recipient
// is its row's miner_addr, CodeRecipientMismatch):
//   - owner epochs (legacymining.OwnerEpochOf, 8640 blocks from height 0):
//     the rewards to one owner in one epoch, sealed under the config, are
//     at most owner_epoch_cap_cell + 2*rewardCell(h) (the cap holds
//     admission only, so accepted proofs may overshoot it by less than two
//     block rewards; legacymining doc.go, WP-D). Core's counter starts at
//     max(epoch start, the window's first height) (S12), so for a config
//     run once this is exactly its counter. A re-activated config's counter
//     also includes the rewards sealed under the configs in between, which
//     only holds admission earlier, but proofs pending at the re-activation
//     boot are still paid; a breach is then a warning, not a failure;
//   - pending: the config's rows that are unpaid on the chain and in the DB
//     are at most max_pending in total and max_pending_per_owner per owner
//     (the Ledger's outstanding counts include them, and both caps are
//     checked under submitMu and again at Enqueue);
//   - a v2 allowlist, when set, is an admission rule: every row accepted
//     under the config is an allowlisted (miner_addr, node_id) pair;
//   - checkOwnerRewards: at most one reward per recipient per block (core
//     S10 for a v2 config).
//
// Not auditable here, and reported as notes: difficulty_bits (the chain
// carries only LMP1 proof IDs, and difficulty is producer-local), and the
// deferred-bond withholding (amounts are gross; the bond accrual happens
// when a node applies the reward tx, and core's caps count gross too).

func (a *auditor) checkOwners(cs *CanarySummary, cfg *legacymining.Config, hash legacymining.ConfigHash, window segment, own []segment) {
	oa := &OwnerAudit{
		ConfigVersion: cfg.Version, OwnerEpochCapCell: cfg.OwnerEpochCapCell, MaxPending: cfg.MaxPending,
		MaxPendingPerOwner: cfg.MaxPendingPerOwner, DifficultyBits: cfg.DifficultyBits,
		DeferredSlotWeightPermille: cfg.DeferredSlotWeightPermille, Owners: []OwnerSummary{},
	}
	cs.HL2 = oa
	oa.Notes = []string{
		fmt.Sprintf("difficulty_bits %d (target 2^%d) is producer-local and not auditable from the chain: reward txs carry only LMP1 proof IDs, and no follower or replay path checks a proof's target", cfg.DifficultyBits, cfg.DifficultyBits),
		"amounts are gross: a reward to an owner with a deferred-bond node below its bond is partly withheld into the bond when the reward tx is applied (AccrueBondFromReward); budget_cell and owner_epoch_cap_cell count the gross amount, as core does",
		"per-owner rate limits and cooldowns are in-memory admission state and leave no record on the chain or in the DB",
	}

	owners := make(map[string]*OwnerSummary)
	get := func(o string) *OwnerSummary {
		s := owners[o]
		if s == nil {
			s = &OwnerSummary{Owner: o}
			owners[o] = s
		}
		return s
	}
	type ownerEpoch struct {
		owner string
		epoch uint64
	}
	epochCell := make(map[ownerEpoch]float64)
	epochLast := make(map[ownerEpoch]uint64) // highest reward height, for rewardCell
	sealedUnder := func(height uint64) bool {
		for _, sg := range own {
			if sg.has(height) {
				return true
			}
		}
		return false
	}
	for _, rw := range a.rewards { // block and tx order, as core sums
		if !window.has(rw.height) {
			continue
		}
		s := get(rw.recipient)
		s.RewardTxs++
		s.Emitted += rw.amount
		if !sealedUnder(rw.height) {
			continue
		}
		k := ownerEpoch{rw.recipient, legacymining.OwnerEpochOf(rw.height)}
		epochCell[k] += rw.amount
		epochLast[k] = max(epochLast[k], rw.height)
	}
	keys := make([]ownerEpoch, 0, len(epochCell))
	for k := range epochCell {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].owner != keys[j].owner {
			return keys[i].owner < keys[j].owner
		}
		return keys[i].epoch < keys[j].epoch
	})
	for _, k := range keys {
		cell := epochCell[k]
		s := owners[k.owner]
		if cell > s.PeakEpochEmitted {
			s.PeakEpoch, s.PeakEpochEmitted = k.epoch, cell
		}
		limit := float64(cfg.OwnerEpochCapCell) + 2*a.cell(epochLast[k])*(1+legacymining.RewardSumSlack)
		if !(cell <= limit) {
			first := k.epoch * legacymining.OwnerEpochBlocks
			sev, why := sevFail, ""
			if len(own) > 1 {
				sev, why = sevWarn, " (re-activated config: proofs pending at a re-activation boot are paid past the cap)"
			}
			a.rep.add(CodeOwnerEpochCap, sev, h(epochLast[k]), "owner %s received %v CELL in owner epoch %d (heights %d..%d) sealed under the config, above owner_epoch_cap_cell %d + 2*rewardCell = %v%s",
				k.owner, cell, k.epoch, first, first+legacymining.OwnerEpochBlocks-1, cfg.OwnerEpochCapCell, limit, why)
		}
	}

	allowed := make(map[legacymining.AllowEntry]bool, len(cfg.Allowed))
	for _, e := range cfg.Allowed {
		allowed[e] = true
	}
	for _, r := range a.db.rows {
		if r.config != hash {
			continue
		}
		s := get(r.minerAddr)
		s.Proofs++
		if _, chainPaid := a.paid[r.id]; !r.paid && !chainPaid {
			s.Pending++
			oa.Pending++
		}
		if len(allowed) > 0 && !allowed[legacymining.AllowEntry{MinerAddr: r.minerAddr, NodeID: r.nodeID}] {
			a.rep.add(CodeNotAllowlisted, sevFail, nil, "proof %x of %s on node %q was accepted under the config, which does not allowlist that pair", r.id[:], r.minerAddr, r.nodeID)
		}
	}
	if oa.Pending > cfg.MaxPending {
		a.rep.add(CodePendingExceeded, sevFail, nil, "%d proofs accepted under the config are unpaid, above max_pending %d", oa.Pending, cfg.MaxPending)
	}

	for _, s := range owners {
		oa.Owners = append(oa.Owners, *s)
	}
	sort.Slice(oa.Owners, func(i, j int) bool {
		x, y := oa.Owners[i], oa.Owners[j]
		if x.Emitted != y.Emitted {
			return x.Emitted > y.Emitted
		}
		return x.Owner < y.Owner
	})
	for _, s := range oa.Owners { // sorted, so the findings are deterministic
		if s.Pending > cfg.MaxPendingPerOwner {
			a.rep.add(CodeOwnerPending, sevFail, nil, "owner %s has %d unpaid proofs accepted under the config, above max_pending_per_owner %d", s.Owner, s.Pending, cfg.MaxPendingPerOwner)
		}
	}
}

// checkOwnerRewards is the version 2 S10 rule: at most one reward per
// recipient in a block, over every reward at or above h0 (as core checks
// with a v2 config). HL1 Take groups the claims by address, so a v1-era
// block passes too.
func (a *auditor) checkOwnerRewards() {
	type key struct {
		height    uint64
		recipient string
	}
	seen := make(map[key]bool, len(a.rewards))
	for _, rw := range a.rewards {
		k := key{rw.height, rw.recipient}
		if seen[k] {
			a.rep.add(CodeOwnerRewardRepeat, sevFail, h(rw.height), "a second reward to %s in one block", rw.recipient)
			continue
		}
		seen[k] = true
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
