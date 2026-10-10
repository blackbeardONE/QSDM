package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/poe"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// dustPerCell mirrors pkg/chain.DustPerCellInt (1 CELL = 1e8 dust).
const dustPerCell = 100_000_000

// resubmitAfter is how long a submitted-but-unapplied envelope is trusted to
// still be in the node's mempool before a probe resubmits it (a dropped
// envelope is re-admitted; a pending one answers "duplicate").
const resubmitAfter = 60 * time.Second

// busyRetryAfterSeconds is the Retry-After hint for 503 payout_in_flight.
// A validator only admits envelope nonce last_applied+1, so a reward wallet
// can have one transfer pending per block.
const busyRetryAfterSeconds = 10

// cellToDust converts a JSON CELL amount to integer dust. It rejects
// non-finite and non-positive values and anything with more than 8 decimals.
func cellToDust(amount float64) (int64, error) {
	if math.IsNaN(amount) || math.IsInf(amount, 0) || amount <= 0 {
		return 0, errors.New("amount must be a positive number")
	}
	if amount > 1_000_000_000 {
		return 0, errors.New("amount is too large")
	}
	scaled := amount * dustPerCell
	d := math.Round(scaled)
	if math.Abs(scaled-d) > 0.05 {
		return 0, errors.New("amount must have at most 8 decimal places")
	}
	if d < 1 {
		return 0, errors.New("amount is below 1 dust")
	}
	return int64(d), nil
}

// chainFloorDust mirrors pkg/chain.floorToDust: the account store credits
// floor(amount * 1e8) dust for a transfer.
func chainFloorDust(cell float64) int64 {
	if cell <= 0 || math.IsNaN(cell) {
		return 0
	}
	return int64(math.Floor(cell * dustPerCell))
}

// dustToWire returns the float64 CELL amount to put in an envelope so the
// chain's flooring credits exactly d dust. float64(d)/1e8 alone can land one
// ULP low (0.29 * 1e8 = 28999999.999999996), which would credit d-1.
func dustToWire(d int64) (float64, error) {
	if d < 0 {
		return 0, errors.New("negative dust")
	}
	if d == 0 {
		return 0, nil
	}
	x := float64(d) / dustPerCell
	for i := 0; i < 8 && chainFloorDust(x) < d; i++ {
		x = math.Nextafter(x, math.Inf(1))
	}
	if chainFloorDust(x) != d {
		return 0, fmt.Errorf("amount %d dust is not representable on the wire", d)
	}
	return x, nil
}

// ---- node submission ------------------------------------------------------

type submitOutcome int

const (
	outcomeAccepted     submitOutcome = iota // 2xx
	outcomeDuplicate                         // 409 status=duplicate (id pending, or sender nonce pending)
	outcomeNonceReplay                       // 409 envelope nonce <= last applied
	outcomeNonceGap                          // 409 envelope nonce > last applied + 1
	outcomeInsufficient                      // 402
	outcomeStaleParents                      // 422 proof-of-entanglement: parents not committed in the window
	outcomeRejected                          // other 4xx: the node refused the envelope
	outcomeUnknown                           // transport error, 5xx, edge 429: may or may not be held
)

type submitResult struct {
	outcome submitOutcome
	status  int
	body    string
}

func classifySubmit(code int, body []byte, err error) submitResult {
	res := submitResult{status: code, body: truncate(strings.TrimSpace(string(body)), 300)}
	lower := strings.ToLower(string(body))
	switch {
	case err != nil:
		res.outcome = outcomeUnknown
		res.body = truncate(err.Error(), 300)
	case code >= 200 && code < 300:
		res.outcome = outcomeAccepted
	case code == http.StatusConflict && strings.Contains(lower, "duplicate"):
		res.outcome = outcomeDuplicate
	case code == http.StatusConflict && strings.Contains(lower, "nonce replay"):
		res.outcome = outcomeNonceReplay
	case code == http.StatusConflict && strings.Contains(lower, "nonce"):
		// "nonce gap" and the storage-path "nonce conflict" (raced; retry later).
		res.outcome = outcomeNonceGap
	case code == http.StatusPaymentRequired:
		res.outcome = outcomeInsufficient
	case code == http.StatusUnprocessableEntity && strings.Contains(lower, "proof-of-entanglement"):
		res.outcome = outcomeStaleParents
	case code == http.StatusTooManyRequests || code >= 500 || code == 0:
		res.outcome = outcomeUnknown
	case code >= 400:
		res.outcome = outcomeRejected
	default:
		res.outcome = outcomeUnknown
	}
	return res
}

