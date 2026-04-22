// Package topology builds JSON projections of the live peer set for the
// dashboard WebGL view and the `/api/v1/network/topology` endpoint.
//
// It lives in a neutral location (not pkg/api and not internal/dashboard) so both
// layers can consume the same projection without introducing an import cycle
// (internal/dashboard already depends on pkg/api for server wiring).
package topology

import (
	"math"
	"sort"
)

// PeerInfo is the minimal per-peer snapshot the dashboard / API need to render
// the live network topology. It is intentionally decoupled from any concrete
// networking type so callers can pass in a projection of their libp2p state.
type PeerInfo struct {
	ID             string  // short peer ID or display label
	Region         string  // optional region label; used to color-group peers
	Reputation     float64 // -1.0 .. 1.0, signed reputation (or any scaled metric)
	Connected      bool    // whether the peer is currently connected
	MessagesInLast int     // messages received from this peer in the last window
	LatencyMs      int     // last measured round-trip latency in ms
}

// BuildLiveView projects a list of peer snapshots into the JSON shape consumed by
// the dashboard WebGL view. It lays peers on a circle in a plane above the local
// node, grouped by region when present, so the visual is stable across calls.
//
// The shape matches the existing mesh3D reference viz (cells + links) so the
// same frontend renderer can consume either the reference geometry or the live
// topology.
func BuildLiveView(localID string, peers []PeerInfo) map[string]interface{} {
	sorted := append([]PeerInfo(nil), peers...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Region != sorted[j].Region {
			return sorted[i].Region < sorted[j].Region
		}
		return sorted[i].ID < sorted[j].ID
	})

	const radius = 110.0
	const height = 45.0

	cells := []map[string]interface{}{
		{
			"id":    localID,
			"label": "Local node",
			"x":     0.0,
			"y":     0.0,
			"z":     0.0,
			"role":  "vertex",
		},
	}

	n := len(sorted)
	links := make([]map[string]interface{}, 0, n)

	if n == 0 {
		return map[string]interface{}{
			"title":           "Live network topology",
			"description":     "No peers connected yet — topology will populate once peers are reachable.",
			"live_peer_count": 0,
			"cells":           cells,
			"links":           links,
		}
	}

	healthyCount := 0
	for idx, p := range sorted {
		angle := (2.0 * math.Pi * float64(idx)) / float64(n)
		x := math.Cos(angle) * radius
		z := math.Sin(angle) * radius
		y := height
		if !p.Connected {
			y = -height
		}
		role := "parent"
		if !p.Connected {
			role = "stale"
		} else if p.Reputation < -0.25 {
			role = "degraded"
		}
		cells = append(cells, map[string]interface{}{
			"id":          p.ID,
			"label":       p.ID,
			"x":           x,
			"y":           y,
			"z":           z,
			"role":        role,
			"region":      p.Region,
			"reputation":  clamp(p.Reputation, -1.0, 1.0),
			"messages_in": p.MessagesInLast,
			"latency_ms":  p.LatencyMs,
			"connected":   p.Connected,
		})
		kind := "dependency"
		if !p.Connected {
			kind = "stale"
		} else if p.Reputation < -0.25 {
			kind = "degraded"
		}
		links = append(links, map[string]interface{}{
			"from": localID,
			"to":   p.ID,
			"kind": kind,
		})
		if p.Connected && p.Reputation >= 0 {
			healthyCount++
		}
	}

	for i := 0; i < n; i++ {
		next := (i + 1) % n
		if n < 2 || i == next {
			continue
		}
		links = append(links, map[string]interface{}{
			"from": sorted[i].ID,
			"to":   sorted[next].ID,
			"kind": "adjacent",
		})
	}

	return map[string]interface{}{
		"title":           "Live network topology",
		"description":     "Peers ranked by region and ID. Hover a peer for reputation and latency.",
		"live_peer_count": n,
		"healthy_peers":   healthyCount,
		"cells":           cells,
		"links":           links,
	}
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
