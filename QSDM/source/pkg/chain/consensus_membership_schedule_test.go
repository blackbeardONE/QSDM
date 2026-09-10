package chain

import (
	"reflect"
	"strings"
	"testing"
)

func TestConsensusMembershipScheduleResolvesByHeight(t *testing.T) {
	genesis := testConsensusMembership(1, 1, 1)
	genesis.EffectiveHeight = 0
	upgrade := testConsensusMembership(2, 3, 5)
	upgrade.EffectiveHeight = 100
	upgrade.Members[0] = testConsensusMember(11, 11, 7)

	// The constructor makes input ordering irrelevant.
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{upgrade, genesis})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := schedule.NetworkID(), "qsdm-mainnet"; got != want {
		t.Fatalf("NetworkID() = %q, want %q", got, want)
	}
	if got, want := schedule.SnapshotHeights(), []uint64{0, 100}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SnapshotHeights() = %v, want %v", got, want)
	}

	tests := []struct {
		height uint64
		want   uint64
	}{
		{height: 0, want: 0},
		{height: 99, want: 0},
		{height: 100, want: 100},
		{height: 101, want: 100},
	}
	for _, test := range tests {
		t.Run("height", func(t *testing.T) {
			got, ok := schedule.ForHeight(test.height)
			if !ok || got.EffectiveHeight != test.want {
				t.Fatalf("ForHeight(%d) = (%d, %t), want (%d, true)", test.height, got.EffectiveHeight, ok, test.want)
			}
		})
	}
}

func TestConsensusMembershipScheduleReportsNoMembershipBeforeFirstActivation(t *testing.T) {
	membership := testConsensusMembership(1)
	membership.EffectiveHeight = 10
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{membership})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := schedule.ForHeight(9); ok {
		t.Fatal("ForHeight before first activation returned a membership")
	}
}

func TestConsensusMembershipScheduleRejectsAmbiguousSnapshots(t *testing.T) {
	base := testConsensusMembership(1)
	base.EffectiveHeight = 10
	other := testConsensusMembership(2)
	other.EffectiveHeight = 10

	tests := []struct {
		name      string
		snapshots []ConsensusMembership
		want      string
	}{
		{
			name: "empty",
			want: "has no snapshots",
		},
		{
			name:      "duplicate-height",
			snapshots: []ConsensusMembership{base, other},
			want:      "both activate at height",
		},
		{
			name: "network-mismatch",
			snapshots: []ConsensusMembership{base, func() ConsensusMembership {
				m := testConsensusMembership(2)
				m.EffectiveHeight = 11
				m.NetworkID = "qsdm-testnet"
				return m
			}()},
			want: "has network ID",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewConsensusMembershipSchedule(test.snapshots)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewConsensusMembershipSchedule() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestConsensusMembershipScheduleReturnsDefensiveCopies(t *testing.T) {
	membership := testConsensusMembership(1, 1)
	membership.EffectiveHeight = 0
	schedule, err := NewConsensusMembershipSchedule([]ConsensusMembership{membership})
	if err != nil {
		t.Fatal(err)
	}

	first, ok := schedule.ForHeight(0)
	if !ok {
		t.Fatal("expected membership at height zero")
	}
	first.Members[0].Address = "changed"
	second, ok := schedule.ForHeight(0)
	if !ok || second.Members[0].Address == "changed" {
		t.Fatal("ForHeight returned aliases to the scheduled membership")
	}

	snapshots := schedule.Snapshots()
	snapshots[0].Members[0].Address = "changed-again"
	third, ok := schedule.ForHeight(0)
	if !ok || third.Members[0].Address == "changed-again" {
		t.Fatal("Snapshots returned aliases to the scheduled membership")
	}
}
