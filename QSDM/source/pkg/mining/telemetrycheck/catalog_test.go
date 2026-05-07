package telemetrycheck

import (
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/telemetry"
)

func TestCatalog_StartsEmpty(t *testing.T) {
	c := NewCatalog()
	if !c.Empty() {
		t.Fatalf("expected empty")
	}
	total, signers, skus := c.Counters()
	if total != 0 || signers != 0 || skus != 0 {
		t.Fatalf("counters = %d/%d/%d", total, signers, skus)
	}
}

func TestCatalog_LoadBaselineKnowsCommonSKUs(t *testing.T) {
	c := NewCatalog()
	added := c.LoadBaseline()
	if added < 15 {
		t.Fatalf("baseline added only %d entries (want ≥15 to cover RTX 3050..H100)", added)
	}
	for _, sku := range []string{
		"NVIDIA GeForce RTX 3050",
		"NVIDIA GeForce RTX 4090",
		"NVIDIA H100 80GB HBM3",
	} {
		obs := c.LookupByName(sku)
		if len(obs) == 0 {
			t.Errorf("baseline missing %q", sku)
		}
	}
}

func TestCatalog_ApplyAcceptsSignedProfile(t *testing.T) {
	c := NewCatalog()
	p := &telemetry.ReferenceProfile{
		SchemaVersion: telemetry.SchemaVersion,
		SignerID:      "attester-deadbeefcafebabe",
		IssuedAt:      1_700_000_000,
		GPUs: []telemetry.GPUObservation{
			{Name: "NVIDIA GeForce RTX 3050", ComputeCapability: "8.6", Architecture: "ampere"},
		},
	}
	added, err := c.Apply(p)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d", added)
	}

	obs := c.LookupByName("NVIDIA GeForce RTX 3050")
	if len(obs) != 1 {
		t.Fatalf("len(obs) = %d", len(obs))
	}
	srcs := c.LookupSourcesByName("NVIDIA GeForce RTX 3050")
	if len(srcs) != 1 || srcs[0] != "attester-deadbeefcafebabe" {
		t.Fatalf("sources = %v", srcs)
	}
}

func TestCatalog_ApplyRejectsNilOrEmptySigner(t *testing.T) {
	c := NewCatalog()
	if _, err := c.Apply(nil); err == nil {
		t.Fatalf("nil profile accepted")
	}
	if _, err := c.Apply(&telemetry.ReferenceProfile{}); err == nil {
		t.Fatalf("empty signer accepted")
	}
}

func TestCatalog_ApplyIgnoresStaleProfile(t *testing.T) {
	c := NewCatalog()
	first := &telemetry.ReferenceProfile{
		SignerID: "attester-x", IssuedAt: 100,
		GPUs: []telemetry.GPUObservation{
			{Name: "NVIDIA GeForce RTX 3050", ComputeCapability: "8.6"},
			{Name: "NVIDIA GeForce RTX 3060", ComputeCapability: "8.6"},
		},
	}
	stale := &telemetry.ReferenceProfile{
		SignerID: "attester-x", IssuedAt: 50, // earlier
		GPUs: []telemetry.GPUObservation{
			{Name: "NVIDIA H100", ComputeCapability: "9.0"},
		},
	}
	c.Apply(first)
	added, err := c.Apply(stale)
	if err != nil {
		t.Fatalf("stale Apply errored: %v", err)
	}
	if added != 0 {
		t.Fatalf("stale added = %d, want 0", added)
	}
	if len(c.LookupByName("NVIDIA H100")) != 0 {
		t.Fatalf("stale profile leaked into catalog")
	}
}

