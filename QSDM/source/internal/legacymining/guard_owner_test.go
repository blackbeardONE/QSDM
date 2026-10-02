package legacymining

// HL2 WP-B: the version 2 (N-miner) guard. Per-submission enrollment lookup,
// per-owner buckets scaled by bonded slots, per-owner non-latching cooldowns,
// and the HL1 global submitter triggers demoted to alarms. The HL1 (v1)
// guard tests in guard_test.go are unchanged.

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

var (
	otHonest = strings.Repeat("11", 32)
	otFlood  = strings.Repeat("22", 32)
	otDup    = strings.Repeat("33", 32)
	otVictim = strings.Repeat("44", 32)
	otForger = strings.Repeat("55", 32)
)

// otView is an in-memory EnrollmentView.
type otView struct {
	mu   sync.Mutex
	recs map[string]EnrollmentInfo
}

func newOTView() *otView { return &otView{recs: make(map[string]EnrollmentInfo)} }

func (v *otView) add(node, owner string, bonded bool) *otView {
	v.mu.Lock()
	defer v.mu.Unlock()
	e := EnrollmentInfo{NodeID: node, Owner: owner, Active: true, FullyBonded: bonded, RequiredDust: 10}
	if bonded {
		e.StakeDust = 10
	}
	v.recs[node] = e
	return v
}

func (v *otView) revoke(node string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e := v.recs[node]
	e.Active = false
	v.recs[node] = e
}

func (v *otView) Lookup(node string) (EnrollmentInfo, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.recs[node]
	return e, ok
}

func (v *otView) OwnerNodes(owner string) []EnrollmentInfo {
	v.mu.Lock()
	defer v.mu.Unlock()
	var out []EnrollmentInfo
	for _, e := range v.recs {
		if e.Owner == owner {
			out = append(out, e)
		}
	}
	return out
}

// otPublicConfig is a public-mode v2 config with no allowlist: 10 proofs per
// minute per bonded slot, at most 4 slots per owner.
func otPublicConfig() Config {
	return Config{
		Version:                 ConfigVersion2,
		MaxProofsPerMin:         600,
		MaxProofsPerMinPerOwner: 10,
		MaxProofsTotal:          72000,
		MaxPending:              600,
		MaxPendingPerOwner:      50,
		OwnerEpochCapCell:       1000,
		DifficultyBits:          16,
		RequireOperatorSig:      true,
		RequireFullyBonded:      true,
		BondedSlotCap:           4,
		BudgetCell:              30808,
		ExpiresUnix:             gtStart.Unix() + 86400,
	}
}

// otAcceptAll is the pre-WP-C OwnerAuth: every bundle is attributed to the
// owner of the node it names.
func otAcceptAll(*mining.Proof, string, string) error { return nil }

type otOptions struct {
	mode   Mode
	policy SlotPolicy
	auth   OwnerAuthFunc
}

// otNew builds a v2 guard on dir, as one boot.
func otNew(t *testing.T, dir string, cfg Config, view EnrollmentView, o otOptions) *gtEnv {
	t.Helper()
	if o.mode == ModeOff {
		o.mode = ModePublic
	}
	if o.auth == nil {
		o.auth = otAcceptAll
	}
	e := &gtEnv{dir: dir, clock: &gtClock{t: gtStart}, store: &gtStore{}}
	g, err := NewGuard(GuardOptions{
		Dir: dir, Config: cfg, ConfigHash: sha256.Sum256([]byte("v2cfg")), Release: "hl2-test",
		Store: e.store, FailStop: e.failStop, Now: e.clock.Now, Logf: t.Logf,
		Mode: o.mode, Enrollments: view, SlotPolicy: o.policy, OwnerAuth: o.auth,
	})
	if err != nil {
		t.Fatalf("NewGuard(v2): %v", err)
	}
	e.g = g
	return e
}

// submit runs §4.1 steps 3-5 for a fresh proof by miner for node and returns
// the candidate and the first error.
func (e *gtEnv) submit(t *testing.T, miner, node string) (Candidate, error) {
	t.Helper()
	raw, _, _ := gtProof(t, e.clock.Now(), miner, node, mining.AttestationTypeHMAC)
	return e.submitRaw(raw)
}

