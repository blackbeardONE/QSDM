package chain

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// Fuzz targets for the consensus messages peers gossip: BFT votes and
// proposals, slashing evidence, POL lock proofs and round certificates, and
// blocks. Each harness decodes raw bytes the way the gossip ingress does.

// checkSignedBy asserts that an accepted authenticator is the claimed
// signer's key and that the reference ML-DSA-87 verifier agrees.
func checkSignedBy(t *testing.T, auth BFTWireAuth, digest []byte, claimed string) {
	t.Helper()
	if !strings.EqualFold(BFTValidatorAddress(auth.PublicKey), claimed) {
		t.Fatalf("accepted a key that derives %s, not the claimed signer %q",
			BFTValidatorAddress(auth.PublicKey), claimed)
	}
	if !fuzzVerifyMLDSA(auth.PublicKey, digest, auth.Signature) {
		t.Fatal("accepted a signature the reference ML-DSA-87 verifier rejects")
	}
}

// fuzzJSON is a snapshot for "the verifier did not modify its input" checks
// on types whose every field is serialised.
func fuzzJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("unencodable: " + err.Error())
	}
	return b
}

func FuzzVerifyBFTWire(f *testing.F) {
	signer, addr := newBFTKey(f)
	wire := func(kind string, msg any) []byte {
		b, err := MarshalBFTWire(kind, msg)
		if err != nil {
			f.Fatal(err)
		}
		return b
	}
	body := &Block{Height: 5, PrevHash: "prev-4", StateRoot: "root-5", ProducerID: addr,
		Transactions: []*mempool.Tx{{ID: "tx-1", Sender: "a", Amount: 1}, {ID: "tx-2"}}}
	body.Hash = computeBlockHash(body)
	withBody := BFTWireProposeMsg{Height: 5, Round: 1, Proposer: addr, BlockHash: "root-5", Block: body}
	if err := SignPropose(&withBody, signer); err != nil {
		f.Fatal(err)
	}
	header := BFTWireProposeMsg{Height: 6, Round: 0, Proposer: addr, BlockHash: "root-6"}
	if err := SignPropose(&header, signer); err != nil {
		f.Fatal(err)
	}
	prevote := BFTWirePrevoteMsg{Height: 7, Round: 2, Validator: addr, BlockHash: "root-7"}
	if err := SignPrevote(&prevote, signer); err != nil {
		f.Fatal(err)
	}
	precommit := BFTWirePrecommitMsg{Height: 7, Round: 2, Validator: addr, BlockHash: NilVoteHash}
	if err := SignPrecommit(&precommit, signer); err != nil {
		f.Fatal(err)
	}
	for _, genuine := range [][]byte{
		wire(BFTWirePropose, withBody), wire(BFTWirePropose, header),
		wire(BFTWirePrevote, prevote), wire(BFTWirePrecommit, precommit),
	} {
		if err := (&BFTExecutor{}).ValidateInboundAuthentication(genuine); err != nil {
			f.Fatalf("genuine seed rejected: %v", err)
		}
	}
	for _, seed := range [][]byte{
		wire(BFTWirePropose, withBody),
		wire(BFTWirePropose, header),
		wire(BFTWirePrevote, prevote),
		wire(BFTWirePrecommit, precommit),
		wire(BFTWirePrecommit, prevote), // a prevote replayed as a precommit
		wire(BFTWirePrevote, BFTWirePrevoteMsg{Height: 1, Validator: "v1", BlockHash: "h"}),
		[]byte(`{"kind":"bft_propose","payload":{"height":3,"block_hash":"r","block":{"height":3,"state_root":"r","transactions":[]}}}`),
		[]byte(`{"kind":"bft_propose","payload":{"height":3,"block_hash":"r","block":{"height":3,"state_root":"r","transactions":null}}}`),
		[]byte(`{"kind":"bft_vote","payload":{}}`),
		[]byte(`{"kind":"bft_prevote","payload":null}`),
		[]byte(`{"kind":"","payload":{}}`),
	} {
		f.Add(seed, false, uint64(0))
		f.Add(seed, true, uint64(7))
	}

	f.Fuzz(func(t *testing.T, raw []byte, require bool, activation uint64) {
		e := &BFTExecutor{}
		e.SetRequireSignedVotes(require)
		e.SetSignedVoteActivationHeight(activation)
		if err := e.ValidateInboundAuthentication(raw); err != nil {
			return
		}
		kind, payload, err := UnmarshalBFTWire(raw)
		if err != nil {
			t.Fatalf("accepted an undecodable envelope: %v", err)
		}
		unsignedOK := func(height uint64) {
			if require && (activation == 0 || height >= activation) {
				t.Fatalf("accepted an unsigned %s at height %d (activation %d)", kind, height, activation)
			}
		}
		switch kind {
		case BFTWirePropose:
			var m BFTWireProposeMsg
			if err := json.Unmarshal(payload, &m); err != nil {
				t.Fatalf("accepted an undecodable propose: %v", err)
			}
			if b := m.Block; b != nil && (b.Height != m.Height || b.StateRoot != m.BlockHash || b.Hash != computeBlockHash(b)) {
				t.Fatalf("accepted a propose whose block body does not match it: %+v", m)
			}
			if !m.Auth.Signed() {
				unsignedOK(m.Height)
				return
			}
			checkSignedBy(t, m.Auth, bftVoteDigest(BFTWirePropose, m.Height, m.Round, m.Proposer, m.BlockHash,
				proposeBodyHash(m.Block)), m.Proposer)
			later, other := m, m
			later.Height++
			other.Round++
			asVote := BFTWirePrevoteMsg{Height: m.Height, Round: m.Round, Validator: m.Proposer, BlockHash: m.BlockHash, Auth: m.Auth}
			if VerifyPropose(later) == nil || VerifyPropose(other) == nil || VerifyPrevote(asVote) == nil {
				t.Fatal("a signed propose verified at another height or round, or as a prevote")
			}
		case BFTWirePrevote, BFTWirePrecommit:
			var m BFTWirePrevoteMsg // the two vote kinds share one shape
			if err := json.Unmarshal(payload, &m); err != nil {
				t.Fatalf("accepted an undecodable vote: %v", err)
			}
			if !m.Auth.Signed() {
				unsignedOK(m.Height)
				return
			}
			checkSignedBy(t, m.Auth, bftVoteDigest(kind, m.Height, m.Round, m.Validator, m.BlockHash, ""), m.Validator)
			later, other := m, m
			later.Height++
			other.Round++
			verify := func(kind string, v BFTWirePrevoteMsg) error {
				if kind == BFTWirePrevote {
					return VerifyPrevote(v)
				}
				return VerifyPrecommit(BFTWirePrecommitMsg(v))
			}
			otherKind := BFTWirePrecommit
			if kind == BFTWirePrecommit {
				otherKind = BFTWirePrevote
			}
			if verify(kind, later) == nil || verify(kind, other) == nil || verify(otherKind, m) == nil {
				t.Fatalf("a signed %s verified at another height or round, or as the other vote kind", kind)
			}
		default:
			t.Fatalf("accepted unknown kind %q", kind)
		}
	})
}

