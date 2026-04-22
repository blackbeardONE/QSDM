package networking

import "testing"

func TestNewEvidenceP2PRelay_NilIngress(t *testing.T) {
	_, err := NewEvidenceP2PRelay(nil, nil, "self")
	if err == nil {
		t.Fatal("expected error for nil ingress")
	}
}
