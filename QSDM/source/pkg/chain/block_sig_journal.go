package chain

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// Producer signatures kept in the journal.
//
// A node held every block of its chain in memory with its producer
// signature, 4,627 bytes of ML-DSA-87 in a 4,864-byte allocation. At height
// 880,376 of the public chain the signatures of its 397,811 signed blocks
// took 1.8 GiB of a 2.7 GiB heap, growing by about 0.45 GiB per 100,000
// blocks. No old signature is needed in memory once it has been verified:
// nothing verifies it again, and only the two paths that relay blocks to
// other nodes (GET /api/v1/chain/blocks and the P2P block_request) read it,
// and the journal (qsdm_chain.ndjson) has it.
//
// LoadVerifiedJournal verifies each block of the journal as it decodes it,
// once (verifyTransitionChainBlock: height, hash, link, producer schedule,
// ML-DSA-87 signature), and then drops the signature of every block but the
// last. What stays is the block's public key (one copy per producer, shared),
// the position of its journal line and the first 16 bytes of the signature's
// SHA-256 (strippedSignature, a 32-byte allocation). A block is never changed
// once LoadVerifiedJournal returns it: blocks are encoded outside the
// producer lock.
//
// The blocks a node appends while it runs lose their signatures the same
// way, one block behind the tip, once the journal holds them durably
// (KeepSignaturesInJournal, at the end of this file). They are not changed
// either: the chain gets a stripped copy in their place.
//
// A stripped block:
//   - is Signed (it is: the signature was verified and is in the journal);
//   - fails VerifyBlockSignature, and so every block check, with
//     ErrBlockSignatureStripped: it is never taken for unsigned or for
//     verified;
//   - refuses to encode as JSON (BFTWireAuth.MarshalJSON), so it can never be
//     relayed or written without its signature;
//   - is relayed by RelayBlocks as a copy that carries the signature read
//     back from its journal line, once the line is checked against it.
//
// Journal positions only move when the journal is rewritten, and no rewrite
// happens while a node holds stripped blocks: the node appends after the
// last line (ChainJournal.Append, the persistence hook's H2), and every
// rewrite is either a startup step of a node without a producer transition
// (the forked-duplicate canonicalization and the unmatched-tail truncation,
// both ReplaceChainFile), which strips nothing, or an offline tool that holds
// the validator state lock (hl1-tail, --hl1-tail-replay, --snapshot-import).
// Should the file change anyway, every read checks the line it gets and
// refuses rather than relay a wrong block.

// strippedSignature is where a verified, stripped producer signature is.
type strippedSignature struct {
	offset int64    // the block's journal line: its first byte
	length uint32   // and its length, without the line end
	digest [16]byte // the first 16 bytes of the signature's SHA-256
}

func signatureDigest(sig []byte) (d [16]byte) {
	sum := sha256.Sum256(sig)
	copy(d[:], sum[:])
	return d
}

// Stripped reports whether a is the producer_auth of a block whose verified
// signature is no longer in memory (LoadVerifiedJournal, or
// KeepSignaturesInJournal for an appended block). Such an authenticator is
// Signed, fails every verification with ErrBlockSignatureStripped and does
// not encode; RelayBlocks reads its signature back from the journal.
func (a BFTWireAuth) Stripped() bool { return a.stripped != nil }

// bftWireAuthFields is BFTWireAuth without its methods: its plain encoding.
type bftWireAuthFields BFTWireAuth

// MarshalJSON encodes a exactly as encoding/json encodes its fields, or
// refuses with ErrBlockSignatureStripped if a is stripped: a block is never
// relayed or written without its signature.
func (a BFTWireAuth) MarshalJSON() ([]byte, error) {
	if a.stripped != nil {
		return nil, ErrBlockSignatureStripped
	}
	return json.Marshal(bftWireAuthFields(a))
}

// journalLines is bufio.ScanLines that also records where each line starts.
type journalLines struct {
	next  int64 // the offset of the first byte not consumed yet
	start int64 // the offset of the line last returned
}

