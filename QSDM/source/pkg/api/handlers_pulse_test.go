package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

type pulseTestPool struct{ added []*mempool.Tx }

func (p *pulseTestPool) Add(tx *mempool.Tx) error { p.added = append(p.added, tx); return nil }

func TestPulseSubmitRefusedWhileInactive(t *testing.T) {
	tp := &pulseTestPool{}
	SetPulseMempool(tp)
	SetPulseStateProvider(chain.NewPulseStateStore(), func() uint64 { return 100 })
	t.Cleanup(func() { SetPulseMempool(nil); SetPulseStateProvider(nil, nil) })
	h := &Handlers{}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/pulse/submit-signed", strings.NewReader(`{}`))
	h.QSDMPulseSubmitSignedHandler(rr, req)
	if rr.Code != http.StatusConflict || len(tp.added) != 0 {
		t.Fatalf("inactive submit: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	h.QSDMPulseStateHandler(rr, httptest.NewRequest(http.MethodGet, "/api/v1/pulse/state", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"active":false`) || !strings.Contains(rr.Body.String(), `"max_fee_bps":300`) {
		t.Fatalf("state: %d %s", rr.Code, rr.Body.String())
	}
}
