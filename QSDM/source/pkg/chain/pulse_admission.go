package chain

import (
	"encoding/hex"
	"fmt"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/internal/pulse"
)

// pulseAdmissionState lets the stateless PulseAdmissionChecker refuse, at the
// mempool, two PULSE txs that block production would refuse anyway: a
// settlement whose fee is above the pool's ceiling, and a reclaim of an escrowed
// reward that is not yet due. Without it such a tx answered 200 and only took a
// mempool slot until the next block refused it (found in the 2026-11-16 end-to-end
// test). The check is the same arithmetic as settle / reclaim; anything it cannot
// decide (an unknown escrow, a wrong signature) is left to block production, so
// admission never refuses a tx a block would accept.
type pulseAdmissionState struct {
	store      *PulseStateStore
	nextHeight func() uint64
}

var pulseAdmission atomic.Pointer[pulseAdmissionState]

// SetPulseAdmissionState installs the live PULSE state and the height the next
// block will have (BlockProducer.TipHeight()+1). nil store or func removes it.
func SetPulseAdmissionState(store *PulseStateStore, nextHeight func() uint64) {
	if store == nil || nextHeight == nil {
		pulseAdmission.Store(nil)
		return
	}
	pulseAdmission.Store(&pulseAdmissionState{store: store, nextHeight: nextHeight})
}

// pulseAdmissionPrecheck returns an error only when the tx is certain to be
// refused in the next block.
func pulseAdmissionPrecheck(sender string, a PulseAction) error {
	st := pulseAdmission.Load()
	if st == nil {
		return nil
	}
	switch a.Op {
	case "settle":
		body, err := hex.DecodeString(a.Batch)
		if err != nil {
			return ErrPulseBadTx
		}
		b, err := pulse.DecodeBatch(body)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrPulseBadTx, err)
		}
		maxFee := PulseParamsSnapshot().MaxFeeBPS
		if e, ok := pulsePool(string(b.PoolID)); ok && e.MaxFeeBPS != 0 && e.MaxFeeBPS < maxFee {
			maxFee = e.MaxFeeBPS
		}
		if b.FeeBPS > maxFee {
			return fmt.Errorf("%w: fee %d bps above the ceiling %d", ErrPulseMismatch, b.FeeBPS, maxFee)
		}
	case "reclaim":
		h := st.nextHeight()
		after := PulseParamsSnapshot().ReclaimAfterBlocks
		st.store.mu.RLock()
		defer st.store.mu.RUnlock()
		for _, id := range a.Proofs {
			e, ok := st.store.escrow[id]
			if !ok || e.Owner != sender {
				continue // block production decides (it may be in flight)
			}
			if h < e.Height || h-e.Height < after {
				return ErrPulseNotDue
			}
		}
	}
	return nil
}
