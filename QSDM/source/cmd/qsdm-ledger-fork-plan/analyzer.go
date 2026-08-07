package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
)

const (
	manifestSchemaVersion = 1
	maxBlockLineBytes     = 16 * 1024 * 1024
	defaultNoticeBlocks   = 60_480 // Seven days at the 10-second target.
)

type analyzeOptions struct {
	AccountsPath     string
	ChainPath        string
	EnrollmentPath   string
	StakingPath      string
	BaselinePath     string
	ActivationHeight uint64
	MinNoticeBlocks  uint64
	Now              time.Time
}

type fileEvidence struct {
	Name       string `json:"name"`
	SizeBytes  int64  `json:"size_bytes"`
	ModifiedAt string `json:"modified_at"`
	SHA256     string `json:"sha256"`
}

type checkResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
}

type accountSummary struct {
	Count                         int    `json:"count"`
	NonFunderCount                int    `json:"non_funder_count"`
	LiquidNonFunderDust           string `json:"liquid_non_funder_dust"`
	SyntheticFunderPresent        bool   `json:"synthetic_funder_present"`
	SyntheticFunderBalanceCell    string `json:"synthetic_funder_balance_cell,omitempty"`
	SyntheticFunderNonce          uint64 `json:"synthetic_funder_nonce,omitempty"`
	PreexistingDustAccountCount   int    `json:"preexisting_dust_account_count"`
	FlooredFractionalAccountCount int    `json:"floored_fractional_account_count"`
}

type chainSummary struct {
	Blocks                       uint64 `json:"blocks"`
	Transactions                 uint64 `json:"transactions"`
	GenesisHash                  string `json:"genesis_hash,omitempty"`
	GenesisStateRoot             string `json:"genesis_state_root,omitempty"`
	TipHeight                    uint64 `json:"tip_height"`
	TipHash                      string `json:"tip_hash,omitempty"`
	FunderTransactions           uint64 `json:"funder_transactions"`
	FunderSelfTransactions       uint64 `json:"funder_self_transactions"`
	MiningRewardTransactions     uint64 `json:"mining_reward_transactions"`
	LegacyMiningRewardTxs        uint64 `json:"legacy_mining_reward_transactions"`
	UnclassifiedFunderIssueTxs   uint64 `json:"unclassified_funder_issue_transactions"`
	MiningRewardDust             string `json:"mining_reward_dust"`
	TreasuryOrGenesisIssueDust   string `json:"treasury_or_genesis_issue_dust"`
	UnclassifiedFunderIssueDust  string `json:"unclassified_funder_issue_dust"`
	TotalFunderIssuedDust        string `json:"total_funder_issued_dust"`
	TransactionFeesBurnedDust    string `json:"transaction_fees_burned_dust"`
	ScheduleMaximumRewardDust    string `json:"schedule_maximum_reward_dust"`
	RewardScheduleViolationCount uint64 `json:"reward_schedule_violation_count"`
	TaskActionTransactions       uint64 `json:"task_action_transactions"`
	StreamActionTransactions     uint64 `json:"stream_action_transactions"`
	ContractReplayFailures       uint64 `json:"contract_replay_failures"`
}

type lockedSummary struct {
	EnrollmentRecords     int    `json:"enrollment_records"`
	EnrollmentLockedDust  string `json:"enrollment_locked_dust"`
	StakingBondCount      int    `json:"staking_bond_count"`
	StakingBondedDust     string `json:"staking_bonded_dust"`
	StakingUnbondCount    int    `json:"staking_unbond_count"`
	StakingUnbondDust     string `json:"staking_unbond_dust"`
	TaskCount             int    `json:"task_count"`
	TaskStakeDust         string `json:"task_stake_dust"`
	TaskRewardPoolDust    string `json:"task_reward_pool_dust"`
	TaskPendingRewardDust string `json:"task_pending_reward_dust"`
	StreamCount           int    `json:"stream_count"`
	StreamEscrowDust      string `json:"stream_escrow_dust"`
}

