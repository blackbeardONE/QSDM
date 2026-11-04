package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestStatus_ReportsEd25519RetirementPosture(t *testing.T) {
	prev := chain.StreamEd25519RetirementHeight()
	t.Cleanup(func() { chain.SetStreamEd25519RetirementHeight(prev) })
	h := &Handlers{}

	chain.SetStreamEd25519RetirementHeight(0)
	info := h.buildConsensusAuthInfo(1 << 40)
	if info.Ed25519TxAccepted || info.StreamEd25519RetirementHeight != 0 || info.StreamEd25519SessionKeysRetired {
		t.Fatalf("unscheduled: %+v", info)
	}
	raw, err := json.Marshal(info)
	if err != nil || !strings.Contains(string(raw), `"ed25519_tx_accepted":false`) ||
		!strings.Contains(string(raw), `"stream_ed25519_retirement_height":0`) ||
		!strings.Contains(string(raw), `"stream_ed25519_session_keys_retired":false`) {
		t.Fatalf("status JSON: %s %v", raw, err)
	}

	chain.SetStreamEd25519RetirementHeight(50)
	if info := h.buildConsensusAuthInfo(48); info.StreamEd25519RetirementHeight != 50 || info.StreamEd25519SessionKeysRetired {
		t.Fatalf("tip 48: %+v", info)
	}
	info = h.buildConsensusAuthInfo(49)
	if !info.StreamEd25519SessionKeysRetired || info.Ed25519TxAccepted {
		t.Fatalf("tip 49 (next block 50 is governed): %+v", info)
	}
	raw, err = json.Marshal(info)
	if err != nil || !strings.Contains(string(raw), `"stream_ed25519_retirement_height":50`) ||
		!strings.Contains(string(raw), `"stream_ed25519_session_keys_retired":true`) {
		t.Fatalf("status JSON: %s %v", raw, err)
	}
}
