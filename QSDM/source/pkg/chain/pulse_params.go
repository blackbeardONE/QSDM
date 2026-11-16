package chain

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/internal/pulse"
)

// PULSE (Payout, Utility & Ledger Settlement Engine) is the non-custodial mining
// pool rule. A pool member's mining rewards are paid by the producer to a
// keyless per-member escrow account; the pool's signed settlement batch then
// moves them, through the chain itself, to every member in proportion to the
// pool's TIDES share window. Nobody holds a key to an escrow account: only this
// rule (or the member's own reclaim after a timeout) can move CELL out of it.
//
// The rule is off until a release sets pulse_activation_height: with the zero
// value every PULSE transaction is refused, no escrow address is valid as a
// reward recipient, and state roots are byte-identical to before.

// PulseContractID marks PULSE transactions (join, leave, settle, reclaim).
const PulseContractID = "qsdm/pulse/v1"

// PulseEscrowPrefix starts every escrow account: prefix + pool + ":" + owner.
// It is not a hex address, so no key can ever sign for it.
const PulseEscrowPrefix = "qsdm-pulse-escrow-"

// DefaultPulseMaxFeeBPS is the network-wide fee ceiling (3 %, owner decision
// 2026-11-16). A pool's own manifest ceiling can only be lower.
const DefaultPulseMaxFeeBPS uint16 = 300

// DefaultPulseReclaimAfterBlocks is how long an escrowed reward waits for its
// pool before the member may reclaim it: 8,640 blocks, about one day (owner
// decision 2026-11-16). A config value overrides it.
const DefaultPulseReclaimAfterBlocks uint64 = 8640

// DefaultPulsePayeeEpochCapCell is the per-payee PULSE cap per 8,640-block
// epoch: the same 2,000 CELL as the solo per-owner cap (owner decision
// 2026-11-16, owner_epoch_cap_cell of the mining config). A config value
// overrides it.
const DefaultPulsePayeeEpochCapCell float64 = 2000

// PulseParams are the consensus parameters. Every node must load the same
// values (the boot line prints PulseParamsFingerprint).
type PulseParams struct {
	ActivationHeight   uint64
	MaxFeeBPS          uint16
	Pools              []pulse.PoolEntry
	PayeeEpochCapDust  uint64 // per payee per 8,640-block epoch, PULSE payouts only
	ReclaimAfterBlocks uint64 // an unsettled escrowed reward may be reclaimed by its owner after this
}

var pulseParams atomic.Pointer[PulseParams]

// SetPulseParams installs the PULSE parameters (boot only). With an activation
// height set, every other parameter must be valid or the node refuses to start.
func SetPulseParams(p PulseParams) error {
	if p.MaxFeeBPS == 0 {
		p.MaxFeeBPS = DefaultPulseMaxFeeBPS
	}
	if p.ActivationHeight > 0 {
		if err := validatePulseParams(&p); err != nil {
			return err
		}
	}
	cp := p
	cp.Pools = append([]pulse.PoolEntry(nil), p.Pools...)
	pulseParams.Store(&cp)
	return nil
}

func validatePulseParams(p *PulseParams) error {
	if p.MaxFeeBPS > 10000 {
		return errors.New("pulse: max fee above 10000 bps")
	}
	if len(p.Pools) == 0 {
		return errors.New("pulse: an activation height needs at least one authorized pool")
	}
	if p.PayeeEpochCapDust == 0 {
		return errors.New("pulse: an activation height needs a per-payee epoch cap")
	}
	if p.ReclaimAfterBlocks == 0 {
		return errors.New("pulse: an activation height needs reclaim_after_blocks")
	}
	seen := map[pulse.Address]bool{}
	for i := range p.Pools {
		e := &p.Pools[i]
		pkb, err := hex.DecodeString(e.PublicKey)
		if err != nil {
			return fmt.Errorf("pulse: pool %d public key is not hex", i)
		}
		pk, err := pulse.ParsePublicKey(pkb)
		if err != nil {
			return fmt.Errorf("pulse: pool %d: %w", i, err)
		}
		if pulse.AddressOf(pk) != e.ID {
			return fmt.Errorf("pulse: pool %d id is not hex(sha256(public key))", i)
		}
		if seen[e.ID] {
			return fmt.Errorf("pulse: pool %s listed twice", e.ID)
		}
		seen[e.ID] = true
		if e.MaxFeeBPS == 0 || e.MaxFeeBPS > p.MaxFeeBPS {
			e.MaxFeeBPS = p.MaxFeeBPS
		}
	}
	return nil
}

