package chainparams

// persist_authority_test.go: extends persist_test.go with
// authority-rotation snapshot round-trips. Two themes:
//
//   - SaveSnapshotWith / LoadOrNewWith preserve voter sets,
//     Crossed flags, and proposal ordering across a save/load
//     cycle.
//
//   - v1 ↔ v2 compatibility: a v2 binary loading a v1 snapshot
//     reads parameters cleanly and starts with an empty vote
//     store; a v1 binary loading a v2 snapshot is correctly
//     refused (unsupported version).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// -----------------------------------------------------------------------------
// SaveSnapshotWith + LoadOrNewWith
// -----------------------------------------------------------------------------

func TestSaveLoadWith_RoundTripAuthorityProposals(t *testing.T) {
	store := NewInMemoryParamStore()
	votes := NewInMemoryAuthorityVoteStore()

	openKey := AuthorityVoteKey{
		Op: AuthorityOpAdd, Address: "qsdm1adding-this", EffectiveHeight: 200,
	}
	crossedKey := AuthorityVoteKey{
		Op: AuthorityOpRemove, Address: "qsdm1retiring", EffectiveHeight: 150,
	}
	if _, _, err := votes.RecordVote(openKey,
		AuthorityVote{Voter: "alice", SubmittedAtHeight: 100, Memo: "open vote"},
		4,
	); err != nil {
		t.Fatalf("open vote: %v", err)
	}
	if _, _, err := votes.RecordVote(crossedKey,
		AuthorityVote{Voter: "alice", SubmittedAtHeight: 100, Memo: "crossed vote"},
		1, // single-auth context → instantly crossed
	); err != nil {
		t.Fatalf("crossed vote: %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "rotation.json")
	if err := SaveSnapshotWith(store, votes, path); err != nil {
		t.Fatalf("SaveSnapshotWith: %v", err)
	}

	_, loadedVotes, err := LoadOrNewWith(path)
	if err != nil {
		t.Fatalf("LoadOrNewWith: %v", err)
	}
	props := loadedVotes.AllProposals()
	if len(props) != 2 {
		t.Fatalf("loaded proposals = %d, want 2", len(props))
	}
	openLoaded, ok := loadedVotes.Lookup(openKey)
	if !ok {
		t.Fatal("open proposal lost across save/load")
	}
	if openLoaded.Crossed {
		t.Error("open proposal incorrectly marked Crossed after load")
	}
	if len(openLoaded.Voters) != 1 || openLoaded.Voters[0].Voter != "alice" {
		t.Errorf("open Voters after load = %+v", openLoaded.Voters)
	}
	if openLoaded.Voters[0].Memo != "open vote" {
		t.Errorf("open vote memo lost: got %q", openLoaded.Voters[0].Memo)
	}
	crossedLoaded, ok := loadedVotes.Lookup(crossedKey)
	if !ok {
		t.Fatal("crossed proposal lost across save/load")
	}
	if !crossedLoaded.Crossed {
		t.Error("crossed proposal lost its Crossed flag")
	}
}

func TestSaveSnapshotWith_NilVoteStoreOmitsAuthoritySection(t *testing.T) {
	store := NewInMemoryParamStore()
	dir := t.TempDir()
	path := filepath.Join(dir, "no-auth.json")

	if err := SaveSnapshotWith(store, nil, path); err != nil {
		t.Fatalf("SaveSnapshotWith: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var doc snapshotDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(doc.AuthorityProposals) != 0 {
		t.Errorf("authority_proposals should be omitted/empty, got %+v",
			doc.AuthorityProposals)
	}
}

// -----------------------------------------------------------------------------
// Backwards compatibility: v1 snapshot replays under v2 binary
// -----------------------------------------------------------------------------

func TestLoadOrNewWith_AcceptsV1Snapshot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.json")

	// Hand-craft a v1 snapshot doc (no authority_proposals).
	v1 := snapshotDoc{
		Version: 1,
		Active: map[string]uint64{
			string(ParamRewardBPS): 1234,
		},
	}
	b, _ := json.MarshalIndent(v1, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatalf("write v1 snapshot: %v", err)
	}

	store, votes, err := LoadOrNewWith(path)
	if err != nil {
		t.Fatalf("LoadOrNewWith(v1) failed: %v", err)
	}
	if v, _ := store.ActiveValue(string(ParamRewardBPS)); v != 1234 {
		t.Errorf("v1 active value lost: got %d, want 1234", v)
	}
	if len(votes.AllProposals()) != 0 {
		t.Errorf("v1 snapshot loaded with non-empty vote store: %+v",
			votes.AllProposals())
	}
}

// -----------------------------------------------------------------------------
// Forward compatibility: v3 snapshot is refused (silent drift would corrupt)
// -----------------------------------------------------------------------------

func TestLoadOrNewWith_RejectsFutureVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "v3.json")
	doc := snapshotDoc{Version: SnapshotVersion + 1}
	b, _ := json.Marshal(doc)
	_ = os.WriteFile(path, b, 0o600)

	if _, _, err := LoadOrNewWith(path); err == nil {
		t.Error("LoadOrNewWith should reject a future-version snapshot")
	}
}

// -----------------------------------------------------------------------------
// Malformed authority entries are dropped silently (forward-compat).
// -----------------------------------------------------------------------------

func TestLoadOrNewWith_DropsMalformedAuthorityProposals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "malformed.json")

	doc := snapshotDoc{
		Version: SnapshotVersion,
		AuthorityProposals: []snapshotAuthorityProposal{
			{Op: "rotate", Address: "x", EffectiveHeight: 1}, // unknown op
			{Op: AuthorityOpAdd, Address: "", EffectiveHeight: 1}, // empty addr
			{Op: AuthorityOpAdd, Address: "x", EffectiveHeight: 0}, // zero height
			{Op: AuthorityOpAdd, Address: "good", EffectiveHeight: 100,
				Voters: []snapshotAuthVote{{Voter: "alice", SubmittedAtHeight: 50}}},
		},
	}
	b, _ := json.Marshal(doc)
	_ = os.WriteFile(path, b, 0o600)

	_, votes, err := LoadOrNewWith(path)
	if err != nil {
		t.Fatalf("LoadOrNewWith: %v", err)
	}
	props := votes.AllProposals()
	if len(props) != 1 {
		t.Fatalf("loaded proposals = %d, want 1 (malformed dropped)", len(props))
	}
	if props[0].Address != "good" {
		t.Errorf("kept proposal addr = %q, want good", props[0].Address)
	}
}