type supplySummary struct {
	SupplyCapDust              string `json:"supply_cap_dust"`
	MiningCapDust              string `json:"mining_cap_dust"`
	TreasuryCapDust            string `json:"treasury_cap_dust"`
	ChainIssuedDust            string `json:"chain_issued_dust"`
	AccountedCirculatingDust   string `json:"accounted_circulating_dust"`
	UnaccountedOrBurnedDust    string `json:"unaccounted_or_burned_dust"`
	AccountedExcessDust        string `json:"accounted_excess_dust"`
	RemainingIssueCapacityDust string `json:"remaining_issue_capacity_dust"`
}

type activationSummary struct {
	RequestedHeight        uint64   `json:"requested_height,omitempty"`
	MinimumNoticeBlocks    uint64   `json:"minimum_notice_blocks"`
	EarliestSafeHeight     uint64   `json:"earliest_safe_height"`
	HeightMeetsNotice      bool     `json:"height_meets_notice"`
	RuntimeTransitionReady bool     `json:"runtime_transition_ready"`
	Ready                  bool     `json:"ready"`
	Blockers               []string `json:"blockers"`
}

type comparisonSummary struct {
	BaselineManifest           fileEvidence `json:"baseline_manifest"`
	BaselineGeneratedAt        string       `json:"baseline_generated_at"`
	BaselineTipHeight          uint64       `json:"baseline_tip_height"`
	HeightDelta                string       `json:"height_delta"`
	ChainIssuedDeltaDust       string       `json:"chain_issued_delta_dust"`
	AccountedSupplyDeltaDust   string       `json:"accounted_supply_delta_dust"`
	AccountedExcessDeltaDust   string       `json:"accounted_excess_delta_dust"`
	AccountedExcessTrend       string       `json:"accounted_excess_trend"`
	BaselineLedgerChecksPassed bool         `json:"baseline_ledger_checks_passed"`
	CurrentLedgerChecksPassed  bool         `json:"current_ledger_checks_passed"`
}

type manifest struct {
	SchemaVersion    int                `json:"schema_version"`
	Mode             string             `json:"mode"`
	GeneratedAt      string             `json:"generated_at"`
	Privacy          string             `json:"privacy"`
	Inputs           []fileEvidence     `json:"inputs"`
	Accounts         accountSummary     `json:"accounts"`
	Chain            chainSummary       `json:"chain"`
	Locked           lockedSummary      `json:"locked"`
	Supply           supplySummary      `json:"supply"`
	Checks           []checkResult      `json:"checks"`
	LedgerChecksPass bool               `json:"ledger_checks_pass"`
	Comparison       *comparisonSummary `json:"comparison,omitempty"`
	Activation       activationSummary  `json:"activation"`
}

type analysisState struct {
	manifest manifest
	failed   bool
}

func (s *analysisState) check(name string, passed bool, detail string) {
	s.manifest.Checks = append(s.manifest.Checks, checkResult{Name: name, Passed: passed, Detail: detail})
	if !passed {
		s.failed = true
	}
}

