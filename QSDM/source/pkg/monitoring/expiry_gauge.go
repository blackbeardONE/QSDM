package monitoring

// Audit row rotation-05: rotation monitoring. Surfaces a
// per-secret days-until-expiry gauge so Prometheus can alert when
// any tracked secret is within the operator-configured threshold
// (default = 30 days) and the dashboard can render a rotation panel.
//
// The gauge is labeled by `kind` (tls_cert, jwt_primary,
// jwt_secondary, mtls_client_ca, ...) and optionally by `subject`
// (e.g. cert CN). Each Record* call is concurrency-safe and
// updates the entry in place; multiple kinds coexist in the same
// gauge series.
//
// SEMANTICS:
//   - For TLS certs (and anything with a real x509 NotAfter):
//     we publish (NotAfter - now) in DAYS. The value can go
//     negative if a cert has already expired — that's intentional
//     so the alert fires loud rather than silently dropping the
//     series.
//   - For HMAC secrets / API keys (no intrinsic expiry):
//     we publish (now - last-set-time) in DAYS as a NEGATIVE
//     number reflecting "this is how MANY days OLD the secret is".
//     The alert rule treats values <= -90 as "too old, rotate
//     now" so the same Prometheus expression `<= 0` catches both:
//     real-cert near-expiry AND HMAC-secret over-age. Operators
//     can adjust the rotation-interval threshold via the alert
//     rule, not via this code.
//
// EVIDENCE WIRING:
//   - pkg/api/server.go calls RecordCertExpiry on TLS-cert load.
//   - pkg/api/auth.go calls RecordSecretSetTime on primary/
//     secondary JWT HMAC key install/clear.
//   - pkg/api/security.go calls RecordSecretSetTime on primary/
//     secondary RequestSigner HMAC install/clear.

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"sync"
	"time"
)

// SecretExpiryKind enumerates the kinds of secrets the
// rotation-monitoring gauge tracks. Adding a new kind here is the
// supported extension point — the alert rule consumes the series by
// label match so new kinds are picked up automatically.
type SecretExpiryKind string

const (
	SecretExpiryKindTLSCert       SecretExpiryKind = "tls_cert"
	SecretExpiryKindMTLSClientCA  SecretExpiryKind = "mtls_client_ca"
	SecretExpiryKindJWTPrimary    SecretExpiryKind = "jwt_primary"
	SecretExpiryKindJWTSecondary  SecretExpiryKind = "jwt_secondary"
	SecretExpiryKindRequestSig    SecretExpiryKind = "request_sig_primary"
	SecretExpiryKindRequestSigAlt SecretExpiryKind = "request_sig_secondary"
)

// SecretExpiryEntry is one row of the gauge series. ExpiresAt is
// the wall-clock time the secret stops being valid; for kinds that
// have no intrinsic expiry (HMAC), it carries the SET time, and
// IsAge=true tells the collector to emit the age (set→now) instead
// of (now→expiry).
type SecretExpiryEntry struct {
	Kind      SecretExpiryKind
	Subject   string
	ExpiresAt time.Time
	IsAge     bool
}

var (
	expiryMu      sync.RWMutex
	expiryEntries = map[string]SecretExpiryEntry{}
)

func expiryKey(kind SecretExpiryKind, subject string) string {
	return string(kind) + "/" + subject
}

// RecordCertExpiry registers (or updates) a real x509 expiry for a
// specific cert subject. Audit row rotation-05.
func RecordCertExpiry(kind SecretExpiryKind, subject string, notAfter time.Time) {
	expiryMu.Lock()
	defer expiryMu.Unlock()
	expiryEntries[expiryKey(kind, subject)] = SecretExpiryEntry{
		Kind: kind, Subject: subject, ExpiresAt: notAfter, IsAge: false,
	}
}

