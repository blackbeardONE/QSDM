# MINING_PROTOCOL_V2.md — QSDM v2 Mining Protocol (Canonical Spec)

> **Status:** Normative. Shipped on `main`. Supersedes the three
> historical fragments listed below.
>
> **Audience:** Validator operators, miner operators, protocol
> implementers, security reviewers, and anyone trying to answer
> the question *"what does v2 actually look like, and what part of
> it is real today?"*.
>
> **Supersedes:**
> [`MINING_PROTOCOL_V2_NVIDIA_LOCKED.md`](./MINING_PROTOCOL_V2_NVIDIA_LOCKED.md)
> (the original Phase-1 design draft),
> [`MINING_PROTOCOL_V2_RATIFICATION.md`](./MINING_PROTOCOL_V2_RATIFICATION.md)
> (the 2026-04-24 owner sign-off recording the three OPEN_QUESTION
> resolutions), and
> [`MINING_PROTOCOL_V2_TIER3_SCOPE.md`](./MINING_PROTOCOL_V2_TIER3_SCOPE.md)
> (the rolling shipped-vs-deferred status doc). Those files are
> retained as thin redirect stubs so old links keep resolving.
>
> **Supersedes from v1:** `MINING_PROTOCOL.md §§1.1(2), 5, 6, 7` at
> activation. The v1 spec stays in-tree as the testnet protocol-of-
> record and is the reference for the legacy double-SHA256 PoW
> path, which is preserved under `ComputeMixDigestV1` for audit and
> replay (§10.5).
>
> **Does not supersede:** [`CELL_TOKENOMICS.md`](./CELL_TOKENOMICS.md)
> (issuance schedule), [`NODE_ROLES.md`](./NODE_ROLES.md) (the
> validator/miner split), or `nvidia_locked_qsdm_blockchain_architecture.md`
> (high-level vision).
>
> **How to read this doc:** sections 0–4 are the normative spec.
> Sections 5–9 are the current implementation contract — every
> table here references a concrete Go file that you can `grep` the
> repo for. Sections 10–13 are activation mechanics, the attacker
> model, the deferred-work register, and the historical decision
> record.

---

## Table of contents

0. Executive summary
1. What changes relative to v1
2. What does NOT change
3. Wire format
4. Tensor-Core PoW mixin (deferred)
5. Trust anchors
6. Freshness window & nonce issuance
7. Verifier
8. On-chain enrollment & slashing
9. Operator surface (HTTP, CLI, miner UX, observability, boot wiring)
10. Activation mechanics — hard fork
11. Attacker model
12. Deferred work register
13. Historical decision record
14. Cross-references

---

## 0. Executive summary

QSDM v2 is the production mining protocol. It locks mining to
NVIDIA GPUs along two axes — **cryptographic** (mandatory
attestation) and **economic** (Tensor-Core-biased PoW, deferred)
— and adds an on-chain enrollment / slashing layer that makes the
NVIDIA lock observable and enforceable without trusting any
single validator.

1. **Hard fork from v1.** Blocks before `FORK_V2_HEIGHT` follow
   `MINING_PROTOCOL.md`. Blocks at or after `FORK_V2_HEIGHT`
   follow this document. Because v2 launches via a chain reset
   (§10.3), `FORK_V2_HEIGHT = 0`.
2. **Mandatory attestation.** `Proof.Attestation` is now a
   consensus-checked field. A proof with an empty, unparseable,
   non-whitelisted, stale, or cryptographically-invalid
   attestation is rejected at the verifier. v1's "transparency
   signal, not a consensus rule" stance is gone.