func analyze(opts analyzeOptions) (manifest, error) {
	if strings.TrimSpace(opts.AccountsPath) == "" || strings.TrimSpace(opts.ChainPath) == "" {
		return manifest{}, errors.New("accounts and chain paths are required")
	}
	if opts.MinNoticeBlocks == 0 {
		opts.MinNoticeBlocks = defaultNoticeBlocks
	}
	if opts.Now.IsZero() {
		opts.Now = time.Now().UTC()
	}
	if opts.EnrollmentPath == "" {
		opts.EnrollmentPath = filepath.Join(filepath.Dir(opts.AccountsPath), "qsdm_enrollment.json")
	}
	if opts.StakingPath == "" {
		opts.StakingPath = filepath.Join(filepath.Dir(opts.AccountsPath), "qsdm_staking.json")
	}

	s := analysisState{manifest: manifest{
		SchemaVersion: manifestSchemaVersion,
		Mode:          "read_only_analysis",
		GeneratedAt:   opts.Now.UTC().Format(time.RFC3339Nano),
		Privacy:       "aggregate-only; wallet, node, GPU, and key identifiers are omitted",
	}}

	accounts, accountsFile, accountsStable, accountErrs, err := inspectAccounts(opts.AccountsPath)
	if err != nil {
		return manifest{}, err
	}
	s.manifest.Inputs = append(s.manifest.Inputs, accountsFile)
	s.manifest.Accounts = accounts.summary
	s.check("accounts-file-stable", accountsStable, "account snapshot did not change while it was read")
	s.check("account-balances-convertible", len(accountErrs) == 0, joinProblems(accountErrs, "all non-funder balances convert deterministically to dust"))

	locked, enrollmentFile, enrollmentStable, enrollmentErrs, err := inspectEnrollment(opts.EnrollmentPath)
	if err != nil {
		return manifest{}, err
	}
	s.manifest.Inputs = append(s.manifest.Inputs, enrollmentFile)
	s.manifest.Locked.EnrollmentRecords = locked.records
	s.manifest.Locked.EnrollmentLockedDust = dustString(locked.dust)
	s.check("enrollment-file-stable", enrollmentStable, "enrollment snapshot did not change while it was read")
	s.check("enrollment-balances-valid", len(enrollmentErrs) == 0, joinProblems(enrollmentErrs, "all enrollment bonds fit in the capped ledger"))

	staking, stakingFile, stakingStable, stakingErrs, err := inspectStaking(opts.StakingPath)
	if err != nil {
		return manifest{}, err
	}
	s.manifest.Inputs = append(s.manifest.Inputs, stakingFile)
	s.manifest.Locked.StakingBondCount = staking.bondCount
	s.manifest.Locked.StakingBondedDust = dustString(staking.bondedDust)
	s.manifest.Locked.StakingUnbondCount = staking.unbondCount
	s.manifest.Locked.StakingUnbondDust = dustString(staking.unbondDust)
	s.check("staking-file-stable", stakingStable, "staking snapshot did not change while it was read")
	s.check("staking-balances-convertible", len(stakingErrs) == 0, joinProblems(stakingErrs, "all staking balances convert deterministically to dust"))

	chainData, chainFile, chainStable, chainChecks, err := inspectChain(opts.ChainPath)
	if err != nil {
		return manifest{}, err
	}
	s.manifest.Inputs = append(s.manifest.Inputs, chainFile)
	s.manifest.Chain = chainData.summary
	s.manifest.Locked.TaskCount = chainData.taskCount
	s.manifest.Locked.TaskStakeDust = dustString(chainData.taskStakeDust)
	s.manifest.Locked.TaskRewardPoolDust = dustString(chainData.taskRewardPoolDust)
	s.manifest.Locked.TaskPendingRewardDust = dustString(chainData.taskPendingRewardDust)
	s.manifest.Locked.StreamCount = chainData.streamCount
	s.manifest.Locked.StreamEscrowDust = dustString(chainData.streamEscrowDust)
	s.check("chain-file-stable", chainStable, "chain journal did not change while it was scanned")
	for _, c := range chainChecks {
		s.check(c.Name, c.Passed, c.Detail)
	}

	const treasuryCapDust = chain.CellSupplyCapDust - chain.CellMiningCapDust
	issued := chainData.totalIssuedDust
	accounted, overflow := addMany(
		accounts.liquidDust,
		locked.dust,
		staking.bondedDust,
		staking.unbondDust,
		chainData.taskStakeDust,
		chainData.taskRewardPoolDust,
		chainData.taskPendingRewardDust,
		chainData.streamEscrowDust,
	)
	s.check("accounted-supply-sum", !overflow, "liquid and locked balances fit in uint64 dust")
	s.check("synthetic-funder-present", accounts.summary.SyntheticFunderPresent, "legacy source snapshot contains the funder that the transition must remove")
	s.check("funder-nonce-reconciles", accounts.summary.SyntheticFunderPresent && accounts.summary.SyntheticFunderNonce == chainData.summary.FunderTransactions,
		fmt.Sprintf("snapshot nonce=%d, chain funder transactions=%d", accounts.summary.SyntheticFunderNonce, chainData.summary.FunderTransactions))
	s.check("no-unclassified-funder-issuance", chainData.unclassifiedDust == 0,
		fmt.Sprintf("unclassified issue transactions=%d", chainData.summary.UnclassifiedFunderIssueTxs))
	s.check("mining-issuance-under-cap", chainData.miningDust <= chain.CellMiningCapDust,
		fmt.Sprintf("issued=%s cap=%s dust", dustString(chainData.miningDust), dustString(chain.CellMiningCapDust)))
	s.check("treasury-issuance-under-cap", chainData.treasuryDust <= treasuryCapDust,
		fmt.Sprintf("issued=%s cap=%s dust", dustString(chainData.treasuryDust), dustString(treasuryCapDust)))
	s.check("total-issuance-under-cap", issued <= chain.CellSupplyCapDust,
		fmt.Sprintf("issued=%s cap=%s dust", dustString(issued), dustString(chain.CellSupplyCapDust)))
	s.check("accounted-supply-not-above-issued", !overflow && accounted <= issued,
		fmt.Sprintf("accounted=%s issued=%s dust", dustString(accounted), dustString(issued)))

	unaccounted := uint64(0)
	excess := uint64(0)
	if !overflow && issued >= accounted {
		unaccounted = issued - accounted
	} else if !overflow {
		excess = accounted - issued
	}
	remaining := uint64(0)
	if issued <= chain.CellSupplyCapDust {
		remaining = chain.CellSupplyCapDust - issued
	}
	s.manifest.Supply = supplySummary{
		SupplyCapDust:              dustString(chain.CellSupplyCapDust),
		MiningCapDust:              dustString(chain.CellMiningCapDust),
		TreasuryCapDust:            dustString(treasuryCapDust),
		ChainIssuedDust:            dustString(issued),
		AccountedCirculatingDust:   dustString(accounted),
		UnaccountedOrBurnedDust:    dustString(unaccounted),
		AccountedExcessDust:        dustString(excess),
		RemainingIssueCapacityDust: dustString(remaining),
	}
	if opts.BaselinePath != "" {
		comparison, checks, err := compareManifest(opts.BaselinePath, s.manifest)
		if err != nil {
			return manifest{}, fmt.Errorf("compare baseline manifest: %w", err)
		}
		s.manifest.Comparison = &comparison
		for _, check := range checks {
			s.check(check.Name, check.Passed, check.Detail)
		}
	}

	s.manifest.LedgerChecksPass = !s.failed
	if s.manifest.Comparison != nil {
		s.manifest.Comparison.CurrentLedgerChecksPassed = s.manifest.LedgerChecksPass
	}
	earliest, noticeOverflow := safeAdd(chainData.summary.TipHeight, opts.MinNoticeBlocks)
	heightOK := !noticeOverflow && opts.ActivationHeight != 0 && opts.ActivationHeight >= earliest
	blockers := []string{
		"the live synthetic-funder-to-capped-issuance transition is not implemented",
		"every validator must approve the same manifest hash and activation height",
	}
	if opts.ActivationHeight == 0 {
		blockers = append(blockers, "no activation height was requested")
	} else if !heightOK {
		blockers = append(blockers, fmt.Sprintf("requested height %d is earlier than minimum %d", opts.ActivationHeight, earliest))
	}
	if !s.manifest.LedgerChecksPass {
		blockers = append(blockers, "one or more ledger checks failed")
	}
	if excess > 0 {
		blockers = append(blockers, "accounted balances exceed chain-classified issuance; governance must explicitly burn or classify the legacy allocation")
	}
	s.manifest.Activation = activationSummary{
		RequestedHeight:        opts.ActivationHeight,
		MinimumNoticeBlocks:    opts.MinNoticeBlocks,
		EarliestSafeHeight:     earliest,
		HeightMeetsNotice:      heightOK,
		RuntimeTransitionReady: false,
		Ready:                  false,
		Blockers:               blockers,
	}
	return s.manifest, nil
}

