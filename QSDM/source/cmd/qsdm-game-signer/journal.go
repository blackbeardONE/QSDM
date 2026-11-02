package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"
)

// The payout journal is the signer's durable memory. It is an append-only
// JSON-lines file, fsynced after every record, and it serves three purposes:
//
//  1. Idempotency that survives inclusion and restarts. A request_id maps to at
//     most one live signed envelope (one tx id, one nonce). A retry resubmits
//     that identical envelope instead of signing a new one. The node only
//     de-duplicates tx ids while they sit in its mempool, and a validator
//     happily applies a second transfer with an already-used id at the next
//     nonce, so a signer that re-signs on retry can pay twice.
//  2. Spending caps that survive restarts (rolling 24h total and per
//     recipient). In-memory counters would reset on every restart.
//  3. An audit log of every payout decision, including policy refusals.
//
// Attempt states:
//
//	signed     envelope built and signed, about to be submitted
//	submitted  the node accepted it (or reported this exact envelope pending)
//	unknown    transport error or 5xx: the node may or may not hold it
//	applied    its nonce is consumed on chain
//	rejected   the node definitively refused it; it can never apply
//
// Every state except rejected counts toward the caps. Records are written
// BEFORE the corresponding network action, so a crash can only leave an
// attempt in a state that is resolved by resubmitting the same envelope.
//
// A "resigned" record replaces the signature and Proof-of-Entanglement
// parents of the latest attempt while keeping its tx id and nonce. It is
// written only when the node refused the journaled envelope because its
// parents are no longer committed inside the reference window and the
// attempt's nonce is still unconsumed: no envelope at that nonce has been
// applied, and at most one envelope per nonce ever can be, so re-signing
// cannot pay twice. Signed records written before parents existed carry
// none and are rebuilt with an empty parent list, byte for byte.
const (
	journalVersion = 1

	stateSigned    = "signed"
	stateSubmitted = "submitted"
	stateUnknown   = "unknown"
	stateApplied   = "applied"
	stateRejected  = "rejected"

	// eventRefused is an audit-only record of a policy refusal (caps, reserve,
	// purpose). It never changes attempt state.
	eventRefused = "refused"
	// eventResigned replaces the signature and parents of the latest attempt
	// (same tx id and nonce); the attempt becomes signed again.
	eventResigned = "resigned"

	capWindow = 24 * time.Hour
)

// journalRecord is one line of the journal. Fields are a superset; each event
// uses the subset it needs.
type journalRecord struct {
	V          int     `json:"v"`
	At         string  `json:"at"`
	Event      string  `json:"event"`
	RequestID  string  `json:"request_id,omitempty"`
	Purpose    string  `json:"purpose,omitempty"`
	Recipient  string  `json:"recipient,omitempty"`
	AmountDust int64   `json:"amount_dust,omitempty"`
	FeeDust    int64   `json:"fee_dust,omitempty"`
	WireAmount float64 `json:"wire_amount,omitempty"`
	WireFee    float64 `json:"wire_fee,omitempty"`
	TxID       string  `json:"tx_id,omitempty"`
	Nonce      uint64  `json:"nonce,omitempty"`
	Timestamp  string  `json:"timestamp,omitempty"`
	Signature  string  `json:"signature,omitempty"`
	HTTPStatus int     `json:"http_status,omitempty"`
	Reason     string  `json:"reason,omitempty"`
	// ParentCells are the signed Proof-of-Entanglement parents (signed and
	// resigned records). Absent on records written before parents existed.
	ParentCells []string `json:"parent_cells,omitempty"`
}

// payoutAttempt is the latest signed envelope for one request_id.
type payoutAttempt struct {
	RequestID  string
	Purpose    string
	Recipient  string
	AmountDust int64
	FeeDust    int64
	WireAmount float64
	WireFee    float64
	TxID       string
	Nonce      uint64
	Timestamp  string
	Signature  string
	// ParentCells are the parents the signature covers; nil for attempts
	// signed before parents were sent (their envelopes carry []).
	ParentCells []string
	State       string
	SignedAt    time.Time
	// LastSubmit is in-memory only: when this process last sent the envelope.
	// A zero value (e.g. after a restart) makes the next probe resubmit.
	LastSubmit time.Time
}

