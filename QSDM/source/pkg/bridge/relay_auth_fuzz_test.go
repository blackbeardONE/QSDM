package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// The fuzz target below feeds bytes to a relay the way a qsdm-bridge message
// from a peer arrives (readLoop -> processRemote: decode, authenticate,
// apply). Keys are fixed and sign deterministically, and the relay clock is
// fixed, so every input reproduces.

var fuzzRelayNow = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

const fuzzRelaySelf = "self-node"

// fuzzRelayKey is an ML-DSA-87 key derived from a fixed seed. It satisfies
// EventSigner.
type fuzzRelayKey struct {
	private *mldsa87.PrivateKey
	public  *mldsa87.PublicKey
	pub     []byte // packed public key
	id      string // hex(sha256(pub)), the signer_key_id
}

func newFuzzRelayKey(tag string) fuzzRelayKey {
	var seed [mldsa87.SeedSize]byte
	copy(seed[:], tag)
	public, private := mldsa87.NewKeyFromSeed(&seed)
	pub := public.Bytes()
	sum := sha256.Sum256(pub)
	return fuzzRelayKey{private: private, public: public, pub: pub, id: hex.EncodeToString(sum[:])}
}

func (k fuzzRelayKey) Sign(message []byte) ([]byte, error) {
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(k.private, message, nil, false, sig); err != nil {
		return nil, err
	}
	return sig, nil
}

func (k fuzzRelayKey) GetPublicKey() []byte { return bytes.Clone(k.pub) }

// fuzzEventPreimage rebuilds the signed byte string from the documented wire
// format (docs/docs/BRIDGE_RELAY_AUTH.md) without bridgeEventSigningBytes:
// the domain tag and the envelope fields, each with a 4-byte big-endian
// length prefix.
func fuzzEventPreimage(e *BridgeP2PEvent) []byte {
	var b bytes.Buffer
	for _, f := range [][]byte{[]byte("QSDM-BRIDGE-EVENT-v1"), []byte(e.Kind), []byte(e.OriginNode),
		[]byte(e.Timestamp), []byte(e.SignerKeyID), e.Payload} {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(f)))
		b.Write(n[:])
		b.Write(f)
	}
	return b.Bytes()
}

// fuzzAuthentic is the reference for "may be applied": the event names key,
// carries a signature by key over the documented pre-image that circl
// verifies, and its timestamp is inside the default age window.
func fuzzAuthentic(e *BridgeP2PEvent, key fuzzRelayKey) bool {
	if strings.ToLower(e.SignerKeyID) != key.id {
		return false
	}
	sig, err := base64.StdEncoding.DecodeString(e.Signature)
	if err != nil || len(sig) != mldsa87.SignatureSize || !mldsa87.Verify(key.public, fuzzEventPreimage(e), nil, sig) {
		return false
	}
	return fuzzFresh(e.Timestamp)
}

func fuzzFresh(ts string) bool {
	t, err := time.Parse(time.RFC3339, ts)
	return err == nil && !t.Before(fuzzRelayNow.Add(-DefaultBridgeEventMaxAge)) &&
		!t.After(fuzzRelayNow.Add(bridgeEventMaxFutureSkew))
}

// fuzzSignedWire builds the wire bytes of an event signed by key, the way a
// relayer's publish does but with a fixed timestamp.
func fuzzSignedWire(tb testing.TB, key fuzzRelayKey, kind string, payload any, origin, ts string) []byte {
	tb.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		tb.Fatal(err)
	}
	e := BridgeP2PEvent{Kind: kind, Payload: raw, OriginNode: origin, Timestamp: ts, SignerKeyID: key.id}
	sig, err := key.Sign(fuzzEventPreimage(&e))
	if err != nil {
		tb.Fatal(err)
	}
	e.Signature = base64.StdEncoding.EncodeToString(sig)
	out, err := json.Marshal(e)
	if err != nil {
		tb.Fatal(err)
	}
	return out
}

