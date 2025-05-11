package config

import (
    "fmt"
    "os"

    "gopkg.in/yaml.v3"
)

type SubmeshConfig struct {
    Name       string            `yaml:"name"`
    Fees       float64           `yaml:"fees"`
    GeoTags    []string          `yaml:"geo_tags"`
    Parameters map[string]string `yaml:"parameters"`
}

func LoadSubmeshConfig(path string) (*SubmeshConfig, error) {
    file, err := os.ReadFile(path)
    if err != nil {
        return nil, fmt.Errorf("failed to read config file: %w", err)
    }
    var config SubmeshConfig
    err = yaml.Unmarshal(file, &config)
    if err != nil {
        return nil, fmt.Errorf("failed to unmarshal yaml: %w", err)
    }
    return &config, nil
}
