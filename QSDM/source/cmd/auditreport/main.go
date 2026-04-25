// Command auditreport renders the QSDM security audit checklist as Markdown or JSON.
//
// Typical usage:
//
//	go run ./cmd/auditreport > audit.md
//	go run ./cmd/auditreport -format json -out audit.json
//	go run ./cmd/auditreport -input review.json -format markdown > audit.md
//
// When -input is set, the JSON file is expected to have the shape produced by -format json
// (top-level "items" array matching audit.ChecklistItem). This lets reviewers edit statuses
// offline and regenerate the report.
//
// Exit code is 2 if any critical/high item is pending or failed (useful for CI gating).
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/blackbeardONE/QSDM/pkg/audit"
)

type inputDoc struct {
	Items []audit.ChecklistItem `json:"items"`
}

func main() {
	format := flag.String("format", "markdown", "output format: markdown|json")
	out := flag.String("out", "", "output path (default: stdout)")
	input := flag.String("input", "", "optional JSON input with reviewed statuses")
	title := flag.String("title", "", "report title (default: QSDM Security Audit Report)")
	notes := flag.Bool("notes", true, "include reviewer notes in markdown output")
	gate := flag.Bool("gate", true, "exit 2 if critical/high items are pending or failed")
	flag.Parse()

	cl := audit.NewChecklist()

	if *input != "" {
		if err := applyReviewJSON(cl, *input); err != nil {
			fmt.Fprintf(os.Stderr, "failed to apply %s: %v\n", *input, err)
			os.Exit(1)
		}
	}

	w, closeFn, err := openOutput(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open output: %v\n", err)
		os.Exit(1)
	}
	defer closeFn()

	opts := audit.ReportOptions{
		Format:       audit.ReportFormat(*format),
		Title:        *title,
		IncludeNotes: *notes,
	}
	if err := cl.Report(w, opts); err != nil {
		fmt.Fprintf(os.Stderr, "render report: %v\n", err)
		os.Exit(1)
	}

	if *gate && cl.HasBlockingFindings() {
		fmt.Fprintf(os.Stderr, "audit gate: blocking findings present (score=%.1f%%)\n", cl.Score())
		os.Exit(2)
	}
}

func applyReviewJSON(cl *audit.Checklist, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var doc inputDoc
	if err := json.NewDecoder(f).Decode(&doc); err != nil {
		return fmt.Errorf("parse json: %w", err)
	}
	if len(doc.Items) == 0 {
		return errors.New("input contains no items")
	}
	for _, it := range doc.Items {
		if it.ID == "" || it.Status == "" {
			continue
		}
		reviewer := it.ReviewedBy
		if reviewer == "" {
			reviewer = "offline"
		}
		if err := cl.UpdateStatus(it.ID, it.Status, reviewer, it.Notes); err != nil {
			// Skip unknown IDs rather than failing the whole report.
			fmt.Fprintf(os.Stderr, "warn: skip %s: %v\n", it.ID, err)
		}
	}
	return nil
}

func openOutput(path string) (io.Writer, func(), error) {
	if path == "" {
		return os.Stdout, func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { _ = f.Close() }, nil
}
