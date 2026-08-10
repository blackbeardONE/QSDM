// Command qsdm-ledger-fork-plan audits one stopped validator snapshot and
// emits an aggregate-only migration manifest. It never edits source files.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

func main() {
	var opts analyzeOptions
	var outPath string
	var allowBlocked bool
	flag.StringVar(&opts.AccountsPath, "accounts", "", "Path to qsdm_accounts.json (required).")
	flag.StringVar(&opts.ChainPath, "chain", "", "Path to qsdm_chain.ndjson (required).")
	flag.StringVar(&opts.EnrollmentPath, "enrollment", "", "Path to qsdm_enrollment.json (default: beside accounts).")
	flag.StringVar(&opts.StakingPath, "staking", "", "Path to qsdm_staking.json (default: beside accounts).")
	flag.StringVar(&opts.BaselinePath, "baseline-manifest", "", "Compare against an earlier planner manifest from the same chain.")
	flag.Uint64Var(&opts.ActivationHeight, "activation-height", 0, "Candidate fork height; zero performs analysis only.")
	flag.Uint64Var(&opts.MinNoticeBlocks, "minimum-notice-blocks", defaultNoticeBlocks, "Minimum blocks between the snapshot tip and a candidate height.")
	flag.StringVar(&outPath, "out", "", "Write the JSON manifest to this new file instead of stdout.")
	flag.BoolVar(&allowBlocked, "allow-blocked", false, "Return exit code 0 after writing a blocked manifest (exploratory analysis only).")
	flag.Parse()

	result, err := analyze(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsdm-ledger-fork-plan: %v\n", err)
		os.Exit(1)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsdm-ledger-fork-plan: encode manifest: %v\n", err)
		os.Exit(1)
	}
	data = append(data, '\n')
	if outPath == "" {
		_, _ = os.Stdout.Write(data)
	} else {
		if _, err := os.Stat(outPath); err == nil {
			fmt.Fprintf(os.Stderr, "qsdm-ledger-fork-plan: refusing to overwrite %s\n", outPath)
			os.Exit(1)
		} else if !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "qsdm-ledger-fork-plan: inspect output: %v\n", err)
			os.Exit(1)
		}
		if err := os.WriteFile(outPath, data, 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "qsdm-ledger-fork-plan: write manifest: %v\n", err)
			os.Exit(1)
		}
	}
	if !allowBlocked {
		if code, reason := readinessExit(result); code != 0 {
			fmt.Fprintf(os.Stderr, "qsdm-ledger-fork-plan: manifest written, but activation remains blocked: %s\n", reason)
			os.Exit(code)
		}
	}
}

func readinessExit(result manifest) (int, string) {
	if !result.LedgerChecksPass {
		return 2, "one or more ledger checks failed"
	}
	if !result.Activation.Ready {
		return 3, "the capped-issuance transition and coordinated activation approval are incomplete"
	}
	return 0, ""
}
