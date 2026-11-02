package api

// HL1 mining canary admission in the recovery write policy (design rev 4 §2
// follower_readonly.go row, §4.1 step 2).

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
)

// setCanaryAdmission installs a predicate that returns *open and counts its
// calls, and removes it when the test ends.
func setCanaryAdmission(t *testing.T, open *atomic.Bool) *atomic.Int64 {
	t.Helper()
	calls := &atomic.Int64{}
	SetMiningCanaryAdmission(func() bool {
		calls.Add(1)
		return open.Load()
	})
	t.Cleanup(func() { SetMiningCanaryAdmission(nil) })
	return calls
}

func recoveryPolicyServe(t *testing.T, method, target string, edit func(*http.Request)) (called bool, rec *httptest.ResponseRecorder) {
	t.Helper()
	policy := followerWriteAdmission{readOnly: true, recoverySignedTransfers: true}
	handler := policy.middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(method, target, nil)
	if edit != nil {
		edit(req)
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return called, rec
}

func TestMiningCanaryAdmissionPathCases(t *testing.T) {
	var open atomic.Bool
	open.Store(true)
	calls := setCanaryAdmission(t, &open)
	for _, tc := range []struct {
		name, method, target string
		allow                bool
		predicate            bool // the predicate is consulted
	}{
		{"exact submit", "POST", "/api/v1/mining/submit", true, true},
		{"query does not alias path", "POST", "/api/v1/mining/submit?client=console", true, true},
		{"trailing slash", "POST", "/api/v1/mining/submit/", false, false},
		{"child path", "POST", "/api/v1/mining/submit/extra", false, false},
		{"suffix", "POST", "/api/v1/mining/submit-signed", false, false},
		{"upper case", "POST", "/api/v1/mining/Submit", false, false},
		{"encoded letter", "POST", "/api/v1/mining/submi%74", false, false},
		{"encoded lower-case letter escape", "POST", "/api/v1/mining/%73ubmit", false, false},
		{"encoded separator", "POST", "/api/v1/mining%2Fsubmit", false, false},
		{"encoded lower-case separator", "POST", "/api/v1%2fmining/submit", false, false},
		{"encoded api prefix", "POST", "/%61pi/v1/mining/submit", false, false},
		{"double slash", "POST", "//api/v1/mining/submit", false, false},
		{"inner double slash", "POST", "/api/v1//mining/submit", false, false},
		{"double slash before leaf", "POST", "/api/v1/mining//submit", false, false},
		{"dot segment", "POST", "/api/v1/./mining/submit", false, false},
		{"dot dot segment", "POST", "/api/v1/other/../mining/submit", false, false},
		{"encoded dot segment", "POST", "/api/v1/%2e/mining/submit", false, false},
		{"encoded dot dot segment", "POST", "/api/v1/wallet/%2e%2e/mining/submit", false, false},
		{"unknown version", "POST", "/api/v2/mining/submit", false, false},
		{"put", "PUT", "/api/v1/mining/submit", false, false},
		{"patch", "PATCH", "/api/v1/mining/submit", false, false},
		{"delete", "DELETE", "/api/v1/mining/submit", false, false},
		{"lowercase method", "post", "/api/v1/mining/submit", false, false},
		{"work is not a write", "POST", "/api/v1/mining/work", false, false},
		{"enroll stays blocked", "POST", "/api/v1/mining/enroll", false, false},
		{"unenroll stays blocked", "POST", "/api/v1/mining/unenroll", false, false},
		{"slash stays blocked", "POST", "/api/v1/mining/slash", false, false},
		{"reads unchanged", "GET", "/api/v1/mining/work", true, false},
		{"signed transfer unchanged", "POST", "/api/v1/wallet/submit-signed", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			called, rec := recoveryPolicyServe(t, tc.method, tc.target, nil)
			if called != tc.allow {
				t.Fatalf("handler called = %v, want %v; status=%d", called, tc.allow, rec.Code)
			}
			if !tc.allow && (rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-QSDM-Node-Role") != "recovery-producer") {
				t.Fatalf("rejection = %d, headers=%v", rec.Code, rec.Header())
			}
			if consulted := calls.Load() != before; consulted != tc.predicate {
				t.Fatalf("predicate consulted = %v, want %v", consulted, tc.predicate)
			}
		})
	}
}

