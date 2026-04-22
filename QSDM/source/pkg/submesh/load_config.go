package submesh

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// submeshProfileFile matches config/micropayments.toml and micropayments.yml.
type submeshProfileFile struct {
	Name     string   `toml:"name" yaml:"name"`
	Fees     float64  `toml:"fees" yaml:"fees"`
	Priority int      `toml:"priority" yaml:"priority"`
	GeoTags  []string `toml:"geo_tags" yaml:"geo_tags"`
	Params struct {
		MaxTxSize string `toml:"max_tx_size" yaml:"max_tx_size"`
	} `toml:"parameters" yaml:"parameters"`
}

type submeshFileMulti struct {
	Submeshes []submeshProfileFile `toml:"submeshes" yaml:"submeshes"`
}

func profileFileToDynamic(raw submeshProfileFile) (*DynamicSubmesh, error) {
	if strings.TrimSpace(raw.Name) == "" {
		return nil, fmt.Errorf("submesh profile: name is required")
	}
	if raw.Fees < 0 {
		return nil, fmt.Errorf("submesh profile: fees cannot be negative")
	}
	if len(raw.GeoTags) == 0 {
		return nil, fmt.Errorf("submesh profile: at least one geo_tags entry is required")
	}

	prio := raw.Priority
	if prio <= 0 {
		prio = 1
	}

	ds := &DynamicSubmesh{
		Name:             raw.Name,
		FeeThreshold:     raw.Fees,
		PriorityLevel:    prio,
		GeoTags:          append([]string(nil), raw.GeoTags...),
		MaxPayloadBytes:  0,
	}

	if s := strings.TrimSpace(raw.Params.MaxTxSize); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("submesh profile: invalid parameters.max_tx_size %q", raw.Params.MaxTxSize)
		}
		ds.MaxPayloadBytes = n
	}

	return ds, nil
}

func profileListToDynamic(in []submeshProfileFile) ([]*DynamicSubmesh, error) {
	out := make([]*DynamicSubmesh, 0, len(in))
	for _, raw := range in {
		ds, err := profileFileToDynamic(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, ds)
	}
	return out, nil
}

func parseProfilesFromData(path string, data []byte) ([]*DynamicSubmesh, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".toml":
		var multi submeshFileMulti
		if _, err := toml.Decode(string(data), &multi); err != nil {
			return nil, fmt.Errorf("decode submesh toml: %w", err)
		}
		if len(multi.Submeshes) > 0 {
			return profileListToDynamic(multi.Submeshes)
		}
		var single submeshProfileFile
		if _, err := toml.Decode(string(data), &single); err != nil {
			return nil, fmt.Errorf("decode submesh toml: %w", err)
		}
		ds, err := profileFileToDynamic(single)
		if err != nil {
			return nil, err
		}
		return []*DynamicSubmesh{ds}, nil
	case ".yaml", ".yml":
		var multi submeshFileMulti
		if err := yaml.Unmarshal(data, &multi); err != nil {
			return nil, fmt.Errorf("decode submesh yaml: %w", err)
		}
		if len(multi.Submeshes) > 0 {
			return profileListToDynamic(multi.Submeshes)
		}
		var single submeshProfileFile
		if err := yaml.Unmarshal(data, &single); err != nil {
			return nil, fmt.Errorf("decode submesh yaml: %w", err)
		}
		ds, err := profileFileToDynamic(single)
		if err != nil {
			return nil, err
		}
		return []*DynamicSubmesh{ds}, nil
	default:
		return nil, fmt.Errorf("submesh profile: unsupported extension %q (use .toml, .yaml, .yml)", ext)
	}
}

// LoadProfilesFromFile decodes one or more submesh profiles. Use [[submeshes]] in TOML or submeshes: in YAML for multiple.
func LoadProfilesFromFile(path string) ([]*DynamicSubmesh, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read submesh profile: %w", err)
	}
	return parseProfilesFromData(path, data)
}

// LoadProfileFromFile decodes exactly one profile (error if the file defines multiple submeshes).
func LoadProfileFromFile(path string) (*DynamicSubmesh, error) {
	all, err := LoadProfilesFromFile(path)
	if err != nil {
		return nil, err
	}
	if len(all) != 1 {
		return nil, fmt.Errorf("submesh profile %q: expected exactly one submesh in file (use [[submeshes]] / submeshes: for multiple)", path)
	}
	return all[0], nil
}

// ApplyProfilesFromFile loads all profiles from path into the manager.
func ApplyProfilesFromFile(m *DynamicSubmeshManager, path string) ([]*DynamicSubmesh, error) {
	all, err := LoadProfilesFromFile(path)
	if err != nil {
		return nil, err
	}
	for _, ds := range all {
		m.AddOrUpdateSubmesh(ds)
	}
	return all, nil
}

// ApplyProfileFromFile loads profiles from path; the file must define exactly one submesh.
func ApplyProfileFromFile(m *DynamicSubmeshManager, path string) (*DynamicSubmesh, error) {
	all, err := ApplyProfilesFromFile(m, path)
	if err != nil {
		return nil, err
	}
	if len(all) != 1 {
		return nil, fmt.Errorf("submesh profile %q: expected exactly one submesh, got %d", path, len(all))
	}
	return all[0], nil
}
