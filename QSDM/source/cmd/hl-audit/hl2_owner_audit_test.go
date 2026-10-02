package main

// HL2 WP-H: the per-owner audit of a version 2 config (audit.go
// checkOwners, checkOwnerRewards) and several config windows in one run.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
)

// v2Cfg is a valid public-mode version 2 config, changed by mut.
func v2Cfg(t *testing.T, mut func(*legacymining.Config)) canaryCfg {
	t.Helper()
	cfg := legacymining.Config{
		Version: 2, Allowed: []legacymining.AllowEntry{}, MaxProofsPerMin: 600, MaxProofsPerMinPerOwner: 60,
		MaxProofsTotal: 3000, MaxPending: 600, MaxPendingPerOwner: 100, OwnerEpochCapCell: 100, DifficultyBits: 20,
		RequireOperatorSig: true, RequireFullyBonded: true, BondedSlotCap: 1, BudgetCell: 1291, ExpiresUnix: 1790000000,
	}
	if mut != nil {
		mut(&cfg)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacymining.ParseConfig(data, sha256.Sum256(data)); err != nil {
		t.Fatalf("fixture config: %v", err)
	}
	return canaryCfg{data: data, hash: sha256.Sum256(data)}
}

// v2FixtureWith is v2Fixture with its config replaced by c.
func v2FixtureWith(t *testing.T, c canaryCfg) *fixture {
	f := v2Fixture(t)
	f.cfg, f.cfgHash = c.data, c.hash
	for i := range f.rows {
		f.rows[i].ConfigSHA256 = c.hash
	}
	return f
}

func ownerOf(t *testing.T, cs *CanarySummary, owner string) OwnerSummary {
	t.Helper()
	if cs == nil || cs.HL2 == nil {
		t.Fatalf("no v2 summary: %+v", cs)
	}
	for _, s := range cs.HL2.Owners {
		if s.Owner == owner {
			return s
		}
	}
	t.Fatalf("owner %s missing from %+v", owner, cs.HL2.Owners)
	return OwnerSummary{}
}

func TestV2OwnerSummary(t *testing.T) {
	f := v2Fixture(t)
	f.writeV2()
	r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
	wantPass(t, r)
	oa := r.Canary.HL2
	if oa == nil || oa.ConfigVersion != 2 || oa.Pending != 1 || oa.OwnerEpochCapCell != 100 || oa.MaxPendingPerOwner != 100 || len(oa.Owners) != 3 {
		t.Fatalf("hl2 %+v", oa)
	}
	cell := legacymining.DefaultRewardCell(10)
	a := ownerOf(t, r.Canary, minerA)
	if a.Proofs != 5 || a.Pending != 1 || a.RewardTxs != 3 || a.Emitted != 2*cell+cell/3 || a.PeakEpoch != 0 || a.PeakEpochEmitted != a.Emitted {
		t.Fatalf("owner A %+v", a)
	}
	if oa.Owners[0].Owner != minerA {
		t.Fatalf("owners not sorted by emission: %+v", oa.Owners)
	}
	notes := strings.Join(oa.Notes, "\n")
	for _, want := range []string{"difficulty_bits 16", "not auditable from the chain", "gross", "deferred-bond"} {
		if !strings.Contains(notes, want) {
			t.Fatalf("notes %q lack %q", notes, want)
		}
	}
}

func TestV2OwnerEpochCap(t *testing.T) {
	// minerA receives two full rewards (6, 8) and a third of one (10):
	// about 8.32 CELL in owner epoch 0.
	t.Run("within cap + 2 rewards", func(t *testing.T) {
		f := v2FixtureWith(t, v2Cfg(t, func(c *legacymining.Config) { c.OwnerEpochCapCell = 2 }))
		f.writeV2()
		wantPass(t, f.audit(func(o *Options) { o.CanaryConfig = f.cfg }))
	})
	t.Run("above cap + 2 rewards", func(t *testing.T) {
		f := v2FixtureWith(t, v2Cfg(t, func(c *legacymining.Config) { c.OwnerEpochCapCell = 1 }))
		f.writeV2()
		r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
		wantFail(t, r, CodeOwnerEpochCap)
		for _, x := range r.Findings {
			if x.Code == CodeOwnerEpochCap && (!strings.Contains(x.Detail, minerA) || x.Height == nil || *x.Height != 10) {
				t.Fatalf("finding %+v", x)
			}
		}
	})

	// Blocks 8630..8650 straddle the owner epoch boundary at 8640.
	epochFixture := func(t *testing.T, c canaryCfg, heights ...uint64) *fixture {
		f := &fixture{t: t, dir: t.TempDir(), origin: 8630, h0: 8632}
		f.chain(8650, "epoch")
		f.boot(c, 8632)
		for _, hgt := range heights {
			f.settle(hgt, c, minerA)
		}
		f.cfg, f.cfgHash = c.data, c.hash
		return f
	}
	// cap 4: an epoch allows 4 + 2*3.565 = 11.13 CELL, three full rewards.
	capped := func(t *testing.T) canaryCfg {
		return v2Cfg(t, func(c *legacymining.Config) { c.OwnerEpochCapCell = 4 })
	}
	t.Run("each epoch counts on its own", func(t *testing.T) {
		c := capped(t)
		f := epochFixture(t, c, 8634, 8636, 8638, 8640, 8642, 8644)
		f.writeV2()
		r := f.audit(func(o *Options) { o.CanaryConfig = c.data })
		wantPass(t, r)
		a := ownerOf(t, r.Canary, minerA)
		if a.RewardTxs != 6 || a.PeakEpoch != 0 {
			t.Fatalf("owner A %+v", a)
		}
	})
	t.Run("a fourth reward in one epoch fails", func(t *testing.T) {
		c := capped(t)
		f := epochFixture(t, c, 8634, 8636, 8638, 8639, 8640, 8642)
		f.writeV2()
		r := f.audit(func(o *Options) { o.CanaryConfig = c.data })
		wantFail(t, r, CodeOwnerEpochCap)
		if n := len(r.Findings); n != 1 || *r.Findings[0].Height != 8639 || !strings.Contains(r.Findings[0].Detail, "owner epoch 0 (heights 0..8639)") {
			t.Fatalf("findings %+v", r.Findings)
		}
	})
	t.Run("a new config hash restarts the count", func(t *testing.T) {
		// Six rewards in epoch 0, three under each config: core's S12
		// counts from the window's first height, so each config is within
		// its cap. Audited as one config, the six would fail.
		x := capped(t)
		y := v2Cfg(t, func(c *legacymining.Config) { c.OwnerEpochCapCell = 4; c.ExpiresUnix++ })
		f := &fixture{t: t, dir: t.TempDir(), origin: 8630, h0: 8632}
		f.chain(8640, "epoch")
		f.boot(x, 8632)
		f.boot(y, 8636)
		for _, hgt := range []uint64{8632, 8633, 8634} {
			f.settle(hgt, x, minerA)
		}
		for _, hgt := range []uint64{8636, 8637, 8638} {
			f.settle(hgt, y, minerA)
		}
		f.cfg, f.cfgHash = x.data, x.hash
		f.writeV2()
		r := f.audit(func(o *Options) { o.CanaryConfig = x.data; o.MoreCanaryConfigs = [][]byte{y.data} })
		wantPass(t, r)
		if r.Canary.Window.String() != "[8632, 8636)" || len(r.OtherCanaries) != 1 || r.OtherCanaries[0].Window.String() != "[8636, tip]" {
			t.Fatalf("windows %v %+v", r.Canary.Window, r.OtherCanaries)
		}
		for _, cs := range []*CanarySummary{r.Canary, r.OtherCanaries[0]} {
			if a := ownerOf(t, cs, minerA); a.RewardTxs != 3 {
				t.Fatalf("owner A %+v", a)
			}
		}
	})
}

func TestV2Pending(t *testing.T) {
	t.Run("per owner", func(t *testing.T) {
		f := v2FixtureWith(t, v2Cfg(t, func(c *legacymining.Config) { c.MaxPendingPerOwner = 1 }))
		f.accept(12, canaryCfg{f.cfg, f.cfgHash}, minerA) // A: pid(4) and this one unpaid
		f.writeV2()
		r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
		wantFail(t, r, CodeOwnerPending)
		if r.Has(CodePendingExceeded) || ownerOf(t, r.Canary, minerA).Pending != 2 {
			t.Fatalf("findings %v", codes(r))
		}
	})
	t.Run("total", func(t *testing.T) {
		f := v2FixtureWith(t, v2Cfg(t, func(c *legacymining.Config) { c.MaxPending = 1; c.MaxPendingPerOwner = 1 }))
		f.accept(12, canaryCfg{f.cfg, f.cfgHash}, minerB)
		f.writeV2()
		r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
		wantFail(t, r, CodePendingExceeded)
		if r.Has(CodeOwnerPending) {
			t.Fatalf("findings %v", codes(r))
		}
	})
	t.Run("paid on the chain, unmarked in the DB, is not pending", func(t *testing.T) {
		f := v2FixtureWith(t, v2Cfg(t, func(c *legacymining.Config) { c.MaxPending = 1; c.MaxPendingPerOwner = 1 }))
		id := f.accept(12, canaryCfg{f.cfg, f.cfgHash}, minerB)
		f.pay(12, 400, minerB, id) // S11 marks it at the next boot
		f.writeV2()
		r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
		wantPass(t, r)
		if !r.Has(CodeDBUnmarked) || r.Canary.HL2.Pending != 1 {
			t.Fatalf("findings %v hl2 %+v", codes(r), r.Canary.HL2)
		}
	})
}

func TestV2Allowlist(t *testing.T) {
	pairs := []legacymining.AllowEntry{
		{MinerAddr: minerA, NodeID: nodeID}, {MinerAddr: minerA, NodeID: "node-aaaa"},
		{MinerAddr: minerB, NodeID: "node-bbbb"}, {MinerAddr: minerC, NodeID: "node-cccc"},
	}
	t.Run("every row allowlisted", func(t *testing.T) {
		f := v2FixtureWith(t, v2Cfg(t, func(c *legacymining.Config) { c.Allowed = pairs }))
		f.writeV2()
		wantPass(t, f.audit(func(o *Options) { o.CanaryConfig = f.cfg }))
	})
	t.Run("a row outside the allowlist", func(t *testing.T) {
		f := v2FixtureWith(t, v2Cfg(t, func(c *legacymining.Config) { c.Allowed = pairs[:3] }))
		f.writeV2()
		r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
		wantFail(t, r, CodeNotAllowlisted)
		if len(r.Findings) != 1 || !strings.Contains(r.Findings[0].Detail, minerC) {
			t.Fatalf("findings %+v", r.Findings)
		}
	})
	t.Run("the miner on another node", func(t *testing.T) {
		f := v2FixtureWith(t, v2Cfg(t, func(c *legacymining.Config) {
			c.Allowed = append(append([]legacymining.AllowEntry{}, pairs[:3]...), legacymining.AllowEntry{MinerAddr: minerC, NodeID: "node-other"})
		}))
		f.writeV2()
		wantFail(t, f.audit(func(o *Options) { o.CanaryConfig = f.cfg }), CodeNotAllowlisted)
	})
}

func TestV2RewardRepeat(t *testing.T) {
	f := v2Fixture(t)
	id := f.accept(10, canaryCfg{f.cfg, f.cfgHash}, minerB)
	tx := rewardTx(500, minerB, 0.001, id) // a second reward to B at 10
	f.at(10).Transactions = append(f.at(10).Transactions, tx)
	f.payments = append(f.payments, legacymining.Payment{ProofID: id, MinerAddr: minerB, Height: 10, TxID: tx.ID})
	f.writeV2()
	r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
	wantFail(t, r, CodeOwnerRewardRepeat)
	// Without a v2 config the rule does not apply (v1 audits are unchanged).
	if r := f.audit(nil); r.Has(CodeOwnerRewardRepeat) {
		t.Fatalf("findings %v", codes(r))
	}
}

// v1ThenV2 is a canary that ran v1 config A (allowlisting minerA) from 5,
// then public v2 config B from 12, paying three owners.
func v1ThenV2(t *testing.T) (*fixture, canaryCfg, canaryCfg) {
	f := &fixture{t: t, dir: t.TempDir(), h0: 5}
	a := newCanaryCfg(minerA, 1291, 3000)
	b := v2Cfg(t, nil)
	f.chain(20, "main")
	f.boot(a, 5)
	f.boot(b, 12)
	for _, hgt := range []uint64{6, 8, 10} {
		f.settle(hgt, a, minerA)
	}
	cell := legacymining.DefaultRewardCell(14)
	for i, m := range []string{minerA, minerB, minerC} {
		id := f.accept(14, b, m)
		tx := rewardTx(600+uint64(i), m, cell/3, id)
		f.at(14).Transactions = append(f.at(14).Transactions, tx)
		f.markPaid(14, id) // the last reward tx in the block: tx
	}
	f.settle(16, b, minerB)
	f.accept(18, b, minerC) // pending
	f.cfg, f.cfgHash = a.data, a.hash
	return f, a, b
}

func TestV1ThenV2Windows(t *testing.T) {
	f, a, b := v1ThenV2(t)
	f.writeV2()

	// The v1 config alone: exactly the HL1 audit of its window.
	r := f.audit(func(o *Options) { o.CanaryConfig = a.data })
	wantPass(t, r)
	if len(r.Findings) != 0 || r.Canary.HL2 != nil || r.OtherCanaries != nil || r.Canary.Window.String() != "[5, 12)" || r.Canary.RewardTxs != 3 {
		t.Fatalf("findings %v canary %+v", r.Findings, r.Canary)
	}
	js, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(js, []byte(`"hl2"`)) || bytes.Contains(js, []byte(`"other_canaries"`)) {
		t.Fatalf("v1 report gained v2 fields: %s", js)
	}

	// Both windows in one run.
	r = f.audit(func(o *Options) { o.CanaryConfig = a.data; o.MoreCanaryConfigs = [][]byte{b.data} })
	wantPass(t, r)
	if len(r.OtherCanaries) != 1 {
		t.Fatalf("other canaries %+v", r.OtherCanaries)
	}
	v2 := r.OtherCanaries[0]
	if v2.Window.String() != "[12, tip]" || v2.RewardTxs != 4 || v2.Proofs != 5 || v2.HL2.Pending != 1 || len(v2.HL2.Owners) != 3 {
		t.Fatalf("v2 canary %+v hl2 %+v", v2, v2.HL2)
	}
	if c := ownerOf(t, v2, minerC); c.Pending != 1 || c.Proofs != 2 || c.RewardTxs != 1 {
		t.Fatalf("owner C %+v", c)
	}
	// The v2 config alone: minerA's v1-era rewards are outside its window.
	r = f.audit(func(o *Options) { o.CanaryConfig = b.data })
	wantPass(t, r)
	if a := ownerOf(t, r.Canary, minerA); a.RewardTxs != 1 {
		t.Fatalf("owner A %+v", a)
	}
	// A v2 reward to minerB is not checked against v1 A's allowlist, but a
	// reward to B sealed in A's window still is.
	f.settle(9, a, minerB)
	f.writeV2Fresh()
	r = f.audit(func(o *Options) { o.CanaryConfig = a.data; o.MoreCanaryConfigs = [][]byte{b.data} })
	wantFail(t, r, CodeNotAllowlisted)
	for _, x := range r.Findings {
		if x.Code == CodeNotAllowlisted && *x.Height != 9 {
			t.Fatalf("finding %+v", x)
		}
	}
}

// writeV2Fresh rewrites the state directory from scratch.
func (f *fixture) writeV2Fresh() {
	f.t.Helper()
	for _, p := range []string{f.dbPath(), f.dbPath() + "-wal", f.dbPath() + "-shm"} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			f.t.Fatal(err)
		}
	}
	f.writeV2()
}

