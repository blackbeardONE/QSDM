package legacymining

import (
	"bytes"
	"encoding/binary"
	"errors"
	"reflect"
	"sort"
	"testing"
)

// plIDs returns n strictly ascending IDs.
func plIDs(n int) []ProofID {
	ids := make([]ProofID, n)
	for i := range ids {
		binary.BigEndian.PutUint32(ids[i][:4], uint32(i+1))
		ids[i][31] = byte(i)
	}
	return ids
}

func plRaw(count uint16, ids ...ProofID) []byte {
	b := append([]byte(PayloadTag), 0, 0)
	binary.BigEndian.PutUint16(b[len(PayloadTag):], count)
	for _, id := range ids {
		b = append(b, id[:]...)
	}
	return b
}

func TestPayloadRoundTrip(t *testing.T) {
	for _, n := range []int{1, 2, 17, MaxPayloadIDs} {
		ids := plIDs(n)
		b, err := EncodePayload(ids)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if !bytes.Equal(b, plRaw(uint16(n), ids...)) || len(b) != payloadHeaderLen+32*n {
			t.Fatalf("n=%d: layout %x...", n, b[:payloadHeaderLen])
		}
		got, err := DecodePayload(b)
		if err != nil || !reflect.DeepEqual(got, ids) {
			t.Fatalf("n=%d: decode = %v", n, err)
		}
		b[len(b)-1] ^= 0xff // the decoded IDs do not alias the payload
		if got[n-1] != ids[n-1] {
			t.Fatalf("n=%d: decoded IDs alias the payload", n)
		}
	}
	if b, _ := EncodePayload(plIDs(MaxPayloadIDs)); len(b) != MaxPayloadBytes || MaxPayloadBytes != 32779 {
		t.Fatalf("maximum payload is %d bytes, want %d", len(b), MaxPayloadBytes)
	}
	one := ProofID{0xaa}
	if b, _ := EncodePayload([]ProofID{one}); string(b[:9]) != "QSDM-LMP1" || b[9] != 0 || b[10] != 1 {
		t.Fatalf("header %q", b[:11])
	}
}

func TestPayloadRejects(t *testing.T) {
	ids := plIDs(3)
	encode := map[string][]ProofID{
		"nil":        nil,
		"empty":      {},
		"too many":   plIDs(MaxPayloadIDs + 1),
		"descending": {ids[1], ids[0]},
		"duplicate":  {ids[0], ids[1], ids[1]},
		"unsorted":   {ids[0], ids[2], ids[1]},
	}
	for name, in := range encode {
		if b, err := EncodePayload(in); !errors.Is(err, ErrBadPayload) || b != nil {
			t.Errorf("encode %s: %x, %v; want ErrBadPayload", name, b, err)
		}
	}

	valid := plRaw(3, ids...)
	lower := append([]byte("qsdm-lmp1"), valid[len(PayloadTag):]...)
	over := plIDs(MaxPayloadIDs + 1)
	decode := map[string][]byte{
		"nil":           nil,
		"empty":         {},
		"tag only":      []byte(PayloadTag),
		"short count":   valid[:payloadHeaderLen-1],
		"lowercase tag": lower,
		"other tag":     append([]byte("QSDM-LMP2"), valid[len(PayloadTag):]...),
		"count 0":       plRaw(0),
		"count 0 + id":  plRaw(0, ids[0]),
		"count 1025":    plRaw(MaxPayloadIDs+1, over...),
		"count 65535":   plRaw(0xffff),
		"truncated id":  valid[:len(valid)-1],
		"missing id":    plRaw(3, ids[:2]...),
		"trailing byte": append(append([]byte(nil), valid...), 0),
		"extra id":      plRaw(2, ids...),
		"descending":    plRaw(2, ids[1], ids[0]),
		"duplicate":     plRaw(2, ids[0], ids[0]),
		"leading junk":  append([]byte{0}, valid...),
	}
	for name, in := range decode {
		if got, err := DecodePayload(in); !errors.Is(err, ErrBadPayload) || got != nil {
			t.Errorf("decode %s: %x, %v; want ErrBadPayload", name, got, err)
		}
	}
}