func (e *gtEnv) submitRaw(raw []byte) (Candidate, error) {
	if err := e.g.Admit(); err != nil {
		return Candidate{}, err
	}
	c, err := e.g.Precheck(raw)
	if err != nil {
		return Candidate{}, err
	}
	return c, e.g.TakeOwnerRate(c)
}

// tokens returns owner's bucket level, or -1 if the owner has no state.
func (e *gtEnv) tokens(owner string) float64 {
	e.g.cmu.Lock()
	defer e.g.cmu.Unlock()
	if o := e.g.owners[owner]; o != nil {
		return o.tokens
	}
	return -1
}

func (e *gtEnv) cooling(owner string) bool {
	now := e.g.mono(e.clock.Now())
	e.g.cmu.Lock()
	defer e.g.cmu.Unlock()
	o := e.g.owners[owner]
	return o != nil && now < o.coolUntil
}

// requireNoLatch asserts the guard is OPEN with no latch marker, no
// fail-stop and admission open.
func (e *gtEnv) requireNoLatch(t *testing.T) {
	t.Helper()
	if s := e.g.State(); s != StateOpen {
		t.Fatalf("state %v, want OPEN", s)
	}
	for _, f := range []string{AdmissionStoppedFile, TrippedFile} {
		if gtExists(t, filepath.Join(e.dir, f)) {
			t.Fatalf("%s exists", f)
		}
	}
	if !e.g.AdmissionOpen() {
		t.Fatalf("admission closed: %s", e.g.closedReason(e.clock.Now()))
	}
	if fs := e.failStops(); len(fs) != 0 {
		t.Fatalf("fail-stops: %v", fs)
	}
}

func wantKind(t *testing.T, name string, err error, kind RejectKind) {
	t.Helper()
	if RejectKindOf(err) != kind {
		t.Fatalf("%s: %v (kind %v), want %v", name, err, RejectKindOf(err), kind)
	}
}

