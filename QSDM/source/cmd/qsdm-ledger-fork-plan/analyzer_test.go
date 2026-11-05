package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

func TestAnalyzeValidLegacySnapshot(t *testing.T) {
	dir := t.TempDir()
	paths := writeFixture(t, dir, false, false)
	result, err := analyze(analyzeOptions{
		AccountsPath:     paths.accounts,
		ChainPath:        paths.chain,
		EnrollmentPath:   paths.enrollment,
		StakingPath:      paths.staking,
		ActivationHeight: 70_000,
		MinNoticeBlocks:  10,
		Now:              time.Unix(1_700_000_000, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.LedgerChecksPass {
		t.Fatalf("expected checks to pass: %+v", result.Checks)
	}
	if result.Activation.Ready || result.Activation.RuntimeTransitionReady {
		t.Fatal("analysis-only tool must never authorize activation")
	}
	if result.Accounts.LiquidNonFunderDust != "150000000" {
		t.Fatalf("liquid dust=%s", result.Accounts.LiquidNonFunderDust)
	}
	if result.Locked.EnrollmentLockedDust != "100000000" {
		t.Fatalf("enrollment dust=%s", result.Locked.EnrollmentLockedDust)
	}
	if result.Chain.MiningRewardDust != "250000000" {
		t.Fatalf("reward dust=%s", result.Chain.MiningRewardDust)
	}
	if result.Chain.TotalFunderIssuedDust != "250000000" {
		t.Fatalf("issued dust=%s", result.Chain.TotalFunderIssuedDust)
	}
}

func TestAnalyzeRejectsBrokenContinuity(t *testing.T) {
	dir := t.TempDir()
	paths := writeFixture(t, dir, true, false)
	result, err := analyze(analyzeOptions{AccountsPath: paths.accounts, ChainPath: paths.chain, EnrollmentPath: paths.enrollment, StakingPath: paths.staking})
	if err != nil {
		t.Fatal(err)
	}
	if result.LedgerChecksPass {
		t.Fatal("broken chain must fail readiness checks")
	}
	assertCheck(t, result, "chain-contiguous", false)
}

func TestAnalyzeRejectsUnrepresentableAccount(t *testing.T) {
	dir := t.TempDir()
	paths := writeFixture(t, dir, false, true)
	result, err := analyze(analyzeOptions{AccountsPath: paths.accounts, ChainPath: paths.chain, EnrollmentPath: paths.enrollment, StakingPath: paths.staking})
	if err != nil {
		t.Fatal(err)
	}
	if result.LedgerChecksPass {
		t.Fatal("overflowing account must fail readiness checks")
	}
	assertCheck(t, result, "account-balances-convertible", false)
}

// An account that already holds dust has the float mirror the node writes,
// DustToCell(BalanceDust). Flooring that float comes back one dust short for
// some amounts (3 and 29 dust here), so the check must compare with
// DustToCell itself, and still catch a mirror that is off.
func TestAnalyzeDustAccountMirror(t *testing.T) {
	for _, tc := range []struct {
		name    string
		dust    uint64
		balance float64
		want    bool
	}{
		{"3 dust", 3, chain.DustToCell(3), true},
		{"29 dust", 29, chain.DustToCell(29), true},
		{"1.5 CELL", 150_000_000, chain.DustToCell(150_000_000), true},
		{"mirror of 4 dust on 3", 3, chain.DustToCell(4), false},
		{"mirror one float step off", 150_000_000, math.Nextafter(1.5, 2), false},
		{"no mirror", 150_000_000, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := writeFixture(t, t.TempDir(), false, false)
			writeJSON(t, paths.accounts, []chain.Account{
				{Address: chain.MiningRewardFunderAddress, Balance: 1e15, Nonce: 2},
				{Address: "miner", Balance: tc.balance, BalanceDust: tc.dust},
			})
			result, err := analyze(analyzeOptions{AccountsPath: paths.accounts, ChainPath: paths.chain, EnrollmentPath: paths.enrollment, StakingPath: paths.staking})
			if err != nil {
				t.Fatal(err)
			}
			assertCheck(t, result, "account-balances-convertible", tc.want)
		})
	}
}

func TestAnalyzeCountsTaskLockedSupply(t *testing.T) {
	dir := t.TempDir()
	accountsPath := filepath.Join(dir, "qsdm_accounts.json")
	chainPath := filepath.Join(dir, "qsdm_chain.ndjson")
	enrollmentPath := filepath.Join(dir, "qsdm_enrollment.json")
	stakingPath := filepath.Join(dir, "qsdm_staking.json")

	// No genesis treasury (Genesis Policy v1.1): the operator's 6 CELL come
	// from two mining rewards, each inside the schedule's per-block reward.
	genesisTx := &mempool.Tx{ID: "genesis", Sender: chain.MiningRewardFunderAddress, Recipient: chain.MiningRewardFunderAddress, Amount: 1, Nonce: 0}
	genesis := fixtureBlock(0, "", genesisTx)
	reward1 := &mempool.Tx{ID: "solo-reward-1-operator", Sender: chain.MiningRewardFunderAddress, Recipient: "operator", Amount: 3, Nonce: 1, ContractID: chain.MiningRewardContractID}
	block1 := fixtureBlock(1, genesis.Hash, reward1)
	reward2 := &mempool.Tx{ID: "solo-reward-2-operator", Sender: chain.MiningRewardFunderAddress, Recipient: "operator", Amount: 3, Nonce: 2, ContractID: chain.MiningRewardContractID}
	block2 := fixtureBlock(2, block1.Hash, reward2)
	action := chain.TaskAction{
		ID:        "fund-task",
		Sender:    "operator",
		TaskID:    "task",
		Action:    "fund",
		Amount:    5,
		Timestamp: time.Unix(4, 0).UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	fundTx := &mempool.Tx{ID: action.ID, Sender: action.Sender, Nonce: 0, ContractID: chain.TaskContractID, Payload: payload}
	block3 := fixtureBlock(3, block2.Hash, fundTx)
	writeNDJSON(t, chainPath, genesis, block1, block2, block3)
	writeJSON(t, accountsPath, []chain.Account{
		{Address: chain.MiningRewardFunderAddress, Balance: 1e15, Nonce: 3},
		{Address: "operator", Balance: 1, Nonce: 1},
	})
	writeJSON(t, enrollmentPath, map[string]any{"records": []any{}})
	writeJSON(t, stakingPath, map[string]any{"v": 1, "bonds": []any{}, "unbond": []any{}})

	result, err := analyze(analyzeOptions{
		AccountsPath: accountsPath, ChainPath: chainPath,
		EnrollmentPath: enrollmentPath, StakingPath: stakingPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.LedgerChecksPass {
		t.Fatalf("expected checks to pass: %+v", result.Checks)
	}
	if result.Locked.TaskRewardPoolDust != "500000000" {
		t.Fatalf("task reward pool dust=%s", result.Locked.TaskRewardPoolDust)
	}
	if result.Supply.AccountedCirculatingDust != "600000000" {
		t.Fatalf("accounted dust=%s", result.Supply.AccountedCirculatingDust)
	}
}

// Genesis Policy v1.1, clause 1 (decision D1-a): 90 M cap, all mining, no
// genesis treasury. The manifest reports a zero treasury allowance, and
// height-0 funder issuance (the shape of the public chain's 100 CELL
// canonical genesis seed) fails the treasury check until the migration burns
// or classifies it.
func TestAnalyzePolicyCapAndGenesisIssuance(t *testing.T) {
	if chain.CellSupplyCapDust != chain.CellMiningCapDust {
		t.Fatalf("supply cap %d != mining cap %d", chain.CellSupplyCapDust, chain.CellMiningCapDust)
	}
	dir := t.TempDir()
	accountsPath := filepath.Join(dir, "qsdm_accounts.json")
	chainPath := filepath.Join(dir, "qsdm_chain.ndjson")
	enrollmentPath := filepath.Join(dir, "qsdm_enrollment.json")
	stakingPath := filepath.Join(dir, "qsdm_staking.json")

	seed := &mempool.Tx{ID: "genesis-seed", Sender: chain.MiningRewardFunderAddress, Recipient: "operator", Amount: 100, Nonce: 0}
	genesis := fixtureBlock(0, "", seed)
	writeNDJSON(t, chainPath, genesis)
	writeJSON(t, accountsPath, []chain.Account{
		{Address: chain.MiningRewardFunderAddress, Balance: 1e15, Nonce: 1},
		{Address: "operator", Balance: 100},
	})
	writeJSON(t, enrollmentPath, map[string]any{"records": []any{}})
	writeJSON(t, stakingPath, map[string]any{"v": 1, "bonds": []any{}, "unbond": []any{}})

	result, err := analyze(analyzeOptions{
		AccountsPath: accountsPath, ChainPath: chainPath,
		EnrollmentPath: enrollmentPath, StakingPath: stakingPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Supply.SupplyCapDust != "9000000000000000" || result.Supply.MiningCapDust != "9000000000000000" {
		t.Fatalf("supply cap=%s mining cap=%s, want 9e15 each", result.Supply.SupplyCapDust, result.Supply.MiningCapDust)
	}
	if result.Supply.TreasuryCapDust != "0" {
		t.Fatalf("treasury cap=%s, want 0", result.Supply.TreasuryCapDust)
	}
	if result.Chain.TreasuryOrGenesisIssueDust != "10000000000" {
		t.Fatalf("genesis issue dust=%s", result.Chain.TreasuryOrGenesisIssueDust)
	}
	assertCheck(t, result, "treasury-issuance-under-cap", false)
	assertCheck(t, result, "total-issuance-under-cap", true)
	assertCheck(t, result, "accounted-supply-not-above-issued", true)
	if result.LedgerChecksPass {
		t.Fatal("genesis issuance must keep the ledger checks from passing")
	}
}

type fixturePaths struct{ accounts, chain, enrollment, staking string }

func writeFixture(t *testing.T, dir string, breakContinuity, overflowAccount bool) fixturePaths {
	t.Helper()
	accountsPath := filepath.Join(dir, "qsdm_accounts.json")
	chainPath := filepath.Join(dir, "qsdm_chain.ndjson")
	enrollmentPath := filepath.Join(dir, "qsdm_enrollment.json")
	stakingPath := filepath.Join(dir, "qsdm_staking.json")

	reward := &mempool.Tx{ID: "solo-reward-1-miner", Sender: chain.MiningRewardFunderAddress, Recipient: "miner", Amount: 2.5, Nonce: 1, ContractID: chain.MiningRewardContractID}
	// No genesis treasury (Genesis Policy v1.1): the genesis block carries a
	// funder self-transaction, which is bookkeeping, not issuance.
	genesisTx := &mempool.Tx{ID: "genesis", Sender: chain.MiningRewardFunderAddress, Recipient: chain.MiningRewardFunderAddress, Amount: 1, Nonce: 0}
	genesis := fixtureBlock(0, "", genesisTx)
	height := uint64(1)
	prev := genesis.Hash
	if breakContinuity {
		height = 2
		prev = "wrong"
	}
	second := fixtureBlock(height, prev, reward)
	writeNDJSON(t, chainPath, genesis, second)

	minerBalance := 1.5
	if overflowAccount {
		minerBalance = math.MaxFloat64
	}
	writeJSON(t, accountsPath, []chain.Account{
		{Address: chain.MiningRewardFunderAddress, Balance: 1e15, Nonce: 2},
		{Address: "miner", Balance: minerBalance},
	})
	writeJSON(t, enrollmentPath, map[string]any{"records": []map[string]any{{"node_id": "node", "owner": "miner", "stake_dust": uint64(100_000_000)}}})
	writeJSON(t, stakingPath, map[string]any{"v": 1, "bonds": []any{}, "unbond": []any{}})
	return fixturePaths{accountsPath, chainPath, enrollmentPath, stakingPath}
}

func fixtureBlock(height uint64, prev string, tx *mempool.Tx) *chain.Block {
	b := &chain.Block{Height: height, PrevHash: prev, Timestamp: time.Unix(int64(height+1), 0).UTC(), Transactions: []*mempool.Tx{tx}, StateRoot: "fixture-state", ProducerID: "fixture"}
	b.Hash = chain.ComputeBlockHash(b)
	return b
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeNDJSON(t *testing.T, path string, blocks ...*chain.Block) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, block := range blocks {
		if err := enc.Encode(block); err != nil {
			t.Fatal(err)
		}
	}
}

func assertCheck(t *testing.T, result manifest, name string, want bool) {
	t.Helper()
	for _, check := range result.Checks {
		if check.Name == name {
			if check.Passed != want {
				t.Fatalf("check %s=%v, want %v", name, check.Passed, want)
			}
			return
		}
	}
	t.Fatalf("check %s not found", name)
}