// fuzzSignTemplate decodes raw as an envelope and signs it with key. The
// envelope is first normalised to the form a receiver decodes (valid UTF-8,
// compact payload), which is what a real publisher's json.Marshal output
// already is. With relabel, a signer_key_id carried by the template replaces
// key's ID after signing. It returns nil when raw is not an envelope.
func fuzzSignTemplate(raw []byte, key fuzzRelayKey, relabel bool) []byte {
	var tmpl BridgeP2PEvent
	if json.Unmarshal(raw, &tmpl) != nil {
		return nil
	}
	label := tmpl.SignerKeyID
	norm, err := json.Marshal(tmpl)
	if err != nil {
		return nil
	}
	var e BridgeP2PEvent
	if json.Unmarshal(norm, &e) != nil {
		return nil
	}
	e.SignerKeyID, e.Signature = key.id, ""
	sig, err := key.Sign(fuzzEventPreimage(&e))
	if err != nil {
		return nil
	}
	e.Signature = base64.StdEncoding.EncodeToString(sig)
	if relabel && label != "" {
		e.SignerKeyID = label
	}
	out, err := json.Marshal(e)
	if err != nil {
		return nil
	}
	return out
}

type fuzzRelayState struct {
	locks map[string]Lock
	swaps map[string]Swap
}

func newFuzzRelay(tb testing.TB, cfg RelayAuthConfig) (*P2PRelay, *BridgeProtocol, *AtomicSwapProtocol) {
	tb.Helper()
	a, err := newRelayAuth(cfg)
	if err != nil {
		tb.Fatal(err)
	}
	bp := &BridgeProtocol{locks: map[string]*Lock{
		"lk-open":     {ID: "lk-open", Amount: 5, SecretHash: "h1", Status: LockStatusLocked},
		"lk-redeemed": {ID: "lk-redeemed", Amount: 7, Status: LockStatusRedeemed},
	}}
	asp := &AtomicSwapProtocol{swaps: map[string]*Swap{
		"sw-open": {ID: "sw-open", InitiatorAmount: 3, Status: SwapStatusInitiated},
		"sw-done": {ID: "sw-done", Status: SwapStatusCompleted},
	}}
	return &P2PRelay{bridge: bp, swap: asp, auth: a}, bp, asp
}

func snapshotFuzzRelay(bp *BridgeProtocol, asp *AtomicSwapProtocol) fuzzRelayState {
	s := fuzzRelayState{locks: map[string]Lock{}, swaps: map[string]Swap{}}
	for id, l := range bp.locks {
		s.locks[id] = *l
	}
	for id, sw := range asp.swaps {
		s.swaps[id] = *sw
	}
	return s
}

// fuzzStateChanges lists the objects that differ between two snapshots. An
// object is never deleted by a remote event.
func fuzzStateChanges(t *testing.T, before, after fuzzRelayState) []string {
	t.Helper()
	var changed []string
	for id, l := range after.locks {
		if b, ok := before.locks[id]; !ok || !reflect.DeepEqual(b, l) {
			changed = append(changed, "lock "+id)
		}
	}
	for id, s := range after.swaps {
		if b, ok := before.swaps[id]; !ok || !reflect.DeepEqual(b, s) {
			changed = append(changed, "swap "+id)
		}
	}
	if len(after.locks) < len(before.locks) || len(after.swaps) < len(before.swaps) {
		t.Fatal("a remote event deleted a lock or swap")
	}
	return changed
}

