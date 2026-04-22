# QSDM Mining Protocol — MINING_PROTOCOL.md

> **Status:** Normative. Ratified per Major Update §5 and §11 Phase 0 recommendation. Cryptographic parameters and hash/KDF choices remain **audit-gated** — mainnet activation depends on the external review required by Major Update §11 Phase 4/6.
>
> **Audience:** Protocol implementers, auditors, validator operators, miner client authors.
>
> **Scope:** This document specifies the QSDM mining sub-protocol (Cell emission) on top of the PoE+BFT consensus that validator nodes run. Mining **does not affect consensus** — see §1.2 and §9.

---

## 0. Terminology

| Term | Meaning |
|---|---|
| **Validator** | CPU-only QSDM node running PoE+BFT consensus and verifying mining proofs. Must not be on GPU hardware. See `NODE_ROLES.md`. |
| **Miner** | GPU-equipped QSDM node whose sole role is submitting mining proofs. Miners are **not** consensus participants. |
| **Proof** | A miner's candidate solution: the tuple `(epoch, height, nonce, batch_root, attestation, miner_addr)` that a validator can accept, reject, or orphan. |
| **Epoch** | A fixed-duration reward period. Two meanings exist in this document and MUST be disambiguated by context: (i) the **emission epoch** (4 wall-clock years — governs halvings, see `CELL_TOKENOMICS.md`) and (ii) the **mining epoch** (≈ 1 week of wall-clock time — governs the rotating work-set; §3). Where ambiguity is possible the spec uses `emission_epoch` or `mining_epoch`. |
| **Work-set** | The per-mining-epoch data structure that a miner must have fully loaded to compute proofs. In the mesh3D-tied design (§2.1) this is a rotating set of pending parent-cell batches that the mesh needs validated. |
| **Target** | The 256-bit upper bound a proof hash must fall below to be valid. Lower target = higher difficulty. |
| **dust** | Smallest indivisible unit of Cell. `1 CELL = 10^8 dust`. |

## 1. Design goals & non-goals

### 1.1 Goals (normative)

1. **Fair launch emission.** All Cell enters circulation via mining rewards, except the fixed 10% treasury allocation documented in `CELL_TOKENOMICS.md`. 0% of Cell is pre-mined to validators, founders, or insiders.
2. **GPU-favored, NVIDIA-favored, NVIDIA-not-required.** CUDA-tuned kernels are the expected production miner. Portable OpenCL / Vulkan / CPU fallbacks MUST remain compilable and correct — they only lose economically.
3. **ASIC resistance (soft).** The proof function is memory-hard (§5) and the work-set mutates on a cadence short enough (§3) that ASIC fabrication has negative ROI relative to the next mutation.
4. **Cheap verification.** A validator on a modest VPS MUST verify any single proof in < 100 ms (CPU-only, single core) and batch-verify any block of ≤ 1000 proofs in < 2 s.
5. **Consensus-safe.** Mining halts, forks, or attacks MUST NOT stop the PoE+BFT chain from producing blocks. The mining protocol is additive.
6. **Deterministic.** Two independent validator implementations MUST agree bit-for-bit on whether a proof is valid and on what reward it earns, given the same block header and emission schedule.

### 1.2 Non-goals

- Mining does **not** elect validators, sign blocks, or participate in view changes.
- The mining protocol does **not** replace `pkg/mesh3d` validation. It **augments** it: see §2.1.
- This document does **not** mandate a specific GPU vendor. Economic favoritism via tuning is permitted; technical exclusion is not. (Major Update §5.4 "Stance 1".)

## 2. Algorithm choice — Candidate C (mesh3D-tied useful PoW)

Per the Major Update §5.2 decision matrix, QSDM adopts **Candidate C** as the design target: miners' work doubles as mesh3D parent-cell batch validation, so the GPU cycles benefit the chain and are not a pure lottery. Candidate A (KawPow-class) is retained as a **fallback** the core team may activate at mainnet only if Candidate C has not cleared the Major Update §11 Phase 6 external audit in the launch window.

This document specifies Candidate C in full. If the fallback is triggered, a separate `MINING_PROTOCOL_KAWPOW.md` governs; it is not required for the reference implementation.

### 2.1 What "useful" means in QSDM

`pkg/mesh3d` produces parent cells that carry transaction data in a 3–5-parent DAG per Major Update §5.7. Each parent cell requires validation against the Dilithium signatures on the transactions it ingests and against the mesh3D structural rules. This validation is CPU-bounded but **embarrassingly parallel** across cells — a textbook GPU workload.

