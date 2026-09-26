package legacymining

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// The api handler maps errors.As(*mining.RejectError) to 400 and
// errors.Is(api.ErrMiningUnavailable) to 503; miningsvc maps ErrUnavailable to
// the latter. Pin the class of every kind, through a wrapping layer.
func TestRejectionClasses(t *testing.T) {
	cases := []struct {
		kind   RejectKind
		status int
		reason mining.RejectReason
	}{
		{KindAdmissionClosed, http.StatusServiceUnavailable, ""},
		{KindRateLimited, http.StatusServiceUnavailable, ""},
		{KindPendingFull, http.StatusServiceUnavailable, ""},
		{KindUnavailable, http.StatusServiceUnavailable, ""},
		{KindMalformed, http.StatusBadRequest, mining.ReasonNonCanonical},
		{KindMinerNotAllowed, http.StatusBadRequest, mining.ReasonBadAddr},
		{KindNodeNotAllowed, http.StatusBadRequest, mining.ReasonAttestation},
		{KindAttestationType, http.StatusBadRequest, mining.ReasonAttestation},
		{KindDuplicate, http.StatusBadRequest, mining.ReasonDuplicate},
		{KindNonceConflict, http.StatusBadRequest, mining.ReasonAttestation},
		{0, http.StatusServiceUnavailable, ""},
		{250, http.StatusServiceUnavailable, ""},
	}
	labels := map[string]RejectKind{}
	for _, c := range cases {
		err := fmt.Errorf("outer: %w", &Rejection{Kind: c.kind, Detail: "d"})
		if got := c.kind.HTTPStatus(); got != c.status {
			t.Errorf("%v: HTTPStatus = %d, want %d", c.kind, got, c.status)
		}
		if got := RejectKindOf(err); got != c.kind {
			t.Errorf("%v: RejectKindOf = %v", c.kind, got)
		}
		var rej *mining.RejectError
		isRej := errors.As(err, &rej)
		isUnavail := errors.Is(err, ErrUnavailable)
		if c.status == http.StatusBadRequest {
			if !isRej || isUnavail {
				t.Fatalf("%v: As RejectError=%v Is ErrUnavailable=%v, want true/false", c.kind, isRej, isUnavail)
			}
			if rej.Reason != c.reason || rej.Detail != c.kind.String()+": d" {
				t.Errorf("%v: RejectError = %q/%q", c.kind, rej.Reason, rej.Detail)
			}
		} else if isRej || !isUnavail {
			t.Errorf("%v: As RejectError=%v Is ErrUnavailable=%v, want false/true", c.kind, isRej, isUnavail)
		}
		if c.kind >= KindAdmissionClosed && c.kind <= KindNonceConflict {
			label := c.kind.String()
			if label == "invalid" || labels[label] != 0 {
				t.Errorf("%v: label %q invalid or reused", c.kind, label)
			}
			labels[label] = c.kind
		}
	}
	if RejectKindOf(errors.New("plain")) != 0 || RejectKindOf(nil) != 0 {
		t.Error("RejectKindOf of a non-Rejection must be 0")
	}
	if got := (&Rejection{Kind: KindDuplicate}).Unwrap().(*mining.RejectError).Detail; got != "duplicate" {
		t.Errorf("empty-detail RejectError detail = %q", got)
	}
}

func TestStates(t *testing.T) {
	cases := []struct {
		s       State
		name    string
		payouts bool
	}{
		{StateOpen, "OPEN", true},
		{StateAdmissionStopped, "ADMISSION_STOPPED", true},
		{StateFrozen, "FROZEN", false},
		{StateKilled, "KILLED", false},
		{0, "INVALID", false},
	}
	for _, c := range cases {
		if c.s.String() != c.name || c.s.PayoutsEnabled() != c.payouts {
			t.Errorf("state %d: %q payouts=%v, want %q %v", c.s, c.s.String(), c.s.PayoutsEnabled(), c.name, c.payouts)
		}
	}
}

