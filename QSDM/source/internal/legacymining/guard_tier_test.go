package legacymining

// HL2 operator decision A: the deferred-bond tier. Deferred-bond nodes that
// are not yet fully bonded mine at deferred_slot_weight_permille of a slot;
// a node becomes a full slot as soon as the chain reports it fully bonded;
// weight 0 excludes them; v1 is unchanged.

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// addDeferred adds an active deferred-bond node (bond_mode mining_rewards)
// with stake dust of 10 locked.
func (v *otView) addDeferred(node, owner string, stake uint64) *otView {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.recs[node] = EnrollmentInfo{NodeID: node, Owner: owner, Active: true, DeferredBond: true,
		FullyBonded: stake >= 10, StakeDust: stake, RequiredDust: 10}
	return v
}

// accrue adds dust to node's bond, as a reward block does on chain.
func (v *otView) accrue(node string, dust uint64) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e := v.recs[node]
	e.StakeDust += dust
	e.FullyBonded = e.StakeDust >= e.RequiredDust
	v.recs[node] = e
}

// otTierConfig is otPublicConfig with the deferred-bond tier at weight.
func otTierConfig(weight int) Config {
	c := otPublicConfig()
	c.RequireFullyBonded = false
	c.DeferredSlotWeightPermille = weight
	return c
}

