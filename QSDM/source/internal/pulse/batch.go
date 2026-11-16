package pulse

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// BatchDomain separates batch signatures from every other QSDM signature.
const BatchDomain = "QSDM/pulse/batch/v1\x00"

// BatchVersion is the only version this code reads or writes.
const BatchVersion = 1

// Decoder bounds. They keep a hostile batch from exhausting memory; they are
// not policy (policy numbers are in the pool config and the chain manifest).
const (
	MaxBatchProofs  = 4096
	MaxBatchWeights = 4096
	MaxProofIDLen   = 128
)

// Weight is one member's share of the window, in difficulty units scaled by
// WeightScale (so the oldest share's fraction stays an integer).
type Weight struct {
	Owner  Address
	Weight uint64
}

// Batch is one block's settlement: which accepted network proofs of pool
// members are pooled, and how their reward is split. The chain pays it.
type Batch struct {
	Version   uint8
	PoolID    Address
	Seq       uint64  // strictly increasing per pool
	Height    uint64  // the block whose reward this settles
	FeeBPS    uint16  // pool fee in basis points (0..10000)
	FeeTo     Address // fee recipient; also receives dust
	FloorDust uint64  // payout floor in dust; smaller balances carry forward
	ProofIDs  []string
	Weights   []Weight // sorted by owner, owners unique, weights > 0
}

// Encode is the canonical body the pool signs. Decode(Encode(b)) == b.
func (b *Batch) Encode() ([]byte, error) {
	if err := b.check(); err != nil {
		return nil, err
	}
	var w bytes.Buffer
	w.WriteByte(b.Version)
	pid, _ := b.PoolID.Bytes()
	w.Write(pid[:])
	u64 := func(v uint64) { var t [8]byte; binary.BigEndian.PutUint64(t[:], v); w.Write(t[:]) }
	u64(b.Seq)
	u64(b.Height)
	var t2 [2]byte
	binary.BigEndian.PutUint16(t2[:], b.FeeBPS)
	w.Write(t2[:])
	fee, _ := b.FeeTo.Bytes()
	w.Write(fee[:])
	u64(b.FloorDust)
	var t4 [4]byte
	binary.BigEndian.PutUint32(t4[:], uint32(len(b.ProofIDs)))
	w.Write(t4[:])
	for _, id := range b.ProofIDs {
		binary.BigEndian.PutUint16(t2[:], uint16(len(id)))
		w.Write(t2[:])
		w.WriteString(id)
	}
	binary.BigEndian.PutUint32(t4[:], uint32(len(b.Weights)))
	w.Write(t4[:])
	for _, x := range b.Weights {
		o, _ := x.Owner.Bytes()
		w.Write(o[:])
		u64(x.Weight)
	}
	return w.Bytes(), nil
}

func (b *Batch) check() error {
	if b.Version != BatchVersion {
		return fmt.Errorf("pulse: batch version %d", b.Version)
	}
	if _, err := b.PoolID.Bytes(); err != nil {
		return errors.New("pulse: batch pool id")
	}
	if _, err := b.FeeTo.Bytes(); err != nil {
		return errors.New("pulse: batch fee recipient")
	}
	if b.FeeBPS > 10000 {
		return errors.New("pulse: fee above 10000 bps")
	}
	if len(b.ProofIDs) == 0 || len(b.ProofIDs) > MaxBatchProofs {
		return errors.New("pulse: batch proof count")
	}
	seen := map[string]bool{}
	for _, id := range b.ProofIDs {
		if len(id) == 0 || len(id) > MaxProofIDLen || seen[id] {
			return errors.New("pulse: batch proof id empty, too long or repeated")
		}
		seen[id] = true
	}
	if len(b.Weights) == 0 || len(b.Weights) > MaxBatchWeights {
		return errors.New("pulse: batch weight count")
	}
	for i, x := range b.Weights {
		if _, err := x.Owner.Bytes(); err != nil || x.Weight == 0 {
			return errors.New("pulse: batch weight owner or zero weight")
		}
		if i > 0 && b.Weights[i-1].Owner >= x.Owner {
			return errors.New("pulse: batch weights not sorted by owner or repeated")
		}
	}
	return nil
}