// TestRunCLIV1ThenV2 chains a v1 report into a v1+v2 audit with -prev, and
// checks the repeated -canary-config flag and the summary.
func TestRunCLIV1ThenV2(t *testing.T) {
	f, a, b := v1ThenV2(t)
	f.writeV2()
	dir := t.TempDir()
	pa, pb := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	for p, data := range map[string][]byte{pa: a.data, pb: b.data} {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out1 := filepath.Join(dir, "audit-1.json")
	var so, se bytes.Buffer
	if code := run([]string{"-state-dir", f.dir, "-canary-config", pa, "-out", out1, "-temp-dir", t.TempDir()}, &so, &se); code != exitPass {
		t.Fatalf("exit %d\n%s%s", code, so.String(), se.String())
	}
	if strings.Contains(so.String(), "  v2:") {
		t.Fatalf("v1 summary %q", so.String())
	}

	// Later: more v2 blocks, audited with both configs against the v1 report.
	f.chain(24, "main")
	f.settle(22, b, minerC)
	f.writeV2Fresh()
	so.Reset()
	se.Reset()
	out2 := filepath.Join(dir, "audit-2.json")
	if code := run([]string{"-state-dir", f.dir, "-canary-config", pa, "-canary-config", pb, "-prev", out1, "-out", out2, "-temp-dir", t.TempDir()}, &so, &se); code != exitPass {
		t.Fatalf("exit %d\n%s%s", code, so.String(), se.String())
	}
	sum := so.String()
	for _, want := range []string{"canary " + hexHash(a.hash), "canary " + hexHash(b.hash), "  v2: 3 owners", "  owner " + minerC, "  note: difficulty_bits 20"} {
		if !strings.Contains(sum, want) {
			t.Fatalf("summary lacks %q:\n%s", want, sum)
		}
	}
	data, err := os.ReadFile(out2)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := ParseReport(data)
	if err != nil {
		t.Fatal(err)
	}
	if rep.ServedHighWater == nil || rep.ServedHighWater.Height != 24 || len(rep.OtherCanaries) != 1 || rep.OtherCanaries[0].HL2 == nil {
		t.Fatalf("report %+v", rep)
	}

	// A W regression below the chained high-water still fails.
	f.chain(22, "main")
	f.writeJournal("")
	f.writeWatermark(legacymining.WatermarkFile, 22, f.blocks[22].Hash, legacymining.WatermarkSourceSeed)
	so.Reset()
	se.Reset()
	if code := run([]string{"-state-dir", f.dir, "-canary-config", pa, "-canary-config", pb, "-prev", out2, "-temp-dir", t.TempDir()}, &so, &se); code != exitFail ||
		!strings.Contains(so.String(), CodeWatermarkRegress) {
		t.Fatalf("exit %d\n%s%s", code, so.String(), se.String())
	}

	// The same config twice is a usage error.
	so.Reset()
	se.Reset()
	if code := run([]string{"-state-dir", f.dir, "-canary-config", pa, "-canary-config", pa}, &so, &se); code != exitUsage || !strings.Contains(se.String(), "same content") {
		t.Fatalf("exit %d stderr %q", code, se.String())
	}
}

func hexHash(h legacymining.ConfigHash) string { return hex.EncodeToString(h[:]) }
