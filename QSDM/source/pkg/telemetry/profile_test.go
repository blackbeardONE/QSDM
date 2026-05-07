package telemetry

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func sampleProfile() *ReferenceProfile {
	return &ReferenceProfile{
		SchemaVersion: SchemaVersion,
		SignerID:      "attester-deadbeefcafebabe",
		HostNote:      "alice's lab Manila",
		IssuedAt:      1_700_000_000,
		CollectorKind: "nvidia-smi",
		GPUs: []GPUObservation{
			{
				UUID:                  "GPU-39925fa6-82f0-0e13-dd28-aa4be2048287",
				Name:                  "NVIDIA GeForce RTX 3050",
				Vendor:                "NVIDIA",
				Architecture:          "ampere",
				ComputeCapability:     "8.6",
				MemoryTotalMB:         8188,
				PCIeGen:               4,
				PCIeWidth:             16,
				PowerMaxW:             130,
				ECCSupported:          false,
				ClockGraphicsBoostMHz: 1755,
				ClockMemoryMHz:        7000,
				DriverVersionsSeen:    []string{"576.28", "566.14"},
				CUDAVersionsSeen:      []string{"12.9"},
				VBIOSVersionsSeen:     []string{"94.06.31.00.20"},
				FirstObservedAt:       1_699_000_000,
				LastObservedAt:        1_700_000_000,
				Observations:          42,
			},
		},
	}
}

func TestSignVerify_RoundTrip(t *testing.T) {
	key := mustKey(t)
	p := sampleProfile()
	if err := p.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if p.Signature == "" {
		t.Fatalf("Sign produced empty Signature")
	}
	if !p.Verify(key) {
		t.Fatalf("Verify rejected its own signature")
	}
	other := mustKey(t)
	if p.Verify(other) {
		t.Fatalf("Verify accepted a wrong-key signature")
	}
}

func TestSign_RejectsShortKey(t *testing.T) {
	p := sampleProfile()
	if err := p.Sign(make([]byte, 8)); err == nil {
		t.Fatalf("Sign accepted 8-byte key")
	}
}

func TestSign_StableAcrossSliceOrder(t *testing.T) {
	// The signature must be invariant to:
	//   - GPU slice order
	//   - DriverVersionsSeen / CUDAVersionsSeen / VBIOSVersionsSeen order
	// because CanonicalForSigning sorts those before
	// hashing. A drift here is the most subtle possible
	// signature bug.
	key := mustKey(t)
	a := sampleProfile()
	a.GPUs = append(a.GPUs, GPUObservation{
		UUID:           "GPU-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		Name:           "NVIDIA GeForce RTX 4090",
		Vendor:         "NVIDIA",
		Architecture:   "ada-lovelace",
		ComputeCapability: "8.9",
		MemoryTotalMB:  24564,
		PowerMaxW:      450,
		FirstObservedAt: 1_700_000_000,
		LastObservedAt:  1_700_000_000,
		Observations:    1,
	})
	if err := a.Sign(key); err != nil {
		t.Fatalf("Sign A: %v", err)
	}
	sigA := a.Signature

	b := sampleProfile()
	b.GPUs = append([]GPUObservation{a.GPUs[1]}, a.GPUs[0])
	// Permute version slice ordering inside the first GPU.
	b.GPUs[1].DriverVersionsSeen = []string{"566.14", "576.28"}
	if err := b.Sign(key); err != nil {
		t.Fatalf("Sign B: %v", err)
	}
	sigB := b.Signature

	if sigA != sigB {
		t.Fatalf("signature drift across slice order:\nA=%s\nB=%s", sigA, sigB)
	}
}

func TestVerify_RejectsTamperedField(t *testing.T) {
	key := mustKey(t)
	p := sampleProfile()
	if err := p.Sign(key); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Mutate a field — Verify must reject.
	p.GPUs[0].MemoryTotalMB = 24564 // claim a 4090's RAM
	if p.Verify(key) {
		t.Fatalf("Verify accepted tampered MemoryTotalMB")
	}
}

