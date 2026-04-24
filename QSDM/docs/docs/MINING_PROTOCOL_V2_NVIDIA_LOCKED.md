# MINING_PROTOCOL_V2_NVIDIA_LOCKED.md — Design Spec (Phase 1)

> **Status:** DRAFT, not yet normative. Blocks implementation in
> Phase 2+ until explicitly ratified by the project owner.
>
> **Scope:** Specifies a hard-fork upgrade of the QSDM mining
> sub-protocol from `v1` (per `MINING_PROTOCOL.md`) to `v2`
> ("NVIDIA-locked"). `v2` makes NVIDIA-GPU mining a consensus
> precondition — validators MUST reject proofs without a valid
> attestation — and biases the proof-of-work so that Tensor Core
> hardware dominates any non-NVIDIA implementation economically.
>
> **Audience:** The project owner (for ratification of the three
> OPEN_QUESTIONs at the end), protocol implementers, and anyone
> reviewing the planned consensus changes.
>
> **Supersedes:** `MINING_PROTOCOL.md §§1.1(2), 5, 6, 7` on
> activation.
>
> **Does not supersede:** `CELL_TOKENOMICS.md` on issuance
> schedule (the fork resets the chain — see §8 — but the emission
> curve stays). `NODE_ROLES.md` on the validator/miner split.
> `nvidia_locked_qsdmplus_blockchain_architecture.md`, which is the
> high-level vision document this spec operationalises.
>
> **Why a standalone doc:** The v1 spec explicitly states
> `MINING_PROTOCOL.md §1.1(2) "GPU-favored, NVIDIA-favored,
> NVIDIA-not-required"`. Amending v1 in-place would lose the
> audit trail. v1 stays frozen as the testnet spec; v2 becomes
> normative at the fork height.

---

## 0. Executive summary

1. **Hard fork** at a chosen block height. Call that height
   `FORK_V2_HEIGHT`. Blocks `< FORK_V2_HEIGHT` follow v1. Blocks
   `>= FORK_V2_HEIGHT` follow v2.

2. **Consensus change:** `pkg/mining/proof.go::Proof.Attestation`
   becomes mandatory. Validators MUST reject any proof whose
   `attestation.type` is empty, unparseable, outside a whitelist of
   recognised types, stale beyond a freshness window, or fails
   cryptographic verification against the genesis-pinned trust
   anchors. Today this field is explicitly a
   "transparency signal, not a consensus rule"
   (`MINING_PROTOCOL.md §6`). v2 flips that clause.

3. **Trust anchor:** (recommendation pending sign-off of §5's
   OPEN_QUESTION_1) — `Tiered`: datacenter-grade NVIDIA CC GPUs
   (Hopper / Blackwell) go through NVIDIA-signed device
   attestation; consumer NVIDIA GPUs (Turing / Ampere / Ada
   RTX series) go through an
   extended version of the existing HMAC + `nvidia-smi` +
   server-nonce model pinned at genesis. Two attestation
   `type` values, one verifier dispatch.

4. **PoW change:** `pkg/mining/pow.go::ComputeMixDigest` gains a
   Tensor-Core mixin — a deterministic FP16/BF16 matrix multiply
   folded into each of the 64 DAG walk steps. Non-NVIDIA GPUs
   and CPUs can still compute the function but run it ~50-200x
   slower than an NVIDIA Tensor Core does. This is the
   "economic" half of the A+B lock: even if someone bypassed
   the attestation check with a rogue validator, the proof
   itself is uneconomic to compute without a Tensor Core.

5. **Chain reset.** The fork is implemented by discarding the
   entire pre-fork state and producing a new genesis block at
   `FORK_V2_HEIGHT = 0`. Pre-fork Cell balances are NOT carried
   forward. Justified by the project owner's assessment that the
   testnet has no real users (2026-04-24 sign-off).

6. **Miner software retirement.** `cmd/qsdmminer` and
   `cmd/qsdmminer-console` are removed from the tree as part of
   the Phase 3 implementation. A new `cmd/qsdm-miner-cuda` binary
   (GPU-only, statically requires NVIDIA runtime) becomes the
   sole supported miner. `Dockerfile.miner` (existing CUDA image)
   is retitled `Dockerfile.qsdm-miner-cuda` and becomes the only
   miner container.

