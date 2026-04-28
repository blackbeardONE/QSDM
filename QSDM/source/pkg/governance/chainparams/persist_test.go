package chainparams

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// firstParam returns a registered param name for tests that
// only need a real-but-arbitrary param. We pick the first one
// in the registry so a registry rename / reorder doesn't
// cascade through every test.
func firstParam(t *testing.T) (ParamSpec, ParamName) {
	t.Helper()
	specs := Registry()
	if len(specs) == 0 {
		t.Fatal("registry is empty; chainparams.Registry must have at least one entry")
	}
	return specs[0], specs[0].Name
}

// midValue returns a value comfortably inside the spec's
// (min, max) bounds without coinciding with the default — so
// "loaded == default" can't false-pass an assertion.
func midValue(spec ParamSpec) uint64 {
	v := (spec.MinValue + spec.MaxValue) / 2
	if v == spec.DefaultValue {
		// Nudge by one if midpoint happens to equal default.
		if v+1 <= spec.MaxValue {
			return v + 1
		}
		if v >= spec.MinValue+1 {
			return v - 1
		}
	}
	return v
}

// -----------------------------------------------------------------------------
// SaveSnapshot + LoadOrNew round-trip
// -----------------------------------------------------------------------------

func TestSaveLoad_RoundTripActives(t *testing.T) {
	spec, name := firstParam(t)
	value := midValue(spec)

	src := NewInMemoryParamStore()
	src.SetForTesting(string(name), value)

	dir := t.TempDir()
	path := filepath.Join(dir, "gov-params.json")
	if err := SaveSnapshot(src, path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot file not created: %v", err)
	}

	loaded, err := LoadOrNew(path)
	if err != nil {
		t.Fatalf("LoadOrNew: %v", err)
	}
	got, ok := loaded.ActiveValue(string(name))
	if !ok {
		t.Fatalf("loaded store missing param %q", name)
	}
	if got != value {
		t.Errorf("loaded active %q = %d, want %d", name, got, value)
	}
}

