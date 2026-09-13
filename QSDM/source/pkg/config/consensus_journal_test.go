package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConsensusSigningJournalConfig(t *testing.T) {
	cfg := &Config{}
	applyDefaults(cfg)
	if cfg.ConsensusSigningJournal {
		t.Fatal("journal must default off")
	}
	for _, format := range []struct{ extension, body string }{
		{"toml", "[consensus]\nsigning_journal = true\n"},
		{"yaml", "consensus:\n  signing_journal: true\n"},
	} {
		t.Run(format.extension, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "node."+format.extension)
			if err := os.WriteFile(path, []byte(format.body), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := &Config{}
			if err := loadConfigFile(path, cfg); err != nil {
				t.Fatal(err)
			}
			if !cfg.ConsensusSigningJournal {
				t.Fatal("journal flag was not loaded")
			}
		})
	}
	t.Setenv("QSDM_CONSENSUS_SIGNING_JOURNAL", "1")
	applyEnvOverrides(cfg)
	if !cfg.ConsensusSigningJournal {
		t.Fatal("journal env flag was not loaded")
	}
	t.Setenv("QSDM_CONSENSUS_SIGNING_JOURNAL", "0")
	applyEnvOverrides(cfg)
	if cfg.ConsensusSigningJournal {
		t.Fatal("explicit disable was not loaded")
	}
}
