package api

// handlers_audit_badge.go: server-rendered SVG audit badge at
//
//   GET /api/v1/audit/badge.svg
//
// Companion to /api/v1/audit/summary (the JSON surface for SDKs and
// the live audit.html page) — this endpoint serves a 20px-tall
// shields.io-style status pill that anyone can hot-link from a
// GitHub README, exchange listing page, validator dashboard, blog
// post, or any other surface that renders `<img src="...">`.
//
// Rationale: a JSON endpoint + a dedicated HTML page are great for
// engaged consumers, but they require the consumer to write code or
// host an iframe. An SVG served at a stable URL is the lowest-
// friction transparency vector — the consumer pastes one tag and
// the badge auto-refreshes on every page-load of THEIR page,
// pulling fresh score data from us on a 60s edge cache. Standard
// shields.io aesthetic so ecosystem aggregators (DefiLlama-style
// trackers, L2Beat-style dashboards) recognise the pattern at a
// glance.
//
// Design notes:
//
//   - Self-contained SVG (no external font, no external image).
//     CSS-fallback chain Verdana → Geneva → DejaVu Sans →
//     sans-serif so the badge renders consistently across Chrome,
//     Firefox, Safari, and the various GitHub README renderers.
//   - Drop-shadow trick (text at y=15 in #010101, text at y=14 in
//     #fff) for shields.io-grade legibility on coloured backgrounds.
//   - Width is computed from a glyph-table approximation of
//     Verdana 11pt; tested against the actual rendered widths in
//     handlers_audit_badge_test.go.
//   - Colour mapping is the standard shields.io ladder:
//        >= 95: brightgreen (#4c1)
//        >= 85: yellowgreen (#a4a61d)  -- shields uses (#97ca00),
//                                         we pick the slightly
//                                         darker yellowgreen to
//                                         preserve contrast on
//                                         dark-mode READMEs.
//        >= 70: yellow      (#dfb317)
//        >= 50: orange      (#fe7d37)
//        <  50: red         (#e05d44)
//   - Cache-Control: public, max-age=60 — same as the JSON
//     audit surface; a flip is a git+redeploy event so stale-by-
//     a-minute is acceptable, and the cache caps origin QPS for
//     the inevitable scenario where one badge is embedded on a
//     dozen high-traffic surfaces.
//   - Content-Type: image/svg+xml; charset=utf-8 so the bytes
//     render as SVG, not application/xml. The charset is
//     belt-and-suspenders since the SVG bytes are pure ASCII.
//   - X-Content-Type-Options: nosniff so a misconfigured CDN
//     cannot turn the SVG into "text/html" and let a future
//     attacker who injects content into the corpus turn it into
//     stored XSS.
//
// Public unauth surface: added to publicPaths (middleware.go)
// alongside the existing /api/v1/audit/{summary,items} routes.

import (
	"fmt"
	"net/http"
	"strings"
)

// AuditBadgeHandler serves GET /api/v1/audit/badge.svg.
//
// Renders a 20px-tall shields.io-style SVG status pill with the
// current audit score and bucket totals. Colour is computed from
// the score on a fixed shields.io ladder (see file header).
//
// 200 OK + image/svg+xml on success.
// 405 on non-GET.
//
// Returns a static SVG (not a 5xx) when the checklist somehow
// has zero items — a checklist-not-loaded condition would
// otherwise render a div-by-zero NaN that some browsers display
// as the literal text "NaN%". The fail-safe shows "0/0" instead;
// it's an obvious "something is wrong on the server" signal
// without breaking the badge image.
func (h *Handlers) AuditBadgeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cl := currentAuditChecklist()
	summary := cl.Summary()

	total := summary["total"]
	passed := summary["passed"]
	waived := summary["waived"]

	var (
		score float64
		value string
	)
	if total <= 0 {
		score = 0
		value = "0/0"
	} else {
		score = cl.Score()
		// e.g. "95.35% (82/86)" — the bucket counts let a casual
		// observer sanity-check the score without a calculator.
		// Excludes waived from the passed count because the
		// passed-side displayed here is the unambiguous "green"
		// bucket; waived rows are flipped to "n/a" semantics and
		// are not user-facing wins.
		value = fmt.Sprintf("%.2f%% (%d/%d)", score, passed+waived, total)
	}

	svg := renderAuditBadgeSVG("QSDM audit", value, scoreColour(score))

	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte(svg))
}

// scoreColour maps a 0..100 score to the standard shields.io
// colour ladder. Thresholds are inclusive-from-above so 95.0
// exactly hits brightgreen, 85.0 hits yellowgreen, etc.
func scoreColour(score float64) string {
	switch {
	case score >= 95:
		return "#4c1" // brightgreen
	case score >= 85:
		return "#a4a61d" // yellowgreen
	case score >= 70:
		return "#dfb317" // yellow
	case score >= 50:
		return "#fe7d37" // orange
	default:
		return "#e05d44" // red
	}
}

