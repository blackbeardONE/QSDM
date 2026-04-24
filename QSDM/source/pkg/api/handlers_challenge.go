package api

// Mining challenge-nonce endpoint (v2 protocol §6.2). Miners fetch
// a fresh (nonce, issued_at, signature) triple before they begin
// computing a v2 proof, and commit to those fields in the proof's
// Attestation (Proof.Attestation.Nonce / .IssuedAt) so the
// validator can detect replayed or pre-computed work.
//
// This handler is intentionally tiny. All crypto and replay-
// tracking lives in pkg/mining/challenge; the handler's only
// responsibilities are:
//
//   1. HTTP method + content-type plumbing
//   2. Marshalling the Challenge into a wire-stable JSON shape
//   3. Telling the operator (503) when no issuer is wired in
//
// Dependency-injection pattern matches MiningService: process-
// wide holder, SetChallengeIssuer() at startup, nil-tolerant.

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// ChallengeWire is the wire JSON shape returned by
// GET /api/v1/mining/challenge. All byte fields are lowercase
// hex; IssuedAt is unix seconds. This struct IS part of the v2
// protocol surface — additive changes (new fields) are safe;
// renames or type changes require a fork bump.
type ChallengeWire struct {
	Nonce     string `json:"nonce"`
	IssuedAt  int64  `json:"issued_at"`
	SignerID  string `json:"signer_id"`
	Signature string `json:"signature"`
}

// ChallengeFromCore lifts a challenge.Challenge into ChallengeWire.
// Exposed so the reference miner (and its tests) can serialise and
// parse round-trips without a second copy of the conversion.
func ChallengeFromCore(c challenge.Challenge) ChallengeWire {
	return ChallengeWire{
		Nonce:     challenge.NonceHex(c.Nonce),
		IssuedAt:  c.IssuedAt,
		SignerID:  c.SignerID,
		Signature: challenge.SignatureHex(c.Signature),
	}
}

// ChallengeIssuer is the narrow interface the HTTP handler
// depends on. The reference implementation lives in
// pkg/mining/challenge (*Issuer satisfies it). A separate
// interface lets tests inject fakes without pulling the full
// crypto surface.
type ChallengeIssuer interface {
	Issue() (challenge.Challenge, error)
}

type challengeIssuerHolder struct {
	mu  sync.RWMutex
	iss ChallengeIssuer
}

var challengeHolder = &challengeIssuerHolder{}

// SetChallengeIssuer installs (or removes, when iss==nil) the
// process-wide challenge issuer. Validators call this once at
// startup after loading their HMAC key material.
func SetChallengeIssuer(iss ChallengeIssuer) {
	challengeHolder.mu.Lock()
	defer challengeHolder.mu.Unlock()
	challengeHolder.iss = iss
}

func currentChallengeIssuer() ChallengeIssuer {
	challengeHolder.mu.RLock()
	defer challengeHolder.mu.RUnlock()
	return challengeHolder.iss
}

// MiningChallengeHandler serves GET /api/v1/mining/challenge.
// Always returns fresh randomness — there is no caching, because
// every miner must commit to its OWN nonce. A shared response
// would defeat replay protection.
func (h *Handlers) MiningChallengeHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	iss := currentChallengeIssuer()
	if iss == nil {
		writeMiningUnavailable(w, "challenge issuer not configured on this node")
		return
	}
	c, err := iss.Issue()
	if err != nil {
		// Issuer failures are always server-internal (PRNG exhausted,
		// signer panic, etc.); a miner can't do anything about them,
		// so 500 is the right code.
		http.Error(w, "challenge issuer error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Cache-Control: no-store is mandatory — a cached response
	// would leak the same nonce to two miners.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(ChallengeFromCore(c))
}