func (a *payoutAttempt) counts() bool { return a != nil && a.State != stateRejected }

func (a *payoutAttempt) live() bool {
	return a != nil && (a.State == stateSigned || a.State == stateSubmitted || a.State == stateUnknown)
}

func (a *payoutAttempt) sameRequest(purpose, recipient string, amountDust int64) bool {
	return a.Purpose == purpose && a.Recipient == recipient && a.AmountDust == amountDust
}

type payoutJournal struct {
	path      string
	f         *os.File
	byRequest map[string]*payoutAttempt
}

// openJournal replays an existing journal (creating it if missing) and opens
// it for appending. A torn final line from a crash mid-write is truncated: the
// envelope it described was never submitted, because submission happens only
// after the record is durably written. Any other malformed line is fatal so an
// operator inspects the file instead of the signer silently forgetting payouts.
func openJournal(path string) (*payoutJournal, error) {
	if path == "" {
		return nil, errors.New("journal path is required")
	}
	j := &payoutJournal{path: path, byRequest: map[string]*payoutAttempt{}}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		cut := bytes.LastIndexByte(data, '\n') + 1
		fmt.Fprintf(os.Stderr, "qsdm-game-signer: journal %s ends with a torn record (%d bytes); truncating it\n",
			path, len(data)-cut)
		if err := os.Truncate(path, int64(cut)); err != nil {
			return nil, fmt.Errorf("truncate torn journal tail: %w", err)
		}
		data = data[:cut]
	}
	if err := j.replay(bytes.NewReader(data)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open journal: %w", err)
	}
	j.f = f
	return j, nil
}

func (j *payoutJournal) replay(r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	line := 0
	for sc.Scan() {
		line++
		raw := bytes.TrimSpace(sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		var rec journalRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			return fmt.Errorf("journal %s line %d: %w", j.path, line, err)
		}
		if rec.V != journalVersion {
			return fmt.Errorf("journal %s line %d: unsupported version %d", j.path, line, rec.V)
		}
		if err := j.applyRecord(rec); err != nil {
			return fmt.Errorf("journal %s line %d: %w", j.path, line, err)
		}
	}
	return sc.Err()
}

func (j *payoutJournal) applyRecord(rec journalRecord) error {
	switch rec.Event {
	case eventRefused:
		return nil
	case stateSigned:
		at, err := time.Parse(time.RFC3339Nano, rec.At)
		if err != nil {
			return fmt.Errorf("signed record time: %w", err)
		}
		if rec.RequestID == "" || rec.TxID == "" || rec.Signature == "" {
			return errors.New("signed record is incomplete")
		}
		j.byRequest[rec.RequestID] = &payoutAttempt{
			RequestID: rec.RequestID, Purpose: rec.Purpose, Recipient: rec.Recipient,
			AmountDust: rec.AmountDust, FeeDust: rec.FeeDust,
			WireAmount: rec.WireAmount, WireFee: rec.WireFee,
			TxID: rec.TxID, Nonce: rec.Nonce, Timestamp: rec.Timestamp,
			Signature: rec.Signature, ParentCells: rec.ParentCells, State: stateSigned, SignedAt: at,
		}
		return nil
	case eventResigned:
		a := j.byRequest[rec.RequestID]
		if a == nil || a.TxID != rec.TxID || a.Nonce != rec.Nonce || rec.Signature == "" {
			return fmt.Errorf("resigned record does not match the latest signed attempt for %q", rec.RequestID)
		}
		if !a.live() {
			return fmt.Errorf("resigned record for %q whose attempt is %s", rec.RequestID, a.State)
		}
		a.Signature = rec.Signature
		a.ParentCells = rec.ParentCells
		a.State = stateSigned
		return nil
	case stateSubmitted, stateUnknown, stateApplied, stateRejected:
		a := j.byRequest[rec.RequestID]
		if a == nil || a.TxID != rec.TxID || a.Nonce != rec.Nonce {
			return fmt.Errorf("%s record does not match the latest signed attempt for %q", rec.Event, rec.RequestID)
		}
		a.State = rec.Event
		return nil
	default:
		return fmt.Errorf("unknown journal event %q", rec.Event)
	}
}