// envelopeFor rebuilds the exact envelope of an attempt. With sign=true it
// signs the canonical bytes and stores the signature on the attempt; with
// sign=false it reuses the journaled signature so a resubmission is
// byte-identical to the original.
func (s *signer) envelopeFor(a *payoutAttempt, sign bool) ([]byte, error) {
	parents := a.ParentCells
	if parents == nil {
		// Attempts journaled before parents existed were signed over [].
		parents = []string{}
	}
	env := txEnvelope{
		ID:          a.TxID,
		Sender:      s.sender,
		Recipient:   a.Recipient,
		Amount:      a.WireAmount,
		Fee:         a.WireFee,
		GeoTag:      a.Purpose,
		ParentCells: parents,
		Nonce:       a.Nonce,
		Timestamp:   a.Timestamp,
	}
	if sign {
		canonical, err := json.Marshal(env)
		if err != nil {
			return nil, fmt.Errorf("marshal canonical: %w", err)
		}
		sig := make([]byte, mldsa87.SignatureSize)
		if err := mldsa87.SignTo(s.sk, canonical, nil, true /*randomized*/, sig); err != nil {
			return nil, fmt.Errorf("sign: %w", err)
		}
		a.Signature = hex.EncodeToString(sig)
	}
	if a.Signature == "" {
		return nil, errors.New("attempt has no signature")
	}
	env.Signature = a.Signature
	env.PublicKey = s.pubHex
	return json.Marshal(env)
}

// freshParents asks the node for the Proof-of-Entanglement parents a new
// signature should cover: its two newest committed transactions. Before the
// network's activation height any parents are accepted, so a node that
// cannot answer (an older release without GET /api/v1/chain/parents or
// /api/v1/receipts) yields none, and the envelope carries [] as it always
// has; after activation such an envelope is refused with 422 and re-signed
// on the next probe.
func (s *signer) freshParents() []string {
	ctx, cancel := context.WithTimeout(context.Background(), s.http.Timeout)
	defer cancel()
	parents, err := poe.FetchParents(ctx, s.http, s.apiURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-game-signer: no proof-of-entanglement parents from the node; signing without:", err)
		return []string{}
	}
	return parents
}

// resignLocked gives a live attempt whose envelope the node refused for
// stale parents a fresh signature over fresh parents, keeping its tx id and
// nonce, and journals it before it is submitted. The caller has established
// that the attempt's nonce is unconsumed.
func (s *signer) resignLocked(a *payoutAttempt) error {
	resigned := *a
	resigned.ParentCells = s.freshParents()
	if _, err := s.envelopeFor(&resigned, true); err != nil {
		return err
	}
	return s.journal.recordResigned(a, resigned.Signature, resigned.ParentCells, s.now())
}

func (s *signer) submitAttempt(a *payoutAttempt, sign bool) (submitResult, error) {
	payload, err := s.envelopeFor(a, sign)
	if err != nil {
		return submitResult{}, err
	}
	body, code, err := s.nodePOST("/api/v1/wallet/submit-signed", payload)
	a.LastSubmit = s.now()
	return classifySubmit(code, body, err), nil
}

// ---- in-flight resolution ---------------------------------------------------

type probeKind int

const (
	probeApplied probeKind = iota // the envelope's nonce is consumed on chain
	probePending                  // the node holds it (or just re-admitted it)
	probeFailed                   // could not confirm; the attempt stays live
)

type probeResult struct {
	kind   probeKind
	status int
	detail string
}

