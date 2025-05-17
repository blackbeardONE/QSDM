package tests

import (
    "testing"
    "time"

    "github.com/blackbeardONE/QSDM/pkg/submesh"
    "github.com/blackbeardONE/QSDM/pkg/governance"
    "github.com/blackbeardONE/QSDM/pkg/mesh3d"
    "github.com/blackbeardONE/QSDM/pkg/quarantine"
    "github.com/blackbeardONE/QSDM/pkg/reputation"
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
    snapshot.CastVote(tokenID, 10)

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

    // Simulate 3D mesh validation (dummy validation)
    valid := validator.ValidateTransaction([]byte("tx1"), [][]byte{[]byte("parent1"), []byte("parent2"), []byte("parent3")})
    if !valid {
        t.Errorf("Expected transaction to be valid")
    }

    // Simulate quarantine due to invalid transactions
    quarantineMgr.RecordTransactionValidity("fastlane", false)
    quarantineMgr.RecordTransactionValidity("fastlane", false)
    quarantineMgr.RecordTransactionValidity("fastlane", true) // 2/3 invalid rate > 0.5 threshold

    if !quarantineMgr.IsQuarantined("fastlane") {
        t.Errorf("Expected fastlane to be quarantined")
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
    results := snapshot.TallyVotes()
    if results[tokenID] != 10 {
        t.Errorf("Expected vote weight 10 for token %s, got %d", tokenID, results[tokenID])
    }
}
