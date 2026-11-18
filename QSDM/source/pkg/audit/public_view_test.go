package audit

import (
	"strings"
	"testing"
)

func TestScrubHostPaths(t *testing.T) {
	cases := map[string]string{
		"key at /opt/qsdm/host_key (0600)":                    "key at <host path> (0600)",
		"drop-in /etc/systemd/system/qsdm.service.d/x.conf":   "drop-in <host path>",
		"served from /var/www/qsdm/.well-known/security.txt.": "served from <host path>",
		"path=/var/lib/qsdm-tech/core;":                       "path=<host path>;",
		"backup `/opt/qsdm/qsdm.v040.bak`":                    "backup `<host path>`",
	}
	for in, want := range cases {
		if got := ScrubHostPaths(in); got != want {
			t.Errorf("ScrubHostPaths(%q) = %q, want %q", in, got, want)
		}
	}
	keep := []string{
		"pkg/crypto/dilithium_circl.go::NewDilithium calls mldsa87.GenerateKey",
		"GET /api/v1/status and /api/v1/mining/work",
		"see QSDM/docs/docs/SECURITY.md",
		"a ratio 3/4 and a date 2026/11/18",
	}
	for _, s := range keep {
		if got := ScrubHostPaths(s); got != s {
			t.Errorf("ScrubHostPaths changed %q to %q", s, got)
		}
	}
}

// Every item of the built-in checklist, as the public endpoint shows it, carries no absolute host path.
func TestChecklistPublicViewHasNoHostPath(t *testing.T) {
	cl := NewChecklist()
	n := 0
	for _, it := range cl.Items() {
		raw := it.Title + " " + it.Description + " " + it.Notes + " " + it.ReviewedBy
		if hostPathRE.MatchString(raw) {
			n++
		}
		pv := it.PublicView()
		pub := pv.Title + " " + pv.Description + " " + pv.Notes + " " + pv.ReviewedBy
		if loc := hostPathRE.FindStringIndex(pub); loc != nil {
			t.Fatalf("item %s still shows a host path: %q", it.ID, pub[loc[0]:])
		}
		if pv.ID != it.ID || pv.Status != it.Status || pv.Severity != it.Severity {
			t.Fatalf("item %s: the public view changed more than its text", it.ID)
		}
	}
	if n == 0 {
		t.Fatal("test setup: no built-in item carried a host path; the check proves nothing")
	}
	if strings.Contains(HostPathPlaceholder, "/") {
		t.Fatal("the placeholder must not look like a path")
	}
}
