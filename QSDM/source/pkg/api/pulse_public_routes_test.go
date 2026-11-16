package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

// rc1115: the PULSE routes through the server's real middleware chain and mux
// (SetupTestHandler), with no session, no CSRF token and no request signature,
// as the hosted pool and Hive call them. rc1114 answered 401 for both reads,
// and the producer's recovery write gate answered 503 for the submit; the
// earlier PULSE tests called the handlers directly and never saw that.

const pulseTestOwner = "13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9"

// pulseRoutesServer builds a server with the live producer's write policy
// (recovery: signed transfers only) or, with recovery false, a plain read-only
// follower. height 0 leaves PULSE unconfigured.
func pulseRoutesServer(t *testing.T, recovery bool, height uint64, tip uint64) http.Handler {
	t.Helper()
	t.Setenv("QSDM_API_READ_ONLY", "1")
	if recovery {
		t.Setenv("QSDM_NETWORK_BLOCK_PRODUCER", "1")
		t.Setenv(recoverySignedTransfersEnv, "1")
	} else {
		t.Setenv(recoverySignedTransfersEnv, "")
	}
	params := chain.PulseParams{}
	if height > 0 {
		p, err := chain.ParsePulseConfig(height, 0, config.PulseDefaultPools(), 2000, 8640)
		if err != nil {
			t.Fatal(err)
		}
		params = p
	}
	if err := chain.SetPulseParams(params); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = chain.SetPulseParams(chain.PulseParams{}) })
	SetPulseMempool(&pulseTestPool{})
	SetPulseStateProvider(chain.NewPulseStateStore(), func() uint64 { return tip })
	t.Cleanup(func() { SetPulseMempool(nil); SetPulseStateProvider(nil, nil) })

	logger := logging.NewLogger(filepath.Join(t.TempDir(), "api.log"), false)
	t.Cleanup(func() { _ = logger.Close() })
	server, err := NewServer(&config.Config{JWTHMACSecret: strings.Repeat("s", 32)}, logger, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return server.SetupTestHandler()
}

func pulseDo(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.RemoteAddr = "198.51.100.7:4000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestPulseRoutesPublicThroughRecoveryProducer(t *testing.T) {
	const height = config.PulseDefaultActivationHeight
	h := pulseRoutesServer(t, true, height, height-10)

	rec := pulseDo(h, http.MethodGet, "/api/v1/pulse/state", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"activation_height":1210000`) {
		t.Fatalf("GET /pulse/state: %d %s, want 200 with the rule", rec.Code, rec.Body.String())
	}
	rec = pulseDo(h, http.MethodGet, "/api/v1/pulse/state?pool="+config.PulseHostedPoolID, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"escrowed_dust"`) {
		t.Fatalf("GET /pulse/state?pool=: %d %s, want 200 with the pool", rec.Code, rec.Body.String())
	}
	rec = pulseDo(h, http.MethodGet, "/api/v1/pulse/member?owner="+pulseTestOwner, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"member":false`) {
		t.Fatalf("GET /pulse/member: %d %s, want 200", rec.Code, rec.Body.String())
	}
	// Before the activation height the gate admits the submit and the handler
	// refuses it itself (409), not the session check (401) or the gate (503).
	rec = pulseDo(h, http.MethodPost, "/api/v1/pulse/submit-signed", "{}")
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST /pulse/submit-signed before activation: %d %s, want 409", rec.Code, rec.Body.String())
	}
}

func TestPulseSubmitReachesTheHandlerWhenActive(t *testing.T) {
	const height = config.PulseDefaultActivationHeight
	h := pulseRoutesServer(t, true, height, height)
	// An empty envelope is not a PULSE transaction: the handler answers 422.
	rec := pulseDo(h, http.MethodPost, "/api/v1/pulse/submit-signed", "{}")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("POST /pulse/submit-signed while active: %d %s, want 422 from the handler", rec.Code, rec.Body.String())
	}
}

func TestPulseRecoveryGateStaysExact(t *testing.T) {
	const height = config.PulseDefaultActivationHeight
	h := pulseRoutesServer(t, true, height, height)
	for _, tc := range []struct{ method, target string }{
		{http.MethodPost, "/api/v1/pulse/submit-signed/"},
		{http.MethodPost, "/api/v1/pulse/./submit-signed"},
		{http.MethodPost, "/api/v1/pulse/state"},
		{http.MethodPut, "/api/v1/pulse/submit-signed"},
		{http.MethodPost, "/api/v1/tasks/actions/submit-signed"},
	} {
		// An alias that is not a public path stops at CSRF (403) before the
		// gate; a public path with the wrong method or route stops at the gate
		// (503). Either way the PULSE handler never runs (it would answer 422).
		rec := pulseDo(h, tc.method, tc.target, "{}")
		switch rec.Code {
		case http.StatusForbidden:
		case http.StatusServiceUnavailable:
			if rec.Header().Get("X-QSDM-Node-Role") != "recovery-producer" {
				t.Fatalf("%s %s: 503 from %q, want the recovery gate", tc.method, tc.target, rec.Header().Get("X-QSDM-Node-Role"))
			}
		default:
			t.Fatalf("%s %s: %d %s, want a refusal before the handler", tc.method, tc.target, rec.Code, rec.Body.String())
		}
	}
}

func TestPulseSubmitClosedWithoutTheRule(t *testing.T) {
	h := pulseRoutesServer(t, true, 0, 100)
	rec := pulseDo(h, http.MethodPost, "/api/v1/pulse/submit-signed", "{}")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-QSDM-Node-Role") != "recovery-producer" {
		t.Fatalf("POST /pulse/submit-signed without PULSE: %d, want the recovery gate's 503", rec.Code)
	}
}

func TestPulseSubmitRefusedOnAFollower(t *testing.T) {
	const height = config.PulseDefaultActivationHeight
	h := pulseRoutesServer(t, false, height, height)
	rec := pulseDo(h, http.MethodPost, "/api/v1/pulse/submit-signed", "{}")
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-QSDM-Node-Role") != "network-follower" {
		t.Fatalf("POST on a follower: %d role %q, want 503 network-follower", rec.Code, rec.Header().Get("X-QSDM-Node-Role"))
	}
	rec = pulseDo(h, http.MethodGet, "/api/v1/pulse/state", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /pulse/state on a follower: %d, want 200", rec.Code)
	}
}

func TestPulseReadsAreHighFrequencyPublicReads(t *testing.T) {
	for _, p := range []string{"/api/v1/pulse/state", "/api/v1/pulse/member"} {
		if !isHighFrequencyPublicReadPath(p, http.MethodGet) {
			t.Fatalf("%s is not a high-frequency public read", p)
		}
	}
	if isHighFrequencyPublicReadPath("/api/v1/pulse/submit-signed", http.MethodPost) {
		t.Fatal("the submit must not use the public-read limit")
	}
}
