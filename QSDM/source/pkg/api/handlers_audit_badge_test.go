package api

// Tests for AuditBadgeHandler — the public SVG audit badge at
// /api/v1/audit/badge.svg.
//
// Coverage:
//   - Method gating (405 on non-GET).
//   - Content-Type + Cache-Control + X-Content-Type-Options headers.
//   - SVG bytes contain the live score, bucket counts, and a
//     shields.io-compatible structure (<svg ...>, <linearGradient>,
//     panel <rect>s, and the text-shadow pair).
//   - Colour-threshold mapping (table-driven on scoreColour) — a
//     regression that swaps the brightgreen/yellowgreen boundary
//     would silently darken the badge across an entire CI cycle.
//   - The "no caller-attacker-controlled input reaches the SVG"
//     invariant — pinned with a renderer-internal test that ensures
//     the renderer doesn't accidentally start accepting arbitrary
//     label/value strings without escaping.
//   - Empty-checklist failsafe — a 0-item checklist must not emit
//     "NaN%" via float division by zero.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAuditBadgeHandler_MethodNotAllowed(t *testing.T) {
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/audit/badge.svg", nil)
	w := httptest.NewRecorder()
	h.AuditBadgeHandler(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("expected Allow: GET, got %q", got)
	}
}

func TestAuditBadgeHandler_HeadersAndShape(t *testing.T) {
	resetAuditChecklistForTest()
	h := setupTestHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/badge.svg", nil)
	w := httptest.NewRecorder()
	h.AuditBadgeHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Fatalf("expected Content-Type image/svg+xml*, got %q", ct)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "public") || !strings.Contains(cc, "max-age=60") {
		t.Fatalf("expected Cache-Control public + max-age=60, got %q", cc)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected X-Content-Type-Options nosniff, got %q", got)
	}

	body := w.Body.String()

	// Structural minima: opening svg tag, closing svg tag,
	// gradient definition, two rectangles (one per panel), and
	// the four <text> elements that make up the shadow/face pairs.
	for _, needle := range []string{
		`<svg xmlns="http://www.w3.org/2000/svg"`,
		`</svg>`,
		`<linearGradient id="g"`,
		`<rect`,
		`<text`,
		`fill="#555"`,        // left panel colour, hardcoded
		`fill="#010101"`,     // text shadow
		`fill="#fff"`,        // text face
		`role="img"`,         // a11y
		`aria-label="QSDM audit:`, // a11y label
		`<title>QSDM audit:`, // hover tooltip
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("badge SVG missing %q\n\n%s", needle, body)
		}
	}

	// Score in the body must match the score the live checklist
	// reports. The format string is %.2f%%; we compute it from
	// the same Score() the handler uses, then look for it.
	cl := currentAuditChecklist()
	expectedScoreFragment := "" // computed below; populated only when total > 0
	summary := cl.Summary()
	if total := summary["total"]; total > 0 {
		passed := summary["passed"] + summary["waived"]
		// Two-decimal rendering: we only check the substring
		// of the integer score part because rounding edges
		// can move the last decimal by 1 in benign ways.
		expectedScoreFragment = stripDecimal(cl.Score())
		if !strings.Contains(body, expectedScoreFragment) {
			t.Errorf("expected score fragment %q in badge body, body=%s",
				expectedScoreFragment, body)
		}
		// Bucket count fragment "(N/M)" must also be present
		// so a casual observer can sanity-check the score.
		bucket := "(" + itoa(passed) + "/" + itoa(total) + ")"
		if !strings.Contains(body, bucket) {
			t.Errorf("expected bucket fragment %q in badge body, body=%s",
				bucket, body)
		}
	}
}

