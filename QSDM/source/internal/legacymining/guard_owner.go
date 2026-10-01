package legacymining

// guard_owner.go (HL2 WP-B): the version 2 admission path of CanaryGuard.
// Precheck looks up each submission's enrollment and attributes it to the
// enrollment owner; per-owner token buckets scale with the owner's bonded
// slots; submitter-driven triggers become per-owner cooldowns that never
// latch, and the HL1 global ADMISSION_STOP triggers they replace become
// log-only alarms (hl1/HL2_PUBLIC_MODE_DESIGN.md §1 Q4, §2 M3).
//
// Global triggers that reflect producer health are unchanged and stay in
// guard.go: KILL, FREEZE (TRIPPED), expiry, the proof total and the budget.
//
// Attribution: without an OwnerAuth a bundle naming a victim's node is
// attributed to the victim, because the HMAC keys are public chain state
// (design §1 Q1). The per-owner state below is keyed on the owner Precheck
// returns; it is forgery-proof when the OwnerAuth (WP-C: OperatorSigAuth,
// opkeys.go) rejects such bundles, which it does before any per-owner
// accounting. A v2 config with require_operator_sig cannot build a Guard
// without an OwnerAuth, and S2 refuses every v2 boot until WP-D and WP-E
// land.
//
// Locks: cmu stays a leaf. EnrollmentView, OwnerAuth, Store.Event and logf
// are called with no guard lock held.

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	hmacattest "github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

var _ OwnerGuard = (*CanaryGuard)(nil)

// ownerState is one attributed owner's admission state, under cmu. Entries
// are created only for owners that passed Precheck (or that the Ledger
// reports), so the map is bounded by the enrollment set.
type ownerState struct {
	// Token bucket: capacity max(rate, 1) per minute, refilled continuously.
	tokens float64
	last   int64 // mono ns of the last refill
	primed bool

	// Rate-burst trigger, as recordSubmission but per owner.
	subMinute int64
	subCount  uint64
	burstLast int64
	burstRun  int

	dups triggerWindow // duplicates and nonce conflicts
	bad  triggerWindow // bad submissions (countsAsBad)

	coolUntil int64 // mono ns; cooling while now < coolUntil
	coolCause string
}

// ownerStats backs GuardStats.
type ownerStats struct {
	rejections      [KindOwnerCooldown + 1]atomic.Uint64
	unattributable  atomic.Uint64
	rateBurstAlarms atomic.Uint64
	dupAlarms       atomic.Uint64
	anonAlarms      atomic.Uint64
	cooldowns       map[string]uint64 // by cause name; under cmu
	dupAlarmAt      int64             // mono ns of the last duplicate alarm; under cmu
	dupAlarmed      bool
}

func (g *CanaryGuard) initOwner(mode Mode, o GuardOptions) {
	g.v2 = true
	g.mode = mode
	g.enroll = o.Enrollments
	g.slots = o.SlotPolicy
	if g.slots == nil {
		g.slots = FullyBondedSlotPolicy
	}
	g.auth = o.OwnerAuth
	g.allow = make(map[string]string, len(g.cfg.Allowed))
	for _, e := range g.cfg.Allowed {
		g.allow[e.NodeID] = e.MinerAddr
	}
	g.owners = make(map[string]*ownerState)
	g.pending = make(map[string]int)
	g.stats.cooldowns = make(map[string]uint64)
}

// reject counts r in GuardStats and returns it.
func (g *CanaryGuard) reject(r *Rejection) error {
	if int(r.Kind) < len(g.stats.rejections) {
		g.stats.rejections[r.Kind].Add(1)
	}
	return r
}

// unattributed rejects a submission before an owner is known. It touches
// no owner's state and no latch: a count, and one log line per
// TriggerWindow once UnattributableAlarm is reached.
func (g *CanaryGuard) unattributed(r *Rejection) (Candidate, error) {
	g.stats.unattributable.Add(1)
	now := g.mono(g.now())
	g.cmu.Lock()
	if g.anonN == 0 || now-g.anonFrom >= int64(TriggerWindow) {
		g.anonFrom, g.anonN = now, 0
	}
	g.anonN++
	alarm := g.anonN == UnattributableAlarm
	g.cmu.Unlock()
	if alarm {
		g.stats.anonAlarms.Add(1)
		g.logf("legacymining: alarm: %d unattributable rejections in %s (last: %v); no owner charged, no admission stop", UnattributableAlarm, TriggerWindow, r)
	}
	return Candidate{}, g.reject(r)
}

