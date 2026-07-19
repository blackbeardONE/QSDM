package main

import (
	"fmt"
	"math"
)

func checkedAvailableDiskBytes(availableBlocks, blockSize uint64) (uint64, error) {
	if blockSize == 0 {
		return 0, fmt.Errorf("filesystem reported a zero block size")
	}
	if availableBlocks > math.MaxUint64/blockSize {
		return 0, fmt.Errorf("available filesystem capacity exceeds uint64")
	}
	return availableBlocks * blockSize, nil
}
