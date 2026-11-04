package producerpolicy

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// A version 1 policy with the heights of the public network's transition:
// blocks below 482566 are unsigned, 482566..659976 are signed by the
// historical producer, and every block from 659977 by the replacement
// producer. The producer IDs and hashes are synthetic.
var (
	shapeHistoricalProducer  = strings.Repeat("b", 64)
	shapeReplacementProducer = strings.Repeat("c", 64)
	shapeCheckpointHash      = strings.Repeat("a", 64)
	shapePrefixSHA256        = strings.Repeat("d", 64)
)

func liveShapedV1() *Transition {
	return &Transition{Version: 1, CheckpointHeight: 659976, CheckpointHash: shapeCheckpointHash,
		HistoricalSignatureHeight: 482566, HistoricalProducer: shapeHistoricalProducer,
		ReplacementProducer: shapeReplacementProducer, EffectiveHeight: 659977,
		HistoricalPrefixBytes: 2728741562, HistoricalPrefixSHA256: shapePrefixSHA256}
}

func liveShapedV2() *Transition {
	return &Transition{Version: 2, HistoricalPrefixBytes: 2728741562, HistoricalPrefixSHA256: shapePrefixSHA256,
		Epochs: []Epoch{
			{FromHeight: 482566, ProducerID: shapeHistoricalProducer},
			{FromHeight: 659977, ProducerID: shapeReplacementProducer, CheckpointHash: shapeCheckpointHash},
		}}
}

func validV2Three() *Transition {
	return &Transition{Version: 2, HistoricalPrefixBytes: 2, HistoricalPrefixSHA256: strings.Repeat("d", 64),
		Epochs: []Epoch{
			{FromHeight: 1, ProducerID: strings.Repeat("b", 64)},
			{FromHeight: 3, ProducerID: strings.Repeat("c", 64), CheckpointHash: strings.Repeat("a", 64)},
			{FromHeight: 6, ProducerID: strings.Repeat("e", 64), CheckpointHash: strings.Repeat("f", 64)},
		}}
}

func TestTransitionV1IsItsTwoEpochScheduleOnLiveShapedHeights(t *testing.T) {
	v1, v2 := liveShapedV1(), liveShapedV2()
	if err := v1.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := v2.Validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v1.Schedule(), v2.Schedule()) {
		t.Fatalf("schedules differ:\n v1 %+v\n v2 %+v", v1.Schedule(), v2.Schedule())
	}
	if v1.FirstHandoverHeight() != 659977 || v2.FirstHandoverHeight() != 659977 {
		t.Fatal("first hand-over height is not the effective height")
	}
	if v1.FinalEpoch() != v2.FinalEpoch() || v1.FinalEpoch().ProducerID != shapeReplacementProducer || v1.FinalEpoch().FromHeight-1 != v1.CheckpointHeight {
		t.Fatalf("final epoch %+v", v1.FinalEpoch())
	}
	for _, h := range []uint64{0, 1, 482565, 482566, 482567, 659975, 659976, 659977, 659978, 1 << 40} {
		want, wantOK := "", false
		if h >= v1.HistoricalSignatureHeight {
			want, wantOK = v1.HistoricalProducer, true
		}
		if h >= v1.EffectiveHeight {
			want = v1.ReplacementProducer
		}
		for _, p := range []*Transition{v1, v2} {
			if got, ok := ProducerAt(p.Schedule(), h); got != want || ok != wantOK {
				t.Fatalf("version %d height %d: producer %q/%v, want %q/%v", p.Version, h, got, ok, want, wantOK)
			}
		}
	}
	// The v1 conversion does not alias the policy.
	s := v2.Schedule()
	s[0].ProducerID = "x"
	if v2.Epochs[0].ProducerID != shapeHistoricalProducer {
		t.Fatal("Schedule aliases the configured epochs")
	}
}

