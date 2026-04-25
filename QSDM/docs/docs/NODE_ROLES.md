# Node roles — Validator vs. Miner

QSDM has exactly **two** node roles. This is a deliberate architectural
decision ratified in the Major Update (see
[`REBRAND_NOTES.md`](./REBRAND_NOTES.md)): one role owns consensus, the
other owns coin emission, and they never overlap in a single process.

| Role | Configuration | Hardware | Responsibilities | Rewards |
|---|---|---|---|---|
| **Validator** | `node.role = "validator"`, `node.mining_enabled = false` | CPU-only (VPS-class) | BFT + Proof-of-Entanglement consensus, transaction ordering, block finality, serving the public JSON-RPC / REST API | Transaction fees (denominated in `dust`) |
| **Miner** | `node.role = "miner"`, `node.mining_enabled = true` | CUDA-capable NVIDIA GPU | Producing valid PoW proofs tied to the current mesh3D epoch; submitting them to validators for inclusion in blocks | Newly-minted Cell (the 90% mining-emission pool; see [`CELL_TOKENOMICS.md`](./CELL_TOKENOMICS.md)) |

There is **no combined "full node" mode**. An operator who wants to do both
things runs two separate processes (different binaries, different config
files, different machines if the validator is on a VPS).

---

## 1. Why the split matters

1. **Safety surface.** Validators hold the state machine. Any CGO linkage
   beyond the minimum required for post-quantum crypto (liboqs for
   ML-DSA-87) increases their attack surface. The validator-only build
   (`-tags validator_only`, shipped as `qsdm/validator:latest`) removes the
   CUDA mining path at link time — a validator binary literally cannot be
   made to run mining code even if misconfigured.
2. **Economic clarity.** Validators earn fees. Miners earn emission. A
   single node doing both would blur that split and invite rent-seeking
   behaviour (e.g. miner-validators front-running their own mining rewards
   into favourable block positions). Keeping them separate preserves the
   fair-launch story documented in [`CELL_TOKENOMICS.md`](./CELL_TOKENOMICS.md).
3. **Hardware cost alignment.** VPS validator hardware is cheap (4 vCPU,
   8 GB RAM, 100 GB NVMe). Mining hardware is expensive (NVIDIA GPUs,
   power, cooling). Bundling them into one node forces validator operators
   to pay GPU costs they do not need, and forces miners to pay VPS-class
   uptime guarantees they cannot profitably offer from a home connection.
4. **Trust signal transparency.** The public trust widget on
   `qsdm.tech/trust` (Phase 5 deliverable) reports NVIDIA NGC attestation
   coverage across the network. By keeping NGC attestation an **optional**
   property of the *miner* path only, we avoid the appearance of
   NVIDIA-gated consensus, which was the single largest misreading of the
   project before the Major Update.

---

## 2. Enforcement surfaces

The split is enforced at four layers. A misconfigured node is rejected
before it can advertise itself on the network:

| Layer | Enforcement |
|---|---|
| Configuration | `pkg/config.Validate` rejects `role=validator` with `mining_enabled=true` and vice versa. |
| Build profile | `-tags validator_only` removes `pkg/mining/cuda` from the binary and forces `MustMatchRole` to refuse `role=miner`. |
| Startup guard | `cmd/qsdm/main.go` calls `roleguard.MustMatchRole` BEFORE opening any listeners. |
| Deployment | `deploy/kubernetes/validator-statefulset.yaml` pins validators to `node-class=cpu`; `deploy/kubernetes/miner-daemonset.yaml` pins miners to GPU nodes with NVIDIA tolerations. |

See [`SCYLLA_CAPACITY.md §0`](./SCYLLA_CAPACITY.md) for the negative-space
version of this rule: *validator nodes must not be provisioned on GPU-enabled
hardware*, regardless of whether the operator intends to mine or not.

---

## 3. Minimum viable deployment

### 3.1 Single validator + multiple home miners (recommended bootstrap)

This is the recommended topology for Phase 4 testnets and for operators
launching a sovereign mainnet instance:

```
┌──────────────────────────────────────────┐
│  Validator (VPS, qsdm/validator:latest) │
│  ─ BFT + PoE consensus                  │
│  ─ Public REST/JSON-RPC on :8080        │
│  ─ libp2p peer on :4001                 │
└──────────────────┬───────────────────────┘
                   ▲  (HTTPS over Internet)
                   │
       ┌───────────┼───────────┐
       │           │           │
   ┌───┴───┐   ┌───┴───┐   ┌───┴───┐
   │ Miner │   │ Miner │   │ Miner │
   │ (home │   │ (home │   │ (home │
   │  GPU) │   │  GPU) │   │  GPU) │
   └───────┘   └───────┘   └───────┘
```

Validators run 24/7 on a VPS with a static IP. Miners join and leave at
will — the network tolerates miner churn because consensus does not depend
on any specific miner being online.

### 3.2 Multi-validator (production)

For production, run a minimum of **4 geographically-distributed
validators** (this is the BFT safety floor; 3f+1 with f=1). Validators form
their own libp2p mesh, exchange consensus messages, and do not need to be
peer with miners at the libp2p layer — miners talk to validators only via
HTTP (submit a proof, fetch the current epoch). This keeps validator-to-
validator traffic clean of mining chatter.

---

## 4. Hardware sizing

### 4.1 Validator

| Tier | CPU | RAM | Disk | Network | Notes |
|---|---|---|---|---|---|
| Minimum | 2 vCPU (x86_64, AVX2) | 4 GB | 50 GB NVMe | 10 Mbps symmetric, static IP | Testnets only |
| Recommended | 4 vCPU | 8 GB | 100 GB NVMe | 100 Mbps symmetric, static IPv4 + IPv6 | Public mainnet validator |
| High-TPS | 8 vCPU | 16 GB | 500 GB NVMe + Scylla cluster | 1 Gbps | Exchange-adjacent validator, > 2 k TPS |

GPU hardware provides **zero benefit** to a validator. Do not provision it.

### 4.2 Miner

| Tier | GPU | VRAM | CPU | RAM | Notes |
|---|---|---|---|---|---|
| Entry | NVIDIA RTX 3060 / 4060 | 8 GB+ | 4-core | 16 GB | Home / hobby |
| Mid | NVIDIA RTX 4070 / 4080 | 12 GB+ | 8-core | 32 GB | Dedicated rig |
| Pro | NVIDIA RTX 4090 / 5090 / data-center Hopper | 24 GB+ | 16-core | 64 GB | Farms / pools |

The mining algorithm (Phase 4 design target: mesh3D-tied PoW, candidate C
in `Major Update.md §5.2`) uses a 2–4 GB per-epoch dataset; anything below
8 GB of VRAM will struggle as the dataset grows.

---

## 5. Operator quickstart links

- **Validator quickstart:** [`VALIDATOR_QUICKSTART.md`](./VALIDATOR_QUICKSTART.md)
- **Miner quickstart (CPU reference miner; Phase 4 deliverable):** `MINER_QUICKSTART.md`
- **Deployment manifests:** [`../../deploy/kubernetes/README.md`](../../deploy/kubernetes/README.md)
- **Rebrand + deprecation window:** [`REBRAND_NOTES.md`](./REBRAND_NOTES.md)
- **Tokenomics:** [`CELL_TOKENOMICS.md`](./CELL_TOKENOMICS.md)
