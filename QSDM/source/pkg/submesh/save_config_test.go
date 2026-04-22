package submesh

import (
	"path/filepath"
	"testing"
)

func TestSaveProfilesToPath_roundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profiles.yaml")

	m := NewDynamicSubmeshManager()
	m.AddOrUpdateSubmesh(&DynamicSubmesh{
		Name: "alpha", PriorityLevel: 2, FeeThreshold: 0.5, GeoTags: []string{"US", "EU"},
	})
	m.AddOrUpdateSubmesh(&DynamicSubmesh{
		Name: "beta", PriorityLevel: 1, FeeThreshold: 0.0, GeoTags: []string{"EU"},
	})

	if err := SaveProfilesToPath(m, p); err != nil {
		t.Fatal(err)
	}

	m2 := NewDynamicSubmeshManager()
	loaded, err := ApplyProfilesFromFile(m2, p)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("loaded count %d", len(loaded))
	}
	by := map[string]*DynamicSubmesh{}
	for _, ds := range m2.ListSubmeshes() {
		by[ds.Name] = ds
	}
	a := by["alpha"]
	b := by["beta"]
	if a == nil || b == nil {
		t.Fatal("missing submesh after reload")
	}
	if a.PriorityLevel != 2 || b.PriorityLevel != 1 {
		t.Fatalf("priority mismatch a=%d b=%d", a.PriorityLevel, b.PriorityLevel)
	}
	if a.FeeThreshold != 0.5 || len(a.GeoTags) != 2 {
		t.Fatalf("alpha fields %+v", a)
	}
}

func TestSaveProfilesToPath_rejectsNonYaml(t *testing.T) {
	m := NewDynamicSubmeshManager()
	err := SaveProfilesToPath(m, "/tmp/x.toml")
	if err == nil {
		t.Fatal("expected error for non-yaml extension")
	}
}