// precheckV2 is Precheck for a version 2 config (see Guard.Precheck).
func (g *CanaryGuard) precheckV2(raw []byte) (Candidate, error) {
	p, err := mining.ParseProof(raw)
	if err != nil {
		return g.unattributed(&Rejection{Kind: KindMalformed, Detail: err.Error()})
	}
	if p.Attestation.Type != mining.AttestationTypeHMAC {
		return g.unattributed(&Rejection{Kind: KindAttestationType, Detail: fmt.Sprintf("want %s", mining.AttestationTypeHMAC)})
	}
	b, err := hmacattest.ParseBundle(p.Attestation.BundleBase64)
	if err != nil {
		return g.unattributed(&Rejection{Kind: KindMalformed, Detail: "bundle: " + err.Error()})
	}
	c := Candidate{Proof: p, NodeID: b.NodeID}
	if len(b.Nonce) != hex.EncodedLen(len(c.AttNonce)) {
		return g.unattributed(&Rejection{Kind: KindMalformed, Detail: "bundle: nonce is not 64 hex characters"})
	}
	if _, err := hex.Decode(c.AttNonce[:], []byte(b.Nonce)); err != nil {
		return g.unattributed(&Rejection{Kind: KindMalformed, Detail: "bundle: nonce: " + err.Error()})
	}
	if len(g.allow) != 0 {
		miner, ok := g.allow[b.NodeID]
		if !ok {
			return g.unattributed(&Rejection{Kind: KindNodeNotAllowed})
		}
		if miner != p.MinerAddr {
			return g.unattributed(&Rejection{Kind: KindMinerNotAllowed})
		}
	}
	e, ok := g.enroll.Lookup(b.NodeID)
	var why string
	switch {
	case !ok || e.NodeID != b.NodeID:
		why = "node is not enrolled"
	case !e.Active:
		why = "enrollment is not active"
	case e.Owner != p.MinerAddr:
		why = "miner_addr is not the node's owner"
	case g.cfg.RequireFullyBonded && !e.FullyBonded:
		why = "node is not fully bonded"
	case g.weight(e) <= 0:
		why = "bond policy gives the node no slot"
	}
	if why != "" {
		return g.unattributed(&Rejection{Kind: KindNotEnrolled, Detail: why})
	}
	if g.auth != nil {
		if err := g.auth(p, b.NodeID, e.Owner); err != nil {
			var r *Rejection
			if !errors.As(err, &r) {
				r = &Rejection{Kind: KindBadOperatorSig, Detail: err.Error()}
			}
			return g.unattributed(r)
		}
	}
	c.Owner = e.Owner
	return c, nil
}

// weight is the SlotPolicy weight of e, clamped to 0..SlotUnit.
func (g *CanaryGuard) weight(e EnrollmentInfo) int {
	w := g.slots(e)
	if w < 0 {
		return 0
	}
	if w > SlotUnit {
		return SlotUnit
	}
	return w
}

// ownerUnits returns the owner's admitted slots in SlotUnit units: the
// weights of its active nodes that pass the bond policy (and, with an
// allowlist, are listed for this owner), capped at BondedSlotCap slots.
func (g *CanaryGuard) ownerUnits(owner string) int {
	units := 0
	for _, e := range g.enroll.OwnerNodes(owner) {
		if e.Owner != owner || !e.Active || (g.cfg.RequireFullyBonded && !e.FullyBonded) {
			continue
		}
		if len(g.allow) != 0 && g.allow[e.NodeID] != owner {
			continue
		}
		units += g.weight(e)
	}
	if limit := g.cfg.BondedSlotCap * SlotUnit; units > limit {
		units = limit
	}
	return units
}

// OwnerRate returns owner's per-minute bucket rate:
// MaxProofsPerMinPerOwner * min(slots, BondedSlotCap). It is 0 with a v1
// config.
func (g *CanaryGuard) OwnerRate(owner string) float64 {
	if !g.v2 {
		return 0
	}
	return float64(g.cfg.MaxProofsPerMinPerOwner) * float64(g.ownerUnits(owner)) / SlotUnit
}

// owner returns the state of owner, creating it. Call with cmu held.
func (g *CanaryGuard) owner(owner string) *ownerState {
	o := g.owners[owner]
	if o == nil {
		o = &ownerState{}
		g.owners[owner] = o
	}
	return o
}

// refill brings o's bucket to now for rate and returns its capacity. Call
// with cmu held.
func (o *ownerState) refill(now int64, rate float64) float64 {
	capacity := 0.0
	if rate > 0 {
		capacity = math.Max(rate, 1)
	}
	if !o.primed {
		o.tokens, o.last, o.primed = capacity, now, true
		return capacity
	}
	if dt := now - o.last; dt > 0 {
		o.tokens += float64(dt) / float64(time.Minute) * rate
	}
	o.last = now
	if o.tokens > capacity {
		o.tokens = capacity
	}
	return capacity
}