func TestSaveLoad_RoundTripPending(t *testing.T) {
	spec, name := firstParam(t)
	pendingValue := midValue(spec)

	src := NewInMemoryParamStore()
	if _, _, err := src.Stage(ParamChange{
		Param:             string(name),
		Value:             pendingValue,
		EffectiveHeight:   12345,
		SubmittedAtHeight: 12000,
		Authority:         "alice-key",
		Memo:              "post-mortem #14",
	}); err != nil {
		t.Fatalf("Stage: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "gov-params.json")
	if err := SaveSnapshot(src, path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	loaded, err := LoadOrNew(path)
	if err != nil {
		t.Fatalf("LoadOrNew: %v", err)
	}
	pending, ok := loaded.Pending(string(name))
	if !ok {
		t.Fatalf("loaded store missing pending for %q", name)
	}
	if pending.Value != pendingValue {
		t.Errorf("pending.Value=%d, want %d", pending.Value, pendingValue)
	}
	if pending.EffectiveHeight != 12345 {
		t.Errorf("pending.EffectiveHeight=%d, want 12345", pending.EffectiveHeight)
	}
	if pending.SubmittedAtHeight != 12000 {
		t.Errorf("pending.SubmittedAtHeight=%d, want 12000", pending.SubmittedAtHeight)
	}
	if pending.Authority != "alice-key" || pending.Memo != "post-mortem #14" {
		t.Errorf("pending metadata not preserved: %+v", pending)
	}
}

func TestSaveLoad_DefaultsWhenSnapshotIncomplete(t *testing.T) {
	specs := Registry()
	if len(specs) < 2 {
		t.Skip("registry needs ≥2 params for this test")
	}
	src := NewInMemoryParamStore()
	src.SetForTesting(string(specs[0].Name), midValue(specs[0]))
	// Deliberately leave specs[1] at default. The on-disk
	// AllActive will include both because NewInMemoryParamStore
	// seeds defaults — so to actually exercise the
	// "missing-from-disk" branch we hand-write a partial JSON.

	dir := t.TempDir()
	path := filepath.Join(dir, "partial.json")
	doc := snapshotDoc{
		Version: SnapshotVersion,
		SavedAt: "test",
		Active: map[string]uint64{
			string(specs[0].Name): midValue(specs[0]),
			// specs[1] omitted.
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadOrNew(path)
	if err != nil {
		t.Fatalf("LoadOrNew: %v", err)
	}
	got1, _ := loaded.ActiveValue(string(specs[1].Name))
	if got1 != specs[1].DefaultValue {
		t.Errorf("missing-from-snapshot param should fall back to default %d, got %d",
			specs[1].DefaultValue, got1)
	}

	_ = src
}

// -----------------------------------------------------------------------------
// Missing file → fresh store with registry defaults
// -----------------------------------------------------------------------------

func TestLoadOrNew_MissingFileReturnsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	loaded, err := LoadOrNew(path)
	if err != nil {
		t.Fatalf("LoadOrNew on missing file should not error, got %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadOrNew returned nil store")
	}
	for _, spec := range Registry() {
		v, ok := loaded.ActiveValue(string(spec.Name))
		if !ok {
			t.Errorf("missing %q in fresh store", spec.Name)
			continue
		}
		if v != spec.DefaultValue {
			t.Errorf("fresh store %q = %d, want default %d",
				spec.Name, v, spec.DefaultValue)
		}
	}
	if got := len(loaded.AllPending()); got != 0 {
		t.Errorf("fresh store has %d pending entries, want 0", got)
	}
}

func TestLoadOrNew_EmptyPath(t *testing.T) {
	loaded, err := LoadOrNew("")
	if err != nil {
		t.Fatalf("LoadOrNew(\"\") should not error, got %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadOrNew(\"\") returned nil")
	}
}

func TestSaveSnapshot_NilStoreNoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "should-not-be-created.json")
	if err := SaveSnapshot(nil, path); err != nil {
		t.Errorf("SaveSnapshot(nil) should be no-op, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("SaveSnapshot(nil) should not create %s", path)
	}
}

func TestSaveSnapshot_EmptyPathNoOp(t *testing.T) {
	src := NewInMemoryParamStore()
	if err := SaveSnapshot(src, ""); err != nil {
		t.Errorf("SaveSnapshot(_, \"\") should be no-op, got %v", err)
	}
}

// -----------------------------------------------------------------------------
// Forward / backward compatibility
// -----------------------------------------------------------------------------

func TestLoadOrNew_UnknownParamSilentlyDropped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unknown-param.json")

	doc := snapshotDoc{
		Version: SnapshotVersion,
		SavedAt: "test",
		Active: map[string]uint64{
			"this_param_does_not_exist_in_registry": 42,
		},
		Pending: []snapshotPending{
			{Param: "this_param_does_not_exist_in_registry", Value: 99, EffectiveHeight: 100},
		},
	}
	b, _ := json.Marshal(doc)
	_ = os.WriteFile(path, b, 0o600)

	loaded, err := LoadOrNew(path)
	if err != nil {
		t.Fatalf("LoadOrNew should tolerate unknown params, got %v", err)
	}
	if got := len(loaded.AllPending()); got != 0 {
		t.Errorf("unknown pending should drop, got %d entries", got)
	}
	// Registered params should still hold their defaults.
	for _, spec := range Registry() {
		v, ok := loaded.ActiveValue(string(spec.Name))
		if !ok || v != spec.DefaultValue {
			t.Errorf("registered param %q corrupted: v=%d ok=%t",
				spec.Name, v, ok)
		}
	}
}

func TestLoadOrNew_OutOfBoundsActiveClampedToDefault(t *testing.T) {
	spec, name := firstParam(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "oob.json")

	bad := spec.MaxValue + 1
	doc := snapshotDoc{
		Version: SnapshotVersion,
		Active:  map[string]uint64{string(name): bad},
	}
	b, _ := json.Marshal(doc)
	_ = os.WriteFile(path, b, 0o600)

	loaded, err := LoadOrNew(path)
	if err != nil {
		t.Fatalf("LoadOrNew: %v", err)
	}
	v, _ := loaded.ActiveValue(string(name))
	if v != spec.DefaultValue {
		t.Errorf("oob active should clamp to default %d, got %d", spec.DefaultValue, v)
	}
}

func TestLoadOrNew_RejectsUnknownVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "future.json")

	doc := snapshotDoc{
		Version: SnapshotVersion + 99,
	}
	b, _ := json.Marshal(doc)
	_ = os.WriteFile(path, b, 0o600)

	if _, err := LoadOrNew(path); err == nil {
		t.Error("LoadOrNew should reject unknown version, got nil error")
	}
}

