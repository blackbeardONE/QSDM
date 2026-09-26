package api

import (
	"fmt"
	"net/http"
	"path"
	"strings"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/envcompat"
)

const recoverySignedTransfersEnv = "QSDM_RECOVERY_SIGNED_TRANSFERS_ENABLED"
const recoverySignedTransferPath = "/api/v1/wallet/submit-signed"
const miningCanarySubmitPath = "/api/v1/mining/submit"

// miningCanaryAdmission holds the predicate installed by
// SetMiningCanaryAdmission; nil means closed.
var miningCanaryAdmission atomic.Pointer[func() bool]

// SetMiningCanaryAdmission installs the HL1 mining canary admission predicate
// (design rev 4 §4.1 step 2), normally the legacy-mining Guard's
// AdmissionOpen. While it returns true, the signed-transfers-only recovery
// policy also admits the exact canonical POST /api/v1/mining/submit. The
// predicate runs on every such request. nil removes it, which closes the
// route again; it is closed by default. Other policies are unaffected: a
// read-only follower still refuses the route, and an unrestricted API already
// admits it.
func SetMiningCanaryAdmission(open func() bool) {
	if open == nil {
		miningCanaryAdmission.Store(nil)
		return
	}
	miningCanaryAdmission.Store(&open)
}

func miningCanaryAdmissionOpen() bool {
	open := miningCanaryAdmission.Load()
	return open != nil && (*open)()
}

// followerWriteAdmission is captured at server construction so changing the
// environment cannot change the admission policy of a running server.
type followerWriteAdmission struct {
	readOnly                bool
	recoverySignedTransfers bool
}

func loadFollowerWriteAdmission() (followerWriteAdmission, error) {
	policy := followerWriteAdmission{
		readOnly: envcompat.Truthy("QSDM_API_READ_ONLY", "QSDM_API_READ_ONLY"),
	}
	switch strings.ToLower(envcompat.Lookup(recoverySignedTransfersEnv, recoverySignedTransfersEnv)) {
	case "", "0", "false":
		return policy, nil
	case "1", "true":
		policy.recoverySignedTransfers = true
	default:
		return followerWriteAdmission{}, fmt.Errorf("%s must be 1, true, 0, or false", recoverySignedTransfersEnv)
	}
	if !policy.readOnly {
		return followerWriteAdmission{}, fmt.Errorf("%s requires QSDM_API_READ_ONLY=1", recoverySignedTransfersEnv)
	}
	if !envcompat.Truthy("QSDM_NETWORK_BLOCK_PRODUCER", "QSDM_NETWORK_BLOCK_PRODUCER") {
		return followerWriteAdmission{}, fmt.Errorf("%s requires QSDM_NETWORK_BLOCK_PRODUCER=1", recoverySignedTransfersEnv)
	}
	return policy, nil
}

// ValidateRecoverySignedTransfersEnvironment is called before node state or
// listeners are initialized. A follower, or an unrestricted API, must not
// accidentally advertise the restricted producer recovery mode.
func ValidateRecoverySignedTransfersEnvironment() error {
	_, err := loadFollowerWriteAdmission()
	return err
}

// FollowerReadOnlyMiddleware prevents a synchronized network follower from
// acknowledging ledger writes it cannot seal. Session management and
// node-local trust telemetry remain available because they do not mutate the
// shared ledger. The default follower policy never permits signed transfers.
func FollowerReadOnlyMiddleware(enabled bool) func(http.Handler) http.Handler {
	return (followerWriteAdmission{readOnly: enabled}).middleware
}

func (policy followerWriteAdmission) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if policy.recoverySignedTransfers {
			w.Header().Set("X-QSDM-Write-Admission", "signed-transfers-only")
		}
		allowed := !policy.readOnly || followerReadOnlyAllows(r.Method, r.URL.Path)
		if policy.recoverySignedTransfers {
			allowed = recoverySignedTransfersAllows(r)
		}
		if allowed {
			next.ServeHTTP(w, r)
			return
		}
		if policy.recoverySignedTransfers {
			w.Header().Set("X-QSDM-Node-Role", "recovery-producer")
			writeErrorResponse(w, http.StatusServiceUnavailable,
				"QSDM recovery allows only signed wallet transfers; other ledger writes remain paused")
			return
		}
		w.Header().Set("X-QSDM-Node-Role", "network-follower")
		writeErrorResponse(w, http.StatusServiceUnavailable,
			"network follower is read-only; send state-changing requests to the authoritative QSDM Core")
	})
}

// Recovery admission is intentionally stricter than the legacy follower policy:
// unknown mutation routes (including administrative routes) fail closed, and
// mux path normalization must not turn a rejected alias into an allowed write.
func recoverySignedTransfersAllows(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if !isCanonicalRecoveryPath(r) {
		return false
	}
	if r.Method == http.MethodPost {
		switch r.URL.Path {
		case "/api/v1/auth/login", "/api/v1/auth/register", "/api/v1/auth/logout", "/api/v1/monitoring/ngc-proof":
			return true
		}
	}
	if isMiningCanarySubmitRequest(r) {
		return miningCanaryAdmissionOpen()
	}
	return isRecoverySignedTransferRequest(r)
}

func isCanonicalRecoveryPath(r *http.Request) bool {
	return strings.HasPrefix(r.URL.Path, "/") && path.Clean(r.URL.Path) == r.URL.Path &&
		(r.URL.RawPath == "" || r.URL.RawPath == r.URL.Path) &&
		r.URL.EscapedPath() == r.URL.Path
}

func isRecoverySignedTransferRequest(r *http.Request) bool {
	// Path is already URL-decoded. Check RawPath as well: EscapedPath falls
	// back to Path when RawPath is invalid, which must not create an alias.
	return r.Method == http.MethodPost && r.URL.Path == recoverySignedTransferPath &&
		(r.URL.RawPath == "" || r.URL.RawPath == recoverySignedTransferPath) &&
		r.URL.EscapedPath() == recoverySignedTransferPath
}

// isMiningCanarySubmitRequest applies the checks of
// isRecoverySignedTransferRequest to the mining submit route.
func isMiningCanarySubmitRequest(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == miningCanarySubmitPath &&
		(r.URL.RawPath == "" || r.URL.RawPath == miningCanarySubmitPath) &&
		r.URL.EscapedPath() == miningCanarySubmitPath
}

func followerReadOnlyAllows(method, path string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if !strings.HasPrefix(path, "/api/v1/") {
		return true
	}
	if strings.HasPrefix(path, "/api/v1/auth/") {
		return true
	}
	return path == "/api/v1/monitoring/ngc-proof"
}