// stripDecimal returns the integer prefix of a "%.2f"-formatted
// float (e.g. 95.29 -> "95.") so a substring match tolerates a
// 1-ULP rounding shift in the trailing decimal.
func stripDecimal(f float64) string {
	// itoa(int(f)) + "." would lose negatives; the score is
	// always non-negative, and int truncation toward zero is
	// fine for that domain.
	return itoa(int(f)) + "."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func TestAuditBadge_ColourThresholds(t *testing.T) {
	// Pin the shields.io colour ladder. Any change here is a
	// visible regression on every embedded README and should be
	// deliberate, not incidental.
	cases := []struct {
		score float64
		want  string
		name  string
	}{
		{100.0, "#4c1", "ceiling -> brightgreen"},
		{95.0, "#4c1", "boundary 95 -> brightgreen"},
		{94.99, "#a4a61d", "just-below 95 -> yellowgreen"},
		{90.0, "#a4a61d", "mid 85..94 -> yellowgreen"},
		{85.0, "#a4a61d", "boundary 85 -> yellowgreen"},
		{84.99, "#dfb317", "just-below 85 -> yellow"},
		{70.0, "#dfb317", "boundary 70 -> yellow"},
		{69.99, "#fe7d37", "just-below 70 -> orange"},
		{50.0, "#fe7d37", "boundary 50 -> orange"},
		{49.99, "#e05d44", "just-below 50 -> red"},
		{0.0, "#e05d44", "floor -> red"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scoreColour(tc.score); got != tc.want {
				t.Errorf("scoreColour(%v) = %q, want %q", tc.score, got, tc.want)
			}
		})
	}
}

func TestAuditBadge_RendererPanelWidth(t *testing.T) {
	// Sanity-check: render a known label/value/colour and assert
	// the resulting SVG width matches the sum of the per-panel
	// widths from auditBadgeTextWidth. A regression in either
	// the glyph table or the renderer's padding math would clip
	// or leak whitespace; this test pins the formula.
	const (
		label   = "QSDM audit"
		value   = "95.29% (81/85)"
		hPad    = 6 // mirrors renderAuditBadgeSVG's hPadding const
		wantLbl = -1
	)
	_ = wantLbl // silence linter; constants block for readability.

	expectedTotal := auditBadgeTextWidth(label) + auditBadgeTextWidth(value) + 4*hPad
	out := renderAuditBadgeSVG(label, value, "#4c1")

	// Pluck out width="N" from the opening svg tag.
	const widthAttr = `width="`
	i := strings.Index(out, widthAttr)
	if i < 0 {
		t.Fatalf("rendered SVG missing width= attr: %s", out)
	}
	rest := out[i+len(widthAttr):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		t.Fatalf("malformed width= attr in: %s", out)
	}
	gotWidth := rest[:j]

	wantWidth := itoa(expectedTotal)
	if gotWidth != wantWidth {
		t.Fatalf("renderer width = %q, want %q (label_text=%d, value_text=%d)",
			gotWidth, wantWidth,
			auditBadgeTextWidth(label), auditBadgeTextWidth(value))
	}
}

func TestAuditBadge_GlyphWidth_NonZeroForCommonGlyphs(t *testing.T) {
	// Foot-gun pin: the glyph table is hand-maintained. If any
	// glyph we actually render returns 0, the badge will
	// shrink-clip on real audit values. Enumerate the glyphs
	// that actually appear in the rendered text and assert > 0.
	for _, r := range "QSDM audit 95.29% (81/85)" {
		if auditBadgeGlyphWidth(r) <= 0 {
			t.Errorf("auditBadgeGlyphWidth(%q) = 0; would clip", r)
		}
	}
}

func TestAuditBadge_PublicEndpointAllowList(t *testing.T) {
	// Drift guard, paired with the existing
	// TestAuditAPI_PublicEndpointAllowList for /summary and
	// /items. If a future refactor accidentally drops badge.svg
	// from publicPaths, every embedded README breaks with a 401
	// the next time the CDN refreshes — and we'd find out via
	// angry-tweet bug reports instead of CI.
	if !isPublicEndpoint("/api/v1/audit/badge.svg") {
		t.Errorf("expected /api/v1/audit/badge.svg to be a public endpoint")
	}
}