// RecordSecretSetTime registers (or updates) the SET time for a
// secret that does not have an intrinsic expiry (HMAC keys, API
// keys, etc). The gauge will publish age-in-days as a NEGATIVE
// number so the same alert expression handles cert-expiry AND
// secret-age in one rule.
func RecordSecretSetTime(kind SecretExpiryKind, subject string, setAt time.Time) {
	expiryMu.Lock()
	defer expiryMu.Unlock()
	expiryEntries[expiryKey(kind, subject)] = SecretExpiryEntry{
		Kind: kind, Subject: subject, ExpiresAt: setAt, IsAge: true,
	}
}

// ClearSecretExpiry removes a tracked entry. Called when a JWT
// secondary key is cleared at cutover so the gauge stops emitting
// a stale "secondary is N days old" series.
func ClearSecretExpiry(kind SecretExpiryKind, subject string) {
	expiryMu.Lock()
	defer expiryMu.Unlock()
	delete(expiryEntries, expiryKey(kind, subject))
}

// SecretExpiryEntries returns a snapshot of every tracked entry
// (copy, safe to mutate). Used by tests and the JSON dashboard
// surface.
func SecretExpiryEntries() []SecretExpiryEntry {
	expiryMu.RLock()
	defer expiryMu.RUnlock()
	out := make([]SecretExpiryEntry, 0, len(expiryEntries))
	for _, e := range expiryEntries {
		out = append(out, e)
	}
	return out
}

// ResetSecretExpiryForTest zeros the registry. Test-only.
func ResetSecretExpiryForTest() {
	expiryMu.Lock()
	defer expiryMu.Unlock()
	expiryEntries = map[string]SecretExpiryEntry{}
}

// SecretExpiryCollector returns a MetricCollector that emits the
// per-entry days_until_expiry gauge. Series:
//
//	qsdm_security_secret_days_until_expiry{kind="tls_cert",subject="..."} = (notAfter - now) / 24h
//	qsdm_security_secret_days_until_expiry{kind="jwt_primary",subject="..."} = -((now - setAt) / 24h)
//
// The negative-for-age trick keeps the alert rule uniform:
//
//	qsdm_security_secret_days_until_expiry < 30   (cert close to expiring)
//	qsdm_security_secret_days_until_expiry < -90  (HMAC secret too old)
//
// A single rule like `... < 30` fires the cert case loud and a
// per-kind threshold (`{kind="jwt_primary"} < -90`) handles the
// HMAC case. The PromQL examples are in the deploy YAML at
// QSDM/deploy/prometheus/alerts/rotation_monitoring.yml.
func SecretExpiryCollector() MetricCollector {
	return func() []Metric {
		expiryMu.RLock()
		defer expiryMu.RUnlock()
		out := make([]Metric, 0, len(expiryEntries))
		now := time.Now()
		for _, e := range expiryEntries {
			var days float64
			if e.IsAge {
				// "Age" mode: publish negative age-in-days.
				delta := now.Sub(e.ExpiresAt)
				days = -delta.Hours() / 24
			} else {
				delta := e.ExpiresAt.Sub(now)
				days = delta.Hours() / 24
			}
			out = append(out, Metric{
				Name:  "qsdm_security_secret_days_until_expiry",
				Help:  "Days until the named secret expires (positive) or, for HMAC kinds, negative age-in-days since set (audit row rotation-05).",
				Type:  MetricGauge,
				Value: days,
				Labels: map[string]string{
					"kind":    string(e.Kind),
					"subject": e.Subject,
				},
			})
		}
		return out
	}
}

// --- Helpers for loading expiry from on-disk PEM certs ---

// RecordCertExpiryFromFile parses a PEM-encoded x509 cert at path,
// extracts NotAfter, and records the resulting gauge entry under
// the supplied kind + subject. Returns the parsed NotAfter for the
// caller's own logging. Used by pkg/api/server.go after the TLS
// listener loads its cert/key pair, and by the autocert path on
// renewal.
func RecordCertExpiryFromFile(kind SecretExpiryKind, subject, path string) (time.Time, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, fmt.Errorf("read cert: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return time.Time{}, fmt.Errorf("no PEM block in %s", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cert: %w", err)
	}
	RecordCertExpiry(kind, subject, cert.NotAfter)
	return cert.NotAfter, nil
}
