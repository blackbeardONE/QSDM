package enrollment

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
)

// -------- ValidateEnrollFields (stateless) --------------------

func validEnrollPayload() EnrollPayload {
	return EnrollPayload{
		Kind:      PayloadKindEnroll,
		NodeID:    "alice-rtx4090-01",
		GPUUUID:   "GPU-deadbeef-0000-0000-0000-000000000001",
		HMACKey:   bytes.Repeat([]byte{0x42}, 32),
		StakeDust: mining.MinEnrollStakeDust,
	}
}

func TestValidateEnrollFields_Accept(t *testing.T) {
	if err := ValidateEnrollFields(validEnrollPayload(), "qsdm1alice"); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestValidateEnrollFields_Rejects(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*EnrollPayload)
		sender   string
		wantErr  error
	}{
		{"wrong kind", func(p *EnrollPayload) { p.Kind = PayloadKindUnenroll }, "a", ErrPayloadInvalid},
		{"empty node_id", func(p *EnrollPayload) { p.NodeID = "" }, "a", ErrPayloadInvalid},
		{"long node_id", func(p *EnrollPayload) { p.NodeID = strings.Repeat("x", MaxNodeIDLen+1) }, "a", ErrPayloadInvalid},
		{"bad char node_id", func(p *EnrollPayload) { p.NodeID = "Alice" }, "a", ErrPayloadInvalid},
		{"empty gpu_uuid", func(p *EnrollPayload) { p.GPUUUID = "" }, "a", ErrPayloadInvalid},
		{"space in gpu_uuid", func(p *EnrollPayload) { p.GPUUUID = "GPU-a b" }, "a", ErrPayloadInvalid},
		{"lowercase gpu prefix", func(p *EnrollPayload) { p.GPUUUID = "gpu-abc" }, "a", ErrPayloadInvalid},
		{"long gpu_uuid", func(p *EnrollPayload) { p.GPUUUID = "GPU-" + strings.Repeat("a", MaxGPUUUIDLen) }, "a", ErrPayloadInvalid},
		{"short hmac key", func(p *EnrollPayload) { p.HMACKey = bytes.Repeat([]byte{1}, 16) }, "a", ErrPayloadInvalid},
		{"long hmac key", func(p *EnrollPayload) { p.HMACKey = bytes.Repeat([]byte{1}, MaxHMACKeyLen+1) }, "a", ErrPayloadInvalid},
		{"stake too low", func(p *EnrollPayload) { p.StakeDust = mining.MinEnrollStakeDust - 1 }, "a", ErrStakeMismatch},
		{"stake too high", func(p *EnrollPayload) { p.StakeDust = mining.MinEnrollStakeDust + 1 }, "a", ErrStakeMismatch},
		{"memo too long", func(p *EnrollPayload) { p.Memo = strings.Repeat("x", MaxMemoLen+1) }, "a", ErrPayloadInvalid},
		{"empty sender", func(p *EnrollPayload) {}, "", ErrPayloadInvalid},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			p := validEnrollPayload()
			tc.mutate(&p)
			err := ValidateEnrollFields(p, tc.sender)
			if err == nil {
				t.Fatalf("%s: expected rejection", tc.name)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("%s: want wrap of %v, got %v", tc.name, tc.wantErr, err)
			}
		})
	}
}

// -------- ValidateUnenrollFields ------------------------------

func TestValidateUnenrollFields_Accept(t *testing.T) {
	p := UnenrollPayload{Kind: PayloadKindUnenroll, NodeID: "alice-rtx4090-01"}
	if err := ValidateUnenrollFields(p, "qsdm1alice"); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestValidateUnenrollFields_Rejects(t *testing.T) {
	cases := []struct {
		name   string
		payload UnenrollPayload
		sender string
	}{
		{"wrong kind", UnenrollPayload{Kind: PayloadKindEnroll, NodeID: "a"}, "q"},
		{"empty node_id", UnenrollPayload{Kind: PayloadKindUnenroll}, "q"},
		{"empty sender", UnenrollPayload{Kind: PayloadKindUnenroll, NodeID: "a"}, ""},
		{"reason too long", UnenrollPayload{Kind: PayloadKindUnenroll, NodeID: "a", Reason: strings.Repeat("x", MaxMemoLen+1)}, "q"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateUnenrollFields(tc.payload, tc.sender); err == nil {
				t.Fatalf("%s: expected rejection", tc.name)
			}
		})
	}
}

// -------- ValidateEnrollAgainstState --------------------------

func TestValidateEnrollAgainstState_Accept(t *testing.T) {
	s := NewInMemoryState()
	p := validEnrollPayload()
	if err := ValidateEnrollAgainstState(p, mining.MinEnrollStakeDust, s); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestValidateEnrollAgainstState_RejectsInsufficientBalance(t *testing.T) {
	s := NewInMemoryState()
	p := validEnrollPayload()
	err := ValidateEnrollAgainstState(p, mining.MinEnrollStakeDust-1, s)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("want ErrInsufficientBalance, got %v", err)
	}
}

