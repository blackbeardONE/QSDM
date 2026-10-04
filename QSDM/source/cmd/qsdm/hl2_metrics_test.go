package main

// HL2 WP-H: the v2 metrics collector (hl2_metrics.go).

import (
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

func hl2MetricKey(m monitoring.Metric) string {
	k := m.Name
	for _, l := range []string{"kind", "cause", "alarm"} {
		if v, ok := m.Labels[l]; ok {
			k += "{" + l + "=" + v + "}"
		}
	}
	return k
}

func hl2Collect(c *hl1CanaryParts) map[string]float64 {
	out := map[string]float64{}
	for _, m := range hl2MetricsCollector(c)() {
		k := hl2MetricKey(m)
		if _, dup := out[k]; dup {
			panic("duplicate series " + k)
		}
		out[k] = m.Value
	}
	return out
}

func TestHL2MetricsCollector(t *testing.T) {
	victim, keyless := newHL2Wallet(t), newHL2Wallet(t)
	st := enrollment.NewInMemoryState()
	hl2Enroll(t, st, "victim-1", victim.owner, "GPU-v1", mining.MinEnrollStakeDust)
	hl2Enroll(t, st, "keyless-1", keyless.owner, "GPU-k1", mining.MinEnrollStakeDust)
	dbPath := filepath.Join(hl2LegacyDir(t), legacymining.DBFile)
	now := time.Now()
	env := legacymining.Env{Mode: legacymining.ModePublic, DBPath: dbPath}

	// v1: nothing is exported.
	v1 := legacymining.Config{
		Version: 1, Allowed: []legacymining.AllowEntry{{MinerAddr: victim.owner, NodeID: "victim-1"}},
		MaxProofsPerMin: 6, MaxProofsTotal: 100, MaxPending: 10, BudgetCell: 100, ExpiresUnix: now.Add(time.Hour).Unix(),
	}
	p1 := hl1NewCanary(hl1BootConfig{Env: legacymining.Env{Mode: legacymining.ModeCanary, DBPath: dbPath}, Config: v1}, true, chain.NewAccountStore(), st)
	if !p1.enabled() || hl2IsV2(p1) || len(hl2MetricsCollector(p1)()) != 0 {
		t.Fatalf("v1 exports HL2 metrics")
	}
	if hl2IsV2(nil) || len(hl2MetricsCollector(nil)()) != 0 {
		t.Fatal("nil parts")
	}

	v2 := legacymining.Config{
		Version: 2, Allowed: []legacymining.AllowEntry{}, MaxProofsPerMin: 60, MaxProofsPerMinPerOwner: 6,
		MaxProofsTotal: 1000, MaxPending: 100, MaxPendingPerOwner: 10, OwnerEpochCapCell: 10, DifficultyBits: 16,
		RequireOperatorSig: true, RequireFullyBonded: true, BondedSlotCap: 1, BudgetCell: 100, ExpiresUnix: now.Add(time.Hour).Unix(),
	}
	parts := hl1NewCanary(hl1BootConfig{Env: env, Config: v2}, true, chain.NewAccountStore(), st)
	if !hl2IsV2(parts) {
		t.Fatalf("v2 parts: %+v", parts)
	}
	if err := parts.store.Open(dbPath, &legacymining.Meta{H0: 1, GenesisHash: "g", Funder: "f", Release: "hl2"}); err != nil {
		t.Fatal(err)
	}
	defer parts.store.Close()
	blocks := []*chain.Block{{Height: 2, Transactions: []*mempool.Tx{
		{ID: "e1", Sender: victim.owner, ContractID: enrollment.SignedContractID, PublicKey: hex.EncodeToString(victim.pub)},
	}}}
	hl2HydrateOperatorKeys(parts, blocks, true)

	// An unsigned forgery for the victim: unattributable bad-operator-sig.
	if _, err := parts.guard.Precheck(hl2Proof(t, nil, victim.owner, "victim-1", "GPU-v1")); legacymining.RejectKindOf(err) != legacymining.KindBadOperatorSig {
		t.Fatalf("forgery: %v", err)
	}
	got := hl2Collect(parts)
	for k, want := range map[string]float64{
		"hl2_guard_rejections_total{kind=bad-operator-sig}":        1,
		"hl2_guard_rejections_total{kind=not-enrolled}":            0,
		"hl2_guard_rejections_total{kind=owner-cooldown}":          0,
		"hl2_guard_rejections_total{kind=admission-closed}":        0,
		"hl2_guard_unattributable_total":                           1,
		"hl2_guard_cooldowns_started_total{cause=rate-burst}":      0,
		"hl2_guard_cooldowns_started_total{cause=duplicates}":      0,
		"hl2_guard_cooldowns_started_total{cause=bad-submissions}": 0,
		"hl2_guard_owners_in_cooldown":                             0,
		"hl2_guard_alarms_total{alarm=rate-burst}":                 0,
		"hl2_guard_alarms_total{alarm=duplicates}":                 0,
		"hl2_guard_alarms_total{alarm=unattributable}":             0,
		"hl2_ledger_outstanding":                                   0,
		"hl2_owner_epoch_cap_cell":                                 10,
		"hl2_owner_epoch_owners":                                   0,
		"hl2_owner_epoch_owners_at_cap":                            0,
		"hl2_owner_epoch_emitted_cell_sum":                         0,
		"hl2_owner_epoch_emitted_cell_max":                         0,
		"hl2_operator_keys_owners":                                 2,
		"hl2_operator_keys_without_key":                            1,
		"hl2_operator_keys_bad_rows":                               0,
	} {
		if v, ok := got[k]; !ok || v != want {
			t.Fatalf("%s = %v (present %v), want %v; all: %v", k, v, ok, want, got)
		}
	}
	if _, ok := got["hl2_owner_epoch"]; !ok {
		t.Fatal("hl2_owner_epoch missing")
	}
	// Bounded: one series per reject kind, cause and alarm, nothing per owner.
	if n := len(got); n != int(legacymining.KindOwnerCooldown)+1+3+1+3+7+3 {
		t.Fatalf("%d series: %v", n, got)
	}
}
