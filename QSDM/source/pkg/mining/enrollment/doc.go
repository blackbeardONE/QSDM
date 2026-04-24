// Package enrollment is the on-chain data model + consensus
// rules for the nvidia-hmac-v1 operator registry
// (MINING_PROTOCOL_V2_NVIDIA_LOCKED.md §5.2, §5.4).
//
// SECURITY MODEL — READ THIS FIRST
//
// The nvidia-hmac-v1 attestation type is HMAC-based, which means
// the symmetric key MUST be known to both the miner AND every
// verifier. In a public-blockchain setting every full node is a
// verifier, so the key is effectively public by the time
// enrollment is committed to the chain.
//
// This is by design. The ratified trust-anchor model is tiered
// (§5 of the spec):
//
//     datacenter GPUs -> nvidia-cc-v1  (real AIK crypto,
//                                       Phase 2c-iv)
//     consumer GPUs   -> nvidia-hmac-v1 (public HMAC key +
//                                        economic deterrence)
//
// The security of the HMAC path is NOT anti-forgery via key
// secrecy. It is:
//
//   1. Identity pinning: the HMAC key is bound at enrollment to
//      a (node_id, gpu_uuid, owner-address) triple. Forgeries
//      necessarily route reward to the enrolled owner, not to
//      the forger.
//
//   2. Stake bond: enrollment requires MinEnrollStakeDust (10
//      CELL as of fork height, governance-adjustable). The
//      stake is locked until Unenroll + UnbondWindow. Misuse of
//      a node_id's key — regardless of WHO used it — costs the
//      owner their stake via the slash path.
//
//   3. Rate limiting: a single node_id can submit only one proof
//      per block (enforced upstream at proof-verification time).
//      A forger racing to steal a block from an enrolled owner
//      wins at most one block, and the owner's next-block proof
//      displaces the forger's in the dedup cache.
//
// So: yes, an adversary who reads the chain can produce valid
// bundles for any enrolled node_id. But the reward goes to the
// enrolled owner, not to the adversary. The rational worst case
// is an operator leaking their own key to increase their own
// hashrate — which is exactly what the protocol rewards anyway
// (they enrolled their GPU; they get credit for its work).
//
// If we later need anti-forgery against the chain observer, we
// have to either (a) switch to asymmetric signatures (requires
// a new attestation type and fork) or (b) require operators to
// submit proofs through an identity-bound TLS channel to a
// smaller trusted verifier set. Neither is in Phase 2c-ii.
//
// PUBLIC SURFACE OF THIS PACKAGE
//
//   EnrollPayload     — wire format of the enroll transaction
//                       payload (encoded into mempool.Tx.Payload)
//   UnenrollPayload   — wire format of the unenroll transaction
//   EnrollmentRecord  — on-chain state entry for an enrolled node
//   EnrollmentState   — read-only view the rest of the system
//                       uses to query enrolled nodes
//   StateBackedRegistry — adapts EnrollmentState to the
//                       hmac.Registry interface the attestation
//                       verifier consumes
//   ValidateEnrollPayload / ValidateUnenrollPayload —
//                       stateless + state-dependent consensus
//                       checks ready to drop into the chain's
//                       tx handler
//
// NOT IN SCOPE for this commit (follow-on work):
//
//   - the pkg/chain state-transition hook that actually debits
//     the sender's balance by MinEnrollStakeDust and inserts
//     the EnrollmentRecord
//   - the block-time trigger that releases stake after
//     UnbondWindow elapses
//   - the slashing path (governance-gated, Phase 2c-v)
//
// Those land in a dedicated pkg/chain commit so the consensus
// diff stays reviewable in isolation. Shipping the model +
// validators + adapter separately means the chain commit
// becomes "plumb pkg/mining/enrollment into state transition"
// rather than "plumb pkg/mining/enrollment AND also design
// pkg/mining/enrollment."
package enrollment
