package networking

import (
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

// PublishPolAfterBlockSeal runs a synthetic BFT round aligned with the sealed block height,
// gossips propose/prevote/precommit on the BFT topic when bftExec has a publisher, and
// gossips PrevoteLockProof + RoundCertificate when the POL relay is non-nil.
// polFollower records the local state root after a successful sidecar Propose (POL alignment);
// MarkLocalRoundCertificatePublished is set when gossip succeeds, the sidecar commits, or POL
// simulation fails after the block is already sealed (liveness for anchored finality).
func PublishPolAfterBlockSeal(log *logging.Logger, relay *PolP2PRelay, polFollower *chain.PolFollower, bftExec *chain.BFTExecutor, bc *chain.BFTConsensus, vs *chain.ValidatorSet, blk *chain.Block) {
	if blk == nil || log == nil {
		return
	}
	h := blk.Height

	markPublished := func() {
		if polFollower != nil {
			polFollower.MarkLocalRoundCertificatePublished(h)
		}
	}

	if bc == nil || vs == nil {
		if polFollower != nil {
			polFollower.RecordLocalSealedBlock(h, blk.StateRoot)
		}
		markPublished()
		return
	}

	polRelayOk := relay != nil

	if bc.IsCommitted(h) {
		publishPolAfterAlreadyCommitted(log, relay, polFollower, bftExec, bc, blk, polRelayOk, markPublished)
		return
	}

	round := bc.NextRoundAfterTimeout(h)
	prop, err := bc.ProposerForRound(round)
	if err != nil {
		log.Debug("POL publish skip: proposer", "error", err)
		if polFollower != nil {
			polFollower.RecordLocalSealedBlock(h, blk.StateRoot)
		}
		markPublished()
		return
	}
	if _, err := bc.Propose(h, round, prop, blk.StateRoot); err != nil {
		log.Debug("POL publish skip: propose", "height", h, "error", err)
		if polFollower != nil {
			polFollower.RecordLocalSealedBlock(h, blk.StateRoot)
		}
		markPublished()
		return
	}
	if bftExec != nil {
		_ = bftExec.BroadcastPropose(h, round, prop, blk.StateRoot, blk)
	}
	if polFollower != nil {
		polFollower.RecordLocalSealedBlock(h, blk.StateRoot)
	}
	for _, v := range vs.ActiveValidators() {
		if v.Status != chain.ValidatorActive {
			continue
		}
		if err := bc.PreVote(h, v.Address, blk.StateRoot); err != nil {
			log.Debug("POL publish prevote", "validator", v.Address, "error", err)
		} else if bftExec != nil {
			_ = bftExec.BroadcastPrevote(h, round, v.Address, blk.StateRoot)
		}
	}
	if proof, err := bc.BuildPrevoteLockProof(h); err != nil {
		log.Debug("POL publish skip: lock proof", "height", h, "error", err)
		_ = bc.FailRound(h)
		markPublished()
		return
	} else if polRelayOk {
		if err := relay.PublishPrevoteLockProof(proof); err != nil {
			log.Warn("POL gossip publish prevote_lock failed", "error", err)
		}
	}
	for _, v := range vs.ActiveValidators() {
		if v.Status != chain.ValidatorActive {
			continue
		}
		if err := bc.PreCommit(h, v.Address, blk.StateRoot); err != nil {
			log.Debug("POL publish precommit", "validator", v.Address, "error", err)
		} else if bftExec != nil {
			_ = bftExec.BroadcastPrecommit(h, round, v.Address, blk.StateRoot)
		}
	}
	if bftExec != nil {
		bftExec.NotifyFromConsensus(h)
	}
	if cert, err := bc.BuildRoundCertificate(h); err != nil {
		log.Debug("POL publish skip: round certificate", "height", h, "error", err)
	} else if polRelayOk {
		if err := relay.PublishRoundCertificate(cert); err != nil {
			log.Warn("POL gossip publish round_certificate failed", "error", err)
		} else {
			markPublished()
		}
	} else {
		markPublished()
	}
	if !bc.IsCommitted(h) {
		_ = bc.FailRound(h)
		markPublished()
		return
	}
	markPublished()
}

// publishPolAfterAlreadyCommitted gossips POL artifacts when BFT already committed this height (e.g. pre-seal).
func publishPolAfterAlreadyCommitted(
	log *logging.Logger,
	relay *PolP2PRelay,
	polFollower *chain.PolFollower,
	bftExec *chain.BFTExecutor,
	bc *chain.BFTConsensus,
	blk *chain.Block,
	polRelayOk bool,
	markPublished func(),
) {
	h := blk.Height
	if polFollower != nil {
		polFollower.RecordLocalSealedBlock(h, blk.StateRoot)
	}
	if proof, err := bc.BuildPrevoteLockProof(h); err != nil {
		log.Debug("POL publish skip: lock proof (already committed)", "height", h, "error", err)
	} else if polRelayOk {
		if err := relay.PublishPrevoteLockProof(proof); err != nil {
			log.Warn("POL gossip publish prevote_lock failed", "error", err)
		}
	}
	if bftExec != nil {
		bftExec.NotifyFromConsensus(h)
	}
	if cert, err := bc.BuildRoundCertificate(h); err != nil {
		log.Debug("POL publish skip: round certificate (already committed)", "height", h, "error", err)
	} else if polRelayOk {
		if err := relay.PublishRoundCertificate(cert); err != nil {
			log.Warn("POL gossip publish round_certificate failed", "error", err)
		}
	}
	markPublished()
}