// fuzzCheckTransition checks that the one change an applied event made is a
// legal transition for its kind (docs/docs/BRIDGE_RELAY_AUTH.md, section 1).
func fuzzCheckTransition(t *testing.T, kind, change string, before, after fuzzRelayState) {
	t.Helper()
	typ, id, _ := strings.Cut(change, " ")
	if typ == "lock" {
		now := after.locks[id]
		old, existed := before.locks[id]
		switch {
		case !existed:
			if kind != "lock_created" || now.Status != LockStatusLocked || now.Secret != "" || id == "" || len(id) > maxBridgeObjectIDLen {
				t.Fatalf("%s created lock %+v", kind, now)
			}
		case old.Status == LockStatusLocked && (kind == "lock_redeemed" && now.Status == LockStatusRedeemed ||
			kind == "lock_refunded" && now.Status == LockStatusRefunded):
			old.Status = now.Status
			if !reflect.DeepEqual(old, now) {
				t.Fatalf("%s changed more than the status of lock %q", kind, id)
			}
		default:
			t.Fatalf("%s moved lock %q from %q to %q", kind, id, old.Status, now.Status)
		}
		return
	}
	now := after.swaps[id]
	old, existed := before.swaps[id]
	switch {
	case !existed:
		if kind != "swap_initiated" || now.Status != SwapStatusInitiated || now.InitiatorSecret != "" ||
			now.ParticipantSecret != "" || id == "" || len(id) > maxBridgeObjectIDLen {
			t.Fatalf("%s created swap %+v", kind, now)
		}
	case kind == "swap_participated" && old.Status == SwapStatusInitiated && now.Status == SwapStatusParticipated,
		kind == "swap_completed" && (old.Status == SwapStatusInitiated || old.Status == SwapStatusParticipated) &&
			now.Status == SwapStatusCompleted:
		old.Status = now.Status
		if !reflect.DeepEqual(old, now) {
			t.Fatalf("%s changed more than the status of swap %q", kind, id)
		}
	default:
		t.Fatalf("%s moved swap %q from %q to %q", kind, id, old.Status, now.Status)
	}
}

func fuzzStatsTotal(s RelayStats) uint64 {
	return s.Applied + s.NoOp + s.Malformed + s.NoTrustedKeys + s.Unsigned + s.UntrustedSigner +
		s.BadSignature + s.Stale + s.BadTransition
}

