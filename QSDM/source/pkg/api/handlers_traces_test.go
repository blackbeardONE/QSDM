package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/contracts"
)

func makeTraceHandlers(t *testing.T) *Handlers {
	t.Helper()
	ce := contracts.NewContractEngine(nil)
	abi := &contracts.ABI{
		Functions: []contracts.Function{
			{Name: "balanceOf", StateMutating: false},
		},
	}
	if _, err := ce.DeployContract(context.Background(), "c1", []byte("sim"), abi, "owner"); err != nil {
		t.Fatal(err)
	}
	_, _ = ce.ExecuteContract(context.Background(), "c1", "balanceOf", map[string]interface{}{"address": "alice"})
	return &Handlers{contractEngine: ce}
}

func TestListContractTraces(t *testing.T) {
	h := makeTraceHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contracts/traces?n=5", nil)
	w := httptest.NewRecorder()
	h.ListContractTraces(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if int(body["count"].(float64)) < 1 {
		t.Fatal("expected at least one trace")
	}
}

func TestGetContractTrace(t *testing.T) {
	h := makeTraceHandlers(t)
	recent := h.contractEngine.Tracer().Recent(1)
	if len(recent) == 0 {
		t.Fatal("expected trace")
	}
	id := recent[0].TraceID
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contracts/trace/"+id, nil)
	w := httptest.NewRecorder()
	h.GetContractTrace(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestContractTraceStats(t *testing.T) {
	h := makeTraceHandlers(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contracts/traces/stats", nil)
	w := httptest.NewRecorder()
	h.ContractTraceStats(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestTraceHandlersUnavailable(t *testing.T) {
	h := &Handlers{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contracts/traces", nil)
	w := httptest.NewRecorder()
	h.ListContractTraces(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", w.Code)
	}
}

