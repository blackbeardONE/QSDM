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
// SHA-256 (strippedSignature, a 32-byte allocation). Blocks appended later
// keep their signatures until the next restart. A block is never changed
// once LoadVerifiedJournal returns it: blocks are encoded outside the
// producer lock.
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
// signature is no longer in memory (LoadVerifiedJournal). Such an
// authenticator is Signed, fails every verification with
// ErrBlockSignatureStripped and does not encode; RelayBlocks reads its
// signature back from the journal.
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
// most one line per worker is read ahead.
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

// journalPipeline reads a journal as LoadChainNDJSON does (one block per
// line, blank lines skipped, lines up to 4 MiB) and runs work on every line
// on its own goroutines. The reader hands each line to a free worker after
// queueing it, in journal order, on order; the caller takes the lines from
// order and waits for each one's done, so at most one line per worker is read
// ahead of the line the caller waits for.
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
	p := &journalPipeline{order: make(chan *journalItem, workers), quit: make(chan struct{})}
	jobs := make(chan *journalItem)
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
// digest of the signature (decodeJournalLine, CheckJournal). Only
// LoadVerifiedJournal and CheckJournal call it, on a block they have read
// and not yet returned: LoadVerifiedJournal on a block it has verified,
// CheckJournal on every block but the last, verified or not (only its
// Verified vouches for a block). A block without a signature is left as it
// is.
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
// block_request relay. A block whose signature LoadVerifiedJournal dropped
// is replaced by a copy that carries the signature its journal line holds,
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