QSDM miners are the network's **parallel batch-validators** for parent cells that would otherwise queue behind validator CPUs. Their CUDA kernel's output is threefold:

1. A `batch_root` Merkle root over the validated parent cells (§4.3).
2. A `nonce` such that the proof hash (§5) is below the current target.
3. An `attestation` asserting the miner ran the kernel honestly (§6).

Validators on the PoE+BFT side then re-verify **one random cell out of the batch** (§7.2) as a probabilistic check, accept the proof, and credit the miner.

### 2.2 Why this is not a consensus change

- The mesh3D batches a miner validates are pre-existing, pre-consumed mesh work the chain needs anyway. Miners do not *choose* which transactions to include — validators do.
- Validators already accept parent cells when their own CPU clears them. Miner-submitted batches are an **accelerator**, not a gate. A validator that ignores every miner proof still produces a canonical chain.
- Cell rewards are a post-block side-effect of the emission calculator (`pkg/chain/emission`). They are issued to miner addresses inside coinbase-style transactions the validator-set appends to the next block; consensus on those appendages uses the same BFT vote the block header already rides on.

## 3. Mining epochs & work-set rotation

### 3.1 Mining-epoch cadence

A **mining epoch** is a contiguous range of block heights `[E.start_height, E.end_height]` during which the same work-set is valid. The reference parameters are:

| Parameter | Symbol | Value | Rationale |
|---|---|---|---|
| Blocks per mining epoch | `M` | `60_480` (≈ 7 days @ 10 s blocks) | Short enough that ASIC fab cycles cannot amortise, long enough that home miners need not refresh the DAG daily. |
| Rotation boundary | — | Any `height` where `height mod M == 0` | Deterministic, gossip-free. |
| Target block time | `T` | 10 s (default) | Matches `pkg/chain/emission.DefaultTargetBlockTimeSeconds`. |

The block height at which epoch `e` begins is `e * M`. Implementations MUST fetch the epoch from the chain state rather than computing it from wall-clock time.

### 3.2 Work-set definition

For mining epoch `e`, the work-set `WS_e` is:

```
WS_e := ordered list of parent-cell batches
        Bᵢ = { pc_1, pc_2, …, pc_k }   where 3 ≤ k ≤ 5
        such that:
          1. Each pc_j is a parent cell that reached `pending` state
             in some block h with (e-1)*M ≤ h < e*M.
          2. Bᵢ is sorted by (pc_1.ID asc, pc_2.ID asc, …).
          3. The batches Bᵢ themselves are sorted by sha256(concat(pc_j.ID)) asc.
```

Every validator and every miner computes `WS_e` deterministically from the chain state at height `(e-1)*M`. There is no gossip step and no canonical "work-set server". A miner that falls behind simply re-derives `WS_e` by replaying the previous mining epoch's blocks.

### 3.3 DAG (memory-hard dataset)

To keep the algorithm memory-hard and to frustrate cell-by-cell lookup attacks, each mining epoch also fixes a DAG `D_e` of 2 GiB ± 5% ("KawPow-class" sizing per Major Update §5.2). The DAG is:

```
D_e[i] := sha3_256( D_e[i-1] || LE32(i) )          for i ∈ [1, N-1]
D_e[0] := sha3_256( "QSDM/mesh3d-pow/v1" || LE64(e) || WS_e.root )
where N = 2^26 entries of 32 bytes each (= 2 GiB exactly).
```

`WS_e.root` is the Merkle root of `WS_e` (domain-separated; §4.3). The KDF is SHA3-256 (FIPS 202). Use of BLAKE3 is **non-conformant** for v1.

## 4. Proof shape

### 4.1 Wire format (canonical JSON)

A submitted proof is the following JSON object (all integer fields are unsigned; all byte fields are lowercase hex without `0x` prefix):

```json
{
  "version":       1,
  "epoch":         "<uint64 mining epoch>",
  "height":        "<uint64 target block height>",
  "header_hash":   "<32-byte hex of the canonical block-header pre-image>",
  "miner_addr":    "<QSDM address string of the reward recipient>",
  "batch_root":    "<32-byte hex Merkle root over the batches the miner validated>",
  "batch_count":   "<uint32 number of batches in the validated set>",
  "nonce":         "<16-byte hex>",
  "mix_digest":    "<32-byte hex intermediate product of the DAG walk>",
  "attestation":   { "<see §6>": "…" }
}
```

Canonical serialization for hashing is: field-order exactly as above, no whitespace, all hex lowercase, integers rendered as decimal strings (to avoid JSON-number precision loss on 64-bit height). Validators MUST reject a proof whose bytes do not round-trip through this canonical form.

