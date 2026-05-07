package telemetry

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewRegistry_RequiresSignerID(t *testing.T) {
	if _, err := NewRegistry("", "", ""); err == nil {
		t.Fatalf("NewRegistry accepted empty signerID")
	}
}

func TestRegistry_ApplyAndSnapshot(t *testing.T) {
	r, err := NewRegistry("attester-x", "test-host", "fixed")
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	changed, err := r.Apply(GPUObservation{
		UUID:               "GPU-x",
		Name:               "NVIDIA RTX 3050",
		MemoryTotalMB:      8188,
		DriverVersionsSeen: []string{"576.28"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !changed {
		t.Fatalf("first Apply did not report changed")
	}
	p := r.Snapshot(1_700_000_000)
	if p.SchemaVersion != SchemaVersion {
		t.Fatalf("schema = %d", p.SchemaVersion)
	}
	if p.SignerID != "attester-x" {
		t.Fatalf("signer = %q", p.SignerID)
	}
	if p.HostNote != "test-host" {
		t.Fatalf("host_note = %q", p.HostNote)
	}
	if p.CollectorKind != "fixed" {
		t.Fatalf("kind = %q", p.CollectorKind)
	}
	if p.IssuedAt != 1_700_000_000 {
		t.Fatalf("issued_at = %d", p.IssuedAt)
	}
	if len(p.GPUs) != 1 || p.GPUs[0].UUID != "GPU-x" {
		t.Fatalf("gpus = %+v", p.GPUs)
	}
	if p.GPUs[0].Observations != 1 {
		t.Fatalf("observations = %d", p.GPUs[0].Observations)
	}
}

func TestRegistry_ApplyEmptyUUIDIsIgnored(t *testing.T) {
	r, _ := NewRegistry("attester-x", "", "")
	changed, err := r.Apply(GPUObservation{UUID: ""})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if changed {
		t.Fatalf("empty UUID should be silently ignored")
	}
	if _, count := r.Counters(); count != 0 {
		t.Fatalf("count = %d, want 0", count)
	}
}

func TestRegistry_ApplyAll(t *testing.T) {
	r, _ := NewRegistry("attester-x", "", "")
	any, err := r.ApplyAll([]GPUObservation{
		{UUID: "GPU-a", MemoryTotalMB: 4096},
		{UUID: "GPU-b", MemoryTotalMB: 8188},
		{UUID: ""}, // ignored
	})
	if err != nil {
		t.Fatalf("ApplyAll: %v", err)
	}
	if !any {
		t.Fatalf("expected change")
	}
	_, count := r.Counters()
	if count != 2 {
		t.Fatalf("expected 2 GPUs, got %d", count)
	}
}

func TestRegistry_SnapshotIsDefensiveCopy(t *testing.T) {
	r, _ := NewRegistry("attester-x", "", "")
	r.Apply(GPUObservation{UUID: "GPU-a", DriverVersionsSeen: []string{"576.28"}})
	p := r.Snapshot(1)
	// Mutate the snapshot — the registry's copy MUST not move.
	p.GPUs[0].DriverVersionsSeen[0] = "tampered"
	p.GPUs[0].MemoryTotalMB = 99999
	p2 := r.Snapshot(2)
	if p2.GPUs[0].DriverVersionsSeen[0] != "576.28" {
		t.Fatalf("snapshot mutation reached the registry: %v", p2.GPUs[0].DriverVersionsSeen)
	}
	if p2.GPUs[0].MemoryTotalMB == 99999 {
		t.Fatalf("snapshot mutation reached registry MemoryTotalMB")
	}
}

func TestRegistry_SignedSnapshot(t *testing.T) {
	r, _ := NewRegistry("attester-x", "", "fixed")
	r.Apply(GPUObservation{UUID: "GPU-a", MemoryTotalMB: 8188})
	key := mustKey(t)
	p, err := r.SignedSnapshot(123, key)
	if err != nil {
		t.Fatalf("SignedSnapshot: %v", err)
	}
	if p.Signature == "" {
		t.Fatalf("Signature empty")
	}
	if !p.Verify(key) {
		t.Fatalf("Verify rejected fresh signature")
	}
	if p.IssuedAt != 123 {
		t.Fatalf("IssuedAt = %d", p.IssuedAt)
	}
}

func TestRegistry_PersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "telemetry.json")

	src, _ := NewRegistry("attester-original", "manila", "fixed")
	src.ApplyAll([]GPUObservation{
		{UUID: "GPU-a", Name: "NVIDIA GeForce RTX 3050", MemoryTotalMB: 8188,
			DriverVersionsSeen: []string{"576.28"}},
		{UUID: "GPU-b", Name: "NVIDIA H100", MemoryTotalMB: 81920,
			DriverVersionsSeen: []string{"550.54"}},
	})
	if err := src.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}

	// New registry with DIFFERENT signer/host — Load
	// should ignore those (live identity wins) but
	// preserve the per-GPU history.
	dst, _ := NewRegistry("attester-new", "different-host", "different-collector")
	loaded, err := dst.LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if !loaded {
		t.Fatalf("expected loaded=true, got false")
	}
	if dst.SignerID() != "attester-new" {
		t.Fatalf("Load overwrote SignerID: %q", dst.SignerID())
	}
	_, count := dst.Counters()
	if count != 2 {
		t.Fatalf("got %d GPUs, want 2", count)
	}
	if dst.LoadedFrom() != path {
		t.Fatalf("LoadedFrom = %q", dst.LoadedFrom())
	}
}