// ParsePulseConfig builds PulseParams from the node config. A pool entry is
// "<pool id>:<ML-DSA-87 public key hex>[:<max fee bps>]". With height 0 the
// other values are ignored (the rule is off).
func ParsePulseConfig(height uint64, maxFeeBPS uint16, pools []string, capCell float64, reclaim uint64) (PulseParams, error) {
	p := PulseParams{ActivationHeight: height, MaxFeeBPS: maxFeeBPS, ReclaimAfterBlocks: reclaim}
	if height == 0 {
		return p, nil
	}
	if capCell < 0 || capCell != capCell {
		return p, errors.New("pulse: pulse_payee_epoch_cap_cell must be a positive number")
	}
	if capCell == 0 {
		capCell = DefaultPulsePayeeEpochCapCell
	}
	if p.ReclaimAfterBlocks == 0 {
		p.ReclaimAfterBlocks = DefaultPulseReclaimAfterBlocks
	}
	p.PayeeEpochCapDust = balanceToDust(capCell)
	for i, s := range pools {
		parts := strings.Split(strings.TrimSpace(s), ":")
		if len(parts) != 2 && len(parts) != 3 {
			return p, fmt.Errorf("pulse: pool %d: want <id>:<public key hex>[:<max fee bps>]", i)
		}
		id, err := pulse.ParseAddress(parts[0])
		if err != nil {
			return p, fmt.Errorf("pulse: pool %d id: %w", i, err)
		}
		e := pulse.PoolEntry{ID: id, PublicKey: strings.ToLower(parts[1])}
		if len(parts) == 3 {
			v, err := strconv.ParseUint(parts[2], 10, 16)
			if err != nil {
				return p, fmt.Errorf("pulse: pool %d max fee: %w", i, err)
			}
			e.MaxFeeBPS = uint16(v)
		}
		p.Pools = append(p.Pools, e)
	}
	return p, nil
}

// PulseParamsSnapshot returns the installed parameters (zero value: off).
func PulseParamsSnapshot() PulseParams {
	if p := pulseParams.Load(); p != nil {
		cp := *p
		cp.Pools = append([]pulse.PoolEntry(nil), p.Pools...)
		return cp
	}
	return PulseParams{MaxFeeBPS: DefaultPulseMaxFeeBPS}
}

// PulseActiveAt reports whether height h is under the PULSE rule.
func PulseActiveAt(h uint64) bool {
	p := pulseParams.Load()
	return p != nil && p.ActivationHeight > 0 && h >= p.ActivationHeight
}

// PulseActivationHeight is the configured height (0 = never).
func PulseActivationHeight() uint64 {
	if p := pulseParams.Load(); p != nil {
		return p.ActivationHeight
	}
	return 0
}

func pulsePool(id string) (*pulse.PoolEntry, bool) {
	p := pulseParams.Load()
	if p == nil {
		return nil, false
	}
	for i := range p.Pools {
		if string(p.Pools[i].ID) == id {
			e := p.Pools[i]
			return &e, true
		}
	}
	return nil, false
}

// PulseParamsFingerprint is printed at boot; it must match on every node.
func PulseParamsFingerprint() string {
	p := PulseParamsSnapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "h=%d fee=%d cap=%d reclaim=%d", p.ActivationHeight, p.MaxFeeBPS, p.PayeeEpochCapDust, p.ReclaimAfterBlocks)
	for _, e := range p.Pools {
		fmt.Fprintf(&b, " pool=%s/%d", e.ID, e.MaxFeeBPS)
	}
	return b.String()
}

// PulseEscrowAddress is the keyless escrow account of one member in one pool.
func PulseEscrowAddress(pool, owner string) string {
	return PulseEscrowPrefix + pool + ":" + owner
}

// ParsePulseEscrow splits an escrow address; ok is false for anything else.
func ParsePulseEscrow(addr string) (pool, owner string, ok bool) {
	rest, found := strings.CutPrefix(addr, PulseEscrowPrefix)
	if !found {
		return "", "", false
	}
	pool, owner, found = strings.Cut(rest, ":")
	if !found {
		return "", "", false
	}
	if _, err := pulse.ParseAddress(pool); err != nil || strings.ToLower(pool) != pool {
		return "", "", false
	}
	if _, err := pulse.ParseAddress(owner); err != nil || strings.ToLower(owner) != owner {
		return "", "", false
	}
	return pool, owner, true
}

// PulseOwnerOf maps a reward recipient to the miner it pays: an escrow account
// pays its member; any other recipient is itself. The legacy-mining ledger
// uses it wherever it compares a reward's recipient with a proof's owner.
func PulseOwnerOf(recipient string) string {
	if _, owner, ok := ParsePulseEscrow(recipient); ok {
		return owner
	}
	return recipient
}
