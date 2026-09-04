package networking

import (
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/chain"
)

func TestPreparePolArtifactsSignsWithConsensusKey(t *testing.T) {
	signer, _, err := chain.LoadOrCreateBFTSigner(filepath.Join(t.TempDir(), "consensus.json"))
	if err != nil {
		t.Fatal(err)
	}
	exec := chain.NewBFTExecutor(chain.NewBFTConsensus(chain.NewValidatorSet(chain.DefaultValidatorSetConfig()), chain.DefaultConsensusConfig()))
	exec.SetVoteSigner(signer)
	logger := logging.NewLogger("", false)

	proof := &chain.PrevoteLockProof{Height: 8, Round: 1, LockedBlockHash: "root"}
	if !preparePrevoteLockProof(logger, exec, proof) {
		t.Fatal("prevote lock proof should be signed")
	}
	if err := chain.VerifyPrevoteLockProof(proof); err != nil {
		t.Fatalf("signed prevote lock proof did not verify: %v", err)
	}

	cert := &chain.RoundCertificate{Height: 8, Round: 1, BlockHash: "root", CommitDigest: "digest"}
	if !prepareRoundCertificate(logger, exec, cert) {
		t.Fatal("round certificate should be signed")
	}
	if err := chain.VerifyRoundCertificate(cert); err != nil {
		t.Fatalf("signed round certificate did not verify: %v", err)
	}
}

func TestPublishPolRefusesSyntheticMultiValidatorRound(t *testing.T) {
	signer, _, err := chain.LoadOrCreateBFTSigner(filepath.Join(t.TempDir(), "consensus.json"))
	if err != nil {
		t.Fatal(err)
	}
	vs := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	if err := vs.Register(signer.Address(), chain.DefaultValidatorSetConfig().MinStake); err != nil {
		t.Fatal(err)
	}
	if err := vs.Register("unavailable-validator", chain.DefaultValidatorSetConfig().MinStake); err != nil {
		t.Fatal(err)
	}
	bc := chain.NewBFTConsensus(vs, chain.DefaultConsensusConfig())
	exec := chain.NewBFTExecutor(bc)
	exec.SetVoteSigner(signer)
	follower := chain.NewPolFollower(vs, 2.0/3.0)
	follower.SetAnchorFinality(true)

	PublishPolAfterBlockSeal(
		logging.NewLogger("", false),
		nil,
		follower,
		exec,
		bc,
		vs,
		&chain.Block{Height: 9, StateRoot: "root"},
	)
	if bc.IsCommitted(9) {
		t.Fatal("POL publisher must not manufacture a quorum for validators whose keys are unavailable")
	}
	if follower.CanExtendFromTip(9, "root") {
		t.Fatal("anchored finality must not fail open after refusing an unauthenticated POL round")
	}
}
