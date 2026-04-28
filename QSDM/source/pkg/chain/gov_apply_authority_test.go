package chain

// gov_apply_authority_test.go: covers the authority-rotation
// dispatch path of GovApplier.ApplyGovTx + the promotion hook
// that mutates the AuthorityList behind the scenes.
//
// Companion to gov_apply_test.go. The integration test in
// internal/v2wiring/v2wiring_gov_test.go exercises the same
// path end-to-end through Wire(); this file pins the
// applier-level invariants (event shapes, fee accounting,
// authoritySet mutation, vote-drop on remove) so a regression
// surfaces here before reaching the integration layer.

import (
	"errors"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/governance/chainparams"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
)

// authRig is parallel to govFixture but pre-wires the
// AuthorityVoteStore and credits multiple authorities so vote-
// tally tests don't have to re-do that setup each time.
type authRig struct {
	t        *testing.T
	accounts *AccountStore
	store    *chainparams.InMemoryParamStore
	votes    *chainparams.InMemoryAuthorityVoteStore
	applier  *GovApplier
	pub      *recordingGovPublisher
}

func buildAuthRig(t *testing.T, authorities []string) *authRig {
	t.Helper()
	accounts := NewAccountStore()
	for _, a := range authorities {
		accounts.Credit(a, 100.0)
	}
	store := chainparams.NewInMemoryParamStore()
	votes := chainparams.NewInMemoryAuthorityVoteStore()
	applier := NewGovApplier(accounts, store, authorities)
	applier.SetAuthorityVoteStore(votes)
	pub := &recordingGovPublisher{}
	applier.Publisher = pub
	return &authRig{
		t: t, accounts: accounts, store: store,
		votes: votes, applier: applier, pub: pub,
	}
}

func authTx(sender, txID string, nonce uint64, op chainparams.AuthorityOp,
	address string, effectiveHeight uint64, memo string,
) *mempool.Tx {
	raw, err := chainparams.EncodeAuthoritySet(chainparams.AuthoritySetPayload{
		Kind:            chainparams.PayloadKindAuthoritySet,
		Op:              op,
		Address:         address,
		EffectiveHeight: effectiveHeight,
		Memo:            memo,
	})
	if err != nil {
		panic(err)
	}
	return &mempool.Tx{
		ID:         txID,
		Sender:     sender,
		Nonce:      nonce,
		Fee:        0.001,
		ContractID: chainparams.ContractID,
		Payload:    raw,
	}
}

// -----------------------------------------------------------------------------
// happy path: vote → cross → activate
// -----------------------------------------------------------------------------

func TestGovApplier_AuthorityVote_CrossAndActivate(t *testing.T) {
	r := buildAuthRig(t, []string{"alice", "bob", "carol"})
	target := "qsdm1new-authority"

	// alice votes; not crossed (threshold=2 at N=3).
	if err := r.applier.ApplyGovTx(
		authTx("alice", "tx-1", 0, chainparams.AuthorityOpAdd, target, 100, "onboarding dave"),
		50,
	); err != nil {
		t.Fatalf("alice vote: %v", err)
	}
	if len(r.pub.authEvents) != 1 || r.pub.authEvents[0].Kind != GovAuthorityEventVoted {
		t.Errorf("after alice vote, authEvents = %+v", r.pub.authEvents)
	}

	// bob votes — threshold crossed.
	if err := r.applier.ApplyGovTx(
		authTx("bob", "tx-2", 0, chainparams.AuthorityOpAdd, target, 100, ""),
		51,
	); err != nil {
		t.Fatalf("bob vote: %v", err)
	}
	var sawStaged bool
	for _, ev := range r.pub.authEvents {
		if ev.Kind == GovAuthorityEventStaged {
			sawStaged = true
		}
	}
	if !sawStaged {
		t.Error("threshold crossing did not emit authority-staged event")
	}

	// PromotePending at h=100 → applies the rotation.
	r.applier.PromotePending(100)
	got := r.applier.AuthorityList()
	want := []string{"alice", "bob", "carol", target}
	if len(got) != len(want) {
		t.Fatalf("post-activation list = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]=%q want %q", i, got[i], want[i])
		}
	}

	var sawActivated bool
	for _, ev := range r.pub.authEvents {
		if ev.Kind == GovAuthorityEventActivated && ev.Address == target {
			sawActivated = true
			if ev.AuthorityCount != 4 {
				t.Errorf("activated AuthorityCount = %d, want 4", ev.AuthorityCount)
			}
		}
	}
	if !sawActivated {
		t.Error("authority-activated event missing")
	}
}

