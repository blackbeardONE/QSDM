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

// A version 2 schedule lives in the same table as version 1, so a build that
// predates it refuses the table ("version must be 1") instead of ignoring an
// unknown key. A table that mixes both forms is refused at boot.
func TestProducerTransitionV2ConfigLoadAndValidation(t *testing.T) {
	h := func(c string) string { return strings.Repeat(c, 64) }
	toml := "[consensus.producer_transition]\nversion = 2\nhistorical_prefix_bytes = 2728741562\nhistorical_prefix_sha256 = \"" + h("d") + "\"\n" +
		"[[consensus.producer_transition.epochs]]\nfrom_height = 482566\nproducer_id = \"" + h("b") + "\"\n" +
		"[[consensus.producer_transition.epochs]]\nfrom_height = 659977\nproducer_id = \"" + h("c") + "\"\ncheckpoint_hash = \"" + h("a") + "\"\n" +
		"[[consensus.producer_transition.epochs]]\nfrom_height = 900001\nproducer_id = \"" + h("e") + "\"\ncheckpoint_hash = \"" + h("f") + "\"\n"
	yaml := "consensus:\n  producer_transition:\n    version: 2\n    historical_prefix_bytes: 2728741562\n    historical_prefix_sha256: \"" + h("d") + "\"\n    epochs:\n" +
		"      - from_height: 482566\n        producer_id: \"" + h("b") + "\"\n" +
		"      - from_height: 659977\n        producer_id: \"" + h("c") + "\"\n        checkpoint_hash: \"" + h("a") + "\"\n" +
		"      - from_height: 900001\n        producer_id: \"" + h("e") + "\"\n        checkpoint_hash: \"" + h("f") + "\"\n"
	for ext, text := range map[string]string{"toml": toml, "yaml": yaml} {
		t.Run(ext, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node."+ext)
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			c := &Config{}
			if err := loadConfigFile(path, c); err != nil {
				t.Fatal(err)
			}
			applyDefaults(c)
			p := c.ProducerTransition
			if p == nil || p.Version != 2 || len(p.Epochs) != 3 || p.Epochs[2].FromHeight != 900001 || p.Epochs[2].CheckpointHash != h("f") || p.Epochs[0].CheckpointHash != "" {
				t.Fatalf("schedule not loaded: %+v", p)
			}
			if err := c.Validate(); err != nil {
				t.Fatal(err)
			}
			p.ReplacementProducer = h("c")
			if c.Validate() == nil {
				t.Fatal("table mixing version 1 and version 2 fields accepted")
			}
			p.ReplacementProducer = ""
			p.Version = 1
			if c.Validate() == nil {
				t.Fatal("version 1 table with epochs accepted")
			}
		})
	}
}
