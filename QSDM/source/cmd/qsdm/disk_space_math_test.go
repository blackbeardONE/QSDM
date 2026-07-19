package main

import (
	"math"
	"testing"
)

func TestCheckedAvailableDiskBytes(t *testing.T) {
	tests := []struct {
		name      string
		blocks    uint64
		blockSize uint64
		want      uint64
		wantErr   bool
	}{
		{name: "normal", blocks: 1024, blockSize: 4096, want: 4 * 1024 * 1024},
		{name: "zero blocks", blocks: 0, blockSize: 4096, want: 0},
		{name: "maximum", blocks: math.MaxUint64, blockSize: 1, want: math.MaxUint64},
		{name: "zero block size", blocks: 1, blockSize: 0, wantErr: true},
		{name: "overflow", blocks: math.MaxUint64/2 + 1, blockSize: 2, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checkedAvailableDiskBytes(tc.blocks, tc.blockSize)
			if (err != nil) != tc.wantErr {
				t.Fatalf("checkedAvailableDiskBytes() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("checkedAvailableDiskBytes() = %d, want %d", got, tc.want)
			}
		})
	}
}
