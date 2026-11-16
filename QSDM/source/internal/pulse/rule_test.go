package pulse

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func addr(i int) Address { return Address(fmt.Sprintf("%064x", i)) }

func all(Address) bool { return true }

func noCap(Address) uint64 { return ^uint64(0) }

// The pool (qsdm-mining-bootstrap) wrote testdata/conformance.json: the node
// must decode the same body, verify the signature and pay the same split.
func TestConformanceWithPool(t *testing.T) {
	raw, err := os.ReadFile("testdata/conformance.json")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		PublicKey string             `json:"public_key"`
		PoolID    Address            `json:"pool_id"`
		Body      string             `json:"body"`
		Sig       string             `json:"signature"`
		Pooled    uint64             `json:"pooled"`
		Owed      map[Address]uint64 `json:"owed"`
		CapLeft   map[Address]uint64 `json:"cap_left"`
		Payouts   []Payout           `json:"payouts"`
		OwedAfter map[Address]uint64 `json:"owed_after"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	body, _ := hex.DecodeString(c.Body)
	sig, _ := hex.DecodeString(c.Sig)
	r := &Rule{Params: Params{ActivationHeight: 1_230_000, MaxFeeBPS: 10000}, Manifest: []PoolEntry{{ID: c.PoolID, PublicKey: c.PublicKey, MaxFeeBPS: 500}}}
	st := NewMemState()
	st.owed[c.PoolID] = c.Owed // carried balances from earlier blocks
	b, err := r.Verify(body, sig, 1_230_005, st)
	if err != nil {
		t.Fatal(err)
	}
	half := c.Pooled / 2
	claims := []Claim{{"lmp1-aa", addr(1), half}, {"lmp1-bb", addr(2), c.Pooled - half}, {"other", addr(50), 7}}
	capLeft := func(a Address) uint64 {
		if v, ok := c.CapLeft[a]; ok {
			return v
		}
		return ^uint64(0)
	}
	pays, rest, err := r.Apply(b, claims, all, capLeft, st)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pays, c.Payouts) {
		t.Fatalf("payouts differ from the pool's:\n%v\n%v", pays, c.Payouts)
	}
	if !reflect.DeepEqual(st.Owed(c.PoolID), c.OwedAfter) {
		t.Fatalf("carried balances differ: %v vs %v", st.Owed(c.PoolID), c.OwedAfter)
	}
	if len(rest) != 1 || rest[0].ProofID != "other" {
		t.Fatalf("unlisted claims: %v", rest)
	}
	// The same batch again is a replay.
	if _, err := r.Verify(body, sig, 1_230_005, st); !errors.Is(err, ErrReplay) {
		t.Fatalf("replay: %v", err)
	}
}

type fixture struct {
	k  *KeyPair
	r  *Rule
	st *MemState
}

func newFixture(t *testing.T) *fixture {
	k, err := NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{k: k, st: NewMemState(), r: &Rule{Params: Params{ActivationHeight: 100, MaxFeeBPS: 300},
		Manifest: []PoolEntry{{ID: k.ID(), PublicKey: hex.EncodeToString(k.Pub.Bytes()), MaxFeeBPS: 200}}, Metrics: &Metrics{}}}
}

func (f *fixture) batch(seq, h uint64, fee uint16, proofs ...string) *Batch {
	return &Batch{Version: BatchVersion, PoolID: f.k.ID(), Seq: seq, Height: h, FeeBPS: fee, FeeTo: addr(77), FloorDust: 0,
		ProofIDs: proofs, Weights: []Weight{{addr(1), 1}, {addr(2), 1}}}
}

func (f *fixture) sign(t *testing.T, b *Batch) ([]byte, []byte) {
	sb, err := SignBatch(f.k, b)
	if err != nil {
		t.Fatal(err)
	}
	return sb.Body, sb.Sig
}

func TestVerifyRefusals(t *testing.T) {
	f := newFixture(t)
	body, sig := f.sign(t, f.batch(1, 150, 100, "p1"))
	if _, err := f.r.Verify(body, sig, 99, f.st); !errors.Is(err, ErrInactive) {
		t.Fatal("verified before the activation height:", err)
	}
	off := &Rule{Manifest: f.r.Manifest}
	if _, err := off.Verify(body, sig, 1<<40, f.st); !errors.Is(err, ErrInactive) {
		t.Fatal("an unset height must mean never:", err)
	}
	if _, err := f.r.Verify(body, sig, 151, f.st); !errors.Is(err, ErrMismatch) {
		t.Fatal("wrong block height accepted:", err)
	}
	bad := append([]byte{}, sig...)
	bad[10] ^= 1
	if _, err := f.r.Verify(body, bad, 150, f.st); !errors.Is(err, ErrBadSignature) {
		t.Fatal("bad signature accepted:", err)
	}
	stranger, _ := NewKeyPair()
	sb, _ := SignBatch(stranger, &Batch{Version: 1, PoolID: stranger.ID(), Seq: 1, Height: 150, FeeTo: addr(77), ProofIDs: []string{"p"}, Weights: []Weight{{addr(1), 1}}})
	if _, err := f.r.Verify(sb.Body, sb.Sig, 150, f.st); !errors.Is(err, ErrUnknownPool) {
		t.Fatal("pool outside the manifest accepted:", err)
	}
	b2, s2 := f.sign(t, f.batch(1, 150, 201, "p1")) // above this pool's 200 bps ceiling
	if _, err := f.r.Verify(b2, s2, 150, f.st); !errors.Is(err, ErrMismatch) {
		t.Fatal("fee above the ceiling accepted:", err)
	}
	if _, err := f.r.Verify(body, sig, 150, f.st); err != nil {
		t.Fatal(err)
	}
}

func TestApplyFailClosed(t *testing.T) {
	f := newFixture(t)
	b := f.batch(1, 150, 0, "p1", "p2")
	cl := []Claim{{"p1", addr(1), 100}, {"p2", addr(2), 100}, {"solo", addr(9), 50}}
	if _, _, err := f.r.Apply(b, cl[:1], all, noCap, f.st); !errors.Is(err, ErrMismatch) {
		t.Fatal("a listed proof missing from the block was accepted:", err)
	}
	foreign := []Claim{{"p1", addr(1), 100}, {"p2", addr(9), 100}}
	if _, _, err := f.r.Apply(b, foreign, all, noCap, f.st); !errors.Is(err, ErrMismatch) {
		t.Fatal("a non-member's proof was pooled:", err)
	}
	if _, _, err := f.r.Apply(b, cl, func(Address) bool { return false }, noCap, f.st); !errors.Is(err, ErrMismatch) {
		t.Fatal("a window of unenrolled owners was paid:", err)
	}
	if f.st.LastSeq(f.k.ID()) != 0 {
		t.Fatal("a refused batch changed state")
	}
	only1 := func(a Address) bool { return a == addr(1) }
	pays, rest, err := f.r.Apply(b, cl, only1, noCap, f.st)
	if err != nil {
		t.Fatal(err)
	}
	if len(pays) != 1 || pays[0].To != addr(1) || pays[0].Amount != 200 || len(rest) != 1 || rest[0].ProofID != "solo" {
		t.Fatalf("unenrolled weight not dropped: %v %v", pays, rest)
	}
	if !f.st.ProofPooled("p1") || f.st.LastSeq(f.k.ID()) != 1 || f.r.Metrics.Applied.Load() != 1 {
		t.Fatal("state not committed")
	}
	if _, _, err := f.r.Apply(f.batch(2, 151, 0, "p1"), []Claim{{"p1", addr(1), 1}}, all, noCap, f.st); err == nil {
		t.Fatal("a proof was pooled twice")
	}
}

func TestPoolIDMustBeItsKey(t *testing.T) {
	f := newFixture(t)
	other, _ := NewKeyPair()
	// A manifest entry that names the pool ID but carries another key: a batch
	// signed by that other key under the pool's ID must not pass.
	f.r.Manifest[0].PublicKey = hex.EncodeToString(other.Pub.Bytes())
	b := f.batch(1, 150, 0, "p1")
	body, _ := b.Encode()
	sig, _ := other.Sign(BatchDomain, body)
	if _, err := f.r.Verify(body, sig, 150, f.st); !errors.Is(err, ErrUnknownPool) {
		t.Fatal("a key that is not the pool ID's was accepted:", err)
	}
}

func TestOldSequenceRefused(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.r.Apply(f.batch(5, 150, 0, "p1"), []Claim{{"p1", addr(1), 10}}, all, noCap, f.st); err != nil {
		t.Fatal(err)
	}
	body, sig := f.sign(t, f.batch(5, 151, 0, "fresh"))
	if _, err := f.r.Verify(body, sig, 151, f.st); !errors.Is(err, ErrReplay) {
		t.Fatal("an old sequence with fresh proofs was accepted:", err)
	}
	body, sig = f.sign(t, f.batch(6, 151, 0, "fresh"))
	if _, err := f.r.Verify(body, sig, 151, f.st); err != nil {
		t.Fatal(err)
	}
}

func TestFeeCountsAgainstFeeAddressCap(t *testing.T) {
	f := newFixture(t)
	b := f.batch(1, 150, 100, "p1")
	pays, _, err := f.r.Apply(b, []Claim{{"p1", addr(1), 10_000}}, all, func(a Address) uint64 {
		if a == addr(77) {
			return 30
		}
		return ^uint64(0)
	}, f.st)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pays {
		if p.To == addr(77) && p.Amount != 30 {
			t.Fatalf("fee recipient paid %d past its cap", p.Amount)
		}
	}
	if f.st.Owed(f.k.ID())[addr(77)] != 70 {
		t.Fatalf("fee above the cap not carried: %v", f.st.Owed(f.k.ID()))
	}
}