func TestLoadOrNew_RejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.json")
	_ = os.WriteFile(path, []byte("this is not JSON"), 0o600)

	if _, err := LoadOrNew(path); err == nil {
		t.Error("LoadOrNew should reject malformed JSON, got nil error")
	}
}

// -----------------------------------------------------------------------------
// Atomic write: tmp file gets cleaned up on success
// -----------------------------------------------------------------------------

func TestSaveSnapshot_AtomicCleanup(t *testing.T) {
	src := NewInMemoryParamStore()
	dir := t.TempDir()
	path := filepath.Join(dir, "atomic.json")

	if err := SaveSnapshot(src, path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("final snapshot %q missing: %v", path, err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("tmp file %q should be cleaned up after rename", path+".tmp")
	}
}

func TestSaveSnapshot_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "overwrite.json")
	_ = os.WriteFile(path, []byte("stale"), 0o600)

	src := NewInMemoryParamStore()
	spec, name := firstParam(t)
	src.SetForTesting(string(name), midValue(spec))
	if err := SaveSnapshot(src, path); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	loaded, err := LoadOrNew(path)
	if err != nil {
		t.Fatalf("LoadOrNew: %v", err)
	}
	if v, _ := loaded.ActiveValue(string(name)); v != midValue(spec) {
		t.Error("overwritten snapshot did not replace prior bytes")
	}
}

// -----------------------------------------------------------------------------
// Promote-then-save lifecycle
// -----------------------------------------------------------------------------

func TestSaveLoad_PromotePersistedAcrossRestart(t *testing.T) {
	spec, name := firstParam(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "promote.json")

	src := NewInMemoryParamStore()
	want := midValue(spec)
	if _, _, err := src.Stage(ParamChange{
		Param:             string(name),
		Value:             want,
		EffectiveHeight:   100,
		SubmittedAtHeight: 50,
		Authority:         "alice",
	}); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	// Save WHILE pending; replay; promote on the replay; save
	// again; replay; verify active reflects the promotion.
	if err := SaveSnapshot(src, path); err != nil {
		t.Fatalf("SaveSnapshot pending: %v", err)
	}

	stage1, err := LoadOrNew(path)
	if err != nil {
		t.Fatalf("LoadOrNew stage1: %v", err)
	}
	if _, ok := stage1.Pending(string(name)); !ok {
		t.Fatal("pending lost across save/load cycle")
	}
	promoted := stage1.Promote(100)
	if len(promoted) != 1 {
		t.Fatalf("Promote returned %d entries, want 1", len(promoted))
	}
	if err := SaveSnapshot(stage1, path); err != nil {
		t.Fatalf("SaveSnapshot post-promote: %v", err)
	}

	stage2, err := LoadOrNew(path)
	if err != nil {
		t.Fatalf("LoadOrNew stage2: %v", err)
	}
	got, _ := stage2.ActiveValue(string(name))
	if got != want {
		t.Errorf("post-promote active %q = %d, want %d", name, got, want)
	}
	if _, ok := stage2.Pending(string(name)); ok {
		t.Errorf("pending should be vacated after promotion")
	}
}
