package wallet

import (
	"crypto/sha256"
	"fmt"
	"strconv"
)

// StableParentCellIDs returns two distinct 64-hex-character parent cell id strings derived from
// the wallet address and a monotonic sequence counter (suitable for API / P2P validation rules).
func StableParentCellIDs(sequence int, walletAddress string) (a, b string) {
	h1 := sha256.Sum256([]byte("qsdm-parent-a:" + strconv.Itoa(sequence) + ":" + walletAddress))
	h2 := sha256.Sum256([]byte("qsdm-parent-b:" + strconv.Itoa(sequence) + ":" + walletAddress))
	return fmt.Sprintf("%x", h1), fmt.Sprintf("%x", h2)
}