7. **Timeline.** Phase 2 = verifier changes (this doc plus ~2-3
   days of Go work). Phase 3 = CUDA miner + Tensor-Core PoW
   implementation (~2-3 days). Phase 4 = genesis ceremony
   extension + testnet activation (~1 day). Phase 5 = docs &
   landing page rewrite (~1 day).

---

## 1. What changes relative to v1

All references below are to the v1 spec file
`QSDM/docs/docs/MINING_PROTOCOL.md` and to Go source paths under
`QSDM/source/`.

| Area | v1 (current) | v2 (this spec) |
|---|---|---|
| Goal §1.1(2) | "GPU-favored, NVIDIA-favored, NVIDIA-not-required. Portable OpenCL / Vulkan / CPU fallbacks MUST remain compilable and correct — they only lose economically." | **"NVIDIA-required."** CPU / OpenCL / Vulkan implementations of `pkg/mining.ComputeMixDigest` remain compilable for protocol auditing, but proofs produced by them are unconditionally rejected — not because the hash function fails but because the attestation layer fails. |
| `Proof.Attestation` field | Optional. An absent, stale, or unverifiable attestation MUST NOT cause rejection (`§6`). | **Mandatory.** Fully-zero `Attestation` → consensus reject. Unparseable → reject. Type not in whitelist → reject. Signature / HMAC invalid → reject. Stale → reject. Verified → accept. |
| `Proof.ProtocolVersion` | `1` | `2` |
| `Attestation.Type` whitelist | `"ngc-v1"` (informational only). | `"nvidia-cc-v1"` (Hopper/Blackwell Confidential Computing, NVIDIA-signed); `"nvidia-hmac-v1"` (consumer GPUs, HMAC-bound by registered node). Extensible, whitelist check at verifier. |
| Trust anchor | None (attestation never verified by the mining verifier — `pkg/mining/verifier.go::Verifier.Verify` never reads `Attestation`). | Genesis-pinned NVIDIA root material (CC path) + genesis-pinned operator registry (HMAC path) — see §5. |
| PoW hash | SHA3-256 in a 64-step DAG walk (`pkg/mining/pow.go::ComputeMixDigest`). | SHA3-256 + **Tensor-Core FP16 matmul mixin** at every DAG walk step. Same output domain, same verification complexity, same validator SLO. ~50-200x faster on a Tensor Core than on CPU / non-NVIDIA GPU. |
| Validator SLO | Verify any single proof in < 100 ms single-core, batch 1000 in < 2s (`§1.1(4)`). | Unchanged. Verification uses only the CPU path — Tensor Core execution is only on the miner side; the validator re-hashes the claimed work via a deterministic CPU reference. |
| Attestation endpoint `/api/v1/monitoring/ngc-proof` | Monitoring-only sink, never feeds consensus (`pkg/api/handlers.go::NGCProofIngest`, `pkg/api/handlers_trust.go` L19-22). | Repurposed. Consensus now reads from the same bundles that ingest produces — but via a new non-HTTP path (proofs carry their bundle inline; ingest endpoint survives for dashboards). |

## 2. What does NOT change

1. Validators remain CPU-only. The NVIDIA lock is on the *miner*
   side of the miner/validator split (see `NODE_ROLES.md`). A
   validator never verifies an NGC signature against a GPU it
   owns; it verifies against the genesis-pinned NVIDIA roots.
2. Cell tokenomics (`CELL_TOKENOMICS.md`) — emission curve,
   halving interval, treasury fraction — are unchanged. The fork
   resets the supply to zero at height 0 because we have no
   users to honor, but the curve that drives issuance from that
   point is identical to v1.
3. PoE+BFT consensus among validators (`pkg/chain`, `pkg/consensus`)
   is unchanged. v2 touches mining only.
4. Proof-ID derivation (`pkg/mining/proof.go::ID`) still excludes
   `Attestation` from the hash input. This is deliberate: two
   validly-signed proofs with identical `(epoch, height, nonce,
   batch_root)` and different attestation bundles share a
   proof-id. The verifier will reject the second one for
   duplicate-proof reasons, not for attestation reasons.
5. The separate `apps/qsdmplus-nvidia-ngc/` sidecar keeps
   operating and keeps pushing to
   `/api/v1/monitoring/ngc-proof`. That path is dashboards and
   transparency surface; it does not feed consensus. Operators
   of CC-capable GPUs may continue to use it in parallel with
   the inline attestation their miner now emits.

