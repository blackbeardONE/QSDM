package monitoring

import "sync/atomic"

var nvidiaLockHTTPBlocks atomic.Uint64
var nvidiaLockP2PRejects atomic.Uint64
var ngcChallengeIssued atomic.Uint64
var ngcChallengeRateLimited atomic.Uint64

// RecordNvidiaLockHTTPBlock increments the count of state-changing API calls blocked by NVIDIA-lock (403).
func RecordNvidiaLockHTTPBlock() {
	nvidiaLockHTTPBlocks.Add(1)
}

// NvidiaLockHTTPBlockCount returns blocks recorded since process start.
func NvidiaLockHTTPBlockCount() uint64 {
	return nvidiaLockHTTPBlocks.Load()
}

// RecordNvidiaLockP2PReject increments drops of libp2p-received txs when P2P NVIDIA-lock gate is on.
func RecordNvidiaLockP2PReject() {
	nvidiaLockP2PRejects.Add(1)
}

// NvidiaLockP2PRejectCount returns P2P gate denials since process start.
func NvidiaLockP2PRejectCount() uint64 {
	return nvidiaLockP2PRejects.Load()
}

// RecordNGCChallengeIssued counts successful GET /ngc-challenge responses (nonce issued).
func RecordNGCChallengeIssued() {
	ngcChallengeIssued.Add(1)
}

// RecordNGCChallengeRateLimited counts 429s from the API rate limiter on ngc-challenge.
func RecordNGCChallengeRateLimited() {
	ngcChallengeRateLimited.Add(1)
}

// NGCChallengeIssuedCount returns nonces issued since process start.
func NGCChallengeIssuedCount() uint64 {
	return ngcChallengeIssued.Load()
}

// NGCChallengeRateLimitedCount returns challenge rate-limit hits since process start.
func NGCChallengeRateLimitedCount() uint64 {
	return ngcChallengeRateLimited.Load()
}