func TestVerify_RejectsBadSignatureFormats(t *testing.T) {
	key := mustKey(t)
	p := sampleProfile()
	for _, bad := range []string{"", "not-hex", "ab"} {
		p.Signature = bad
		if p.Verify(key) {
			t.Fatalf("Verify accepted %q", bad)
		}
	}
}

func TestValidate_HappyAndSadPaths(t *testing.T) {
	good := sampleProfile()
	if err := good.Validate(); err != nil {
		t.Fatalf("good profile rejected: %v", err)
	}
	cases := []struct {
		name string
		fn   func(*ReferenceProfile)
	}{
		{"zero schema", func(p *ReferenceProfile) { p.SchemaVersion = 0 }},
		{"empty signer", func(p *ReferenceProfile) { p.SignerID = "" }},
		{"zero issued_at", func(p *ReferenceProfile) { p.IssuedAt = 0 }},
		{"empty gpu uuid", func(p *ReferenceProfile) { p.GPUs[0].UUID = "" }},
		{"reversed timestamps", func(p *ReferenceProfile) {
			p.GPUs[0].FirstObservedAt = 2
			p.GPUs[0].LastObservedAt = 1
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := sampleProfile()
			tc.fn(p)
			if err := p.Validate(); err == nil {
				t.Fatalf("expected validation error for %q", tc.name)
			}
		})
	}
}

func TestJSON_RoundTripPreservesEverything(t *testing.T) {
	p := sampleProfile()
	if err := p.Sign(mustKey(t)); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	enc, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var dec ReferenceProfile
	if err := json.Unmarshal(enc, &dec); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(p.GPUs, dec.GPUs) {
		t.Fatalf("GPUs differ after round-trip:\n in=%+v\nout=%+v", p.GPUs, dec.GPUs)
	}
	if p.Signature != dec.Signature {
		t.Fatalf("Signature drift: in=%s out=%s", p.Signature, dec.Signature)
	}
	if p.SignerID != dec.SignerID {
		t.Fatalf("SignerID drift")
	}
}

func TestMergeWith_StaticFieldsUpdate(t *testing.T) {
	o := &GPUObservation{UUID: "GPU-x"}
	now := int64(100)
	changed := o.MergeWith(GPUObservation{
		UUID:                  "GPU-x",
		Name:                  "NVIDIA RTX 3050",
		ComputeCapability:     "8.6",
		MemoryTotalMB:         8188,
		PCIeGen:               4,
		PCIeWidth:             16,
		PowerMaxW:             130,
		DriverVersionsSeen:    []string{"576.28"},
		ClockGraphicsBoostMHz: 1755,
	}, now)
	if !changed {
		t.Fatalf("first MergeWith did not report changed")
	}
	if o.Name != "NVIDIA RTX 3050" || o.MemoryTotalMB != 8188 || o.PCIeGen != 4 ||
		o.PowerMaxW != 130 || len(o.DriverVersionsSeen) != 1 ||
		o.ClockGraphicsBoostMHz != 1755 {
		t.Fatalf("static fields not populated: %+v", o)
	}
	if o.FirstObservedAt != now || o.LastObservedAt != now || o.Observations != 1 {
		t.Fatalf("lifetime fields wrong: first=%d last=%d obs=%d", o.FirstObservedAt, o.LastObservedAt, o.Observations)
	}
}

