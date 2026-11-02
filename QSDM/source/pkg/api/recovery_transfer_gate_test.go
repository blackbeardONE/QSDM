package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

func TestRecoverySignedTransfersEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, enabled, readOnly, producer string
		wantEnabled, wantError            bool
	}{
		{"default follower", "", "1", "", false, false},
		{"default producer", "", "0", "1", false, false},
		{"disabled numeric", "0", "0", "0", false, false},
		{"disabled boolean", "false", "0", "0", false, false},
		{"numeric enabled", "1", "1", "1", true, false},
		{"boolean enabled", "true", "true", "true", true, false},
		{"normalized explicit boolean", " TRUE ", "1", "1", true, false},
		{"reject implicit yes", "yes", "1", "1", false, true},
		{"reject implicit on", "on", "1", "1", false, true},
		{"reject garbage", "tru", "1", "1", false, true},
		{"requires read only", "1", "0", "1", false, true},
		{"requires explicit read only", "1", "", "1", false, true},
		{"reject follower", "1", "1", "0", false, true},
		{"requires producer role", "1", "1", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(recoverySignedTransfersEnv, tc.enabled)
			t.Setenv("QSDM_API_READ_ONLY", tc.readOnly)
			t.Setenv("QSDM_NETWORK_BLOCK_PRODUCER", tc.producer)
			policy, err := loadFollowerWriteAdmission()
			if (err != nil) != tc.wantError {
				t.Fatalf("load error = %v, want error = %v", err, tc.wantError)
			}
			if err == nil && policy.recoverySignedTransfers != tc.wantEnabled {
				t.Fatalf("recovery enabled = %v, want %v", policy.recoverySignedTransfers, tc.wantEnabled)
			}
			if err := ValidateRecoverySignedTransfersEnvironment(); (err != nil) != tc.wantError {
				t.Fatalf("startup validation = %v, want error = %v", err, tc.wantError)
			}
		})
	}
}

func TestRecoverySignedTransferAdmissionRoutes(t *testing.T) {
	policy := followerWriteAdmission{readOnly: true, recoverySignedTransfers: true}
	for _, tc := range []struct {
		name, method, target string
		allow                bool
	}{
		{"exact signed transfer", "POST", "/api/v1/wallet/submit-signed", true},
		{"query does not alias path", "POST", "/api/v1/wallet/submit-signed?client=hive", true},
		{"status", "GET", "/api/v1/status", true},
		{"head", "HEAD", "/api/v1/status", true},
		{"preflight", "OPTIONS", "/api/v1/wallet/submit-signed", true},
		{"login", "POST", "/api/v1/auth/login", true},
		{"register", "POST", "/api/v1/auth/register", true},
		{"logout", "POST", "/api/v1/auth/logout", true},
		{"telemetry", "POST", "/api/v1/monitoring/ngc-proof", true},
		{"unknown auth write", "POST", "/api/v1/auth/arbitrary", false},
		{"auth child alias", "POST", "/api/v1/auth/login/extra", false},
		{"auth wrong method", "DELETE", "/api/v1/auth/login", false},
		{"telemetry child alias", "POST", "/api/v1/monitoring/ngc-proof/extra", false},
		{"mining", "POST", "/api/v1/mining/submit", false},
		{"task action", "POST", "/api/v1/tasks/actions/submit-signed", false},
		{"unsigned transfer", "POST", "/api/v1/wallet/transfer", false},
		{"admin mutation", "POST", "/api/admin/produce-block", false},
		{"unknown version", "POST", "/api/v2/wallet/submit-signed", false},
		{"unknown outside api", "POST", "/arbitrary", false},
		{"put", "PUT", "/api/v1/wallet/submit-signed", false},
		{"delete", "DELETE", "/api/v1/wallet/submit-signed", false},
		{"lowercase method", "post", "/api/v1/wallet/submit-signed", false},
		{"trailing slash", "POST", "/api/v1/wallet/submit-signed/", false},
		{"encoded letter", "POST", "/api/v1/wallet/submit%2Dsigned", false},
		{"encoded separator", "POST", "/api/v1%2Fwallet/submit-signed", false},
		{"encoded api prefix", "POST", "/%61pi/v1/wallet/submit-signed", false},
		{"double slash", "POST", "//api/v1/wallet/submit-signed", false},
		{"inner double slash", "POST", "/api//v1/wallet/submit-signed", false},
		{"dot segment", "POST", "/api/./v1/wallet/submit-signed", false},
		{"dot dot segment", "POST", "/other/../api/v1/wallet/submit-signed", false},
		{"encoded dot segment", "POST", "/api/%2e/v1/wallet/submit-signed", false},
		{"encoded read unchanged", "GET", "/%61pi/v1/status", true},
		{"read normalization unchanged", "GET", "/api//v1/status", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			handler := policy.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.target, nil))
			if called != tc.allow {
				t.Fatalf("handler called = %v, want %v; status=%d body=%s", called, tc.allow, recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("X-QSDM-Write-Admission") != "signed-transfers-only" {
				t.Fatal("recovery admission header missing")
			}
			if !tc.allow && (recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("X-QSDM-Node-Role") != "recovery-producer") {
				t.Fatalf("recovery rejection = %d, headers=%v", recorder.Code, recorder.Header())
			}
		})
	}
}

