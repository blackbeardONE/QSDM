package monitoring

import "time"

// NvidiaLockP2PGate gates libp2p-received transactions when Enabled (opt-in: nvidia_lock_gate_p2p).
// Uses the same ingested NGC proof criteria as HTTP NVIDIA-lock but never consumes ring buffer
// entries (consume=false), so single-use HTTP paths with ingest nonces remain independent.
type NvidiaLockP2PGate struct {
	Enabled         bool
	MaxProofAge     time.Duration
	ExpectedNodeID  string
	ProofHMACSecret string
}

// Allows reports whether a validated P2P transaction may be stored.
func (g *NvidiaLockP2PGate) Allows() bool {
	if g == nil || !g.Enabled {
		return true
	}
	ok, _ := NvidiaLockProofOK(g.MaxProofAge, g.ExpectedNodeID, g.ProofHMACSecret, false)
	return ok
}
