// Package tunnel implements the QSDM reverse-HTTP tunnel:
// the wire protocol that lets a qsdm-attester running on a
// home machine (behind NAT, no public IP) be reached from the
// public internet via a small relay running on a host that
// HAS a public IP (typically the validator VPS).
//
// Architecture (no third-party dependency):
//
//	[ home Windows  ]                     [ public VPS ]                  [ any miner ]
//	  qsdm-attester  ── outbound TLS ──►  qsdm-relay   ◄── HTTPS ────  curl/qsdmminer
//	  (yamux server)    HTTP/1.1 Upgrade  (yamux client)
//	                    101 Switching     stores slot →
//	                    Protocols         active session
//
//	miner request:  GET https://attest.qsdm.tech/<slot>/api/v1/mining/challenge
//	relay opens a fresh yamux stream → tunnels the HTTP request back to
//	the attester → attester answers via its existing http.Server →
//	response copied back through the same stream → returned to miner.
//
// Why a custom protocol (instead of just running a tunnel
// daemon like cloudflared, frp, ngrok, or chisel):
//
//   - All four require an external binary or service. Bundling
//     tunnel logic into qsdm-attester / qsdm-relay means QSDM
//     operators install ONE thing per side and never reach for
//     a third-party trust root.
//
//   - The auth model is HMAC-bound to the same key the operator
//     already used to register their attester in peer_signers.toml.
//     One key, one allowlist file, one mental model.
//
//   - The wire shape is a stable, audit-sized 200-line protocol:
//     a 101 Upgrade with three custom headers, then yamux on
//     the hijacked connection. Every byte is reviewable.
//
// Why yamux: stream multiplexing is a solved problem and the
// libp2p/go-yamux/v5 package is already a transitive dep of
// the libp2p stack we use for BFT gossip. Adopting it here
// adds zero new dependencies. yamux's Session implements
// net.Listener directly via Accept, so the attester can
// http.Serve(yamuxSession, attesterMux) with no extra glue.
package tunnel

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Protocol identifiers — the values miners and tunnel clients
// place in the HTTP/1.1 Upgrade headers. Bump UpgradeProtocol
// only when the on-the-wire framing or auth shape changes
// (i.e. would cause an old client to misinterpret the new
// server, or vice versa). Adding new headers or extending
// auth is additive and does NOT require a bump.
const (
	UpgradeProtocol = "qsdm-tunnel/1"

	HeaderSlotID    = "X-QSDM-Slot"
	HeaderSignerID  = "X-QSDM-Signer-ID"
	HeaderTimestamp = "X-QSDM-Timestamp"
	HeaderAuth      = "X-QSDM-Auth"
	HeaderVersion   = "X-QSDM-Version"

	// MaxAuthSkew is the maximum tolerated difference between
	// a tunnel client's claimed timestamp and the relay's
	// wall clock. A 60-second window is generous enough to
	// cover NTP drift on home machines while small enough to
	// kill replay windows that matter — the connection itself
	// is also bound by the live yamux session, so an old auth
	// header reused after disconnect can't open a new one.
	MaxAuthSkew = 60 * time.Second

	// TunnelEndpoint is the canonical relay-side path that
	// expects an HTTP/1.1 Upgrade to UpgradeProtocol. Hard-
	// coded in both client and server so a typo at one end
	// cannot accidentally connect to the wrong endpoint
	// (which would otherwise look like a generic 404).
	TunnelEndpoint = "/_tunnel/connect"
)

// AuthInputs is the canonical material a tunnel client
// HMACs to prove possession of a slot's key. Field order is
// frozen — switching it would invalidate every existing
// client's auth header and require a UpgradeProtocol bump.
//
// The encoding is a single newline-delimited byte string:
//
//	"qsdm-tunnel-auth\n" || version "\n" || slot "\n" || signer_id "\n" || timestamp "\n"
//
// Newlines are safe because none of the field types can
// legitimately contain '\n' (slot IDs are restricted to
// AllowedSlotChars, signer IDs are hex-derived, timestamp is
// decimal digits, version is a fixed token). A future field
// addition extends the suffix; the prefix never changes so
// signature stability for v1 messages is preserved.
type AuthInputs struct {
	Version   string
	SlotID    string
	SignerID  string
	Timestamp int64
}