func TestTransitionV2JournalPrefixMatchesV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chain.ndjson")
	original := []byte("{\"height\":0}\n{\"height\":1}\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	p := validV2Three()
	p.HistoricalPrefixBytes = int64(len(original))
	p.HistoricalPrefixSHA256 = fmt.Sprintf("%x", sha256.Sum256(original))
	if err := p.ValidateJournalPrefix(path); err != nil {
		t.Fatal(err)
	}
	p.HistoricalPrefixSHA256 = strings.Repeat("d", 64)
	if p.ValidateJournalPrefix(path) == nil {
		t.Fatal("wrong prefix accepted")
	}
	p.Epochs = p.Epochs[:1]
	if p.ValidateJournalPrefix(path) == nil {
		t.Fatal("invalid schedule passed the prefix check")
	}
}

func TestTransitionV2Validate(t *testing.T) {
	if err := validV2Three().Validate(); err != nil {
		t.Fatal(err)
	}
	h := func(c string) string { return strings.Repeat(c, 64) }
	cases := map[string]func(*Transition){
		"v1 with epochs":            func(p *Transition) { *p = *validTransition(); p.Epochs = validV2Three().Epochs },
		"v2 with checkpoint_height": func(p *Transition) { p.CheckpointHeight = 2 },
		"v2 with checkpoint_hash":   func(p *Transition) { p.CheckpointHash = h("a") },
		"v2 with signature height":  func(p *Transition) { p.HistoricalSignatureHeight = 1 },
		"v2 with historical":        func(p *Transition) { p.HistoricalProducer = h("b") },
		"v2 with replacement":       func(p *Transition) { p.ReplacementProducer = h("e") },
		"v2 with effective_height":  func(p *Transition) { p.EffectiveHeight = 3 },
		"no epochs":                 func(p *Transition) { p.Epochs = nil },
		"one epoch":                 func(p *Transition) { p.Epochs = p.Epochs[:1] },
		"too many epochs": func(p *Transition) {
			p.Epochs = nil
			for i := 0; i <= MaxEpochs; i++ {
				e := Epoch{FromHeight: uint64(i + 1), ProducerID: fmt.Sprintf("%064x", i+1)}
				if i > 0 {
					e.CheckpointHash = h("a")
				}
				p.Epochs = append(p.Epochs, e)
			}
		},
		"first from genesis":         func(p *Transition) { p.Epochs[0].FromHeight = 0 },
		"first with checkpoint":      func(p *Transition) { p.Epochs[0].CheckpointHash = h("a") },
		"equal heights (overlap)":    func(p *Transition) { p.Epochs[2].FromHeight = 3 },
		"decreasing heights":         func(p *Transition) { p.Epochs[2].FromHeight = 2 },
		"unordered epochs":           func(p *Transition) { p.Epochs[1], p.Epochs[2] = p.Epochs[2], p.Epochs[1] },
		"missing checkpoint":         func(p *Transition) { p.Epochs[2].CheckpointHash = "" },
		"zero checkpoint":            func(p *Transition) { p.Epochs[1].CheckpointHash = h("0") },
		"uppercase checkpoint":       func(p *Transition) { p.Epochs[1].CheckpointHash = h("A") },
		"placeholder producer":       func(p *Transition) { p.Epochs[2].ProducerID = "REPLACEMENT_REQUIRED" },
		"empty producer":             func(p *Transition) { p.Epochs[1].ProducerID = "" },
		"zero producer":              func(p *Transition) { p.Epochs[1].ProducerID = h("0") },
		"same producer adjacent":     func(p *Transition) { p.Epochs[2].ProducerID = p.Epochs[1].ProducerID },
		"producer reused (failback)": func(p *Transition) { p.Epochs[2].ProducerID = p.Epochs[0].ProducerID },
		"no prefix bytes":            func(p *Transition) { p.HistoricalPrefixBytes = 0 },
		"bad prefix hash":            func(p *Transition) { p.HistoricalPrefixSHA256 = "" },
		"unknown version":            func(p *Transition) { p.Version = 3 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			p := validV2Three()
			change(p)
			if p.Validate() == nil {
				t.Fatal("invalid schedule accepted")
			}
		})
	}
}
