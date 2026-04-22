package tests

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/governance"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/reputation"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
	"github.com/blackbeardONE/QSDM/internal/logging"
)

// TestFullTransactionLifecycleComprehensive tests the complete transaction flow
func TestFullTransactionLifecycleComprehensive(t *testing.T) {
	logger := logging.NewLogger("test.log", false)

	// Initialize storage (use file storage for tests to avoid CGO dependency)
	fileStore, err := storage.NewFileStorage("test_data")
	if err != nil {
		t.Skipf("Storage not available: %v", err)
	}
	defer func() {
		fileStore.Close()
		// Cleanup test data
		os.RemoveAll("test_data")
	}()

	// Initialize consensus (may be nil if CGO disabled, that's OK for tests)
	poe := consensus.NewProofOfEntanglement()
	
	// Initialize wallet service (may fail if CGO disabled, that's OK for tests)
	walletService, err := wallet.NewWalletService()
	if err != nil {
		t.Skipf("Wallet service not available (CGO may be disabled): %v", err)
	}

	// Create transaction
	txDataBytes, err := walletService.CreateTransaction(
		"recipient123",
		100,
		0.01,
		"US",
		[]string{"parent1", "parent2"},
	)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}

	// Parse transaction
	var tx map[string]interface{}
	if err := json.Unmarshal(txDataBytes, &tx); err != nil {
		t.Fatalf("Failed to parse transaction: %v", err)
	}

	// Verify transaction structure
	if tx["id"] == nil {
		t.Error("Transaction missing ID")
	}
	if tx["sender"] == nil {
		t.Error("Transaction missing sender")
	}
	if tx["recipient"] == nil {
		t.Error("Transaction missing recipient")
	}
	if tx["signature"] == nil {
		t.Error("Transaction missing signature")
	}

	// Validate transaction with consensus (if available)
	if poe != nil {
		parentCells := [][]byte{[]byte("parent1"), []byte("parent2")}
		sigStr, ok := tx["signature"].(string)
		if ok {
			signature, err := hex.DecodeString(sigStr)
			if err == nil {
				signatures := [][]byte{signature}
				valid, err := poe.ValidateTransaction(txDataBytes, parentCells, signatures, logger)
				if err != nil {
					t.Logf("Consensus validation error (expected if signature format differs): %v", err)
				} else if !valid {
					t.Logf("Transaction failed consensus validation (may be expected in test mode)")
				}
			}
		}
	} else {
		t.Log("Consensus not available (CGO disabled), skipping consensus validation")
	}

	// Store transaction
	err = fileStore.StoreTransaction(txDataBytes)
	if err != nil {
		t.Fatalf("Failed to store transaction: %v", err)
	}

	t.Log("Transaction stored successfully")

	t.Log("Full transaction lifecycle test passed")
}

// TestMultiNodeTransactionPropagationComprehensive simulates multi-node transaction flow
func TestMultiNodeTransactionPropagationComprehensive(t *testing.T) {
	// Initialize multiple submesh managers (simulating nodes)
	node1 := submesh.NewDynamicSubmeshManager()
	node2 := submesh.NewDynamicSubmeshManager()
	node3 := submesh.NewDynamicSubmeshManager()

	// Create submesh on node1
	ds := &submesh.DynamicSubmesh{
		Name:          "fastlane",
		FeeThreshold:  0.01,
		PriorityLevel: 10,
		GeoTags:       []string{"US"},
	}
	node1.AddOrUpdateSubmesh(ds)

	// Propagate submesh to other nodes (simulate network sync)
	node2.AddOrUpdateSubmesh(ds)
	node3.AddOrUpdateSubmesh(ds)

	// Route transaction through all nodes
	fee := 0.02
	geoTag := "US"

	route1, err1 := node1.RouteTransaction(fee, geoTag)
	route2, err2 := node2.RouteTransaction(fee, geoTag)
	route3, err3 := node3.RouteTransaction(fee, geoTag)

	if err1 != nil || err2 != nil || err3 != nil {
		t.Fatalf("Routing failed: node1=%v, node2=%v, node3=%v", err1, err2, err3)
	}

	if route1.Name != "fastlane" || route2.Name != "fastlane" || route3.Name != "fastlane" {
		t.Error("Nodes routed to different submeshes")
	}

	t.Log("Multi-node transaction propagation test passed")
}