## 3. Wire format

### 3.1 `Attestation` struct (v2)

```go
// pkg/mining/proof.go after the v2 fork. Field order is normative
// per MINING_PROTOCOL.md §4.1 canonical-JSON rules. Do NOT
// reorder.
type Attestation struct {
    Type                 string `json:"type"`
    BundleBase64         string `json:"bundle"`
    GPUArch              string `json:"gpu_arch"`
    ClaimedHashrateHPS   uint64 `json:"claimed_hashrate_hps"`

    // NEW in v2. Pinned outside Bundle so the verifier can
    // deserialize enough metadata to dispatch to the right
    // verify path without parsing a variable-schema nested
    // document.
    Nonce                [32]byte `json:"nonce"`     // server-issued freshness challenge, serialized as lowercase hex
    IssuedAt             int64    `json:"issued_at"` // unix seconds; validator's clock tolerance is FRESHNESS_WINDOW
}
```

Canonical JSON wire order, nested in `Proof`, for v2:

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

### 3.2 `Attestation.Bundle` payload (per type)

#### 3.2.1 `type = "nvidia-cc-v1"` (datacenter Confidential Computing)

Bundle is a base64-encoded concatenation of:

1. **NVIDIA device certificate chain** — the per-GPU attestation
   certificate chain produced by NVIDIA's CC toolchain
   (`nvtrust` / equivalent) for Hopper / Blackwell SXM /
   PCIe-CC-enabled parts. Rooted in an NVIDIA issuing CA whose
   public key is pinned at genesis (§5.1).
2. **Quote** — a signed statement from the GPU AIK (Attestation
   Identity Key) over the tuple:
   `H(device_uuid || challenge_nonce || issued_at || miner_addr ||
      batch_root || mix_digest)`
   where `challenge_nonce == Attestation.Nonce` and the other
   fields are taken from the enclosing `Proof`.
3. **PCR-equivalent measurements** — current GPU firmware
   version + driver version, as recorded by the CC subsystem, so
   a downgrade-to-vulnerable-firmware attack is detectable.

Verifier flow:

```
1. Parse chain; verify it terminates in a genesis-pinned NVIDIA CA
   public key.
2. Verify Quote signature against the AIK in the chain.
3. Check that challenge_nonce matches Attestation.Nonce.
4. Check that Attestation.Nonce was issued by this validator
   (or a validator we trust — see §4.3) no more than
   FRESHNESS_WINDOW seconds ago (FRESHNESS_WINDOW = 60s, see §6).
5. Check PCR measurements against the genesis-pinned minimum
   firmware / driver versions.
6. If all pass → proof is attested. Else → reject.
```

#### 3.2.2 `type = "nvidia-hmac-v1"` (consumer GPUs)

Bundle is a base64-encoded canonical-JSON object:

```json
{
  "node_id":              "<operator-registered GPU handle, e.g. 'alice-rtx4090-01'>",
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

Note: `challenge_sig` and `challenge_signer_id` were added in
Phase 2c-iii (commits `51e1c5e` + `71ba995`) so the validator can
prove, before running the expensive HMAC check, that
`(nonce, issued_at)` was actually minted by some known validator
rather than being an unsourced value the miner made up.
Canonical-form field order is alphabetical on the JSON key —
`challenge_sig` and `challenge_signer_id` land between
`challenge_bind` and `compute_cap`, not in the order shown above
(which is the human-reading order, grouped by topic). Reference
implementation: `pkg/mining/attest/hmac/bundle.go` (wire) +
`pkg/mining/challenge/` (issuer/verifier crypto).

Verifier flow:

```
1. Parse JSON.
2. Recompute H(miner_addr || batch_root || mix_digest) from the
   enclosing Proof; assert it matches bundle.challenge_bind.
3. Look up bundle.node_id in the genesis-pinned operator
   registry (§5.2). If absent → reject.
4. Fetch the HMAC key associated with that node_id from the
   registry. Recompute HMAC-SHA256 over the canonical-JSON of
   the bundle minus the hmac field. Reject on mismatch.
