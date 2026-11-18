package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
)

type pulseMembersProv struct{ members map[string][]string }

func (p pulseMembersProv) MemberPool(string) (string, bool) { return "", false }
func (p pulseMembersProv) Members(pool string) []string     { return p.members[pool] }
func (p pulseMembersProv) EscrowedProofs(string) map[string]chain.PulseEscrowView {
	return map[string]chain.PulseEscrowView{}
}
func (p pulseMembersProv) Owed(string) map[string]uint64 { return map[string]uint64{} }
func (p pulseMembersProv) LastSeq(string) uint64         { return 0 }
func (p pulseMembersProv) Stats() chain.PulseStats       { return chain.PulseStats{} }

// 2026-11-18: the chain refuses a settlement whose weight owner is not a member of the settling pool, so a pool
// must know its members: /pulse/state?pool= lists them, and only them.
func TestPulseStateListsThePoolsMembers(t *testing.T) {
	poolA := strings.Repeat("a", 64)
	poolB := strings.Repeat("b", 64)
	m1, m2, m3 := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64)
	SetPulseStateProvider(pulseMembersProv{members: map[string][]string{poolA: {m1, m2}, poolB: {m3}}}, func() uint64 { return 5 })
	t.Cleanup(func() { SetPulseStateProvider(nil, nil) })
	h := &Handlers{}
	rr := httptest.NewRecorder()
	h.QSDMPulseStateHandler(rr, httptest.NewRequest(http.MethodGet, "/api/v1/pulse/state?pool="+poolA, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("state: %d %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Pool struct {
			Members []string `json:"members"`
		} `json:"pool"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Pool.Members) != 2 || out.Pool.Members[0] != m1 || out.Pool.Members[1] != m2 {
		t.Fatalf("members of pool A: %v, want [%s %s]", out.Pool.Members, m1, m2)
	}
}
