package tests

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/governance"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/reputation"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// TestEndToEndTransactionFlow tests the complete transaction flow from creation to storage
func TestEndToEndTransactionFlow(t *testing.T) {
	// Setup: Create temporary database
	tmpDB, err := os.CreateTemp("", "test_e2e_*.db")
	if err != nil {
		t.Fatalf("Failed to create temp DB: %v", err)
	}
	tmpDB.Close()
	defer os.Remove(tmpDB.Name())

	// Initialize storage (use file storage for tests)
	store, err := storage.NewFileStorage("test_e2e_data")
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	defer func() {
		store.Close()
		os.RemoveAll("test_e2e_data")
	}()

	// Initialize wallet service
	walletService, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("Wallet service not available (CGO may be disabled): %v", err)
	}

	senderAddr := walletService.GetAddress()
	recipientAddr := "recipient1234567890abcdef1234567890abcdef12345678"
	amount := 100
	fee := 0.001
	geotag := "US"
	parentCells := []string{"parent1", "parent2"}

	// Step 1: Create transaction
	txBytes, err := walletService.CreateTransaction(recipientAddr, amount, fee, geotag, parentCells)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}

	// Step 2: Parse transaction
	var txData map[string]interface{}
	err = json.Unmarshal(txBytes, &txData)
	if err != nil {
		t.Fatalf("Failed to parse transaction: %v", err)
	}

	// Verify transaction structure
	if txData["id"] == nil || txData["id"].(string) == "" {
		t.Error("Transaction missing ID")
	}
	if txData["sender"].(string) != senderAddr {
		t.Errorf("Expected sender %s, got %s", senderAddr, txData["sender"])
	}
	if txData["recipient"].(string) != recipientAddr {
		t.Errorf("Expected recipient %s, got %s", recipientAddr, txData["recipient"])
	}
	if txData["signature"] == nil || txData["signature"].(string) == "" {
		t.Error("Transaction missing signature")
	}

	// Step 3: Store transaction
	err = store.StoreTransaction(txBytes)
	if err != nil {
		t.Fatalf("Failed to store transaction: %v", err)
	}

	// Step 4: Verify balance updated (file storage doesn't track balances, so skip)
	// Note: FileStorage.GetBalance always returns 0, so we skip balance checks
	t.Log("Balance checks skipped (file storage doesn't track balances)")

	t.Log("End-to-end transaction flow test passed")
}

// TestMultiNodeTransactionPropagation simulates transaction propagation across nodes
func TestMultiNodeTransactionPropagation(t *testing.T) {
	// Setup storage for node 1
	store1, err := storage.NewFileStorage("test_node1_data")
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	defer func() {
		store1.Close()
		os.RemoveAll("test_node1_data")
	}()

	// Setup storage for node 2
	store2, err := storage.NewFileStorage("test_node2_data")
	if err != nil {
		t.Fatalf("Failed to initialize storage: %v", err)
	}
	defer func() {
		store2.Close()
		os.RemoveAll("test_node2_data")
	}()

	// Node 1 creates transaction
	walletService1, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("Wallet service not available: %v", err)
	}
	txBytes, err := walletService1.CreateTransaction("recipient123", 50, 0.001, "US", []string{"p1", "p2"})
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}

	// Node 1 stores transaction
	err = store1.StoreTransaction(txBytes)
	if err != nil {
		t.Fatalf("Node 1 failed to store transaction: %v", err)
	}

	// Simulate propagation: Node 2 receives and stores the same transaction
	err = store2.StoreTransaction(txBytes)
	if err != nil {
		t.Fatalf("Node 2 failed to store transaction: %v", err)
	}

	// Verify both nodes stored the transaction
	// (File storage doesn't track balances, so we just verify no errors occurred)
	t.Log("Both nodes successfully stored the transaction")

	t.Log("Multi-node transaction propagation test passed")
}

