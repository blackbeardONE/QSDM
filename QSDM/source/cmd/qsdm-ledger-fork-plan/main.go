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
	flag.StringVar(&opts.AccountsPath, "accounts", "", "Path to qsdm_accounts.json (required).")
	flag.StringVar(&opts.ChainPath, "chain", "", "Path to qsdm_chain.ndjson (required).")
	flag.StringVar(&opts.EnrollmentPath, "enrollment", "", "Path to qsdm_enrollment.json (default: beside accounts).")
	flag.StringVar(&opts.StakingPath, "staking", "", "Path to qsdm_staking.json (default: beside accounts).")
	flag.Uint64Var(&opts.ActivationHeight, "activation-height", 0, "Candidate fork height; zero performs analysis only.")
	flag.Uint64Var(&opts.MinNoticeBlocks, "minimum-notice-blocks", defaultNoticeBlocks, "Minimum blocks between the snapshot tip and a candidate height.")
	flag.StringVar(&outPath, "out", "", "Write the JSON manifest to this new file instead of stdout.")
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
		return
	}
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
