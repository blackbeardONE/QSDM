package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/contracts"
)

// --- Contract API request/response types ---

type DeployContractRequest struct {
	ContractID string          `json:"contract_id"`
	Template   string          `json:"template,omitempty"`
	Code       json.RawMessage `json:"code,omitempty"`
	ABI        *contracts.ABI  `json:"abi,omitempty"`
}

type ExecuteContractRequest struct {
	Function string                 `json:"function"`
	Args     map[string]interface{} `json:"args"`
}

// --- Handlers ---

func (h *Handlers) DeployContract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.contractEngine == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "contract engine not available")
		return
	}

	claims, ok := r.Context().Value("claims").(*Claims)
	if !ok {
		writeErrorResponse(w, http.StatusUnauthorized, "missing authentication")
		return
	}
	if !h.enforceNvidiaLock(w) {
		return
	}

	var req DeployContractRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ContractID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "contract_id is required")
		return
	}

	var code []byte
	var abi *contracts.ABI

	if req.Template != "" {
		tmpl, err := contracts.GetTemplate(req.Template)
		if err != nil {
			writeErrorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		code = tmpl.Code
		abi = tmpl.ABI
	} else {
		code = []byte(req.Code)
		abi = req.ABI
	}
	if abi == nil {
		writeErrorResponse(w, http.StatusBadRequest, "abi is required (provide template or abi)")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	contract, err := h.contractEngine.DeployContract(ctx, req.ContractID, code, abi, claims.Address)
	if err != nil {
		writeErrorResponse(w, http.StatusConflict, err.Error())
		return
	}

	h.logger.Info("Contract deployed", "id", contract.ID, "owner", contract.Owner)
	writeJSONResponse(w, http.StatusCreated, map[string]interface{}{
		"contract_id": contract.ID,
		"owner":       contract.Owner,
		"deployed_at": contract.DeployedAt,
		"functions":   len(contract.ABI.Functions),
	})
}

func (h *Handlers) ExecuteContract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.contractEngine == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "contract engine not available")
		return
	}

	// Extract contract ID from URL: /api/v1/contracts/{id}/execute
	path := r.URL.Path
	parts := strings.Split(strings.TrimPrefix(path, "/api/v1/contracts/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		writeErrorResponse(w, http.StatusBadRequest, "contract_id required in URL path")
		return
	}
	contractID := parts[0]

	if !h.enforceNvidiaLock(w) {
		return
	}

	var req ExecuteContractRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErrorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Function == "" {
		writeErrorResponse(w, http.StatusBadRequest, "function is required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	result, err := h.contractEngine.ExecuteContract(ctx, contractID, req.Function, req.Args)
	if err != nil {
		writeErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"success":   result.Success,
		"output":    result.Output,
		"gas_used":  result.GasUsed,
		"error":     result.Error,
		"timestamp": result.Timestamp,
	})
}

func (h *Handlers) ListContracts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.contractEngine == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "contract engine not available")
		return
	}

	list := h.contractEngine.ListContracts()
	out := make([]map[string]interface{}, 0, len(list))
	for _, c := range list {
		out = append(out, map[string]interface{}{
			"contract_id": c.ID,
			"owner":       c.Owner,
			"deployed_at": c.DeployedAt,
			"functions":   len(c.ABI.Functions),
		})
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"contracts": out,
		"count":     len(out),
	})
}

func (h *Handlers) GetContract(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if h.contractEngine == nil {
		writeErrorResponse(w, http.StatusServiceUnavailable, "contract engine not available")
		return
	}

	contractID := strings.TrimPrefix(r.URL.Path, "/api/v1/contracts/")
	if contractID == "" {
		writeErrorResponse(w, http.StatusBadRequest, "contract_id required")
		return
	}

	contract, err := h.contractEngine.GetContract(contractID)
	if err != nil {
		writeErrorResponse(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"contract_id": contract.ID,
		"owner":       contract.Owner,
		"deployed_at": contract.DeployedAt,
		"abi":         contract.ABI,
		"state":       contract.State,
	})
}

func (h *Handlers) ListContractTemplates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	templates := contracts.GetTemplates()
	out := make([]map[string]interface{}, 0, len(templates))
	for _, t := range templates {
		out = append(out, map[string]interface{}{
			"name":        t.Name,
			"description": t.Description,
			"functions":   len(t.ABI.Functions),
			"events":      len(t.ABI.Events),
		})
	}
	writeJSONResponse(w, http.StatusOK, map[string]interface{}{
		"templates": out,
		"count":     len(out),
	})
}