// The design's acceptance scenario (HL2 §4 row B): three owners misbehave.
// One floods, one sends duplicates, one forges for a victim's node. Admission
// stays open for the others, nothing latches, and each misbehaving owner ends
// in its own cooldown.
func TestV2MisbehavingOwnersAreIsolated(t *testing.T) {
	view := newOTView().
		add("honest-1", otHonest, true).
		add("flood-1", otFlood, true).
		add("dup-1", otDup, true).add("dup-2", otDup, true).
		add("victim-1", otVictim, true).
		add("forger-1", otForger, true)
	e := otNew(t, gtDir(t), otPublicConfig(), view, otOptions{})
	e.open(t)

	honestOK := func(n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if _, err := e.submit(t, otHonest, "honest-1"); err != nil {
				t.Fatalf("honest submission %d: %v", i, err)
			}
		}
	}

	// 1. The flooder sends 100 per minute against a bucket of 10. It is
	// rate-limited (503) at once, and after RateBurstMinutes consecutive
	// minutes above 2x its rate it is in a cooldown. The honest owner keeps
	// mining every minute.
	kinds := map[RejectKind]int{}
	for m := 0; m < RateBurstMinutes+1; m++ {
		for i := 0; i < 100; i++ {
			_, err := e.submit(t, otFlood, "flood-1")
			kinds[RejectKindOf(err)]++
		}
		honestOK(5)
		e.requireNoLatch(t)
		e.tick(time.Minute)
	}
	if kinds[0] < 10 || kinds[KindOwnerRateLimited] == 0 || kinds[KindOwnerCooldown] == 0 {
		t.Fatalf("flood outcomes %v: want admitted, owner-rate-limited and owner-cooldown", kinds)
	}
	if kinds[KindRateLimited] != 0 {
		t.Fatalf("the flooder drained the global bucket: %v", kinds)
	}
	if !e.cooling(otFlood) {
		t.Fatal("flooder not in cooldown")
	}
	_, err := e.submit(t, otFlood, "flood-1")
	wantKind(t, "flooder in cooldown", err, KindOwnerCooldown)
	if KindOwnerCooldown.HTTPStatus() != http.StatusServiceUnavailable || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("owner-cooldown is not a 503: %v", err)
	}

	// 2. The duplicate sender (two bonded nodes, so 20 per minute): each of
	// its proofs passes admission, then the Store reports a UNIQUE hit or
	// the verifier a replay.
	for i := 0; i <= DuplicateLimit; i++ {
		c, err := e.submit(t, otDup, "dup-1")
		if err != nil {
			t.Fatalf("dup sender submission %d: %v", i, err)
		}
		e.g.ObserveOwnerRejection(c, &Rejection{Kind: KindDuplicate, Detail: "proof_id"})
		e.requireNoLatch(t)
	}
	if !e.cooling(otDup) {
		t.Fatal("duplicate sender not in cooldown")
	}
	_, err = e.submit(t, otDup, "dup-1")
	wantKind(t, "dup sender in cooldown", err, KindOwnerCooldown)
	honestOK(5)

	// 3. The forger names the victim's node. Without an OwnerAuth the HMAC
	// key is public and nothing distinguishes the forger from the victim, so
	// the submission is attributed to the victim and spends the victim's
	// quota. TestV2ForgeryRejectedByOwnerAuth (opkeys_test.go) shows the WP-C
	// operator_sig check closing this gap. The forger cannot redirect the
	// reward: miner_addr must be the victim (owner binding), and naming its
	// own address is not-enrolled.
	_, err = e.submit(t, otForger, "victim-1")
	wantKind(t, "forger's own address on the victim's node", err, KindNotEnrolled)
	if e.tokens(otVictim) != -1 || e.tokens(otForger) != -1 {
		t.Fatal("an unattributable rejection created owner state")
	}
	c, err := e.submit(t, otVictim, "victim-1")
	if err != nil || c.Owner != otVictim {
		t.Fatalf("pre-WP-C forgery: %+v, %v; want attributed to the victim", c, err)
	}

	// 4. Unattributable noise from anyone: malformed, wrong attestation type,
	// unknown node, inactive node. No owner is charged and nothing latches.
	before := map[string]float64{otHonest: e.tokens(otHonest), otVictim: e.tokens(otVictim)}
	cc, _, _ := gtProof(t, e.clock.Now(), otHonest, "honest-1", mining.AttestationTypeCC)
	for i := 0; i < UnattributableAlarm; i++ {
		_, err := e.submitRaw([]byte(`{"not":"a proof"}`))
		wantKind(t, "malformed", err, KindMalformed)
		_, err = e.submitRaw(cc)
		wantKind(t, "cc", err, KindAttestationType)
		_, err = e.submit(t, otHonest, "no-such-node")
		wantKind(t, "unknown node", err, KindNotEnrolled)
	}
	for o, v := range before {
		if got := e.tokens(o); got != v {
			t.Fatalf("unattributable rejections changed %s's bucket: %v -> %v", o[:4], v, got)
		}
	}
	e.requireNoLatch(t)
	honestOK(5)

	st := e.g.Stats()
	if st.OwnersInCooldown != 2 || st.CooldownsStarted[CauseRateBurst] != 1 || st.CooldownsStarted[CauseDuplicates] != 1 {
		t.Fatalf("stats %+v: want the flooder and the dup sender cooling", st)
	}
	if st.Unattributable < 3*UnattributableAlarm || st.UnattributableAlarms == 0 {
		t.Fatalf("stats %+v: unattributable not counted", st)
	}
	for _, ev := range e.store.snapshot() {
		if ev.Kind != "owner-cooldown" {
			t.Fatalf("unexpected event %+v", ev)
		}
	}
	if gtExists(t, filepath.Join(e.dir, AdmissionStoppedFile)) {
		t.Fatal("ADMISSION_STOPPED latched")
	}
}

// An OwnerAuth that returns a *Rejection (for example a 503 when the key
// store is unavailable) is passed through unchanged.
func TestV2OwnerAuthRejectionPassThrough(t *testing.T) {
	view := newOTView().add("n", otHonest, true)
	e := otNew(t, gtDir(t), otPublicConfig(), view, otOptions{auth: func(*mining.Proof, string, string) error {
		return &Rejection{Kind: KindUnavailable, Detail: "operator keys unavailable"}
	}})
	e.open(t)
	_, err := e.submit(t, otHonest, "n")
	wantKind(t, "auth 503", err, KindUnavailable)
}

