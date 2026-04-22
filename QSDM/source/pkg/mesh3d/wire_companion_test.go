package mesh3d

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildMeshCompanionFromWalletJSON_roundTrip(t *testing.T) {
	wallet := map[string]interface{}{
		"id":           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"sender":       "addr1",
		"recipient":    "addr2",
		"amount":       1.0,
		"fee":          0.1,
		"geotag":       "US",
		"parent_cells": []string{"p1", "p2"},
		"signature":    strings.Repeat("ab", 50), // 100 hex chars
	}

	raw, err := json.Marshal(wallet)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := BuildMeshCompanionFromWalletJSON(raw, []string{"lab-a", "lab-b"}, "sm1")
	if err != nil {
		t.Fatal(err)
	}
	tx, sub, err := ParseMeshPubsubWire(wire)
	if err != nil {
		t.Fatal(err)
	}
	if sub != "sm1" || len(tx.ParentCells) != 3 {
		t.Fatalf("sub=%q parents=%d", sub, len(tx.ParentCells))
	}
	if !json.Valid(tx.Data) {
		t.Fatal("payload not json")
	}
}
