package producerpolicy

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

// FuzzTransitionValidate builds a [consensus.producer_transition] table from
// fuzz input: a version (0 to 3), any mix of the version 1 fields, and up to
// MaxEpochs+2 epochs whose heights may repeat, fall or wrap around and whose
// producer IDs and checkpoint hashes are valid, repeated or malformed.
//
// Validate must accept the table exactly when an independent restatement of
// the documented rules (fuzzTransitionRulesHold) does. An accepted table must
// give a schedule the block rule can rely on: version 1 is its two-epoch
// schedule; heights strictly increase from at least 1; epoch 0 has no
// checkpoint and every later epoch has one; producers never repeat;
// FirstHandoverHeight and FinalEpoch match the schedule; ProducerAt names the
// epoch that contains each probed height; Schedule returns a copy; and the
// same table with the other version's fields mixed in is refused.
func FuzzTransitionValidate(f *testing.F) {
	// version, v1 field mask, first height, historical_signature_height,
	// layout (5 bytes for version 1 fields, then 3 bytes per epoch),
	// historical_prefix_bytes, historical_prefix_sha256 selector.
	f.Add(uint8(2), uint8(0), uint64(1), uint64(0),
		[]byte{0, 0, 0, 0, 0, 2, 0x80, 0, 4, 0x81, 0x80, 5, 0x82, 0x81}, int64(2), uint8(0x80))
	f.Add(uint8(1), uint8(0x3f), uint64(659976), uint64(482566),
		[]byte{1, 0x90, 0x80, 0x81, 0}, int64(2728741562), uint8(0x80))
	f.Add(uint8(2), uint8(0x01), uint64(1), uint64(0),
		[]byte{0, 0, 0, 0, 0, 2, 0x80, 0, 4, 0x81, 0x80}, int64(2), uint8(0x80)) // mixed
	f.Add(uint8(1), uint8(0x7f), uint64(10), uint64(5),
		[]byte{1, 0x90, 0x80, 0x81, 0, 2, 0x82, 0}, int64(2), uint8(0x80)) // version 1 with epochs
	f.Add(uint8(2), uint8(0), uint64(1), uint64(0),
		[]byte{0, 0, 0, 0, 0, 2, 0x80, 0, 2, 0x81, 0x80}, int64(2), uint8(0x80)) // equal heights
	f.Add(uint8(2), uint8(0), uint64(math.MaxUint64), uint64(0),
		[]byte{0, 0, 0, 0, 0, 2, 0x80, 0, 3, 0x81, 0x80}, int64(2), uint8(0x80)) // wraps
	f.Add(uint8(3), uint8(0), uint64(1), uint64(0), []byte{}, int64(0), uint8(0))

	f.Fuzz(func(t *testing.T, version, v1mask uint8, first, sigHeight uint64, layout []byte, prefixBytes int64, prefixSel uint8) {
		p := fuzzTransitionFrom(version, v1mask, first, sigHeight, layout, prefixBytes, prefixSel)
		err := p.Validate()
		if want := fuzzTransitionRulesHold(p); (err == nil) != want {
			t.Fatalf("Validate() = %v, but the documented rules say valid=%v for %+v", err, want, p)
		}
		if again := p.Validate(); (again == nil) != (err == nil) {
			t.Fatalf("Validate is not deterministic: %v then %v", err, again)
		}
		if err == nil {
			fuzzCheckAcceptedSchedule(t, p)
		}
	})
}

// fuzzHexValue maps a selector to a producer ID or hash. Selectors below 0x80
// pick from a pool of empty, malformed and shared values; 0x80 and above give
// distinct valid values (salt keeps producer IDs, checkpoints and the prefix
// apart).
func fuzzHexValue(sel byte, salt int) string {
	if sel >= 0x80 {
		return fmt.Sprintf("%064x", salt+int(sel))
	}
	pool := []string{
		"",
		strings.Repeat("0", 64),       // zero
		strings.Repeat("B", 64),       // uppercase
		"REPLACEMENT_REQUIRED",        // placeholder
		strings.Repeat("b", 63),       // short
		strings.Repeat("b", 65),       // long
		" " + strings.Repeat("c", 63), // whitespace
		strings.Repeat("g", 64),       // not hex
		strings.Repeat("b", 64),       // valid, shared by every salt
		strings.Repeat("c", 64),       // valid, shared by every salt
	}
	return pool[int(sel)%len(pool)]
}

func fuzzTransitionFrom(version, v1mask uint8, first, sigHeight uint64, layout []byte, prefixBytes int64, prefixSel uint8) *Transition {
	at := func(i int) byte {
		if i < len(layout) {
			return layout[i]
		}
		return 0
	}
	p := &Transition{Version: uint64(version % 4), HistoricalPrefixBytes: prefixBytes,
		HistoricalPrefixSHA256: fuzzHexValue(prefixSel, 0x2000)}
	if v1mask&0x01 != 0 {
		p.CheckpointHeight = first
	}
	if v1mask&0x02 != 0 {
		p.EffectiveHeight = p.CheckpointHeight + uint64(at(0)%3) // one of +0, +1, +2, wrapping
	}
	if v1mask&0x04 != 0 {
		p.HistoricalSignatureHeight = sigHeight
	}
	if v1mask&0x08 != 0 {
		p.CheckpointHash = fuzzHexValue(at(2), 0x1000)
	}
	if v1mask&0x10 != 0 {
		p.HistoricalProducer = fuzzHexValue(at(1), 0)
	}
	if v1mask&0x20 != 0 {
		p.ReplacementProducer = fuzzHexValue(at(3), 0)
	}
	if p.Version == 1 && v1mask&0x40 == 0 {
		return p
	}
	height := first
	for i, n := 5, 0; i+2 < len(layout) && n < MaxEpochs+2; i, n = i+3, n+1 {
		if n > 0 {
			height += uint64(layout[i]) - 2 // falls, repeats or rises; wraps at the ends
		}
		p.Epochs = append(p.Epochs, Epoch{FromHeight: height,
			ProducerID: fuzzHexValue(layout[i+1], 0), CheckpointHash: fuzzHexValue(layout[i+2], 0x1000)})
	}
	return p
}