// -----------------------------------------------------------------------------
// remove path: drops removed authority's open votes
// -----------------------------------------------------------------------------

func TestGovApplier_RemoveActivates_DropsOpenVotesByRemoved(t *testing.T) {
	r := buildAuthRig(t, []string{"alice", "bob", "carol"})

	// First, schedule alice's removal (carol + bob vote → cross).
	if err := r.applier.ApplyGovTx(
		authTx("bob", "rm-1", 0, chainparams.AuthorityOpRemove, "alice", 100, ""),
		10,
	); err != nil {
		t.Fatalf("bob remove vote: %v", err)
	}
	if err := r.applier.ApplyGovTx(
		authTx("carol", "rm-2", 0, chainparams.AuthorityOpRemove, "alice", 100, ""),
		11,
	); err != nil {
		t.Fatalf("carol remove vote: %v", err)
	}

	// Concurrently, alice + bob vote on adding "newcomer" but
	// carol has not joined yet → not crossed (threshold=2,
	// have 2 votes including alice and bob — wait, that DOES
	// cross). Use a 3rd open proposal that alice is part of
	// without bob being on board too: make it (carol +
	// alice) → need more authorities. Actually the assertion
	// matters only if dropping alice's vote leaves bob alone
	// → un-crossed. Let me redo:
	//
	// Open proposal: add "newcomer" with effective_height=200.
	// Voters = [alice]. Threshold at N=3 = 2 → not crossed.
	if err := r.applier.ApplyGovTx(
		authTx("alice", "add-1", 0, chainparams.AuthorityOpAdd, "newcomer", 200, ""),
		12,
	); err != nil {
		t.Fatalf("alice add vote: %v", err)
	}

	// Promote at h=100 → alice removed. The open add-newcomer
	// proposal should lose alice's vote; since she was the only
	// voter it should be abandoned entirely.
	r.applier.PromotePending(100)

	got := r.applier.AuthorityList()
	for _, a := range got {
		if a == "alice" {
			t.Errorf("alice still on AuthorityList post-removal: %v", got)
		}
	}
	// Verify the abandoned proposal disappeared.
	addKey := chainparams.AuthorityVoteKey{
		Op: chainparams.AuthorityOpAdd, Address: "newcomer", EffectiveHeight: 200,
	}
	if _, exists := r.votes.Lookup(addKey); exists {
		t.Error("orphaned add-proposal not abandoned after alice removed")
	}
	// Verify an authority-abandoned event was emitted.
	var sawAbandoned bool
	for _, ev := range r.pub.authEvents {
		if ev.Kind == GovAuthorityEventAbandoned && ev.Address == "newcomer" {
			sawAbandoned = true
		}
	}
	if !sawAbandoned {
		t.Error("authority-abandoned event missing")
	}
}

// -----------------------------------------------------------------------------
// would-empty refusal
// -----------------------------------------------------------------------------

func TestGovApplier_RemoveLastAuthority_RefusedAtPromotion(t *testing.T) {
	r := buildAuthRig(t, []string{"alice"})

	// Single authority vote-removes themselves; threshold=1
	// for N=1 → instantly crosses.
	if err := r.applier.ApplyGovTx(
		authTx("alice", "rm-self", 0, chainparams.AuthorityOpRemove, "alice", 50, "I quit"),
		10,
	); err != nil {
		t.Fatalf("alice self-remove vote: %v", err)
	}
	r.applier.PromotePending(50)

	got := r.applier.AuthorityList()
	if len(got) != 1 || got[0] != "alice" {
		t.Errorf("alice was removed from solo AuthorityList: got %v, want [alice]", got)
	}
	// A rejected event should be on the publisher.
	var sawRejected bool
	for _, ev := range r.pub.authEvents {
		if ev.Kind == GovAuthorityEventRejected &&
			ev.RejectReason == GovRejectReasonAuthorityWouldEmpty {
			sawRejected = true
		}
	}
	if !sawRejected {
		t.Error("would-empty rejection event missing")
	}
}

// -----------------------------------------------------------------------------
// rejection paths
// -----------------------------------------------------------------------------

