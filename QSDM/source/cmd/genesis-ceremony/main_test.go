package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/crypto/sha3"
)

func TestRunCeremony_RoundTripVerifies(t *testing.T) {
	b, err := RunCeremony(5, "qsdm-test", "qsdm-treasury-TEST-0000000000000000000000", DefaultParams())
	if err != nil {
		t.Fatalf("RunCeremony: %v", err)
	}
	if !b.DryRun {
		t.Fatal("bundle must be flagged dry_run=true")
	}
	if b.SchemaVersion != bundleSchemaVersion {
		t.Fatalf("schema version drift: have %d, expected %d", b.SchemaVersion, bundleSchemaVersion)
	}
	if len(b.Participants) != 5 {
		t.Fatalf("expected 5 participants, got %d", len(b.Participants))
	}
	if err := VerifyBundle(b); err != nil {
		t.Fatalf("VerifyBundle on freshly-run bundle: %v", err)
	}
}

func TestRunCeremony_TokenomicsInvariant(t *testing.T) {
	p := DefaultParams()
	p.TreasuryAllocationCell = 12_345_678
	_, err := RunCeremony(3, "qsdm-test", "addr", p)
	if err == nil {
		t.Fatal("expected tokenomics invariant violation to fail")
	}
	if !strings.Contains(err.Error(), "invariant") {
		t.Fatalf("error should mention invariant, got: %v", err)
	}
}

func TestVerifyBundle_RejectsNonDryRun(t *testing.T) {
	b, err := RunCeremony(3, "qsdm-test", "addr", DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	b.DryRun = false
	if err := VerifyBundle(b); err == nil {
		t.Fatal("verifier must refuse to bless a non-dry-run bundle")
	}
}

func TestVerifyBundle_DetectsCommitTamper(t *testing.T) {
	b, err := RunCeremony(3, "qsdm-test", "addr", DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	// Flip one commit byte.
	orig := b.Participants[1].Commit
	commitBytes, _ := hex.DecodeString(orig)
	commitBytes[0] ^= 0xff
	b.Participants[1].Commit = hex.EncodeToString(commitBytes)
	if err := VerifyBundle(b); err == nil {
		t.Fatal("expected verifier to detect commit tamper")
	}
}

func TestVerifyBundle_DetectsRevealTamper(t *testing.T) {
	b, err := RunCeremony(3, "qsdm-test", "addr", DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	rv, _ := hex.DecodeString(b.Participants[0].Reveal)
	rv[0] ^= 0xff
	b.Participants[0].Reveal = hex.EncodeToString(rv)
	if err := VerifyBundle(b); err == nil {
		t.Fatal("expected verifier to detect reveal tamper")
	}
}

func TestVerifyBundle_DetectsSeedTamper(t *testing.T) {
	b, err := RunCeremony(3, "qsdm-test", "addr", DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	// Clobber the stored seed so recomputation disagrees.
	bad := sha3.Sum256([]byte("forged-seed"))
	b.GenesisSeed = hex.EncodeToString(bad[:])
	if err := VerifyBundle(b); err == nil {
		t.Fatal("expected verifier to detect seed tamper")
	}
}

func TestVerifyBundle_DetectsSignatureTamper(t *testing.T) {
	b, err := RunCeremony(3, "qsdm-test", "addr", DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := hex.DecodeString(b.Participants[2].Signature)
	sig[0] ^= 0xff
	b.Participants[2].Signature = hex.EncodeToString(sig)
	if err := VerifyBundle(b); err == nil {
		t.Fatal("expected verifier to detect signature tamper")
	}
}

func TestVerifyBundle_DetectsParticipantReorder(t *testing.T) {
	b, err := RunCeremony(3, "qsdm-test", "addr", DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	b.Participants[0], b.Participants[1] = b.Participants[1], b.Participants[0]
	if err := VerifyBundle(b); err == nil {
		t.Fatal("expected verifier to detect participant reorder")
	}
}

func TestRunCeremony_MinimumParticipants(t *testing.T) {
	if _, err := RunCeremony(1, "qsdm-test", "addr", DefaultParams()); err == nil {
		t.Fatal("ceremony with 1 participant must fail the invariant")
	}
}

func TestBundle_JSONRoundTrip(t *testing.T) {
	b, err := RunCeremony(4, "qsdm-test", "addr", DefaultParams())
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Bundle
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := VerifyBundle(&got); err != nil {
		t.Fatalf("verify after JSON round-trip: %v", err)
	}
	// Re-marshal and assert byte equivalence for stability.
	data2, err := json.Marshal(&got)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	if !bytes.Equal(data, data2) {
		t.Fatal("bundle JSON not idempotent across round-trip")
	}
}

func TestDefaultParams_AddUpToTotal(t *testing.T) {
	p := DefaultParams()
	if p.TotalSupplyCell != p.TreasuryAllocationCell+p.MiningEmissionCell {
		t.Fatalf("default params break the invariant: %d != %d + %d",
			p.TotalSupplyCell, p.TreasuryAllocationCell, p.MiningEmissionCell)
	}
	if p.CoinDecimals != 8 {
		t.Errorf("default decimals drift: %d", p.CoinDecimals)
	}
	if p.SmallestUnitName != "dust" {
		t.Errorf("default smallest unit drift: %q", p.SmallestUnitName)
	}
}