func (j *journalLines) split(data []byte, atEOF bool) (int, []byte, error) {
	advance, token, err := bufio.ScanLines(data, atEOF)
	if token != nil {
		j.start = j.next
	}
	j.next += int64(advance)
	return advance, token, err
}

// LoadVerifiedJournal is the startup load of a producer with a producer
// transition. It reads path as LoadChainNDJSON does (one block per line,
// blank lines skipped, lines up to 4 MiB, repeated values shared) and
// authenticates each block as it is decoded, exactly as
// ValidateProducerTransitionChain authenticates a loaded chain: the journal
// must start at genesis, be contiguous and linked, reach the final approved
// checkpoint, every block must satisfy the producer schedule, and every
// signature must verify. Each block hash is computed and each signature
// verified once, here.
//
// The lines are decoded and verified on every core (one worker per
// GOMAXPROCS); each block is then checked against the one before it in
// journal order, and the error is the first failure in journal order. At
// most journalReadAhead lines per worker are read ahead.
//
// Every verified signature except the last block's is then dropped from
// memory (see above). The last block keeps its signature, so that the boot
// step S4r can replay it through TryAppendExternalBlock. RestoreVerifiedChain
// installs the returned chain without verifying it again, and RelayBlocks
// reads the dropped signatures back from path.
//
// A missing file is a fresh chain: no blocks and no error. Any other failure
// returns no chain.
func (bp *BlockProducer) LoadVerifiedJournal(path string) (*VerifiedChain, error) {
	return bp.loadVerifiedJournal(path, runtime.GOMAXPROCS(0))
}

// journalItem is one journal line on its way through a journalPipeline.
type journalItem struct {
	index  uint64            // its block's index: the number of blocks before it
	lineno int               // its line number
	at     strippedSignature // its position, and its signature's digest once verified
	raw    []byte            // the line (a copy) until it is decoded
	blk    *Block
	err    error
	// CheckJournal only: whether blk's hash is its contents' hash, and what
	// checkTransitionBlockAuth found (nil when blk passed or was not checked).
	hashOK  bool
	authErr error
	done    chan struct{} // closed once the line's results are set
}

// journalReadAhead is how many lines per worker the reader of a
// journalPipeline may hold ahead of the line the caller waits for. With one
// line per worker, every line that took longer than the others (a block with
// transactions, a garbage collection) left the other workers idle. With eight,
// the startup load of a copy of the real journal (880,377 blocks, six workers,
// the restore harness in cmd/qsdm/restore_bench_test.go) took 25.0 seconds
// instead of 28.0 (mean of five runs each), at the same peak memory. Journal
// lines are about 10 KB, so this holds about half a megabyte (at most 4 MiB a
// line).
const journalReadAhead = 8

// journalPipeline reads a journal as LoadChainNDJSON does (one block per
// line, blank lines skipped, lines up to 4 MiB) and runs work on every line
// on its own goroutines. The reader queues each line, in journal order, on
// order and hands it to a free worker; the caller takes the lines from order
// and waits for each one's done, so at most journalReadAhead lines per worker
// are read ahead of the line the caller waits for.
type journalPipeline struct {
	order   chan *journalItem
	quit    chan struct{} // closed by stop: the reader and the workers return early
	wg      sync.WaitGroup
	scanErr error
}

// startJournalPipeline starts reading f with workers goroutines (at least
// one) that run work on each line. work sets the line's results; the line is
// then dropped and done closed.
func startJournalPipeline(f *os.File, workers int, work func(*journalItem)) *journalPipeline {
	if workers < 1 {
		workers = 1
	}
	ahead := workers * journalReadAhead
	p := &journalPipeline{order: make(chan *journalItem, ahead), quit: make(chan struct{})}
	jobs := make(chan *journalItem, ahead)
	p.wg.Add(1 + workers)
	go func() {
		defer p.wg.Done()
		defer close(jobs)
		defer close(p.order)
		var lines journalLines
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // LoadChainNDJSON's limits
		scanner.Split(lines.split)
		var index uint64
		lineno := 0
		for scanner.Scan() {
			lineno++
			raw := scanner.Bytes()
			if len(raw) == 0 {
				continue
			}
			it := &journalItem{index: index, lineno: lineno, raw: append([]byte(nil), raw...), done: make(chan struct{}),
				at: strippedSignature{offset: lines.start, length: uint32(len(raw))}}
			index++
			select {
			case p.order <- it:
			case <-p.quit:
				return
			}
			select {
			case jobs <- it:
			case <-p.quit:
				return
			}
		}
		p.scanErr = scanner.Err()
	}()
	for w := 0; w < workers; w++ {
		go func() {
			defer p.wg.Done()
			for it := range jobs {
				work(it)
				it.raw = nil
				close(it.done)
			}
		}()
	}
	return p
}

