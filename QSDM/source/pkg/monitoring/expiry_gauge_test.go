package monitoring

// Audit row rotation-05: rotation monitoring. Pins the contract for
// the qsdm_security_secret_days_until_expiry gauge.

import (
	"math"
	"testing"
	"time"
)

func TestSecretExpiry_CertExpiry_PositiveDays(t *testing.T) {
	ResetSecretExpiryForTest()
	defer ResetSecretExpiryForTest()

	// Cert that expires in ~10 days.
	notAfter := time.Now().Add(10 * 24 * time.Hour)
	RecordCertExpiry(SecretExpiryKindTLSCert, "api.qsdm.tech", notAfter)

	metrics := SecretExpiryCollector()()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	m := metrics[0]
	if m.Name != "qsdm_security_secret_days_until_expiry" {
		t.Fatalf("unexpected metric name: %s", m.Name)
	}
	if m.Type != MetricGauge {
		t.Fatalf("expected MetricGauge, got %v", m.Type)
	}
	if m.Labels["kind"] != "tls_cert" {
		t.Fatalf("kind label: got %q", m.Labels["kind"])
	}
	if m.Labels["subject"] != "api.qsdm.tech" {
		t.Fatalf("subject label: got %q", m.Labels["subject"])
	}
	// Allow some clock slop; the value should be in (9.99, 10.0).
	if m.Value < 9.99 || m.Value > 10.0 {
		t.Fatalf("expected ~10 days, got %f", m.Value)
	}
}

func TestSecretExpiry_CertExpiry_NegativeDaysWhenExpired(t *testing.T) {
	ResetSecretExpiryForTest()
	defer ResetSecretExpiryForTest()

	// Cert that expired 3 days ago.
	notAfter := time.Now().Add(-3 * 24 * time.Hour)
	RecordCertExpiry(SecretExpiryKindTLSCert, "old.example.com", notAfter)

	metrics := SecretExpiryCollector()()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	if metrics[0].Value > -3.0+0.01 || metrics[0].Value < -3.01 {
		t.Fatalf("expected ~-3 days (already expired), got %f", metrics[0].Value)
	}
}

func TestSecretExpiry_HMACAge_NegativeDays(t *testing.T) {
	ResetSecretExpiryForTest()
	defer ResetSecretExpiryForTest()

	// HMAC secret set 45 days ago.
	setAt := time.Now().Add(-45 * 24 * time.Hour)
	RecordSecretSetTime(SecretExpiryKindJWTPrimary, "jwt-hmac-primary", setAt)

	metrics := SecretExpiryCollector()()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	m := metrics[0]
	if m.Labels["kind"] != "jwt_primary" {
		t.Fatalf("kind label: got %q", m.Labels["kind"])
	}
	// Age-mode publishes negative age-in-days. 45-day-old secret
	// should be ~-45.
	if math.Abs(m.Value-(-45.0)) > 0.05 {
		t.Fatalf("expected ~-45 days (negative age), got %f", m.Value)
	}
}

func TestSecretExpiry_HMACAge_FreshKeyIsNearZero(t *testing.T) {
	ResetSecretExpiryForTest()
	defer ResetSecretExpiryForTest()

	// Just-set key: age ~= 0.
	RecordSecretSetTime(SecretExpiryKindJWTPrimary, "jwt-hmac-primary", time.Now())

	metrics := SecretExpiryCollector()()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(metrics))
	}
	// Allow up to 1 second of slop = ~1.2e-5 days. Should be very near 0.
	if math.Abs(metrics[0].Value) > 0.001 {
		t.Fatalf("expected ~0 days for fresh key, got %f", metrics[0].Value)
	}
}

func TestSecretExpiry_ClearRemovesEntry(t *testing.T) {
	ResetSecretExpiryForTest()
	defer ResetSecretExpiryForTest()

	RecordSecretSetTime(SecretExpiryKindJWTSecondary, "jwt-hmac-secondary", time.Now())
	if len(SecretExpiryCollector()()) != 1 {
		t.Fatal("expected 1 metric after set")
	}
	ClearSecretExpiry(SecretExpiryKindJWTSecondary, "jwt-hmac-secondary")
	if got := len(SecretExpiryCollector()()); got != 0 {
		t.Fatalf("expected 0 metrics after clear, got %d", got)
	}
}

func TestSecretExpiry_MultipleEntries_AllEmitted(t *testing.T) {
	ResetSecretExpiryForTest()
	defer ResetSecretExpiryForTest()

	RecordCertExpiry(SecretExpiryKindTLSCert, "api.qsdm.tech", time.Now().Add(60*24*time.Hour))
	RecordCertExpiry(SecretExpiryKindMTLSClientCA, "client-ca", time.Now().Add(180*24*time.Hour))
	RecordSecretSetTime(SecretExpiryKindJWTPrimary, "jwt-hmac-primary", time.Now().Add(-30*24*time.Hour))
	RecordSecretSetTime(SecretExpiryKindJWTSecondary, "jwt-hmac-secondary", time.Now().Add(-2*24*time.Hour))

	metrics := SecretExpiryCollector()()
	if len(metrics) != 4 {
		t.Fatalf("expected 4 metrics, got %d", len(metrics))
	}
	// Every metric must have non-empty kind + subject labels.
	for _, m := range metrics {
		if m.Labels["kind"] == "" {
			t.Fatalf("metric with empty kind label: %+v", m)
		}
		if m.Labels["subject"] == "" {
			t.Fatalf("metric with empty subject label: %+v", m)
		}
	}
}

func TestSecretExpiry_UpdateOverwrites(t *testing.T) {
	ResetSecretExpiryForTest()
	defer ResetSecretExpiryForTest()

	// First registration: 100 days out.
	RecordCertExpiry(SecretExpiryKindTLSCert, "api.qsdm.tech", time.Now().Add(100*24*time.Hour))
	// Second registration of the SAME kind+subject: 5 days out
	// (new cert installed).
	RecordCertExpiry(SecretExpiryKindTLSCert, "api.qsdm.tech", time.Now().Add(5*24*time.Hour))

	metrics := SecretExpiryCollector()()
	if len(metrics) != 1 {
		t.Fatalf("expected 1 metric (update, not append), got %d", len(metrics))
	}
	if metrics[0].Value > 5.0 || metrics[0].Value < 4.99 {
		t.Fatalf("expected ~5 days, got %f", metrics[0].Value)
	}
}
