// Command trustcheck is an independent HTTP client that scrapes a QSDM
// validator's /api/v1/trust/attestations/{summary,recent} endpoints and
// validates the responses against MINING_PROTOCOL-adjacent Major Update
// §8.5.3 / §8.5.4 contracts.
//
// The binary exists so third-party monitoring services, journalists, and
// ops teams can run a fast black-box check of a validator's transparency
// surface without pulling in the entire QSDM codebase or trusting our
// SDKs. Exit codes:
//
//	0 — all assertions passed.
//	1 — usage / network / HTTP-level error.
//	2 — one or more contract assertions failed.
//	3 — the endpoint legitimately returned 503 warming-up or 404 disabled
//	    (informational; see --allow-warmup / --allow-disabled to downgrade).
//
// The tool is intentionally dependency-free (stdlib only) so it can be
// cross-compiled for any OS/arch and shipped as a single artifact.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Wire shapes — mirror pkg/api.TrustSummary / TrustRecent. We redeclare
// them locally so this command has no import on the main QSDM module and
// stays a drop-in black-box scraper.
// ---------------------------------------------------------------------------

type trustSummary struct {
	Attested         int     `json:"attested"`
	TotalPublic      int     `json:"total_public"`
	Ratio            float64 `json:"ratio"`
	FreshWithin      string  `json:"fresh_within"`
	LastAttestedAt   *string `json:"last_attested_at"`
	LastCheckedAt    string  `json:"last_checked_at"`
	NGCServiceStatus string  `json:"ngc_service_status"`
	ScopeNote        string  `json:"scope_note"`
}

type trustAttestation struct {
	NodeIDPrefix    string `json:"node_id_prefix"`
	AttestedAt      string `json:"attested_at"`
	FreshAgeSeconds int64  `json:"fresh_age_seconds"`
	GPUArchitecture string `json:"gpu_architecture"`
	GPUAvailable    bool   `json:"gpu_available"`
	NGCHMACOK       bool   `json:"ngc_hmac_ok"`
	RegionHint      string `json:"region_hint"`
}

type trustRecent struct {
	FreshWithin  string             `json:"fresh_within"`
	Count        int                `json:"count"`
	Attestations []trustAttestation `json:"attestations"`
}

// Fixed scope-note string required by Major Update §8.5.2 to appear
// verbatim on every summary response.
const expectedScopeNote = "NVIDIA-lock is an opt-in, per-operator API policy — not a consensus rule. See NVIDIA_LOCK_CONSENSUS_SCOPE.md."

// Validation result bookkeeping. The caller prints a checklist of every
// assertion that ran with pass / fail / skip status.
type result struct {
	name string
	ok   bool
	msg  string
}

type results struct {
	rows []result
}

func (rs *results) pass(name string) { rs.rows = append(rs.rows, result{name: name, ok: true}) }
func (rs *results) fail(name, msg string) {
	rs.rows = append(rs.rows, result{name: name, ok: false, msg: msg})
}