// stop ends the pipeline before the end of the journal. Call it once, and
// only instead of wait.
func (p *journalPipeline) stop() {
	close(p.quit)
	p.wg.Wait()
}

// wait waits for the reader and the workers once order is drained, and
// returns the reader's error.
func (p *journalPipeline) wait() error {
	p.wg.Wait()
	return p.scanErr
}

func (bp *BlockProducer) loadVerifiedJournal(path string, workers int) (*VerifiedChain, error) {
	if bp == nil {
		return nil, errors.New("chain.LoadVerifiedJournal: nil producer")
	}
	if path == "" {
		return nil, errors.New("chain.LoadVerifiedJournal: empty path")
	}
	bp.mu.Lock()
	configured, epochs := bp.producerTransition != nil, bp.producerEpochs
	bp.mu.Unlock()
	if !configured {
		return nil, errors.New("chain.LoadVerifiedJournal: no producer transition is configured (load the journal with LoadChainNDJSON)")
	}
	v := &VerifiedChain{epochs: epochs, settings: currentVerifySettings(), journal: path}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return v, nil
		}
		return nil, fmt.Errorf("chain.LoadVerifiedJournal: open %s: %w", path, err)
	}
	defer f.Close()

	// One table of prepared producer keys for the whole load: each key is
	// unpacked once, not once per signature (producer_keys.go).
	keys := newProducerKeys()
	p := startJournalPipeline(f, workers, func(it *journalItem) {
		it.blk, it.err = decodeJournalLine(epochs, keys, path, it)
	})
	fail := func(err error) (*VerifiedChain, error) {
		p.stop()
		return nil, err
	}

	share := newBlockSharing()
	var held *Block // the last block verified: its signature is dropped when the next one verifies
	var heldAt strippedSignature
	for it := range p.order {
		<-it.done
		blk := it.blk
		if it.index == 0 && blk != nil && (blk.Height != 0 || blk.PrevHash != "") {
			return fail(errTransitionNoGenesis)
		}
		if it.err != nil {
			return fail(it.err)
		}
		if n := len(v.blocks); n > 0 && blk.PrevHash != v.blocks[n-1].Hash {
			return fail(fmt.Errorf("chain: producer transition broken linkage at height %d (%s line %d)", blk.Height, path, it.lineno))
		}
		share.add(blk)
		if held != nil {
			held.stripSignature(heldAt)
		}
		held, heldAt = blk, it.at
		v.blocks = append(v.blocks, blk)
	}
	if scanErr := p.wait(); scanErr != nil && !errors.Is(scanErr, io.EOF) {
		return nil, fmt.Errorf("chain.LoadVerifiedJournal: scan %s: %w", path, scanErr)
	}
	if n := len(v.blocks); n > 0 && v.blocks[n-1].Height < finalCheckpointHeight(epochs) {
		return nil, errTransitionIncomplete
	}
	return v, nil
}

