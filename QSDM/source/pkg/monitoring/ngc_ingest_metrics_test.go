package monitoring

import (
	"errors"
	"fmt"
	"testing"
)

func TestNGCProofIngestRejectReason(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{errors.New("ngc proof is not valid JSON: EOF"), "invalid_json"},
		{errors.New("ngc proof body size invalid"), "body_too_large"},
		{errors.New("ngc proof missing cuda_proof_hash"), "missing_cuda_hash"},
		{errors.New("invalid, expired, or reused ingest nonce"), "nonce"},
		{errors.New("invalid qsdm_proof_hmac (required"), "hmac"},
		{fmt.Errorf("wrap: %w", errors.New("not valid JSON")), "invalid_json"},
		{errors.New("something else"), "other"},
	}
	for _, tc := range cases {
		if got := NGCProofIngestRejectReason(tc.err); got != tc.want {
			t.Errorf("NGCProofIngestRejectReason(%q) = %q, want %q", tc.err.Error(), got, tc.want)
		}
	}
}

func TestNGCIngestMetricsRoundTrip(t *testing.T) {
	t.Cleanup(ResetNGCIngestMetricsForTest)
	ResetNGCIngestMetricsForTest()
	RecordNGCProofIngestRejected("unauthorized")
	RecordNGCProofIngestAccepted()
	RecordNGCProofIngestAccepted()
	if NGCIngestAcceptedTotal() != 2 {
		t.Fatalf("accepted: %d", NGCIngestAcceptedTotal())
	}
	if NGCIngestRejectedTotal() != 1 {
		t.Fatalf("rejected: %d", NGCIngestRejectedTotal())
	}
}
