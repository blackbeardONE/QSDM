package audit

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestChecklist_Score_EmptyStart(t *testing.T) {
	cl := NewChecklist()
	if got := cl.Score(); got != 0 {
		t.Fatalf("expected score 0 with all pending, got %v", got)
	}
}

func TestChecklist_Score_HalfPassed(t *testing.T) {
	cl := NewChecklist()
	items := cl.Items()
	half := len(items) / 2
	for i := 0; i < half; i++ {
		_ = cl.UpdateStatus(items[i].ID, StatusPassed, "auditor", "")
	}
	score := cl.Score()
	expected := (float64(half) / float64(len(items))) * 100.0
	if score < expected-0.5 || score > expected+0.5 {
		t.Fatalf("expected score ~%.2f, got %.2f", expected, score)
	}
}

func TestChecklist_HasBlockingFindings(t *testing.T) {
	cl := NewChecklist()
	if !cl.HasBlockingFindings() {
		t.Fatal("fresh checklist must have blocking findings (everything pending)")
	}

	for _, it := range cl.Items() {
		if it.Severity == SevCritical || it.Severity == SevHigh {
			_ = cl.UpdateStatus(it.ID, StatusPassed, "auditor", "")
		}
	}
	if cl.HasBlockingFindings() {
		t.Fatal("expected no blocking findings after clearing critical/high")
	}
}

func TestChecklist_Report_Markdown_IncludesSummaryAndCategories(t *testing.T) {
	cl := NewChecklist()
	_ = cl.UpdateStatus("crypto-01", StatusPassed, "auditor", "csprng verified")

	var buf bytes.Buffer
	if err := cl.Report(&buf, ReportOptions{Format: ReportMarkdown, IncludeNotes: true}); err != nil {
		t.Fatalf("Report: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"# QSDM+ Security Audit Report",
		"## Summary",
		"**Completion score:**",
		"### Cryptography",
		"`crypto-01`",
		"csprng verified",
		"## Blocking findings",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("markdown report missing %q. output:\n%s", want, out)
		}
	}
}

func TestChecklist_Report_JSON_Roundtrip(t *testing.T) {
	cl := NewChecklist()
	var buf bytes.Buffer
	if err := cl.Report(&buf, ReportOptions{Format: ReportJSON}); err != nil {
		t.Fatalf("Report: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("JSON unmarshal: %v — body:\n%s", err, buf.String())
	}
	if _, ok := payload["summary"]; !ok {
		t.Fatal("expected summary key in JSON report")
	}
	items, ok := payload["items"].([]interface{})
	if !ok || len(items) == 0 {
		t.Fatal("expected non-empty items array in JSON report")
	}
}

func TestChecklist_Report_UnknownFormat(t *testing.T) {
	cl := NewChecklist()
	err := cl.Report(&bytes.Buffer{}, ReportOptions{Format: ReportFormat("wat")})
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
}
