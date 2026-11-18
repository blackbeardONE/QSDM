package legacymining

// The I7 enrollment policy (2026-11-18 report: one accepted signed enrollment in a local block latched the mining
// guard FROZEN on a producer built from public main, although the enrollment routes are wired there).

import (
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// With the enrollment policy only qsdm/enroll/v2 joins the I7 families; the strict audit (AuditTxFamilies, the zero
// policy) is unchanged.
func TestAuditTxFamiliesEnrollmentPolicy(t *testing.T) {
	open := TxFamilyPolicy{Enrollment: true}
	clean := []*mempool.Tx{
		{ID: "hb", Sender: ltFunder, Recipient: ltFunder},
		{ID: "wt", Sender: ltMinerA, Recipient: ltMinerB, Amount: 1, ContractID: chain.WalletTransferContractID},
		{ID: "task", Sender: ltMinerA, ContractID: chain.TaskContractID},
		{ID: "enroll2", Sender: ltMinerA, ContractID: enrollment.SignedContractID},
	}
	if v := AuditTxFamiliesWith(&chain.Block{Transactions: clean}, open); len(v) != 0 {
		t.Fatalf("open policy, clean block: %+v", v)
	}
	if v := AuditTxFamilies(&chain.Block{Transactions: clean}); len(v) != 1 || v[0].TxID != "enroll2" {
		t.Fatalf("strict audit: %+v, want the v2 enrollment only", v)
	}
	if v := AuditTxFamiliesWith(&chain.Block{Transactions: clean}, TxFamilyPolicy{}); len(v) != 1 || v[0].TxID != "enroll2" {
		t.Fatalf("zero policy: %+v", v)
	}
	still := []*mempool.Tx{
		{ID: "enroll1", Sender: ltMinerA, ContractID: enrollment.ContractID},
		{ID: "enroll2-case", Sender: ltMinerA, ContractID: "QSDM/enroll/v2"},
		{ID: "enroll3", Sender: ltMinerA, ContractID: "qsdm/enroll/v3"},
		{ID: "stream", Sender: ltMinerA, Recipient: ltMinerB, ContractID: chain.StreamContractID},
		{ID: "plain", Sender: ltMinerA, Recipient: ltMinerB, Amount: 1},
		{ID: "foreign-reward", Sender: ltMinerA, Recipient: ltMinerB, Amount: 1, ContractID: chain.MiningRewardContractID},
		nil,
	}
	if v := AuditTxFamiliesWith(&chain.Block{Transactions: still}, open); len(v) != len(still) {
		t.Fatalf("open policy violations %+v, want %d", v, len(still))
	}
	if AuditTxFamiliesWith(nil, open) != nil {
		t.Fatal("nil block")
	}
}

// The canary Ledger audits durable local blocks with its configured policy: with it, a block carrying a signed
// enrollment is clean; without it, the same block trips the I7 FREEZE.
func TestLedgerEnrollmentFamilyPolicy(t *testing.T) {
	for _, open := range []bool{true, false} {
		t.Run(map[bool]string{true: "policy open", false: "strict"}[open], func(t *testing.T) {
			h := newLedgerHarnessRaw(t)
			l, err := NewLedger(LedgerConfig{Store: h.s, Guard: h.g, Accounts: h.accts, RewardCell: h.rewardCell,
				Families: TxFamilyPolicy{Enrollment: open}})
			if err != nil {
				t.Fatal(err)
			}
			h.l = l
			if err := h.l.Init(h.initState(Totals{ConfigSHA256: ltHash})); err != nil {
				t.Fatal(err)
			}
			_, err = h.seal(true, &mempool.Tx{ID: "enroll2", Sender: ltMinerC, ContractID: enrollment.SignedContractID})
			if open {
				if err != nil {
					t.Fatalf("seal: %v", err)
				}
				h.noFreeze()
				return
			}
			f := h.freezes()
			if err == nil || len(f) == 0 || !strings.HasPrefix(f[0], CauseTxFamily) {
				t.Fatalf("strict: err=%v freezes=%q, want the I7 FREEZE", err, f)
			}
		})
	}
}
