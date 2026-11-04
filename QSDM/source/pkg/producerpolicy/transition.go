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

// MaxEpochs bounds a version 2 schedule. Each hand-over is an operator act
// with fencing, so a real schedule stays tiny; the bound keeps a malformed
// configuration from becoming a per-block cost.
const MaxEpochs = 64

// Epoch is one entry of a version 2 producer schedule: from FromHeight until
// the next epoch's FromHeight (or forever, for the last epoch) every block must
// be signed by ProducerID. Every epoch after the first starts at a hand-over
// pinned by CheckpointHash, the hash of block FromHeight-1, so a hand-over can
// only be configured after the previous producer was fenced and its last block
// is known. The first epoch has no checkpoint.
type Epoch struct {
	FromHeight     uint64 `toml:"from_height" yaml:"from_height" json:"from_height"`
	ProducerID     string `toml:"producer_id" yaml:"producer_id" json:"producer_id"`
	CheckpointHash string `toml:"checkpoint_hash" yaml:"checkpoint_hash" json:"checkpoint_hash,omitempty"`
}

// Transition pins the complete original journal prefix and the approved
// producers. Every participating validator must install the same configuration.
// Nil means the pre-existing producer policy; an empty object is an error.
//
// Version 1 describes exactly one hand-over (historical -> replacement) with
// the scalar fields below. Version 2 describes an ordered list of producer
// epochs in Epochs and uses only the historical_prefix_* scalar fields. Both
// live in the same [consensus.producer_transition] table on purpose: a build
// that only knows version 1 refuses a version 2 table at boot ("version must
// be 1") instead of silently ignoring an unknown key. Mixing the two forms is
// refused. Internally a version 1 policy is the equivalent two-epoch schedule
// (see Schedule).
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
	// Epochs is the version 2 schedule. It must be empty in version 1.
	Epochs []Epoch `toml:"epochs" yaml:"epochs" json:"epochs,omitempty"`
}

func validHash(name, value string) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
		return fmt.Errorf("producer transition: %s must be exactly 64 lowercase hexadecimal characters", name)
	}
	if value == strings.Repeat("0", 64) {
		return fmt.Errorf("producer transition: %s must not be zero", name)
	}
	return nil
}

func (p *Transition) Validate() error {
	if p == nil {
		return nil
	}
	switch p.Version {
	case 1:
		return p.validateV1()
	case 2:
		return p.validateV2()
	}
	return fmt.Errorf("producer transition: version must be 1 or 2")
}

func (p *Transition) validateV1() error {
	if len(p.Epochs) != 0 {
		return fmt.Errorf("producer transition: version 1 must not contain epochs (use version 2 for an epoch schedule)")
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
		if err := validHash(f.name, f.value); err != nil {
			return err
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

// validateV2 checks an epoch schedule. Epochs are defined by their start
// height only, so strictly increasing heights mean no gaps and no overlaps.
func (p *Transition) validateV2() error {
	if p.CheckpointHeight != 0 || p.CheckpointHash != "" || p.HistoricalSignatureHeight != 0 ||
		p.HistoricalProducer != "" || p.ReplacementProducer != "" || p.EffectiveHeight != 0 {
		return fmt.Errorf("producer transition: version 2 must not set version 1 fields (checkpoint_height, checkpoint_hash, historical_signature_height, historical_producer, replacement_producer, effective_height); describe every producer in epochs")
	}
	if len(p.Epochs) < 2 || len(p.Epochs) > MaxEpochs {
		return fmt.Errorf("producer transition: version 2 requires 2..%d epochs (one hand-over or more)", MaxEpochs)
	}
	seen := make(map[string]int, len(p.Epochs))
	for i, e := range p.Epochs {
		if err := validHash(fmt.Sprintf("epochs[%d].producer_id", i), e.ProducerID); err != nil {
			return err
		}
		if j, dup := seen[e.ProducerID]; dup {
			return fmt.Errorf("producer transition: epochs[%d].producer_id repeats epochs[%d]; a producer key is never reused after a hand-over", i, j)
		}
		seen[e.ProducerID] = i
		if i == 0 {
			if e.FromHeight == 0 {
				return fmt.Errorf("producer transition: epochs[0].from_height must be at least 1 (genesis is never signed)")
			}
			if e.CheckpointHash != "" {
				return fmt.Errorf("producer transition: epochs[0] must not have a checkpoint_hash")
			}
			continue
		}
		if e.FromHeight <= p.Epochs[i-1].FromHeight {
			return fmt.Errorf("producer transition: epochs[%d].from_height must be greater than epochs[%d].from_height", i, i-1)
		}
		if err := validHash(fmt.Sprintf("epochs[%d].checkpoint_hash", i), e.CheckpointHash); err != nil {
			return err
		}
	}
	if err := validHash("historical_prefix_sha256", p.HistoricalPrefixSHA256); err != nil {
		return err
	}
	if p.HistoricalPrefixBytes <= 0 {
		return fmt.Errorf("producer transition: historical_prefix_bytes must be positive")
	}
	return nil
}

// Schedule returns the ordered producer epochs as a fresh slice. A version 1
// policy is exactly the two-epoch schedule
//
//	[{historical_signature_height, historical_producer, ""},
//	 {effective_height, replacement_producer, checkpoint_hash}]
//
// (version 1 validation forces checkpoint_height = effective_height-1). Any
// version other than 2 is read through the version 1 fields; callers validate
// first.
func (p *Transition) Schedule() []Epoch {
	if p == nil {
		return nil
	}
	if p.Version == 2 {
		return append([]Epoch(nil), p.Epochs...)
	}
	return []Epoch{
		{FromHeight: p.HistoricalSignatureHeight, ProducerID: p.HistoricalProducer},
		{FromHeight: p.EffectiveHeight, ProducerID: p.ReplacementProducer, CheckpointHash: p.CheckpointHash},
	}
}

// FirstHandoverHeight is the first height of the second epoch (version 1:
// effective_height). Blocks below it keep the immutable legacy state root.
func (p *Transition) FirstHandoverHeight() uint64 {
	if s := p.Schedule(); len(s) >= 2 {
		return s[1].FromHeight
	}
	return 0
}

// FinalEpoch is the last (currently producing) epoch. Only its producer may
// seal new blocks (version 1: replacement_producer from effective_height).
func (p *Transition) FinalEpoch() Epoch {
	if s := p.Schedule(); len(s) > 0 {
		return s[len(s)-1]
	}
	return Epoch{}
}

// ProducerAt returns the producer of the epoch containing height h, and false
// below the first epoch (where blocks may be unsigned).
func ProducerAt(epochs []Epoch, h uint64) (string, bool) {
	for i := len(epochs) - 1; i >= 0; i-- {
		if h >= epochs[i].FromHeight {
			return epochs[i].ProducerID, true
		}
	}
	return "", false
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
