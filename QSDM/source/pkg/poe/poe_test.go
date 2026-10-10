package poe

import (
	"errors"
	"strings"
	"testing"
)

func TestValidParentID(t *testing.T) {
	good := []string{
		strings.Repeat("a", MinParentIDLen),
		strings.Repeat("Z", MaxParentIDLen),
		"solo-heartbeat-805400-1791611907090994535",
		"solo-reward-805400-0123456789abcdef",
		"hive_wallet_1784876606130_1a4983ed8ce85b54",
		"a3f1c0de9b8e7d6c5b4a39281706f5e4",
	}
	for _, id := range good {
		if !ValidParentID(id) {
			t.Errorf("ValidParentID(%q) = false", id)
		}
	}
	bad := []string{
		"",
		"parent1",                              // the old CreateTransaction placeholder
		strings.Repeat("a", MinParentIDLen-1),  // too short
		strings.Repeat("a", MaxParentIDLen+1),  // too long
		"solo-heartbeat-1:2-3333333333333",     // ':' not allowed
		"0000000000000000000000000000000a1 ",   // trailing space
		"tx-1f2e3d4c5b6" + string(rune(0xe9)), // non-ASCII
	}
	for _, id := range bad {
		if ValidParentID(id) {
			t.Errorf("ValidParentID(%q) = true", id)
		}
	}
}

func TestCheckShape(t *testing.T) {
	a, b, c := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	self := "self-transaction-0001"
	many := make([]string, MaxParents+1)
	for i := range many {
		many[i] = strings.Repeat(string(rune('a'+i)), 20)
	}
	cases := []struct {
		name    string
		parents []string
		want    error
	}{
		{"two", []string{a, b}, nil},
		{"three", []string{a, b, c}, nil},
		{"max", many[:MaxParents], nil},
		{"nil", nil, ErrParentCount},
		{"empty", []string{}, ErrParentCount},
		{"one", []string{a}, ErrParentCount},
		{"too-many", many, ErrParentCount},
		{"duplicate", []string{a, b, a}, ErrDuplicateParent},
		{"self", []string{a, self}, ErrSelfParent},
		{"malformed", []string{a, "parent2"}, ErrParentFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckShape(self, tc.parents)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("CheckShape = %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) || !IsViolation(err) {
				t.Fatalf("CheckShape = %v, want %v", err, tc.want)
			}
			if Reason(err) == "" || Reason(err) == "other" {
				t.Fatalf("Reason(%v) = %q", err, Reason(err))
			}
		})
	}
}

func TestReasonsCoverEveryError(t *testing.T) {
	all := []error{ErrParentCount, ErrParentFormat, ErrDuplicateParent, ErrSelfParent,
		ErrParentNotEarlier, ErrParentUnknown, ErrDuplicateTxID, ErrHistoryUnavailable, ErrViolation}
	seen := map[string]bool{}
	for _, err := range all {
		r := Reason(err)
		found := false
		for _, known := range Reasons {
			found = found || known == r
		}
		if !found {
			t.Fatalf("Reason(%v) = %q is not listed in Reasons", err, r)
		}
		seen[r] = true
	}
	if len(seen) != len(Reasons) {
		t.Fatalf("errors map to %d reasons, Reasons lists %d", len(seen), len(Reasons))
	}
	if Reason(errors.New("unrelated")) != "" || IsViolation(errors.New("unrelated")) {
		t.Fatal("unrelated error classified as a PoE violation")
	}
}