5. Fetch the GPU UUID associated with that node_id from the
   registry. Assert bundle.gpu_uuid matches. Reject on mismatch.
   This is what binds a single operator key to a single GPU —
   absent this check, one key could mine forever on any hardware.
6a. Assert bundle.nonce matches Attestation.Nonce and
    bundle.issued_at matches Attestation.IssuedAt.
6b. If a ChallengeVerifier is configured (production MUST):
    reconstruct challenge.Challenge{Nonce, IssuedAt, SignerID,
    Signature} from bundle.{nonce, issued_at, challenge_signer_id,
    challenge_sig} and verify the signature using the SignerID's
    registered public key. Reject unknown signer_id or bad
    signature.
6c. Assert bundle.issued_at falls within FRESHNESS_WINDOW of the
    validator's wall clock (and ≤ AllowedFutureSkew ahead).
6d. Check the nonce-replay cache; reject if (node_id, nonce)
    already seen.
7. Assert bundle.gpu_name does NOT contain any of the deny-list
   strings (see §5.3 deny-list — empty at genesis; governance
   can append).
8. Verify the Tensor-Core mix_digest (§4) is consistent with
   the claimed gpu_arch — if an RTX 4090 claims to be
   "hopper", reject.
9. If all pass → proof is attested. Else → reject.
```

HTTP issuer endpoint (Phase 2c-iii):

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

**Why HMAC and not a device key?** See §5 for the full
justification. Short version: consumer NVIDIA GPUs have no
publicly-accessible cryptographic device key. The only
NVIDIA-signed identity they expose is a UUID string from
`nvidia-smi`, which any Go program can fake. HMAC binds the UUID
and the node_id to a registered operator secret at genesis; the
operator is trusted not to lend their secret to an AMD miner.
This is "economic lock" rather than "cryptographic lock" for
consumer cards. See §9 for the attacker model and why the
combination with the Tensor-Core PoW mixin makes this
uneconomic to bypass.

### 3.3 `Proof` struct total wire change

```go
// pkg/mining/proof.go — v2 layout. Protocol version is the only
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

Zero-value `Attestation{}` → `validateShape` → reject with
`ErrAttestationRequired`. This is the hard invariant the verifier
enforces above every other check.

## 4. Tensor-Core PoW mixin

### 4.1 Why a PoW mixin at all

The attestation gate in §3 is the consensus rule. A rogue
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

The matmul output is deterministic IEEE-754 FP16 (with a pinned
rounding mode — `round-to-nearest-even`, CUDA default), so the
validator's CPU reference implementation produces bit-identical
`tc` to the miner's Tensor Core. The 32-byte canonical encoding
appends both the 16 FP16 values and a fixed 0-byte pad so the
hash input stays at a constant length.

### 4.3 Validator cost

A single `ComputeMixDigest` call on a CPU now costs ~64 × (one
sha3 + one 16×16 FP16 matmul). A 16×16 FP16 matmul is ~4000
FLOP on a modern x86 — the `gonum.org/v1/gonum/blas/gonum` path
covers this in under 10 µs. Single-proof CPU verify budget
therefore moves from ~60 µs (sha3 only) to ~700 µs. Still
comfortably inside the `MINING_PROTOCOL.md §1.1(4) < 100 ms`
validator SLO.

### 4.4 Miner cost

On an RTX 4090 Tensor Core: 16×16 FP16 matmul per dispatched
thread completes in ~20 ns, ~250x faster than CPU. A Hopper
H100 with FP16 Tensor Cores does the same in ~8 ns. A
straightforward CUDA kernel (`cuda_v2_mixdigest.cu`) runs the
full 64-step loop in one thread and dispatches millions of
threads in parallel. Expected hashrate: ~5 MH/s on RTX 4090,
~20-40 MH/s on H100. CPU miner: ~0.02 MH/s (250x slower). That
is the economic lock.

### 4.5 Backward compatibility

The v1 function is renamed `ComputeMixDigestV1` and kept in-tree
for: (i) replaying pre-fork blocks during state migration (even
though the supply resets, chain-history replay is useful for
audit); (ii) protocol-conformance tests; (iii) any future
soft-unlock if governance ever wants to re-enable non-NVIDIA
mining.

## 5. Trust anchors — recommendation

This is the OPEN_QUESTION section. It needs explicit owner
sign-off before Phase 2 implementation begins.