// append durably writes one record and applies it to the in-memory state.
func (j *payoutJournal) append(rec journalRecord, now time.Time) error {
	rec.V = journalVersion
	rec.At = now.UTC().Format(time.RFC3339Nano)
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encode journal record: %w", err)
	}
	line = append(line, '\n')
	if _, err := j.f.Write(line); err != nil {
		return fmt.Errorf("write journal: %w", err)
	}
	if err := j.f.Sync(); err != nil {
		return fmt.Errorf("sync journal: %w", err)
	}
	return j.applyRecord(rec)
}

// recordSigned journals a freshly signed envelope (before it is submitted).
// A request may only get a new envelope when its previous one was rejected.
func (j *payoutJournal) recordSigned(a *payoutAttempt, now time.Time) error {
	if prev := j.byRequest[a.RequestID]; prev != nil && prev.State != stateRejected {
		return fmt.Errorf("request %q already has a %s envelope; refusing to sign another", a.RequestID, prev.State)
	}
	return j.append(journalRecord{
		Event: stateSigned, RequestID: a.RequestID, Purpose: a.Purpose, Recipient: a.Recipient,
		AmountDust: a.AmountDust, FeeDust: a.FeeDust, WireAmount: a.WireAmount, WireFee: a.WireFee,
		TxID: a.TxID, Nonce: a.Nonce, Timestamp: a.Timestamp, Signature: a.Signature,
		ParentCells: a.ParentCells,
	}, now)
}

// recordResigned journals a new signature and parents for the live attempt
// of a request, keeping its tx id and nonce (before it is submitted).
func (j *payoutJournal) recordResigned(a *payoutAttempt, signature string, parents []string, now time.Time) error {
	return j.append(journalRecord{
		Event: eventResigned, RequestID: a.RequestID, TxID: a.TxID, Nonce: a.Nonce,
		Signature: signature, ParentCells: parents,
	}, now)
}

// transition journals a state change of the latest attempt for a request.
func (j *payoutJournal) transition(a *payoutAttempt, state string, httpStatus int, reason string, now time.Time) error {
	if a.State == state {
		return nil
	}
	return j.append(journalRecord{
		Event: state, RequestID: a.RequestID, TxID: a.TxID, Nonce: a.Nonce,
		HTTPStatus: httpStatus, Reason: truncate(reason, 300),
	}, now)
}

// refused writes an audit-only record of a policy refusal.
func (j *payoutJournal) refused(requestID, purpose, recipient string, amountDust int64, reason string, now time.Time) {
	if err := j.append(journalRecord{
		Event: eventRefused, RequestID: truncate(requestID, 128), Purpose: purpose,
		Recipient: recipient, AmountDust: amountDust, Reason: truncate(reason, 300),
	}, now); err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-game-signer: audit journal write failed:", err)
	}
}

// windowSpend sums the cap-relevant spend of attempts signed within the
// rolling window ending at now: total = amount+fee across all recipients, and
// amount for the given recipient.
func (j *payoutJournal) windowSpend(recipient string, now time.Time) (total, toRecipient int64) {
	from := now.Add(-capWindow)
	for _, a := range j.byRequest {
		if !a.counts() || !a.SignedAt.After(from) {
			continue
		}
		total += a.AmountDust + a.FeeDust
		if a.Recipient == recipient {
			toRecipient += a.AmountDust
		}
	}
	return total, toRecipient
}

// liveAttempts returns attempts that may still be pending at the node, in
// nonce order.
func (j *payoutJournal) liveAttempts() []*payoutAttempt {
	var out []*payoutAttempt
	for _, a := range j.byRequest {
		if a.live() {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Nonce < out[k].Nonce })
	return out
}

func (j *payoutJournal) close() error {
	if j == nil || j.f == nil {
		return nil
	}
	return j.f.Close()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
