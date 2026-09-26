// Command hl-audit is the offline HL1 exactly-once checker (HL1 design rev 4,
// §7 "Audit: hl-audit on copies", §8 step 5.6).
//
// It audits a COPY of the core state directory, never the live one:
//
//	hl-audit -state-dir /path/to/copy [-prev earlier-report.json]
//	         [-canary-config mining-canary.json] [-require-watermark]
//	         [-out report.json] [-json]
//
// The copy supplies qsdm_chain.ndjson, hl1-served-watermark.json, any
// hl1-served-watermark.json.retired-* files and legacy-mining/legacy-mining.db
// (each can be overridden with -journal, -watermark and -db; "-db none" skips
// the DB). The audit never writes to its inputs: the DB is copied to a private
// temporary directory before SQLite opens it.
//
// It checks, among others:
//   - oracle 1: no proof ID appears in two LMP1 payloads (double pay);
//   - W3/S10: every LMP1 payment is a well-formed funder reward at or above
//     meta.h0, the per-block reward sum is within the schedule, every paid ID
//     has a DB row with the same miner, and the DB's paid state agrees with the
//     chain;
//   - S5 rules 3-5 against the journal: W <= tip, the block at W has W's hash,
//     and tip <= W+1;
//   - oracle 3 (W regression): the served high-water mark of the -prev report
//     and every retired watermark is still in the journal with the same hash,
//     and W is not below the previous high-water mark;
//   - with -canary-config: emitted_H <= budget_cell, proofs_H <=
//     max_proofs_total and every reward in the window pays an allowlisted
//     address.
//
// Exit status: 0 PASS, 1 FAIL (at least one fail finding), 2 usage or input
// error. The JSON report written with -out is the -prev input of the next
// audit; -out refuses to overwrite an existing file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
)

const (
	exitPass  = 0
	exitFail  = 1
	exitUsage = 2
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("hl-audit", flag.ContinueOnError)
	fl.SetOutput(stderr)
	var (
		stateDir  = fl.String("state-dir", "", "copy of the core state directory")
		journal   = fl.String("journal", "", "chain journal (default <state-dir>/"+JournalFile+")")
		watermark = fl.String("watermark", "", "served watermark (default <state-dir>/"+legacymining.WatermarkFile+")")
		dbPath    = fl.String("db", "", `legacy-mining.db (default <state-dir>/legacy-mining/legacy-mining.db; "none" skips it)`)
		prev      = fl.String("prev", "", "previous hl-audit JSON report")
		canary    = fl.String("canary-config", "", "canary config file (enables the budget checks)")
		requireW  = fl.Bool("require-watermark", false, "fail when the watermark is absent")
		out       = fl.String("out", "", "write the JSON report to this new file")
		asJSON    = fl.Bool("json", false, "print the JSON report instead of the summary")
		tempDir   = fl.String("temp-dir", "", "directory for the private DB copy (default: system temp)")
	)
	if err := fl.Parse(args); err != nil {
		return exitUsage
	}
	if fl.NArg() != 0 {
		fmt.Fprintf(stderr, "hl-audit: unexpected arguments %q\n", fl.Args())
		return exitUsage
	}
	o := Options{RequireWatermark: *requireW, TempDir: *tempDir}
	o.JournalPath = pick(*journal, *stateDir, JournalFile)
	o.WatermarkPath = pick(*watermark, *stateDir, legacymining.WatermarkFile)
	o.RetiredDir = *stateDir
	if *dbPath != "none" {
		o.DBPath = pick(*dbPath, *stateDir, filepath.Join(legacymining.LegacyDirName, legacymining.DBFile))
	}
	if o.JournalPath == "" {
		fmt.Fprintln(stderr, "hl-audit: -state-dir or -journal is required")
		return exitUsage
	}
	if *prev != "" {
		data, err := os.ReadFile(*prev)
		if err != nil {
			fmt.Fprintf(stderr, "hl-audit: %v\n", err)
			return exitUsage
		}
		if o.Prev, err = ParseReport(data); err != nil {
			fmt.Fprintf(stderr, "hl-audit: %v\n", err)
			return exitUsage
		}
	}
	if *canary != "" {
		data, err := os.ReadFile(*canary)
		if err != nil {
			fmt.Fprintf(stderr, "hl-audit: %v\n", err)
			return exitUsage
		}
		o.CanaryConfig = data
	}

	rep, err := Audit(o)
	if err != nil {
		fmt.Fprintf(stderr, "hl-audit: %v\n", err)
		return exitUsage
	}
	js, err := json.MarshalIndent(rep, "", " ")
	if err != nil {
		fmt.Fprintf(stderr, "hl-audit: %v\n", err)
		return exitUsage
	}
	js = append(js, '\n')
	if *out != "" {
		if err := writeNew(*out, js); err != nil {
			fmt.Fprintf(stderr, "hl-audit: report: %v\n", err)
			return exitUsage
		}
	}
	if *asJSON {
		_, _ = stdout.Write(js)
	} else {
		printSummary(stdout, rep)
	}
	if rep.Failed() {
		return exitFail
	}
	return exitPass
}

// pick returns explicit if set, else <dir>/<name> if dir is set, else "".
func pick(explicit, dir, name string) string {
	switch {
	case explicit != "":
		return explicit
	case dir != "":
		return filepath.Join(dir, name)
	}
	return ""
}

func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func printSummary(w io.Writer, r *Report) {
	fmt.Fprintf(w, "hl-audit: %s\n", r.Result)
	c := r.Chain
	fmt.Fprintf(w, "chain: %d blocks, heights %d..%d, tip hash %s\n", c.Blocks, c.FirstHeight, c.Tip, c.TipHash)
	if r.Watermark != nil {
		fmt.Fprintf(w, "watermark: %d %s (%s)\n", r.Watermark.Height, r.Watermark.Hash, r.Watermark.Source)
	}
	for _, p := range r.Retired {
		fmt.Fprintf(w, "retired watermark: %d %s (%s)\n", p.Height, p.Hash, p.Origin)
	}
	if d := r.DB; d != nil {
		fmt.Fprintf(w, "db: h0 %d, %d rows (%d paid, %d unpaid), %d config windows\n", d.H0, d.Rows, d.Paid, d.Unpaid, d.Windows)
	}
	p := r.Payments
	fmt.Fprintf(w, "payments: %d reward txs, %d payload IDs, %d distinct, %v CELL\n", p.RewardTxs, p.PayloadIDs, p.DistinctIDs, p.Emitted)
	if cs := r.Canary; cs != nil {
		fmt.Fprintf(w, "canary %s: emitted %v of %d CELL, %d of %d proofs\n", cs.ConfigSHA256, cs.Emitted, cs.BudgetCell, cs.Proofs, cs.MaxProofsTotal)
	}
	if hw := r.ServedHighWater; hw != nil {
		fmt.Fprintf(w, "served high-water: %d %s\n", hw.Height, hw.Hash)
	}
	for _, f := range r.Findings {
		at := ""
		if f.Height != nil {
			at = fmt.Sprintf(" [height %d]", *f.Height)
		}
		fmt.Fprintf(w, "%s %s%s: %s\n", severityLabel(f.Severity), f.Code, at, f.Detail)
	}
	if r.FindingsDropped > 0 {
		fmt.Fprintf(w, "... %d more findings not listed\n", r.FindingsDropped)
	}
}

func severityLabel(s string) string {
	if s == sevFail {
		return "FAIL"
	}
	return "WARN"
}
