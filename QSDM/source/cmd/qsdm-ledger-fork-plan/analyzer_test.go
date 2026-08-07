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
	if result.Accounts.LiquidNonFunderDust != "250000000" {
		t.Fatalf("liquid dust=%s", result.Accounts.LiquidNonFunderDust)
	}
	if result.Locked.EnrollmentLockedDust != "100000000" {
		t.Fatalf("enrollment dust=%s", result.Locked.EnrollmentLockedDust)
	}
	if result.Chain.MiningRewardDust != "250000000" {
		t.Fatalf("reward dust=%s", result.Chain.MiningRewardDust)
	}
	if result.Chain.TotalFunderIssuedDust != "350000000" {
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

func TestAnalyzeCountsTaskLockedSupply(t *testing.T) {
	dir := t.TempDir()
	accountsPath := filepath.Join(dir, "qsdm_accounts.json")
	chainPath := filepath.Join(dir, "qsdm_chain.ndjson")
	enrollmentPath := filepath.Join(dir, "qsdm_enrollment.json")
	stakingPath := filepath.Join(dir, "qsdm_staking.json")

	genesisTx := &mempool.Tx{ID: "genesis", Sender: chain.MiningRewardFunderAddress, Recipient: "operator", Amount: 6, Nonce: 0}
	genesis := fixtureBlock(0, "", genesisTx)
	action := chain.TaskAction{
		ID:        "fund-task",
		Sender:    "operator",
		TaskID:    "task",
		Action:    "fund",
		Amount:    5,
		Timestamp: time.Unix(2, 0).UTC().Format(time.RFC3339Nano),
	}
	payload, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	fundTx := &mempool.Tx{ID: action.ID, Sender: action.Sender, Nonce: 0, ContractID: chain.TaskContractID, Payload: payload}
	second := fixtureBlock(1, genesis.Hash, fundTx)
	writeNDJSON(t, chainPath, genesis, second)
	writeJSON(t, accountsPath, []chain.Account{
		{Address: chain.MiningRewardFunderAddress, Balance: 1e15, Nonce: 1},
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

type fixturePaths struct{ accounts, chain, enrollment, staking string }

func writeFixture(t *testing.T, dir string, breakContinuity, overflowAccount bool) fixturePaths {
	t.Helper()
	accountsPath := filepath.Join(dir, "qsdm_accounts.json")
	chainPath := filepath.Join(dir, "qsdm_chain.ndjson")
	enrollmentPath := filepath.Join(dir, "qsdm_enrollment.json")
	stakingPath := filepath.Join(dir, "qsdm_staking.json")

	reward := &mempool.Tx{ID: "solo-reward-1-miner", Sender: chain.MiningRewardFunderAddress, Recipient: "miner", Amount: 2.5, Nonce: 1, ContractID: chain.MiningRewardContractID}
	genesisTx := &mempool.Tx{ID: "genesis", Sender: chain.MiningRewardFunderAddress, Recipient: "treasury", Amount: 1, Nonce: 0}
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
		{Address: "treasury", Balance: 1},
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
