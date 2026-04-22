//go:build validator_only
// +build validator_only

package roleguard

import (
	"fmt"

	"github.com/blackbeardONE/QSDM/pkg/config"
)

// BuildProfile reports which compile-time profile this binary was built with.
// In a validator-only build (produced with `go build -tags validator_only`)
// we return "validator_only" so operators can see the profile in logs and on
// the /api/v1/status endpoint if they wire it through there.
const BuildProfile = "validator_only"

// MustMatchRole enforces the validator-only build contract.
//
// A validator_only binary was produced precisely so operators can run a
// provably-CPU-only node with no CUDA linkage. Starting such a binary with
// node.role=miner is therefore a hard error: the miner code path is not
// compiled in, the process would silently fail to emit any blocks, and the
// operator would have the worst of both worlds (thinks they run a miner,
// actually runs a bare validator). We abort before any listeners open.
//
// This is a COMPILE-TIME assertion combined with a runtime guard: the default
// build's roleguard.go is mutually exclusive with this file, so the linker
// refuses to produce a validator_only binary that accepts miner roles.
func MustMatchRole(role config.NodeRole, miningEnabled bool) error {
	if !role.IsValid() {
		return fmt.Errorf("roleguard (validator_only): invalid node role %q (must be %q)",
			string(role), string(config.NodeRoleValidator))
	}
	if !role.IsValidator() {
		return fmt.Errorf(
			"roleguard (validator_only): this binary was built with -tags validator_only and "+
				"refuses node.role=%q; use the full-profile binary to run a miner, "+
				"or set node.role=%q",
			string(role), string(config.NodeRoleValidator))
	}
	if miningEnabled {
		return fmt.Errorf(
			"roleguard (validator_only): node.mining_enabled=true is incompatible with a " +
				"validator_only binary; set mining_enabled=false or switch to the full-profile binary")
	}
	return nil
}