3. **Tiered trust anchor.**
   - `nvidia-cc-v1` — datacenter Hopper / Blackwell GPUs,
     verified via NVIDIA-signed AIK quote against a
     genesis-pinned root. Implementation
     [shipped](#5-trust-anchors) in `pkg/mining/attest/cc/`.
   - `nvidia-hmac-v1` — consumer NVIDIA GPUs (Turing / Ampere /
     Ada / Blackwell consumer), verified via HMAC over a
     canonical-JSON bundle, bound to a stake-locked operator
     entry in the on-chain registry. Implementation shipped in
     `pkg/mining/attest/hmac/`.
4. **Tensor-Core PoW mixin.** Specified in §4. **Not yet
   implemented** — gated behind a future `FORK_V2_TC_HEIGHT`
   that activates as a soft-tightening fork once
   `cmd/qsdm-miner-cuda` lands. Pre-mixin, v2 proofs validate
   under the legacy double-SHA256 PoW, so attestation is the
   only NVIDIA-locking surface that is consensus-active today.
5. **On-chain enrollment.** Operators register
   `(node_id, gpu_uuid, hmac_key)` tuples by submitting a
   `qsdm/enroll/v1` transaction that locks `MIN_ENROLL_STAKE = 10
   CELL`. Unenroll bonds the stake for `UnbondWindow`
   (default 30 d) and the `gpu_uuid` releases at maturity, so a
   physical card can be re-enrolled by a fresh `node_id` after
   the original record retires. Implementation in
   `pkg/mining/enrollment/`.
6. **On-chain slashing.** A `qsdm/slash/v1` transaction can
   drain bonded stake by submitting verifier-checked evidence.
   Two evidence kinds ship today: `forged-attestation` and
   `double-mining` (both with end-to-end tests). One,
   `freshness-cheat`, is deferred behind BFT finality.
   Implementation in `pkg/mining/slashing/` and the chain-side
   applier in `pkg/chain/slash_apply.go`.
7. **Miner UX.** `cmd/qsdmminer-console` ships an opt-in v2 path
   (`--protocol=v2`) that drives the full enrollment → challenge
   → HMAC-bundle → submit loop, with a built-in setup wizard, a
   live `v2 NVIDIA` panel row, and a background enrollment
   poller. The CPU-only PoW kernel is preserved purely for
   testnet attestation participation; once
   `FORK_V2_TC_HEIGHT` activates, this binary is no longer
   profitable. The CUDA-native miner (`cmd/qsdm-miner-cuda`) is
   the deferred replacement (§12.2).

The rest of this document is the normative wire spec
(§§1–4), the implementation contract (§§5–9), activation
mechanics (§10), the attacker model (§11), and the deferred-work
+ decision register (§§12–13).

---

## 1. What changes relative to v1

All references below are to v1 spec
[`MINING_PROTOCOL.md`](./MINING_PROTOCOL.md) and to Go source
paths under `QSDM/source/`.

| Area | v1 (current) | v2 (this spec) |
|---|---|---|
| Goal §1.1(2) | "GPU-favored, NVIDIA-favored, NVIDIA-not-required. Portable OpenCL / Vulkan / CPU fallbacks MUST remain compilable and correct — they only lose economically." | **"NVIDIA-required."** Non-NVIDIA implementations of `ComputeMixDigest` remain compilable for protocol auditing, but proofs produced by them are unconditionally rejected at the attestation gate. |
| `Proof.Attestation` field | Optional. An absent, stale, or unverifiable attestation MUST NOT cause rejection (`§6`). | **Mandatory.** Empty / unparseable / unknown-type / signature-invalid / stale → consensus reject. |
| `Proof.Version` | `1` | `2` (`mining.ProtocolVersionV2`). |
| `Attestation.Type` whitelist | `"ngc-v1"` (informational only). | `"nvidia-cc-v1"` (datacenter CC) and `"nvidia-hmac-v1"` (consumer GPUs). Whitelist enforced at the verifier dispatcher. |
| Trust anchor | None — `Verifier.Verify` never reads `Attestation`. | Genesis-pinned NVIDIA CC root material + on-chain operator registry (HMAC path). See §5. |
| PoW hash | SHA3-256 in a 64-step DAG walk (`pkg/mining/pow.go::ComputeMixDigest`). | SHA3-256 + Tensor-Core FP16 matmul mixin per DAG step (§4). **Deferred** — gated behind `FORK_V2_TC_HEIGHT`. |
| Validator SLO | Verify any single proof in < 100 ms single-core, batch 1000 in < 2 s (`§1.1(4)`). | Unchanged. The Tensor-Core mixin runs only on the miner side; the validator re-hashes via a deterministic CPU reference. |
| Attestation endpoint `/api/v1/monitoring/ngc-proof` | Monitoring-only sink, never feeds consensus. | Unchanged role. v2 attestations travel inline on the proof. The legacy ingest endpoint remains for dashboards; it is no longer the consensus path. |

---

## 2. What does NOT change

1. Validators remain CPU-only. The NVIDIA lock is on the *miner*
   side of the miner/validator split (see
   [`NODE_ROLES.md`](./NODE_ROLES.md)). A validator never verifies
   an NGC signature against a GPU it owns; it verifies against
   genesis-pinned NVIDIA roots and the on-chain operator registry.
2. Cell tokenomics ([`CELL_TOKENOMICS.md`](./CELL_TOKENOMICS.md))
   are unchanged. The fork resets supply to zero at height 0
   because the testnet has no real users (2026-04-24 owner
   sign-off, §13.4); the issuance curve from that point is
   identical to v1.
3. PoE+BFT consensus among validators (`pkg/chain`,
   `pkg/consensus`) is unchanged. v2 touches mining only.
4. Proof-ID derivation (`pkg/mining/proof.go::ID`) still excludes
   `Attestation` from the hash input. Two validly-signed proofs
   with identical `(epoch, height, nonce, batch_root)` and
   different attestation bundles share a proof-id; the verifier
   rejects the second one for duplicate-proof reasons, not for
   attestation reasons.
5. The `apps/qsdm-nvidia-ngc/` sidecar keeps operating and keeps
   pushing to `/api/v1/monitoring/ngc-proof`. That path is
   dashboards and transparency surface; it does not feed
   consensus.

---

## 3. Wire format

### 3.1 `Attestation` struct (v2)

```go
// pkg/mining/proof.go (v2). Field order is normative per
// MINING_PROTOCOL.md §4.1 canonical-JSON rules. Do NOT reorder.
type Attestation struct {
    Type                 string   `json:"type"`
    BundleBase64         string   `json:"bundle"`
    GPUArch              string   `json:"gpu_arch"`
    ClaimedHashrateHPS   uint64   `json:"claimed_hashrate_hps"`

    // Pinned outside Bundle so the verifier can deserialize
    // enough metadata to dispatch to the right verify path
    // without parsing a variable-schema nested document.
    Nonce                [32]byte `json:"nonce"`     // server-issued freshness challenge, lowercase hex
    IssuedAt             int64    `json:"issued_at"` // unix seconds; tolerance is FRESHNESS_WINDOW
}
```

Canonical JSON wire order, nested in `Proof`:

```json
{
  "version": 2,
  "epoch":   "<uint64 as string>",
  "height":  "<uint64 as string>",
  "header_hash": "<hex 32B>",
  "miner_addr":  "qsdm1...",
  "batch_root":  "<hex 32B>",
  "batch_count": <uint32>,
  "nonce":       "<hex 16B>",
  "mix_digest":  "<hex 32B>",
  "attestation": {
    "type": "nvidia-cc-v1" | "nvidia-hmac-v1",
    "bundle": "<base64 blob; contents depend on type>",
    "gpu_arch": "hopper" | "ada-lovelace" | "blackwell" | ...,
    "claimed_hashrate_hps": "<uint64 as string>",
    "nonce":     "<hex 32B>",
    "issued_at": "<int64 as string>"
  }
}
```

Zero-value `Attestation{}` → `validateShape` → reject with
`ErrAttestationRequired`. This is the hard invariant the verifier
enforces above every other check.

### 3.2 `Attestation.Bundle` payload — `nvidia-cc-v1`

Used by Hopper / Blackwell datacenter GPUs running NVIDIA
Confidential Computing. The bundle is a base64-encoded
length-prefixed binary blob carrying:

1. **NVIDIA device certificate chain** — the per-GPU attestation
   certificate chain, rooted in a genesis-pinned NVIDIA issuing
   CA public key (§5.1).
2. **Quote** — an ECDSA signature from the GPU AIK
   (Attestation Identity Key) over the canonical preimage:
   ```
   H( device_uuid
   || challenge_nonce
   || issued_at
   || miner_addr
   || batch_root
   || mix_digest
   || challenge_signer_id
   || challenge_sig )
   ```
   `challenge_nonce == Attestation.Nonce`; the other fields
   come from the enclosing `Proof`. `challenge_signer_id` /
   `challenge_sig` mirror the consumer-GPU path (§3.3) — they
   bind the bundle to a specific validator-issued challenge so
   replay outside the freshness window is detectable in the
   AIK preimage itself.
3. **PCR-equivalent measurements** — current GPU firmware
   version + driver version, recorded by the CC subsystem so a
   downgrade-to-vulnerable-firmware attack is detectable.

#### Verifier flow (8 steps, all enforced)

```
1. Parse the cert chain; verify it terminates in a
   genesis-pinned NVIDIA CA public key.
2. Verify the AIK Quote signature over the canonical preimage
   above using the leaf cert's public key.
3. Check challenge_nonce == Attestation.Nonce.
4. Check Attestation.IssuedAt is within FRESHNESS_WINDOW of the
   validator's wall clock and not future-dated past
   AllowedFutureSkew.
5. Verify (challenge_signer_id, challenge_sig) against the
   registered ChallengeVerifier — same crypto the HMAC path
   uses. (Skipped only if the operator deliberately wires
   ChallengeVerifier=nil; production MUST set it.)
6. Check PCR firmware/driver versions against the genesis-
   pinned minimum floor for the claimed gpu_arch.
7. Look up (device_uuid, challenge_nonce) in the replay cache;
   reject if seen.
8. If all pass → proof is attested. Else → reject with the
   precise reason.
```

Reference implementation:
[`pkg/mining/attest/cc/verifier.go`](../../source/pkg/mining/attest/cc/verifier.go),
bundle parser
[`pkg/mining/attest/cc/bundle.go`](../../source/pkg/mining/attest/cc/bundle.go).
Production wiring: `attest.ProductionConfig.CCConfig`. When no
NVIDIA root is pinned, the dispatcher falls through to
[`cc.NewStubVerifier()`](../../source/pkg/mining/attest/cc/stub.go)
which always rejects — fail-closed.

Test coverage: 28 unit tests including 13 negative cases
(tampered AIK signature, wrong root, expired leaf, nonce
mismatch, issued_at mismatch, miner_addr/mix_digest preimage
tamper, stale, future-dated, below firmware floor, below driver
floor, replay through `NonceStore`, malformed base64, unknown
JSON field, over-length cert chain). Test vectors are generated
**deterministically in-process** by
[`pkg/mining/attest/cc/testvectors.go`](../../source/pkg/mining/attest/cc/testvectors.go);
no testdata files. The seam to swap in real `nvtrust` framing is
a single `ParseBundle` reimplementation — verifier code does not
change.

### 3.3 `Attestation.Bundle` payload — `nvidia-hmac-v1`

Used by consumer NVIDIA GPUs (Turing / Ampere / Ada / Blackwell
consumer). The bundle is a base64-encoded canonical-JSON object:

```json
{
  "node_id":              "<operator-registered handle, e.g. 'alice-rtx4090-01'>",
  "gpu_uuid":             "<GPU instance UUID from nvidia-smi, hex>",
  "gpu_name":             "NVIDIA GeForce RTX 4090",
  "driver_ver":           "572.16",
  "cuda_version":         "12.8",
  "compute_cap":          "8.9",
  "nonce":                "<same 32-byte hex as Attestation.Nonce>",
  "issued_at":            <unix seconds>,
  "challenge_bind":       "<hex H(miner_addr || batch_root || mix_digest)>",
  "challenge_sig":        "<hex validator signature over (signer_id, issued_at, nonce)>",
  "challenge_signer_id":  "<validator identity that issued this challenge>",
  "hmac":                 "<hex HMAC-SHA256(operator_key, canonical_json_without_hmac_field)>"
}
```

The shown order is human-reading order. **Canonical-form field
order is alphabetical on the JSON key** — `challenge_sig` and
`challenge_signer_id` land between `challenge_bind` and
`compute_cap`. Reference implementation:
[`pkg/mining/attest/hmac/bundle.go`](../../source/pkg/mining/attest/hmac/bundle.go)
+ [`pkg/mining/challenge/`](../../source/pkg/mining/challenge/).

#### Verifier flow (9 steps)

```
1. Parse JSON.
2. Recompute H(miner_addr || batch_root || mix_digest) from the
   enclosing Proof; assert it matches bundle.challenge_bind.
3. Look up bundle.node_id in the on-chain operator registry
   (§5.2). If absent or revoked → reject.
4. Fetch the HMAC key associated with that node_id from the
   registry. Recompute HMAC-SHA256 over canonical-JSON minus
   the hmac field. Reject on mismatch.
5. Fetch the GPU UUID from the registry. Assert
   bundle.gpu_uuid matches. Reject on mismatch.
6a. Assert bundle.nonce == Attestation.Nonce and
    bundle.issued_at == Attestation.IssuedAt.
6b. Reconstruct challenge.Challenge{Nonce, IssuedAt, SignerID,
    Signature} from bundle.{nonce, issued_at,
    challenge_signer_id, challenge_sig}; verify the signature
    using the SignerID's registered public key. Reject unknown
    signer_id or bad signature.
6c. Assert bundle.issued_at falls within FRESHNESS_WINDOW of
    the validator's wall clock and ≤ AllowedFutureSkew ahead.
6d. Check the nonce-replay cache; reject if (node_id, nonce)
    already seen.
7. Assert bundle.gpu_name does NOT contain any deny-list
   substring (§5.3 — empty at genesis).
8. Verify the Tensor-Core mix_digest (§4) is consistent with
   the claimed gpu_arch. (Soft check pre-mixin — see §4.6.)
9. If all pass → proof is attested. Else → reject.
```

Reference implementation:
[`pkg/mining/attest/hmac/verifier.go`](../../source/pkg/mining/attest/hmac/verifier.go).
Production wiring: `attest.ProductionConfig.HMACConfig`.

#### HTTP challenge endpoint

```
GET /api/v1/mining/challenge

Response 200:
{
  "nonce":     "<64 hex chars>",
  "issued_at": <unix seconds>,
  "signer_id": "<validator identity>",
  "signature": "<hex signer output>"
}

Response headers:
  Cache-Control: no-store    (required — a cached response would
                              leak the same nonce to two miners)

Response 503 + Retry-After: 5 when no ChallengeIssuer is wired in.
Response 500 on issuer internal failure (PRNG exhausted, etc.).
```

Implementation:
[`pkg/mining/challenge/`](../../source/pkg/mining/challenge/) +
HTTP handler in
[`pkg/api/handlers.go`](../../source/pkg/api/handlers.go).

### 3.4 `Proof` struct total wire change

```go
// pkg/mining/proof.go — v2 layout. ProtocolVersion is the only
// difference from v1 at the struct level; v2 adds two new
// sub-fields inside Attestation (see §3.1).
type Proof struct {
    Version     uint32      // = 2 post-fork
    Epoch       uint64
    Height      uint64
    HeaderHash  [32]byte
    MinerAddr   string
    BatchRoot   [32]byte
    BatchCount  uint32
    Nonce       [16]byte
    MixDigest   [32]byte
    Attestation Attestation  // mandatory
}
```

---

## 4. Tensor-Core PoW mixin (deferred)

> **Status as of this revision:** specified, not implemented.
> Pre-mixin, a `Version=2` proof is accepted under the legacy
> double-SHA256 PoW. The Tensor-Core mixin activates at a future
> `FORK_V2_TC_HEIGHT` as a soft-tightening fork (validators get
> stricter; no chain reset). Tracking: §12.2.

### 4.1 Why a PoW mixin at all

The attestation gate (§3) is the consensus rule. A rogue
validator that ignores it could accept proofs from anybody.
Economic lock: make the proof itself uneconomic to produce
without a Tensor Core, so a fork that bypasses the attestation
rule also gets no hashrate advantage from CPU miners.

### 4.2 The mixin

v1 hash (`pkg/mining/pow.go::ComputeMixDigest`):

```
seed := SHA3-256(header_hash || nonce)
mix  := seed
for s in 0..64:
    idx := uint32(BE(mix[0..4])) mod N
    mix := SHA3-256(mix || D_e[idx])
return mix
```

v2 hash:

```
seed := SHA3-256(header_hash || nonce)
mix  := seed
for s in 0..64:
    idx      := uint32(BE(mix[0..4])) mod N
    entry    := D_e[idx]

    // --- NEW ---
    // Deterministic 16x16 FP16 matmul over the matrix M(mix)
    // and the vector v(entry). Result is a 16-element FP16
    // vector converted back to a 32-byte canonical IEEE-754
    // big-endian byte string.
    M := unpack_matrix_fp16(mix)        // 16x16 FP16 matrix
    v := unpack_vector_fp16(entry)      // 16 FP16 elements
    r := (M * v) mod FP16_CANONICAL     // 16-element FP16 result
    tc := pack_canonical_fp16(r)        // 32 bytes
    // --- /NEW ---

    mix := SHA3-256(mix || entry || tc)
return mix
```

The matmul output is deterministic IEEE-754 FP16 with a pinned
rounding mode (`round-to-nearest-even`, CUDA default), so the
validator's CPU reference produces bit-identical `tc` to the
miner's Tensor Core.

### 4.3 Validator cost

Single-proof CPU verify budget moves from ~60 µs (sha3 only) to
~700 µs (sha3 + 64 × 16×16 FP16 matmul on `gonum/blas`). Still
inside the `MINING_PROTOCOL.md §1.1(4) < 100 ms` validator SLO.

### 4.4 Miner cost

On an RTX 4090 Tensor Core: 16×16 FP16 matmul per dispatched
thread completes in ~20 ns, ~250x faster than CPU. H100 with
FP16 Tensor Cores: ~8 ns. Expected hashrate: ~5 MH/s on RTX
4090, ~20-40 MH/s on H100. CPU miner: ~0.02 MH/s. That is the
economic lock.

### 4.5 Backward compatibility

The v1 function is renamed `ComputeMixDigestV1` and kept in-tree
for replaying pre-fork blocks (audit), protocol-conformance
tests, and any future soft-unlock if governance ever wants to
re-enable non-NVIDIA mining.

### 4.6 Pre-mixin soft check

Step 8 of the HMAC verifier flow ("Tensor-Core mix_digest
consistent with claimed gpu_arch") is a no-op until
`FORK_V2_TC_HEIGHT` activates, because there is nothing
arch-specific in the v1 PoW output. Post-`FORK_V2_TC_HEIGHT`
the mixin's deterministic FP16 path lets the verifier reject
arch-spoof attempts (e.g. "RTX 4090 claiming to be H100" — the
matmul rounding fingerprint differs).

---

## 5. Trust anchors

This section records what is **shipped today**, not what the
draft recommended. The 2026-04-24 owner sign-off (§13) ratified
the tiered model below; no other configuration is supported.

### 5.1 CC path (datacenter Hopper / Blackwell)

Genesis embeds:

- The NVIDIA device-attestation CA root public key(s).
- A list of accepted NVIDIA attestation-chain issuers.
- A minimum firmware / driver floor per supported architecture
  (Hopper SM90, Blackwell SM100).

Live NGC HTTP attestation is **not used.** The validator SLO
(`< 100 ms` per proof) does not tolerate a synchronous HTTPS
round-trip to NVIDIA's attestation service per proof, and a
chain halt caused by an NVIDIA service outage is unacceptable.
Root rotation handled the same way every pinned-root system
handles it: a governance-gated chain-config update committing
the new root, activated at a future height.

Deferred only insofar as **real-world `nvtrust` bundle framing**
is concerned; see §12.1.

### 5.2 Consumer GPU path — on-chain operator registry

Schema (`pkg/mining/enrollment/types.go`):

```
node_id:     UTF-8 string, ≤ 64 bytes
gpu_uuid:    exact UUID string from `nvidia-smi --query-gpu=uuid`
pub_key:     ed25519 public key of the operator
hmac_key:    32 random bytes, shared secret operator↔registry
stake_dust:  uint64; ≥ MinEnrollStakeDust at enrollment
```

Enrollment lifecycle:

1. Operator generates an HMAC key locally (e.g. via
   `qsdmminer-console --gen-hmac-key=PATH`).
2. Operator submits `qsdm/enroll/v1` transaction carrying
   `(node_id, gpu_uuid, pub_key, hmac_key_commitment)` and
   locking `MinEnrollStakeDust` at the validator's
   `EnrollmentApplier`. Mempool admission: stateless validation
   via `enrollment.AdmissionChecker` in
   [`pkg/mining/enrollment/admit.go`](../../source/pkg/mining/enrollment/admit.go);
   chain-side state mutation in
   [`pkg/chain.EnrollmentApplier`](../../source/pkg/chain/enroll_apply.go).
3. From then on, every proof the miner emits carries a
   `nvidia-hmac-v1` bundle signed with that key.
4. Operators or governance can revoke a `node_id` via
   `qsdm/unenroll/v1`. Stake bonds for `UnbondWindow` (default
   30 d at v2 genesis); `BlockProducer.OnSealedBlock` auto-
   sweeps matured records and releases the `gpu_uuid` so the
   physical card can be re-enrolled by a fresh `node_id`.

**Why this is not cryptographically airtight.** An operator with
a legitimately-registered `(node_id, gpu_uuid, hmac_key)` tuple
can lend their HMAC key to an accomplice running on an AMD GPU
that reports a fake `gpu_uuid`. The verifier cannot distinguish.
This is an *economic* lock, not a *cryptographic* one for
consumer cards: the Tensor-Core PoW mixin (§4) makes the AMD
bypass uneconomic, and the stake-at-enrollment makes Sybil
attacks expensive (10 CELL × N keys, plus `freshness-cheat`/
`forged-attestation`/`double-mining` slashing risk).

### 5.3 Deny-list

Genesis embeds a deny-list of GPU name substrings that must not
appear in any `nvidia-hmac-v1` bundle (`bundle.gpu_name`).
Initially empty. Governance can append strings (e.g. a future
revelation that a particular card model has a driver bypass
attackers are abusing). Enforcement: §3.3 step 7.

### 5.4 Stake-at-enrollment (anti-Sybil)

`MinEnrollStakeDust = 10 * 100_000_000` dust = **10 CELL**.
Ratified 2026-04-24 (§13.2). Governance may adjust post-launch
via the chain-config delta mechanism. Enforced by
[`pkg/mining/fork.go`](../../source/pkg/mining/fork.go) and the
mempool admission gate; defended by
[`pkg/chain/slash_apply.go`](../../source/pkg/chain/slash_apply.go)'s
`SlashApplier.AutoRevokeMinStakeDust` which automatically
revokes any record drained below the threshold.

---

## 6. Freshness window & nonce issuance

### 6.1 The problem

Without a freshness mechanism, a miner could record one valid
attestation bundle and replay it forever. The attestation check
would pass but the bundle conveys no evidence about the specific
proof it's paired with.

### 6.2 The solution

`FRESHNESS_WINDOW = 60 s`. Ratified 2026-04-24 (§13.3).
Validator nonce ring buffer retention: `2 × FRESHNESS_WINDOW =
120 s`.

1. Every validator exposes
   `GET /api/v1/mining/challenge` (§3.3). Response: 32-byte
   random nonce + issued-at timestamp + signature over both by
   the validator's challenge signing key.
2. A miner fetches a challenge before starting a round and
   embeds the exact `(nonce, issued_at)` in its
   `Attestation.Nonce` / `Attestation.IssuedAt`.
3. A proof is stale if `issued_at + FRESHNESS_WINDOW < now`.
4. A validator can verify a challenge it didn't issue by
   checking the issuer's signature — any validator's challenge
   is accepted as long as it's within the freshness window.
   This prevents a single-validator DoS where the network stalls
   because one validator's challenge service is down.

### 6.3 Replay store

Validators remember `(node_id, nonce)` (or `(device_uuid, nonce)`
for the CC path) for `2 × FRESHNESS_WINDOW`. A proof reusing a
nonce already seen → reject.

---

## 7. Verifier

### 7.1 Acceptance flow

```go
// pkg/mining/verifier.go (v2). The attestation gate runs first
// after validateShape so we reject bad attestations before
// spending CPU on the DAG walk.
func (v *Verifier) Verify(p Proof, …) error {
    if err := p.validateShape(); err != nil { return err }

    if p.Height >= ForkV2Height {
        if err := v.verifyAttestation(p); err != nil {
            return fmt.Errorf("v2 attestation: %w", err)
        }
    }
    // existing DAG walk, target check, etc…
    return nil
}

func (v *Verifier) verifyAttestation(p Proof) error {
    a := p.Attestation
    if a.Type == "" { return ErrAttestationRequired }
    return v.dispatcher.Verify(p, a)
}
```

Dispatcher: `pkg/mining/attest/dispatcher.go` — type-keyed
registry mapping `Attestation.Type` to a concrete
`AttestationVerifier`.

### 7.2 Shipped packages

| Package | Purpose | Status |
|---|---|---|
| [`pkg/mining/attest/cc/`](../../source/pkg/mining/attest/cc/) | NVIDIA CC cert-chain + AIK quote verification | **Shipped.** 28 unit tests, deterministic in-process test vectors. |
| [`pkg/mining/attest/hmac/`](../../source/pkg/mining/attest/hmac/) | Consumer-GPU HMAC verification + registry lookup | **Shipped.** Wired through `attest.ProductionConfig.HMACConfig`. |
| [`pkg/mining/attest/dispatcher.go`](../../source/pkg/mining/attest/dispatcher.go) | Type-keyed verifier registry | **Shipped.** |
| [`pkg/mining/challenge/`](../../source/pkg/mining/challenge/) | Validator-issued nonce challenge crypto | **Shipped.** |
| [`pkg/mining/enrollment/`](../../source/pkg/mining/enrollment/) | On-chain operator registry, admission gate, sweep | **Shipped.** |
| [`pkg/mining/slashing/`](../../source/pkg/mining/slashing/) | Slashing data model + dispatcher + admission | **Shipped** (`forged-attestation` + `double-mining`); `freshness-cheat` deferred. |

### 7.3 Test vectors

Phase 2c-iv ships test vectors **inline, not as testdata files**
— [`pkg/mining/attest/cc/testvectors.go`](../../source/pkg/mining/attest/cc/testvectors.go)
is a deterministic in-process generator (seeded PRNG, fresh
self-signed root + AIK leaf per call) so CI runs on machines
without GPU hardware. Coverage:

- 1 happy path
- 13 negative cases: tampered AIK signature, wrong root, expired
  leaf, nonce mismatch, issued_at mismatch, miner_addr/mix_digest
  preimage tamper, stale, future-dated, below firmware floor,
  below driver floor, replay through `NonceStore`, malformed
  base64, unknown JSON field, over-length cert chain.

When NVIDIA-issued real-world H100 / B100 bundles become
available, the swap is a single `ParseBundle` reimplementation
— the verifier code does not change.

---

## 8. On-chain enrollment & slashing

The §3 attestation gate is the *consensus* rule. To make the
NVIDIA lock observable and enforceable, v2 adds an on-chain
registry (enrollment) and a punishment surface (slashing) that
let any node that *witnesses* a forged proof drain the offender's
stake without trusting any single validator.

### 8.1 Enrollment

Wire types in
[`pkg/mining/enrollment/types.go`](../../source/pkg/mining/enrollment/types.go):

```go
type EnrollPayload struct {
    NodeID     string
    GPUUUID    string
    PubKey     []byte // ed25519
    HMACKey    []byte // 32 random bytes
    StakeDust  uint64 // ≥ MinEnrollStakeDust
    ContractID = "qsdm/enroll/v1"
}

type UnenrollPayload struct {
    NodeID     string
    ContractID = "qsdm/unenroll/v1"
}

type EnrollmentRecord struct {
    NodeID, GPUUUID, Owner string
    PubKey, HMACKey        []byte
    StakeDust              uint64
    EnrolledAtHeight       uint64
    RevokedAtHeight        uint64 // 0 == active
    UnbondMaturesAtHeight  uint64 // 0 == active
}
```

Stateless mempool admission:
[`pkg/mining/enrollment/admit.go`](../../source/pkg/mining/enrollment/admit.go)
(`AdmissionChecker`).

Stateful chain-side applier:
[`pkg/chain/enroll_apply.go`](../../source/pkg/chain/enroll_apply.go)
(`EnrollmentApplier`), composed via
[`pkg/chain/applier.go`](../../source/pkg/chain/applier.go)
(`EnrollmentAwareApplier`) into the block producer's state
transition pipeline.

Auto-sweep at unbond maturity:
`BlockProducer.OnSealedBlock = SealedBlockHook(...)`. Matured
`UnbondMaturesAtHeight ≤ height` records release stake to the
operator's account and free the `gpu_uuid` binding for fresh
re-enrollment.

### 8.2 Slashing

Wire types in
[`pkg/mining/slashing/types.go`](../../source/pkg/mining/slashing/types.go):

```go
const ContractID = "qsdm/slash/v1"

type EvidenceKind string

const (
    EvidenceKindForgedAttestation EvidenceKind = "forged-attestation"
    EvidenceKindDoubleMining      EvidenceKind = "double-mining"
    EvidenceKindFreshnessCheat    EvidenceKind = "freshness-cheat"
)

type SlashPayload struct {
    Offender, Slasher string
    Kind              EvidenceKind
    EvidenceBlob      []byte
    SlashAmountDust   uint64
    Memo              string
}
```

Concrete `EvidenceVerifier` implementations:

| Kind | Detects | Status |
|---|---|---|
| `forged-attestation` | An HMAC bundle whose MAC fails verification, whose `gpu_uuid` mismatches the enrolled record, whose `challenge_bind` mismatches the proof, or whose `gpu_name` matches the deny-list. | **Shipped** in [`pkg/mining/slashing/forgedattest`](../../source/pkg/mining/slashing/forgedattest/). |
| `double-mining` | Two distinct accepted proofs from the same `(node_id, epoch, height)`, both crypto-valid under the registered HMAC key. | **Shipped** in [`pkg/mining/slashing/doublemining`](../../source/pkg/mining/slashing/doublemining/). Encoder canonicalises proof order so two slashers observing the same equivocation produce byte-identical evidence. |
| `freshness-cheat` | A proof whose `challenge.issued_at` is older than `FRESHNESS_WINDOW` and was nonetheless accepted (i.e. retroactive evidence of validator collusion or clock skew). | **Stubbed.** Depends on BFT finality — see §12.3. |

Production dispatcher:
[`pkg/mining/slashing/production.go`](../../source/pkg/mining/slashing/production.go).

#### Chain-side applier

[`pkg/chain/slash_apply.go`](../../source/pkg/chain/slash_apply.go)
(`SlashApplier`):

1. Decodes `SlashPayload` and dispatches to the matching
   `EvidenceVerifier`.
2. Fingerprints the evidence (`(node_id, evidence_hash)`) and
   rejects duplicates → per-fingerprint replay protection.
3. Drains `min(SlashAmountDust, record.StakeDust)` from the
   offender's bonded stake.
4. Pays a configurable `RewardBPS` fraction (capped at
   `SlashRewardCap = 5000` bps = 50%) to the slasher; burns the
   rest.
5. **Auto-revoke under-bonded records:** if the post-slash stake
   is `< AutoRevokeMinStakeDust` (default `MinEnrollStakeDust`),
   `enrollment.InMemoryState.RevokeIfUnderBonded` retires the
   record at the standard unbond window. This closes the
   "slash-to-zero, keep mining for free" loophole. Set to 0 to
   disable.

End-to-end coverage:
[`pkg/chain/slash_forgedattest_e2e_test.go`](../../source/pkg/chain/slash_forgedattest_e2e_test.go),
[`pkg/chain/slash_doublemining_e2e_test.go`](../../source/pkg/chain/slash_doublemining_e2e_test.go),
[`pkg/chain/slash_apply_autorevoke_test.go`](../../source/pkg/chain/slash_apply_autorevoke_test.go).

---

## 9. Operator surface

### 9.1 HTTP

| Endpoint | Method | Purpose | Source |
|---|---|---|---|
| `/api/v1/mining/challenge` | GET | Issue a fresh nonce challenge for §3.3 / §6. | `pkg/api/handlers.go` |
| `/api/v1/mining/enroll` | POST | Submit `qsdm/enroll/v1` transaction. | `pkg/api/handlers_enroll.go` |
| `/api/v1/mining/unenroll` | POST | Submit `qsdm/unenroll/v1` transaction. | `pkg/api/handlers_enroll.go` |
| `/api/v1/mining/slash` | POST | Submit `qsdm/slash/v1` transaction. | `pkg/api/handlers_slashing.go` |
| `/api/v1/mining/slash/{tx_id}` | GET | Read sanitised slash receipt (FIFO-bounded in-memory store). | `pkg/api/handlers_slash_query.go` |
| `/api/v1/mining/enrollment/{node_id}` | GET | Read sanitised enrollment view (`phase`, `slashable`; HMAC key never leaks). | `pkg/api/handlers_enrollment_query.go` |
| `/api/v1/mining/enrollments?cursor=&limit=&phase=` | GET | Paginated list of enrollments, lexicographic by node_id, with `Phase` filter. | `pkg/api/handlers_enrollment_list.go` |

All endpoints wired in
[`internal/v2wiring/v2wiring.go`](../../source/internal/v2wiring/v2wiring.go)
(see §9.4). Each endpoint returns 503 until its `Set*` is called,
so a misconfigured boot fails loudly rather than silently
degrading.

### 9.2 CLI — `qsdmcli`

Source: [`cmd/qsdmcli/`](../../source/cmd/qsdmcli/). Subcommands:

| Subcommand | Purpose |
|---|---|
| `qsdmcli enroll` | Build + submit `qsdm/enroll/v1`. |
| `qsdmcli unenroll` | Build + submit `qsdm/unenroll/v1`. |
| `qsdmcli slash` | Build + submit `qsdm/slash/v1` (consumes evidence-bundle bytes). |
| `qsdmcli enrollment-status` | Query `/api/v1/mining/enrollment/{node_id}`. |
| `qsdmcli enrollments` | Paginated list, with `--phase`, `--limit`, `--cursor`, `--all` flags. |
| `qsdmcli slash-receipt` | Query `/api/v1/mining/slash/{tx_id}`. |
| `qsdmcli slash-helper {forged-attestation,double-mining,inspect}` | Offline evidence-bundle assembly (see §9.3). |
| `qsdmcli watch enrollments [--phase --node-id --interval --json --once --include-existing]` | Stream phase-change / stake-delta events. Polling-only, no key required. Single-node and list modes. Emits `new`/`transition`/`stake_delta`/`dropped`/`error` events on stdout (human or JSON-Lines). See [`MINER_QUICKSTART.md` "Streaming phase-change events"](./MINER_QUICKSTART.md#streaming-phase-change-events-with-qsdmcli-watch). |

The CLI builds canonical payloads through `pkg/mining/{enrollment,
slashing}` so it shares the exact codec the mempool admission
gate validates against — no parallel hand-rolled JSON path.

### 9.3 Slash-helper — offline evidence-bundle assembly

[`cmd/qsdmcli/slash_helper.go`](../../source/cmd/qsdmcli/slash_helper.go)
+ [`cmd/qsdmcli/slash_helper_test.go`](../../source/cmd/qsdmcli/slash_helper_test.go).
Three subcommands produce / decode the canonical `EvidenceBlob`
bytes the chain-side `forgedattest` / `doublemining` decoders
consume:

```
qsdmcli slash-helper forged-attestation --proof=PATH \
                                        [--fault-class=KIND] \
                                        [--memo=STR] [--node-id=ID] \
                                        [--out=PATH] [--print-cmd]

qsdmcli slash-helper double-mining --proof-a=PATH --proof-b=PATH \
                                   [--memo=STR] [--node-id=ID] \
                                   [--out=PATH] [--print-cmd]

qsdmcli slash-helper inspect --kind=KIND \
                             (--evidence-file=PATH | --evidence-hex=HEX)
```

Pre-flight checks reject obviously-broken evidence locally
(saves a tx fee on guaranteed `verifier_failed`):

| Check | `forged-attestation` | `double-mining` |
|---|---|---|
| Proof carries `Version=2` | ✓ | ✓ (both) |
| Attestation bundle non-empty | ✓ | — |
| `node_id` matches across inputs | ✓ (--node-id) | ✓ (a vs b) |
| Same `(Epoch, Height)` | — | ✓ |
| Distinct canonical bytes | — | ✓ |

Output defaults to stdout (raw bytes; pipe directly into
`qsdmcli slash --evidence-file=-`). `--out=PATH` writes to a
0o600 file. `--print-cmd` emits a copy-pasteable
`qsdmcli slash …` snippet on **stderr** (never stdout — keeps
the bytes pipe clean). Encoder ordering is canonicalised in
`double-mining` so two slashers observing the same equivocation
produce byte-identical evidence, preserving chain-side
per-fingerprint replay protection. 21 unit tests cover happy
paths, stdout-write, stderr-only `--print-cmd`, every pre-flight
rejection, encoder round-trip, and `inspect` against
forged-attestation + double-mining blobs supplied as either
`--evidence-file` or `--evidence-hex`.

### 9.4 Production boot wiring (`internal/v2wiring`)

[`internal/v2wiring/v2wiring.go`](../../source/internal/v2wiring/v2wiring.go)
constructs the entire v2 surface in one call ordered before
`chain.NewBlockProducer`:

- `enrollment.NewInMemoryState()` — registry source-of-truth.
- `chain.NewEnrollmentApplier`, `chain.NewEnrollmentAwareApplier`.
- `doublemining.NewProductionSlashingDispatcher`,
  `chain.NewSlashApplier`, `aware.SetSlashApplier(...)` —
  failure here is a hard boot error.
- `monitoring.SetEnrollmentStateProvider(...)` — populates
  the four `qsdm_enrollment_*` gauges.
- `pool.SetAdmissionChecker(slashing.AdmissionChecker(
  enrollment.AdmissionChecker(prev)))` — the stacked mempool
  gate. Layer order: slashing > enrollment > base.
- All HTTP `Set*` hooks (mempool, query registry, lister,
  receipt store).

Post-construction `Wired.AttachToProducer(bp)` closes the knot
by setting `SetHeightFn` and `OnSealedBlock = SealedBlockHook(...)`.

The 14-test suite in
[`internal/v2wiring/v2wiring_test.go`](../../source/internal/v2wiring/v2wiring_test.go)
is the contract `cmd/qsdm/main.go` must honour. Any drift
between `Wire` and the production boot sequence is caught here,
not on mainnet.

### 9.5 Reference miner — `cmd/qsdmminer-console`

Source: [`cmd/qsdmminer-console/`](../../source/cmd/qsdmminer-console/).
This binary is the v1 reference miner with an opt-in v2
attestation path bolted on — sufficient for testnet
participation pre-`FORK_V2_TC_HEIGHT`, replaced by
`cmd/qsdm-miner-cuda` (deferred — §12.2) once the Tensor-Core
mixin activates.

Operational flow with `--protocol=v2`:

1. `--gen-hmac-key=PATH` — produces a 0o600 hex key file and
   prints the matching `qsdmcli enroll …` snippet.
2. `--setup` — opt-in v2 sub-wizard that drives operators
   end-to-end through key generation → field collection → bond
   command emission.
3. `runLoop` fetches a fresh `/api/v1/mining/challenge`, builds
   an `nvidia-hmac-v1` bundle via `pkg/mining/v2client`, and
   submits a `Version=2` proof.
4. The live console panel shows a `v2 NVIDIA` row carrying
   `node`, `arch`, `attestations`, and `challenge=Ns ago` so
   operators can spot freshness-window drift.
5. A background `EnrollmentPoller` (default 30 s,
   `--enrollment-poll`) queries
   `/api/v1/mining/enrollment/{node_id}` and emits
   `EvEnrollment` events; the panel paints `phase` / `stake` /
   `slashable` and surfaces phase-transition events.
6. A challenge-endpoint outage produces a clear `EvError` and
   refuses to fall back to v1, preventing accidental v1
   submissions to a forked validator.

Coverage gated by `TestIntegration_RunLoop_v2_EndToEnd`.

### 9.6 Observability

The slashing applier and the enrollment applier emit two
parallel observability streams, **both wired through a
dependency-inverted seam (`chain.MetricsRecorder`,
`chain.ChainEventPublisher`) so `pkg/chain` does not import
`pkg/monitoring`** — that import cycle is what historically kept
slashing observability under-instrumented. The seam is
populated automatically when `pkg/monitoring` is loaded into a
binary (`init()` in `pkg/monitoring/chain_recorder.go`); a
binary that does not import `pkg/monitoring` falls back to
`noopRecorder{}` and `NoopEventPublisher{}` and pays no
overhead.

#### 9.6.1 Prometheus metrics

Exposed by `pkg/monitoring/prometheus_scrape.go` on
`/api/metrics/prometheus`. Single-counter convention; deltas are
the operator's responsibility.

| Metric | Type | Labels | Source |
|---|---|---|---|
| `qsdm_slash_applied_total` | counter | `kind` | Successful slash transitions per evidence kind. |
| `qsdm_slash_drained_dust_total` | counter | `kind` | Dust drained from offenders. |
| `qsdm_slash_rewarded_dust_total` | counter | — | Dust paid to slashers across all kinds. |
| `qsdm_slash_burned_dust_total` | counter | — | Dust burned (not rewarded). |
| `qsdm_slash_rejected_total` | counter | `reason` (`verifier_failed`, `evidence_replayed`, `node_not_enrolled`, `decode_failed`, `fee_invalid`, `wrong_contract`, `state_lookup_failed`, `stake_mutation_failed`, `other`) | Per-reason rejection. |
| `qsdm_slash_auto_revoked_total` | counter | `reason` (`fully_drained`, `under_bonded`) | Auto-revokes (§8.2). |
| `qsdm_enrollment_applied_total` | counter | — | Successful enroll txs. |
| `qsdm_unenrollment_applied_total` | counter | — | Successful unenroll txs. |
| `qsdm_enrollment_rejected_total` | counter | `reason` | Per-reason enroll rejection. |
| `qsdm_unenrollment_rejected_total` | counter | `reason` (incl. `not_enrolled`) | Per-reason unenroll rejection. |
| `qsdm_enrollment_unbond_swept_total` | counter | — | Matured unbond windows. |
| `qsdm_enrollment_active_count` | gauge | — | Records where `Active() == true`. |
| `qsdm_enrollment_bonded_dust` | gauge | — | Sum of `StakeDust` across active records. |
| `qsdm_enrollment_pending_unbond_count` | gauge | — | Revoked records whose unbond window has not yet matured. |
| `qsdm_enrollment_pending_unbond_dust` | gauge | — | Dust still locked in pending-unbond records. |

Gauges are callback-driven via
`monitoring.SetEnrollmentStateProvider(...)` — one mutex
acquisition per scrape, O(n) in active miners. Without
enrollment, the provider is unset and the gauges read 0.

Alert rules: `deploy/observability/qsdm-mining-rules.yml`
(checked by `promtool` in CI).

#### 9.6.2 Structured events

[`pkg/chain/events.go`](../../source/pkg/chain/events.go) defines
`MiningSlashEvent` and `EnrollmentEvent`. Both are published via
`ChainEventPublisher`; default is `NoopEventPublisher{}`.
Production deployments attach a publisher (Kafka, NATS, on-chain
log emitter, audit sink) at construction.
`CompositePublisher` lets multiple sinks subscribe.

Both layers share the same canonical reason-tag string set
(`SlashRejectReason*`, `EnrollRejectReason*`) so a metric spike
on `qsdm_slash_rejected_total{reason="evidence_replayed"}` maps
1:1 onto the corresponding `MiningSlashEvent` records in the
audit sink.

---

## 10. Activation mechanics — hard fork

### 10.1 Summary

Testnet reset at a coordinated wall-clock moment. Block 0 of v2
is a fresh genesis committing: the v2 protocol version, the
NVIDIA CC root material, the initial operator registry (empty),
the deny-list (empty), and `MinEnrollStakeDust`.

### 10.2 Genesis file extension

`cmd/genesis-ceremony/main.go::Bundle` adds (at the end of the
struct, so v1 fixtures deserialise zero-valued):

```go
SchemaVersion        int                     `json:"schema_version"`
NvidiaCCRoots        []string                `json:"nvidia_cc_roots_pem"`
NvidiaCCMinFirmware  map[string]string       `json:"nvidia_cc_min_fw"`
OperatorRegistry     []RegistryEntry         `json:"operator_registry"`
GPUDenyList          []string                `json:"gpu_deny_list"`
MinEnrollStake       uint64                  `json:"min_enroll_stake_dust"`
ForkV2Params         ForkV2Params            `json:"fork_v2_params"`
```

`SchemaVersion` goes from `1` (implicit) to `2`. `VerifyBundle`
rejects a `SchemaVersion=2` bundle with zero-length
`NvidiaCCRoots`.

### 10.3 Pre-fork state disposition

All pre-fork state is discarded. The v2 genesis block is height
0. Pre-fork wallets are invalidated. Consistent with the
2026-04-24 owner sign-off (§13.4) that the testnet has no real
users.

### 10.4 Retirement of v1 binaries

At the same commit that ships `cmd/qsdm-miner-cuda` (deferred —
§12.2):

- `cmd/qsdmminer/` — removed.
- `cmd/qsdmminer-console/` — removed (current opt-in v2 path
  retires with the binary).
- `scripts/install-qsdmminer-console.*` — already removed in
  Phase 0 (`19e756a`).
- `QSDM/Dockerfile.miner-console` — already removed in Phase 0.
- `QSDM/Dockerfile.miner` — retained, renamed to
  `QSDM/Dockerfile.qsdm-miner-cuda`.
- New `cmd/qsdm-miner-cuda/` ships with the fork commit.

Until then, `cmd/qsdmminer-console` remains the reference miner
for testnet v2 attestation participation (§9.5).

### 10.5 Backward compatibility — `ComputeMixDigestV1`

The v1 PoW is renamed `ComputeMixDigestV1` and kept in-tree for
audit, protocol-conformance tests, and any future soft-unlock if
governance ever wants to re-enable non-NVIDIA mining.

---

## 11. Attacker model

### 11.1 In-scope threats

1. **CPU-only miner.** Rejected by the attestation gate; even if
   a rogue validator accepts it, the proof takes ~250x longer to
   compute on a CPU than on an NVIDIA GPU (§4.4) once the
   mixin lands. Pre-mixin: rejected on attestation alone.
2. **AMD / Intel GPU with forged `nvidia-smi` output.** The
   verifier does not trust `nvidia-smi` output directly; it
   trusts the HMAC over it. The HMAC key binds to a registered
   `(node_id, gpu_uuid)`. To mine from an AMD GPU the attacker
   needs a real registered NVIDIA GPU's HMAC key. If they have
   that, the registered operator is running two miners from one
   key — detectable as `double-mining` evidence (§8.2) and
   slashable.
3. **Nonce replay.** Prevented by `FRESHNESS_WINDOW` and the
   validator's nonce ring buffer (§6).
4. **Stale proof.** Same mitigation.
5. **Rogue validator accepting unattested proofs.** Such a
   validator is in consensus minority if >50% honest, so its
   proposed blocks lose in the PoE+BFT commit round. If it is
   the honest majority, the chain has a bigger problem than the
   attestation rule.
6. **Sybil enrollment.** `MinEnrollStakeDust` puts a Cell cost
   on creating many fake `node_id`s.
7. **HMAC key leak / rental.** Detectable as `forged-
   attestation` (gpu_uuid mismatch) or `double-mining`
   (equivocation under the same key) and slashable. Stake bonds
   the key to honest behaviour.

### 11.2 Out-of-scope threats

1. **NVIDIA CA compromise.** If NVIDIA's attestation root is
   compromised, every NVIDIA-CC-based chain world-wide has a
   problem. Mitigation: governance-driven root rotation via
   chain-config delta.
2. **Operator leaks their HMAC key publicly.** Revocable
   on-chain. Before revocation clears, the attacker mines from
   the operator's identity; the operator loses reputation and
   staked Cell. The attacker can't redirect rewards because
   `miner_addr` is HMAC'd over.
3. **Side-channel attack on an operator's TPM / key storage.**
   Out of scope for consensus; same issue as every PoS chain's
   validator key.

---

## 12. Deferred work register

Work that v2 reserves wire-room for but does not yet ship.
None of it blocks v2 activation; all of it is upgradable
behind feature gates.

### 12.1 Real-world `nvtrust` bundle framing for `nvidia-cc-v1`

`pkg/mining/attest/cc/` ships a Go-native `cc.Bundle` shape
that mirrors what an `nvtrust` quote contains (cert chain +
AIK signature over the spec preimage + PCR-equivalent
versions). The verifier flow (§3.2) is consensus-complete.
What's deferred:

- **NVIDIA-issued real-world test vectors.** Determinism in
  `cc/testvectors.go` is enough for CI and protocol regression
  testing, but a real H100 / B100 produces an AIK quote whose
  on-the-wire framing is NVIDIA-proprietary. The seam to swap
  in real `nvtrust` framing is a single `ParseBundle`
  reimplementation; the verifier code does NOT change.
- **Genesis-pinned NVIDIA root rotation.** `VerifierConfig.PinnedRoots`
  is plumbed end-to-end; ratifying the actual NVIDIA-issued
  Hopper/Blackwell root cert at v2 fork-time is a separate
  governance decision.
- **CUDA-side miner integration.** Once `cmd/qsdm-miner-cuda`
  ships (§12.2), it produces live CC bundles using the
  on-host nvtrust SDK; today only `cmd/qsdmminer-console`
  produces v2 attestations and it produces `nvidia-hmac-v1`
  only.

Hard external dependencies: NVIDIA NGC Attestation Service
contract; physical Hopper / Blackwell GPU for swap-in
test vectors. Estimated remaining work post-hardware: **~5
days** (down from the original ~8 — verifier pipeline is
already done).

### 12.2 Tensor-Core PoW kernel

Specified in §4. Ships as `cmd/qsdm-miner-cuda` containing:

1. A CUDA kernel performing the §4.2 mixin (per nonce attempt,
   16 dependent `mma.m16n8k16.f16` Tensor-Core ops over a
   deterministic matrix derived from `(prev_block_hash ||
   nonce_high)`, then folded into the standard double-SHA256
   outer hash).
2. A non-CUDA fallback (slow, ~1000× slower than RTX 4090) for
   validator use.
3. A pure-Go validator-side reference impl in
   `pkg/mining/pow/v2/`.
4. A calibration suite that pins difficulty so an RTX 4090
   hits ~1 block / 30 s on a ~1000-validator testnet (numbers
   TBD against real hardware).

Hard external dependencies: working CUDA Toolkit 12.x in CI
(self-hosted GPU runner OR cross-compile + offline smoke
test); at least one RTX 4090 for difficulty calibration. The
mixin is gated behind a second fork height
(`FORK_V2_TC_HEIGHT`) so it can activate as a soft-rejection
fork (validators get stricter), no chain reset required.

`mma.m16n8k16.f16` is Ampere+ only — Turing miners (RTX
20-series) cannot mine v2 even with a CUDA build. We owe
miners a deprecation notice for pre-Ampere cards before the
fork.

Estimated work: **~14 days** post-hardware.

### 12.3 `freshness-cheat` slasher

Detects a proof whose `challenge.issued_at` is older than
`FRESHNESS_WINDOW` and was nonetheless accepted (i.e.
retroactive evidence of validator collusion or clock skew).
Last item in the slashing trilogy; gated on BFT finality
landing first, because the verifier needs a quorum statement
of-the-form "block at height H accepted proof P that was stale
relative to its parent block's wall-clock anchor".

Estimated work: **~4 days** plus design review, plus the BFT
finality dependency.

### 12.4 `qsdm/gov/v1` runtime tuning hook

`SlashApplier.RewardBPS` and `AutoRevokeMinStakeDust` are
construction-time parameters today. A `qsdm/gov/v1` transaction
type would let governance retune them at runtime. Estimated
work: **~2 days** once the governance contract type is defined.

---

## 13. Historical decision record

### 13.1 Trust-anchor model — RATIFIED

> Tiered. NVIDIA-CC-pinned for Hopper / Blackwell Confidential
> Computing GPUs, plus Registered-operator HMAC for consumer RTX
> cards.

Ratified 2026-04-24, project owner, in-chat decision. Spec
revision: `6826bc4` of `MINING_PROTOCOL_V2_NVIDIA_LOCKED.md`
(now superseded by this doc).

Rationale:

- Keeps consumer NVIDIA GPUs (Turing / Ampere / Ada) eligible to
  mine. Without a consumer path the chain is effectively a
  datacenter-only product.
- Gives CC-capable datacenter GPUs a strictly stronger crypto
  guarantee than the HMAC path. Operators who have paid for CC
  hardware get the attestation value they paid for.
- Implementation cost is manageable — both paths compose with
  existing code (the HMAC path extends
  `pkg/monitoring/nvidia_hmac.go`; the CC path uses `crypto/x509`
  plus a pinned-root genesis extension).

Two `Attestation.Type` values land: `nvidia-cc-v1` and
`nvidia-hmac-v1`. Verifier dispatches on `Attestation.Type`.

### 13.2 `MIN_ENROLL_STAKE` — RATIFIED

> Initial enrollment stake required to register a
> `(node_id, gpu_uuid, hmac_key)` tuple in the
> `nvidia-hmac-v1` operator registry.

Ratified 2026-04-24: **10 CELL.** Encoded as
`MinEnrollStake = 10 * 10^8` dust in the v2 genesis ceremony
bundle.

Rationale:

- Low enough that a miner with roughly one day of pre-mining can
  self-fund enrollment, which keeps onboarding accessible.
- High enough that thousand-GPU Sybil enrollments cost
  10,000 CELL locked for 30 days — comparable to the cost of
  the GPUs themselves, so not a free attack.

### 13.3 `FRESHNESS_WINDOW` — RATIFIED

> Maximum age of an attestation nonce / issued-at timestamp
> before a proof carrying it becomes stale.

Ratified 2026-04-24: **60 seconds.**

Rationale:

- Short enough that a replayed bundle becomes invalid within one
  block-production cycle.
- Long enough that a miner on a slow residential link has time
  to fetch a challenge, compute a proof, and submit it without
  false-positive rejection.
- Symmetric around the validator nonce ring-buffer retention
  (`2 × FRESHNESS_WINDOW = 120 s`) which the spec defines for
  same-challenge double-spend protection.

### 13.4 Chain reset — RATIFIED

The original spec deferred `FORK_V2_HEIGHT` to Phase 4. The
2026-04-24 owner sign-off resolved it via the chain-reset path:
v2 launches via genesis, so `FORK_V2_HEIGHT = 0`. Justification:
the testnet has no real users, so resetting Cell balances has no
custodial impact. Pre-fork wallets are invalidated.

`FORK_V2_TC_HEIGHT` (the second fork that activates the §4 PoW
mixin) remains deferred — see §12.2.

### 13.5 Revocation

These ratifications can be revisited at any time by a new
sign-off recorded as an additional section here. Changing a
ratified parameter after Phase 2 code has shipped may require
corresponding code changes and should be coordinated with the
existing activation plan.

---

## 14. Cross-references

### 14.1 Source-of-truth Go files

- Wire format + verifier:
  - [`pkg/mining/proof.go`](../../source/pkg/mining/proof.go),
    [`pkg/mining/verifier.go`](../../source/pkg/mining/verifier.go),
    [`pkg/mining/fork.go`](../../source/pkg/mining/fork.go).
- Attestation:
  - [`pkg/mining/attest/dispatcher.go`](../../source/pkg/mining/attest/dispatcher.go).
  - CC: [`pkg/mining/attest/cc/`](../../source/pkg/mining/attest/cc/)
    (`bundle.go`, `verifier.go`, `testvectors.go`, `stub.go`).
  - HMAC: [`pkg/mining/attest/hmac/`](../../source/pkg/mining/attest/hmac/)
    (`bundle.go`, `verifier.go`).
  - Challenge crypto: [`pkg/mining/challenge/`](../../source/pkg/mining/challenge/).
- Enrollment:
  - [`pkg/mining/enrollment/`](../../source/pkg/mining/enrollment/)
    (`types.go`, `registry.go`, `admit.go`, `stats_test.go`,
    `revoke_underbonded_test.go`).
  - Chain-side applier: [`pkg/chain/enroll_apply.go`](../../source/pkg/chain/enroll_apply.go),
    [`pkg/chain/applier.go`](../../source/pkg/chain/applier.go).
- Slashing:
  - Data model: [`pkg/mining/slashing/types.go`](../../source/pkg/mining/slashing/types.go).
  - Concrete verifiers:
    [`pkg/mining/slashing/forgedattest/`](../../source/pkg/mining/slashing/forgedattest/),
    [`pkg/mining/slashing/doublemining/`](../../source/pkg/mining/slashing/doublemining/).
  - Production dispatcher:
    [`pkg/mining/slashing/production.go`](../../source/pkg/mining/slashing/production.go).
  - Mempool admission:
    [`pkg/mining/slashing/admit.go`](../../source/pkg/mining/slashing/admit.go).
  - Chain-side applier:
    [`pkg/chain/slash_apply.go`](../../source/pkg/chain/slash_apply.go),
    [`pkg/chain/slash_receipts.go`](../../source/pkg/chain/slash_receipts.go).
- HTTP:
  - [`pkg/api/handlers.go`](../../source/pkg/api/handlers.go),
    [`pkg/api/handlers_enroll.go`](../../source/pkg/api/handlers_enroll.go),
    [`pkg/api/handlers_slashing.go`](../../source/pkg/api/handlers_slashing.go),
    [`pkg/api/handlers_slash_query.go`](../../source/pkg/api/handlers_slash_query.go),
    [`pkg/api/handlers_enrollment_query.go`](../../source/pkg/api/handlers_enrollment_query.go),
    [`pkg/api/handlers_enrollment_list.go`](../../source/pkg/api/handlers_enrollment_list.go).
- CLI: [`cmd/qsdmcli/`](../../source/cmd/qsdmcli/)
  (`mining.go`, `slash_helper.go`, `slash_helper_test.go`).
- Reference miner:
  [`cmd/qsdmminer-console/`](../../source/cmd/qsdmminer-console/)
  (`v2.go`, `enrollment_poller.go`, `v2_integration_test.go`).
- Production wiring:
  [`internal/v2wiring/`](../../source/internal/v2wiring/)
  (`v2wiring.go`, `v2wiring_test.go`); consumed by
  [`cmd/qsdm/main.go`](../../source/cmd/qsdm/main.go).
- Observability:
  [`pkg/chain/events.go`](../../source/pkg/chain/events.go),
  [`pkg/monitoring/chain_recorder.go`](../../source/pkg/monitoring/chain_recorder.go),
  [`pkg/monitoring/slashing_metrics.go`](../../source/pkg/monitoring/slashing_metrics.go),
  [`pkg/monitoring/enrollment_metrics.go`](../../source/pkg/monitoring/enrollment_metrics.go),
  [`pkg/monitoring/enrollment_state_provider.go`](../../source/pkg/monitoring/enrollment_state_provider.go),
  [`pkg/monitoring/prometheus_scrape.go`](../../source/pkg/monitoring/prometheus_scrape.go).

### 14.2 Other docs

- v1 spec: [`MINING_PROTOCOL.md`](./MINING_PROTOCOL.md) (frozen).
- Miner quick start:
  [`MINER_QUICKSTART.md`](./MINER_QUICKSTART.md).
- Validator quick start:
  [`VALIDATOR_QUICKSTART.md`](./VALIDATOR_QUICKSTART.md).
- Node roles: [`NODE_ROLES.md`](./NODE_ROLES.md).
- Cell tokenomics:
  [`CELL_TOKENOMICS.md`](./CELL_TOKENOMICS.md).
- NVIDIA-lock consensus scope:
  [`NVIDIA_LOCK_CONSENSUS_SCOPE.md`](./NVIDIA_LOCK_CONSENSUS_SCOPE.md).
- Phase 0 retirement decision: commit `19e756a`.

### 14.3 Superseded predecessors (kept as redirect stubs)

- [`MINING_PROTOCOL_V2_NVIDIA_LOCKED.md`](./MINING_PROTOCOL_V2_NVIDIA_LOCKED.md)
  — original Phase-1 design draft.
- [`MINING_PROTOCOL_V2_RATIFICATION.md`](./MINING_PROTOCOL_V2_RATIFICATION.md)
  — 2026-04-24 owner sign-off (now §13 here).
- [`MINING_PROTOCOL_V2_TIER3_SCOPE.md`](./MINING_PROTOCOL_V2_TIER3_SCOPE.md)
  — rolling shipped-vs-deferred status doc (now folded into
  §§5–12 here).
