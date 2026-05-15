package api

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestValidateTimestamp_EmptyOK(t *testing.T) {
	if err := ValidateTimestamp(""); err != nil {
		t.Fatalf("empty timestamp must be allowed: %v", err)
	}
}

func TestValidateTimestamp_RFC3339(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339)
	if err := ValidateTimestamp(ts); err != nil {
		t.Fatalf("valid RFC3339 rejected: %v", err)
	}
}

func TestValidateTimestamp_RFC3339Nano(t *testing.T) {
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if err := ValidateTimestamp(ts); err != nil {
		t.Fatalf("valid RFC3339Nano rejected: %v", err)
	}
}

func TestValidateTimestamp_RejectsGarbage(t *testing.T) {
	cases := []string{
		"not-a-timestamp",
		"2025/05/15",
		"@@@",
	}
	for _, c := range cases {
		if err := ValidateTimestamp(c); err == nil {
			t.Errorf("expected error for %q", c)
		} else if !errors.Is(err, ErrInvalidTimestamp) {
			t.Errorf("expected ErrInvalidTimestamp wrap, got %v", err)
		}
	}
}

func TestValidateTimestamp_RejectsFarFuture(t *testing.T) {
	future := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if err := ValidateTimestamp(future); err == nil {
		t.Fatal("expected error for timestamp 10m in the future")
	}
}

func TestValidateTimestamp_AllowsSmallClockSkew(t *testing.T) {
	// 5 seconds in the future is well within MaxClockSkew (30s).
	ts := time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339)
	if err := ValidateTimestamp(ts); err != nil {
		t.Fatalf("small clock skew must be allowed: %v", err)
	}
}

func TestValidateTimestamp_RejectsAncient(t *testing.T) {
	old := time.Now().Add(-48 * time.Hour).UTC().Format(time.RFC3339)
	if err := ValidateTimestamp(old); err == nil {
		t.Fatal("expected error for 48h-old timestamp")
	}
}

func TestValidateAmount_RejectsInfinityAndNaN(t *testing.T) {
	if err := ValidateAmount(math.NaN()); err == nil {
		t.Fatal("NaN must be rejected")
	}
	if err := ValidateAmount(math.Inf(1)); err == nil {
		t.Fatal("+Inf must be rejected")
	}
	if err := ValidateAmount(math.Inf(-1)); err == nil {
		t.Fatal("-Inf must be rejected")
	}
}