func TestMiningCanaryAdmissionRejectsInvalidRawPath(t *testing.T) {
	var open atomic.Bool
	open.Store(true)
	setCanaryAdmission(t, &open)
	for _, rawPath := range []string{"/%zz", "/unrelated", "/api/v1/mining/submi%74", "/api/v1/mining%2Fsubmit", "/api/v1/wallet/submit-signed"} {
		req := httptest.NewRequest(http.MethodPost, miningCanarySubmitPath, nil)
		req.URL.RawPath = rawPath
		if isMiningCanarySubmitRequest(req) || recoverySignedTransfersAllows(req) {
			t.Fatalf("invalid or encoded RawPath admitted: %q", rawPath)
		}
	}
	// Control: the exact request with an empty or identical RawPath passes.
	for _, rawPath := range []string{"", miningCanarySubmitPath} {
		req := httptest.NewRequest(http.MethodPost, miningCanarySubmitPath, nil)
		req.URL.RawPath = rawPath
		if !isMiningCanarySubmitRequest(req) || !recoverySignedTransfersAllows(req) {
			t.Fatalf("canonical request refused with RawPath %q", rawPath)
		}
	}
}

// The route is admitted only while the predicate returns true, and the
// predicate is read on every request.
func TestMiningCanaryAdmissionFollowsPredicate(t *testing.T) {
	SetMiningCanaryAdmission(nil)
	t.Cleanup(func() { SetMiningCanaryAdmission(nil) })
	submit := func() bool {
		called, _ := recoveryPolicyServe(t, http.MethodPost, miningCanarySubmitPath, nil)
		return called
	}
	if submit() {
		t.Fatal("admitted with no predicate installed")
	}
	var open atomic.Bool
	setCanaryAdmission(t, &open)
	for _, state := range []bool{false, true, false, true} {
		open.Store(state)
		if got := submit(); got != state {
			t.Fatalf("predicate %v: admitted = %v", state, got)
		}
	}
	SetMiningCanaryAdmission(nil)
	if submit() {
		t.Fatal("admitted after the predicate was removed")
	}
	// A predicate returning false never affects the signed-transfer route.
	SetMiningCanaryAdmission(func() bool { return false })
	if called, _ := recoveryPolicyServe(t, http.MethodPost, recoverySignedTransferPath, nil); !called {
		t.Fatal("signed transfer refused while mining admission is closed")
	}
}

// Only the recovery policy consults the predicate: a read-only follower still
// refuses mining submits, and an unrestricted API is unchanged.
func TestMiningCanaryAdmissionOnlyInRecoveryPolicy(t *testing.T) {
	var open atomic.Bool
	open.Store(true)
	calls := setCanaryAdmission(t, &open)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	FollowerReadOnlyMiddleware(true)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, miningCanarySubmitPath, nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("X-QSDM-Node-Role") != "network-follower" {
		t.Fatalf("follower admitted mining submit: %d", rec.Code)
	}
	open.Store(false)
	rec = httptest.NewRecorder()
	FollowerReadOnlyMiddleware(false)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, miningCanarySubmitPath, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unrestricted API changed: %d", rec.Code)
	}
	if calls.Load() != 0 {
		t.Fatalf("predicate consulted %d times outside the recovery policy", calls.Load())
	}
}

// Non-canonical aliases are refused before the mux can clean or redirect
// them to the submit handler.
func TestMiningCanaryGateRunsBeforeMuxCanonicalRedirect(t *testing.T) {
	var open atomic.Bool
	open.Store(true)
	setCanaryAdmission(t, &open)
	var reached atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc(miningCanarySubmitPath, func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	handler := (followerWriteAdmission{readOnly: true, recoverySignedTransfers: true}).middleware(mux)
	for _, target := range []string{"//api/v1/mining/submit", "/api//v1/mining/submit", "/api/v1/wallet/../mining/submit", "/api/v1/mining/./submit"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, target, nil))
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Location") != "" {
			t.Fatalf("%s normalized or redirected: status=%d location=%q", target, rec.Code, rec.Header().Get("Location"))
		}
	}
	if reached.Load() != 0 {
		t.Fatal("non-canonical request reached the submit handler")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, miningCanarySubmitPath, nil))
	if rec.Code != http.StatusOK || reached.Load() != 1 {
		t.Fatalf("canonical submit: status=%d reached=%d", rec.Code, reached.Load())
	}
}

// The predicate may be swapped while requests are in flight.
func TestMiningCanaryAdmissionConcurrentSwap(t *testing.T) {
	t.Cleanup(func() { SetMiningCanaryAdmission(nil) })
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				req := httptest.NewRequest(http.MethodPost, miningCanarySubmitPath, nil)
				_ = recoverySignedTransfersAllows(req)
			}
		}()
	}
	for i := 0; i < 200; i++ {
		v := i%2 == 0
		SetMiningCanaryAdmission(func() bool { return v })
		SetMiningCanaryAdmission(nil)
	}
	close(stop)
	wg.Wait()
}
