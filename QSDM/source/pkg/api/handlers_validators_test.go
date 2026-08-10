package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

func testValidatorSet(t *testing.T) *chain.ValidatorSet {
	t.Helper()
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	if err := vs.Register("validator-b", 200); err != nil {
		t.Fatal(err)
	}
	if err := vs.Register("validator-a", 100); err != nil {
		t.Fatal(err)
	}
	vs.RecordBlock("validator-a")
	if _, err := vs.Slash("validator-b", chain.SlashDoubleSign); err != nil {
		t.Fatal(err)
	}
	return vs
}

func TestValidatorsHandlerResponseIsPublicMembershipOnly(t *testing.T) {
	SetValidatorSetProvider(testValidatorSet(t))
	t.Cleanup(func() { SetValidatorSetProvider(nil) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/validators", nil)
	(&Handlers{}).ValidatorsHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var response ValidatorsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Count != 2 || response.ActiveCount != 1 || response.Epoch != 0 {
		t.Fatalf("unexpected summary: %+v", response)
	}
	if response.TotalStake != 290 {
		t.Fatalf("total_stake = %v, want 290", response.TotalStake)
	}
	if len(response.Validators) != 2 ||
		response.Validators[0].Address != "validator-a" ||
		response.Validators[1].Address != "validator-b" {
		t.Fatalf("validators are not address-sorted: %+v", response.Validators)
	}
	if response.Validators[0].BlocksProduced != 1 {
		t.Fatalf("blocks_produced = %d, want 1", response.Validators[0].BlocksProduced)
	}
	if response.Validators[1].Status != chain.ValidatorJailed ||
		response.Validators[1].SlashCount != 1 ||
		response.Validators[1].TotalSlashed != 10 {
		t.Fatalf("public slashing metadata mismatch: %+v", response.Validators[1])
	}
	for _, forbidden := range []string{"registered_at", "jailed_until", "last_block_at", "accounts", "slash_log"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("response leaked forbidden field %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestValidatorsHandlerRequiresProvider(t *testing.T) {
	SetValidatorSetProvider(nil)
	t.Cleanup(func() { SetValidatorSetProvider(nil) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/validators", nil)
	(&Handlers{}).ValidatorsHandler(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestValidatorsHandlerIsGETOnly(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/validators", nil)
	(&Handlers{}).ValidatorsHandler(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != http.MethodGet {
		t.Fatalf("Allow = %q, want GET", allow)
	}
}

func TestValidatorsRouteIsRegisteredAndPublic(t *testing.T) {
	SetValidatorSetProvider(testValidatorSet(t))
	t.Cleanup(func() { SetValidatorSetProvider(nil) })

	server := &Server{
		config: &config.Config{},
		logger: logging.NewSilentLogger(),
	}
	mux := http.NewServeMux()
	server.registerRoutes(mux)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/validators", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("registered route status = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !isPublicEndpoint("/api/v1/validators") {
		t.Fatal("/api/v1/validators must remain public")
	}
}