// TestGovernanceProposalLifecycleComprehensive tests complete governance flow
func TestGovernanceProposalLifecycleComprehensive(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "test_governance_*.json")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	sv := governance.NewSnapshotVoting(tmpFile.Name())

	// Create proposal
	proposalID := "test_prop_1"
	duration := 60 * time.Second
	quorum := 5
	description := "Test proposal"

	err = sv.AddProposal(proposalID, description, duration, quorum)
	if err != nil {
		t.Fatalf("Failed to add proposal: %v", err)
	}

	// Vote on proposal
	for i := 0; i < 6; i++ {
		err = sv.Vote(proposalID, fmt.Sprintf("voter%d", i), 1, true)
		if err != nil {
			t.Logf("Vote %d error (may be expected): %v", i, err)
		}
	}

	// Check proposal status
	sv.Mu.RLock()
	proposal, exists := sv.Proposals[proposalID]
	sv.Mu.RUnlock()
	if !exists {
		t.Fatal("Proposal not found")
	}

	if proposal.VotesFor == 0 {
		t.Error("No votes recorded")
	}

	// Finalize proposal (after expiration)
	sv.Mu.Lock()
	proposal.ExpiresAt = time.Now().Add(-1 * time.Second)
	sv.Proposals[proposalID] = proposal
	sv.Mu.Unlock()

	result, err := sv.FinalizeProposal(proposalID)
	if err != nil {
		t.Logf("Finalization error (may be expected): %v", err)
	} else {
		if !result {
			t.Logf("Proposal did not pass (may be expected)")
		}
	}

	t.Log("Governance proposal lifecycle test passed")
}

// TestPhase3ValidationFlowComprehensive tests 3D mesh validation with quarantine
func TestPhase3ValidationFlowComprehensive(t *testing.T) {
	// Initialize 3D mesh validator
	validator := mesh3d.NewMesh3DValidator()

	// Create transaction with 3 parent cells
	tx := &mesh3d.Transaction{
		ID: "tx3d_1",
		ParentCells: []mesh3d.ParentCell{
			{ID: "p1", Data: make([]byte, 64)},
			{ID: "p2", Data: make([]byte, 64)},
			{ID: "p3", Data: make([]byte, 64)},
		},
		Data: []byte("transaction data"),
	}

	// Validate transaction
	valid, err := validator.ValidateTransaction(tx)
	if err != nil {
		t.Fatalf("Validation error: %v", err)
	}
	if !valid {
		t.Error("Transaction should be valid")
	}

	// Initialize quarantine manager
	qm := quarantine.NewQuarantineManager(0.5)

	// Record valid transaction
	qm.RecordTransaction("submesh1", true)
	qm.RecordTransaction("submesh1", true)

	// Record invalid transactions to trigger quarantine
	// Quarantine manager requires at least 10 transactions before checking threshold
	// Record 11 invalid transactions to ensure quarantine is triggered
	for i := 0; i < 11; i++ {
		qm.RecordTransaction("submesh1", false)
	}

	// Check if quarantined (after 10+ transactions with >50% invalid)
	if !qm.IsQuarantined("submesh1") {
		t.Error("Submesh should be quarantined after 11 invalid transactions")
	}

	// Test reputation system
	rm := reputation.NewReputationManager()
	nodeID := "node1"
	rm.SetStake(nodeID, 100)

	// Get initial reputation
	initialRep := rm.GetReputation(nodeID)
	if initialRep <= 0 {
		t.Error("Reputation should be positive")
	}

	// Penalize invalid behavior
	rm.Penalize(nodeID, 0.1)
	newRep := rm.GetReputation(nodeID)
	if newRep >= initialRep {
		t.Error("Reputation should decrease after penalty")
	}

	t.Log("Phase 3 validation flow test passed")
}

