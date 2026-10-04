package producerpolicy

import (
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validTransition() *Transition {
	return &Transition{Version: 1, CheckpointHeight: 2, CheckpointHash: strings.Repeat("a", 64), HistoricalSignatureHeight: 1,
		HistoricalProducer: strings.Repeat("b", 64), ReplacementProducer: strings.Repeat("c", 64), EffectiveHeight: 3,
		HistoricalPrefixBytes: 2, HistoricalPrefixSHA256: strings.Repeat("d", 64)}
}

func TestTransitionValidate(t *testing.T) {
	if err := (*Transition)(nil).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := validTransition().Validate(); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*Transition){
		"empty":                      func(p *Transition) { *p = Transition{} },
		"unknown version":            func(p *Transition) { p.Version = 2 },
		"zero checkpoint":            func(p *Transition) { p.CheckpointHeight = 0; p.EffectiveHeight = 1 },
		"overflow":                   func(p *Transition) { p.CheckpointHeight = math.MaxUint64; p.EffectiveHeight = 0 },
		"gap":                        func(p *Transition) { p.EffectiveHeight = 4 },
		"unsigned signed prefix":     func(p *Transition) { p.HistoricalSignatureHeight = 0 },
		"signature after checkpoint": func(p *Transition) { p.HistoricalSignatureHeight = 3 },
		"same key":                   func(p *Transition) { p.ReplacementProducer = p.HistoricalProducer },
		"placeholder":                func(p *Transition) { p.ReplacementProducer = "REPLACEMENT_REQUIRED" },
		"uppercase":                  func(p *Transition) { p.ReplacementProducer = strings.Repeat("A", 64) },
		"whitespace":                 func(p *Transition) { p.CheckpointHash = " " + p.CheckpointHash },
		"zero hash":                  func(p *Transition) { p.CheckpointHash = strings.Repeat("0", 64) },
		"invalid hash":               func(p *Transition) { p.HistoricalPrefixSHA256 = strings.Repeat("z", 64) },
		"no prefix bytes":            func(p *Transition) { p.HistoricalPrefixBytes = 0 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := validTransition()
			change(p)
			if p.Validate() == nil {
				t.Fatal("invalid transition accepted")
			}
		})
	}
}

func TestTransitionImmutablePrefixAllowsSuffixOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain.ndjson")
	original := []byte("{\"height\":0}\n{\"height\":1}\n{\"height\":2}\n")
	p := validTransition()
	p.HistoricalPrefixBytes = int64(len(original))
	p.HistoricalPrefixSHA256 = fmt.Sprintf("%x", sha256.Sum256(original))
	cases := map[string][]byte{"exact": original, "suffix": append(append([]byte{}, original...), []byte("{\"height\":3}\n")...), "truncated": original[:len(original)-1], "normalized": []byte("{ \"height\":0}\n{\"height\":1}\n{\"height\":2}\n")}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			err := p.ValidateJournalPrefix(path)
			wantOK := name == "exact" || name == "suffix"
			if (err == nil) != wantOK {
				t.Fatalf("error=%v expected pass=%t", err, wantOK)
			}
		})
	}
	if p.ValidateJournalPrefix(filepath.Join(t.TempDir(), "missing")) == nil {
		t.Fatal("missing history accepted")
	}
	raw := []byte("{}")
	p.HistoricalPrefixBytes = int64(len(raw))
	p.HistoricalPrefixSHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	os.WriteFile(path, raw, 0600)
	if p.ValidateJournalPrefix(path) == nil {
		t.Fatal("prefix without final newline accepted")
	}
}