### 5.1 CC path trust anchor (Hopper / Blackwell)

**Recommendation:** `Tiered/NGC-pinned`. At genesis, embed:

- The NVIDIA device-attestation CA root public key(s) — published
  by NVIDIA in their confidential-computing documentation.
- A list of accepted NVIDIA attestation-chain issuers (e.g.
  "NVIDIA Attestation CA", "NVIDIA RIM Issuing CA").
- A minimum firmware / driver floor per supported architecture
  (Hopper SM90, Blackwell SM100).

This removes the runtime dependency on NVIDIA's live attestation
HTTP service (the `NGC-live` option in the original menu). When
NVIDIA rotates their root material, we handle it the same way
every pinned-root system does: a governance-gated chain-config
update committing the new root, activated at a future height.

**Why not NGC-live:** the validator SLO (`<100 ms` per proof)
does not tolerate a synchronous HTTPS round-trip to NVIDIA's
attestation service on every proof. Caching mitigates that but
not to the point where we can keep <100 ms tail. Also, a
dependency on NVIDIA's live service is a dependency on NVIDIA's
uptime — a chain halt caused by an NVIDIA service outage is not
acceptable.

**Why not purely physical bootstrap for CC:** if NVIDIA has done
the work of issuing a device certificate chain per GPU, using it
is strictly better than rebuilding our own TOFU registry. We
get the chain-of-trust from NVIDIA; we just pin their root.

### 5.2 Consumer GPU trust anchor (Turing / Ampere / Ada)

**Recommendation:** `Registered-operator` — an extension of the
existing HMAC model in `pkg/monitoring/nvidia_hmac.go`.

At genesis (§8.2), the project publishes a registry schema:

```
node_id:     UTF-8 string, <= 64 bytes
gpu_uuid:    exact UUID string reported by nvidia-smi --query-gpu=uuid
pub_key:     ed25519 public key of the operator (for future
             governance votes on registry updates)
hmac_key:    32 random bytes, shared secret between operator
             and the registry. This is what binds a
             (node_id, gpu_uuid) tuple to a specific operator.
```

Miners onboard by:

1. Submitting an enrollment transaction to a validator
   containing `(node_id, gpu_uuid, pub_key)` plus a stake in
   Cell (anti-Sybil, see §5.4).
2. Validator returns the generated `hmac_key` encrypted to the
   operator's `pub_key`.
3. Operator stores the `hmac_key` in their miner config.
4. From then on, every proof the miner emits carries a
   `nvidia-hmac-v1` bundle signed with that key.

Revocation: operators or governance can revoke a `node_id` by
on-chain transaction; the genesis-pinned registry is mutable
via consensus-committed deltas.

**Why this is not cryptographically airtight.** An operator with
a legitimately-registered `(node_id, gpu_uuid, hmac_key)` tuple
can lend their HMAC key to an accomplice running on an AMD GPU
that reports a fake `gpu_uuid` matching the registered one. The
verifier cannot distinguish. See §9 for why this is acceptable:
the Tensor-Core PoW mixin makes the AMD bypass uneconomic, and
the stake-at-enrollment makes Sybil attacks expensive.

**Why not a real device key:** consumer NVIDIA GPUs (RTX series
through Ada) do NOT expose a per-device cryptographic key to
userland. NVIDIA does not ship an equivalent of TPM EK for
consumer GPUs. Hopper and Blackwell CC parts do — hence the CC
path in §5.1. For consumer cards the HMAC model is the best
available approximation to "this proof came from a specific
NVIDIA GPU operated by a specific person we've registered."

### 5.3 Deny-list

Genesis also embeds a deny-list of GPU name substrings that must
not appear in any `nvidia-hmac-v1` bundle (`bundle.gpu_name`).
Initially empty. Governance can append strings (e.g. a future
revelation that a particular card model has a driver bypass
attackers are abusing). Enforcement is in the verifier — see
§3.2.2 step 7.

### 5.4 Stake-at-enrollment (anti-Sybil)

Enrollment transactions must lock `MIN_ENROLL_STAKE` Cell at
the validator. Stake unlocks 30 days after a revocation
transaction clears. Exact value of `MIN_ENROLL_STAKE` is an
open governance parameter; Phase 4 proposes `10 CELL` on testnet
with the understanding that mainnet activation tunes it higher
based on observed network conditions.