// authMessage formats AuthInputs into the stable byte string
// used for HMAC signing/verification. Lifted into its own
// helper so client + server compute IDENTICAL bytes — a
// drift here is the most subtle possible auth bug.
func authMessage(in AuthInputs) []byte {
	var b strings.Builder
	b.WriteString("qsdm-tunnel-auth\n")
	b.WriteString(in.Version)
	b.WriteString("\n")
	b.WriteString(in.SlotID)
	b.WriteString("\n")
	b.WriteString(in.SignerID)
	b.WriteString("\n")
	b.WriteString(strconv.FormatInt(in.Timestamp, 10))
	b.WriteString("\n")
	return []byte(b.String())
}

// SignAuth computes the lowercase-hex HMAC-SHA256 a tunnel
// client must place in HeaderAuth. key MUST be ≥16 bytes;
// shorter keys panic at the HMAC primitive call site, so we
// surface a clean error here instead.
func SignAuth(key []byte, in AuthInputs) (string, error) {
	if len(key) < 16 {
		return "", fmt.Errorf("tunnel: SignAuth: key %d bytes < 16", len(key))
	}
	if err := validateAuthInputs(in); err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(authMessage(in)); err != nil {
		return "", fmt.Errorf("tunnel: SignAuth: write: %w", err)
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// VerifyAuth checks that gotHex was produced by SignAuth(key, in).
// Returns true on a constant-time match, false otherwise.
// The slot/signer/version mismatch is the caller's job to
// enforce — VerifyAuth ONLY validates the cryptographic
// signature given a pre-validated AuthInputs.
func VerifyAuth(key []byte, in AuthInputs, gotHex string) bool {
	if len(key) < 16 {
		return false
	}
	want, err := SignAuth(key, in)
	if err != nil {
		return false
	}
	// hmac.Equal is constant-time. Decode both sides into
	// raw bytes first so a malformed gotHex doesn't leak
	// timing through the hex.DecodeString fast path.
	wantBytes, _ := hex.DecodeString(want)
	gotBytes, err := hex.DecodeString(gotHex)
	if err != nil {
		return false
	}
	return hmac.Equal(wantBytes, gotBytes)
}

// validateAuthInputs enforces the format invariants we rely
// on when newline-delimiting the auth message. Centralised
// so client + server see the same rules.
func validateAuthInputs(in AuthInputs) error {
	if in.Version == "" {
		return errors.New("tunnel: AuthInputs.Version empty")
	}
	if in.SlotID == "" {
		return errors.New("tunnel: AuthInputs.SlotID empty")
	}
	if !ValidSlotID(in.SlotID) {
		return fmt.Errorf("tunnel: AuthInputs.SlotID %q contains disallowed characters (allowed: %s)",
			in.SlotID, AllowedSlotChars)
	}
	if in.SignerID == "" {
		return errors.New("tunnel: AuthInputs.SignerID empty")
	}
	if strings.ContainsAny(in.SignerID, "\n\r") {
		return errors.New("tunnel: AuthInputs.SignerID contains forbidden newline")
	}
	if in.Timestamp <= 0 {
		return errors.New("tunnel: AuthInputs.Timestamp must be > 0")
	}
	return nil
}

// AllowedSlotChars is the visible character set permitted in
// slot IDs. Restricted because slot IDs end up as URL path
// segments (https://relay/<slot>/api/...) and arbitrary
// unicode there causes percent-encoding ambiguity. The
// restriction also makes the auth message format unambiguous.
const AllowedSlotChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_."

// ValidSlotID returns true if every byte of slot is in
// AllowedSlotChars and 1 ≤ len(slot) ≤ 64. The upper bound
// keeps URLs sane and prevents an operator from accidentally
// pasting a key as a slot.
func ValidSlotID(slot string) bool {
	if slot == "" || len(slot) > 64 {
		return false
	}
	for i := 0; i < len(slot); i++ {
		if !strings.ContainsRune(AllowedSlotChars, rune(slot[i])) {
			return false
		}
	}
	return true
}

// VerifyTimestampWithin checks that ts is within
// MaxAuthSkew of now. Exposed so tests + the relay can
// reuse identical drift logic.
func VerifyTimestampWithin(ts int64, now time.Time) error {
	wallNow := now.Unix()
	skew := wallNow - ts
	if skew < 0 {
		skew = -skew
	}
	if time.Duration(skew)*time.Second > MaxAuthSkew {
		return fmt.Errorf("tunnel: timestamp skew %ds exceeds MaxAuthSkew %s",
			skew, MaxAuthSkew)
	}
	return nil
}

// AuthError is a typed error returned by the relay when a
// tunnel client's headers fail validation. Exposed so tests
// can assert on specific reasons without parsing strings.
type AuthError struct {
	Reason string
}

func (e *AuthError) Error() string { return "tunnel auth: " + e.Reason }