func TestLimitsAndNames(t *testing.T) {
	if MaxPayloadBytes != 32779 || MaxPendingLimit != 1024 {
		t.Errorf("MaxPayloadBytes=%d MaxPendingLimit=%d", MaxPayloadBytes, MaxPendingLimit)
	}
	if ExitFatalRestore != 78 || ExitFailStop != 86 {
		t.Error("exit codes changed")
	}
	if RewardSumSlack*(1<<40) != 1 {
		t.Error("RewardSumSlack != 2^-40")
	}
	want := map[string]string{
		FailStopArmedFile:         "FAILSTOP.armed",
		FailStopFile:              "FAILSTOP.json",
		FailStopCauseFile:         "FAILSTOP.cause.json",
		TrippedArmedFile:          "TRIPPED.armed",
		TrippedFile:               "TRIPPED.json",
		AdmissionStoppedArmedFile: "ADMISSION_STOPPED.armed",
		AdmissionStoppedFile:      "ADMISSION_STOPPED.json",
		AdmissionStoppedCauseFile: "ADMISSION_STOPPED.cause.json",
		WatermarkRetiredPrefix:    "hl1-served-watermark.json.retired-",
	}
	for got, w := range want {
		if got != w {
			t.Errorf("name %q, want %q", got, w)
		}
	}
	if id := fmt.Sprintf(RewardIDFormat, 7, "ab", "0123456789abcdef"); !strings.HasPrefix(id, RewardIDPrefix) || id != "solo-reward-7-ab-0123456789abcdef" {
		t.Errorf("reward ID %q", id)
	}
	if fmt.Sprintf(GenerationLinkFormat, "qsdm_accounts.json", 41) != "qsdm_accounts.json.h41" {
		t.Error("generation link format")
	}
}

func strictDecode(t *testing.T, doc string, v any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(doc))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode %s: %v", doc, err)
	}
}

// The §6.1 and §3.1 documents must decode strictly into the contract types.
func TestJSONShapes(t *testing.T) {
	var c Config
	strictDecode(t, `{"version":1,"allowed":[{"miner_addr":"`+strings.Repeat("a", 64)+`","node_id":"n1"}],
	 "max_proofs_per_min":60,"max_proofs_total":3000,"max_pending":600,
	 "budget_cell":1291,"expires_unix":1790000000}`, &c)
	if c.Version != ConfigVersion || len(c.Allowed) != 1 || c.Allowed[0].NodeID != "n1" || c.MaxProofsPerMin != 60 ||
		c.MaxProofsTotal != 3000 || c.MaxPending != 600 || c.BudgetCell != 1291 || c.ExpiresUnix != 1790000000 {
		t.Errorf("config = %+v", c)
	}

	var w Watermark
	strictDecode(t, `{"version":1,"height":9,"hash":"h9","source":"seed","written_ns":5,"served_tip":8,"follower_height":7,"follower_hash":"h7"}`, &w)
	if w.ServedTip == nil || *w.ServedTip != 8 || w.FollowerHeight == nil || *w.FollowerHeight != 7 || w.FollowerHash != "h7" {
		t.Errorf("seeded watermark = %+v", w)
	}
	b, err := json.Marshal(Watermark{Version: WatermarkVersion, Height: 9, Hash: "h9", Source: WatermarkSourceSeal, WrittenNS: 5})
	if err != nil || !bytes.Equal(b, []byte(`{"version":1,"height":9,"hash":"h9","source":"seal","written_ns":5}`)) {
		t.Errorf("seal watermark = %s, %v", b, err)
	}

	var m ArmedMarker
	strictDecode(t, `{"release":"r","boot_ns":1,"pid":2}`, &m)
	var mc MarkerCause
	strictDecode(t, `{"cause":"marker-io","at_ns":3}`, &mc)
}
