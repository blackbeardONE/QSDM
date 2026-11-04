package bridge

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// Bridge relay authentication.
//
// Events on the qsdm-bridge topic change local bridge state, which the
// auto-saver persists. A remote event is therefore only applied when it is
// signed with ML-DSA-87 (FIPS 204, empty context, the same primitive and key
// encoding as the validator consensus key) by a key on the operator's
// allowlist of bridge-relayer public keys. With an empty allowlist (the
// default) every remote event is dropped and counted; local API operations
// are unaffected. See docs/docs/BRIDGE_RELAY_AUTH.md.

const (
	// bridgeEventSigDomain separates bridge-event signatures from every other
	// ML-DSA-87 message the node or an operator key may sign.
	bridgeEventSigDomain = "QSDM-BRIDGE-EVENT-v1"

	// DefaultBridgeEventMaxAge bounds how old a signed event may be when it is
	// received. It limits replay of captured events.
	DefaultBridgeEventMaxAge = 10 * time.Minute
	// bridgeEventMaxFutureSkew tolerates sender clocks that run ahead.
	bridgeEventMaxFutureSkew = 2 * time.Minute

	// maxBridgeObjectIDLen bounds lock/swap IDs taken from remote events.
	// Local IDs are 32 hex characters.
	maxBridgeObjectIDLen = 128
)

// EventSigner signs outbound bridge events. *crypto.Dilithium and
// *chain.PersistentBFTSigner both satisfy it.
type EventSigner interface {
	Sign(message []byte) ([]byte, error)
	GetPublicKey() []byte
}

// RelayAuthConfig configures bridge event signing and verification.
type RelayAuthConfig struct {
	// TrustedKeys are packed ML-DSA-87 public keys (2592 bytes each) whose
	// signatures are accepted on remote events. Empty: no remote event is
	// ever applied.
	TrustedKeys [][]byte
	// Signer, when set, signs every locally published event.
	Signer EventSigner
	// MaxEventAge bounds the age of an accepted remote event. 0 uses
	// DefaultBridgeEventMaxAge.
	MaxEventAge time.Duration
	// Now is a test hook; nil uses time.Now.
	Now func() time.Time
}

// BridgeKeyID returns the identifier carried in signed events for a packed
// ML-DSA-87 public key: hex(sha256(publicKey)).
func BridgeKeyID(publicKey []byte) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

// ParseBridgeRelayerPublicKey decodes a hex-encoded packed ML-DSA-87 public
// key and checks that it unpacks.
func ParseBridgeRelayerPublicKey(hexKey string) ([]byte, error) {
	s := strings.TrimSpace(hexKey)
	s = strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X")
	raw, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("bridge relayer key: not hex: %w", err)
	}
	if len(raw) != mldsa87.PublicKeySize {
		return nil, fmt.Errorf("bridge relayer key: want %d bytes (ML-DSA-87), got %d", mldsa87.PublicKeySize, len(raw))
	}
	if err := new(mldsa87.PublicKey).UnmarshalBinary(raw); err != nil {
		return nil, fmt.Errorf("bridge relayer key: unpack: %w", err)
	}
	return raw, nil
}

// RelayStats counts what happened to events received from remote peers.
type RelayStats struct {
	Applied         uint64 // signed, trusted, and changed local state
	NoOp            uint64 // signed and trusted but already applied / unknown kind
	Malformed       uint64 // not a decodable event
	NoTrustedKeys   uint64 // dropped because the allowlist is empty
	Unsigned        uint64 // allowlist set, event carried no signature
	UntrustedSigner uint64 // signer key ID not on the allowlist
	BadSignature    uint64 // signature did not verify
	Stale           uint64 // timestamp missing, unparsable, too old or too far ahead
	BadTransition   uint64 // rejected by the local state-transition checks
}

type relayCounters struct {
	applied, noop, malformed, noTrustedKeys, unsigned, untrusted, badSig, stale, badTransition atomic.Uint64
}

func (c *relayCounters) snapshot() RelayStats {
	return RelayStats{
		Applied:         c.applied.Load(),
		NoOp:            c.noop.Load(),
		Malformed:       c.malformed.Load(),
		NoTrustedKeys:   c.noTrustedKeys.Load(),
		Unsigned:        c.unsigned.Load(),
		UntrustedSigner: c.untrusted.Load(),
		BadSignature:    c.badSig.Load(),
		Stale:           c.stale.Load(),
		BadTransition:   c.badTransition.Load(),
	}
}

// relayAuth is the validated, immutable form of RelayAuthConfig.
type relayAuth struct {
	trusted     map[string]*mldsa87.PublicKey // key ID -> key
	signer      EventSigner
	signerKeyID string
	maxAge      time.Duration
	now         func() time.Time
}