func TestGovApplier_AuthorityVote_RejectsNonAuthoritySender(t *testing.T) {
	r := buildAuthRig(t, []string{"alice"})
	r.accounts.Credit("eve", 100)
	err := r.applier.ApplyGovTx(
		authTx("eve", "tx-bad", 0, chainparams.AuthorityOpAdd, "newcomer", 50, ""),
		10,
	)
	if !errors.Is(err, chainparams.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestGovApplier_AuthorityVote_RejectsAddOfExistingAuthority(t *testing.T) {
	r := buildAuthRig(t, []string{"alice", "bob"})
	err := r.applier.ApplyGovTx(
		authTx("alice", "tx-dup", 0, chainparams.AuthorityOpAdd, "bob", 50, ""),
		10,
	)
	if !errors.Is(err, chainparams.ErrAuthorityAlreadyPresent) {
		t.Errorf("err = %v, want ErrAuthorityAlreadyPresent", err)
	}
}

func TestGovApplier_AuthorityVote_RejectsRemoveOfNonAuthority(t *testing.T) {
	r := buildAuthRig(t, []string{"alice", "bob"})
	err := r.applier.ApplyGovTx(
		authTx("alice", "tx-bad-rm", 0, chainparams.AuthorityOpRemove, "ghost", 50, ""),
		10,
	)
	if !errors.Is(err, chainparams.ErrAuthorityNotPresent) {
		t.Errorf("err = %v, want ErrAuthorityNotPresent", err)
	}
}

func TestGovApplier_AuthorityVote_RejectsDuplicateVote(t *testing.T) {
	r := buildAuthRig(t, []string{"alice", "bob", "carol"})
	if err := r.applier.ApplyGovTx(
		authTx("alice", "tx-1", 0, chainparams.AuthorityOpAdd, "newcomer", 50, ""),
		10,
	); err != nil {
		t.Fatalf("first vote: %v", err)
	}
	err := r.applier.ApplyGovTx(
		authTx("alice", "tx-2", 1, chainparams.AuthorityOpAdd, "newcomer", 50, ""),
		11,
	)
	if !errors.Is(err, chainparams.ErrDuplicateVote) {
		t.Errorf("err = %v, want ErrDuplicateVote", err)
	}
}

func TestGovApplier_AuthorityVote_RejectsHeightInPast(t *testing.T) {
	r := buildAuthRig(t, []string{"alice"})
	err := r.applier.ApplyGovTx(
		authTx("alice", "tx-bad-h", 0, chainparams.AuthorityOpAdd, "newcomer", 5, ""),
		100,
	)
	if !errors.Is(err, chainparams.ErrEffectiveHeightInPast) {
		t.Errorf("err = %v, want ErrEffectiveHeightInPast", err)
	}
}

func TestGovApplier_AuthorityVote_RejectsHeightTooFar(t *testing.T) {
	r := buildAuthRig(t, []string{"alice"})
	err := r.applier.ApplyGovTx(
		authTx("alice", "tx-bad-h2", 0, chainparams.AuthorityOpAdd,
			"newcomer", 100+chainparams.MaxActivationDelay+1, ""),
		100,
	)
	if !errors.Is(err, chainparams.ErrEffectiveHeightTooFar) {
		t.Errorf("err = %v, want ErrEffectiveHeightTooFar", err)
	}
}

func TestGovApplier_AuthorityVote_RejectsWhenGovernanceDisabled(t *testing.T) {
	r := buildAuthRig(t, nil)
	r.accounts.Credit("alice", 100)
	err := r.applier.ApplyGovTx(
		authTx("alice", "tx-x", 0, chainparams.AuthorityOpAdd, "newcomer", 50, ""),
		10,
	)
	if !errors.Is(err, chainparams.ErrGovernanceNotConfigured) {
		t.Errorf("err = %v, want ErrGovernanceNotConfigured", err)
	}
}

// -----------------------------------------------------------------------------
// fee semantics — burned on the applier path even when the
// vote is a no-op (e.g. duplicate after the threshold).
// -----------------------------------------------------------------------------

func TestGovApplier_AuthorityVote_FeeBurnedOnAccept(t *testing.T) {
	r := buildAuthRig(t, []string{"alice"})
	preAcc, _ := r.accounts.Get("alice")
	pre := preAcc.Balance

	if err := r.applier.ApplyGovTx(
		authTx("alice", "tx-1", 0, chainparams.AuthorityOpAdd, "newcomer", 50, ""),
		10,
	); err != nil {
		t.Fatalf("alice vote: %v", err)
	}
	postAcc, _ := r.accounts.Get("alice")
	if postAcc.Balance >= pre {
		t.Errorf("fee not debited: pre=%v post=%v", pre, postAcc.Balance)
	}
}