### 5.5 The three options side by side

| | NGC-live | NGC-pinned (CC) + Registered-operator (consumer) — RECOMMENDED | Physical bootstrap only |
|---|---|---|---|
| Hardware cost to mine | Any NVIDIA CC GPU | Any NVIDIA GPU (CC or consumer) | Any NVIDIA GPU pre-registered at genesis |
| Crypto soundness | NVIDIA-signed per proof | CC: NVIDIA-signed; Consumer: HMAC + stake | Only as strong as the genesis registration ceremony |
| Implementation cost on top of current tree | Very high — NGC live API is not used anywhere | Medium — extends `pkg/monitoring/nvidia_hmac.go` | Medium — new GPU-key registry |
| NVIDIA dependency | Live HTTP API dependency per proof | Only for CC GPU cert rotation events | None after genesis |
| Acceptable for consumer RTX cards | No (CC only) | Yes | Yes (if registered) |
| Scales beyond a few hundred GPUs | Yes (NVIDIA scales) | Yes (on-chain enrollment) | Poorly (registry bloat at genesis) |
| Validator-SLO risk | High (per-proof HTTP) | None | None |

### 5.6 OPEN_QUESTION_1

**Accept §5.1 + §5.2 tiered recommendation?** Default
recommendation is yes. Sign-off required before Phase 2
implements the verifier.

## 6. Freshness window & nonce issuance

### 6.1 The problem

Without a freshness mechanism, a miner could record one valid
attestation bundle and replay it in every future proof. The
attestation check would pass but the bundle conveys no evidence
about the specific proof it's paired with.

### 6.2 The solution

1. Every validator exposes `GET /api/v1/mining/challenge` (new
   endpoint). Response is a 32-byte random nonce + an issued-at
   timestamp + a signature over both by the validator's
   consensus signing key.
2. A miner fetches a challenge before starting a round. The
   miner's attestation bundle MUST include that exact nonce
   (and issued-at) in `Attestation.Nonce` / `Attestation.IssuedAt`.
3. A proof is stale if `issued_at + FRESHNESS_WINDOW < now`.
   `FRESHNESS_WINDOW = 60s` balances: short enough that a
   replayed bundle becomes invalid within one block cycle, long
   enough that a miner on a slow link has time to fetch +
   compute + submit.
4. The validator can verify a challenge it didn't issue by
   checking the validator-signature — any validator's challenge
   is accepted as long as it's within the freshness window.
   This prevents a single-validator DoS where the network stalls
   because one validator's challenge service is down.

### 6.3 Nonce store

Validators remember issued nonces in a ring buffer for `2 *
FRESHNESS_WINDOW`; a proof that reuses a nonce already seen in
the same bundle type → reject (prevents same-challenge double-spend).

## 7. Verifier state

### 7.1 Changes to `pkg/mining/verifier.go`

```go
// Verifier.Verify post-fork, adding the attestation gate. All
// existing v1 checks remain; the new gate is the first thing the
// verifier runs after Proof.validateShape so we reject bad
// attestations before we spend CPU on the DAG walk.
func (v *Verifier) Verify(p Proof, …) error {
    if err := p.validateShape(); err != nil { return err }

    // v2 hard-fork gate (new). Before FORK_V2_HEIGHT falls
    // through to the v1 path.
    if p.Height >= FORK_V2_HEIGHT {
        if err := v.verifyAttestation(p); err != nil {
            return fmt.Errorf("v2 attestation: %w", err)
        }
    }

    // existing DAG walk, target check, etc…
    // …
    return nil
}

func (v *Verifier) verifyAttestation(p Proof) error {
    a := p.Attestation
    if a.Type == "" { return ErrAttestationRequired }

    switch a.Type {
    case "nvidia-cc-v1":
        return v.verifyNvidiaCC(p, a)
    case "nvidia-hmac-v1":
        return v.verifyNvidiaHMAC(p, a)
    default:
        return ErrAttestationTypeUnknown
    }
}
```

### 7.2 New packages

- `pkg/mining/attest/cc/` — CC cert-chain parsing + quote
  verification. ~300 Go lines on top of `crypto/x509`.
- `pkg/mining/attest/hmac/` — consumer-GPU HMAC verification and
  registry lookup. ~200 Go lines; reuses
  `pkg/monitoring/nvidia_hmac.go`'s payload canonicalisation.