### 4.2 Proof identifier

```
proof_id := sha256( canonical_json(proof_without("attestation")) )
```

The attestation is excluded from `proof_id` so a single solved share can be re-submitted with different attestation evidence (e.g. if the miner's NGC bundle rotates mid-flight). The validator-side dedup set is keyed on `proof_id`.

### 4.3 Batch Merkle tree

Each batch `Bᵢ` in `WS_e` has a `batch_hash`:

```
batch_hash(Bᵢ) := sha256( 0x00 || pc_1.ID || pc_1.content_hash
                              || pc_2.ID || pc_2.content_hash
                              || …
                              || pc_k.ID || pc_k.content_hash )
```

The miner validates some subset `S ⊆ WS_e` of size `batch_count`. The reported `batch_root` is the binary Merkle root (SHA-256, RFC 6962 style with `0x00` leaf / `0x01` internal-node prefixes) over `{ batch_hash(Bᵢ) : Bᵢ ∈ S }` sorted by index.

## 5. Proof-of-Work function

### 5.1 Hash

Let `H(x) := sha3_256(x)` (FIPS 202 Keccak with standard padding).

### 5.2 Target

Each block height carries a 256-bit target `target(height)` derived from the current chain-level difficulty `difficulty(height)`:

```
target(height) := floor( 2^256 / difficulty(height) ) - 1
```

A proof is valid (with respect to work only — §7 defines the full acceptance test) iff:

```
H( header_hash || nonce || batch_root || mix_digest ) < target(height)
```

All four inputs concatenate as fixed-width byte strings (32 || 16 || 32 || 32 = 112 bytes).

### 5.3 DAG walk (mix_digest derivation)

The miner MUST produce `mix_digest` from a 64-step walk over `D_e`:

```
seed := sha3_256( header_hash || nonce )
mix  := seed                                # 32 bytes

for s in 0..64:
    idx  := uint32(BE(mix[0..4])) mod N
    mix  := sha3_256( mix || D_e[idx] )

mix_digest := mix
```

This is the verification critical path: a validator that lacks `D_e` in memory needs only 64 deterministic lookups (64 × 32 B = 2 KiB of DAG reads) plus 65 SHA3 invocations to recompute `mix_digest` and check the target inequality. That is the "< 100 ms" promise of §1.1(4).

### 5.4 Rationale for the parameter set

- 2 GiB DAG fits on consumer GPUs from RTX 20-series upward; excludes most memory-starved ASICs available in 2026.
- 64 lookups per attempt is enough to amortise DRAM-access noise on GPUs but negligible on validator CPUs (the 2 KiB of reads is cache-resident after the first proof in a block).
- SHA3-256 is chosen over BLAKE3/Keccak-variant combinators to share the transaction-signature hash family and reuse existing FIPS-validated primitives in `pkg/crypto`.

## 6. Attestation block (optional, §8.5-compatible)

The `attestation` field is **optional** at the protocol layer. When present it is:

```json
{
  "type":     "ngc-v1",
  "bundle":   "<base64 of the raw NGC proof bundle — see pkg/monitoring/ngc_proofs>",
  "gpu_arch": "<string, e.g. 'ada-lovelace' | 'hopper' | 'blackwell'>",
  "claimed_hashrate_hps": "<uint64 miner-declared hashrate, for anti-claim guardrails only>"
}
```

Per Major Update §1 and §5.4, **NGC attestation is a transparency signal, not a consensus rule.** Validators MUST NOT reject an otherwise-valid proof solely because its attestation is missing, stale, or unverifiable; they MUST, however:

1. Mark the proof `attested=false` in the NGC ring buffer surfaced to `/api/v1/trust/…` (`handlers_trust.go`, Phase 5.1).
2. Propagate the attestation bundle unmodified to the mesh so downstream peers can independently verify it via the NGC public keys in `pkg/monitoring/ngc_proofs`.

Miners that **do** attest are surfaced on the landing-page "Attested validators / miners" widget (Major Update §8.5) and get discovery preference in `/api/v1/network/peers` sort order. This is economic, not protocol, favoritism.

## 7. Validator acceptance algorithm

A validator receiving a proof `P` for block height `h` MUST execute, in order, the following checks. Any failed step causes rejection with the listed reason code; passing all steps causes the validator to credit `miner_reward(h)` dust (§8) to `P.miner_addr` when the block is finalised.

| # | Check | Reject reason on failure |
|---|---|---|
| 1 | `P.version == 1` | `bad-version` |
| 2 | `P.height == h` | `stale-height` |
| 3 | `P.epoch == h / M` | `wrong-epoch` |
| 4 | Canonical-JSON round-trip of `P` yields identical bytes | `non-canonical` |
| 5 | `P.header_hash` equals the hash of the known block-header pre-image at `h` | `header-mismatch` |
| 6 | `proof_id(P)` not already seen in the last `M` blocks | `duplicate` |
| 7 | `P.miner_addr` passes `pkg/crypto.ValidateAddress` | `bad-addr` |
| 8 | `batch_count ≤ ceil(|WS_e| / 16)` and `≥ 1` | `batch-size` |
| 9 | `P.batch_root` matches one of the `2^k` valid Merkle roots derivable from `WS_e` at size `batch_count` | `batch-root` |
| 10 | PoW check (§5.2) holds with recomputed `mix_digest` | `work` |
| 11 | **Probabilistic spot-check:** pick one random leaf `j` from `P.batch_root`, CPU-verify the underlying parent-cell batch via `pkg/mesh3d.Mesh3DValidator.ValidateBatch(Bⱼ)`. If invalid: reject **and slash** (§8.3). | `batch-fraud` |

### 7.1 Why step 9 is tractable

`WS_e` is deterministic (§3.2). Given `batch_count = c`, the number of *lexicographically-sorted-then-Merkleised* size-`c` subsets a validator must consider is exactly one (the first `c` batches). Miners therefore implicitly agree on the subset they're claiming, and step 9 is a single Merkle-root computation, not a set search. This is a deliberate design choice: it trades a miner's freedom to cherry-pick batches (no freedom: they always take the first `c`) for O(1) validator verification.

### 7.2 Spot-check cadence

Step 11 runs with probability `p = min(1, 10 / batch_count)` per proof. Over an attacker's 1000-proof window this yields a detection probability of `1 - (1-p)^k ≥ 0.9999` against an attacker faking > 1% of leaves. The spot-check cost is one `ValidateBatch` call ≈ 1–3 ms of CPU.

## 8. Rewards & difficulty

### 8.1 Per-block reward

```
miner_reward(h) := pkg/chain/emission.EmissionSchedule{…}.BlockRewardDust(h)
```

Precisely the same function used by `/api/v1/status`. The reward is paid in a coinbase-style transaction the validator-set appends atomically with block finalisation. If zero valid proofs arrive for block `h`, the reward for `h` is **burned** — it does not roll over. (This simplifies bookkeeping and caps the sliding unclaimed-emission pool at one block's worth.)

### 8.2 Difficulty adjustment (normative)

Let `D_h` be the difficulty active at height `h`. Retargeting occurs every `R = 1008` blocks (≈ 2.8 hours @ 10 s). At a retarget block `h`:

```
actual_time  := block_timestamp(h) - block_timestamp(h - R)
target_time  := R * T         # T = 10 s
D_{h+1}      := D_h * target_time / actual_time     # integer division, with
                                                    # ±4× clamp per retarget

# clamp:
#   D_{h+1} := max(D_h / 4, min(D_h * 4, D_{h+1}))
#   D_{h+1} := max(D_min, D_{h+1})
```

`D_min = 2^16` (fixed across all mining epochs; ensures even on the first-ever retarget a sane lower bound exists). All multiplications are done in `math/big.Int` to avoid overflow; the result is truncated back to a 256-bit big-endian field stored alongside the block header.

### 8.3 Slashing on fraud detection

If step 11 of §7 rejects a proof:

1. The miner's address is marked `quarantined` for `Q = 10_080` blocks (≈ 1 day).
2. Any further proof from that address during quarantine is rejected with `quarantined`.
3. No staking bond is burned — miners have no staking bond. The slash is purely a reputation / rate-limit primitive to prevent a single bad actor from flooding validators with fraud-proofs.

## 9. Interaction with PoE+BFT consensus

- A block is **final** once the validator-set's BFT vote completes, **regardless of how many mining proofs have arrived.** Unacknowledged proofs do not delay finality.
- A mining proof for finalised block `h` is accepted by a validator until block `h + G` (grace window, `G = 6`). Proofs arriving after `h + G` are rejected (`too-late`), and the block's coinbase simply goes without an award for slot-`h` if no proof won inside the window.
- In a BFT view-change that re-orgs height `h` to `h'`, all proofs keyed to the orphaned `header_hash` at `h` become permanently invalid; miners MUST re-solve against `h'`. This is identical to how any PoW-augmented chain handles re-orgs.

## 10. CPU reference miner

`cmd/qsdmminer/` is the reference implementation (Major Update §5.6). It is CPU-only, single-threaded by default, and intentionally slow — its purpose is protocol compliance testing, not competitive mining. The CUDA production miner ships under `pkg/mining/cuda/` (build-tagged `cuda`) after the external audit of Major Update §11 Phase 6.

Reference-miner operational requirements:

- MUST produce a valid proof for a test epoch of ≤ 100 batches in ≤ 60 s on a laptop CPU. This is the Phase 4.5 acceptance gate.
- MUST use only `pkg/mining`, `pkg/chain/emission`, `pkg/crypto` (for address validation), and the Go stdlib. No CGO.
- MUST compile on Windows amd64 and Linux amd64 without build tags.

## 11. Versioning & upgrade path

This document pins protocol version **v1** (`proof.version == 1`). A future v2 (e.g. to swap SHA3-256 for SHA3-384 if FIPS guidance evolves) MUST:

1. Be gated by a BFT vote from the validator set.
2. Activate at a pre-announced `height_v2_activate`.
3. Accept both v1 and v2 proofs for a transition window `W = 7 * M` (one mining epoch per RFC 0 precedent in the Cosmos Hub upgrade manual — cited only as a reference, not a binding dependency).

## 12. Conformance checklist for implementers

A QSDM mining implementation is conformant iff all the following are true:

- [ ] All field widths, encodings, and canonicalisation rules in §4 are implemented byte-exact.
- [ ] `pkg/mining.EpochForHeight(h, M)` matches `pkg/chain/emission`'s epoch accounting for coinbase attribution.
- [ ] Validator-side verification of any single proof runs in < 100 ms on an AWS t3.medium (§1.1(4)).
- [ ] The reference miner produces one valid proof for a scripted 100-batch test epoch in < 60 s on a laptop CPU (Phase 4.5 gate).
- [ ] Difficulty clamp enforces the ±4× bound per retarget (§8.2).
- [ ] Dedup set retains proof IDs for at least `M` blocks (§7 step 6).
- [ ] Rejection reasons use the strings in §7 verbatim so dashboards can group them.

## 13. Open questions deferred to audit (Phase 6)

These items are **out of scope** for Phase 4 implementation and must be answered by the external audit before mainnet Cell emission is unlocked:

1. Is SHA3-256 the correct choice given hardware SHA3 extensions on Ampere+? Could Keccak-f[1600] directly save 20% on kernel time without losing standards coverage?
2. Is 2 GiB DAG still ASIC-defeating in 2026? Evidence from the Ravencoin KawPow operational record suggests yes, but the audit should confirm against the latest Bitmain disclosures.
3. Is the "first `c` batches always" rule of §7.1 exploitable by a validator selectively feeding `WS_e` orderings? The current gossip-free derivation claims not, but this deserves an explicit non-malleability proof.
4. Does the spot-check probability `p` in §7.2 survive a well-funded adversary pre-computing partial Merkle trees? Audit must consider adaptive attackers across thousands of proofs.
5. Is the `Q = 10_080` block quarantine (§8.3) sufficient against a Sybil attacker rotating addresses?

Answers feed back into §3.1 (parameter `M`), §5 (hash family), §7.2 (probability `p`), and §8.3 (quarantine length).

---

## Appendix A. Cross-reference map

| Major Update section | This document |
|---|---|
| §1 (rebrand / Cell) | §0, §8.1 |
| §5.1 goals | §1.1 |
| §5.2 algorithm choice | §2 |
| §5.3 proof-and-reward flow | §4, §5, §7, §8 |
| §5.4 NVIDIA-favored not -exclusive | §1.1(2), §6 |
| §5.6 new packages | §10, and Phase 4.2/4.3 implementation todos |
| §8.5 attestation | §6 |
| §11 Phase 4 testnet | §11 (upgrade path), §13 (audit gates) |

## Appendix B. Illustrative numbers

Using the emission-schedule defaults (`target_block_time = 10 s`, `M = 60480` blocks/mining-epoch):

| Quantity | Value |
|---|---|
| Blocks per 24 h | 8 640 |
| Mining epochs per emission epoch (4 yr) | ≈ 208.7 |
| First-emission-epoch block reward | 3.56490987 CELL/block (per `pkg/chain/emission` tests) |
| Cell issued per day in epoch 0 | ≈ 30 800 CELL |
| Cell issued per mining epoch (≈ 7 days) in epoch 0 | ≈ 215 700 CELL |

These figures are illustrative only — the binding values come from `pkg/chain/emission` and `CELL_TOKENOMICS.md`.
