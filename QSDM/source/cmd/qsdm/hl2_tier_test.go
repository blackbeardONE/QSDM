package main

// HL2 operator decision A, end to end over the real chain state: a
// deferred-bond enrollment mines at deferred_slot_weight_permille through the
// production wiring (hl1NewCanary, ConfigSlotPolicy, the EnrollmentView over
// *enrollment.InMemoryState), its LMP1 reward txs credit its bond through the
// consensus reward path (chain.EnrollmentApplier.ApplyMiningRewardTx ->
// AccrueBondFromReward), and once the bond reaches 10 CELL the guard gives it
// a full slot with no restart.

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

func TestHL2DeferredBondTierRewardsCreditTheBond(t *testing.T) {
	deferred, bonded := newHL2Wallet(t), newHL2Wallet(t)
	st := enrollment.NewInMemoryState()
	hl2Enroll(t, st, "node-d", deferred.owner, "GPU-d", 0) // bond_mode mining_rewards, nothing locked
	hl2Enroll(t, st, "node-b", bonded.owner, "GPU-b", mining.MinEnrollStakeDust)

	accounts := chain.NewAccountStore()
	accounts.Credit(chain.MiningRewardFunderAddress, 1000)
	legacy := hl2LegacyDir(t)
	cfg := legacymining.Config{
		Version: 2, MaxProofsPerMin: 60, MaxProofsPerMinPerOwner: 10, MaxProofsTotal: 1000, MaxPending: 100,
		MaxPendingPerOwner: 10, OwnerEpochCapCell: 100, DifficultyBits: 24, RequireOperatorSig: true,
		RequireFullyBonded: false, DeferredSlotWeightPermille: 100, BondedSlotCap: 1, BudgetCell: 1000,
		ExpiresUnix: time.Now().Add(time.Hour).Unix(),
	}
	if err := legacymining.CheckModeConfig(legacymining.ModePublic, cfg); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if err := legacymining.CheckSupported(legacymining.ModePublic, cfg); err != nil {
		t.Fatalf("fixture not bootable: %v", err)
	}
	env := legacymining.Env{Mode: legacymining.ModePublic, DBPath: filepath.Join(legacy, legacymining.DBFile)}
	parts := hl1NewCanary(hl1BootConfig{Env: env, Config: cfg}, true, accounts, st)
	if !parts.enabled() {
		t.Fatalf("parts: %s", parts.status())
	}
	blocks := []*chain.Block{{Height: 2, Transactions: []*mempool.Tx{
		{ID: "e1", Sender: deferred.owner, ContractID: enrollment.SignedContractID, PublicKey: hex.EncodeToString(deferred.pub)},
		{ID: "e2", Sender: bonded.owner, ContractID: enrollment.SignedContractID, PublicKey: hex.EncodeToString(bonded.pub)},
	}}}
	if rep := hl2HydrateOperatorKeys(parts, blocks, false); rep.Total != 2 {
		t.Fatalf("hydrate: %+v", rep)
	}
	g := parts.guard

	// The deferred node is admitted, at 100/1000 of a slot.
	if c, err := g.Precheck(hl2Proof(t, deferred, deferred.owner, "node-d", "GPU-d")); err != nil || c.Owner != deferred.owner {
		t.Fatalf("deferred node: %+v, %v", c, err)
	}
	if r := g.OwnerRate(deferred.owner); r != 1 {
		t.Fatalf("deferred OwnerRate = %v, want 1", r)
	}
	if r := g.OwnerRate(bonded.owner); r != 10 {
		t.Fatalf("bonded OwnerRate = %v, want 10", r)
	}

	// Reward txs exactly as blockdriver builds them (funder, reward contract)
	// through the consensus apply path.
	applier := chain.NewEnrollmentApplier(accounts, st)
	nonce := uint64(0)
	reward := func(to string, amount float64) {
		t.Helper()
		tx := &mempool.Tx{
			ID: fmt.Sprintf(legacymining.RewardIDFormat, nonce, to, "0011223344556677"), Sender: chain.MiningRewardFunderAddress,
			Recipient: to, Amount: amount, Nonce: nonce, ContractID: chain.MiningRewardContractID,
			Payload: []byte(legacymining.PayloadTag),
		}
		if err := applier.ApplyMiningRewardTx(tx); err != nil {
			t.Fatalf("ApplyMiningRewardTx: %v", err)
		}
		nonce++
	}
	balance := func(addr string) float64 {
		if a, ok := accounts.Get(addr); ok {
			return a.Balance
		}
		return 0
	}
	stake := func() uint64 {
		r, err := st.Lookup("node-d")
		if err != nil {
			t.Fatal(err)
		}
		return r.StakeDust
	}

	// 6 CELL: all of it is locked into the bond; the owner gets nothing
	// liquid and the node is still in the tier.
	reward(deferred.owner, 6)
	if s := stake(); s != 6*mining.MinEnrollStakeDust/10 || balance(deferred.owner) != 0 {
		t.Fatalf("after 6 CELL: stake %d dust, balance %v", s, balance(deferred.owner))
	}
	if r := g.OwnerRate(deferred.owner); r != 1 {
		t.Fatalf("OwnerRate at 6/10 CELL = %v, want 1", r)
	}
	// A fully bonded owner's reward is not withheld.
	reward(bonded.owner, 3)
	if balance(bonded.owner) != 3 {
		t.Fatalf("bonded owner balance %v, want 3", balance(bonded.owner))
	}
	// 5 CELL: 4 complete the bond, 1 is liquid; the node is now a full slot
	// for the next submission, with no restart.
	reward(deferred.owner, 5)
	if s := stake(); s != mining.MinEnrollStakeDust || balance(deferred.owner) != 1 {
		t.Fatalf("after 11 CELL: stake %d dust, balance %v", s, balance(deferred.owner))
	}
	if info, ok := parts.view.Lookup("node-d"); !ok || !info.FullyBonded || !info.DeferredBond {
		t.Fatalf("view: %+v, %v", info, ok)
	}
	if r := g.OwnerRate(deferred.owner); r != 10 {
		t.Fatalf("OwnerRate once fully bonded = %v, want 10", r)
	}
	if c, err := g.Precheck(hl2Proof(t, deferred, deferred.owner, "node-d", "GPU-d")); err != nil || c.Owner != deferred.owner {
		t.Fatalf("fully bonded node: %+v, %v", c, err)
	}
}