- `pkg/mining/attest/registry/` — genesis-pinned trust store +
  on-chain delta application. ~250 Go lines. Backed by the same
  LMDB/Scylla store the chain uses.

### 7.3 Test vectors

Phase 2 ships: a fixture `testdata/fork_v2/` with:

- A valid `nvidia-cc-v1` bundle + chain + quote, extracted from
  a real H100.
- A valid `nvidia-hmac-v1` bundle for a registered RTX 4090.
- 15+ malformed bundles (missing fields, expired nonce, wrong
  signature, wrong GPU UUID, chain not rooted in pinned CA,
  etc.) — one golden-vector per rejection reason.

Each vector is a `.json` file pairable with an expected
`verifier_error` string. The verifier tests load every fixture
and assert the exact rejection reason. Verifier regressions get
caught loudly.

## 8. Activation mechanics — hard fork

### 8.1 Summary

Testnet reset at a coordinated wall-clock moment. Block 0 of v2
is a fresh genesis committing: the v2 protocol version, the NVIDIA
CC root material, the initial operator registry (empty), the new
deny-list (empty), and the `MIN_ENROLL_STAKE` parameter.

### 8.2 Genesis file extension

`cmd/genesis-ceremony/main.go::Bundle` gains:

```go
// New fields at the END of the struct so existing fixtures
// deserialize with zero-value for the new fields and the
// schema_version bump signals v2 was authored.
type Bundle struct {
    // … existing fields unchanged …
    SchemaVersion        int                     `json:"schema_version"`
    NvidiaCCRoots        []string                `json:"nvidia_cc_roots_pem"`      // PEM-encoded
    NvidiaCCMinFirmware  map[string]string       `json:"nvidia_cc_min_fw"`         // arch → min fw version
    OperatorRegistry     []RegistryEntry         `json:"operator_registry"`
    GPUDenyList          []string                `json:"gpu_deny_list"`
    MinEnrollStake       uint64                  `json:"min_enroll_stake_dust"`
    ForkV2Params         ForkV2Params            `json:"fork_v2_params"`
}
```

`SchemaVersion` goes from `1` (implicit today) to `2`.
`VerifyBundle` rejects a `SchemaVersion=2` bundle that has a
zero-length `NvidiaCCRoots`.

### 8.3 Pre-fork state disposition

All pre-fork state is discarded. The v2 genesis block is height
0. Pre-fork wallets are invalidated. This is consistent with the
owner's 2026-04-24 sign-off that the testnet has no real users.

### 8.4 Retirement of v1 binaries

At fork activation (same commit that ships Phase 3):

- `cmd/qsdmminer/` — removed.
- `cmd/qsdmminer-console/` — removed.
- `scripts/install-qsdmminer-console.*` — already removed in
  Phase 0.
- `QSDM/Dockerfile.miner-console` — already removed in Phase 0.
- `QSDM/Dockerfile.miner` — retained, renamed to
  `QSDM/Dockerfile.qsdm-miner-cuda`.
- New `cmd/qsdm-miner-cuda/` ships with the fork commit.

#### 8.4.1 Pre-fork: qsdmminer-console has opt-in v2 support

Until the fork commit lands, `cmd/qsdmminer-console` carries an
opt-in v2 code path so the validator + enrollment plumbing can
be exercised end-to-end on testnet without having to wait for
the CUDA-native miner binary. The v1 path remains the default;
operators who want to test a forked validator pass:

```
qsdmminer-console \
  --protocol=v2 \
  --node-id=alice-rtx4090-01 \
  --gpu-uuid=GPU-... \
  --gpu-name="NVIDIA GeForce RTX 4090" \
  --gpu-arch=ada \
  --hmac-key-path=/etc/qsdm/operator.key.hex
```

The loop between `mining.Solve` and `submitProof` then calls
`pkg/mining/v2client.FetchChallenge` +
`v2client.BuildHMACAttestation` (see `cmd/qsdmminer-console/v2.go`)
so the submitted proof carries a `nvidia-hmac-v1` attestation
bundle. This does **not** make the CPU miner economically
viable post-fork — the PoW re-tune of §4 still renders it
unprofitable — but it lets us exercise the non-PoW half of the
v2 stack (enrollment → challenge → bundle → verifier) without
GPU hardware in CI.

