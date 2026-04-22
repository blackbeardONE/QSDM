package submesh

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadProfileFromFileTOML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profile.toml")
	content := `
name = "micropayments"
fees = 0.001
priority = 5
geo_tags = ["US", "EU"]

[parameters]
max_tx_size = "2048"
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ds, err := LoadProfileFromFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Name != "micropayments" || ds.FeeThreshold != 0.001 || ds.PriorityLevel != 5 {
		t.Fatalf("unexpected core fields: %+v", ds)
	}
	if len(ds.GeoTags) != 2 || ds.MaxPayloadBytes != 2048 {
		t.Fatalf("unexpected tags/limits: %+v", ds)
	}

	m := NewDynamicSubmeshManager()
	if _, err := ApplyProfileFromFile(m, p); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetSubmesh("micropayments")
	if err != nil || got.MaxPayloadBytes != 2048 {
		t.Fatalf("manager: %+v err=%v", got, err)
	}
}

func TestLoadProfileFromFileYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "profile.yml")
	content := `
name: micropayments
fees: 0.002
priority: 2
geo_tags:
  - US
parameters:
  max_tx_size: "512"
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ds, err := LoadProfileFromFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if ds.FeeThreshold != 0.002 || ds.MaxPayloadBytes != 512 {
		t.Fatalf("yaml decode: %+v", ds)
	}
}

func TestLoadProfileFromFileErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.toml")
	if err := os.WriteFile(bad, []byte("name = \"\"\nfees = 0\ngeo_tags = [\"US\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfileFromFile(bad); err == nil {
		t.Fatal("expected error for empty name")
	}
	emptyTags := filepath.Join(dir, "notags.toml")
	if err := os.WriteFile(emptyTags, []byte("name = \"a\"\nfees = 0\ngeo_tags = []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfileFromFile(emptyTags); err == nil {
		t.Fatal("expected error for empty geo_tags")
	}
}

func TestLoadProfilesFromFileMultiTOML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "multi.toml")
	content := `
[[submeshes]]
name = "fast"
fees = 0.01
priority = 10
geo_tags = ["US"]

[[submeshes]]
name = "slow"
fees = 0.001
priority = 1
geo_tags = ["US", "EU"]
`
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProfileFromFile(p); err == nil {
		t.Fatal("LoadProfileFromFile should reject multi-profile file")
	}
	all, err := LoadProfilesFromFile(p)
	if err != nil || len(all) != 2 {
		t.Fatalf("got %v err=%v", all, err)
	}
	m := NewDynamicSubmeshManager()
	got, err := ApplyProfilesFromFile(m, p)
	if err != nil || len(got) != 2 {
		t.Fatalf("apply: %v err=%v", got, err)
	}
	ds, err := m.RouteTransaction(0.02, "US")
	if err != nil || ds.Name != "fast" {
		t.Fatalf("route: %+v err=%v", ds, err)
	}
}