func newRelayAuth(cfg RelayAuthConfig) (*relayAuth, error) {
	a := &relayAuth{
		trusted: make(map[string]*mldsa87.PublicKey, len(cfg.TrustedKeys)),
		maxAge:  cfg.MaxEventAge,
		now:     cfg.Now,
	}
	if a.maxAge <= 0 {
		a.maxAge = DefaultBridgeEventMaxAge
	}
	if a.now == nil {
		a.now = time.Now
	}
	for i, raw := range cfg.TrustedKeys {
		if len(raw) != mldsa87.PublicKeySize {
			return nil, fmt.Errorf("bridge relay: trusted key %d: want %d bytes, got %d", i, mldsa87.PublicKeySize, len(raw))
		}
		pk := new(mldsa87.PublicKey)
		if err := pk.UnmarshalBinary(raw); err != nil {
			return nil, fmt.Errorf("bridge relay: trusted key %d: %w", i, err)
		}
		a.trusted[BridgeKeyID(raw)] = pk
	}
	if cfg.Signer != nil {
		pub := cfg.Signer.GetPublicKey()
		if len(pub) != mldsa87.PublicKeySize {
			return nil, fmt.Errorf("bridge relay: signer public key: want %d bytes, got %d", mldsa87.PublicKeySize, len(pub))
		}
		a.signer = cfg.Signer
		a.signerKeyID = BridgeKeyID(pub)
	}
	return a, nil
}

// bridgeEventSigningBytes is the exact byte string an event signature
// covers: the domain tag and every envelope field except the signature,
// each length-prefixed so no field boundary is ambiguous.
func bridgeEventSigningBytes(evt *BridgeP2PEvent) []byte {
	fields := [][]byte{
		[]byte(bridgeEventSigDomain),
		[]byte(evt.Kind),
		[]byte(evt.OriginNode),
		[]byte(evt.Timestamp),
		[]byte(evt.SignerKeyID),
		evt.Payload,
	}
	n := 0
	for _, f := range fields {
		n += 4 + len(f)
	}
	out := make([]byte, 0, n)
	var lenBuf [4]byte
	for _, f := range fields {
		binary.BigEndian.PutUint32(lenBuf[:], uint32(len(f)))
		out = append(out, lenBuf[:]...)
		out = append(out, f...)
	}
	return out
}

// sign fills SignerKeyID and Signature when a signer is configured.
func (a *relayAuth) sign(evt *BridgeP2PEvent) error {
	if a == nil || a.signer == nil {
		return nil
	}
	evt.SignerKeyID = a.signerKeyID
	sig, err := a.signer.Sign(bridgeEventSigningBytes(evt))
	if err != nil {
		evt.SignerKeyID = ""
		return err
	}
	evt.Signature = base64.StdEncoding.EncodeToString(sig)
	return nil
}

var (
	errNoTrustedKeys   = errors.New("no trusted bridge relayer keys configured")
	errUnsigned        = errors.New("event is unsigned")
	errUntrustedSigner = errors.New("event signer is not a trusted bridge relayer")
	errBadSignature    = errors.New("event signature is invalid")
	errStale           = errors.New("event timestamp is missing or outside the accepted window")
)

// verify reports whether a remote event may be applied. It never touches
// bridge state.
func (a *relayAuth) verify(evt *BridgeP2PEvent) error {
	if a == nil || len(a.trusted) == 0 {
		return errNoTrustedKeys
	}
	if evt.SignerKeyID == "" || evt.Signature == "" {
		return errUnsigned
	}
	pk, ok := a.trusted[strings.ToLower(evt.SignerKeyID)]
	if !ok {
		return errUntrustedSigner
	}
	sig, err := base64.StdEncoding.DecodeString(evt.Signature)
	if err != nil || len(sig) != mldsa87.SignatureSize {
		return errBadSignature
	}
	if !mldsa87.Verify(pk, bridgeEventSigningBytes(evt), nil, sig) {
		return errBadSignature
	}
	// Freshness is checked after the signature so the timestamp is
	// authenticated.
	ts, err := time.Parse(time.RFC3339, evt.Timestamp)
	if err != nil {
		return errStale
	}
	now := a.now()
	if now.Sub(ts) > a.maxAge || ts.Sub(now) > bridgeEventMaxFutureSkew {
		return errStale
	}
	return nil
}

func (c *relayCounters) countAuthErr(err error) {
	switch {
	case errors.Is(err, errNoTrustedKeys):
		c.noTrustedKeys.Add(1)
	case errors.Is(err, errUnsigned):
		c.unsigned.Add(1)
	case errors.Is(err, errUntrustedSigner):
		c.untrusted.Add(1)
	case errors.Is(err, errBadSignature):
		c.badSig.Add(1)
	case errors.Is(err, errStale):
		c.stale.Add(1)
	}
}

func validBridgeObjectID(id string) bool {
	return id != "" && len(id) <= maxBridgeObjectIDLen
}
