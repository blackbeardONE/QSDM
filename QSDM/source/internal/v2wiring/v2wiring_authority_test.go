package v2wiring_test

// v2wiring_authority_test.go: end-to-end integration coverage
// for the `qsdm/gov/v1` authority-rotation payload kind through
// the full production wiring path. Companion to
// v2wiring_gov_test.go (which covers the param-set surface).
//
// What's exercised here:
//
//	pool.Add(authTx) →
//	    chainparams.AdmissionChecker (kind dispatch) →
//	    producer.ProduceBlock() →
//	    GovApplier.applyAuthoritySet (vote tally + threshold) →
//	    SealedBlockHook (PromotePending: param store + auth votes) →
//	    SaveSnapshotWith (when persistence is configured) →
//	    api.SetGovernanceProvider snapshot reflects the rotated set
//
// What's NOT exercised here (covered by unit tests instead):
//
//   - threshold arithmetic (pkg/governance/chainparams/authority_test.go)
//   - vote-store record / drop / recompute mechanics (same)
//   - applier rejection branches (pkg/chain/gov_apply_authority_test.go)
//   - persistence shape / v1 backcompat (pkg/governance/chainparams/persist_authority_test.go)
//
// The remaining concerns are integration glue: does the
// admission stack route authority-set txs through the right
// validators? Does the SealedBlockHook actually call
// promoteAuthorityPending at the right height? Does the
// AuthorityList expand under a successful add rotation?