type accountInspection struct {
	summary    accountSummary
	liquidDust uint64
}

func inspectAccounts(path string) (accountInspection, fileEvidence, bool, []string, error) {
	raw, evidence, stable, err := readStableFile(path)
	if err != nil {
		return accountInspection{}, fileEvidence{}, false, nil, fmt.Errorf("read accounts: %w", err)
	}
	var accounts []chain.Account
	if err := json.Unmarshal(raw, &accounts); err != nil {
		return accountInspection{}, fileEvidence{}, false, nil, fmt.Errorf("parse accounts: %w", err)
	}
	result := accountInspection{}
	result.summary.Count = len(accounts)
	var problems []string
	for _, account := range accounts {
		if account.Address == chain.MiningRewardFunderAddress {
			result.summary.SyntheticFunderPresent = true
			result.summary.SyntheticFunderBalanceCell = formatCell(account.Balance)
			result.summary.SyntheticFunderNonce = account.Nonce
			continue
		}
		result.summary.NonFunderCount++
		if account.BalanceDust != 0 {
			result.summary.PreexistingDustAccountCount++
			if account.Balance < 0 || math.IsNaN(account.Balance) || math.IsInf(account.Balance, 0) {
				problems = append(problems, "an account has an invalid float mirror")
				continue
			}
			if next, ok := checkedAdd(result.liquidDust, account.BalanceDust); ok {
				result.liquidDust = next
			} else {
				problems = append(problems, "account dust total overflows uint64")
			}
			if mirrorDust, _, err := floorCellToDust(account.Balance); err != nil || mirrorDust != account.BalanceDust {
				problems = append(problems, "an account dust balance disagrees with its float mirror")
			}
			continue
		}
		dust, fractional, err := floorCellToDust(account.Balance)
		if err != nil {
			problems = append(problems, "an account balance cannot be represented in dust")
			continue
		}
		if fractional {
			result.summary.FlooredFractionalAccountCount++
		}
		if next, ok := checkedAdd(result.liquidDust, dust); ok {
			result.liquidDust = next
		} else {
			problems = append(problems, "account balance total overflows uint64")
		}
	}
	result.summary.LiquidNonFunderDust = dustString(result.liquidDust)
	return result, evidence, stable, unique(problems), nil
}

