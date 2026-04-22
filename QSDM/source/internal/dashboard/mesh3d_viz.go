package dashboard

import "math"

// mesh3DReferenceViz returns JSON for the dashboard WebGL view: a regular tetrahedron of
// four parent cells around a central vertex (Phase-3 “3–5 parents” topology), plus optional
// fifth parent on an outer shell. Coordinates are illustrative, not on-chain state.
func mesh3DReferenceViz(livePeerCount int) map[string]interface{} {
	const scale = 95.0
	// Regular tetrahedron vertices (centered-ish); center cell at origin.
	raw := [][3]float64{
		{1, 1, 1},
		{1, -1, -1},
		{-1, 1, -1},
		{-1, -1, 1},
	}
	cells := []map[string]interface{}{
		{"id": "local", "label": "Local cell", "x": 0.0, "y": 0.0, "z": 0.0, "role": "vertex"},
	}
	for i, p := range raw {
		cells = append(cells, map[string]interface{}{
			"id":    string(rune('A'+i)), // A,B,C,D — stable ids for links
			"label": "Parent " + string(rune('A'+i)),
			"x":     p[0] * scale,
			"y":     p[1] * scale,
			"z":     p[2] * scale,
			"role":  "parent",
		})
	}
	// Optional fifth parent (outer), common upper bound in validator
	phi := math.Pi * (3.0 - math.Sqrt(5.0))
	i := 4.0
	y := 1.0 - (2.0*i)/4.0
	r := math.Sqrt(1.0 - y*y)
	cells = append(cells, map[string]interface{}{
		"id":    "E",
		"label": "Parent E",
		"x":     math.Cos(phi*i) * r * scale * 1.35,
		"y":     y * scale * 1.35,
		"z":     math.Sin(phi*i) * r * scale * 1.35,
		"role":  "parent",
	})

	links := []map[string]interface{}{}
	parentIDs := []string{"A", "B", "C", "D", "E"}
	for _, pid := range parentIDs {
		links = append(links, map[string]interface{}{"from": "local", "to": pid, "kind": "dependency"})
	}
	// Tetra edges among A–D
	tedges := [][2]string{{"A", "B"}, {"A", "C"}, {"A", "D"}, {"B", "C"}, {"B", "D"}, {"C", "D"}}
	for _, e := range tedges {
		links = append(links, map[string]interface{}{"from": e[0], "to": e[1], "kind": "adjacent"})
	}
	// Sparse links from E to two parents (illustrative mesh connectivity)
	links = append(links, map[string]interface{}{"from": "E", "to": "A", "kind": "adjacent"})
	links = append(links, map[string]interface{}{"from": "E", "to": "B", "kind": "adjacent"})

	return map[string]interface{}{
		"title":            "Phase-3 parent mesh (reference geometry)",
		"description":      "Illustrative 3–5 parent layout; not live ledger cells. Drag to orbit, scroll to zoom.",
		"live_peer_count":  livePeerCount,
		"cells":            cells,
		"links":            links,
	}
}
