package main

import (
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

// 2026-11-18 report: the seal hook's I7 audit (H8 (a)) froze mining on one accepted signed enrollment. With the
// enrollment policy main sets once the enrollment routes are wired, signed enroll and unenroll txs are clean; the
// unsigned v1 contract still freezes, and the strict hook is unchanged.
func TestHL1HookEnrollmentFamilyPolicy(t *testing.T) {
	enroll := &mempool.Tx{ID: "e2", Sender: "a", ContractID: enrollment.SignedContractID}
	unenroll := &mempool.Tx{ID: "u2", Sender: "a", ContractID: enrollment.SignedContractID}
	v1 := &mempool.Tx{ID: "e1", Sender: "a", ContractID: enrollment.ContractID}
	for _, tc := range []struct {
		name   string
		open   bool
		local  bool
		txs    []*mempool.Tx
		count  uint64
		freeze bool
	}{
		{"strict: v2 enrollment freezes", false, true, []*mempool.Tx{hl1Heartbeat(1), enroll, unenroll}, 2, true},
		{"policy: v2 enroll and unenroll are clean", true, true, []*mempool.Tx{hl1Heartbeat(1), enroll, unenroll}, 0, false},
		{"policy: v1 enrollment still freezes", true, true, []*mempool.Tx{hl1Heartbeat(1), enroll, v1}, 1, true},
		{"policy: non-local block is not audited", true, false, []*mempool.Tx{enroll}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &hl1Recorder{}
			h, guard := hl1TestHook(rec, &hl1FailStopRecord{}, &hl1DurableTip{}, true)
			if tc.open {
				h.Families = legacymining.TxFamilyPolicy{Enrollment: true}
			}
			h.LocalSeal.Store(tc.local)
			h.OnSealedBlock(hl1TestBlock(5, tc.txs...))
			if got := h.Metrics.unexpected.Load(); got != tc.count {
				t.Fatalf("hl1_unexpected_tx_family_total = %d, want %d", got, tc.count)
			}
			fz := guard.Freezes()
			if tc.freeze != (len(fz) == 1) || (tc.freeze && !strings.HasPrefix(fz[0], legacymining.CauseTxFamily+":")) {
				t.Fatalf("freezes = %q, want freeze=%v", fz, tc.freeze)
			}
		})
	}
}