// FuzzProcessRemoteBridgeEvent runs one remote qsdm-bridge message through
// processRemote on a relay that trusts one fixed key, and on a default relay
// that trusts none. mode%3 selects the input:
//
//	0: the bytes as received from a peer;
//	1: an envelope the harness signs with the trusted key, so the fuzzer
//	   reaches acceptance;
//	2: an envelope the harness signs with an untrusted key (optionally
//	   relabelled afterwards with the signer_key_id the template carries).
//
// Nothing may change local state unless it carries a valid signature by the
// trusted key over the documented pre-image and is inside the age window; the
// authentication step accepts exactly those events; an event signed by the
// trusted key inside the window is always accepted; each message is counted
// once; and an applied event makes exactly one legal state change.
func FuzzProcessRemoteBridgeEvent(f *testing.F) {
	prev := log.Writer()
	log.SetOutput(io.Discard) // the relay logs every applied event
	f.Cleanup(func() { log.SetOutput(prev) })

	trusted := newFuzzRelayKey("bridge relay fuzz: trusted relayer key")
	other := newFuzzRelayKey("bridge relay fuzz: untrusted peer key")
	fresh := fuzzRelayNow.Add(-time.Minute).Format(time.RFC3339)
	newLock := Lock{ID: "lk-new", SourceChain: "a", TargetChain: "b", Asset: "TOKEN", Amount: 9, SecretHash: "h2",
		Secret: "pre-image", Status: LockStatusLocked, LockedAt: fuzzRelayNow, ExpiresAt: fuzzRelayNow.Add(time.Hour)}
	open := Lock{ID: "lk-open", Amount: 5, SecretHash: "h1", Status: LockStatusLocked}

	genuine := fuzzSignedWire(f, trusted, "lock_created", newLock, "peer-a", fresh)
	relay, bp, _ := newFuzzRelay(f, RelayAuthConfig{TrustedKeys: [][]byte{trusted.pub}, Now: func() time.Time { return fuzzRelayNow }})
	relay.processRemote(genuine, fuzzRelaySelf)
	if l, ok := bp.locks["lk-new"]; !ok || l.Secret != "" || relay.Stats().Applied != 1 {
		f.Fatalf("genuine seed not applied as expected: %+v", relay.Stats())
	}

	var forged, tampered BridgeP2PEvent
	if err := json.Unmarshal(fuzzSignedWire(f, other, "lock_created", newLock, "peer-a", fresh), &forged); err != nil {
		f.Fatal(err)
	}
	forged.SignerKeyID = trusted.id // an untrusted signature relabelled as the trusted key
	forgedWire, _ := json.Marshal(forged)
	if err := json.Unmarshal(genuine, &tampered); err != nil {
		f.Fatal(err)
	}
	tampered.Payload = bytes.Replace(tampered.Payload, []byte(`"Amount":9`), []byte(`"Amount":9000`), 1)
	tamperedWire, _ := json.Marshal(tampered)
	unsigned, _ := json.Marshal(BridgeP2PEvent{Kind: "lock_created", Payload: json.RawMessage(`{"ID":"lk-u","Status":"locked"}`),
		OriginNode: "peer-a", Timestamp: fresh})

	for _, seed := range [][]byte{
		genuine,
		fuzzSignedWire(f, trusted, "lock_redeemed", open, "peer-a", fresh),
		fuzzSignedWire(f, trusted, "lock_refunded", map[string]string{"lock_id": "lk-open"}, "peer-a", fresh),
		fuzzSignedWire(f, trusted, "lock_refunded", map[string]string{"lock_id": "lk-redeemed"}, "peer-a", fresh),
		fuzzSignedWire(f, trusted, "lock_redeemed", map[string]string{"lock_id": "lk-redeemed"}, "peer-a", fresh),
		fuzzSignedWire(f, trusted, "swap_initiated", Swap{ID: "sw-new", Status: SwapStatusInitiated, InitiatorSecret: "x"}, "peer-a", fresh),
		fuzzSignedWire(f, trusted, "swap_participated", map[string]string{"ID": "sw-open"}, "peer-a", fresh),
		fuzzSignedWire(f, trusted, "swap_completed", map[string]string{"swap_id": "sw-open"}, "peer-a", fresh),
		fuzzSignedWire(f, trusted, "lock_created", newLock, "peer-a", fuzzRelayNow.Add(-11*time.Minute).Format(time.RFC3339)),
		fuzzSignedWire(f, trusted, "lock_created", newLock, "peer-a", fuzzRelayNow.Add(3*time.Minute).Format(time.RFC3339)),
		fuzzSignedWire(f, trusted, "lock_created", newLock, fuzzRelaySelf, fresh),
		fuzzSignedWire(f, other, "lock_created", newLock, "peer-a", fresh),
		forgedWire,
		tamperedWire,
		unsigned,
		[]byte(`{not json`),
		[]byte(`null`),
	} {
		f.Add(seed, uint8(0))
	}
	for _, tmpl := range []string{
		`{"kind":"lock_created","payload":{"ID":"lk-t","Status":"locked","Amount":1},"origin_node":"peer-b","ts":"2026-08-01T11:59:00Z"}`,
		`{"kind":"lock_redeemed","payload":{"ID":"lk-open"},"origin_node":"peer-b","ts":"2026-08-01T11:50:00Z"}`,
		`{"kind":"swap_completed","payload":{"swap_id":"sw-open"},"origin_node":"peer-b","ts":"2026-08-01T12:02:00Z"}`,
		`{"kind":"lock_refunded","payload":{"lock_id":"lk-open"},"origin_node":"peer-b","ts":"2026-08-01T11:49:59Z"}`,
		`{"kind":"swap_initiated","payload":{"ID":"sw-t","Status":"initiated"},"origin_node":"peer-b","ts":"2026-08-01T12:02:01Z"}`,
		`{"kind":"lock_created","payload":{"ID":"lk-t","Status":"locked"},"origin_node":"peer-b","ts":"2026-08-01T11:59:00Z","signer_key_id":"` + trusted.id + `"}`,
	} {
		f.Add([]byte(tmpl), uint8(1))
		f.Add([]byte(tmpl), uint8(2))
	}

	f.Fuzz(func(t *testing.T, raw []byte, mode uint8) {
		data := raw
		switch mode % 3 {
		case 1:
			data = fuzzSignTemplate(raw, trusted, false)
		case 2:
			data = fuzzSignTemplate(raw, other, true)
		}
		if data == nil {
			return
		}
		var evt BridgeP2PEvent
		decoded := json.Unmarshal(data, &evt) == nil
		self := decoded && evt.OriginNode == fuzzRelaySelf

		// A node with no trusted keys (the default) never changes state.
		def, dbp, dasp := newFuzzRelay(t, RelayAuthConfig{Now: func() time.Time { return fuzzRelayNow }})
		dbefore := snapshotFuzzRelay(dbp, dasp)
		def.processRemote(data, fuzzRelaySelf)
		if c := fuzzStateChanges(t, dbefore, snapshotFuzzRelay(dbp, dasp)); len(c) != 0 {
			t.Fatalf("a relay with no trusted keys changed %v", c)
		}
		switch ds := def.Stats(); {
		case !decoded && ds != (RelayStats{Malformed: 1}),
			self && ds != (RelayStats{}),
			decoded && !self && ds != (RelayStats{NoTrustedKeys: 1}):
			t.Fatalf("default relay counted %+v (decoded %v, self-origin %v)", ds, decoded, self)
		}

		r, bp, asp := newFuzzRelay(t, RelayAuthConfig{TrustedKeys: [][]byte{trusted.pub}, Now: func() time.Time { return fuzzRelayNow }})
		before := snapshotFuzzRelay(bp, asp)
		r.processRemote(data, fuzzRelaySelf)
		after := snapshotFuzzRelay(bp, asp)
		changed := fuzzStateChanges(t, before, after)
		st := r.Stats()

		if !decoded || self {
			want := RelayStats{} // self-origin: skipped without a count
			if !decoded {
				want.Malformed = 1
			}
			if len(changed) != 0 || st != want {
				t.Fatalf("undecodable or self-origin message: changed %v, stats %+v", changed, st)
			}
			return
		}
		if fuzzStatsTotal(st) != 1 {
			t.Fatalf("one message was counted %d times: %+v", fuzzStatsTotal(st), st)
		}

		// The authentication step accepts exactly the authentic events.
		authentic := fuzzAuthentic(&evt, trusted)
		check := evt
		if err := r.auth.verify(&check); (err == nil) != authentic {
			t.Fatalf("verify returned %v, reference says authentic=%v", err, authentic)
		}
		accepted := st.Applied+st.NoOp+st.BadTransition == 1
		if accepted != authentic {
			t.Fatalf("authentic=%v but stats %+v", authentic, st)
		}

		// Nothing changes unless an authentic event was applied, and an
		// applied event makes exactly one legal change.
		if len(changed) > 0 && !authentic {
			t.Fatalf("state changed (%v) by an event without a valid trusted signature in the window: %+v", changed, evt)
		}
		if (st.Applied == 1) != (len(changed) == 1) || len(changed) > 1 {
			t.Fatalf("applied=%d but changed %v", st.Applied, changed)
		}
		if len(changed) == 1 {
			fuzzCheckTransition(t, evt.Kind, changed[0], before, after)
		}

		switch mode % 3 {
		case 1:
			// Signed by the trusted key: accepted inside the window, stale
			// outside it, never refused for its signature.
			if fuzzFresh(evt.Timestamp) != accepted || !accepted && st.Stale != 1 {
				t.Fatalf("event signed by the trusted key (ts %q) counted %+v", evt.Timestamp, st)
			}
		case 2:
			if st.UntrustedSigner+st.BadSignature != 1 {
				t.Fatalf("event signed by an untrusted key counted %+v", st)
			}
		}
	})
}
