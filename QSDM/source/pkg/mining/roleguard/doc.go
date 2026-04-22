// Package roleguard enforces the Major Update Phase 2 "two node roles, two
// build profiles" contract at process startup.
//
// Background
//
// The QSDM binary ships in two flavours:
//
//   - Default build (no extra build tags): supports both roles. Operators can
//     configure node.role = "validator" or "miner" in their TOML/YAML config,
//     and the runtime enforces the consistency rules from pkg/config.Validate
//     (validator => mining_enabled=false; miner => mining_enabled=true).
//
//   - Validator-only build (`-tags validator_only`): a deliberately smaller
//     binary that omits pkg/mining/cuda and any other GPU-linked code paths.
//     This is the build we recommend for VPS-hosted validator operators; it
//     has no CUDA runtime dependency, ships on Alpine, and is much smaller.
//
// Guarantee
//
// Calling MustMatchRole at process startup guarantees:
//
//   - In a default build:     any valid role (validator | miner) is accepted.
//   - In a validator_only build: only role=="validator" is accepted. Mining
//     support is not present in the binary and attempting to start a miner
//     with the wrong binary is a hard configuration error — we abort before
//     any network listeners open.
//
// Build-tag variants
//
// The two implementations of MustMatchRole live in sibling files:
//
//   - roleguard.go            (//go:build !validator_only)
//   - roleguard_validator.go  (//go:build validator_only)
//
// They share the same exported signature so main.go can call
// roleguard.MustMatchRole(cfg.NodeRole, cfg.MiningEnabled) unconditionally.
//
// This package is intentionally tiny and has no runtime dependencies beyond
// the standard library and pkg/config; it MUST NOT import pkg/mining,
// pkg/mining/cuda, or any CUDA-linked package, to preserve its ability to be
// linked into the validator_only binary.
package roleguard
