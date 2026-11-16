package v2wiring_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/v2wiring"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Wire installs the PULSE store on the applier and the PULSE admission layer next to the QSDM-20 one; the
// layer stays after ReinstallAdmissionGate (what main() calls).
func TestWire_Pulse(t *testing.T) {
	r := buildRig(t, 20)
	if r.w.PulseState == nil || r.w.Aware.PulseStateStore() != r.w.PulseState {
		t.Fatal("Wire did not install the PULSE store on the applier")
	}
	check := func(stage string) {
		t.Helper()
		join := &mempool.Tx{ID: "pulse-" + stage, Sender: strings.Repeat("a", 64), ContractID: chain.PulseContractID,
			Payload: []byte(`{"op":"join","pool":"` + strings.Repeat("b", 64) + `"}`)}
		if err := r.pool.Add(join); !errors.Is(err, chain.ErrPulseInactive) {
			t.Fatalf("%s: a PULSE tx with the rule off: %v, want ErrPulseInactive from the PULSE layer", stage, err)
		}
		escrow := chain.PulseEscrowAddress(strings.Repeat("b", 64), strings.Repeat("c", 64))
		toEscrow := &mempool.Tx{ID: "escrow-" + stage, Sender: tAlice, Recipient: escrow, Amount: 1, Fee: 0.001, Nonce: 0}
		if err := r.pool.Add(toEscrow); err == nil || !strings.Contains(err.Error(), "escrow accounts receive only") {
			t.Fatalf("%s: a transfer to a PULSE escrow account: %v", stage, err)
		}
	}
	check("wire")
	v2wiring.ReinstallAdmissionGate(r.pool, nil)
	check("reinstall")
}