type enrollmentSnapshot struct {
	Records []enrollment.EnrollmentRecord `json:"records"`
}

type lockedInspection struct {
	records int
	dust    uint64
}

func inspectEnrollment(path string) (lockedInspection, fileEvidence, bool, []string, error) {
	raw, evidence, stable, err := readStableFile(path)
	if err != nil {
		return lockedInspection{}, fileEvidence{}, false, nil, fmt.Errorf("read enrollment: %w", err)
	}
	var snap enrollmentSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return lockedInspection{}, fileEvidence{}, false, nil, fmt.Errorf("parse enrollment: %w", err)
	}
	result := lockedInspection{records: len(snap.Records)}
	var problems []string
	for _, record := range snap.Records {
		next, ok := checkedAdd(result.dust, record.StakeDust)
		if !ok {
			problems = append(problems, "enrollment bond total overflows uint64")
			continue
		}
		result.dust = next
	}
	return result, evidence, stable, unique(problems), nil
}

type stakingPersistDoc struct {
	Version int `json:"v"`
	Bonds   []struct {
		Amount float64 `json:"a"`
	} `json:"bonds"`
	Unbond []struct {
		Amount float64 `json:"amount"`
	} `json:"unbond"`
}

type stakingInspection struct {
	bondCount   int
	bondedDust  uint64
	unbondCount int
	unbondDust  uint64
}

