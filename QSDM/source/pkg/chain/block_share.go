package chain

import "bytes"

// Values repeated across blocks share one copy in memory.
//
// A node holds every block of its chain in memory, and decoding gives every
// block its own copy of every value. Much of that is repetition: a producer
// signs every block of its epoch with the same 2,592-byte ML-DSA-87 public key
// (producer_auth), a block's prev_hash is its parent's hash, and producer IDs
// and transaction senders, recipients, contract IDs and public keys come from
// small sets. At height 880,376 of the public chain the copies of the two
// producer keys alone took about 1 GB.
//
// These helpers only ever replace a value by another one that is byte for
// byte equal to it, so block hashes, signature checks, state roots and the
// JSON a node serves or writes are unchanged. Strings are immutable. A shared
// public key must never be written in place: nothing in this repository
// writes into a producer_auth public key (it is hashed, compared, encoded and
// passed to ML-DSA-87 verification, which only reads it), and the shared
// slice's capacity equals its length, so appending to it copies instead of
// writing into the shared array.

// blockSharing deduplicates the values of the blocks of one journal load
// (LoadChainNDJSON). Its tables hold one entry per distinct value and go away
// with it; the blocks keep the shared values.
type blockSharing struct {
	strs   map[string]string
	keys   map[string][]byte
	parent *Block // the previous block of the load
}

func newBlockSharing() *blockSharing {
	return &blockSharing{strs: map[string]string{}, keys: map[string][]byte{}}
}

// str returns the first string of this load that equals v.
func (s *blockSharing) str(v string) string {
	if v == "" {
		return v
	}
	if c, ok := s.strs[v]; ok {
		return c
	}
	s.strs[v] = v
	return v
}

// add makes b share the values it has in common with the blocks loaded before
// it, then records b as the parent of the next block.
func (s *blockSharing) add(b *Block) {
	if b == nil {
		return
	}
	if s.parent != nil && b.PrevHash == s.parent.Hash {
		b.PrevHash = s.parent.Hash
	}
	b.ProducerID = s.str(b.ProducerID)
	if pk := b.ProducerAuth.PublicKey; len(pk) > 0 {
		k, ok := s.keys[string(pk)]
		if !ok {
			k = pk[:len(pk):len(pk)]
			s.keys[string(k)] = k
		}
		b.ProducerAuth.PublicKey = k
	}
	for _, tx := range b.Transactions {
		if tx == nil {
			continue
		}
		tx.Sender = s.str(tx.Sender)
		tx.Recipient = s.str(tx.Recipient)
		tx.ContractID = s.str(tx.ContractID)
		tx.PublicKey = s.str(tx.PublicKey)
	}
	s.parent = b
}

// shareWithParent makes b, which is about to be appended after parent (nil
// for the first block), hold parent's copy of what the two have in common: the
// parent hash, the producer ID and the producer's public key. b's key is left
// with a capacity equal to its length either way, so that appending to it
// never writes into an array its descendants share.
func shareWithParent(b, parent *Block) {
	if b == nil {
		return
	}
	pk := b.ProducerAuth.PublicKey
	if parent != nil {
		if b.PrevHash == parent.Hash {
			b.PrevHash = parent.Hash
		}
		if b.ProducerID == parent.ProducerID {
			b.ProducerID = parent.ProducerID
		}
		if ppk := parent.ProducerAuth.PublicKey; len(pk) > 0 && bytes.Equal(pk, ppk) {
			pk = ppk
		}
	}
	if len(pk) > 0 {
		b.ProducerAuth.PublicKey = pk[:len(pk):len(pk)]
	}
}

// shareWithTipLocked runs shareWithParent for b, which is about to become the
// tip. bp.mu must be held.
func (bp *BlockProducer) shareWithTipLocked(b *Block) {
	var tip *Block
	if n := len(bp.chain); n > 0 {
		tip = bp.chain[n-1]
	}
	shareWithParent(b, tip)
}
