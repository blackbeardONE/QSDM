package monitoring

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/branding"
)

// NGCProofHMACPayload builds the canonical UTF-8 message over which an NGC
// proof's HMAC is computed. Both pre-rebrand (qsdm_*) and post-rebrand
// (qsdm_*) bundle field names are accepted; the preferred name wins when
// both are present. The signature is taken over the *value* of the node ID,
// not over the field name, so old sidecars continue to validate under the
// new code as long as the value they signed still appears in the bundle.
//
// v2 appends the ingest nonce line when a server-issued ingest-nonce field
// is present (see Major Update §8.5.5 rationale for v2 binding).
func NGCProofHMACPayload(m map[string]interface{}) string {
	node := ngcFieldString(m, branding.ProofNodeIDFieldPreferred, branding.ProofNodeIDFieldLegacy)
	cuda, _ := m["cuda_proof_hash"].(string)
	ts, _ := m["timestamp_utc"].(string)
	nonce := strings.TrimSpace(ngcFieldString(m, branding.ProofIngestNonceFieldPreferred, branding.ProofIngestNonceFieldLegacy))
	if nonce != "" {
		return "v2\n" + node + "\n" + cuda + "\n" + ts + "\n" + nonce + "\n"
	}
	return "v1\n" + node + "\n" + cuda + "\n" + ts + "\n"
}

// NGCProofHMACValid reports whether the proof bundle's HMAC field matches
// NGCProofHMACPayload under the given secret. Preferred field name is
// qsdm_proof_hmac; legacy qsdm_proof_hmac is still accepted.
// If secret is empty, returns true (caller enforces requiring HMAC only when
// secret is configured on the node).
func NGCProofHMACValid(m map[string]interface{}, secret string) bool {
	if strings.TrimSpace(secret) == "" {
		return true
	}
	hexSig := ngcFieldString(m, branding.ProofHMACFieldPreferred, branding.ProofHMACFieldLegacy)
	hexSig = strings.TrimSpace(hexSig)
	if hexSig == "" {
		return false
	}
	b, err := hex.DecodeString(hexSig)
	if err != nil || len(b) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(NGCProofHMACPayload(m)))
	return hmac.Equal(b, mac.Sum(nil))
}