func TestRegistry_LoadMissingPathIsNoError(t *testing.T) {
	r, _ := NewRegistry("attester-x", "", "")
	loaded, err := r.LoadFromFile(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("LoadFromFile (missing) errored: %v", err)
	}
	if loaded {
		t.Fatalf("expected loaded=false")
	}
}

func TestRegistry_LoadMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	r, _ := NewRegistry("attester-x", "", "")
	if _, err := r.LoadFromFile(path); err == nil {
		t.Fatalf("expected error on malformed JSON")
	}
}

func TestRegistry_LoadFutureSchemaVersionRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "future.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":99,"signer_id":"x","gpus":[]}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	r, _ := NewRegistry("attester-x", "", "")
	if _, err := r.LoadFromFile(path); err == nil {
		t.Fatalf("expected error on future schema_version")
	}
}

func TestRegistry_Concurrent(t *testing.T) {
	r, _ := NewRegistry("attester-x", "", "fixed")
	var wg sync.WaitGroup
	const N = 64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Apply(GPUObservation{
				UUID:               "GPU-a",
				DriverVersionsSeen: []string{string(rune('A' + (i % 26)))},
			})
		}(i)
	}
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Snapshot(0)
		}()
	}
	wg.Wait()
	applies, count := r.Counters()
	if count != 1 {
		t.Fatalf("want 1 GPU, got %d", count)
	}
	if applies < N {
		t.Fatalf("want ≥%d applies, got %d", N, applies)
	}
}

// FixedCollector roundtrip — exercises the contract that
// makes test wiring easy.
func TestFixedCollector_RoundTrip(t *testing.T) {
	want := []GPUObservation{{UUID: "GPU-a", Name: "RTX 3050"}}
	c := &FixedCollector{Observations: want}
	got, err := c.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if len(got) != 1 || got[0].UUID != "GPU-a" {
		t.Fatalf("got %+v", got)
	}
	if c.Kind() != "fixed" {
		t.Fatalf("kind = %q", c.Kind())
	}
}

func TestFixedCollector_PropagatesError(t *testing.T) {
	want := errors.New("boom")
	c := &FixedCollector{Err: want}
	if _, err := c.Collect(context.Background()); !errors.Is(err, want) {
		t.Fatalf("expected error to propagate, got %v", err)
	}
}