func plCheckDecoded(t *testing.T, in []byte, ids []ProofID, err error) {
	t.Helper()
	if err != nil {
		if !errors.Is(err, ErrBadPayload) || ids != nil {
			t.Fatalf("DecodePayload(%x) = %v, %v; errors must wrap ErrBadPayload and return nil", in, ids, err)
		}
		return
	}
	if len(ids) < 1 || len(ids) > MaxPayloadIDs || len(in) > MaxPayloadBytes {
		t.Fatalf("DecodePayload(%x) accepted %d IDs from %d bytes", in, len(ids), len(in))
	}
	for i := 1; i < len(ids); i++ {
		if bytes.Compare(ids[i-1][:], ids[i][:]) >= 0 {
			t.Fatalf("DecodePayload(%x) accepted IDs out of order at %d", in, i)
		}
	}
	// Canonical: the only accepted encoding of ids is in itself.
	out, err := EncodePayload(ids)
	if err != nil || !bytes.Equal(out, in) {
		t.Fatalf("DecodePayload(%x) is not canonical: re-encoded %x, %v", in, out, err)
	}
}

func FuzzDecodePayload(f *testing.F) {
	ids := plIDs(4)
	for _, seed := range [][]byte{
		nil,
		[]byte(PayloadTag),
		plRaw(1, ids[0]),
		plRaw(4, ids...),
		plRaw(2, ids[1], ids[0]),
		plRaw(2, ids[0], ids[0]),
		plRaw(0),
		plRaw(3, ids...),
		append(plRaw(1, ids[0]), 0),
		plRaw(0xffff, ids...),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		orig := append([]byte(nil), in...)
		got, err := DecodePayload(in)
		if !bytes.Equal(in, orig) {
			t.Fatal("DecodePayload modified its input")
		}
		plCheckDecoded(t, in, got, err)
		// Prefix the tag so the fuzzer mostly explores the count and body
		// checks: the codec must still accept exactly the canonical inputs.
		forged := append([]byte(PayloadTag), in...)
		got, err = DecodePayload(forged)
		plCheckDecoded(t, forged, got, err)
	})
}

func FuzzEncodePayload(f *testing.F) {
	f.Add([]byte{}, false)
	f.Add(bytes.Repeat([]byte{1}, 32), false)
	f.Add(bytes.Repeat([]byte{1}, 64), false)
	f.Add(append(bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)...), false)
	f.Add(append(bytes.Repeat([]byte{2}, 32), bytes.Repeat([]byte{1}, 32)...), true)
	f.Fuzz(func(t *testing.T, raw []byte, sorted bool) {
		ids := make([]ProofID, len(raw)/32)
		for i := range ids {
			copy(ids[i][:], raw[i*32:])
		}
		if sorted {
			sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
		}
		valid := len(ids) >= 1 && len(ids) <= MaxPayloadIDs
		for i := 1; valid && i < len(ids); i++ {
			valid = bytes.Compare(ids[i-1][:], ids[i][:]) < 0
		}
		b, err := EncodePayload(ids)
		if valid != (err == nil) {
			t.Fatalf("EncodePayload(%d IDs) = %v, want valid=%v", len(ids), err, valid)
		}
		if err != nil {
			if !errors.Is(err, ErrBadPayload) || b != nil {
				t.Fatalf("EncodePayload error %v with %x", err, b)
			}
			return
		}
		got, err := DecodePayload(b)
		if err != nil || !reflect.DeepEqual(got, ids) || len(b) != payloadHeaderLen+32*len(ids) {
			t.Fatalf("round trip of %d IDs: %v", len(ids), err)
		}
	})
}