func (rs *results) allOK() bool {
	for _, r := range rs.rows {
		if !r.ok {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

func main() {
	base := flag.String("base", "https://api.qsdm.tech", "Base URL of the validator HTTP surface (no trailing slash).")
	timeout := flag.Duration("timeout", 10*time.Second, "HTTP timeout per request.")
	limit := flag.Int("limit", 50, "limit query parameter for the /recent endpoint (server clamps to [1,200]).")
	allowWarmup := flag.Bool("allow-warmup", false, "Exit 0 instead of 3 when the aggregator is still warming up (503).")
	allowDisabled := flag.Bool("allow-disabled", false, "Exit 0 instead of 3 when the operator has disabled trust endpoints (404).")
	jsonOut := flag.Bool("json", false, "Emit machine-readable JSON instead of the human-friendly checklist.")
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "trustcheck — black-box validator for QSDM trust transparency endpoints.\n\n")
		fmt.Fprintf(out, "Usage: %s [flags]\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(out, "\nExit codes: 0=pass, 1=usage/network, 2=contract failure, 3=warming-up/disabled.\n")
	}
	flag.Parse()

	cleanBase := strings.TrimRight(*base, "/")
	if cleanBase == "" {
		fmt.Fprintln(os.Stderr, "trustcheck: --base must be set to a reachable validator URL")
		os.Exit(1)
	}

	client := &http.Client{Timeout: *timeout}
	rs := &results{}

	summary, summaryStatus, err := fetchSummary(client, cleanBase)
	switch {
	case errors.Is(err, errWarmingUp):
		handleInformational(*allowWarmup, 3, "warming up", *jsonOut, rs)
		return
	case errors.Is(err, errDisabled):
		handleInformational(*allowDisabled, 3, "disabled", *jsonOut, rs)
		return
	case err != nil:
		fmt.Fprintf(os.Stderr, "trustcheck: failed to fetch summary: %v\n", err)
		os.Exit(1)
	}
	_ = summaryStatus

	validateSummary(summary, rs)

	recent, recentStatus, rErr := fetchRecent(client, cleanBase, *limit)
	switch {
	case errors.Is(rErr, errWarmingUp):
		rs.fail("recent-endpoint-fetch", "warming up (503) despite summary being OK — inconsistent state")
	case errors.Is(rErr, errDisabled):
		rs.fail("recent-endpoint-fetch", "disabled (404) despite summary being OK — inconsistent state")
	case rErr != nil:
		rs.fail("recent-endpoint-fetch", rErr.Error())
	default:
		_ = recentStatus
		validateRecent(recent, summary, rs)
	}

	emit(rs, summary, recent, *jsonOut)

	if !rs.allOK() {
		os.Exit(2)
	}
}

// ---------------------------------------------------------------------------
// fetch helpers
// ---------------------------------------------------------------------------

var (
	errWarmingUp = errors.New("trust aggregator warming up (503)")
	errDisabled  = errors.New("trust endpoints disabled on this node (404)")
)

func fetchSummary(c *http.Client, base string) (*trustSummary, int, error) {
	u := base + "/api/v1/trust/attestations/summary"
	body, status, err := getJSON(c, u)
	if err != nil {
		return nil, status, err
	}
	var s trustSummary
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, status, fmt.Errorf("unmarshal summary: %w; body=%q", err, string(body))
	}
	return &s, status, nil
}

func fetchRecent(c *http.Client, base string, limit int) (*trustRecent, int, error) {
	u := fmt.Sprintf("%s/api/v1/trust/attestations/recent?limit=%d", base, limit)
	body, status, err := getJSON(c, u)
	if err != nil {
		return nil, status, err
	}
	var r trustRecent
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, status, fmt.Errorf("unmarshal recent: %w; body=%q", err, string(body))
	}
	return &r, status, nil
}

func getJSON(c *http.Client, u string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return body, resp.StatusCode, nil
	case http.StatusServiceUnavailable:
		return body, resp.StatusCode, errWarmingUp
	case http.StatusNotFound:
		return body, resp.StatusCode, errDisabled
	default:
		return body, resp.StatusCode, fmt.Errorf("unexpected HTTP %d: %s", resp.StatusCode, snippet(body))
	}
}

func snippet(b []byte) string {
	s := string(b)
	if len(s) > 240 {
		s = s[:240] + "…"
	}
	return s
}

// ---------------------------------------------------------------------------
// assertions
// ---------------------------------------------------------------------------