func inspectStaking(path string) (stakingInspection, fileEvidence, bool, []string, error) {
	raw, evidence, stable, err := readStableFile(path)
	if err != nil {
		return stakingInspection{}, fileEvidence{}, false, nil, fmt.Errorf("read staking: %w", err)
	}
	var doc stakingPersistDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return stakingInspection{}, fileEvidence{}, false, nil, fmt.Errorf("parse staking: %w", err)
	}
	result := stakingInspection{bondCount: len(doc.Bonds), unbondCount: len(doc.Unbond)}
	var problems []string
	if doc.Version != 1 {
		problems = append(problems, "staking snapshot version is not 1")
	}
	for _, bond := range doc.Bonds {
		dust, _, err := floorCellToDust(bond.Amount)
		if err != nil {
			problems = append(problems, "a staking bond cannot be represented in dust")
			continue
		}
		if next, ok := checkedAdd(result.bondedDust, dust); ok {
			result.bondedDust = next
		} else {
			problems = append(problems, "staking bond total overflows uint64")
		}
	}
	for _, unbond := range doc.Unbond {
		dust, _, err := floorCellToDust(unbond.Amount)
		if err != nil {
			problems = append(problems, "an unbonding amount cannot be represented in dust")
			continue
		}
		if next, ok := checkedAdd(result.unbondDust, dust); ok {
			result.unbondDust = next
		} else {
			problems = append(problems, "unbonding total overflows uint64")
		}
	}
	return result, evidence, stable, unique(problems), nil
}

type chainInspection struct {
	summary               chainSummary
	miningDust            uint64
	treasuryDust          uint64
	unclassifiedDust      uint64
	totalIssuedDust       uint64
	feeDust               uint64
	taskCount             int
	taskStakeDust         uint64
	taskRewardPoolDust    uint64
	taskPendingRewardDust uint64
	streamCount           int
	streamEscrowDust      uint64
}