func TestMergeWith_VersionSetUnionDedup(t *testing.T) {
	o := &GPUObservation{
		UUID:               "GPU-x",
		DriverVersionsSeen: []string{"576.28"},
	}
	now := int64(200)
	o.MergeWith(GPUObservation{UUID: "GPU-x", DriverVersionsSeen: []string{"576.28", "576.30"}}, now)
	if len(o.DriverVersionsSeen) != 2 {
		t.Fatalf("expected union dedup; got %v", o.DriverVersionsSeen)
	}
	// Re-merge same value: no growth, no spurious change
	// (lifetime counter still ticks).
	prevObs := o.Observations
	changed := o.MergeWith(GPUObservation{UUID: "GPU-x", DriverVersionsSeen: []string{"576.28"}}, now+1)
	if !changed {
		t.Fatalf("MergeWith should always report changed (Observations always advances)")
	}
	if len(o.DriverVersionsSeen) != 2 {
		t.Fatalf("dedup leaked: %v", o.DriverVersionsSeen)
	}
	if o.Observations != prevObs+1 {
		t.Fatalf("Observations did not advance: %d -> %d", prevObs, o.Observations)
	}
}

func TestMergeWith_FirstObservedAt_TakesEarliest(t *testing.T) {
	o := &GPUObservation{
		UUID:            "GPU-x",
		FirstObservedAt: 1000,
	}
	o.MergeWith(GPUObservation{UUID: "GPU-x", FirstObservedAt: 500}, 1500)
	if o.FirstObservedAt != 500 {
		t.Fatalf("FirstObservedAt = %d, want 500", o.FirstObservedAt)
	}
}

func TestMergeWith_RefusesUUIDMismatch(t *testing.T) {
	o := &GPUObservation{UUID: "GPU-x", Observations: 5}
	changed := o.MergeWith(GPUObservation{UUID: "GPU-y"}, 1)
	if changed {
		t.Fatalf("MergeWith with wrong UUID should be a no-op")
	}
	if o.Observations != 5 {
		t.Fatalf("MergeWith mutated Observations on UUID mismatch")
	}
}

func TestCanonicalForSigning_OrderInvariant(t *testing.T) {
	p1 := sampleProfile()
	p2 := sampleProfile()
	// Reverse the version slice in p2 — canonical
	// encoding must sort, so the bytes match.
	p2.GPUs[0].DriverVersionsSeen = []string{"566.14", "576.28"}
	c1, err := p1.CanonicalForSigning()
	if err != nil {
		t.Fatalf("p1: %v", err)
	}
	c2, err := p2.CanonicalForSigning()
	if err != nil {
		t.Fatalf("p2: %v", err)
	}
	if !bytes.Equal(c1, c2) {
		t.Fatalf("canonical bytes differ:\np1=%s\np2=%s", c1, c2)
	}
	// Sanity: the original slice ordering on p1 must be
	// preserved AFTER signing — CanonicalForSigning works
	// on a copy.
	if !reflect.DeepEqual(p1.GPUs[0].DriverVersionsSeen, []string{"576.28", "566.14"}) {
		t.Fatalf("CanonicalForSigning mutated input slice: %v", p1.GPUs[0].DriverVersionsSeen)
	}
}

func TestFreshnessAge_ReportsDelta(t *testing.T) {
	p := &ReferenceProfile{IssuedAt: time.Now().Unix() - 30}
	age := p.FreshnessAge(time.Now())
	if age < 25*time.Second || age > 35*time.Second {
		t.Fatalf("age %s, expected ~30s", age)
	}
}

func TestUnionStringSet_RespectsCap(t *testing.T) {
	dst := []string{"a"}
	added := unionStringSet(&dst, []string{"b", "c", "d"}, 2)
	if !added {
		t.Fatalf("expected change")
	}
	if len(dst) != 2 {
		t.Fatalf("cap leaked, got %d items: %v", len(dst), dst)
	}
}

func TestUnionStringSet_TrimsAndDedups(t *testing.T) {
	dst := []string{"576.28"}
	unionStringSet(&dst, []string{"  576.28  ", "", "576.28", "566.14"}, 100)
	if len(dst) != 2 || dst[0] != "576.28" || !strings.Contains(strings.Join(dst, ","), "566.14") {
		t.Fatalf("unexpected: %v", dst)
	}
}