func TestSlotPolicies(t *testing.T) {
	bonded := EnrollmentInfo{Active: true, FullyBonded: true, StakeDust: 10, RequiredDust: 10}
	deferred := EnrollmentInfo{Active: true, DeferredBond: true, StakeDust: 3, RequiredDust: 10}
	deferredDone := EnrollmentInfo{Active: true, DeferredBond: true, FullyBonded: true, StakeDust: 10, RequiredDust: 10}
	slashedUpfront := EnrollmentInfo{Active: true, StakeDust: 5, RequiredDust: 10}
	for name, c := range map[string]struct {
		p    SlotPolicy
		want [4]int // bonded, deferred, deferredDone, slashedUpfront
	}{
		"fully bonded":        {FullyBondedSlotPolicy, [4]int{SlotUnit, 0, SlotUnit, 0}},
		"tier 250":            {DeferredBondSlotPolicy(250), [4]int{SlotUnit, 250, SlotUnit, 0}},
		"tier 0":              {DeferredBondSlotPolicy(0), [4]int{SlotUnit, 0, SlotUnit, 0}},
		"tier clamped high":   {DeferredBondSlotPolicy(5000), [4]int{SlotUnit, SlotUnit, SlotUnit, 0}},
		"tier clamped low":    {DeferredBondSlotPolicy(-1), [4]int{SlotUnit, 0, SlotUnit, 0}},
		"config v2 tier":      {ConfigSlotPolicy(otTierConfig(100)), [4]int{SlotUnit, 100, SlotUnit, 0}},
		"config v2 no tier":   {ConfigSlotPolicy(otPublicConfig()), [4]int{SlotUnit, 0, SlotUnit, 0}},
		"config v1 never":     {ConfigSlotPolicy(gtConfig()), [4]int{SlotUnit, 0, SlotUnit, 0}},
		"config v1 with tier": {ConfigSlotPolicy(func() Config { c := gtConfig(); c.DeferredSlotWeightPermille = 500; return c }()), [4]int{SlotUnit, 0, SlotUnit, 0}},
	} {
		got := [4]int{c.p(bonded), c.p(deferred), c.p(deferredDone), c.p(slashedUpfront)}
		if got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// A deferred-bond node is admitted at the reduced rate, and becomes a full
// slot at runtime once the chain reports it fully bonded.
func TestV2DeferredBondTier(t *testing.T) {
	view := newOTView().
		addDeferred("d", otHonest, 0).
		add("b", otFlood, true).
		add("u", otDup, false) // upfront bond, not fully bonded (slashed): never in the tier
	cfg := otTierConfig(100) // 10/min per slot -> 1/min for the deferred node
	if err := CheckModeConfig(ModePublic, cfg); err != nil {
		t.Fatalf("public tier config: %v", err)
	}
	if err := CheckSupported(ModePublic, cfg); err != nil {
		t.Fatalf("public tier config is bootable: %v", err)
	}
	e := otNew(t, gtDir(t), cfg, view, otOptions{}) // nil SlotPolicy: ConfigSlotPolicy
	if got := e.g.OwnerRate(otHonest); got != 1 {
		t.Fatalf("deferred OwnerRate = %v, want 1 (10 x 100/1000)", got)
	}
	if got := e.g.OwnerRate(otFlood); got != 10 {
		t.Fatalf("bonded OwnerRate = %v, want 10", got)
	}
	e.open(t)

	if c, err := e.submit(t, otHonest, "d"); err != nil || c.Owner != otHonest {
		t.Fatalf("deferred node: %+v, %v", c, err)
	}
	_, err := e.submit(t, otHonest, "d")
	wantKind(t, "deferred node, second proof in the minute", err, KindOwnerRateLimited)
	for i := 0; i < 10; i++ {
		if _, err := e.submit(t, otFlood, "b"); err != nil {
			t.Fatalf("bonded node, proof %d: %v", i, err)
		}
	}
	_, err = e.submit(t, otDup, "u")
	wantKind(t, "upfront node not fully bonded", err, KindNotEnrolled)
	if !strings.Contains(err.Error(), "no slot") {
		t.Fatalf("detail: %v", err)
	}

	// One minute later the deferred bucket has refilled one token.
	e.tick(time.Minute)
	if _, err := e.submit(t, otHonest, "d"); err != nil {
		t.Fatalf("deferred node after a minute: %v", err)
	}
	_, err = e.submit(t, otHonest, "d")
	wantKind(t, "deferred node, still reduced", err, KindOwnerRateLimited)

	// Rewards accrue into the bond: partial accrual changes nothing ...
	view.accrue("d", 9)
	if got := e.g.OwnerRate(otHonest); got != 1 {
		t.Fatalf("OwnerRate at 9/10 bonded = %v, want 1", got)
	}
	// ... and the block that completes the bond makes it a full slot at the
	// next submission, with no restart.
	view.accrue("d", 1)
	if got := e.g.OwnerRate(otHonest); got != 10 {
		t.Fatalf("OwnerRate once fully bonded = %v, want 10", got)
	}
	e.tick(time.Minute) // refill at the full rate
	for i := 0; i < 10; i++ {
		if _, err := e.submit(t, otHonest, "d"); err != nil {
			t.Fatalf("fully bonded node, proof %d: %v", i, err)
		}
	}
	_, err = e.submit(t, otHonest, "d")
	wantKind(t, "full bucket drained", err, KindOwnerRateLimited)
	e.requireNoLatch(t)
}

// An owner with one bonded and several deferred nodes: the weights add up,
// and BondedSlotCap still bounds the total.
func TestV2DeferredBondTierSlotSum(t *testing.T) {
	view := newOTView().add("b", otHonest, true).
		addDeferred("d1", otHonest, 0).addDeferred("d2", otHonest, 4).addDeferred("d3", otHonest, 9)
	e := otNew(t, gtDir(t), otTierConfig(500), view, otOptions{})
	if got := e.g.OwnerRate(otHonest); got != 25 {
		t.Fatalf("OwnerRate = %v, want 25 (1 + 3 x 0.5 slots)", got)
	}
	cfg := otTierConfig(500)
	cfg.BondedSlotCap = 2
	e = otNew(t, gtDir(t), cfg, view, otOptions{})
	if got := e.g.OwnerRate(otHonest); got != 20 {
		t.Fatalf("OwnerRate capped = %v, want 20 (bonded_slot_cap 2)", got)
	}
}

// Weight 0 excludes deferred-bond nodes, whatever require_fully_bonded says;
// public mode then needs require_fully_bonded true.
func TestV2DeferredBondTierWeightZero(t *testing.T) {
	view := newOTView().addDeferred("d", otHonest, 0).add("b", otHonest, true)
	cfg := otTierConfig(0)
	if err := CheckModeConfig(ModePublic, cfg); !errors.Is(err, ErrConfig) {
		t.Fatalf("public, not fully bonded, no tier: %v", err)
	}
	// Public with require_fully_bonded (the default) refuses the node.
	e := otNew(t, gtDir(t), otPublicConfig(), view, otOptions{})
	e.open(t)
	_, err := e.submit(t, otHonest, "d")
	wantKind(t, "public, fully bonded required", err, KindNotEnrolled)
	if got := e.g.OwnerRate(otHonest); got != 10 {
		t.Fatalf("OwnerRate = %v, want 10 (the deferred node is no slot)", got)
	}
	// A canary with require_fully_bonded false and weight 0 still refuses it.
	cfg.Allowed = []AllowEntry{{MinerAddr: otHonest, NodeID: "d"}, {MinerAddr: otHonest, NodeID: "b"}}
	e = otNew(t, gtDir(t), cfg, view, otOptions{mode: ModeCanary})
	e.open(t)
	_, err = e.submit(t, otHonest, "d")
	wantKind(t, "canary, weight 0", err, KindNotEnrolled)
	if _, err := e.submit(t, otHonest, "b"); err != nil {
		t.Fatalf("bonded node: %v", err)
	}
}