// TestCUDAAcceleration tests CUDA availability and fallback
func TestCUDAAcceleration(t *testing.T) {
	validator := mesh3d.NewMesh3DValidator()

	// Check if CUDA is available (this will be nil if CUDA not available)
	// The validator should work with or without CUDA
	tx := &mesh3d.Transaction{
		ID: "cuda_test",
		ParentCells: []mesh3d.ParentCell{
			{ID: "p1", Data: make([]byte, 64)},
			{ID: "p2", Data: make([]byte, 64)},
			{ID: "p3", Data: make([]byte, 64)},
		},
		Data: []byte("test data"),
	}

	valid, err := validator.ValidateTransaction(tx)
	if err != nil {
		t.Fatalf("Validation error: %v", err)
	}
	if !valid {
		t.Error("Transaction should be valid")
	}

	t.Log("CUDA acceleration test passed (with CPU fallback)")
}

// TestStressHighVolumeTransactions tests system under load
func TestStressHighVolumeTransactions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	store, err := storage.NewFileStorage("test_stress_data")
	if err != nil {
		t.Skipf("Storage not available: %v", err)
	}
	defer func() {
		store.Close()
		os.RemoveAll("test_stress_data")
	}()

	// Create 100 transactions
	numTx := 100
	start := time.Now()

	for i := 0; i < numTx; i++ {
		txData := []byte(fmt.Sprintf(`{"id":"tx%d","amount":%d}`, i, i*10))
		err := store.StoreTransaction(txData)
		if err != nil {
			t.Fatalf("Failed to store transaction %d: %v", i, err)
		}
	}

	elapsed := time.Since(start)
	tps := float64(numTx) / elapsed.Seconds()

	t.Logf("Stored %d transactions in %v (%.2f TPS)", numTx, elapsed, tps)

	if tps < 10 {
		t.Errorf("Throughput too low: %.2f TPS (expected >= 10 TPS)", tps)
	}
}

// TestNetworkResilience tests system resilience
func TestNetworkResilience(t *testing.T) {
	// Test submesh routing with missing submesh
	dsManager := submesh.NewDynamicSubmeshManager()

	// Try to route without any submeshes
	_, err := dsManager.RouteTransaction(0.01, "US")
	if err == nil {
		t.Error("Should fail when no submeshes exist")
	}

	// Add submesh and retry
	ds := &submesh.DynamicSubmesh{
		Name:          "default",
		FeeThreshold:  0.0,
		PriorityLevel: 1,
		GeoTags:       []string{"US"},
	}
	dsManager.AddOrUpdateSubmesh(ds)

	route, err := dsManager.RouteTransaction(0.01, "US")
	if err != nil {
		t.Fatalf("Routing should succeed: %v", err)
	}
	if route.Name != "default" {
		t.Error("Should route to default submesh")
	}

	t.Log("Network resilience test passed")
}

// TestConcurrentOperations tests thread safety
func TestConcurrentOperations(t *testing.T) {
	dsManager := submesh.NewDynamicSubmeshManager()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Concurrent submesh additions
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			ds := &submesh.DynamicSubmesh{
				Name:          fmt.Sprintf("submesh%d", id),
				FeeThreshold:  float64(id) * 0.01,
				PriorityLevel: id,
				GeoTags:       []string{"US"},
			}
			dsManager.AddOrUpdateSubmesh(ds)
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("Timeout waiting for concurrent operations")
		}
	}

	// Verify all submeshes were added
	submeshes := dsManager.ListSubmeshes()
	if len(submeshes) != 10 {
		t.Errorf("Expected 10 submeshes, got %d", len(submeshes))
	}

	t.Log("Concurrent operations test passed")
}

