package main

// HL2 WP-D (minimal hl-audit change; the full audit work is WP-H): a version
// 2 config has no allowlist rule. I6 for v2 is "each recipient is the paid
// proof's row miner_addr", which checkDB already enforces for every version
// (CodeRecipientMismatch). Version 1 audits are unchanged (audit_test.go).

import (
	"crypto/sha256"
	"encoding/json"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
)

const minerC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

// v2Fixture is newFixture with a public-mode v2 config (no allowlist) and a
// block at height 10 that pays three miners, one LMP1 reward tx each. The DB
// is migrated to schema version 2 as an HL2 boot does.
func v2Fixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	cfg := legacymining.Config{
		Version: 2, Allowed: []legacymining.AllowEntry{}, MaxProofsPerMin: 600, MaxProofsPerMinPerOwner: 60,
		MaxProofsTotal: 3000, MaxPending: 600, MaxPendingPerOwner: 100, OwnerEpochCapCell: 100, DifficultyBits: 16,
		RequireOperatorSig: true, RequireFullyBonded: true, BondedSlotCap: 1, BudgetCell: 1291, ExpiresUnix: 1790000000,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.cfg = data
	f.cfgHash = sha256.Sum256(f.cfg)
	for i := range f.rows {
		f.rows[i].ConfigSHA256 = f.cfgHash
	}
	cell := legacymining.DefaultRewardCell(10)
	for i, m := range []string{minerA, minerB, minerC} {
		b := byte(20 + i)
		f.rows = append(f.rows, legacymining.Record{ProofID: pid(b), MinerAddr: m, NodeID: "node-" + m[:4], AttNonce: pid(b + 100),
			WorkHeight: 9, AcceptTip: 9, AcceptedNS: int64(3000 + i), ConfigSHA256: f.cfgHash, ProofJSON: []byte(`{}`)})
		tx := rewardTx(200+uint64(i), m, cell/3, pid(b))
		f.at(10).Transactions = append(f.at(10).Transactions, tx)
		f.payments = append(f.payments, legacymining.Payment{ProofID: pid(b), MinerAddr: m, Height: 10, TxID: tx.ID})
	}
	return f
}

func (f *fixture) writeV2() {
	f.t.Helper()
	f.write()
	st := legacymining.NewSQLiteStoreV2()
	if err := st.Open(f.dbPath(), nil); err != nil {
		f.t.Fatal(err)
	}
	if st.SchemaVersion() != legacymining.StoreUserVersionOperatorKeys {
		f.t.Fatal("not migrated")
	}
	if err := st.Close(); err != nil {
		f.t.Fatal(err)
	}
}

func TestV2CanaryRecipients(t *testing.T) {
	t.Run("three recipients, no allowlist: clean", func(t *testing.T) {
		f := v2Fixture(t)
		f.writeV2()
		r := f.audit(func(o *Options) { o.CanaryConfig = f.cfg })
		wantPass(t, r)
		if r.Has(CodeNotAllowlisted) || r.Canary == nil || r.Canary.RewardTxs != 5 || r.Canary.Proofs != 7 {
			t.Fatalf("findings %v canary %+v", codes(r), r.Canary)
		}
		if r.DB == nil || r.DB.Rows != 7 || r.DB.Paid != 6 {
			t.Fatalf("db %+v", r.DB)
		}
	})
	t.Run("a recipient that is not the row's miner fails", func(t *testing.T) {
		f := v2Fixture(t)
		f.pay(11, 300, minerC, pid(4)) // minerA's pending proof paid to C
		f.writeV2()
		wantFail(t, f.audit(func(o *Options) { o.CanaryConfig = f.cfg }), CodeRecipientMismatch)
	})
}
