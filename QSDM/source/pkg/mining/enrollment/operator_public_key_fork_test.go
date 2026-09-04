package enrollment

import (
	"math"
	"testing"
)

func TestOperatorPublicKeyRetention_DefaultDisabled(t *testing.T) {
	if got := OperatorPublicKeyRetentionHeight(); got != math.MaxUint64 {
		t.Fatalf("default retention height = %d, want MaxUint64", got)
	}
	if RetainOperatorPublicKey(math.MaxUint64 - 1) {
		t.Fatal("public-key retention should be inert before the default sentinel height")
	}
}

func TestOperatorPublicKeyRetention_CanBeScheduled(t *testing.T) {
	prev := OperatorPublicKeyRetentionHeight()
	SetOperatorPublicKeyRetentionHeight(100)
	t.Cleanup(func() { SetOperatorPublicKeyRetentionHeight(prev) })

	if RetainOperatorPublicKey(99) {
		t.Fatal("public-key retention activated before the scheduled height")
	}
	if !RetainOperatorPublicKey(100) {
		t.Fatal("public-key retention did not activate at the scheduled height")
	}
}
