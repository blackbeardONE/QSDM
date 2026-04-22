//go:build !validator_only
// +build !validator_only

package roleguard

import (
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/config"
)

func TestMustMatchRole_FullBuild(t *testing.T) {
	cases := []struct {
		name          string
		role          config.NodeRole
		miningEnabled bool
		wantErr       bool
		errContains   string
	}{
		{"validator+mining_off_ok", config.NodeRoleValidator, false, false, ""},
		{"miner+mining_on_ok", config.NodeRoleMiner, true, false, ""},
		{"validator+mining_on_rejected", config.NodeRoleValidator, true, true, "incompatible"},
		{"miner+mining_off_rejected", config.NodeRoleMiner, false, true, "requires node.mining_enabled=true"},
		{"unknown_role_rejected", config.NodeRole("guardian"), false, true, "invalid node role"},
		{"empty_role_rejected", config.NodeRole(""), false, true, "invalid node role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := MustMatchRole(tc.role, tc.miningEnabled)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (role=%q mining=%v)", tc.role, tc.miningEnabled)
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errContains)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestBuildProfile_Full(t *testing.T) {
	if BuildProfile != "full" {
		t.Errorf("BuildProfile = %q, want %q", BuildProfile, "full")
	}
}
