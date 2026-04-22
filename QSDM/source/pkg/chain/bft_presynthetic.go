package chain

import "fmt"

// RunSyntheticBFTRoundWithExecutor runs a full local propose → prevote → precommit round for height,
// gossiping each step via exec when non-nil. Used before the block is appended (pre-seal).
// tentative carries Height, StateRoot, Hash, and txs for inclusion in the propose gossip payload.
// On failure the active round is torn down with FailRound where applicable.
func RunSyntheticBFTRoundWithExecutor(exec *BFTExecutor, vs *ValidatorSet, tentative *Block) error {
	if exec == nil || vs == nil || tentative == nil {
		return fmt.Errorf("chain: RunSyntheticBFTRoundWithExecutor needs executor, validator set, and tentative block")
	}
	bc := exec.Consensus()
	if bc == nil {
		return fmt.Errorf("chain: BFT consensus is nil")
	}
	height := tentative.Height
	stateRoot := tentative.StateRoot
	if bc.IsCommitted(height) {
		return nil
	}
	round := bc.NextRoundAfterTimeout(height)
	prop, err := bc.ProposerForRound(round)
	if err != nil {
		return err
	}
	if _, err := bc.Propose(height, round, prop, stateRoot); err != nil {
		return err
	}
	_ = exec.BroadcastPropose(height, round, prop, stateRoot, tentative)
	for _, v := range vs.ActiveValidators() {
		if v.Status != ValidatorActive {
			continue
		}
		if err := bc.PreVote(height, v.Address, stateRoot); err != nil {
			_ = bc.FailRound(height)
			return fmt.Errorf("chain: prevote %s: %w", v.Address, err)
		}
		_ = exec.BroadcastPrevote(height, round, v.Address, stateRoot)
	}
	if _, err := bc.BuildPrevoteLockProof(height); err != nil {
		_ = bc.FailRound(height)
		return err
	}
	for _, v := range vs.ActiveValidators() {
		if v.Status != ValidatorActive {
			continue
		}
		if err := bc.PreCommit(height, v.Address, stateRoot); err != nil {
			_ = bc.FailRound(height)
			return fmt.Errorf("chain: precommit %s: %w", v.Address, err)
		}
		_ = exec.BroadcastPrecommit(height, round, v.Address, stateRoot)
	}
	if !bc.IsCommitted(height) {
		_ = bc.FailRound(height)
		return fmt.Errorf("chain: BFT height %d did not commit after synthetic round", height)
	}
	exec.NotifyFromConsensus(height)
	return nil
}