func TestV2BondedSlotScaling(t *testing.T) {
	view := newOTView().
		add("one-1", otHonest, true).
		add("three-1", otFlood, true).add("three-2", otFlood, true).add("three-3", otFlood, true).
		add("three-u1", otFlood, false).add("three-u2", otFlood, false).
		add("revoked-1", otFlood, true)
	view.revoke("revoked-1")
	for i := 0; i < 6; i++ {
		view.add(fmt.Sprintf("six-%d", i), otDup, true)
	}
	view.add("deferred-1", otVictim, false)
	e := otNew(t, gtDir(t), otPublicConfig(), view, otOptions{})
	for owner, want := range map[string]float64{otHonest: 10, otFlood: 30, otDup: 40, otVictim: 0, otForger: 0} {
		if got := e.g.OwnerRate(owner); got != want {
			t.Errorf("OwnerRate(%s) = %v, want %v", owner[:4], got, want)
		}
	}

	// The bucket holds one minute at the owner's rate and refills
	// continuously.
	e.open(t)
	for i := 0; i < 30; i++ {
		if _, err := e.submit(t, otFlood, fmt.Sprintf("three-%d", 1+i%3)); err != nil {
			t.Fatalf("3-slot owner, submission %d: %v", i, err)
		}
	}
	_, err := e.submit(t, otFlood, "three-1")
	wantKind(t, "3-slot owner over its bucket", err, KindOwnerRateLimited)
	e.clock.Advance(6 * time.Second) // 30/min * 0.1 min = 3 tokens
	for i := 0; i < 3; i++ {
		if _, err := e.submit(t, otFlood, "three-2"); err != nil {
			t.Fatalf("after refill, submission %d: %v", i, err)
		}
	}
	_, err = e.submit(t, otFlood, "three-2")
	wantKind(t, "after the refill", err, KindOwnerRateLimited)

	// An unbonded node of a bonded owner is still not admitted.
	_, err = e.submit(t, otFlood, "three-u1")
	wantKind(t, "unbonded node", err, KindNotEnrolled)
}

// A reduced-cap tier plugs in as a SlotPolicy. RequireFullyBonded still
// rejects every node that is not fully bonded.
func TestV2SlotPolicyTier(t *testing.T) {
	half := func(e EnrollmentInfo) int {
		if e.FullyBonded {
			return SlotUnit
		}
		return SlotUnit / 2
	}
	view := newOTView().add("b", otHonest, true).add("d1", otHonest, false).add("d2", otHonest, false)

	// require_fully_bonded false without deferred_slot_weight_permille is a
	// canary-only setting, and a canary v2 needs an allowlist.
	cfg := otPublicConfig()
	cfg.RequireFullyBonded = false
	cfg.Allowed = []AllowEntry{{MinerAddr: otHonest, NodeID: "b"}, {MinerAddr: otHonest, NodeID: "d1"}, {MinerAddr: otHonest, NodeID: "d2"}}
	e := otNew(t, gtDir(t), cfg, view, otOptions{mode: ModeCanary, policy: half})
	if got := e.g.OwnerRate(otHonest); got != 20 {
		t.Fatalf("tiered OwnerRate = %v, want 20 (1 + 2 x 0.5 slots)", got)
	}
	e.open(t)
	if c, err := e.submit(t, otHonest, "d1"); err != nil || c.Owner != otHonest {
		t.Fatalf("deferred node under the tier policy: %+v, %v", c, err)
	}

	// The same policy with require_fully_bonded: deferred nodes are refused
	// and do not count as slots.
	e = otNew(t, gtDir(t), otPublicConfig(), view, otOptions{policy: half})
	if got := e.g.OwnerRate(otHonest); got != 10 {
		t.Fatalf("OwnerRate with require_fully_bonded = %v, want 10", got)
	}
	e.open(t)
	_, err := e.submit(t, otHonest, "d1")
	wantKind(t, "deferred node, require_fully_bonded", err, KindNotEnrolled)

	// The default policy admits only fully bonded nodes even when
	// require_fully_bonded is false.
	e = otNew(t, gtDir(t), cfg, view, otOptions{mode: ModeCanary})
	e.open(t)
	_, err = e.submit(t, otHonest, "d2")
	wantKind(t, "deferred node, default policy", err, KindNotEnrolled)
	if !strings.Contains(err.Error(), "no slot") {
		t.Fatalf("detail: %v", err)
	}
}

