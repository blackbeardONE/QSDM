package dashboard

import "testing"

func TestMesh3DReferenceViz_structure(t *testing.T) {
	p := mesh3DReferenceViz(7)
	if p["live_peer_count"].(int) != 7 {
		t.Fatalf("live_peer_count: %v", p["live_peer_count"])
	}
	cells, ok := p["cells"].([]map[string]interface{})
	if !ok {
		t.Fatalf("cells type %T", p["cells"])
	}
	if len(cells) != 6 { // local + 4 tetra + E
		t.Fatalf("want 6 cells, got %d", len(cells))
	}
	links, ok := p["links"].([]map[string]interface{})
	if !ok {
		t.Fatalf("links type %T", p["links"])
	}
	if len(links) < 5 {
		t.Fatalf("expected several links, got %d", len(links))
	}
}
