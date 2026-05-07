package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveRelayKey_FallsBackToSignerKey(t *testing.T) {
	signer := bytes.Repeat([]byte{0xAB}, 32)
	got, err := resolveRelayKey("", signer)
	if err != nil {
		t.Fatalf("resolveRelayKey(empty): %v", err)
	}
	if !bytes.Equal(got, signer) {
		t.Fatalf("expected fallback to signer key bytes")
	}
	// Mutating the returned slice MUST NOT touch the signer.
	got[0] = 0
	if signer[0] != 0xAB {
		t.Fatalf("returned slice aliases signer key — defensive copy required")
	}
}

func TestResolveRelayKey_DecodesHex(t *testing.T) {
	hex32 := strings.Repeat("ab", 32)
	got, err := resolveRelayKey(hex32, nil)
	if err != nil {
		t.Fatalf("resolveRelayKey: %v", err)
	}
	if len(got) != 32 {
		t.Fatalf("len = %d want 32", len(got))
	}
	for i, b := range got {
		if b != 0xAB {
			t.Fatalf("byte %d = %x want 0xAB", i, b)
		}
	}
}

func TestResolveRelayKey_RejectsBad(t *testing.T) {
	cases := []struct {
		name string
		hex  string
	}{
		{"odd length", "abc"},
		{"non-hex", "zz" + strings.Repeat("a", 30)},
		{"too short (8 bytes)", strings.Repeat("ab", 8)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := resolveRelayKey(tc.hex, nil); err == nil {
				t.Fatalf("expected error for %q", tc.hex)
			}
		})
	}
}

func TestHexValue_KnownDigits(t *testing.T) {
	want := map[byte]byte{
		'0': 0, '9': 9,
		'a': 10, 'f': 15,
		'A': 10, 'F': 15,
	}
	for in, exp := range want {
		if got := hexValue(in); got != exp {
			t.Errorf("hexValue(%c) = %d want %d", in, got, exp)
		}
	}
}
