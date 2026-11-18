package audit

import "regexp"

// hostPathRE matches an absolute path on a server file system (2026-11-18 report: the public /api/v1/audit/items
// notes showed paths such as /opt/qsdm/host_key and /etc/systemd/system/...). Only well-known host roots match, so
// repository paths (pkg/crypto/...) and API routes (/api/v1/...) are left as they are.
var hostPathRE = regexp.MustCompile("(^|[\\s\"'(\\[=:,`])(/(?:opt|root|var|etc|home|srv|usr|tmp|run|mnt|data|proc|sys|boot|lib|media)(?:/[^\\s\"'()\\[\\],;`]*)?)")

// HostPathPlaceholder replaces each absolute host path in public audit text.
const HostPathPlaceholder = "<host path>"

// ScrubHostPaths replaces every absolute host path in s with HostPathPlaceholder.
func ScrubHostPaths(s string) string {
	return hostPathRE.ReplaceAllString(s, "${1}"+HostPathPlaceholder)
}

// PublicView returns the item as public endpoints may show it: its free text with absolute host paths replaced.
func (it ChecklistItem) PublicView() ChecklistItem {
	it.Title = ScrubHostPaths(it.Title)
	it.Description = ScrubHostPaths(it.Description)
	it.Notes = ScrubHostPaths(it.Notes)
	it.ReviewedBy = ScrubHostPaths(it.ReviewedBy)
	return it
}
