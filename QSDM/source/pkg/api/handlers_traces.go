package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var traceUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// ListContractTraces returns recent traces or traces filtered by contract+function.
func (h *Handlers) ListContractTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.contractEngine == nil || h.contractEngine.Tracer() == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "contract tracer not available")
		return
	}
	tr := h.contractEngine.Tracer()
	contractID := strings.TrimSpace(r.URL.Query().Get("contract"))
	function := strings.TrimSpace(r.URL.Query().Get("function"))
	if contractID != "" && function != "" {
		writeJSONResponse(w, http.StatusOK, map[string]interface{}{
			"traces": tr.GetByCall(contractID, function),
			"count":  len(tr.GetByCall(contractID, function)),
		})
		return
	}
	n := 20
	if s := r.URL.Query().Get("n"); s != "" {
		if p, err := strconv.Atoi(s); err == nil && p > 0 {
			n = p
		}
	}
	recent := tr.Recent(n)
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{"traces": recent, "count": len(recent)})
}

// GetContractTrace gets a specific trace by ID.
func (h *Handlers) GetContractTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.contractEngine == nil || h.contractEngine.Tracer() == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "contract tracer not available")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v1/contracts/trace/")
	if id == "" {
		writeErrorResponse(w, http.StatusBadRequest, "trace id required")
		return
	}
	tr, ok := h.contractEngine.Tracer().Get(id)
	if !ok {
		writeErrorResponse(w, http.StatusNotFound, "trace not found")
		return
	}
	writeJSONResponse(w, http.StatusOK, tr)
}

// ContractTraceStats returns aggregate trace stats.
func (h *Handlers) ContractTraceStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.contractEngine == nil || h.contractEngine.Tracer() == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "contract tracer not available")
		return
	}
	writeJSONResponse(w, http.StatusOK, h.contractEngine.Tracer().Stats())
}

// StreamContractTracesWS streams recent traces over websocket for dashboard/dev tools.
func (h *Handlers) StreamContractTracesWS(w http.ResponseWriter, r *http.Request) {
	if h.contractEngine == nil || h.contractEngine.Tracer() == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "contract tracer not available")
		return
	}
	conn, err := traceUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	n := 10
	if s := r.URL.Query().Get("n"); s != "" {
		if p, err := strconv.Atoi(s); err == nil && p > 0 {
			n = p
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		payload := map[string]interface{}{
			"type":      "contract_traces",
			"timestamp": time.Now().UTC(),
			"traces":    h.contractEngine.Tracer().Recent(n),
		}
		b, _ := json.Marshal(payload)
		if err := conn.WriteMessage(websocket.TextMessage, b); err != nil {
			return
		}
		if err := conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err == nil {
			_, _, _ = conn.ReadMessage()
		}
		<-ticker.C
	}
}

