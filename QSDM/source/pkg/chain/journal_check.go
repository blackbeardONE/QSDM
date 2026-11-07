package chain

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sync/atomic"

	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// A whole journal checked in one read.
//
// qsdm --snapshot-import (and --snapshot-export, which checks what it wrote)
// and qsdm --hl1-tail-replay check a whole journal before they restore it.
// They loaded it with LoadChainNDJSON, every block with its producer
// signature, computed every block hash twice and, under a producer
// transition, verified every signature on one core
// (ValidateProducerTransitionChain). At height 880,376 of the public chain the
// signatures alone took 1.8 GiB, and the import's checks peaked at 5.5 to
// 5.7 GiB on 1 to 16 threads (main at ff6a9f8), where a start, which loads
// with LoadVerifiedJournal, keeps only the tip's signature.
//
// CheckJournal reads the journal once and decodes and checks its blocks on
// every core, as LoadVerifiedJournal does, keeping every signature but the
// last block's only in the journal. Unlike LoadVerifiedJournal it does not
// stop at the first block that fails: these tools report the first failure in
// the order of their own checks (forked duplicates before a broken link, a
// broken link or a wrong hash before the checkpoint pin, the pin before a
// bad signature, ...), and that order needs everything the three checks it
// replaces would have found. So it returns what each of them returns:
// LoadChainNDJSON's blocks or error, the first block whose hash is not its
// contents' hash, and ValidateProducerTransitionChain's error, from one read.

// JournalCheck is what CheckJournal found in a journal.
type JournalCheck struct {
	// Blocks are the blocks LoadChainNDJSON returns, in journal order, with
	// the same values shared. Under a producer transition every block but
	// the last holds its producer signature only in the journal, whether it
	// verified or not (Stripped): a block is trusted only through Verified.
	Blocks []*Block
	// LoadErr is the error LoadChainNDJSON returns for the journal. When it
	// is set, Blocks holds the blocks before the line it names, BadHash is
	// -1, and VerifyErr and Verified are nil.
	LoadErr error
	// BadHash is the index in Blocks of the first block whose hash is not
	// ComputeBlockHash of its contents, or -1.
	BadHash int
	// VerifyErr is the error ValidateProducerTransitionChain returns for
	// Blocks under the producer transition; nil without one.
	VerifyErr error
	// Verified holds Blocks when VerifyErr is nil under a producer
	// transition: RestoreVerifiedChain installs them, or a prefix of them
	// that reaches the final checkpoint, without verifying them again. Nil
	// otherwise.
	Verified *VerifiedChain
}

// CheckJournal reads the journal at path once and returns what
// LoadChainNDJSON, a check of every block hash and, under transition,
// ValidateProducerTransitionChain return for it, each exactly as that check
// returns it.
//
// The lines are decoded and checked on every core, as LoadVerifiedJournal
// does: each block hash is computed once and, under a producer transition,
// each block's producer schedule and signature are checked once, with each
// producer key prepared once. Signatures after the first block that fails are
// not verified (ValidateProducerTransitionChain stops at that block). Only a
// line that does not decode, or a read error, stops the read, as it stops
// LoadChainNDJSON. As for LoadVerifiedJournal, the package consensus settings
// must already be the ones the blocks are checked under; Verified records
// them.
//
// transition must be valid (ValidateJournalPrefix validates it). The error is
// for an invalid transition or path only: what the checks find is in the
// JournalCheck.
func CheckJournal(path string, transition *producerpolicy.Transition) (*JournalCheck, error) {
	return checkJournal(path, transition, runtime.GOMAXPROCS(0))
}