func TestCatalog_ApplyReplacesPriorEntriesPerSigner(t *testing.T) {
	c := NewCatalog()
	p1 := &telemetry.ReferenceProfile{
		SignerID: "attester-x", IssuedAt: 100,
		GPUs: []telemetry.GPUObservation{
			{Name: "NVIDIA GeForce RTX 3050", ComputeCapability: "8.6"},
			{Name: "NVIDIA GeForce RTX 3060", ComputeCapability: "8.6"},
		},
	}
	p2 := &telemetry.ReferenceProfile{
		SignerID: "attester-x", IssuedAt: 200,
		GPUs: []telemetry.GPUObservation{
			{Name: "NVIDIA GeForce RTX 3050", ComputeCapability: "8.6"},
			// 3060 dropped, H100 added
			{Name: "NVIDIA H100", ComputeCapability: "9.0"},
		},
	}
	c.Apply(p1)
	c.Apply(p2)
	if len(c.LookupByName("NVIDIA GeForce RTX 3060")) != 0 {
		t.Fatalf("p1.RTX3060 should have been removed by p2")
	}
	if len(c.LookupByName("NVIDIA H100")) != 1 {
		t.Fatalf("p2.H100 should be present")
	}
	total, signers, _ := c.Counters()
	if signers != 1 {
		t.Fatalf("signers = %d, want 1", signers)
	}
	if total != 2 {
		t.Fatalf("total = %d, want 2", total)
	}
}

func TestCatalog_ApplySkipsEmptyName(t *testing.T) {
	c := NewCatalog()
	added, err := c.Apply(&telemetry.ReferenceProfile{
		SignerID: "attester-x", IssuedAt: 1,
		GPUs: []telemetry.GPUObservation{
			{Name: "", ComputeCapability: "8.6"},
			{Name: "NVIDIA RTX 3050", ComputeCapability: "8.6"},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if added != 1 {
		t.Fatalf("added = %d, want 1 (empty-name entry should be skipped)", added)
	}
}

func TestCatalog_LookupCaseInsensitive(t *testing.T) {
	c := NewCatalog()
	c.LoadBaseline()
	for _, alt := range []string{
		"NVIDIA GeForce RTX 3050",
		"nvidia geforce rtx 3050",
		"  NVIDIA GeForce RTX 3050  ",
	} {
		if len(c.LookupByName(alt)) == 0 {
			t.Errorf("lookup %q returned empty (case/whitespace not normalised)", alt)
		}
	}
}

func TestCatalog_KnownNamesSorted(t *testing.T) {
	c := NewCatalog()
	c.LoadBaseline()
	names := c.KnownNames()
	if len(names) < 10 {
		t.Fatalf("KnownNames returned %d entries", len(names))
	}
	check := make([]string, len(names))
	copy(check, names)
	sort.Strings(check)
	if !reflect.DeepEqual(names, check) {
		t.Fatalf("KnownNames not sorted")
	}
}

func TestCatalog_LookupSourcesByNameDedups(t *testing.T) {
	c := NewCatalog()
	c.Apply(&telemetry.ReferenceProfile{
		SignerID: "attester-a", IssuedAt: 1,
		GPUs: []telemetry.GPUObservation{
			{Name: "NVIDIA RTX 3050"},
			{Name: "NVIDIA RTX 3050"}, // same SKU, same signer (different physical card)
		},
	})
	c.Apply(&telemetry.ReferenceProfile{
		SignerID: "attester-b", IssuedAt: 1,
		GPUs:     []telemetry.GPUObservation{{Name: "NVIDIA RTX 3050"}},
	})
	srcs := c.LookupSourcesByName("NVIDIA RTX 3050")
	if !reflect.DeepEqual(srcs, []string{"attester-a", "attester-b"}) {
		t.Fatalf("sources = %v", srcs)
	}
}

func TestCatalog_Concurrent(t *testing.T) {
	c := NewCatalog()
	c.LoadBaseline()

	var wg sync.WaitGroup
	const N = 32
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.LookupByName("NVIDIA GeForce RTX 3050")
		}()
	}
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Apply(&telemetry.ReferenceProfile{
				SignerID: "attester-x", IssuedAt: int64(i),
				GPUs: []telemetry.GPUObservation{
					{Name: "NVIDIA GeForce RTX 3050", ComputeCapability: "8.6"},
				},
			})
		}(i)
	}
	wg.Wait()
	// Sanity: should still be queryable after the racing.
	if len(c.LookupByName("NVIDIA GeForce RTX 3050")) == 0 {
		t.Fatalf("catalog corrupted under concurrency")
	}
}