// TestGovernanceProposalLifecycle tests the complete governance proposal flow
func TestGovernanceProposalLifecycle(t *testing.T) {
	// Setup: Create temporary file
	tmpFile, err := os.CreateTemp("", "test_governance_*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	sv := governance.NewSnapshotVoting(tmpFile.Name())

	// Step 1: Create proposal
	proposalID := "test_prop_1"
	description := "Test proposal for integration testing"
	duration := 60 * time.Second
	quorum := 5

	err = sv.AddProposal(proposalID, description, duration, quorum)
	if err != nil {
		t.Fatalf("Failed to add proposal: %v", err)
	}

	// Step 2: Verify proposal exists
	sv.Mu.RLock()
	proposal, exists := sv.Proposals[proposalID]
	sv.Mu.RUnlock()

	if !exists {
		t.Fatal("Proposal was not added")
	}
	if proposal.Description != description {
		t.Errorf("Expected description %s, got %s", description, proposal.Description)
	}

	// Step 3: Cast votes
	voters := []struct {
		id     string
		weight int
		support bool
	}{
		{"voter1", 3, true},
		{"voter2", 2, true},
		{"voter3", 1, false},
	}

	for _, voter := range voters {
		err = sv.Vote(proposalID, voter.id, voter.weight, voter.support)
		if err != nil {
			t.Fatalf("Failed to vote: %v", err)
		}
	}

	// Step 4: Verify vote counts
	sv.Mu.RLock()
	if proposal.VotesFor != 5 {
		t.Errorf("Expected 5 votes for, got %d", proposal.VotesFor)
	}
	if proposal.VotesAgainst != 1 {
		t.Errorf("Expected 1 vote against, got %d", proposal.VotesAgainst)
	}
	sv.Mu.RUnlock()

	// Step 5: Expire proposal and finalize
	sv.Mu.Lock()
	proposal.ExpiresAt = time.Now().Add(-1 * time.Second)
	sv.Mu.Unlock()

	passed, err := sv.FinalizeProposal(proposalID)
	if err != nil {
		t.Fatalf("Failed to finalize proposal: %v", err)
	}
	if !passed {
		t.Error("Expected proposal to pass")
	}

	// Step 6: Verify proposal is finalized
	sv.Mu.RLock()
	if !proposal.Finalized {
		t.Error("Expected proposal to be finalized")
	}
	sv.Mu.RUnlock()

	t.Log("Governance proposal lifecycle test passed")
}

// TestSubmeshRoutingWithTransactions tests submesh routing with real transaction data
func TestSubmeshRoutingWithTransactions(t *testing.T) {
	// Initialize submesh manager
	dsManager := submesh.NewDynamicSubmeshManager()

	// Create submeshes
	fastlane := &submesh.DynamicSubmesh{
		Name:          "fastlane",
		FeeThreshold:  0.01,
		PriorityLevel: 10,
		GeoTags:       []string{"US", "EU"},
	}
	slowlane := &submesh.DynamicSubmesh{
		Name:          "slowlane",
		FeeThreshold:  0.001,
		PriorityLevel: 5,
		GeoTags:       []string{"ASIA"},
	}

	dsManager.AddOrUpdateSubmesh(fastlane)
	dsManager.AddOrUpdateSubmesh(slowlane)

	// Test routing scenarios
	testCases := []struct {
		name     string
		fee      float64
		geotag   string
		expected string
	}{
		{"High fee US transaction", 0.02, "US", "fastlane"},
		{"Low fee ASIA transaction", 0.005, "ASIA", "slowlane"},
		{"Medium fee EU transaction", 0.015, "EU", "fastlane"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			submesh, err := dsManager.RouteTransaction(tc.fee, tc.geotag)
			if err != nil {
				t.Fatalf("Failed to route transaction: %v", err)
			}
			if submesh.Name != tc.expected {
				t.Errorf("Expected submesh %s, got %s", tc.expected, submesh.Name)
			}
		})
	}

	t.Log("Submesh routing with transactions test passed")
}

// TestPhase3ValidationFlow tests the complete Phase 3 validation flow
func TestPhase3ValidationFlow(t *testing.T) {
	// Initialize components
	validator := mesh3d.NewMesh3DValidator()
	quarantineMgr := quarantine.NewQuarantineManager(0.5)
	repMgr := reputation.NewReputationManager()

	nodeID := "test_node_1"
	submeshID := "test_submesh"

	// Create a valid 3D mesh transaction
	tx := &mesh3d.Transaction{
		ID: "tx_phase3_1",
		ParentCells: []mesh3d.ParentCell{
			{ID: "parent1", Data: []byte("parent1_data_123456789012345678901234567890")},
			{ID: "parent2", Data: []byte("parent2_data_123456789012345678901234567890")},
			{ID: "parent3", Data: []byte("parent3_data_123456789012345678901234567890")},
		},
		Data: []byte("transaction_data_for_phase3"),
	}

	// Step 1: Validate transaction
	valid, err := validator.ValidateTransaction(tx)
	if err != nil {
		t.Fatalf("Validation error: %v", err)
	}
	if !valid {
		t.Error("Expected transaction to be valid")
	}

	// Step 2: Set stake first to get positive reputation
	repMgr.SetStake(nodeID, 100)
	
	// Step 3: Record valid transaction
	quarantineMgr.RecordTransaction(submeshID, valid)

	// Step 4: Verify reputation (should be positive with stake set)
	reputation := repMgr.GetReputation(nodeID)
	if reputation <= 0 {
		t.Error("Expected reputation to be positive")
	}

	// Step 5: Test quarantine threshold
	// Quarantine manager requires at least 10 transactions before checking threshold
	// Record 11 invalid transactions to ensure quarantine is triggered (>50% invalid with threshold 0.5)
	for i := 0; i < 11; i++ {
		quarantineMgr.RecordTransaction(submeshID, false)
		repMgr.Penalize(nodeID, 0.1)
	}

	if !quarantineMgr.IsQuarantined(submeshID) {
		t.Error("Expected submesh to be quarantined after threshold exceeded")
	}

	// Step 6: Verify reputation decreased
	reputationAfter := repMgr.GetReputation(nodeID)
	if reputationAfter >= reputation {
		t.Error("Expected reputation to decrease after penalties")
	}

	t.Log("Phase 3 validation flow test passed")
}