// fuzzValidHash is the documented hash and producer ID format: exactly 64
// lowercase hexadecimal characters, not all zero.
func fuzzValidHash(s string) bool {
	if len(s) != 64 || s == strings.Repeat("0", 64) {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// fuzzTransitionRulesHold restates the rules of QSDM/docs/docs/PRODUCER_TRANSITION.md
// without using Validate.
func fuzzTransitionRulesHold(p *Transition) bool {
	switch p.Version {
	case 1:
		return len(p.Epochs) == 0 &&
			p.CheckpointHeight >= 1 && p.CheckpointHeight < math.MaxUint64 &&
			p.EffectiveHeight == p.CheckpointHeight+1 &&
			p.HistoricalSignatureHeight >= 1 && p.HistoricalSignatureHeight <= p.CheckpointHeight &&
			fuzzValidHash(p.CheckpointHash) && fuzzValidHash(p.HistoricalProducer) &&
			fuzzValidHash(p.ReplacementProducer) && fuzzValidHash(p.HistoricalPrefixSHA256) &&
			p.HistoricalProducer != p.ReplacementProducer && p.HistoricalPrefixBytes > 0
	case 2:
		if p.CheckpointHeight != 0 || p.CheckpointHash != "" || p.HistoricalSignatureHeight != 0 ||
			p.HistoricalProducer != "" || p.ReplacementProducer != "" || p.EffectiveHeight != 0 {
			return false
		}
		if len(p.Epochs) < 2 || len(p.Epochs) > 64 {
			return false
		}
		seen := make(map[string]bool)
		for i, e := range p.Epochs {
			if !fuzzValidHash(e.ProducerID) || seen[e.ProducerID] {
				return false
			}
			seen[e.ProducerID] = true
			if i == 0 {
				if e.FromHeight < 1 || e.CheckpointHash != "" {
					return false
				}
				continue
			}
			if e.FromHeight <= p.Epochs[i-1].FromHeight || !fuzzValidHash(e.CheckpointHash) {
				return false
			}
		}
		return fuzzValidHash(p.HistoricalPrefixSHA256) && p.HistoricalPrefixBytes > 0
	}
	return false
}

func fuzzCheckAcceptedSchedule(t *testing.T, p *Transition) {
	t.Helper()
	s := p.Schedule()
	if len(s) < 2 || len(s) > MaxEpochs {
		t.Fatalf("accepted schedule has %d epochs", len(s))
	}
	if p.Version == 1 {
		want := []Epoch{
			{FromHeight: p.HistoricalSignatureHeight, ProducerID: p.HistoricalProducer},
			{FromHeight: p.EffectiveHeight, ProducerID: p.ReplacementProducer, CheckpointHash: p.CheckpointHash},
		}
		if !reflect.DeepEqual(s, want) || s[1].FromHeight-1 != p.CheckpointHeight {
			t.Fatalf("version 1 schedule %+v, want %+v", s, want)
		}
	} else if !reflect.DeepEqual(s, p.Epochs) {
		t.Fatalf("version 2 schedule %+v differs from its epochs %+v", s, p.Epochs)
	}
	if s[0].FromHeight < 1 || s[0].CheckpointHash != "" {
		t.Fatalf("first epoch %+v", s[0])
	}
	ids := make(map[string]bool)
	for i, e := range s {
		if ids[e.ProducerID] {
			t.Fatalf("producer %s repeats", e.ProducerID)
		}
		ids[e.ProducerID] = true
		if i > 0 && (e.FromHeight <= s[i-1].FromHeight || e.CheckpointHash == "") {
			t.Fatalf("epoch %d %+v after %+v", i, e, s[i-1])
		}
	}
	if p.FirstHandoverHeight() != s[1].FromHeight || p.FinalEpoch() != s[len(s)-1] {
		t.Fatalf("first hand-over %d, final %+v for %+v", p.FirstHandoverHeight(), p.FinalEpoch(), s)
	}
	probes := []uint64{0, math.MaxUint64}
	for _, e := range s {
		probes = append(probes, e.FromHeight-1, e.FromHeight, e.FromHeight+1)
	}
	for _, h := range probes {
		want, wantOK := "", false
		for _, e := range s {
			if h >= e.FromHeight {
				want, wantOK = e.ProducerID, true
			}
		}
		if got, ok := ProducerAt(s, h); got != want || ok != wantOK {
			t.Fatalf("ProducerAt(%d) = %q/%v, want %q/%v", h, got, ok, want, wantOK)
		}
	}
	s[0].ProducerID = "changed by the caller"
	if p.Schedule()[0].ProducerID == "changed by the caller" {
		t.Fatal("Schedule aliases the policy")
	}

	mixed := *p
	mixed.Epochs = append([]Epoch(nil), p.Epochs...)
	if p.Version == 2 {
		mixed.EffectiveHeight = p.Epochs[1].FromHeight
	} else {
		mixed.Epochs = p.Schedule()
	}
	if mixed.Validate() == nil {
		t.Fatalf("a table mixing version 1 and version 2 fields was accepted: %+v", mixed)
	}
}