// probeLocked resolves a live attempt without ever signing anything new. The
// reward wallet must be spent by this signer only, so a consumed nonce at or
// above the attempt's nonce means the attempt itself was applied. Never marks
// an attempt rejected: only a fresh envelope's first answer can do that.
func (s *signer) probeLocked(a *payoutAttempt) probeResult {
	last, err := s.lastAppliedNonce()
	if err != nil {
		return probeResult{kind: probeFailed, status: http.StatusBadGateway, detail: err.Error()}
	}
	if a.Nonce <= last {
		if err := s.journal.transition(a, stateApplied, 0, "nonce consumed on chain", s.now()); err != nil {
			return probeResult{kind: probeFailed, status: http.StatusInternalServerError, detail: err.Error()}
		}
		return probeResult{kind: probeApplied}
	}
	if a.State == stateSubmitted && !a.LastSubmit.IsZero() && s.now().Sub(a.LastSubmit) < resubmitAfter {
		return probeResult{kind: probePending}
	}
	res, err := s.submitAttempt(a, false)
	if err != nil {
		return probeResult{kind: probeFailed, status: http.StatusInternalServerError, detail: err.Error()}
	}
	if res.outcome == outcomeStaleParents {
		// The journaled envelope names parents that are no longer committed
		// in the reference window (or none, from before activation). Its
		// nonce is unconsumed (checked above), so nothing at this nonce has
		// been applied: re-sign the same tx id and nonce with fresh parents.
		if err := s.resignLocked(a); err != nil {
			return probeResult{kind: probeFailed, status: http.StatusInternalServerError, detail: err.Error()}
		}
		if res, err = s.submitAttempt(a, false); err != nil {
			return probeResult{kind: probeFailed, status: http.StatusInternalServerError, detail: err.Error()}
		}
	}
	switch res.outcome {
	case outcomeAccepted, outcomeDuplicate:
		if err := s.journal.transition(a, stateSubmitted, res.status, "", s.now()); err != nil {
			return probeResult{kind: probeFailed, status: http.StatusInternalServerError, detail: err.Error()}
		}
		return probeResult{kind: probePending}
	case outcomeNonceReplay:
		if err := s.journal.transition(a, stateApplied, res.status, "nonce consumed on chain", s.now()); err != nil {
			return probeResult{kind: probeFailed, status: http.StatusInternalServerError, detail: err.Error()}
		}
		return probeResult{kind: probeApplied}
	case outcomeUnknown:
		_ = s.journal.transition(a, stateUnknown, res.status, res.body, s.now())
		return probeResult{kind: probeFailed, status: http.StatusBadGateway, detail: "node unavailable: " + res.body}
	default:
		// nonce gap, insufficient balance or a refusal for an envelope that
		// may already be in a block: an operator must look before anything
		// else is signed from this wallet.
		return probeResult{kind: probeFailed, status: http.StatusConflict,
			detail: fmt.Sprintf("in-flight transfer %s (nonce %d) needs review: HTTP %d %s", a.TxID, a.Nonce, res.status, res.body)}
	}
}

// resolveLiveLocked resolves every live attempt. It reports busy while one is
// still pending: a validator admits only nonce last_applied+1, so the next
// payout must wait for the block that applies the current one.
func (s *signer) resolveLiveLocked() (busy bool, failure *probeResult) {
	for _, a := range s.journal.liveAttempts() {
		res := s.probeLocked(a)
		switch res.kind {
		case probeApplied:
			continue
		case probePending:
			return true, nil
		default:
			return false, &res
		}
	}
	return false, nil
}

// ---- pay --------------------------------------------------------------------

type payError struct {
	status     int
	code       string
	msg        string
	retryAfter int
}

func (s *signer) payResponseFor(a *payoutAttempt, duplicate bool) payResponse {
	return payResponse{
		TransactionID: a.TxID,
		Nonce:         a.Nonce,
		Sender:        s.sender,
		Recipient:     a.Recipient,
		Amount:        float64(a.AmountDust) / dustPerCell,
		Duplicate:     duplicate,
		Status:        a.State,
	}
}

