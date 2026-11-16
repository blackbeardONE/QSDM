package pulse

import (
	"errors"
	"math/big"
	"sort"
)

// DustPerCell is QSDM's unit: 1 CELL = 100,000,000 dust.
const DustPerCell uint64 = 100_000_000

// Payout is one direct transfer the chain makes for a block.
type Payout struct {
	To     Address
	Amount uint64 // dust
	Kind   string // "member", "fee"
}

// SplitInput is everything one block's settlement needs.
type SplitInput struct {
	Pooled    uint64             // dust: the block reward of the batch's member proofs
	FeeBPS    uint16             // 0..10000
	FeeTo     Address            // fee recipient (receives dust too)
	FloorDust uint64             // balances below the floor carry forward
	Weights   []Weight           // the batch's TIDES weights, sorted, unique
	Owed      map[Address]uint64 // carried balances before this block (pool's members)
	CapLeft   func(Address) uint64
}

// SplitResult is what the chain pays now and what it carries.
type SplitResult struct {
	Payouts []Payout           // sorted by address, fee recipient included
	Owed    map[Address]uint64 // carried balances after this block (no zeros)
	Fee     uint64             // the fee part (before dust)
	Dust    uint64             // rounding + forfeited sub-floor balances, paid to FeeTo
}

// Split is the PULSE per-block rule. It conserves value exactly:
// Pooled + sum(old Owed) == sum(Payouts) + sum(new Owed).
//  1. fee = Pooled * FeeBPS / 10000 (rounded down);
//  2. each member's credit = (Pooled - fee) * weight / total weight (rounded
//     down); the rounding remainder is dust;
//  3. balance = carried + credit. A balance at or above the floor is paid up
//     to the owner's remaining cap; anything over the cap stays owed. A
//     balance under the floor carries forward;
//  4. an owner with a carried balance but no weight in this window has left
//     the window: a balance at or above the floor is paid (cap applies), a
//     smaller one is forfeited as dust;
//  5. the fee recipient gets fee + dust, up to its own cap; the rest stays owed.
func Split(in SplitInput) (*SplitResult, error) {
	if in.FeeBPS > 10000 {
		return nil, errors.New("pulse: fee above 10000 bps")
	}
	if _, err := in.FeeTo.Bytes(); err != nil {
		return nil, errors.New("pulse: fee recipient required")
	}
	if in.CapLeft == nil {
		return nil, errors.New("pulse: cap function required")
	}
	var total uint64
	for i, w := range in.Weights {
		if w.Weight == 0 || (i > 0 && in.Weights[i-1].Owner >= w.Owner) {
			return nil, errors.New("pulse: weights must be positive, sorted and unique")
		}
		if total+w.Weight < total {
			return nil, errors.New("pulse: weight sum overflows")
		}
		total += w.Weight
	}
	if len(in.Weights) == 0 {
		return nil, errors.New("pulse: no weights")
	}
	fee := uint64(new(big.Int).Div(new(big.Int).Mul(new(big.Int).SetUint64(in.Pooled), big.NewInt(int64(in.FeeBPS))), big.NewInt(10000)).Uint64())
	dist := in.Pooled - fee
	var credited, dust uint64
	credit := map[Address]uint64{}
	for _, w := range in.Weights {
		c := new(big.Int).Div(new(big.Int).Mul(new(big.Int).SetUint64(dist), new(big.Int).SetUint64(w.Weight)), new(big.Int).SetUint64(total)).Uint64()
		credit[w.Owner] = c
		credited += c
	}
	dust += dist - credited

	owners := map[Address]bool{}
	for o := range in.Owed {
		owners[o] = true
	}
	for o := range credit {
		owners[o] = true
	}
	delete(owners, in.FeeTo) // the fee recipient is settled last
	sorted := make([]Address, 0, len(owners))
	for o := range owners {
		sorted = append(sorted, o)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	res := &SplitResult{Owed: map[Address]uint64{}, Fee: fee}
	pay := map[Address]uint64{}
	for _, o := range sorted {
		_, inWindow := credit[o]
		bal := in.Owed[o] + credit[o]
		if bal == 0 {
			continue
		}
		if bal < in.FloorDust {
			if inWindow {
				res.Owed[o] = bal
			} else {
				dust += bal
			}
			continue
		}
		p := bal
		if c := in.CapLeft(o); p > c {
			p = c
		}
		pay[o] = p
		if bal-p > 0 {
			res.Owed[o] = bal - p
		}
	}
	feeBal := in.Owed[in.FeeTo] + credit[in.FeeTo] + fee + dust
	p := feeBal
	if c := in.CapLeft(in.FeeTo); p > c {
		p = c
	}
	pay[in.FeeTo] += p
	if feeBal-p > 0 {
		res.Owed[in.FeeTo] = feeBal - p
	}
	res.Dust = dust
	for o, a := range pay {
		if a == 0 {
			continue
		}
		kind := "member"
		if o == in.FeeTo {
			kind = "fee"
		}
		res.Payouts = append(res.Payouts, Payout{To: o, Amount: a, Kind: kind})
	}
	sort.Slice(res.Payouts, func(i, j int) bool { return res.Payouts[i].To < res.Payouts[j].To })
	return res, nil
}

// Conserved checks Pooled + old owed == payouts + new owed.
func Conserved(in SplitInput, r *SplitResult) bool {
	var a, b uint64
	a = in.Pooled
	for _, v := range in.Owed {
		a += v
	}
	for _, p := range r.Payouts {
		b += p.Amount
	}
	for _, v := range r.Owed {
		b += v
	}
	return a == b
}