// decodeJournalLine decodes the line of it and verifies the block in it as
// the block at it.index of a chain that starts at genesis: everything
// verifyTransitionChainBlock checks but the link to the block before it,
// which the caller checks in journal order. keys are the load's prepared
// producer keys. It records the digest of the verified signature in it.at.
// On a verification failure it returns the block with the error.
func decodeJournalLine(epochs []producerpolicy.Epoch, keys *producerKeys, path string, it *journalItem) (*Block, error) {
	blk := &Block{}
	if err := json.Unmarshal(it.raw, blk); err != nil {
		return nil, fmt.Errorf("chain.LoadVerifiedJournal: parse line %d of %s: %w (verified %d blocks before it; trim the bad line to recover)",
			it.lineno, path, err, it.index)
	}
	if err := verifyTransitionChainBlock(epochs, keys, nil, blk, it.index); err != nil {
		return blk, fmt.Errorf("%w (%s line %d)", err, path, it.lineno)
	}
	if a := blk.ProducerAuth; len(a.PublicKey) > 0 && len(a.Signature) > 0 {
		it.at.digest = signatureDigest(a.Signature)
	}
	return blk, nil
}

// stripSignature drops b's signature from memory, recording where it is:
// at.offset and at.length locate b's journal line, and at.digest is the
// digest of the signature (decodeJournalLine, CheckJournal, journaled). It is
// only ever called on a block nothing else holds yet: by LoadVerifiedJournal
// and CheckJournal on a block they have read and not yet returned
// (LoadVerifiedJournal on a block it has verified, CheckJournal on every
// block but the last, verified or not: only its Verified vouches for a
// block), and by dropSignatureLocked on the copy it is about to put in the
// chain. A block without a signature is left as it is.
func (b *Block) stripSignature(at strippedSignature) {
	a := b.ProducerAuth
	if len(a.PublicKey) == 0 || len(a.Signature) == 0 || a.stripped != nil {
		return
	}
	b.ProducerAuth = BFTWireAuth{PublicKey: a.PublicKey, stripped: &at}
}

// RelayBlocks returns the blocks whose heights lie in [from, to], in height
// order (the first limit of them when limit is positive), each with its
// producer signature: what GET /api/v1/chain/blocks and the P2P
// block_request relay. A block whose signature was dropped from memory (by
// LoadVerifiedJournal, or after its append by KeepSignaturesInJournal) is
// replaced by a copy that carries the signature its journal line holds,
// once the line has been checked against it (readStrippedSignature). The
// journal is opened only when such a block is in the range, once per call,
// and closed before RelayBlocks returns: nothing holds it open between calls
// (Windows cannot rename an open file).
//
// A block that cannot be read back is an error, never a wrong block:
// RelayBlocks then returns the blocks before it, and the error.
func (bp *BlockProducer) RelayBlocks(from, to uint64, limit int) ([]*Block, error) {
	if bp == nil {
		return nil, nil
	}
	bp.mu.Lock()
	blocks := bp.blockRangeLocked(from, to, limit)
	journal := bp.journalPath
	bp.mu.Unlock()

	var f *os.File
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()
	for i, b := range blocks {
		s := b.ProducerAuth.stripped
		if s == nil {
			continue
		}
		if f == nil {
			if journal == "" {
				return blocks[:i], fmt.Errorf("chain: block %d: its signature is not in memory and no journal is known", b.Height)
			}
			var err error
			if f, err = os.Open(journal); err != nil {
				return blocks[:i], fmt.Errorf("chain: block %d: open the journal for its signature: %w", b.Height, err)
			}
		}
		full, err := readStrippedSignature(f, b, s)
		if err != nil {
			return blocks[:i], fmt.Errorf("chain: block %d: %s: %w", b.Height, journal, err)
		}
		blocks[i] = full
	}
	return blocks, nil
}

// journalLineIdentity is what readStrippedSignature decodes of a journal line.
type journalLineIdentity struct {
	Height       uint64 `json:"height"`
	PrevHash     string `json:"prev_hash"`
	Hash         string `json:"hash"`
	ProducerID   string `json:"producer_id"`
	ProducerAuth struct {
		PublicKey []byte `json:"public_key"`
		Signature []byte `json:"signature"`
	} `json:"producer_auth"`
}