import (
	"path/filepath"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// authTx mints a well-formed qsdm/gov/v1 AuthoritySet vote tx
// for the requested rotation tuple. Mirrors govTx in shape.
func authTx(
	t *testing.T,
	sender, txID string,
	nonce uint64,
	op chainparams.AuthorityOp,
	address string,
	effectiveHeight uint64,
	memo string,
) *mempool.Tx {
	t.Helper()
	raw, err := chainparams.EncodeAuthoritySet(chainparams.AuthoritySetPayload{
		Kind:            chainparams.PayloadKindAuthoritySet,
		Op:              op,
		Address:         address,
		EffectiveHeight: effectiveHeight,
		Memo:            memo,
	})
	if err != nil {
		t.Fatalf("EncodeAuthoritySet: %v", err)
	}
	return &mempool.Tx{
		ID:         txID,
		Sender:     sender,
		Nonce:      nonce,
		Fee:        0.001,
		Payload:    raw,
		ContractID: chainparams.ContractID,
	}
}

// rotationFundedRig wraps buildGovRig and additionally credits
// "newcomer" so a successful "add newcomer" rotation has a
// usable account on the chain post-activation.
func rotationFundedRig(t *testing.T, storePath string) *govRig {
	r := buildGovRig(t, 100, storePath)
	r.accounts.Credit("newcomer", 100)
	return r
}

// -----------------------------------------------------------------------------
// happy path: add rotation through M-of-N → activation
// -----------------------------------------------------------------------------

// TestAuthWire_AddRotationActivates drives the canonical flow:
// alice + carol vote to add "newcomer", threshold=2 at N=2, so
// the second vote crosses; activation at h=1 expands the
// AuthorityList to {alice, carol, newcomer}.
func TestAuthWire_AddRotationActivates(t *testing.T) {
	r := rotationFundedRig(t, "")

	// Genesis block carries both votes.
	v1 := authTx(t, tAlice, "vote-1", 0, chainparams.AuthorityOpAdd,
		"newcomer", 1, "onboarding")
	v2 := authTx(t, tCarol, "vote-2", 0, chainparams.AuthorityOpAdd,
		"newcomer", 1, "")
	if err := r.pool.Add(v1); err != nil {
		t.Fatalf("admit v1: %v", err)
	}
	if err := r.pool.Add(v2); err != nil {
		t.Fatalf("admit v2: %v", err)
	}
	if _, err := r.producer.ProduceBlock(); err != nil {
		t.Fatalf("genesis block: %v", err)
	}

	// At this point both votes are tallied; the proposal is
	// Crossed but not yet activated (genesis hook fires
	// Promote(0); EffectiveHeight=1).
	pre := r.w.Gov.AuthorityList()
	if len(pre) != 2 {
		t.Errorf("pre-activation list size = %d, want 2", len(pre))
	}

	// Seal h=1 with a filler — hook fires Promote(1). Both
	// authorities used nonce=0 in their vote tx, so the
	// filler's nonce must be 1.
	_ = sealOne(t, r, tCarol, 1, "rotation-promote")

	post := r.w.Gov.AuthorityList()
	if len(post) != 3 {
		t.Fatalf("post-activation list = %v, want 3 entries", post)
	}
	var sawNewcomer bool
	for _, a := range post {
		if a == "newcomer" {
			sawNewcomer = true
		}
	}
	if !sawNewcomer {
		t.Errorf("newcomer not on post-activation list: %v", post)
	}
}

// -----------------------------------------------------------------------------
// admission stack routes authority-set txs correctly
// -----------------------------------------------------------------------------

// TestAuthWire_BadAuthorityTxRejectedAtAdmission confirms that
// chainparams.AdmissionChecker dispatches on the kind tag and
// runs the authority-set validator (empty address rejects
// pre-pool, as it should for a stateless guard).
//
// We hand-build the bad payload because EncodeAuthoritySet
// runs the same validator pre-emit and would refuse to
// produce a malformed payload — exactly the right call-site
// behaviour, but unhelpful here.
func TestAuthWire_BadAuthorityTxRejectedAtAdmission(t *testing.T) {
	r := buildGovRig(t, 100, "")
	tx := &mempool.Tx{
		ID:         "vote-bad",
		Sender:     tAlice,
		Nonce:      0,
		Fee:        0.001,
		ContractID: chainparams.ContractID,
		Payload:    []byte(`{"kind":"authority-set","op":"add","address":"","effective_height":1}`),
	}
	if err := r.pool.Add(tx); err == nil {
		t.Fatal("admission accepted malformed authority-set (empty address)")
	}
}

// -----------------------------------------------------------------------------
// persistence: a vote crosses, snapshot, restart, activate.
// -----------------------------------------------------------------------------

// TestAuthWire_PersistenceReplaysCrossedAcrossRestart simulates
// a node crash between threshold-crossing and the activation
// block. The snapshot from the crossed-but-not-yet-activated
// state must replay under a fresh wire and still activate at
// the original EffectiveHeight.
func TestAuthWire_PersistenceReplaysCrossedAcrossRestart(t *testing.T) {
	t.Cleanup(func() {
		monitoring.SetEnrollmentStateProvider(nil)
		api.SetGovernanceProvider(nil)
	})

	dir := t.TempDir()
	storePath := filepath.Join(dir, "auth.json")

	// Stage 1: cast 2 votes that cross threshold; effective
	// height = 5 (still in future). Snapshot saves on every
	// sealed block.
	{
		r := rotationFundedRig(t, storePath)
		v1 := authTx(t, tAlice, "stage1-1", 0, chainparams.AuthorityOpAdd,
			"newcomer", 5, "")
		v2 := authTx(t, tCarol, "stage1-2", 0, chainparams.AuthorityOpAdd,
			"newcomer", 5, "")
		if err := r.pool.Add(v1); err != nil {
			t.Fatalf("admit v1: %v", err)
		}
		if err := r.pool.Add(v2); err != nil {
			t.Fatalf("admit v2: %v", err)
		}
		if _, err := r.producer.ProduceBlock(); err != nil {
			t.Fatalf("stage1 genesis: %v", err)
		}
		// The genesis hook saves the snapshot; the proposal is
		// crossed but not yet activated (EffectiveHeight=5,
		// blk.Height=0).
		props := r.w.GovAuthVotes.AllProposals()
		if len(props) != 1 || !props[0].Crossed {
			t.Fatalf("stage1 proposals = %+v, want 1 Crossed", props)
		}
	}

	// Stage 2: fresh wire, load from the snapshot, drive
	// blocks until h=5 — the crossed proposal must replay
	// and activate.
	{
		r := rotationFundedRig(t, storePath)
		props := r.w.GovAuthVotes.AllProposals()
		if len(props) != 1 || !props[0].Crossed {
			t.Fatalf("stage2 proposals after replay = %+v, want 1 Crossed", props)
		}
		// Drive blocks until effective height (=5) activates.
		// Stage-2 boots a fresh AccountStore (only the gov
		// snapshot is persisted across the simulated
		// restart), so carol's nonce starts at 0. Producer
		// also starts at TipHeight=0, so blocks seal at
		// heights 0,1,2,3,4,5; block 5 fires Promote(5).
		for i := uint64(0); i <= 5; i++ {
			_ = sealOne(t, r, tCarol, i, "h"+string(rune('a'+i)))
		}
		post := r.w.Gov.AuthorityList()
		var sawNewcomer bool
		for _, a := range post {
			if a == "newcomer" {
				sawNewcomer = true
			}
		}
		if !sawNewcomer {
			t.Errorf("newcomer not on post-replay list: %v", post)
		}
	}
}

// -----------------------------------------------------------------------------
// Smoke test: param-set txs still work alongside authority-set
// -----------------------------------------------------------------------------

// TestAuthWire_ParamSetStillWorks confirms the kind dispatch
// hasn't broken the param-set path. A regression in the dispatch
// would cause a param-set tx to be parsed as an authority-set
// payload, failing decode.
func TestAuthWire_ParamSetStillWorks(t *testing.T) {
	r := buildGovRig(t, 100, "")
	tx := govTx(t, tAlice, "param-mixed", 0, 2500, 1, "")
	if err := r.pool.Add(tx); err != nil {
		t.Fatalf("admit param-set: %v", err)
	}
	if _, err := r.producer.ProduceBlock(); err != nil {
		t.Fatalf("genesis: %v", err)
	}
	if _, ok := r.w.GovParams.Pending(string(chainparams.ParamRewardBPS)); !ok {
		t.Error("param-set pending lost — kind dispatch likely misrouted")
	}
	_ = sealOne(t, r, tCarol, 0, "param-promote")
	v, _ := r.w.GovParams.ActiveValue(string(chainparams.ParamRewardBPS))
	if v != 2500 {
		t.Errorf("active reward_bps = %d, want 2500 (param-set still works)", v)
	}
	_ = chain.SlashRewardCap // silence unused import if cap stays untouched
}