This opt-in path is retired in the same commit that removes
the binary.

## 9. Attacker model

### 9.1 In-scope threats

1. **CPU-only miner.** Rejected by the attestation gate; even if
   a rogue validator accepts it, the proof takes 250x longer to
   compute on a CPU than on an NVIDIA GPU (§4.4). Never
   profitable.
2. **AMD / Intel GPU with forged `nvidia-smi` output.** The
   verifier does not trust `nvidia-smi` output directly; it
   trusts the HMAC over it. The HMAC key binds to a registered
   `(node_id, gpu_uuid)`. To mine from an AMD GPU the attacker
   needs a real registered NVIDIA GPU's HMAC key. If they have
   that, the registered operator is running two miners from one
   key — detectable as duplicate proof-id collisions if both
   miners try to solve the same challenge.
3. **Nonce replay.** Prevented by `FRESHNESS_WINDOW` and the
   validator's nonce ring buffer (§6).
4. **Stale proof.** Same mitigation.
5. **Rogue validator accepting unattested proofs.** Such
   validator is in consensus minority if >50% honest, so its
   proposed blocks lose in the PoE+BFT commit round. If it is
   the honest majority, the chain has a bigger problem than the
   attestation rule.
6. **Sybil enrollment.** `MIN_ENROLL_STAKE` puts a Cell cost on
   creating many fake `node_id`s.

### 9.2 Out-of-scope threats

1. **NVIDIA CA compromise.** If NVIDIA's attestation root is
   compromised, every NVIDIA-CC-based chain world-wide has a
   problem. Mitigation is governance-driven root rotation, same
   as any pinned-root system.
2. **Operator leaks their HMAC key publicly.** The key is
   revocable on-chain. Before revocation clears, the attacker
   mines from the operator's identity; the operator loses
   reputation and staked Cell. The attacker can't steal rewards
   because the `miner_addr` in the proof is the reward
   destination, and the `miner_addr` is HMAC'd over, so the
   attacker can only redirect rewards to the original operator
   unless they fake the HMAC (which requires the key, back to
   square one).
3. **Side-channel attack on an operator's TPM / key storage.**
   Out of scope for consensus; same issue as every PoS chain's
   validator key.

## 10. Implementation phase map

| Phase | Deliverable | Blocks on | Est. |
|---|---|---|---|
| **Phase 0** | Retire CPU-miner onboarding UX. Deprecation banner on v1 binaries. | — | ✅ done (19e756a) |
| **Phase 1** | **This doc.** | — | Today |
| **Phase 1 sign-off** | Owner ratifies §5.6 + this whole spec. | Phase 1 doc. | Owner |
| **Phase 2** | `pkg/mining/attest/*` implementation, verifier gate behind `FORK_V2_HEIGHT`, test vectors. No activation height set. | §1.6 sign-off | 2-3 days |
| **Phase 3** | `cmd/qsdm-miner-cuda`, Tensor-Core CUDA kernel, delete `cmd/qsdmminer*`. | Phase 2 | 2-3 days |
| **Phase 4** | Genesis ceremony extension, testnet reset, FORK_V2_HEIGHT=0 activation. | Phase 3 | 1 day |
| **Phase 5** | Docs / landing page rewrite. CHANGELOG final. | Phase 4 | 1 day |

Total: ~8-10 days wall-clock after this spec is ratified.

## 11. OPEN_QUESTION summary

**OPEN_QUESTION_1** (§5.6): Accept the tiered trust-anchor
recommendation (NVIDIA-CC-pinned + Registered-operator for
consumer GPUs)?

**OPEN_QUESTION_2** (§5.4): Initial `MIN_ENROLL_STAKE` value.
Proposal: `10 CELL` at v2 genesis, with a governance-accessible
parameter for future tuning.

**OPEN_QUESTION_3** (§6.2): `FRESHNESS_WINDOW` value. Proposal:
`60 s`. Tighter values risk false-rejecting honest miners on
slow links; looser values widen the replay window.

Phase 2 implementation is blocked until these three are resolved.
A ratification transcript should be committed as
`QSDM/docs/docs/MINING_PROTOCOL_V2_RATIFICATION.md` before any
Phase 2 code lands.
