// Package slashing defines the data model and consensus
// scaffolding for slashing transactions in the v2 NVIDIA-locked
// mining protocol.
//
// Scope of this commit (Phase 2c-xi, scaffolding-only):
//
//   - Payload struct (qsdm/slash/v1) carrying the target
//     NodeID, an EvidenceKind tag, and an opaque evidence blob.
//   - Canonical JSON codec with a deterministic field order
//     matching the rest of the v2 protocol's signing-input
//     conventions (see pkg/mining/enrollment/codec.go for the
//     model).
//   - EvidenceVerifier interface and a Dispatcher registry
//     keyed by EvidenceKind, mirroring pkg/mining/attest's
//     attestation-type dispatcher. Concrete verifiers will
//     plug in over time without touching this package.
//   - Stateless validation (ValidateSlashFields).
//
// Explicitly out of scope (and called out so a reviewer can
// confirm by reading this file alone, not by grepping):
//
//   - Chain-side applier (StakeForfeitureApplier) — defers to a
//     follow-on commit because it cuts across pkg/chain and
//     pkg/mining/enrollment in non-trivial ways and deserves
//     its own review window.
//   - Concrete EvidenceVerifier implementations — every
//     evidence kind (forged-attestation, double-mining,
//     freshness-cheat) needs its own cryptographic spec and
//     specimen-collection process. Shipping a stub registry
//     now lets miners see the wire format and lets the
//     verifier work proceed in parallel without re-encoding
//     the on-chain payload later.
//   - Slash sink address policy (burn vs. proposer reward vs.
//     governance treasury). Deferred until governance is
//     stood up post-fork.
//
// Why a separate package (rather than extending
// pkg/mining/enrollment):
//
//   - The verifier set will grow large (tracing-style log
//     parsers, attestation cross-checkers, BFT equivocation
//     detectors). Keeping it isolated avoids inflating
//     enrollment's surface area.
//   - Future revisions of the slash payload (qsdm/slash/v2,
//     etc.) can live alongside qsdm/slash/v1 here without
//     polluting enrollment.
//   - A node operator who builds with -tags noslashing for a
//     read-only-mirror role can omit this whole subtree.
package slashing