func TestV2PrecheckEnrollment(t *testing.T) {
	view := newOTView().add("good", otHonest, true).add("unbonded", otHonest, false).add("gone", otHonest, true)
	view.revoke("gone")
	e := otNew(t, gtDir(t), otPublicConfig(), view, otOptions{})
	e.open(t)

	raw, p, _ := gtProof(t, e.clock.Now(), otHonest, "good", mining.AttestationTypeHMAC)
	c, err := e.g.Precheck(raw)
	if err != nil || c.Owner != otHonest || c.NodeID != "good" || c.AttNonce != p.Attestation.Nonce || c.Proof.MinerAddr != otHonest {
		t.Fatalf("Precheck(good) = %+v, %v", c, err)
	}
	for _, tc := range []struct{ name, miner, node, detail string }{
		{"unknown node", otHonest, "nope", "not enrolled"},
		{"inactive", otHonest, "gone", "not active"},
		{"not the owner", otFlood, "good", "not the node's owner"},
		{"not fully bonded", otHonest, "unbonded", "not fully bonded"},
	} {
		raw, _, _ := gtProof(t, e.clock.Now(), tc.miner, tc.node, mining.AttestationTypeHMAC)
		_, err := e.g.Precheck(raw)
		wantKind(t, tc.name, err, KindNotEnrolled)
		var re *mining.RejectError
		if !errors.As(err, &re) || re.Reason != mining.ReasonAttestation || !strings.Contains(err.Error(), tc.detail) {
			t.Fatalf("%s: %v, want 400 attestation %q", tc.name, err, tc.detail)
		}
	}
	if len(e.g.owners) != 0 {
		t.Fatalf("Precheck created owner state: %v", e.g.owners)
	}
	e.requireNoLatch(t)
}

// Canary v2 requires allowlist membership; public enforces a non-empty
// allowlist too. Allowlist misses are unattributable and never latch.
func TestV2Allowlist(t *testing.T) {
	view := newOTView().add("listed", otHonest, true).add("unlisted", otHonest, true).add("other", otFlood, true)
	cfg := otPublicConfig()
	cfg.Allowed = []AllowEntry{{MinerAddr: otHonest, NodeID: "listed"}, {MinerAddr: otVictim, NodeID: "other"}}
	for _, mode := range []Mode{ModeCanary, ModePublic} {
		e := otNew(t, gtDir(t), cfg, view, otOptions{mode: mode})
		e.open(t)
		if c, err := e.submit(t, otHonest, "listed"); err != nil || c.Owner != otHonest {
			t.Fatalf("%s listed: %+v, %v", mode, c, err)
		}
		for i := 0; i < 2*NotAllowlistedLimit; i++ {
			_, err := e.submit(t, otHonest, "unlisted")
			wantKind(t, string(mode)+" unlisted node", err, KindNodeNotAllowed)
			_, err = e.submit(t, otFlood, "other")
			wantKind(t, string(mode)+" listed node, other miner", err, KindMinerNotAllowed)
		}
		// "other" is listed for otVictim, but the chain says otFlood owns it.
		_, err := e.submit(t, otVictim, "other")
		wantKind(t, string(mode)+" listed pair, wrong enrollment owner", err, KindNotEnrolled)
		e.requireNoLatch(t)
		// Only listed nodes count as slots.
		if got := e.g.OwnerRate(otHonest); got != 10 {
			t.Fatalf("%s OwnerRate = %v, want 10 (only the listed node)", mode, got)
		}
	}
}

// v2 has no global enrollment predicate: the first allowlist entry being
// revoked closes nothing for the others (closedReason no longer uses
// Allowed[0]).
func TestV2ClosedReasonIgnoresAllowedZero(t *testing.T) {
	view := newOTView().add("first", otVictim, true).add("second", otHonest, true)
	view.revoke("first")
	cfg := otPublicConfig()
	cfg.Allowed = []AllowEntry{{MinerAddr: otVictim, NodeID: "first"}, {MinerAddr: otHonest, NodeID: "second"}}
	e := otNew(t, gtDir(t), cfg, view, otOptions{mode: ModeCanary})
	e.open(t)
	if _, err := e.submit(t, otHonest, "second"); err != nil {
		t.Fatalf("second entry: %v", err)
	}
	_, err := e.submit(t, otVictim, "first")
	wantKind(t, "revoked first entry", err, KindNotEnrolled)
	e.requireNoLatch(t)
}