// readStrippedSignature reads b's journal line from f and returns a copy of
// b that carries the signature in it. The line must hold b (its height, hash,
// parent hash, producer ID and producer public key) and a signature whose
// digest is the one recorded when it was verified; anything else, a short or
// garbled line included, is an error. The copy is b with that signature, so
// it encodes exactly as b did before its signature was dropped.
func readStrippedSignature(f *os.File, b *Block, s *strippedSignature) (*Block, error) {
	line := make([]byte, s.length)
	if _, err := f.ReadAt(line, s.offset); err != nil {
		return nil, fmt.Errorf("read the journal line at offset %d: %w", s.offset, err)
	}
	var got journalLineIdentity
	if err := json.Unmarshal(line, &got); err != nil {
		return nil, fmt.Errorf("the journal line at offset %d does not decode: %w", s.offset, err)
	}
	if got.Height != b.Height || got.Hash != b.Hash || got.PrevHash != b.PrevHash || got.ProducerID != b.ProducerID ||
		!bytes.Equal(got.ProducerAuth.PublicKey, b.ProducerAuth.PublicKey) {
		return nil, fmt.Errorf("the journal line at offset %d holds block %d (%s), not this block (%s)", s.offset, got.Height, got.Hash, b.Hash)
	}
	if signatureDigest(got.ProducerAuth.Signature) != s.digest {
		return nil, fmt.Errorf("the journal line at offset %d holds a producer signature other than the one verified at load", s.offset)
	}
	full := *b
	full.ProducerAuth = BFTWireAuth{PublicKey: b.ProducerAuth.PublicKey, Signature: got.ProducerAuth.Signature}
	return &full, nil
}

// Signatures of appended blocks.
//
// A block the node appends while it runs is in its chain (bp.chain) before
// its journal line is written. ProduceBlock, and TryAppendExternalBlock for a
// block from the producer, a peer (gossip, catch-up response, BFT commit) or
// the HTTPS sync, append it under bp.mu, then run OnSealedBlock, the
// persistence hook, with the seal lifecycle lock still held. The hook writes
// and fsyncs the line (ChainJournal.Append, step H2), makes the block's
// state durable (H3-H7), lets the served surfaces reach it (H9: the API and
// the P2P provider serve nothing above the durable tip) and announces it
// (H10: POL and the block gossip, which encodes the block it was handed).
// The lifecycle lock makes the next append wait for all of that.
//
// KeepSignaturesInJournal attaches the journal to the producer: Append then
// tells the producer where each block's line is once it is durable
// (journaled), and the producer drops the signature of the block before it.
// So block N-1 loses its signature during block N's step H2:
//   - its own line has been durable since its own H2, and it was announced
//     with its signature at its own H10, before block N existed;
//   - block N's line starts right where block N-1's line ends, which shows
//     that nothing wrote to the journal between the two appends;
//   - the tip keeps its signature, as after LoadVerifiedJournal, and so does
//     a block whose successor never becomes durable (a failed write or fsync
//     tells the producer nothing, and the hook then stops the node).
//
// A block is never changed once it is in the chain: API handlers and P2P
// providers take blocks from the chain under bp.mu and encode them outside
// it. dropSignatureLocked builds a stripped copy and puts it in the block's
// slot of the chain under bp.mu instead. The chain slice is the producer's
// only index of its blocks (a height is an index computation into it,
// block_range.go), so that slot is all that points at the block. A reader
// that took the block before the swap holds a complete block that stays
// complete, and the original is collected once the last such reader lets go
// of it; a reader that takes it after the swap gets the copy, which only
// RelayBlocks relays (it reads the signature back and checks it). Only
// blocks appended while the node runs are swapped: the tip a start restored
// keeps its signature until the next start, and the slots of the restored
// blocks, which the restore's caller may still read, are never written.
//
// RelayBlocks reads every dropped signature from one file, journalPath:
// attaching a journal at another path is refused, and a line in another
// file, or one Append could not locate exactly, drops nothing. A node
// without a journal, or that never attaches one, keeps every appended
// block's signature in memory, as before.

// journaledBlock is a block an attached journal wrote, with where its line
// is and the digest of its signature (none for an unsigned block).
type journaledBlock struct {
	blk *Block
	at  strippedSignature
}