// DecodeBatch is strict: one encoding per batch, no trailing bytes.
func DecodeBatch(p []byte) (*Batch, error) {
	r := bytes.NewReader(p)
	need := func(n int) ([]byte, error) {
		if r.Len() < n {
			return nil, errors.New("pulse: batch truncated")
		}
		out := make([]byte, n)
		_, _ = r.Read(out)
		return out, nil
	}
	var b Batch
	v, err := need(1)
	if err != nil {
		return nil, err
	}
	b.Version = v[0]
	addr := func() (Address, error) {
		x, err := need(32)
		if err != nil {
			return "", err
		}
		return Address(fmt.Sprintf("%x", x)), nil
	}
	u64 := func() (uint64, error) {
		x, err := need(8)
		if err != nil {
			return 0, err
		}
		return binary.BigEndian.Uint64(x), nil
	}
	if b.PoolID, err = addr(); err != nil {
		return nil, err
	}
	if b.Seq, err = u64(); err != nil {
		return nil, err
	}
	if b.Height, err = u64(); err != nil {
		return nil, err
	}
	x, err := need(2)
	if err != nil {
		return nil, err
	}
	b.FeeBPS = binary.BigEndian.Uint16(x)
	if b.FeeTo, err = addr(); err != nil {
		return nil, err
	}
	if b.FloorDust, err = u64(); err != nil {
		return nil, err
	}
	x, err = need(4)
	if err != nil {
		return nil, err
	}
	np := binary.BigEndian.Uint32(x)
	if np == 0 || np > MaxBatchProofs {
		return nil, errors.New("pulse: batch proof count")
	}
	b.ProofIDs = make([]string, 0, np)
	for i := uint32(0); i < np; i++ {
		x, err = need(2)
		if err != nil {
			return nil, err
		}
		l := int(binary.BigEndian.Uint16(x))
		if l == 0 || l > MaxProofIDLen {
			return nil, errors.New("pulse: batch proof id length")
		}
		s, err := need(l)
		if err != nil {
			return nil, err
		}
		b.ProofIDs = append(b.ProofIDs, string(s))
	}
	x, err = need(4)
	if err != nil {
		return nil, err
	}
	nw := binary.BigEndian.Uint32(x)
	if nw == 0 || nw > MaxBatchWeights {
		return nil, errors.New("pulse: batch weight count")
	}
	b.Weights = make([]Weight, 0, nw)
	for i := uint32(0); i < nw; i++ {
		o, err := addr()
		if err != nil {
			return nil, err
		}
		wv, err := u64()
		if err != nil {
			return nil, err
		}
		b.Weights = append(b.Weights, Weight{Owner: o, Weight: wv})
	}
	if r.Len() != 0 {
		return nil, errors.New("pulse: trailing bytes after batch")
	}
	if err := b.check(); err != nil {
		return nil, err
	}
	return &b, nil
}

// SignedBatch is a batch body with the pool's ML-DSA-87 signature.
type SignedBatch struct {
	Body []byte
	Sig  []byte
}

// SignBatch encodes and signs b with the pool key; the key must be b.PoolID.
func SignBatch(k *KeyPair, b *Batch) (*SignedBatch, error) {
	if k.ID() != b.PoolID {
		return nil, errors.New("pulse: batch pool id is not this key")
	}
	body, err := b.Encode()
	if err != nil {
		return nil, err
	}
	sig, err := k.Sign(BatchDomain, body)
	if err != nil {
		return nil, err
	}
	return &SignedBatch{Body: body, Sig: sig}, nil
}

// SortWeights sorts weights by owner (the canonical order).
func SortWeights(ws []Weight) {
	sort.Slice(ws, func(i, j int) bool { return ws[i].Owner < ws[j].Owner })
}
