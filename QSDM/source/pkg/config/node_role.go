package config

import (
	"fmt"
	"strings"
)

// NodeRole enumerates the two canonical roles a QSDM node can run in.
//
// The roles are declared by configuration, but also guarded by build tags and
// startup checks in `cmd/qsdm/main.go`:
//
//   - NodeRoleValidator runs Proof-of-Entanglement + BFT consensus on CPU-only
//     hardware. Validators never require a GPU. They may optionally publish
//     NVIDIA NGC proof bundles as a transparency signal; NGC attestation is
//     explicitly not a consensus rule.
//
//   - NodeRoleMiner runs the additive GPU Proof-of-Work layer for Cell (CELL)
//     emission. Miners do not participate in consensus and do not propose or
//     order blocks; they submit proofs to any validator and receive rewards per
//     the published emission schedule. Miners REQUIRE `MiningEnabled=true` and
//     a build that is NOT tagged `validator_only`.
//
// The string representation of these values is canonical and stable; config
// files, environment variables, and the /api/v1/status endpoint all use the
// lowercase form.
type NodeRole string

const (
	// NodeRoleValidator is the default role. Safe for VPS-class hardware.
	NodeRoleValidator NodeRole = "validator"
	// NodeRoleMiner is the GPU emission role. Only valid when MiningEnabled=true
	// and the binary was built without the `validator_only` tag.
	NodeRoleMiner NodeRole = "miner"
)

// String implements fmt.Stringer. It returns the canonical lowercase form.
func (r NodeRole) String() string {
	if r == "" {
		return string(NodeRoleValidator)
	}
	return string(r)
}

// IsValid reports whether r is one of the known canonical roles.
func (r NodeRole) IsValid() bool {
	switch r {
	case NodeRoleValidator, NodeRoleMiner:
		return true
	}
	return false
}

// IsValidator reports whether r is the validator role (or the unset default,
// which resolves to validator).
func (r NodeRole) IsValidator() bool {
	return r == "" || r == NodeRoleValidator
}

// IsMiner reports whether r is the miner role. An unset role is NOT considered
// a miner — the default is always validator.
func (r NodeRole) IsMiner() bool {
	return r == NodeRoleMiner
}

// ParseNodeRole parses a string into a NodeRole. The input is trimmed and
// lower-cased. An empty string resolves to NodeRoleValidator (the default).
// Any other non-canonical value returns an error; callers should surface the
// error at startup rather than silently defaulting.
func ParseNodeRole(s string) (NodeRole, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "":
		return NodeRoleValidator, nil
	case string(NodeRoleValidator), "primary", "vps":
		return NodeRoleValidator, nil
	case string(NodeRoleMiner), "gpu":
		return NodeRoleMiner, nil
	}
	return "", fmt.Errorf("unknown node_role %q (valid: %q, %q)", s, NodeRoleValidator, NodeRoleMiner)
}

// NodeRoleFromBoolMining is a convenience helper for the legacy wiring path
// where a node was described solely by a boolean "mining_enabled" flag. If the
// flag is true the role is NodeRoleMiner, otherwise NodeRoleValidator. This is
// used only when [node] role is unset in the config file AND both an
// environment variable and the file agree on `mining_enabled`.
func NodeRoleFromBoolMining(miningEnabled bool) NodeRole {
	if miningEnabled {
		return NodeRoleMiner
	}
	return NodeRoleValidator
}