// startCooldown puts o in an OwnerCooldown. Call with cmu held, then call
// cooldownStarted with no lock held.
func (g *CanaryGuard) startCooldown(o *ownerState, now int64, cause string) {
	o.coolUntil = now + int64(OwnerCooldown)
	o.coolCause = cause
	o.dups, o.bad = triggerWindow{}, triggerWindow{}
	o.burstRun = 0
	g.stats.cooldowns[causeName(cause)]++
}

// cooldownStarted logs the cooldown and appends an events row, best effort.
func (g *CanaryGuard) cooldownStarted(owner, cause string) {
	g.logf("legacymining: owner-cooldown %s for %s: %s (per owner, not latched)", owner, OwnerCooldown, cause)
	if g.store != nil {
		if err := g.store.Event(Event{AtNS: g.now().UnixNano(), Kind: "owner-cooldown", Detail: owner + ":" + cause}); err != nil {
			g.logf("legacymining: owner-cooldown: events row (best effort): %v", err)
		}
	}
}

func cooldownRejection(o *ownerState, now int64) *Rejection {
	left := time.Duration(o.coolUntil - now).Round(time.Second)
	return &Rejection{Kind: KindOwnerCooldown, Detail: fmt.Sprintf("%s; %s left", o.coolCause, left)}
}

// TakeOwnerRate implements OwnerGuard.
func (g *CanaryGuard) TakeOwnerRate(c Candidate) error {
	if !g.v2 {
		return g.TakeRate()
	}
	if c.Owner == "" || c.Proof == nil || c.Proof.MinerAddr != c.Owner {
		return g.reject(&Rejection{Kind: KindOwnerRateLimited, Detail: "submission has no attributed owner"})
	}
	rate := g.OwnerRate(c.Owner) // EnrollmentView, outside cmu
	burst := uint64(math.Ceil(rate)) * RateBurstFactor
	if burst < RateBurstFactor {
		burst = RateBurstFactor
	}
	t := g.now()
	now, m := g.mono(t), g.minute(t)

	g.cmu.Lock()
	o := g.owner(c.Owner)
	if now < o.coolUntil {
		r := cooldownRejection(o, now)
		g.cmu.Unlock()
		return g.reject(r)
	}
	if m != o.subMinute {
		o.subMinute, o.subCount = m, 0
	}
	o.subCount++
	cause := ""
	if o.subCount == burst+1 {
		if o.burstRun > 0 && o.burstLast == m-1 {
			o.burstRun++
		} else {
			o.burstRun = 1
		}
		o.burstLast = m
		if o.burstRun >= RateBurstMinutes {
			cause = fmt.Sprintf("%s:>%d/min for %d consecutive minutes", CauseRateBurst, burst, RateBurstMinutes)
			g.startCooldown(o, now, cause)
		}
	}
	if cause != "" {
		r := cooldownRejection(o, now)
		g.cmu.Unlock()
		g.cooldownStarted(c.Owner, cause)
		return g.reject(r)
	}
	capacity := o.refill(now, rate)
	if o.tokens < 1 {
		g.cmu.Unlock()
		return g.reject(&Rejection{Kind: KindOwnerRateLimited, Detail: fmt.Sprintf("max %g proofs per minute for this owner", rate)})
	}
	o.tokens--
	g.cmu.Unlock()

	if err := g.TakeRate(); err != nil {
		// The global bucket is full: give the owner its token back.
		g.cmu.Lock()
		o.tokens = math.Min(o.tokens+1, capacity)
		g.cmu.Unlock()
		var r *Rejection
		if errors.As(err, &r) {
			return g.reject(r)
		}
		return err
	}
	return nil
}

// countsAsBad reports whether err is a Verify rejection an honest miner
// does not produce. Stale work, a wrong epoch, a header mismatch, a late
// proof and an address quarantine are not counted: honest miners hit the
// first four, and the quarantine already blocks the address.
func countsAsBad(err error) bool {
	var re *mining.RejectError
	if !errors.As(err, &re) {
		return false
	}
	switch re.Reason {
	case mining.ReasonBadVersion, mining.ReasonNonCanonical, mining.ReasonBadAddr, mining.ReasonBatchSize,
		mining.ReasonBatchRoot, mining.ReasonWork, mining.ReasonBatchFraud, mining.ReasonAttestation:
		return true
	}
	return false
}

// ObserveOwnerRejection implements OwnerGuard.
func (g *CanaryGuard) ObserveOwnerRejection(c Candidate, err error) {
	if !g.v2 {
		g.ObserveRejection(err)
		return
	}
	if err == nil {
		return
	}
	dup := countsAsDuplicate(err)
	if dup {
		g.observeGlobalDuplicate()
	}
	if c.Owner == "" || (!dup && !countsAsBad(err)) {
		return
	}
	now := g.mono(g.now())
	cause := ""
	g.cmu.Lock()
	o := g.owner(c.Owner)
	if now >= o.coolUntil {
		if dup {
			if n := o.dups.add(now, DuplicateLimit+1); n > DuplicateLimit {
				cause = fmt.Sprintf("%s:%d in %s", CauseDuplicates, n, TriggerWindow)
			}
		} else if n := o.bad.add(now, OwnerBadLimit+1); n > OwnerBadLimit {
			cause = fmt.Sprintf("%s:%d in %s", CauseBadSubmissions, n, TriggerWindow)
		}
		if cause != "" {
			g.startCooldown(o, now, cause)
		}
	}
	g.cmu.Unlock()
	if cause != "" {
		g.cooldownStarted(c.Owner, cause)
	}
}