func checkJournal(path string, transition *producerpolicy.Transition, workers int) (*JournalCheck, error) {
	if path == "" {
		return nil, errors.New("chain.CheckJournal: empty path")
	}
	if err := transition.Validate(); err != nil {
		return nil, fmt.Errorf("chain.CheckJournal: %w", err)
	}
	epochs := transition.Schedule() // nil without a transition
	settings := currentVerifySettings()
	c := &JournalCheck{BadHash: -1}
	f, err := os.Open(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return &JournalCheck{BadHash: -1, LoadErr: fmt.Errorf("chain.LoadChainNDJSON: open %s: %w", path, err)}, nil
		}
		// A missing journal is no blocks and no error, as for LoadChainNDJSON.
		c.finish(epochs, settings, path, false, nil)
		return c, nil
	}
	defer f.Close()

	keys := newProducerKeys()
	// authUntil is the index of the first block that failed, once the loop
	// below has seen it: the workers verify no signature after it.
	var authUntil atomic.Uint64
	authUntil.Store(math.MaxUint64)
	p := startJournalPipeline(f, workers, func(it *journalItem) {
		blk := &Block{}
		if err := json.Unmarshal(it.raw, blk); err != nil {
			it.err = err
			return
		}
		it.blk = blk
		it.hashOK = blk.Hash == computeBlockHash(blk)
		if epochs == nil {
			return
		}
		if a := blk.ProducerAuth; len(a.PublicKey) > 0 && len(a.Signature) > 0 {
			it.at.digest = signatureDigest(a.Signature)
		}
		if it.hashOK && it.index <= authUntil.Load() {
			it.authErr = checkTransitionBlockAuth(epochs, keys, blk)
		}
	})

	share := newBlockSharing()
	var held *Block // the last block read: its signature is dropped when the next one is read
	var heldAt strippedSignature
	noGenesis := false
	var blockErr error // the first failure of ValidateProducerTransitionChain's per-block checks
	for it := range p.order {
		<-it.done
		if it.err != nil {
			p.stop()
			return &JournalCheck{Blocks: c.Blocks, BadHash: -1,
				LoadErr: fmt.Errorf("chain.LoadChainNDJSON: parse line %d of %s: %w (loaded %d blocks before failure; trim the bad line to recover)",
					it.lineno, path, it.err, len(c.Blocks))}, nil
		}
		blk, i := it.blk, it.index
		if !it.hashOK && c.BadHash < 0 {
			c.BadHash = int(i)
		}
		if epochs != nil && !noGenesis && blockErr == nil {
			// validateProducerTransitionChainLocked, one block at a time.
			if i == 0 && (blk.Height != 0 || blk.PrevHash != "") {
				noGenesis = true
			} else {
				var prev *Block
				if i > 0 {
					prev = c.Blocks[i-1]
				}
				if blockErr = transitionChainPlaceErr(prev, blk, i, it.hashOK); blockErr == nil {
					blockErr = it.authErr
				}
			}
			if noGenesis || blockErr != nil {
				authUntil.Store(i)
			}
		}
		share.add(blk)
		if held != nil && epochs != nil {
			held.stripSignature(heldAt)
		}
		held, heldAt = blk, it.at
		c.Blocks = append(c.Blocks, blk)
	}
	if scanErr := p.wait(); scanErr != nil && !errors.Is(scanErr, io.EOF) {
		return &JournalCheck{Blocks: c.Blocks, BadHash: -1, LoadErr: fmt.Errorf("chain.LoadChainNDJSON: scan %s: %w", path, scanErr)}, nil
	}
	c.finish(epochs, settings, path, noGenesis, blockErr)
	return c, nil
}

// finish sets VerifyErr, or Verified, as ValidateProducerTransitionChain
// decides for c.Blocks: a chain that does not start at genesis first, then one
// that does not reach the final checkpoint, then the first block that failed
// (blockErr). It does nothing without a producer transition (epochs nil).
func (c *JournalCheck) finish(epochs []producerpolicy.Epoch, settings verifySettings, path string, noGenesis bool, blockErr error) {
	if epochs == nil {
		return
	}
	n := len(c.Blocks)
	switch {
	case n == 0 || noGenesis:
		c.VerifyErr = errTransitionNoGenesis
	case c.Blocks[n-1].Height < finalCheckpointHeight(epochs):
		c.VerifyErr = errTransitionIncomplete
	case blockErr != nil:
		c.VerifyErr = blockErr
	default:
		c.Verified = &VerifiedChain{epochs: epochs, settings: settings, blocks: append([]*Block(nil), c.Blocks...), journal: path}
	}
}
