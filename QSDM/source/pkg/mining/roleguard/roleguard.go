//go:build !validator_only
// +build !validator_only

package roleguard

import (
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/config"
)

// BuildProfile reports which compile-time profile this binary was built with.
// The default profile is "full" — both validator and miner roles are
// supported. See the validator-only sibling file for the other value.
const BuildProfile = "full"

// MustMatchRole verifies that the runtime (role, miningEnabled) pair is
// consistent with the rules baked into pkg/config.Validate AND with the
// build-time profile of this binary.
//
// In the default ("full") build, both validator and miner roles are legal, so
// the only failure modes are:
//
//   - role is not a recognised NodeRole value
//   - validator declared with mining_enabled=true
//   - miner declared with mining_enabled=false
//
// The rules themselves are enforced by (*config.Config).Validate; this
// function exists so the same checks also run on Config structs that were
// built in memory (tests, embedded scenarios), and so main.go has a single
// explicit entry point for the "startup guard" mandated by Major Update
// Phase 2.3.
func MustMatchRole(role config.NodeRole, miningEnabled bool) error {
	if !role.IsValid() {
		return fmt.Errorf("roleguard: invalid node role %q (must be %q or %q)",
			string(role), string(config.NodeRoleValidator), string(config.NodeRoleMiner))
	}

	if role.IsValidator() && miningEnabled {
		return fmt.Errorf(
			"roleguard: node.role=%q is incompatible with node.mining_enabled=true; "+
				"validators do not run the emission PoW layer, run a separate miner process instead",
			string(role))
	}
	if role.IsMiner() && !miningEnabled {
		return fmt.Errorf(
			"roleguard: node.role=%q requires node.mining_enabled=true; "+
				"set mining_enabled=true or pick node.role=%q",
			string(role), string(config.NodeRoleValidator))
	}
	return nil
}
