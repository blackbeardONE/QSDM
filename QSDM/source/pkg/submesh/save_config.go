package submesh

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SaveProfilesToPath writes the current manager state to path as YAML (`submeshes:` list),
// matching the shape consumed by `LoadProfilesFromFile` / `ApplyProfilesFromFile`.
func SaveProfilesToPath(m *DynamicSubmeshManager, path string) error {
	if m == nil {
		return fmt.Errorf("submesh: nil manager")
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("submesh: empty path")
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".yaml" && ext != ".yml" {
		return fmt.Errorf("submesh: save only supports .yaml/.yml (got %q)", ext)
	}

	list := m.ListSubmeshes()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	raw := make([]submeshProfileFile, 0, len(list))
	for _, ds := range list {
		if ds == nil {
			continue
		}
		p := submeshProfileFile{
			Name:     ds.Name,
			Fees:     ds.FeeThreshold,
			Priority: ds.PriorityLevel,
			GeoTags:  append([]string(nil), ds.GeoTags...),
		}
		if ds.MaxPayloadBytes > 0 {
			p.Params.MaxTxSize = fmt.Sprintf("%d", ds.MaxPayloadBytes)
		}
		raw = append(raw, p)
	}

	out := submeshFileMulti{Submeshes: raw}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return fmt.Errorf("submesh: marshal yaml: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("submesh: write temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("submesh: rename: %w", err)
	}
	return nil
}
