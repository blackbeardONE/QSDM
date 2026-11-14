package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestStatus_ReportsEdgeRelayOnlyPosture(t *testing.T) {
	prev := chain.EdgeRelayOnlyActivationHeight()
	t.Cleanup(func() { chain.SetEdgeRelayOnlyActivationHeight(prev) })
	chain.SetEdgeRelayOnlyActivationHeight(50)
	h := &Handlers{}
	if info := h.buildConsensusAuthInfo(48); info.EdgeRelayOnlyActivationHeight != 50 || info.EdgeRelayOnlyActive {
		t.Fatalf("tip 48: %+v", info)
	}
	info := h.buildConsensusAuthInfo(49)
	if !info.EdgeRelayOnlyActive {
		t.Fatalf("tip 49 (next block 50 is active): %+v", info)
	}
	raw, err := json.Marshal(info)
	if err != nil || !strings.Contains(string(raw), `"edge_relay_only_activation_height":50`) ||
		!strings.Contains(string(raw), `"edge_relay_only_active":true`) {
		t.Fatalf("status JSON: %s %v", raw, err)
	}
	chain.SetEdgeRelayOnlyActivationHeight(0)
	if info := h.buildConsensusAuthInfo(1 << 40); info.EdgeRelayOnlyActive || info.EdgeRelayOnlyActivationHeight != 0 {
		t.Fatalf("unscheduled: %+v", info)
	}
}