// payLocked runs one validated payout request. Callers hold s.mu.
func (s *signer) payLocked(req payRequest, amountDust int64) (payResponse, *payError) {
	now := s.now()

	// 1. A known request: never sign again unless the last envelope was rejected.
	if a := s.journal.byRequest[req.RequestID]; a != nil && a.State != stateRejected {
		if !a.sameRequest(req.Purpose, req.Recipient, amountDust) {
			s.journal.refused(req.RequestID, req.Purpose, req.Recipient, amountDust, "request_id reused with different parameters", now)
			return payResponse{}, &payError{status: http.StatusConflict, code: "request_conflict",
				msg: "request_id was already used for a different recipient, amount or purpose"}
		}
		if a.State == stateApplied {
			return s.payResponseFor(a, true), nil
		}
		res := s.probeLocked(a)
		switch res.kind {
		case probeApplied, probePending:
			return s.payResponseFor(a, true), nil
		default:
			code := "outcome_unknown"
			if res.status == http.StatusConflict {
				code = "needs_review"
			}
			return payResponse{}, &payError{status: res.status, code: code, msg: res.detail}
		}
	}

	// 2. One transfer in flight per reward wallet.
	busy, failure := s.resolveLiveLocked()
	if failure != nil {
		code := "in_flight_unresolved"
		if failure.status == http.StatusConflict {
			code = "needs_review"
		}
		return payResponse{}, &payError{status: failure.status, code: code, msg: failure.detail}
	}
	if busy {
		return payResponse{}, &payError{status: http.StatusServiceUnavailable, code: "payout_in_flight",
			msg: "a previous payout is still pending; retry after the next block", retryAfter: busyRetryAfterSeconds}
	}

	// 3. Rolling 24h caps, counted from the journal (survive restarts).
	total, toRecipient := s.journal.windowSpend(req.Recipient, now)
	if total+amountDust+s.feeDust > s.dailyCapDust {
		s.journal.refused(req.RequestID, req.Purpose, req.Recipient, amountDust, "daily_cap_exceeded", now)
		return payResponse{}, &payError{status: http.StatusTooManyRequests, code: "daily_cap_exceeded",
			msg: "payout would exceed the signer's rolling 24h cap"}
	}
	if toRecipient+amountDust > s.recipientCapDust {
		s.journal.refused(req.RequestID, req.Purpose, req.Recipient, amountDust, "recipient_daily_cap_exceeded", now)
		return payResponse{}, &payError{status: http.StatusTooManyRequests, code: "recipient_daily_cap_exceeded",
			msg: "payout would exceed the signer's rolling 24h cap for this recipient"}
	}

	// 4. Reserve floor.
	balance, err := s.balance()
	if err != nil {
		return payResponse{}, &payError{status: http.StatusBadGateway, code: "node_error", msg: err.Error()}
	}
	// The node reports a float mirror of an integer dust balance.
	balanceDust := int64(math.Round(balance * dustPerCell))
	if balanceDust-amountDust-s.feeDust < s.reserveDust {
		s.journal.refused(req.RequestID, req.Purpose, req.Recipient, amountDust, "reserve_policy", now)
		return payResponse{}, &payError{status: http.StatusPaymentRequired, code: "reserve_policy",
			msg: "treasury reserve policy blocks this payout"}
	}

	// 5. Sign at the node's next nonce, journal, then submit.
	next, err := s.nextNonce()
	if err != nil {
		return payResponse{}, &payError{status: http.StatusBadGateway, code: "node_error", msg: err.Error()}
	}
	wireAmount, err := dustToWire(amountDust)
	if err != nil {
		return payResponse{}, &payError{status: http.StatusBadRequest, code: "invalid_amount", msg: err.Error()}
	}
	a := &payoutAttempt{
		RequestID:   req.RequestID,
		Purpose:     req.Purpose,
		Recipient:   req.Recipient,
		AmountDust:  amountDust,
		FeeDust:     s.feeDust,
		WireAmount:  wireAmount,
		WireFee:     s.wireFee,
		TxID:        deriveRequestID(s.sender, req.Purpose, req.RequestID),
		Nonce:       next,
		Timestamp:   now.UTC().Format(time.RFC3339),
		ParentCells: s.freshParents(),
		State:       stateSigned,
		SignedAt:    now,
	}
	if _, err := s.envelopeFor(a, true); err != nil {
		return payResponse{}, &payError{status: http.StatusInternalServerError, code: "sign_error", msg: err.Error()}
	}
	if err := s.journal.recordSigned(a, now); err != nil {
		return payResponse{}, &payError{status: http.StatusInternalServerError, code: "journal_error", msg: err.Error()}
	}
	a = s.journal.byRequest[req.RequestID]
	res, err := s.submitAttempt(a, false)
	if err != nil {
		return payResponse{}, &payError{status: http.StatusInternalServerError, code: "sign_error", msg: err.Error()}
	}
	switch res.outcome {
	case outcomeAccepted:
		if err := s.journal.transition(a, stateSubmitted, res.status, "", s.now()); err != nil {
			return payResponse{}, &payError{status: http.StatusInternalServerError, code: "journal_error", msg: err.Error()}
		}
		s.nonce = next + 1
		return s.payResponseFor(a, false), nil
	case outcomeDuplicate, outcomeNonceReplay, outcomeNonceGap:
		// A fresh envelope cannot be a duplicate of itself: the node refused
		// it because another envelope holds this nonce. It was not admitted.
		_ = s.journal.transition(a, stateRejected, res.status, "nonce busy: "+res.body, s.now())
		return payResponse{}, &payError{status: http.StatusServiceUnavailable, code: "payout_in_flight",
			msg: "the node already has a pending transfer at this nonce; retry after the next block", retryAfter: busyRetryAfterSeconds}
	case outcomeInsufficient:
		_ = s.journal.transition(a, stateRejected, res.status, res.body, s.now())
		return payResponse{}, &payError{status: http.StatusPaymentRequired, code: "insufficient_balance", msg: res.body}
	case outcomeRejected, outcomeStaleParents:
		_ = s.journal.transition(a, stateRejected, res.status, res.body, s.now())
		return payResponse{}, &payError{status: http.StatusBadGateway, code: "node_rejected",
			msg: fmt.Sprintf("node refused the transfer: HTTP %d %s", res.status, res.body)}
	default:
		_ = s.journal.transition(a, stateUnknown, res.status, res.body, s.now())
		return payResponse{}, &payError{status: http.StatusBadGateway, code: "outcome_unknown",
			msg: "submission outcome unknown; retry with the same request_id: " + res.body}
	}
}