// Cooldowns expire, can be released, and do not survive a restart.
func TestV2CooldownIsNotLatched(t *testing.T) {
	view := newOTView().add("n", otDup, true).add("n2", otDup, true)
	dir := gtDir(t)
	e := otNew(t, dir, otPublicConfig(), view, otOptions{})
	e.open(t)
	cool := func() {
		t.Helper()
		for i := 0; i <= DuplicateLimit; i++ {
			c, err := e.submit(t, otDup, "n")
			if err != nil {
				t.Fatalf("submission %d: %v", i, err)
			}
			e.g.ObserveOwnerRejection(c, &Rejection{Kind: KindNonceConflict})
		}
		if !e.cooling(otDup) {
			t.Fatal("no cooldown")
		}
	}
	cool()
	e.tick(OwnerCooldown - 10*time.Second)
	_, err := e.submit(t, otDup, "n")
	wantKind(t, "before expiry", err, KindOwnerCooldown)
	e.tick(10 * time.Second)
	if _, err := e.submit(t, otDup, "n"); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	e.tick(time.Minute)

	cool()
	if !e.g.ReleaseOwnerCooldown(otDup) || e.g.ReleaseOwnerCooldown(otDup) {
		t.Fatal("ReleaseOwnerCooldown")
	}
	if _, err := e.submit(t, otDup, "n"); err != nil {
		t.Fatalf("after release: %v", err)
	}
	e.tick(time.Minute)

	cool()
	e.requireNoLatch(t)
	e2 := otNew(t, dir, otPublicConfig(), view, otOptions{})
	e2.open(t)
	if _, err := e2.submit(t, otDup, "n"); err != nil {
		t.Fatalf("after restart: %v", err)
	}
}

func TestV2BadSubmissionsCooldown(t *testing.T) {
	view := newOTView().add("n", otFlood, true)
	e := otNew(t, gtDir(t), otPublicConfig(), view, otOptions{})
	e.open(t)
	c, err := e.submit(t, otFlood, "n")
	if err != nil {
		t.Fatal(err)
	}
	// Honest-miner noise never cools down.
	for _, r := range []mining.RejectReason{mining.ReasonStaleHeight, mining.ReasonWrongEpoch, mining.ReasonHeaderMismatch, mining.ReasonTooLate, mining.ReasonQuarantined} {
		for i := 0; i < 2*OwnerBadLimit; i++ {
			e.g.ObserveOwnerRejection(c, &mining.RejectError{Reason: r})
		}
	}
	e.g.ObserveOwnerRejection(c, errors.New("internal error"))
	if e.cooling(otFlood) {
		t.Fatal("honest-miner rejections started a cooldown")
	}
	for i := 0; i < OwnerBadLimit; i++ {
		e.g.ObserveOwnerRejection(c, &mining.RejectError{Reason: mining.ReasonWork})
	}
	if e.cooling(otFlood) {
		t.Fatal("cooldown at the limit, want above it")
	}
	e.g.ObserveOwnerRejection(c, &mining.RejectError{Reason: mining.ReasonAttestation, Detail: "hmac mismatch"})
	if !e.cooling(otFlood) || e.g.Stats().CooldownsStarted[CauseBadSubmissions] != 1 {
		t.Fatalf("no bad-submissions cooldown: %+v", e.g.Stats())
	}
	e.requireNoLatch(t)
}