func validateSummary(s *trustSummary, rs *results) {
	// §8.5.2: "X of Y", never just "X".
	if s.TotalPublic < s.Attested {
		rs.fail("summary/ratio-sanity", fmt.Sprintf("total_public=%d < attested=%d", s.TotalPublic, s.Attested))
	} else {
		rs.pass("summary/ratio-sanity")
	}
	if s.TotalPublic == 0 && s.Attested != 0 {
		rs.fail("summary/attested-without-denominator", "attested>0 but total_public==0; violates §8.5.2 anti-claim guardrail")
	} else {
		rs.pass("summary/attested-without-denominator")
	}
	// §8.5.2: scope_note must be verbatim.
	if s.ScopeNote == expectedScopeNote {
		rs.pass("summary/scope-note-verbatim")
	} else {
		rs.fail("summary/scope-note-verbatim", fmt.Sprintf("got %q; expected §8.5.2 verbatim string", s.ScopeNote))
	}
	// §8.5.3: fresh_within parses as a Go duration.
	if _, err := time.ParseDuration(s.FreshWithin); err != nil {
		rs.fail("summary/fresh-within-parseable", fmt.Sprintf("%q is not a time.Duration (%v)", s.FreshWithin, err))
	} else {
		rs.pass("summary/fresh-within-parseable")
	}
	// §8.5.3: ngc_service_status is one of healthy / degraded / outage.
	switch s.NGCServiceStatus {
	case "healthy", "degraded", "outage":
		rs.pass("summary/ngc-status-enum")
	default:
		rs.fail("summary/ngc-status-enum", fmt.Sprintf("%q is not in {healthy,degraded,outage}", s.NGCServiceStatus))
	}
	// §8.5.3: last_checked_at is RFC3339 and within 1 h of now.
	if t, err := time.Parse(time.RFC3339, s.LastCheckedAt); err != nil {
		rs.fail("summary/last-checked-at-rfc3339", fmt.Sprintf("%q is not RFC3339 (%v)", s.LastCheckedAt, err))
	} else if time.Since(t) > time.Hour || time.Until(t) > time.Hour {
		rs.fail("summary/last-checked-at-fresh", fmt.Sprintf("last_checked_at=%s, more than 1h from local clock", t))
	} else {
		rs.pass("summary/last-checked-at-rfc3339")
		rs.pass("summary/last-checked-at-fresh")
	}
	// §8.5.3: last_attested_at is RFC3339 when present; nil is legal.
	if s.LastAttestedAt != nil {
		if _, err := time.Parse(time.RFC3339, *s.LastAttestedAt); err != nil {
			rs.fail("summary/last-attested-at-rfc3339", fmt.Sprintf("%q is not RFC3339 (%v)", *s.LastAttestedAt, err))
		} else {
			rs.pass("summary/last-attested-at-rfc3339")
		}
	} else {
		rs.pass("summary/last-attested-at-rfc3339")
	}
	// §8.5.3: ratio is monotonic w.r.t. attested/total_public within 0.01.
	if s.TotalPublic > 0 {
		expected := float64(s.Attested) / float64(s.TotalPublic)
		if abs(s.Ratio-expected) > 0.01 {
			rs.fail("summary/ratio-consistency", fmt.Sprintf("ratio=%v but attested/total_public=%v", s.Ratio, expected))
		} else {
			rs.pass("summary/ratio-consistency")
		}
	} else if s.Ratio != 0 {
		rs.fail("summary/ratio-consistency", "ratio should be 0 when total_public==0")
	} else {
		rs.pass("summary/ratio-consistency")
	}
}

