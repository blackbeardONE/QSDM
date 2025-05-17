package submesh

import (
    "testing"
)

func TestDynamicSubmeshManager(t *testing.T) {
    manager := NewDynamicSubmeshManager()

    ds1 := &DynamicSubmesh{
        Name:          "fastlane",
        FeeThreshold:  0.01,
        PriorityLevel: 10,
        GeoTags:       []string{"US", "EU"},
    }
    ds2 := &DynamicSubmesh{
        Name:          "slowlane",
        FeeThreshold:  0.001,
        PriorityLevel: 1,
        GeoTags:       []string{"US"},
    }

    manager.AddOrUpdateSubmesh(ds1)
    manager.AddOrUpdateSubmesh(ds2)

    // Test GetSubmesh
    got, err := manager.GetSubmesh("fastlane")
    if err != nil {
        t.Fatalf("GetSubmesh failed: %v", err)
    }
    if got.Name != "fastlane" {
        t.Errorf("Expected fastlane, got %s", got.Name)
    }

    // Test RouteTransaction with high fee and US geoTag
    ds, err := manager.RouteTransaction(0.02, "US")
    if err != nil {
        t.Fatalf("RouteTransaction failed: %v", err)
    }
    if ds.Name != "fastlane" {
        t.Errorf("Expected fastlane, got %s", ds.Name)
    }

    // Test RouteTransaction with low fee and US geoTag
    ds, err = manager.RouteTransaction(0.001, "US")
    if err != nil {
        t.Fatalf("RouteTransaction failed: %v", err)
    }
    if ds.Name != "slowlane" {
        t.Errorf("Expected slowlane, got %s", ds.Name)
    }

    // Test RouteTransaction with no matching geoTag
    _, err = manager.RouteTransaction(0.01, "ASIA")
    if err == nil {
        t.Errorf("Expected error for no matching submesh, got nil")
    }

    // Test RemoveSubmesh
    err = manager.RemoveSubmesh("fastlane")
    if err != nil {
        t.Fatalf("RemoveSubmesh failed: %v", err)
    }
    _, err = manager.GetSubmesh("fastlane")
    if err == nil {
        t.Errorf("Expected error for removed submesh, got nil")
    }

    // Additional tests

    // Test GetSubmesh for non-existent submesh
    _, err = manager.GetSubmesh("nonexistent")
    if err == nil {
        t.Errorf("Expected error for non-existent submesh, got nil")
    }

    // Test AddOrUpdateSubmesh updates existing submesh
    dsUpdate := &DynamicSubmesh{
        Name:          "slowlane",
        FeeThreshold:  0.005,
        PriorityLevel: 5,
        GeoTags:       []string{"US", "ASIA"},
    }
    manager.AddOrUpdateSubmesh(dsUpdate)
    updated, err := manager.GetSubmesh("slowlane")
    if err != nil {
        t.Fatalf("GetSubmesh failed after update: %v", err)
    }
    if updated.FeeThreshold != 0.005 || updated.PriorityLevel != 5 || len(updated.GeoTags) != 2 {
        t.Errorf("Submesh update failed, got %+v", updated)
    }

    // Test RouteTransaction with updated submesh
    ds, err = manager.RouteTransaction(0.004, "ASIA")
    if err != nil {
        t.Fatalf("RouteTransaction failed for updated submesh: %v", err)
    }
    if ds.Name != "slowlane" {
        t.Errorf("Expected slowlane for ASIA, got %s", ds.Name)
    }

    // Test RouteTransaction with empty submesh list
    emptyManager := NewDynamicSubmeshManager()
    _, err = emptyManager.RouteTransaction(0.01, "US")
    if err == nil {
        t.Errorf("Expected error for empty submesh list, got nil")
    }
}
