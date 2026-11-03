package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// Fuzz targets for the request-field validators in validation.go. Each one
// is checked against its documented rule written out independently.

func fuzzAllBytes(s string, ok func(byte) bool) bool {
	for i := 0; i < len(s); i++ {
		if !ok(s[i]) {
			return false
		}
	}
	return true
}

func fuzzIsHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func fuzzIsTxIDChar(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '-'
}

func fuzzValidTxID(s string) bool {
	return len(s) >= MinTxIDLength && len(s) <= MaxTxIDLength && fuzzAllBytes(s, fuzzIsTxIDChar)
}

func FuzzValidateAddressAndTxID(f *testing.F) {
	sum := sha256.Sum256([]byte("wallet public key"))
	address := hex.EncodeToString(sum[:])
	for _, s := range []string{
		address, strings.ToUpper(address), address[:MinAddressLength], address[:MinAddressLength-1],
		strings.Repeat(address, 2), strings.Repeat(address, 2) + "0", "0x" + address, address + " ",
		"tx-0000000000000001", "solo-heartbeat-805400-1791611907090994535", "tx_" + address,
		"short-id", "tx-00000000000:01", "",
		address + "," + "tx-0000000000000001", strings.Repeat("tx-0000000000000001,", 11),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		err := ValidateAddress(s)
		want := len(s) >= MinAddressLength && len(s) <= MaxAddressLength && fuzzAllBytes(s, fuzzIsHex)
		if (err == nil) != want || (err != nil && !errors.Is(err, ErrInvalidAddress)) {
			t.Fatalf("ValidateAddress(%q) = %v, rule says valid=%v", s, err, want)
		}
		err = ValidateTransactionID(s)
		if (err == nil) != fuzzValidTxID(s) || (err != nil && !errors.Is(err, ErrInvalidTransactionID)) {
			t.Fatalf("ValidateTransactionID(%q) = %v", s, err)
		}
		// The same input as a comma-separated parent_cells list.
		parents := strings.Split(s, ",")
		want = len(parents) <= MaxParentCells
		for _, p := range parents {
			want = want && fuzzValidTxID(p)
		}
		if err := ValidateParentCells(parents); (err == nil) != want {
			t.Fatalf("ValidateParentCells(%q) = %v, rule says valid=%v", parents, err, want)
		}
	})
}

func FuzzValidateHexAndSignature(f *testing.F) {
	public, private, err := mldsa87.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	signature := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(private, []byte("canonical envelope"), nil, true, signature); err != nil {
		f.Fatal(err)
	}
	publicHex, signatureHex := hex.EncodeToString(public.Bytes()), hex.EncodeToString(signature)
	for _, seed := range []struct {
		s string
		n int
	}{
		{publicHex, mldsa87.PublicKeySize}, {strings.ToUpper(publicHex), 0}, {publicHex[1:], 0},
		{publicHex + "00", mldsa87.PublicKeySize}, {signatureHex, mldsa87.SignatureSize}, {signatureHex, -1},
		{signatureHex[:100], 50}, {signatureHex[:99], 0}, {strings.Repeat("ab", 5001), 0}, {"", 0}, {"zz", 1},
	} {
		f.Add(seed.s, seed.n)
	}
	f.Fuzz(func(t *testing.T, s string, expectedLength int) {
		isHex := s != "" && len(s)%2 == 0 && fuzzAllBytes(s, fuzzIsHex)
		want := isHex && (expectedLength <= 0 || len(s) == 2*expectedLength)
		if err := ValidateHexString(s, expectedLength); (err == nil) != want {
			t.Fatalf("ValidateHexString(%q, %d) = %v, rule says valid=%v", s, expectedLength, err, want)
		}
		want = isHex && len(s) >= 100 && len(s) <= 10000
		if err := ValidateSignature(s); (err == nil) != want {
			t.Fatalf("ValidateSignature(%d chars) = %v, rule says valid=%v", len(s), err, want)
		}
		want = s == "" || isHex && len(s) == 2*mldsa87.PublicKeySize
		if err := ValidateOptionalMLDSAPublicKeyHex(s); (err == nil) != want {
			t.Fatalf("ValidateOptionalMLDSAPublicKeyHex(%d chars) = %v, rule says valid=%v", len(s), err, want)
		}
	})
}

func FuzzValidateAmountAndTimestamp(f *testing.F) {
	now := time.Now().UTC()
	for _, seed := range []struct {
		amount float64
		ts     string
	}{
		{1, now.Format(time.RFC3339)},
		{MinAmount, now.Add(-time.Hour).Format(time.RFC3339Nano)},
		{MaxAmount, now.Add(MaxClockSkew / 2).Format(time.RFC3339)},
		{MinAmount / 2, now.Add(time.Hour).Format(time.RFC3339)},
		{math.Nextafter(MaxAmount, math.Inf(1)), now.Add(-MaxTransactionAge - time.Minute).Format(time.RFC3339)},
		{math.Inf(1), ""},
		{math.NaN(), "2026-08-01 12:00:00"},
		{-1, now.Format("2006-01-02T15:04:05.999999999-07:00")},
		{0, "garbage"},
	} {
		f.Add(seed.amount, seed.ts)
	}
	f.Fuzz(func(t *testing.T, amount float64, ts string) {
		err := ValidateAmount(amount)
		want := !math.IsNaN(amount) && !math.IsInf(amount, 0) && amount >= MinAmount && amount <= MaxAmount
		if (err == nil) != want || (err != nil && !errors.Is(err, ErrInvalidAmount)) {
			t.Fatalf("ValidateAmount(%v) = %v, rule says valid=%v", amount, err, want)
		}

		before := time.Now()
		err = ValidateTimestamp(ts)
		after := time.Now()
		if err != nil {
			if ts == "" || !errors.Is(err, ErrInvalidTimestamp) {
				t.Fatalf("ValidateTimestamp(%q) = %v", ts, err)
			}
			return
		}
		if ts == "" {
			return // optional: the legacy wire format may omit it
		}
		at, perr := time.Parse(time.RFC3339Nano, ts)
		if perr != nil {
			at, perr = time.Parse(time.RFC3339, ts)
		}
		if perr != nil || at.After(after.Add(MaxClockSkew)) || at.Before(before.Add(-MaxTransactionAge)) {
			t.Fatalf("ValidateTimestamp accepted %q (parse %v) outside [now-%s, now+%s]", ts, perr, MaxTransactionAge, MaxClockSkew)
		}
	})
}
