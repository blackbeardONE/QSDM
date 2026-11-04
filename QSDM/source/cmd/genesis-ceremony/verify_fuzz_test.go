package main

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// FuzzVerifyBundle decodes any JSON as a ceremony bundle, as an auditor
// would decode a published one, and verifies it. VerifyBundle must never
// panic, and a bundle it accepts must meet the documented conditions,
// checked here without VerifyBundle: a dry run of schema version 2 that
// names ML-DSA-87; no treasury allocation, so the total equals the mining
// emission; at least two participants in strictly ascending ID order; and
// for each participant a 2592-byte ML-DSA-87 public key and a 4627-byte
// signature that verifies over the genesis hash with circl directly.
func FuzzVerifyBundle(f *testing.F) {
	b, err := RunCeremony(2, "qsdm-fuzz", "addr", DefaultParams())
	if err != nil {
		f.Fatal(err)
	}
	valid, err := json.Marshal(b)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(valid)
	for _, mutate := range []func(*Bundle){
		func(x *Bundle) { x.SignatureAlg = "ed25519" },
		func(x *Bundle) { x.SchemaVersion = 1 },
		func(x *Bundle) { x.DryRun = false },
		func(x *Bundle) { x.Params.TotalSupplyCell, x.Params.TreasuryAllocationCell = 100_000_000, 10_000_000 },
		func(x *Bundle) { x.Participants[0].PubKeyHex = strings.Repeat("ab", 32) },
		func(x *Bundle) { x.Participants[1].Signature = x.Participants[0].Signature },
		func(x *Bundle) { x.Participants[0], x.Participants[1] = x.Participants[1], x.Participants[0] },
	} {
		c := *b
		c.Participants = append([]Participant(nil), b.Participants...)
		mutate(&c)
		data, err := json.Marshal(&c)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"dry_run":true,"schema_version":2,"signature_algorithm":"ML-DSA-87","participants":[{},{}]}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var got Bundle
		if json.Unmarshal(data, &got) != nil {
			return
		}
		if VerifyBundle(&got) != nil {
			return
		}
		if !got.DryRun || got.SchemaVersion != 2 || got.SignatureAlg != "ML-DSA-87" {
			t.Fatalf("accepted dry_run=%v schema %d algorithm %q", got.DryRun, got.SchemaVersion, got.SignatureAlg)
		}
		if got.Params.TreasuryAllocationCell != 0 || got.Params.TotalSupplyCell != got.Params.MiningEmissionCell {
			t.Fatalf("accepted params %+v", got.Params)
		}
		if len(got.Participants) < 2 {
			t.Fatalf("accepted %d participants", len(got.Participants))
		}
		msg, err := hex.DecodeString(got.GenesisHash)
		if err != nil {
			t.Fatalf("accepted genesis hash %q", got.GenesisHash)
		}
		for i, p := range got.Participants {
			if i > 0 && got.Participants[i-1].ID >= p.ID {
				t.Fatalf("accepted participants out of order: %q then %q", got.Participants[i-1].ID, p.ID)
			}
			pub, perr := hex.DecodeString(p.PubKeyHex)
			sig, serr := hex.DecodeString(p.Signature)
			if perr != nil || serr != nil || len(pub) != 2592 || len(sig) != 4627 {
				t.Fatalf("participant %q: accepted a %d-byte key and a %d-byte signature", p.ID, len(pub), len(sig))
			}
			var pk mldsa87.PublicKey
			if err := pk.UnmarshalBinary(pub); err != nil || !mldsa87.Verify(&pk, msg, nil, sig) {
				t.Fatalf("participant %q: accepted a signature circl does not verify", p.ID)
			}
		}
	})
}
