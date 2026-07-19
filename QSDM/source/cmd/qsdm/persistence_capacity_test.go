package main

import (
	"errors"
	"strings"
	"testing"
)

func TestParsePersistenceReserve(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    uint64
		wantErr string
	}{
		{name: "default", want: defaultPersistenceReserveBytes},
		{name: "custom", raw: "3221225472", want: 3 << 30},
		{name: "too small", raw: "1", wantErr: "must be at least"},
		{name: "invalid", raw: "two-gib", wantErr: "unsigned byte count"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePersistenceReserve(tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("parsePersistenceReserve(%q) error = %v, want %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parsePersistenceReserve(%q) = %d, %v; want %d", tc.raw, got, err, tc.want)
			}
		})
	}
}

func TestCheckPersistenceCapacity(t *testing.T) {
	const minimum = uint64(2 << 30)
	got, err := checkPersistenceCapacity("state", minimum, func(path string) (uint64, error) {
		if path != "state" {
			t.Fatalf("probe path = %q", path)
		}
		return minimum + 1, nil
	})
	if err != nil || got != minimum+1 {
		t.Fatalf("healthy capacity = %d, %v", got, err)
	}

	got, err = checkPersistenceCapacity("state", minimum, func(string) (uint64, error) {
		return minimum - 1, nil
	})
	if err == nil || got != minimum-1 || !strings.Contains(err.Error(), "reserve breached") {
		t.Fatalf("low capacity = %d, %v", got, err)
	}

	probeErr := errors.New("probe failed")
	_, err = checkPersistenceCapacity("state", minimum, func(string) (uint64, error) {
		return 0, probeErr
	})
	if err == nil || !errors.Is(err, probeErr) {
		t.Fatalf("probe error = %v", err)
	}
}

func TestAvailableDiskBytes(t *testing.T) {
	available, err := availableDiskBytes(t.TempDir())
	if err != nil {
		t.Fatalf("availableDiskBytes: %v", err)
	}
	if available == 0 {
		t.Fatal("availableDiskBytes returned zero for the test volume")
	}
}