func inspectChain(path string) (chainInspection, fileEvidence, bool, []checkResult, error) {
	before, err := os.Stat(path)
	if err != nil {
		return chainInspection{}, fileEvidence{}, false, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return chainInspection{}, fileEvidence{}, false, nil, err
	}
	defer f.Close()

	h := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(f, h))
	scanner.Buffer(make([]byte, 64*1024), maxBlockLineBytes)
	result := chainInspection{}
	schedule := chain.DefaultEmissionSchedule()
	tasks := chain.NewTaskStateStore()
	streams := chain.NewStreamStateStore()
	var previous *chain.Block
	var continuityOK = true
	var hashesOK = true
	var amountsOK = true
	var overflowOK = true
	var nonEmptyLines uint64
	for scanner.Scan() {
		raw := scanner.Bytes()
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		nonEmptyLines++
		var block chain.Block
		if err := json.Unmarshal(raw, &block); err != nil {
			return result, fileEvidence{}, false, nil, fmt.Errorf("parse chain line %d: %w", nonEmptyLines, err)
		}
		if previous == nil {
			if block.Height != 0 || block.PrevHash != "" {
				continuityOK = false
			}
			result.summary.GenesisHash = block.Hash
			result.summary.GenesisStateRoot = block.StateRoot
		} else if block.Height != previous.Height+1 || block.PrevHash != previous.Hash {
			continuityOK = false
		}
		if chain.ComputeBlockHash(&block) != block.Hash {
			hashesOK = false
		}

		var blockRewardDust uint64
		for _, tx := range block.Transactions {
			result.summary.Transactions++
			if tx == nil {
				amountsOK = false
				continue
			}
			amountDust, _, amountErr := floorCellToDust(tx.Amount)
			feeDust, _, feeErr := floorCellToDust(tx.Fee)
			if amountErr != nil || feeErr != nil {
				amountsOK = false
				continue
			}
			if next, ok := checkedAdd(result.feeDust, feeDust); ok {
				result.feeDust = next
			} else {
				overflowOK = false
			}
			switch tx.ContractID {
			case chain.TaskContractID:
				result.summary.TaskActionTransactions++
				if err := tasks.ApplyHistoricalTx(tx, block.Height); err != nil &&
					!errors.Is(err, chain.ErrDuplicateTaskAction) &&
					!errors.Is(err, chain.ErrTaskActionNonceReplay) &&
					!errors.Is(err, chain.ErrTaskActionRequiresStake) {
					result.summary.ContractReplayFailures++
				}
			case chain.StreamContractID:
				result.summary.StreamActionTransactions++
				if err := streams.ApplyHistoricalTx(tx, block.Height); err != nil {
					result.summary.ContractReplayFailures++
				}
			}
			if tx.Sender != chain.MiningRewardFunderAddress {
				continue
			}
			result.summary.FunderTransactions++
			if tx.Recipient == chain.MiningRewardFunderAddress {
				result.summary.FunderSelfTransactions++
				continue
			}
			if amountDust == 0 {
				continue
			}
			switch {
			case tx.ContractID == chain.MiningRewardContractID:
				result.summary.MiningRewardTransactions++
				if next, ok := checkedAdd(result.miningDust, amountDust); ok {
					result.miningDust = next
				} else {
					overflowOK = false
				}
				if next, ok := checkedAdd(blockRewardDust, amountDust); ok {
					blockRewardDust = next
				} else {
					overflowOK = false
				}
			case tx.ContractID == "" && strings.HasPrefix(tx.ID, "solo-reward-"):
				result.summary.LegacyMiningRewardTxs++
				if next, ok := checkedAdd(result.miningDust, amountDust); ok {
					result.miningDust = next
				} else {
					overflowOK = false
				}
				if next, ok := checkedAdd(blockRewardDust, amountDust); ok {
					blockRewardDust = next
				} else {
					overflowOK = false
				}
			case block.Height == 0:
				if next, ok := checkedAdd(result.treasuryDust, amountDust); ok {
					result.treasuryDust = next
				} else {
					overflowOK = false
				}
			default:
				result.summary.UnclassifiedFunderIssueTxs++
				if next, ok := checkedAdd(result.unclassifiedDust, amountDust); ok {
					result.unclassifiedDust = next
				} else {
					overflowOK = false
				}
			}
		}
		maximum := schedule.BlockRewardDust(block.Height)
		if blockRewardDust > maximum {
			result.summary.RewardScheduleViolationCount++
		}
		previous = &block
		result.summary.Blocks++
		result.summary.TipHeight = block.Height
		result.summary.TipHash = block.Hash
	}
	if err := scanner.Err(); err != nil {
		return result, fileEvidence{}, false, nil, err
	}
	after, err := os.Stat(path)
	if err != nil {
		return result, fileEvidence{}, false, nil, err
	}
	stable := sameFileState(before, after)
	evidence := evidenceFromInfo(path, before, hex.EncodeToString(h.Sum(nil)))

	issued, issuedOverflow := addMany(result.miningDust, result.treasuryDust, result.unclassifiedDust)
	if issuedOverflow {
		overflowOK = false
	}
	result.totalIssuedDust = issued
	result.taskCount = tasks.Count()
	for _, task := range tasks.AllTasks() {
		stakeDust, _, stakeErr := floorCellToDust(task.TotalStakeAmount)
		poolDust, _, poolErr := floorCellToDust(task.RewardPoolAmount)
		pendingDust, _, pendingErr := floorCellToDust(task.PendingRewardAmount)
		if stakeErr != nil || poolErr != nil || pendingErr != nil {
			amountsOK = false
			continue
		}
		if next, ok := checkedAdd(result.taskStakeDust, stakeDust); ok {
			result.taskStakeDust = next
		} else {
			overflowOK = false
		}
		if next, ok := checkedAdd(result.taskRewardPoolDust, poolDust); ok {
			result.taskRewardPoolDust = next
		} else {
			overflowOK = false
		}
		if next, ok := checkedAdd(result.taskPendingRewardDust, pendingDust); ok {
			result.taskPendingRewardDust = next
		} else {
			overflowOK = false
		}
	}
	result.streamCount = streams.Count()
	for _, stream := range streams.AllStreams() {
		if stream.SettledDust > stream.BudgetDust || stream.RefundedDust > stream.BudgetDust-stream.SettledDust {
			amountsOK = false
			continue
		}
		escrowDust := stream.BudgetDust - stream.SettledDust - stream.RefundedDust
		if next, ok := checkedAdd(result.streamEscrowDust, escrowDust); ok {
			result.streamEscrowDust = next
		} else {
			overflowOK = false
		}
	}
	result.summary.MiningRewardDust = dustString(result.miningDust)
	result.summary.TreasuryOrGenesisIssueDust = dustString(result.treasuryDust)
	result.summary.UnclassifiedFunderIssueDust = dustString(result.unclassifiedDust)
	result.summary.TotalFunderIssuedDust = dustString(result.totalIssuedDust)
	result.summary.ScheduleMaximumRewardDust = dustString(schedule.CumulativeEmittedDust(result.summary.TipHeight))
	result.summary.TransactionFeesBurnedDust = dustString(result.feeDust)

	checks := []checkResult{
		{Name: "chain-not-empty", Passed: result.summary.Blocks > 0, Detail: fmt.Sprintf("blocks=%d", result.summary.Blocks)},
		{Name: "chain-contiguous", Passed: continuityOK, Detail: "heights and previous hashes form one branch"},
		{Name: "block-hashes-valid", Passed: hashesOK, Detail: "every persisted block hash matches canonical encoding"},
		{Name: "transaction-amounts-valid", Passed: amountsOK, Detail: "all amounts and fees are finite, non-negative, and dust-representable"},
		{Name: "chain-amount-sums-fit", Passed: overflowOK, Detail: "aggregate dust counters do not overflow uint64"},
		{Name: "contract-state-replays", Passed: result.summary.ContractReplayFailures == 0, Detail: fmt.Sprintf("failed task or stream actions=%d", result.summary.ContractReplayFailures)},
		{Name: "mining-rewards-follow-schedule", Passed: result.summary.RewardScheduleViolationCount == 0, Detail: fmt.Sprintf("violating blocks=%d", result.summary.RewardScheduleViolationCount)},
	}
	return result, evidence, stable, checks, nil
}

