package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestStatus_ReportsQSDM20Activation(t *testing.T) {
	prev := chain.QSDM20ActivationHeight()
	t.Cleanup(func() { chain.SetQSDM20ActivationHeight(prev) })
	h := &Handlers{}

	chain.SetQSDM20ActivationHeight(0)
	info := h.buildConsensusAuthInfo(1 << 40)
	if info.QSDM20ActivationHeight != 0 || info.QSDM20Active {
		t.Fatalf("switched off: %+v", info)
	}
	raw, err := json.Marshal(info)
	if err != nil || !strings.Contains(string(raw), `"qsdm20_activation_height":0`) ||
		!strings.Contains(string(raw), `"qsdm20_active":false`) {
		t.Fatalf("status JSON: %s %v", raw, err)
	}

	chain.SetQSDM20ActivationHeight(50)
	if info := h.buildConsensusAuthInfo(48); info.QSDM20ActivationHeight != 50 || info.QSDM20Active {
		t.Fatalf("tip 48: %+v", info)
	}
	info = h.buildConsensusAuthInfo(49)
	if !info.QSDM20Active {
		t.Fatalf("tip 49 (next block 50 accepts tokens): %+v", info)
	}
	raw, err = json.Marshal(info)
	if err != nil || !strings.Contains(string(raw), `"qsdm20_activation_height":50`) ||
		!strings.Contains(string(raw), `"qsdm20_active":true`) {
		t.Fatalf("status JSON: %s %v", raw, err)
	}
}
