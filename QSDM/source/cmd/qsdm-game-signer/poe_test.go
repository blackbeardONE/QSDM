package main

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// A new payout signs the node's two newest committed transactions as its
// Proof-of-Entanglement parents.
func TestPay_SignsCommittedParents(t *testing.T) {
	h := newHarness(t, 5, 20, 100)
	h.node.mu.Lock()
	h.node.poeWindow = 10
	want := h.node.parentsLocked()
	h.node.mu.Unlock()

	got := h.pay("poe-1:alice", addr(1), 1)
	mustStatus(t, got, http.StatusOK, "")
	h.node.mu.Lock()
	sub := h.node.submissions[len(h.node.submissions)-1]
	h.node.mu.Unlock()
	if len(sub.ParentCells) != 2 || sub.ParentCells[0] != want[0] || sub.ParentCells[1] != want[1] {
		t.Fatalf("parent_cells = %v, want %v", sub.ParentCells, want)
	}
	if err := verifyEnvelope(sub); err != nil {
		t.Fatalf("signature does not cover the parents: %v", err)
	}
	h.node.produceBlock()
	if _, _, applied := h.node.counts(); applied != 1 {
		t.Fatalf("applied = %d", applied)
	}
}

// A journaled envelope whose parents fell out of the PoE window (here: the
// node lost its mempool and blocks moved on while the signer was away) is
// refused with 422. Its nonce is unconsumed, so the signer re-signs the SAME
// tx id and nonce over fresh parents, journals that first, and the payout is
// applied exactly once -- also across a restart.
func TestPay_ResignsStaleParentsKeepingIDAndNonce(t *testing.T) {
	h := newHarness(t, 5, 20, 100)
	h.node.mu.Lock()
	h.node.poeWindow = 3
	h.node.mu.Unlock()

	first := h.pay("poe-2:bob", addr(2), 1)
	mustStatus(t, first, http.StatusOK, "")
	h.node.mu.Lock()
	original := h.node.submissions[len(h.node.submissions)-1]
	h.node.mempool = nil // e.g. a node restart evicted it
	for i := 0; i < 5; i++ {
		h.node.commitHeartbeatLocked()
	}
	h.node.mu.Unlock()

	h.start() // the signer restarts too: the attempt comes back from the journal
	h.clock.Advance(2 * resubmitAfter)
	retry := h.pay("poe-2:bob", addr(2), 1)
	mustStatus(t, retry, http.StatusOK, "")
	if !retry.ok.Duplicate || retry.ok.TransactionID != first.ok.TransactionID || retry.ok.Nonce != first.ok.Nonce {
		t.Fatalf("retry: %+v (first %+v)", retry.ok, first.ok)
	}

	h.node.mu.Lock()
	var resent []txEnvelope
	for _, s := range h.node.submissions {
		if s.ID == original.ID {
			resent = append(resent, s)
		}
	}
	fresh := h.node.parentsLocked()
	h.node.mu.Unlock()
	if len(resent) != 3 {
		t.Fatalf("submissions of the payout = %d, want 3 (original, stale resubmission, re-signed)", len(resent))
	}
	if resent[1].Signature != original.Signature {
		t.Fatal("the first resubmission must be byte-identical to the journaled envelope")
	}
	last := resent[2]
	if last.Nonce != original.Nonce || last.Signature == original.Signature ||
		len(last.ParentCells) != 2 || last.ParentCells[0] != fresh[0] {
		t.Fatalf("re-signed envelope: nonce %d parents %v", last.Nonce, last.ParentCells)
	}
	if err := verifyEnvelope(last); err != nil {
		t.Fatalf("re-signed envelope does not verify: %v", err)
	}

	// The re-signature is durable: after another restart a probe resubmits
	// the re-signed envelope byte for byte, not the stale one.
	raw, err := os.ReadFile(h.journal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"event":"resigned"`) {
		t.Fatalf("journal lacks the resigned record:\n%s", raw)
	}
	h.start()
	if a := h.s.journal.byRequest["poe-2:bob"]; a == nil || a.Signature != last.Signature {
		t.Fatal("journal replay did not restore the re-signed envelope")
	}

	h.node.produceBlock()
	if _, _, applied := h.node.counts(); applied != 1 {
		t.Fatalf("applied = %d, want exactly one transfer", applied)
	}
	again := h.pay("poe-2:bob", addr(2), 1)
	mustStatus(t, again, http.StatusOK, "")
	if again.ok.Status != stateApplied || again.ok.TransactionID != first.ok.TransactionID {
		t.Fatalf("after inclusion: %+v", again.ok)
	}
	if got := h.node.balances[addr(2)]; got != 100_000_000 {
		t.Fatalf("recipient balance %d dust, want exactly one payout", got)
	}
}

// Attempts journaled before parents existed (no parent_cells in the signed
// record) were signed over []; they must still resubmit byte for byte.
func TestJournal_LegacySignedRecordWithoutParentsResubmitsUnchanged(t *testing.T) {
	h := newHarness(t, 5, 20, 100)
	first := h.pay("legacy:carol", addr(3), 1)
	mustStatus(t, first, http.StatusOK, "")
	a := h.s.journal.byRequest["legacy:carol"]

	// Rewrite the journal as the pre-PoE signer wrote it: same envelope,
	// signed over an empty parent list, no parent_cells in the record.
	legacy := *a
	legacy.ParentCells = nil
	if _, err := h.s.envelopeFor(&legacy, true); err != nil {
		t.Fatal(err)
	}
	rec := journalRecord{V: journalVersion, At: h.clock.Now().UTC().Format(time.RFC3339Nano), Event: stateSigned,
		RequestID: a.RequestID, Purpose: a.Purpose, Recipient: a.Recipient, AmountDust: a.AmountDust,
		FeeDust: a.FeeDust, WireAmount: a.WireAmount, WireFee: a.WireFee, TxID: a.TxID, Nonce: a.Nonce,
		Timestamp: a.Timestamp, Signature: legacy.Signature}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(line), "parent_cells") {
		t.Fatalf("legacy record must not carry parent_cells: %s", line)
	}
	_ = h.s.journal.close()
	if err := os.WriteFile(h.journal, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	h.node.mu.Lock()
	h.node.mempool = nil
	h.node.mu.Unlock()
	h.srv.Close()
	h.srv = nil
	h.start()
	h.clock.Advance(2 * resubmitAfter)

	again := h.pay("legacy:carol", addr(3), 1)
	mustStatus(t, again, http.StatusOK, "")
	h.node.mu.Lock()
	sub := h.node.submissions[len(h.node.submissions)-1]
	h.node.mu.Unlock()
	if sub.Signature != legacy.Signature || sub.ParentCells == nil || len(sub.ParentCells) != 0 {
		t.Fatalf("legacy resubmission changed: parents %v", sub.ParentCells)
	}
	if err := verifyEnvelope(sub); err != nil {
		t.Fatalf("legacy resubmission does not verify: %v", err)
	}
	encoded, _ := json.Marshal(sub)
	if !strings.Contains(string(encoded), `"parent_cells":[]`) {
		t.Fatalf("legacy envelope must carry an empty array: %s", encoded)
	}
}

// classifySubmit recognises the node's PoE refusal.
func TestClassifySubmit_StaleParents(t *testing.T) {
	body := []byte(`{"error":"proof-of-entanglement: proof-of-entanglement violation: parent is not a committed transaction in the reference window: parent 0 \"x\""}`)
	if got := classifySubmit(http.StatusUnprocessableEntity, body, nil).outcome; got != outcomeStaleParents {
		t.Fatalf("outcome = %v, want outcomeStaleParents", got)
	}
	if got := classifySubmit(http.StatusUnprocessableEntity, []byte(`{"error":"signature does not verify under envelope.public_key"}`), nil).outcome; got != outcomeRejected {
		t.Fatalf("bad signature outcome = %v, want outcomeRejected", got)
	}
}
