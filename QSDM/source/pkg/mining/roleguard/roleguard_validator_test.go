//go:build validator_only
// +build validator_only

package roleguard

import (
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/config"
)

func TestMustMatchRole_ValidatorOnly(t *testing.T) {
	cases := []struct {
		name          string
		role          config.NodeRole
		miningEnabled bool
		wantErr       bool
		errContains   string
	}{
		{"validator+mining_off_ok", config.NodeRoleValidator, false, false, ""},
		{"validator+mining_on_rejected", config.NodeRoleValidator, true, true, "validator_only"},
		{"miner_rejected", config.NodeRoleMiner, true, true, "refuses node.role="},
		{"unknown_rejected", config.NodeRole("gpu-validator"), false, true, "invalid node role"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := MustMatchRole(tc.role, tc.miningEnabled)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (role=%q)", tc.role)
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

func TestBuildProfile_ValidatorOnly(t *testing.T) {
	if BuildProfile != "validator_only" {
		t.Errorf("BuildProfile = %q, want %q", BuildProfile, "validator_only")
	}
}