func TestRecoverySignedTransferRejectsInvalidRawPath(t *testing.T) {
	for _, rawPath := range []string{"/%zz", "/unrelated", "/api/v1/wallet/submit%2dsigned"} {
		req := httptest.NewRequest(http.MethodPost, recoverySignedTransferPath, nil)
		req.URL.RawPath = rawPath
		if isRecoverySignedTransferRequest(req) || recoverySignedTransfersAllows(req) {
			t.Fatalf("invalid or encoded RawPath admitted: %q", rawPath)
		}
	}
}

func TestRecoveryGateRunsBeforeMuxCanonicalRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(recoverySignedTransferPath, func(w http.ResponseWriter, r *http.Request) {
		t.Error("noncanonical request reached transfer handler")
	})
	handler := (followerWriteAdmission{readOnly: true, recoverySignedTransfers: true}).middleware(mux)
	for _, target := range []string{"//api/v1/wallet/submit-signed", "/api//v1/wallet/submit-signed", "/api/v1/auth/../wallet/submit-signed"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, target, nil))
		if recorder.Code != http.StatusServiceUnavailable || recorder.Header().Get("Location") != "" {
			t.Fatalf("%s normalized or redirected: status=%d location=%q", target, recorder.Code, recorder.Header().Get("Location"))
		}
	}
}

func TestRecoveryPathHardeningDoesNotChangeLegacyAdmission(t *testing.T) {
	handler := FollowerReadOnlyMiddleware(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, target := range []string{"//api/v1/wallet/submit-signed", "/api/admin/produce-block", "/api/v1/auth/custom"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, target, nil))
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("legacy follower behavior changed for %s: %d", target, recorder.Code)
		}
		if recorder.Header().Get("X-QSDM-Write-Admission") != "" {
			t.Fatal("legacy follower incorrectly advertises recovery admission")
		}
	}
}

func TestRecoveryPolicyCapturedAtServerConstruction(t *testing.T) {
	for _, initiallyEnabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "cannot enable after startup", true: "cannot disable after startup"}[initiallyEnabled], func(t *testing.T) {
			t.Setenv("QSDM_API_READ_ONLY", "1")
			t.Setenv("QSDM_NETWORK_BLOCK_PRODUCER", "1")
			initial := "0"
			changed := "1"
			if initiallyEnabled {
				initial, changed = changed, initial
			}
			t.Setenv(recoverySignedTransfersEnv, initial)
			logger := logging.NewLogger(filepath.Join(t.TempDir(), "api.log"), false)
			t.Cleanup(func() { _ = logger.Close() })
			server, err := NewServer(&config.Config{JWTHMACSecret: strings.Repeat("s", 32)}, logger, nil, nil, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv(recoverySignedTransfersEnv, changed)
			t.Setenv("QSDM_API_READ_ONLY", "0")
			t.Setenv("QSDM_NETWORK_BLOCK_PRODUCER", "0")
			recorder := httptest.NewRecorder()
			handler := server.writeAdmission.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, recoverySignedTransferPath, nil))
			want := http.StatusServiceUnavailable
			if initiallyEnabled {
				want = http.StatusNoContent
			}
			if recorder.Code != want {
				t.Fatalf("changed environment altered captured policy: got %d want %d", recorder.Code, want)
			}
		})
	}
}

func TestRecoveryInvalidPolicyFailsBeforeServerInitialization(t *testing.T) {
	t.Setenv(recoverySignedTransfersEnv, "1")
	t.Setenv("QSDM_API_READ_ONLY", "1")
	t.Setenv("QSDM_NETWORK_BLOCK_PRODUCER", "0")
	// Nil dependencies deliberately prove validation runs before logger, crypto,
	// user store, or listener initialization.
	server, err := NewServer(nil, nil, nil, nil, nil, nil)
	if err == nil || server != nil || !strings.Contains(err.Error(), "QSDM_NETWORK_BLOCK_PRODUCER") {
		t.Fatalf("invalid recovery environment did not fail early: server=%v error=%v", server, err)
	}
}