// The HL1 global submitter triggers are alarms only with a v2 config.
func TestV2GlobalAlarmsDoNotLatch(t *testing.T) {
	view := newOTView().add("n", otHonest, true)
	cfg := otPublicConfig()
	e := otNew(t, gtDir(t), cfg, view, otOptions{})
	e.open(t)

	limit := RateBurstFactor * cfg.MaxProofsPerMin
	for m := 0; m < RateBurstMinutes+1; m++ {
		for i := 0; i <= limit; i++ {
			_ = e.g.Admit()
		}
		e.tick(time.Minute)
	}
	for i := 0; i < 3*GlobalDuplicateAlarm; i++ {
		e.g.ObserveRejection(&Rejection{Kind: KindDuplicate})
		e.g.ObserveOwnerRejection(Candidate{}, &Rejection{Kind: KindNonceConflict})
	}
	st := e.g.Stats()
	if st.RateBurstAlarms == 0 || st.DuplicateAlarms != 1 {
		t.Fatalf("alarms %+v", st)
	}
	e.requireNoLatch(t)
	if _, err := e.submit(t, otHonest, "n"); err != nil {
		t.Fatalf("honest owner after the alarms: %v", err)
	}

	// Producer-health triggers stay global.
	e.g.ObserveTotals(Totals{ConfigSHA256: e.g.ConfigHash(), Proofs: cfg.MaxProofsTotal}, 3.5)
	if s := e.g.State(); s != StateAdmissionStopped {
		t.Fatalf("proofs-total: %v", s)
	}
	_, err := e.submit(t, otHonest, "n")
	wantKind(t, "after proofs-total", err, KindAdmissionClosed)
	if err := os.WriteFile(filepath.Join(e.dir, KillFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if s := e.g.State(); s != StateKilled {
		t.Fatalf("KILL: %v", s)
	}
}

// Per-owner first: a full global bucket refunds the owner's token, and one
// owner cannot take more than its own bucket of the global one.
func TestV2GlobalBucketAfterOwner(t *testing.T) {
	view := newOTView().add("a", otHonest, true).add("b", otFlood, true)
	cfg := otPublicConfig()
	cfg.MaxProofsPerMin = 15
	e := otNew(t, gtDir(t), cfg, view, otOptions{})
	e.open(t)
	for i := 0; i < 10; i++ {
		if _, err := e.submit(t, otHonest, "a"); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.submit(t, otHonest, "a")
	wantKind(t, "owner bucket", err, KindOwnerRateLimited)
	for i := 0; i < 5; i++ {
		if _, err := e.submit(t, otFlood, "b"); err != nil {
			t.Fatal(err)
		}
	}
	_, err = e.submit(t, otFlood, "b")
	wantKind(t, "global bucket", err, KindRateLimited)
	if got := e.tokens(otFlood); got != 5 {
		t.Fatalf("owner tokens after a global refusal = %v, want 5 (refunded)", got)
	}
}

func TestV2OwnerPendingHooks(t *testing.T) {
	view := newOTView().add("a", otHonest, true)
	cfg := otPublicConfig()
	e := otNew(t, gtDir(t), cfg, view, otOptions{})
	if err := e.g.CheckOwnerPending(otHonest); err != nil {
		t.Fatal(err)
	}
	e.g.SetOwnerOutstanding(otHonest, cfg.MaxPendingPerOwner-1)
	if err := e.g.CheckOwnerPending(otHonest); err != nil {
		t.Fatal(err)
	}
	e.g.SetOwnerOutstanding(otHonest, cfg.MaxPendingPerOwner)
	err := e.g.CheckOwnerPending(otHonest)
	wantKind(t, "owner pending full", err, KindOwnerPendingFull)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("owner-pending-full is not a 503: %v", err)
	}
	e.g.ResetOwnerOutstanding(map[string]int{otFlood: cfg.MaxPendingPerOwner, otVictim: 0})
	if err := e.g.CheckOwnerPending(otHonest); err != nil {
		t.Fatalf("after reset: %v", err)
	}
	wantKind(t, "reset owner", e.g.CheckOwnerPending(otFlood), KindOwnerPendingFull)
	e.g.SetOwnerOutstanding(otFlood, 0)
	if err := e.g.CheckOwnerPending(otFlood); err != nil {
		t.Fatal(err)
	}
}

// With a v1 config the OwnerGuard methods are the HL1 behaviour.
func TestV1OwnerGuardMethodsAreHL1(t *testing.T) {
	e := gtNew(t, gtDir(t), gtConfig())
	e.open(t)
	raw, _, _ := gtProof(t, e.clock.Now(), gtMiner, gtNode, mining.AttestationTypeHMAC)
	c, err := e.g.Precheck(raw)
	if err != nil || c.Owner != "" {
		t.Fatalf("v1 Precheck: %+v, %v", c, err)
	}
	for i := 0; i < gtConfig().MaxProofsPerMin; i++ {
		if err := e.g.TakeOwnerRate(c); err != nil {
			t.Fatal(err)
		}
	}
	wantKind(t, "v1 TakeOwnerRate shares TakeRate's minute", e.g.TakeRate(), KindRateLimited)
	e.g.SetOwnerOutstanding(gtMiner, 1<<20)
	if err := e.g.CheckOwnerPending(gtMiner); err != nil {
		t.Fatal(err)
	}
	if e.g.OwnerRate(gtMiner) != 0 || e.g.ReleaseOwnerCooldown(gtMiner) {
		t.Fatal("v1 owner state")
	}
	for i := 0; i <= DuplicateLimit; i++ {
		e.g.ObserveOwnerRejection(c, &Rejection{Kind: KindDuplicate})
	}
	if s := e.g.State(); s != StateAdmissionStopped || gtReadCause(t, e.dir, MarkerAdmissionStopped) != fmt.Sprintf("%s:%d in %s", CauseDuplicates, DuplicateLimit+1, TriggerWindow) {
		t.Fatalf("v1 ObserveOwnerRejection: state %v", s)
	}
	if st := e.g.Stats(); len(st.Rejections) != 0 || st.Unattributable != 0 {
		t.Fatalf("v1 stats %+v", st)
	}
}

func TestNewGuardV2Options(t *testing.T) {
	view := newOTView()
	base := func() GuardOptions {
		return GuardOptions{
			Dir: gtDir(t), Config: otPublicConfig(), FailStop: func(int, string) {},
			Mode: ModePublic, Enrollments: view, OwnerAuth: otAcceptAll,
		}
	}
	if _, err := NewGuard(base()); err != nil {
		t.Fatalf("valid v2 options: %v", err)
	}
	o := base()
	o.OwnerAuth = nil
	if _, err := NewGuard(o); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("require_operator_sig without OwnerAuth: %v", err)
	}
	o = base()
	o.Enrollments = nil
	if _, err := NewGuard(o); err == nil || !strings.Contains(err.Error(), "Enrollments") {
		t.Errorf("no Enrollments: %v", err)
	}
	o = base()
	o.Mode = ModeCanary // a canary v2 needs an allowlist
	if _, err := NewGuard(o); !errors.Is(err, ErrConfig) {
		t.Errorf("canary v2 without allowlist: %v", err)
	}
	o = base()
	o.Config = gtConfig()
	o.EnrollmentActive = func(string, string) bool { return true }
	if _, err := NewGuard(o); !errors.Is(err, ErrConfig) {
		t.Errorf("public v1: %v", err)
	}
	// Since WP-E the S2 gate passes v2 in both modes.
	for _, m := range []Mode{ModeCanary, ModePublic} {
		if err := CheckSupported(m, v2Config()); err != nil {
			t.Errorf("CheckSupported(%s, v2) = %v", m, err)
		}
	}
}

func TestV2GuardConcurrency(t *testing.T) {
	view := newOTView()
	owners := []string{otHonest, otFlood, otDup, otVictim}
	for i, o := range owners {
		view.add(fmt.Sprintf("n%d", i), o, true)
	}
	e := otNew(t, gtDir(t), otPublicConfig(), view, otOptions{})
	e.open(t)
	raws := make([][][]byte, len(owners))
	for i, o := range owners {
		for j := 0; j < 30; j++ {
			raw, _, _ := gtProof(t, e.clock.Now(), o, fmt.Sprintf("n%d", i), mining.AttestationTypeHMAC)
			raws[i] = append(raws[i], raw)
		}
	}
	var wg sync.WaitGroup
	ok := make([]int, len(owners))
	for i := range owners {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for _, raw := range raws[i] {
				c, err := e.submitRaw(raw)
				if err == nil {
					ok[i]++
					e.g.ObserveOwnerRejection(c, &mining.RejectError{Reason: mining.ReasonStaleHeight})
				}
				e.g.SetOwnerOutstanding(c.Owner, ok[i])
				_ = e.g.CheckOwnerPending(c.Owner)
				_ = e.g.Stats()
			}
		}(i)
	}
	wg.Wait()
	for i, n := range ok {
		if n != otPublicConfig().MaxProofsPerMinPerOwner {
			t.Errorf("owner %d admitted %d, want exactly its bucket", i, n)
		}
	}
	e.requireNoLatch(t)
}