func validateRecent(r *trustRecent, s *trustSummary, rs *results) {
	// count matches the slice length.
	if r.Count != len(r.Attestations) {
		rs.fail("recent/count-matches-length", fmt.Sprintf("count=%d, attestations=%d", r.Count, len(r.Attestations)))
	} else {
		rs.pass("recent/count-matches-length")
	}
	// fresh_within identical to summary.
	if r.FreshWithin != s.FreshWithin {
		rs.fail("recent/fresh-within-consistent", fmt.Sprintf("recent=%q, summary=%q", r.FreshWithin, s.FreshWithin))
	} else {
		rs.pass("recent/fresh-within-consistent")
	}
	// recent count cannot exceed summary attested (§8.5.3: entries older
	// than fresh_within are never returned).
	if r.Count > s.Attested {
		rs.fail("recent/attested-upper-bound", fmt.Sprintf("recent.count=%d > summary.attested=%d", r.Count, s.Attested))
	} else {
		rs.pass("recent/attested-upper-bound")
	}
	// each row: redaction, region bucket, HMAC flag, monotonic age.
	seen := map[string]bool{}
	prevAge := int64(-1)
	for i, a := range r.Attestations {
		name := fmt.Sprintf("recent/row%02d", i)
		if !strings.Contains(a.NodeIDPrefix, "…") {
			rs.fail(name+"/redaction", fmt.Sprintf("node_id_prefix %q has no ellipsis — redaction rule violated", a.NodeIDPrefix))
			continue
		}
		if seen[a.NodeIDPrefix] {
			rs.fail(name+"/unique-prefix", fmt.Sprintf("duplicate node_id_prefix %q in recent feed", a.NodeIDPrefix))
		}
		seen[a.NodeIDPrefix] = true
		if !isRegion(a.RegionHint) {
			rs.fail(name+"/region-enum", fmt.Sprintf("region_hint=%q is not in {eu,us,apac,other}", a.RegionHint))
			continue
		}
		if _, err := time.Parse(time.RFC3339, a.AttestedAt); err != nil {
			rs.fail(name+"/attested-at-rfc3339", err.Error())
			continue
		}
		if a.FreshAgeSeconds < 0 {
			rs.fail(name+"/age-non-negative", fmt.Sprintf("fresh_age_seconds=%d < 0", a.FreshAgeSeconds))
			continue
		}
		if prevAge >= 0 && a.FreshAgeSeconds < prevAge {
			rs.fail(name+"/age-monotonic", fmt.Sprintf("row ages not monotonic: %d then %d", prevAge, a.FreshAgeSeconds))
			continue
		}
		prevAge = a.FreshAgeSeconds
		rs.pass(name)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func isRegion(r string) bool {
	switch r {
	case "eu", "us", "apac", "other":
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// output
// ---------------------------------------------------------------------------

func emit(rs *results, s *trustSummary, r *trustRecent, asJSON bool) {
	if asJSON {
		emitJSON(rs, s, r)
		return
	}
	fmt.Println("trustcheck — QSDM trust transparency validator")
	fmt.Println()
	if s != nil {
		fmt.Printf("  summary: %d of %d attested (ratio=%.3f, status=%s)\n", s.Attested, s.TotalPublic, s.Ratio, s.NGCServiceStatus)
		if s.LastAttestedAt != nil {
			fmt.Printf("  last attested: %s\n", *s.LastAttestedAt)
		}
		fmt.Printf("  fresh within: %s · last checked: %s\n", s.FreshWithin, s.LastCheckedAt)
	}
	if r != nil {
		fmt.Printf("  recent: %d entries returned\n", r.Count)
	}
	fmt.Println()
	fail := 0
	for _, row := range rs.rows {
		mark := "PASS"
		if !row.ok {
			mark = "FAIL"
			fail++
		}
		if row.msg != "" {
			fmt.Printf("  [%s] %s — %s\n", mark, row.name, row.msg)
		} else {
			fmt.Printf("  [%s] %s\n", mark, row.name)
		}
	}
	fmt.Println()
	total := len(rs.rows)
	fmt.Printf("  %d/%d assertions passed\n", total-fail, total)
}

func emitJSON(rs *results, s *trustSummary, r *trustRecent) {
	type row struct {
		Name   string `json:"name"`
		Pass   bool   `json:"pass"`
		Detail string `json:"detail,omitempty"`
	}
	out := struct {
		Summary *trustSummary `json:"summary,omitempty"`
		Recent  *trustRecent  `json:"recent,omitempty"`
		Rows    []row         `json:"assertions"`
		Pass    bool          `json:"pass"`
	}{Summary: s, Recent: r}
	for _, rr := range rs.rows {
		out.Rows = append(out.Rows, row{Name: rr.name, Pass: rr.ok, Detail: rr.msg})
	}
	out.Pass = rs.allOK()
	_ = json.NewEncoder(os.Stdout).Encode(out)
}

func handleInformational(allow bool, code int, label string, asJSON bool, rs *results) {
	rs.rows = append(rs.rows, result{name: "endpoint/state", ok: allow, msg: fmt.Sprintf("%s (allow=%v)", label, allow)})
	emit(rs, nil, nil, asJSON)
	if allow {
		return
	}
	os.Exit(code)
}