func FuzzValidateUntrustedEvidence(f *testing.F) {
	signer, addr := newBFTKey(f)
	evidence := func(kind string, h uint64, r uint32) ConsensusEvidence {
		ev, err := BuildEquivocationEvidence(addr,
			signedExhibit(f, signer, addr, kind, h, r, "value-a"),
			signedExhibit(f, signer, addr, kind, h, r, "value-b"))
		if err != nil {
			f.Fatal(err)
		}
		if err := ValidateUntrustedEvidence(ev); err != nil {
			f.Fatalf("genuine seed rejected: %v", err)
		}
		ev.Timestamp = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		return ev
	}
	add := func(ev ConsensusEvidence) {
		b, err := json.Marshal(ev)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	prevotes := evidence(BFTWirePrevote, 9, 1)
	add(prevotes)
	add(evidence(BFTWirePrecommit, 10, 0))
	add(evidence(BFTWirePropose, 12, 2))
	swapped := prevotes
	swapped.Proof = &EquivocationProof{VoteA: prevotes.Proof.VoteB, VoteB: prevotes.Proof.VoteA}
	swapped.BlockHashes = []string{"value-b", "value-a"}
	add(swapped)
	shifted := prevotes
	shifted.Height++ // the envelope overstates what the proof shows
	add(shifted)
	upper := prevotes
	upper.Validator = strings.ToUpper(upper.Validator)
	add(upper)
	proofless := prevotes
	proofless.Proof = nil
	add(proofless)
	add(ConsensusEvidence{Type: EvidenceForkWitness, Height: 3, BlockHashes: []string{"a", "b"}, Details: "fork"})
	add(ConsensusEvidence{Type: EvidenceInvalidVote, Validator: addr, Height: 3, Details: "x"})
	add(ConsensusEvidence{Type: "double_sign", Validator: addr})
	f.Add([]byte(`{"type":"equivocation","proof":{}}`))
	f.Add([]byte(`{"type":"equivocation","proof":null}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		var ev ConsensusEvidence
		if json.Unmarshal(raw, &ev) != nil {
			return
		}
		before := fuzzJSON(ev)
		err := ValidateUntrustedEvidence(ev)
		if !bytes.Equal(before, fuzzJSON(ev)) {
			t.Fatal("ValidateUntrustedEvidence modified its input")
		}
		if err != nil {
			return
		}
		switch ev.Type {
		case EvidenceForkWitness:
			return // slashes nobody, so carries nothing to prove
		case EvidenceEquivocation:
		default:
			t.Fatalf("accepted evidence of type %q from a peer", ev.Type)
		}
		p := ev.Proof
		if p == nil {
			t.Fatal("accepted equivocation evidence without a proof")
		}
		a, b := p.VoteA, p.VoteB
		if ev.Validator != strings.ToLower(ev.Validator) || a.Validator != ev.Validator || b.Validator != ev.Validator {
			t.Fatalf("accused %q is not canonical or not the exhibits' signer (%q, %q)", ev.Validator, a.Validator, b.Validator)
		}
		if a.Kind != b.Kind || a.Height != b.Height || a.Round != b.Round || a.BlockHash == b.BlockHash ||
			ev.Height != a.Height || ev.Round != a.Round {
			t.Fatalf("accepted exhibits that do not show one equivocation at the envelope's height and round: %+v", ev)
		}
		got := append([]string(nil), ev.BlockHashes...)
		sort.Strings(got)
		want := []string{a.BlockHash, b.BlockHash}
		sort.Strings(want)
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") || len(got) != 2 {
			t.Fatalf("envelope hashes %q are not the signed values %q", ev.BlockHashes, want)
		}
		for _, x := range []SignedVoteExhibit{a, b} {
			checkSignedBy(t, x.Auth, bftVoteDigest(x.Kind, x.Height, x.Round, x.Validator, x.BlockHash, x.BodyHash), ev.Validator)
		}
		// The gossip gate never admits what the evidence manager would refuse.
		if err := validateEvidence(ev); err != nil {
			t.Fatalf("untrusted gate accepted evidence validateEvidence rejects: %v", err)
		}
		// Exhibit and hash order are not part of the offence.
		swapped := ev
		swapped.Proof = &EquivocationProof{VoteA: b, VoteB: a}
		swapped.BlockHashes = []string{ev.BlockHashes[1], ev.BlockHashes[0]}
		if err := ValidateUntrustedEvidence(swapped); err != nil {
			t.Fatalf("the same offence with its exhibits swapped was refused: %v", err)
		}
		if StableEvidenceID(swapped) != StableEvidenceID(ev) {
			t.Fatal("swapping the exhibits changed the evidence id")
		}
	})
}

// setCertificatePolicy installs the signed-certificate policy for one input
// and returns a func restoring the previous one.
func setCertificatePolicy(require bool, activation uint64) func() {
	prevRequire, prevActivation := RequireSignedCertificates(), SignedCertificateActivationHeight()
	SetRequireSignedCertificates(require)
	SetSignedCertificateActivationHeight(activation)
	return func() {
		SetRequireSignedCertificates(prevRequire)
		SetSignedCertificateActivationHeight(prevActivation)
	}
}

func FuzzVerifyPrevoteLockProof(f *testing.F) {
	signer, _ := newBFTKey(f)
	proof := &PrevoteLockProof{
		Height: 5, Round: 2, LockedBlockHash: "locked", CarriedFromLock: "earlier",
		Prevotes: []BlockVote{
			{Validator: "v1", BlockHash: "locked", Height: 5, Round: 2},
			{Validator: "v2", BlockHash: "locked", Height: 5, Round: 2},
			{Validator: "v3", BlockHash: NilVoteHash, Height: 5, Round: 2},
		},
	}
	if err := SignPrevoteLockProof(proof, signer); err != nil {
		f.Fatal(err)
	}
	if err := VerifyPrevoteLockProof(proof); err != nil {
		f.Fatalf("genuine seed rejected: %v", err)
	}
	signed, err := EncodePrevoteLockProof(proof)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{
		signed,
		bytes.Replace(signed, []byte(`"v3"`), []byte(`"v4"`), 1),
		[]byte(`{"height":5,"round":2,"locked_block_hash":"locked"}`),
		[]byte(`{"height":5,"locked_block_hash":"locked","prevotes":[{"validator":"v1"},{"validator":"v1"}],"auth":{"public_key":"AA==","signature":"AA=="}}`),
		[]byte(`null`),
	} {
		f.Add(seed, false, uint64(0))
		f.Add(seed, true, uint64(6))
	}

	f.Fuzz(func(t *testing.T, raw []byte, require bool, activation uint64) {
		p, err := DecodePrevoteLockProof(raw)
		if err != nil || p == nil {
			return
		}
		defer setCertificatePolicy(require, activation)()
		before := fuzzJSON(p)
		err = VerifyPrevoteLockProof(p)
		if !bytes.Equal(before, fuzzJSON(p)) {
			t.Fatal("VerifyPrevoteLockProof modified its input")
		}
		if !p.Auth.Signed() {
			if SignedCertificatesRequiredAt(p.Height) != errors.Is(err, ErrCertUnsigned) {
				t.Fatalf("unsigned proof at height %d (require %v, activation %d): %v", p.Height, require, activation, err)
			}
			return
		}
		// A present signature is judged on its own; the policy plays no part.
		SetRequireSignedCertificates(!require)
		if again := VerifyPrevoteLockProof(p); (again == nil) != (err == nil) {
			t.Fatalf("signed verdict depends on the policy: %v vs %v", err, again)
		}
		if err != nil {
			if !errors.Is(err, ErrCertBadSignature) {
				t.Fatalf("rejection does not wrap ErrCertBadSignature: %v", err)
			}
			return
		}
		checkSignedBy(t, p.Auth, polProofDigest(p), p.Signer)
		// The signature covers the set of prevotes, not their order. Votes
		// that tie on validator and block hash keep their input order in the
		// pre-image (sort.Slice is not stable), so only reorder when none do.
		seen := map[[2]string]bool{}
		for _, v := range p.Prevotes {
			seen[[2]string{v.Validator, v.BlockHash}] = true
		}
		if len(seen) == len(p.Prevotes) && len(p.Prevotes) > 1 {
			reordered := *p
			reordered.Prevotes = append([]BlockVote(nil), p.Prevotes...)
			for i, j := 0, len(reordered.Prevotes)-1; i < j; i, j = i+1, j-1 {
				reordered.Prevotes[i], reordered.Prevotes[j] = reordered.Prevotes[j], reordered.Prevotes[i]
			}
			if err := VerifyPrevoteLockProof(&reordered); err != nil {
				t.Fatalf("reordering the prevotes broke a valid proof: %v", err)
			}
		}
		injected := *p
		injected.Prevotes = append(append([]BlockVote(nil), p.Prevotes...), BlockVote{Validator: "injected", BlockHash: p.LockedBlockHash})
		if VerifyPrevoteLockProof(&injected) == nil {
			t.Fatal("a proof verified with an injected prevote")
		}
	})
}

func FuzzVerifyRoundCertificate(f *testing.F) {
	signer, addr := newBFTKey(f)
	cert := &RoundCertificate{
		Height: 12, Round: 1, Proposer: addr, BlockHash: "bh", CommitDigest: "cd",
		ValidatorSet: []string{"v1", "v2", "v3"}, CommitCount: 3, NilCommitCount: 1,
	}
	if err := SignRoundCertificate(cert, signer); err != nil {
		f.Fatal(err)
	}
	if err := VerifyRoundCertificate(cert); err != nil {
		f.Fatalf("genuine seed rejected: %v", err)
	}
	signed, err := json.Marshal(cert)
	if err != nil {
		f.Fatal(err)
	}
	for _, seed := range [][]byte{
		signed,
		bytes.Replace(signed, []byte(`"commit_count":3`), []byte(`"commit_count":4`), 1),
		[]byte(`{"height":12,"commit_digest":"cd","validators":["v2","v1"]}`),
		[]byte(`{"height":12,"signer":"x","auth":{"public_key":"AA==","signature":"AA=="}}`),
	} {
		f.Add(seed, false, uint64(0))
		f.Add(seed, true, uint64(13))
	}

	f.Fuzz(func(t *testing.T, raw []byte, require bool, activation uint64) {
		var c RoundCertificate
		if json.Unmarshal(raw, &c) != nil {
			return
		}
		defer setCertificatePolicy(require, activation)()
		before := fuzzJSON(c)
		err := VerifyRoundCertificate(&c)
		if !bytes.Equal(before, fuzzJSON(c)) {
			t.Fatal("VerifyRoundCertificate modified its input")
		}
		if !c.Auth.Signed() {
			if SignedCertificatesRequiredAt(c.Height) != errors.Is(err, ErrCertUnsigned) {
				t.Fatalf("unsigned certificate at height %d (require %v, activation %d): %v", c.Height, require, activation, err)
			}
			return
		}
		SetRequireSignedCertificates(!require)
		if again := VerifyRoundCertificate(&c); (again == nil) != (err == nil) {
			t.Fatalf("signed verdict depends on the policy: %v vs %v", err, again)
		}
		if err != nil {
			if !errors.Is(err, ErrCertBadSignature) {
				t.Fatalf("rejection does not wrap ErrCertBadSignature: %v", err)
			}
			return
		}
		checkSignedBy(t, c.Auth, roundCertDigest(&c), c.Signer)
		// The validator set is covered as a set: order is not significant,
		// membership is.
		reordered := c
		reordered.ValidatorSet = append([]string(nil), c.ValidatorSet...)
		sort.Sort(sort.Reverse(sort.StringSlice(reordered.ValidatorSet)))
		if err := VerifyRoundCertificate(&reordered); err != nil {
			t.Fatalf("reordering the validator set broke a valid certificate: %v", err)
		}
		grown := c
		grown.ValidatorSet = append(append([]string(nil), c.ValidatorSet...), "extra-validator")
		if VerifyRoundCertificate(&grown) == nil {
			t.Fatal("a certificate verified against a different validator set")
		}
	})
}

// setBlockPolicy installs the signed-block policy for one input and returns
// a func restoring the previous one.
func setBlockPolicy(require bool, activation uint64) func() {
	prevRequire, prevActivation := RequireSignedBlocks(), SignedBlockActivationHeight()
	SetRequireSignedBlocks(require)
	SetSignedBlockActivationHeight(activation)
	return func() {
		SetRequireSignedBlocks(prevRequire)
		SetSignedBlockActivationHeight(prevActivation)
	}
}

// FuzzVerifyBlockSignature decodes a block the way block gossip does
// (propagation.go) and runs both the producer-signature check and the gossip
// gate around it.
func FuzzVerifyBlockSignature(f *testing.F) {
	signer, addr := newBFTKey(f)
	block := &Block{
		Height: 4, PrevHash: "prev-3", StateRoot: "sr-4", ProducerID: addr,
		Timestamp:    time.Date(2026, 8, 1, 0, 0, 4, 0, time.UTC),
		Transactions: []*mempool.Tx{{ID: "tx-1", Sender: "alice", Recipient: "bob", Amount: 1.5, Nonce: 1}, {ID: "tx-2"}},
	}
	block.Hash = computeBlockHash(block)
	if err := SignBlock(block, signer); err != nil {
		f.Fatal(err)
	}
	genesis := &Block{Height: 0, StateRoot: "sr-0", ProducerID: addr}
	genesis.Hash = computeBlockHash(genesis)
	if err := SignBlock(genesis, signer); err != nil {
		f.Fatal(err)
	}
	unsigned := &Block{Height: 9, PrevHash: "prev-8", StateRoot: "sr-9", ProducerID: "producer"}
	unsigned.Hash = computeBlockHash(unsigned)
	for _, b := range []*Block{block, genesis, unsigned} {
		if err := VerifyBlockSignature(b); err != nil {
			f.Fatalf("genuine seed rejected: %v", err)
		}
		raw, err := json.Marshal(b)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw, false, uint64(0))
		f.Add(raw, true, uint64(5))
	}
	f.Add([]byte(`{"height":1,"hash":"h","transactions":[]}`), false, uint64(0))
	f.Add([]byte(`{"height":1,"hash":"h","prev_hash":"p","transactions":null,"producer_auth":{"public_key":"AA=="}}`), true, uint64(0))

	f.Fuzz(func(t *testing.T, raw []byte, require bool, activation uint64) {
		var b Block
		if json.Unmarshal(raw, &b) != nil {
			return
		}
		defer setBlockPolicy(require, activation)()
		before := fuzzJSON(&b)
		err := VerifyBlockSignature(&b)
		gate := (&BlockPropagator{}).validateBlock(&b)
		if !bytes.Equal(before, fuzzJSON(&b)) {
			t.Fatal("block verification modified its input")
		}
		hashOK := b.Hash != "" && computeBlockHash(&b) == b.Hash
		if want := hashOK && !(b.Height > 0 && b.PrevHash == "") && err == nil; gate != want {
			t.Fatalf("gossip gate says %v, hash and signature checks say %v (%v)", gate, want, err)
		}
		if !b.ProducerAuth.Signed() {
			if SignedBlocksRequiredAt(b.Height) != errors.Is(err, ErrBlockUnsigned) {
				t.Fatalf("unsigned block at height %d (require %v, activation %d): %v", b.Height, require, activation, err)
			}
			return
		}
		if err != nil {
			if !errors.Is(err, ErrBlockBadSignature) {
				t.Fatalf("rejection does not wrap ErrBlockBadSignature: %v", err)
			}
			return
		}
		if !hashOK {
			t.Fatal("accepted a signed block whose hash does not match its contents")
		}
		checkSignedBy(t, b.ProducerAuth, blockSigDigest(&b), b.ProducerID)
		moved := b
		moved.StateRoot += "x"
		if VerifyBlockSignature(&moved) == nil {
			t.Fatal("a signed block verified after its state root changed")
		}
	})
}
