package monitoring

import (
	"testing"
	"time"
)

func TestIssueAndConsumeNGCIngestNonce(t *testing.T) {
	ResetNGCIngestNoncesForTest()
	t.Cleanup(ResetNGCIngestNoncesForTest)

	n, exp, err := IssueNGCIngestNonce(time.Minute)
	if err != nil || n == "" || exp == 0 {
		t.Fatalf("issue: n=%q exp=%d err=%v", n, exp, err)
	}
	if !ValidateAndConsumeNGCIngestNonce(n) {
		t.Fatal("first consume should succeed")
	}
	if ValidateAndConsumeNGCIngestNonce(n) {
		t.Fatal("second consume should fail")
	}
}