// KeepSignaturesInJournal makes bp keep the producer signature of each block
// it appends from now on only in j, once the next block is durable in j
// right after it (see above). j must be the journal the persistence hook
// appends bp's blocks to.
//
// It refuses, and nothing changes, when:
//   - bp has no producer transition. Such a node loads its journal with every
//     signature (LoadChainNDJSON: LoadVerifiedJournal needs a transition) and
//     keeps them in memory while it runs, as before: no block of it is ever
//     stripped, whatever reads it;
//   - the signatures bp dropped at its start are in another file than j:
//     RelayBlocks reads them all from one journal (journalPath).
//
// A producer that holds no dropped signature yet (a fresh chain, or one
// restored by RestoreChain) takes j's path as its journalPath.
func (bp *BlockProducer) KeepSignaturesInJournal(j *ChainJournal) error {
	if bp == nil || j == nil {
		return errors.New("chain.KeepSignaturesInJournal: nil producer or journal")
	}
	bp.mu.Lock()
	switch {
	case bp.producerTransition == nil:
		bp.mu.Unlock()
		return errors.New("chain.KeepSignaturesInJournal: no producer transition is configured (appended blocks keep their signatures in memory)")
	case bp.journalPath != "" && bp.journalPath != j.path:
		held := bp.journalPath
		bp.mu.Unlock()
		return fmt.Errorf("chain.KeepSignaturesInJournal: the signatures dropped at the start are in %s, not in %s (appended blocks keep their signatures in memory)", held, j.path)
	}
	bp.journalPath = j.path
	bp.mu.Unlock()
	// Not under bp.mu: Append holds j.mu when it calls journaled, which
	// takes bp.mu.
	j.mu.Lock()
	j.keep = bp
	j.mu.Unlock()
	return nil
}

// journaled is ChainJournal.Append telling bp, with j.mu held, that the line
// of blk in the journal at path is durable, at at if located. If the line of
// the block before blk was located too and ends right where blk's line
// starts, that block's signature is dropped (dropSignatureLocked). blk is then
// remembered for the next line. Anything else drops nothing: a line Append
// could not locate, a journal other than journalPath, a block that is not in
// bp's chain, or a block or a line that does not follow the remembered one.
func (bp *BlockProducer) journaled(path string, blk *Block, at strippedSignature, located bool) {
	if blk == nil {
		return
	}
	// blk is in the chain and never changed: reading it needs no lock.
	if a := blk.ProducerAuth; located && len(a.PublicKey) > 0 && len(a.Signature) > 0 {
		at.digest = signatureDigest(a.Signature)
	}
	bp.mu.Lock()
	defer bp.mu.Unlock()
	prev := bp.appendTail
	bp.appendTail = journaledBlock{}
	if !located || bp.producerTransition == nil || path != bp.journalPath {
		return
	}
	if b, ok := bp.blockAtLocked(blk.Height); !ok || b != blk {
		return
	}
	if p := prev.blk; p != nil && p.Height+1 == blk.Height && prev.at.offset+int64(prev.at.length)+1 == at.offset {
		bp.dropSignatureLocked(p, prev.at)
	}
	bp.appendTail = journaledBlock{blk: blk, at: at}
}

// dropSignatureLocked puts in b's slot of the chain a copy of b without its
// signature, at saying where the signature is (its journal line, and the
// signature's digest). b itself is not changed (see above). An unsigned
// block, or one no longer in its slot, is left alone. bp.mu must be held.
func (bp *BlockProducer) dropSignatureLocked(b *Block, at strippedSignature) {
	if a := b.ProducerAuth; len(a.PublicKey) == 0 || len(a.Signature) == 0 || a.stripped != nil {
		return
	}
	if len(bp.chain) == 0 || b.Height < bp.chain[0].Height {
		return
	}
	i := b.Height - bp.chain[0].Height
	if i >= uint64(len(bp.chain)) || bp.chain[i] != b {
		return
	}
	c := *b
	c.stripSignature(at)
	bp.chain[i] = &c
}
