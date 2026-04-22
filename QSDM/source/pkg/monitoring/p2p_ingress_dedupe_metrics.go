package monitoring

import "sync/atomic"

var p2pWalletIngressDedupeSkipTotal atomic.Uint64

// RecordP2PWalletIngressDedupeSkip counts inbound P2P drops because the wallet tx id was already ingested
// (e.g. mesh companion + raw JSON for the same logical transaction).
func RecordP2PWalletIngressDedupeSkip() {
	p2pWalletIngressDedupeSkipTotal.Add(1)
}

// P2PWalletIngressDedupeSkipCount returns dedupe skips since process start.
func P2PWalletIngressDedupeSkipCount() uint64 {
	return p2pWalletIngressDedupeSkipTotal.Load()
}
