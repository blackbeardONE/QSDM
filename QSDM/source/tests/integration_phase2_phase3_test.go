package tests

import (
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/governance"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/reputation"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
)

func TestPhase2Phase3Integration(t *testing.T) {
	// Initialize Dynamic Submesh Manager
	dsManager := submesh.NewDynamicSubmeshManager()
	ds1 := &submesh.DynamicSubmesh{
		Name:          "fastlane",
		FeeThreshold:  0.01,
		PriorityLevel: 10,
		GeoTags:       []string{"US", "EU"},
	}
	dsManager.AddOrUpdateSubmesh(ds1)

	// Initialize Governance Snapshot Voting
	snapshot := governance.NewSnapshot("test_snapshot", 1*time.Minute)
	tokenID := "token123"
	snapshot.SetTokenWeight(tokenID, 10)
	snapshot.CastVote(tokenID, governance.VoteYes)

	// Initialize 3D Mesh Validator
	validator := mesh3d.NewMesh3DValidator()

	// Initialize Quarantine Manager
	quarantineMgr := quarantine.NewQuarantineManager(0.5)

	// Initialize Reputation Manager
	repMgr := reputation.NewReputationManager()
	nodeID := "node1"
	repMgr.SetStake(nodeID, 100)

	// Simulate routing a transaction
	ds, err := dsManager.RouteTransaction(0.02, "US")
	if err != nil {
		t.Fatalf("Failed to route transaction: %v", err)
	}
	if ds.Name != "fastlane" {
		t.Errorf("Expected fastlane submesh, got %s", ds.Name)
	}

	// Simulate 3D mesh validation
	// Parent cell data must be at least 32 bytes (mesh3d validator requirement)
	parentData1 := make([]byte, 64)
	copy(parentData1, "parent1_data_123456789012345678901234567890")
	parentData2 := make([]byte, 64)
	copy(parentData2, "parent2_data_123456789012345678901234567890")
	parentData3 := make([]byte, 64)
	copy(parentData3, "parent3_data_123456789012345678901234567890")
	
	tx := &mesh3d.Transaction{
		ID: "tx1",
		ParentCells: []mesh3d.ParentCell{
			{ID: "parent1", Data: parentData1},
			{ID: "parent2", Data: parentData2},
			{ID: "parent3", Data: parentData3},
		},
		Data: []byte("tx1"),
	}
	valid, err := validator.ValidateTransaction(tx)
	if err != nil {
		t.Fatalf("Validation error: %v", err)
	}
	if !valid {
		t.Errorf("Expected transaction to be valid")
	}

	// Simulate quarantine due to invalid transactions
	// Quarantine manager requires at least 10 transactions before checking threshold
	// Record 11 invalid transactions to ensure quarantine is triggered (>50% invalid with threshold 0.5)
	for i := 0; i < 11; i++ {
		quarantineMgr.RecordTransaction("fastlane", false)
	}

	if !quarantineMgr.IsQuarantined("fastlane") {
		t.Errorf("Expected fastlane to be quarantined after 11 invalid transactions")
	}

	// Remove quarantine and check
	quarantineMgr.RemoveQuarantine("fastlane")
	if quarantineMgr.IsQuarantined("fastlane") {
		t.Errorf("Expected fastlane quarantine to be removed")
	}

	// Simulate reputation penalty
	repMgr.Penalize(nodeID, 0.4)
	rep := repMgr.GetReputation(nodeID)
	if rep != 0.6 {
		t.Errorf("Expected reputation 0.6 after penalty, got %f", rep)
	}

	// Check governance voting results
	yes, _, _ := snapshot.TallyVotes()
	if yes != 10 {
		t.Errorf("Expected vote weight 10 for token %s, got %f", tokenID, yes)
	}
}
