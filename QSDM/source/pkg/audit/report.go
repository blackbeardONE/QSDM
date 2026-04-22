package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// ReportFormat selects the output format produced by Checklist.Report.
type ReportFormat string

const (
	ReportMarkdown ReportFormat = "markdown"
	ReportJSON     ReportFormat = "json"
)

// ReportOptions tunes the generated audit report.
type ReportOptions struct {
	Format       ReportFormat // default: ReportMarkdown
	Title        string       // default: "QSDM+ Security Audit Report"
	GeneratedAt  time.Time    // default: time.Now()
	IncludeNotes bool         // if true, reviewer notes are included verbatim
}

// Report writes a human- or machine-readable audit report to w using the current checklist state.
func (cl *Checklist) Report(w io.Writer, opts ReportOptions) error {
	if opts.Format == "" {
		opts.Format = ReportMarkdown
	}
	if opts.Title == "" {
		opts.Title = "QSDM+ Security Audit Report"
	}
	if opts.GeneratedAt.IsZero() {
		opts.GeneratedAt = time.Now().UTC()
	}

	items := cl.Items()
	summary := cl.Summary()

	switch opts.Format {
	case ReportJSON:
		return writeJSONReport(w, opts, items, summary)
	case ReportMarkdown:
		return writeMarkdownReport(w, opts, items, summary)
	default:
		return fmt.Errorf("unknown report format: %s", opts.Format)
	}
}

// Score returns a numeric 0..100 completion score: (passed + waived) / total * 100.
// Failed or pending items count against the score.
func (cl *Checklist) Score() float64 {
	s := cl.Summary()
	total := s["total"]
	if total == 0 {
		return 0
	}
	done := s[string(StatusPassed)] + s[string(StatusWaived)]
	return (float64(done) / float64(total)) * 100.0
}

// HasBlockingFindings returns true if any critical/high item is pending or failed.
// Useful as an exit code gate in CI pipelines.
func (cl *Checklist) HasBlockingFindings() bool {
	for _, it := range cl.Items() {
		if it.Severity != SevCritical && it.Severity != SevHigh {
			continue
		}
		if it.Status == StatusPending || it.Status == StatusFailed {
			return true
		}
	}
	return false
}

func writeJSONReport(w io.Writer, opts ReportOptions, items []ChecklistItem, summary map[string]int) error {
	payload := map[string]interface{}{
		"title":        opts.Title,
		"generated_at": opts.GeneratedAt.Format(time.RFC3339),
		"summary":      summary,
		"score":        scoreOf(summary),
		"items":        items,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

func scoreOf(summary map[string]int) float64 {
	total := summary["total"]
	if total == 0 {
		return 0
	}
	done := summary[string(StatusPassed)] + summary[string(StatusWaived)]
	return (float64(done) / float64(total)) * 100.0
}

func writeMarkdownReport(w io.Writer, opts ReportOptions, items []ChecklistItem, summary map[string]int) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", opts.Title)
	fmt.Fprintf(&b, "_Generated: %s_\n\n", opts.GeneratedAt.Format(time.RFC3339))

	fmt.Fprintln(&b, "## Summary")
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "| Status | Count |")
	fmt.Fprintln(&b, "|--------|-------|")
	statusOrder := []string{"total", string(StatusPassed), string(StatusFailed), string(StatusPending), string(StatusWaived)}
	for _, s := range statusOrder {
		fmt.Fprintf(&b, "| %s | %d |\n", s, summary[s])
	}
	fmt.Fprintf(&b, "\n**Completion score:** %.1f%%\n\n", scoreOf(summary))

	// Group by category.
	byCat := map[Category][]ChecklistItem{}
	catsSeen := []Category{}
	for _, it := range items {
		if _, seen := byCat[it.Category]; !seen {
			catsSeen = append(catsSeen, it.Category)
		}
		byCat[it.Category] = append(byCat[it.Category], it)
	}
	sort.SliceStable(catsSeen, func(i, j int) bool { return string(catsSeen[i]) < string(catsSeen[j]) })

	fmt.Fprintln(&b, "## Findings by category")
	fmt.Fprintln(&b, "")

	for _, cat := range catsSeen {
		fmt.Fprintf(&b, "### %s\n\n", titleCase(string(cat)))
		fmt.Fprintln(&b, "| ID | Sev | Status | Title |")
		fmt.Fprintln(&b, "|----|-----|--------|-------|")
		for _, it := range byCat[cat] {
			fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", it.ID, it.Severity, it.Status, escapePipe(it.Title))
		}
		if opts.IncludeNotes {
			any := false
			for _, it := range byCat[cat] {
				if strings.TrimSpace(it.Notes) == "" {
					continue
				}
				if !any {
					fmt.Fprintln(&b, "\n**Notes:**")
					any = true
				}
				fmt.Fprintf(&b, "- `%s` — %s\n", it.ID, it.Notes)
			}
		}
		fmt.Fprintln(&b, "")
	}

	// Blocking findings section.
	blocking := []ChecklistItem{}
	for _, it := range items {
		if (it.Severity == SevCritical || it.Severity == SevHigh) &&
			(it.Status == StatusPending || it.Status == StatusFailed) {
			blocking = append(blocking, it)
		}
	}
	if len(blocking) > 0 {
		fmt.Fprintln(&b, "## Blocking findings")
		fmt.Fprintln(&b, "")
		fmt.Fprintln(&b, "The following critical/high items are pending or failed and block production sign-off:")
		fmt.Fprintln(&b, "")
		for _, it := range blocking {
			fmt.Fprintf(&b, "- [ ] `%s` (%s) — %s\n", it.ID, it.Severity, escapePipe(it.Title))
		}
		fmt.Fprintln(&b, "")
	} else {
		fmt.Fprintln(&b, "## Blocking findings")
		fmt.Fprintln(&b, "")
		fmt.Fprintln(&b, "None — no pending or failed critical/high items.")
		fmt.Fprintln(&b, "")
	}

	_, err := io.WriteString(w, b.String())
	return err
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func escapePipe(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