// observeGlobalDuplicate is the v2 global duplicate ceiling: an alarm (log
// and GuardStats), at most once per TriggerWindow, never a latch.
func (g *CanaryGuard) observeGlobalDuplicate() {
	now := g.mono(g.now())
	g.cmu.Lock()
	n := g.gdups.add(now, GlobalDuplicateAlarm+1)
	alarm := n > GlobalDuplicateAlarm && (!g.stats.dupAlarmed || now-g.stats.dupAlarmAt >= int64(TriggerWindow))
	if alarm {
		g.stats.dupAlarmed, g.stats.dupAlarmAt = true, now
	}
	g.cmu.Unlock()
	if alarm {
		g.stats.dupAlarms.Add(1)
		g.logf("legacymining: alarm %s: >%d duplicates or nonce conflicts in %s across all owners (v2: no admission stop)", CauseDuplicates, GlobalDuplicateAlarm, TriggerWindow)
	}
}

// CheckOwnerPending implements OwnerGuard.
func (g *CanaryGuard) CheckOwnerPending(owner string) error {
	if !g.v2 {
		return nil
	}
	g.cmu.Lock()
	n := g.pending[owner]
	g.cmu.Unlock()
	if n >= g.cfg.MaxPendingPerOwner {
		return g.reject(&Rejection{Kind: KindOwnerPendingFull, Detail: fmt.Sprintf("owner pending plus in-flight >= %d", g.cfg.MaxPendingPerOwner)})
	}
	return nil
}

// SetOwnerOutstanding implements OwnerGuard.
func (g *CanaryGuard) SetOwnerOutstanding(owner string, n int) {
	if !g.v2 {
		return
	}
	g.cmu.Lock()
	if n <= 0 {
		delete(g.pending, owner)
	} else {
		g.pending[owner] = n
	}
	g.cmu.Unlock()
}

// ResetOwnerOutstanding implements OwnerGuard.
func (g *CanaryGuard) ResetOwnerOutstanding(counts map[string]int) {
	if !g.v2 {
		return
	}
	m := make(map[string]int, len(counts))
	for k, n := range counts {
		if n > 0 {
			m[k] = n
		}
	}
	g.cmu.Lock()
	g.pending = m
	g.cmu.Unlock()
}

// ReleaseOwnerCooldown ends owner's cooldown now (an operator action; WP-H
// may expose it). It reports whether the owner was cooling. A restart also
// clears every cooldown: they are not latched.
func (g *CanaryGuard) ReleaseOwnerCooldown(owner string) bool {
	if !g.v2 {
		return false
	}
	now := g.mono(g.now())
	g.cmu.Lock()
	o := g.owners[owner]
	was := o != nil && now < o.coolUntil
	if o != nil {
		o.coolUntil = 0
	}
	g.cmu.Unlock()
	if was {
		g.logf("legacymining: owner-cooldown %s released", owner)
	}
	return was
}

// Stats returns a snapshot of the HL2 counters (zero with a v1 config).
func (g *CanaryGuard) Stats() GuardStats {
	s := GuardStats{
		Rejections:           make(map[string]uint64),
		CooldownsStarted:     make(map[string]uint64),
		Unattributable:       g.stats.unattributable.Load(),
		RateBurstAlarms:      g.stats.rateBurstAlarms.Load(),
		DuplicateAlarms:      g.stats.dupAlarms.Load(),
		UnattributableAlarms: g.stats.anonAlarms.Load(),
	}
	for k := range g.stats.rejections {
		if n := g.stats.rejections[k].Load(); n != 0 {
			s.Rejections[RejectKind(k).String()] = n
		}
	}
	if !g.v2 {
		return s
	}
	now := g.mono(g.now())
	g.cmu.Lock()
	for cause, n := range g.stats.cooldowns {
		s.CooldownsStarted[cause] = n
	}
	for _, o := range g.owners {
		if now < o.coolUntil {
			s.OwnersInCooldown++
		}
	}
	g.cmu.Unlock()
	return s
}

// causeName strips the ":<detail>" of a cause.
func causeName(cause string) string {
	for i := 0; i < len(cause); i++ {
		if cause[i] == ':' {
			return cause[:i]
		}
	}
	return cause
}
