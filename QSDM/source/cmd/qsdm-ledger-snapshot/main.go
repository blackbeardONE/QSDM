// Command qsdm-ledger-snapshot creates a lock-verified private copy of the
// validator files required by qsdm-ledger-fork-plan.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/blackbeardONE/QSDM/pkg/ledgersnapshot"
)

func main() {
	var opts ledgersnapshot.Options
	flag.StringVar(&opts.StateDir, "state-dir", "", "Stopped validator state directory (required).")
	flag.StringVar(&opts.OutDir, "out", "", "New private snapshot directory to create (required).")
	flag.Parse()

	result, err := ledgersnapshot.Capture(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "qsdm-ledger-snapshot: %v\n", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintf(os.Stderr, "qsdm-ledger-snapshot: print manifest: %v\n", err)
		os.Exit(1)
	}
}
