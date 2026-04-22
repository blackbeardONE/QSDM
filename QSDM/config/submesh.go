package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

type SubmeshConfig struct {
	Name       string            `yaml:"name" toml:"name"`
	Fees       float64           `yaml:"fees" toml:"fees"`
	GeoTags    []string          `yaml:"geo_tags" toml:"geo_tags"`
	Parameters map[string]string `yaml:"parameters" toml:"parameters"`
}

func LoadSubmeshConfig(path string) (*SubmeshConfig, error) {
	file, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	var config SubmeshConfig
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		err = yaml.Unmarshal(file, &config)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal yaml: %w", err)
		}
	case ".toml":
		if _, err := toml.Decode(string(file), &config); err != nil {
			return nil, fmt.Errorf("failed to decode toml: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported config file extension: %s", ext)
	}
	return &config, nil
}
