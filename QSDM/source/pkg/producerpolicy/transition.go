// Package producerpolicy describes an explicitly operator-approved producer
// replacement. It contains no keys and does not elect or discover producers.
package producerpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
)

// Transition pins the complete original journal prefix and a single replacement
// producer. Every participating validator must install the same configuration.
// Nil means the pre-existing producer policy; an empty object is an error.
type Transition struct {
	Version                   uint64 `toml:"version" yaml:"version" json:"version"`
	CheckpointHeight          uint64 `toml:"checkpoint_height" yaml:"checkpoint_height" json:"checkpoint_height"`
	CheckpointHash            string `toml:"checkpoint_hash" yaml:"checkpoint_hash" json:"checkpoint_hash"`
	HistoricalSignatureHeight uint64 `toml:"historical_signature_height" yaml:"historical_signature_height" json:"historical_signature_height"`
	HistoricalProducer        string `toml:"historical_producer" yaml:"historical_producer" json:"historical_producer"`
	ReplacementProducer       string `toml:"replacement_producer" yaml:"replacement_producer" json:"replacement_producer"`
	EffectiveHeight           uint64 `toml:"effective_height" yaml:"effective_height" json:"effective_height"`
	HistoricalPrefixBytes     int64  `toml:"historical_prefix_bytes" yaml:"historical_prefix_bytes" json:"historical_prefix_bytes"`
	HistoricalPrefixSHA256    string `toml:"historical_prefix_sha256" yaml:"historical_prefix_sha256" json:"historical_prefix_sha256"`
}

func (p *Transition) Validate() error {
	if p == nil {
		return nil
	}
	if p.Version != 1 {
		return fmt.Errorf("producer transition: version must be 1")
	}
	if p.CheckpointHeight == 0 || p.CheckpointHeight == math.MaxUint64 || p.EffectiveHeight != p.CheckpointHeight+1 {
		return fmt.Errorf("producer transition: effective_height must immediately follow a nonzero checkpoint_height without overflow")
	}
	if p.HistoricalSignatureHeight == 0 || p.HistoricalSignatureHeight > p.CheckpointHeight {
		return fmt.Errorf("producer transition: historical_signature_height must be in 1..checkpoint_height")
	}
	for _, f := range []struct{ name, value string }{
		{"checkpoint_hash", p.CheckpointHash}, {"historical_producer", p.HistoricalProducer},
		{"replacement_producer", p.ReplacementProducer}, {"historical_prefix_sha256", p.HistoricalPrefixSHA256},
	} {
		decoded, err := hex.DecodeString(f.value)
		if err != nil || len(decoded) != sha256.Size || f.value != strings.ToLower(f.value) {
			return fmt.Errorf("producer transition: %s must be exactly 64 lowercase hexadecimal characters", f.name)
		}
		if f.value == strings.Repeat("0", 64) {
			return fmt.Errorf("producer transition: %s must not be zero", f.name)
		}
	}
	if p.HistoricalProducer == p.ReplacementProducer {
		return fmt.Errorf("producer transition: replacement producer must differ from historical producer")
	}
	if p.HistoricalPrefixBytes <= 0 {
		return fmt.Errorf("producer transition: historical_prefix_bytes must be positive")
	}
	return nil
}

// ValidateJournalPrefix checks exactly the original immutable bytes, allowing
// only a new suffix after them. Missing/truncated/normalized history fails closed.
// Prefix bytes must finish on a newline so no new block can share its last line.
func (p *Transition) ValidateJournalPrefix(path string) error {
	if p == nil {
		return nil
	}
	if err := p.Validate(); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("producer transition: open immutable journal: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() < p.HistoricalPrefixBytes {
		return fmt.Errorf("producer transition: immutable journal prefix missing or truncated")
	}
	h := sha256.New()
	if _, err := io.CopyN(h, f, p.HistoricalPrefixBytes); err != nil {
		return fmt.Errorf("producer transition: read immutable prefix: %w", err)
	}
	if hex.EncodeToString(h.Sum(nil)) != p.HistoricalPrefixSHA256 {
		return fmt.Errorf("producer transition: immutable journal prefix SHA-256 mismatch")
	}
	var last [1]byte
	if _, err := f.ReadAt(last[:], p.HistoricalPrefixBytes-1); err != nil {
		return err
	}
	if last[0] != '\n' {
		return fmt.Errorf("producer transition: immutable prefix must end with newline")
	}
	return nil
}