func TestValidateEnrollAgainstState_RejectsNodeIDTaken(t *testing.T) {
	s := NewInMemoryState()
	// First enrollment goes in.
	p := validEnrollPayload()
	if err := s.ApplyEnroll(EnrollmentRecord{
		NodeID: p.NodeID, Owner: "q1", GPUUUID: p.GPUUUID,
		HMACKey: p.HMACKey, StakeDust: p.StakeDust, EnrolledAtHeight: 1,
	}); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	// Second attempt fails.
	err := ValidateEnrollAgainstState(p, mining.MinEnrollStakeDust, s)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !errors.Is(err, ErrNodeIDTaken) {
		t.Fatalf("want ErrNodeIDTaken, got %v", err)
	}
}

func TestValidateEnrollAgainstState_RejectsGPUUUIDTaken(t *testing.T) {
	s := NewInMemoryState()
	p1 := validEnrollPayload()
	if err := s.ApplyEnroll(EnrollmentRecord{
		NodeID: p1.NodeID, Owner: "q1", GPUUUID: p1.GPUUUID,
		HMACKey: p1.HMACKey, StakeDust: p1.StakeDust, EnrolledAtHeight: 1,
	}); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	// Different node_id, same GPU UUID.
	p2 := validEnrollPayload()
	p2.NodeID = "bob-rtx4090-01"
	err := ValidateEnrollAgainstState(p2, mining.MinEnrollStakeDust, s)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !errors.Is(err, ErrGPUUUIDTaken) {
		t.Fatalf("want ErrGPUUUIDTaken, got %v", err)
	}
}

func TestValidateEnrollAgainstState_NilStateRejects(t *testing.T) {
	p := validEnrollPayload()
	err := ValidateEnrollAgainstState(p, mining.MinEnrollStakeDust, nil)
	if err == nil {
		t.Fatal("expected rejection on nil state")
	}
}

// -------- ValidateUnenrollAgainstState ------------------------

func TestValidateUnenrollAgainstState_Accept(t *testing.T) {
	s := NewInMemoryState()
	p := validEnrollPayload()
	if err := s.ApplyEnroll(EnrollmentRecord{
		NodeID: p.NodeID, Owner: "q1", GPUUUID: p.GPUUUID,
		HMACKey: p.HMACKey, StakeDust: p.StakeDust, EnrolledAtHeight: 1,
	}); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	u := UnenrollPayload{Kind: PayloadKindUnenroll, NodeID: p.NodeID}
	if err := ValidateUnenrollAgainstState(u, "q1", s); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestValidateUnenrollAgainstState_RejectsWrongOwner(t *testing.T) {
	s := NewInMemoryState()
	p := validEnrollPayload()
	if err := s.ApplyEnroll(EnrollmentRecord{
		NodeID: p.NodeID, Owner: "q1", GPUUUID: p.GPUUUID,
		HMACKey: p.HMACKey, StakeDust: p.StakeDust, EnrolledAtHeight: 1,
	}); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	u := UnenrollPayload{Kind: PayloadKindUnenroll, NodeID: p.NodeID}
	err := ValidateUnenrollAgainstState(u, "q2", s)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !errors.Is(err, ErrNodeNotOwned) {
		t.Fatalf("want ErrNodeNotOwned, got %v", err)
	}
}

func TestValidateUnenrollAgainstState_RejectsAlreadyUnenrolled(t *testing.T) {
	s := NewInMemoryState()
	p := validEnrollPayload()
	if err := s.ApplyEnroll(EnrollmentRecord{
		NodeID: p.NodeID, Owner: "q1", GPUUUID: p.GPUUUID,
		HMACKey: p.HMACKey, StakeDust: p.StakeDust, EnrolledAtHeight: 1,
	}); err != nil {
		t.Fatalf("ApplyEnroll: %v", err)
	}
	if err := s.ApplyUnenroll(p.NodeID, 100); err != nil {
		t.Fatalf("ApplyUnenroll: %v", err)
	}
	u := UnenrollPayload{Kind: PayloadKindUnenroll, NodeID: p.NodeID}
	err := ValidateUnenrollAgainstState(u, "q1", s)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !errors.Is(err, ErrNodeAlreadyUnenrolled) {
		t.Fatalf("want ErrNodeAlreadyUnenrolled, got %v", err)
	}
}

func TestValidateUnenrollAgainstState_RejectsUnknownNode(t *testing.T) {
	s := NewInMemoryState()
	u := UnenrollPayload{Kind: PayloadKindUnenroll, NodeID: "never-enrolled"}
	err := ValidateUnenrollAgainstState(u, "q1", s)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !errors.Is(err, ErrNodeNotOwned) {
		t.Fatalf("want ErrNodeNotOwned, got %v", err)
	}
}