func readStableFile(path string) ([]byte, fileEvidence, bool, error) {
	before, err := os.Stat(path)
	if err != nil {
		return nil, fileEvidence{}, false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fileEvidence{}, false, err
	}
	after, err := os.Stat(path)
	if err != nil {
		return nil, fileEvidence{}, false, err
	}
	h := sha256.Sum256(raw)
	return raw, evidenceFromInfo(path, before, hex.EncodeToString(h[:])), sameFileState(before, after), nil
}

func evidenceFromInfo(path string, info os.FileInfo, hash string) fileEvidence {
	return fileEvidence{
		Name:       filepath.Base(path),
		SizeBytes:  info.Size(),
		ModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
		SHA256:     hash,
	}
}

func sameFileState(a, b os.FileInfo) bool {
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func floorCellToDust(cell float64) (uint64, bool, error) {
	if math.IsNaN(cell) || math.IsInf(cell, 0) || cell < 0 {
		return 0, false, fmt.Errorf("invalid CELL amount %v", cell)
	}
	scaled := cell * float64(chain.DustPerCell)
	if scaled >= float64(math.MaxUint64) {
		return 0, false, fmt.Errorf("CELL amount %v exceeds uint64 dust", cell)
	}
	floored := math.Floor(scaled)
	return uint64(floored), math.Abs(scaled-floored) > 1e-9, nil
}

func checkedAdd(a, b uint64) (uint64, bool) {
	if b > math.MaxUint64-a {
		return 0, false
	}
	return a + b, true
}

func safeAdd(a, b uint64) (uint64, bool) {
	n, ok := checkedAdd(a, b)
	return n, !ok
}

func addMany(values ...uint64) (uint64, bool) {
	var total uint64
	for _, value := range values {
		next, ok := checkedAdd(total, value)
		if !ok {
			return 0, true
		}
		total = next
	}
	return total, false
}

func dustString(v uint64) string { return fmt.Sprintf("%d", v) }

func formatCell(v float64) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return "invalid"
	}
	return fmt.Sprintf("%.8f", v)
}

func joinProblems(problems []string, success string) string {
	if len(problems) == 0 {
		return success
	}
	return strings.Join(unique(problems), "; ")
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