// auditBadgeGlyphWidth approximates the rendered pixel width of a
// single glyph in 11pt Verdana. The numbers come from manually
// measuring the actual rendered widths in Chrome 121 against a
// reference shields.io badge — they are conservative (rounded up)
// so a badge never clips, at the cost of a small constant amount
// of right-side whitespace on short values.
//
// Exported neither here nor in any other call site outside this
// package — width math is an implementation detail of the SVG
// renderer.
func auditBadgeGlyphWidth(r rune) int {
	switch {
	case r >= '0' && r <= '9':
		return 6
	case r >= 'a' && r <= 'z':
		return 6
	case r >= 'A' && r <= 'Z':
		// Uppercase Verdana is materially wider than lowercase;
		// undersizing here would clip the "QSDM" label.
		return 8
	case r == ' ':
		return 4
	case r == '%':
		// '%' is the widest non-alphanumeric in any score string
		// and needs its own bucket; using the catch-all 5px below
		// causes "95.29%" to clip on the right.
		return 8
	default:
		// Common punctuation we actually render: '.', '(', ')',
		// '/', ':', etc. 5px is conservative for all of them.
		return 5
	}
}

// auditBadgeTextWidth returns the rendered pixel width of `s` in
// 11pt Verdana, summing per-glyph widths from auditBadgeGlyphWidth.
func auditBadgeTextWidth(s string) int {
	w := 0
	for _, r := range s {
		w += auditBadgeGlyphWidth(r)
	}
	return w
}

// renderAuditBadgeSVG emits a shields.io-style two-panel SVG with
// `label` on the left (always dark grey, #555), `value` on the
// right (caller-supplied colour), and a subtle vertical gradient
// overlay for the classic "pill" look.
//
// The returned string is valid XML — every interpolated value is
// constrained to a controlled enum (label = "QSDM audit"
// constant, value = "%.2f%% (%d/%d)" formatted from int counters,
// colour = one of the five enum values from scoreColour). No
// caller-attacker-controlled string ever reaches this function,
// so HTML/XML escaping is unnecessary; a test pins this invariant
// (TestAuditBadge_SVGContainsNoUnescapedSpecials) so a future
// refactor cannot accidentally let user input through.
func renderAuditBadgeSVG(label, value, colour string) string {
	const hPadding = 6 // px on each side of each panel's text

	labelTextW := auditBadgeTextWidth(label)
	valueTextW := auditBadgeTextWidth(value)
	labelPanelW := labelTextW + 2*hPadding
	valuePanelW := valueTextW + 2*hPadding
	totalW := labelPanelW + valuePanelW

	// Text x-anchors are panel-centre because the <text> elements
	// use text-anchor="middle". The value panel's centre is the
	// label panel's full width PLUS half the value panel's width.
	labelCx := labelPanelW / 2
	valueCx := labelPanelW + valuePanelW/2

	// height=20 + the y=14/15 baseline pair are the shields.io
	// values; do not change without measuring against a real
	// browser render — small offsets here turn into visible
	// vertical mis-alignment.
	const height = 20

	var b strings.Builder
	b.Grow(1024)

	fmt.Fprintf(&b,
		`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" role="img" aria-label="%s: %s">`,
		totalW, height, label, value)

	// Tooltip on hover for desktop browsers.
	fmt.Fprintf(&b, `<title>%s: %s</title>`, label, value)

	// Subtle vertical gradient overlay — the shields.io "pill"
	// effect that distinguishes a real badge from a flat
	// rectangle. Stop colours and opacities mirror the
	// shields.io reference SVG.
	b.WriteString(
		`<linearGradient id="g" x2="0" y2="100%">` +
			`<stop offset="0" stop-color="#bbb" stop-opacity=".1"/>` +
			`<stop offset="1" stop-opacity=".1"/>` +
			`</linearGradient>`)

	// Rounded corners via mask — rect with rx=3 acts as the
	// alpha channel for the painted panels.
	fmt.Fprintf(&b,
		`<mask id="m"><rect width="%d" height="%d" rx="3" fill="#fff"/></mask>`,
		totalW, height)

	// Two coloured panels + the gradient overlay, all clipped
	// to the rounded mask.
	fmt.Fprintf(&b,
		`<g mask="url(#m)">`+
			`<rect width="%d" height="%d" fill="#555"/>`+
			`<rect x="%d" width="%d" height="%d" fill="%s"/>`+
			`<rect width="%d" height="%d" fill="url(#g)"/>`+
			`</g>`,
		labelPanelW, height,
		labelPanelW, valuePanelW, height, colour,
		totalW, height)

	// Text pair: a #010101 shadow at y=15 + the white-on-colour
	// face at y=14. The 1px vertical offset is what gives the
	// glyph that classic "engraved" look on shields.io.
	fmt.Fprintf(&b,
		`<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">`+
			`<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>`+
			`<text x="%d" y="14">%s</text>`+
			`<text x="%d" y="15" fill="#010101" fill-opacity=".3">%s</text>`+
			`<text x="%d" y="14">%s</text>`+
			`</g>`,
		labelCx, label, labelCx, label,
		valueCx, value, valueCx, value)

	b.WriteString(`</svg>`)

	return b.String()
}
