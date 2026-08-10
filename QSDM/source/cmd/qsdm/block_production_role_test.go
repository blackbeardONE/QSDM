package main

import "testing"

func TestResolveBlockProductionRole(t *testing.T) {
	tests := []struct {
		name            string
		solo            bool
		networkProducer bool
		want            blockProductionRole
		wantLocal       bool
		wantGenesis     bool
		wantErr         bool
	}{
		{name: "network follower", want: blockProductionRoleNetworkFollower},
		{name: "solo", solo: true, want: blockProductionRoleSolo, wantLocal: true, wantGenesis: true},
		{name: "network producer", networkProducer: true, want: blockProductionRoleNetworkProducer, wantLocal: true},
		{name: "conflicting roles", solo: true, networkProducer: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveBlockProductionRole(tt.solo, tt.networkProducer)
			if tt.wantErr {
				if err == nil {
					t.Fatal("resolveBlockProductionRole() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBlockProductionRole() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("resolveBlockProductionRole() = %q, want %q", got, tt.want)
			}
			if got.localProductionEnabled() != tt.wantLocal {
				t.Fatalf("localProductionEnabled() = %t, want %t", got.localProductionEnabled(), tt.wantLocal)
			}
			if got.localGenesisEnabled() != tt.wantGenesis {
				t.Fatalf("localGenesisEnabled() = %t, want %t", got.localGenesisEnabled(), tt.wantGenesis)
			}
		})
	}
}
