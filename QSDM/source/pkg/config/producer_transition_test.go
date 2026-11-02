package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProducerTransitionConfigLoadAndValidation(t *testing.T) {
	for _, ext := range []string{"toml", "yaml"} {
		t.Run(ext, func(t *testing.T) {
			fields := []string{"version = 1", "checkpoint_height = 659976", "checkpoint_hash = \"" + strings.Repeat("a", 64) + "\"", "historical_signature_height = 482566", "historical_producer = \"" + strings.Repeat("b", 64) + "\"", "replacement_producer = \"" + strings.Repeat("c", 64) + "\"", "effective_height = 659977", "historical_prefix_bytes = 2728741562", "historical_prefix_sha256 = \"" + strings.Repeat("d", 64) + "\""}
			text := "[consensus.producer_transition]\n" + strings.Join(fields, "\n")
			if ext == "yaml" {
				text = "consensus:\n  producer_transition:\n"
				for _, line := range fields {
					text += "    " + strings.Replace(line, " = ", ": ", 1) + "\n"
				}
			}
			path := filepath.Join(t.TempDir(), "node."+ext)
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			c := &Config{}
			if err := loadConfigFile(path, c); err != nil {
				t.Fatal(err)
			}
			applyDefaults(c)
			if c.ProducerTransition == nil || c.ProducerTransition.CheckpointHeight != 659976 || c.ProducerTransition.HistoricalPrefixBytes != 2728741562 {
				t.Fatal("transition not loaded")
			}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			c.ProducerTransition.ReplacementProducer = "REPLACEMENT_REQUIRED"
			if c.Validate() == nil {
				t.Fatal("placeholder accepted")
			}
		})
	}
}
