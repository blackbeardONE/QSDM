# QSDM Operator Guide — end-to-end wiki

> **Audience.** You want to run something on the QSDM network — a
> validator, a miner, or both — and you want one page that stitches
> together all of the existing reference docs in the order you'll
> actually meet them.
>
> **Scope.** Testnet today, mainnet when the
> [`ROADMAP.md`](./ROADMAP.md) Phase 6 external audit closes. This
> guide never promises mainnet earnings; both difficulty and the block
> reward are moving targets documented in
> [`CELL_TOKENOMICS.md`](./CELL_TOKENOMICS.md).

---

## 0. The 60-second map

Before picking hardware, make sure you understand the shape of the
network. The single biggest source of confusion from new operators is
the assumption that "connecting to QSDM" means "connecting to our
VPS." It doesn't.

### 0.1 QSDM is a peer-to-peer mesh

QSDM runs on **libp2p + GossipSub**. Every validator is a full peer;
there is no central server that holds the chain. The ledger is
replicated byte-for-byte across every validator that has finished its
initial sync.

```
                 ┌─────────────────────────────────────────────┐
                 │     The QSDM validator mesh (libp2p)        │
                 │                                             │
  [Your new     ───►  [Seed: api.qsdm.tech]  ◄──►  [Peer B]     │
   validator]        (bootstrap only — NOT                      │
                      a central server)                         │
                          ▲        ▲                            │
                          │        │                            │
                        [Peer C] [Peer D]                       │
                 │                                             │
                 └─────────────────────────────────────────────┘
                                  ▲
                                  │  HTTPS /api/v1/mining/work
                  ┌───────────────┼───────────────┐
                  │               │               │
             [Your miner]   [Another miner]  [Pool op.]
             (home rig, GPU — talks to ONE validator over HTTPS)
```

### 0.2 What about `api.qsdm.tech`?

`https://api.qsdm.tech/` is the reference deployment we operate. It
plays two roles right now:

1. **Canonical bootstrap peer.** A new validator uses its libp2p
   multiaddr to discover the mesh the first time it starts. After that
   handshake, your validator gossips with every other validator
   directly. Bootstrap ≠ sync.
2. **Public REST endpoint.** Miners, wallets, and the dashboard call
   `/api/v1/*` on this hostname. Anyone can stand up their own
   validator, publish a competing REST hostname, and point traffic at
   it — the protocol has no preference.

If `api.qsdm.tech` went offline tomorrow:

- Validators already in the mesh would keep finalising blocks.
- New joiners would just need a different peer's multiaddr in
  `bootstrap_peers`.
- Miners pointing at `api.qsdm.tech` would switch to any other
  validator they trust (`--validator=https://your-node.example/`).

So the honest answer to *"do I have to connect and sync to your VPS?"*
is: **no, you connect and sync to *the mesh*.** Our VPS happens to be
one of the peers in that mesh today, and it is the one we recommend
for Phase 4 testnet bootstrap because it is the genesis validator. Once
you publish your own validator, it is peer-equal to ours.

### 0.3 Where miners fit

Miners do **not** join the libp2p mesh. They speak plain HTTPS to one
validator's `/api/v1/mining/work` endpoint
([`MINING_PROTOCOL.md`](./MINING_PROTOCOL.md)). Choose a validator the
same way you'd choose an upstream in any mining pool:

- Pick a validator with a reliable API endpoint (`/api/v1/status`
  returns `200` and `chain_tip` is advancing).
- Pick a validator whose operator you trust not to withhold your
  ACCEPTED proofs or front-run them.
- Geographic proximity helps with the 6-block grace window
  ([`MINING_PROTOCOL.md §9`](./MINING_PROTOCOL.md)).

---

## 1. Pick your role

QSDM has exactly two node roles. They never overlap in a single
process — see [`NODE_ROLES.md`](./NODE_ROLES.md) for the full
rationale.

| You want to… | Run this | Hardware | Reward |
|---|---|---|---|
| Secure the chain, earn fees | **Validator** | CPU-only VPS | Transaction fees (denominated in `dust`) |
| Mint new Cell supply | **Miner** | NVIDIA GPU (see §3) | Newly-issued Cell per block |
| Do both | **Two separate machines** running two separate binaries | 1 × CPU VPS + 1 × GPU rig | Both |

There is no "full node" that does both in one process. Config
validation and a startup guard reject the combination — this is
intentional, see [`NODE_ROLES.md §2`](./NODE_ROLES.md).

---

## 2. Validator path — CPU-only

### 2.1 Hardware

- **4–8 vCPU**, 8–16 GB RAM, 200 GB NVMe, 1 Gbps symmetric.
- Ubuntu 24.04 LTS is the reference OS. Windows 10+ works; macOS is in
  development.
- **No GPU.** Validator builds (`qsdm/validator:latest`, or `-tags
  validator_only` from source) do not link CUDA — a validator binary
  literally cannot run the mining path.
- Any mainstream VPS provider works (DigitalOcean, Hetzner, OVH, AWS
  Lightsail, etc.). Budget: roughly US$20–40/month.

### 2.2 Install & bootstrap

Follow [`VALIDATOR_QUICKSTART.md`](./VALIDATOR_QUICKSTART.md)
end-to-end. The critical bits:

1. Drop a `config.toml` with your desired `node.address`,
   `api.port = 8080`, `network.port = 4001`.
2. Set `bootstrap_peers` to the **current multiaddr** of an existing
   peer. For Phase 4 testnet that is us; the live multiaddr is
   published at [`qsdm.tech/validators.html`](https://qsdm.tech/validators.html)
   and the peer-id is always queryable live at
   `curl -s https://api.qsdm.tech/api/v1/status | jq -r .node_id`. The
   multiaddr looks like:

   ```
   /ip4/206.189.132.232/tcp/4001/p2p/12D3KooW…
   ```

   The `12D3KooW…` peer-id changes every time the node's libp2p key
   rotates, so don't hard-code it from a stale tutorial — always grab
   the current value.
3. Start `qsdm-validator` under systemd, expose `:4001/tcp` to the
   internet, put Caddy in front of `:8080` for TLS.
4. Watch `/api/v1/status` — once `connected_peers > 0` and `sync_lag`
   trends down, you're mesh-resident.

### 2.3 Running a second validator yourself

If you already operate one validator and want to add a second one for
redundancy or for running against a sovereign fork, use the helper
script:

```bash
sudo bash QSDM/deploy/bring-up-validator.sh \
  --bootstrap /ip4/<your-first-node-ip>/tcp/4001/p2p/<your-first-node-peer-id>
```

This is the same systemd shape as
`VALIDATOR_QUICKSTART.md` but skips manual config editing. It is what
we use to bring up paired validators for blue/green deploys.

### 2.4 Optional: NGC attestation transparency badge

You do not need a GPU to run a validator. You also do not need
NVIDIA NGC attestation to run a validator. If, however, you want the
"Attested" badge on the public [trust page](https://qsdm.tech/trust.html)
as a transparency signal, run the attestation sidecar — see §4.

---

## 3. Miner path — GPU-accelerated

### 3.1 Do I *need* an NVIDIA GPU?

No. You can mine with CPU today and the protocol will accept your
proofs. What you lose is the hashrate that makes your proofs
competitive.

### 3.2 Performance tiers

The numbers below are **qualitative tiers**, not benchmark commits.
Actual hashrate depends on driver version, power/thermal budget, DAG
size, and the current mesh3D epoch. Treat them as where you sit in
the queue, not what you'll earn.

| Tier | Hardware | Relative hashrate | Practical use | NGC attestation eligible? |
|---|---|---|---|---|
| **Reference CPU** | Any x86-64 with Go 1.25+, no GPU | 1× (baseline, single-digit H/s) | Protocol conformance, self-test, occasional testnet block under low difficulty | No (no GPU to attest) |
| **Entry NVIDIA (RTX 3050 / 3060, 8 GB VRAM)** | CUDA 11+, 8 GB+ VRAM, NVIDIA Container Toolkit | Thousands× CPU baseline (mesh3D CUDA kernels unlock) | Home miner, expected to earn occasional mainnet blocks once mainnet is live | **Yes (free NGC tier)** |
| **Mid-range NVIDIA (RTX 3080 / 4070)** | CUDA 11+, ≥ 10 GB VRAM | Higher — scales with SM count and memory bandwidth | Serious home miner, longer profitable runway as difficulty rises | **Yes (free NGC tier)** |
| **High-end NVIDIA (RTX 4090 / H100-class)** | CUDA 12+, ≥ 16 GB VRAM | Much higher — matches or exceeds mining-farm economics | Rack-grade, tolerates aggressive difficulty ramps, best power-per-hash | **Yes (free NGC tier)** |
| **Non-NVIDIA GPU (AMD / Intel)** | OpenCL/ROCm, no first-party support yet | Not shipped today — requires community port of `pkg/mesh3d` kernels | Research / goodwill contributions only | No (NGC is NVIDIA-only tooling) |

The `CUDA` build of `qsdmminer` is the only GPU-accelerated path we
publish binaries for. A CPU-parallel fallback is built in
(`pkg/mesh3d.CPUParallelAccelerator`), which is what the CPU tier
above uses. Until a community contributes ROCm or Level Zero kernels,
"mining on QSDM" in practice means "mining on NVIDIA."

### 3.3 Why NVIDIA is the first-class tier today

- **Kernels shipped in-tree.** `pkg/mesh3d/cuda.go` links
  `libmesh3d_kernels.so`, compiled with `nvcc` in
  `Dockerfile.miner`. There is no equivalent shipped kernel for other
  vendors.
- **CI builds `miner:latest` against CUDA.** Every `main` commit
  produces a runnable NVIDIA-GPU miner image; non-NVIDIA operators
  have to build from source with a patched accelerator.
- **Attestation is free on NVIDIA hardware.** NGC CLI API keys are
  free at [ngc.nvidia.com/setup](https://ngc.nvidia.com/setup) — you
  do **not** need NVIDIA AI Enterprise or any paid plan. See §4.
- **Trust-page badge visibility.** Attested miners show up in the
  public [trust summary](https://qsdm.tech/trust.html) as a
  cryptographic transparency signal tied to their specific silicon.
  Non-attested miners are fully first-class on the network; they
  simply don't carry the badge.

### 3.4 Install & run

Follow [`MINER_QUICKSTART.md`](./MINER_QUICKSTART.md) end-to-end. Two
CPU miner binaries ship in-tree — pick one:

- **`qsdmminer-console`** — recommended for home operators. Interactive
  first-run wizard asks for your reward address and validator URL,
  persists the answer to `~/.qsdm/miner.toml` (Windows:
  `%USERPROFILE%\.qsdm\miner.toml`), then opens a live console panel
  showing hashrate, accepted/rejected proofs, current epoch, and
  uptime. Auto-falls back to one-line-per-event logs under
  `systemd` / `journalctl` / CI (`--plain` forces log mode on a TTY).
  See [`MINER_QUICKSTART.md §2.5`](./MINER_QUICKSTART.md#25-friendly-console-miner-recommended-for-home-operators).
- **`qsdmminer`** — the audit-clean single-file reference miner. Use
  this if you are conformance-testing against `MINING_PROTOCOL.md` or
  embedding the miner in your own tooling; the binary is intentionally
  flag-driven with no TUI so it remains readable top-to-bottom against
  the spec.

Either way:

1. `git clone https://github.com/blackbeardONE/QSDM.git && cd
   QSDM/source`.
2. `go build -o qsdmminer-console ./cmd/qsdmminer-console` (or swap
   in `./cmd/qsdmminer` for the reference binary).
3. `./qsdmminer-console --self-test` (or `./qsdmminer --self-test`) —
   passes in <10 s on any laptop. This is the Phase 4.5 acceptance
   gate; if it fails, stop and open an issue. CI runs both self-tests
   on every push, so a green build means your binary is protocol-
   conformant if rebuilt from the same tag.
4. Point at a validator:

   ```bash
   # Console front-end (zero flags — wizard prompts you):
   ./qsdmminer-console

   # Reference binary (explicit flags — recommended under systemd):
   ./qsdmminer \
     --validator=https://api.qsdm.tech \
     --address=qsdm1<your-reward-address> \
     --batch-count=1
   ```

   For production, run under systemd using the unit file in
   [`MINER_QUICKSTART.md §3.3`](./MINER_QUICKSTART.md).

Pre-built, signed binaries for both miners ship on every tagged
release as `qsdmminer-<os>-<arch>[.exe]` and
`qsdmminer-console-<os>-<arch>[.exe]` on the GitHub Releases page, with
a consolidated `SHA256SUMS` file. If you do not want to install a Go
toolchain, grab the matching asset, verify the hash, and run the
binary directly.

> ⚠️ **NVIDIA-lock pivot in progress.** The `qsdmminer` and
> `qsdmminer-console` CPU binaries are being retired over the next few
> releases in favour of the GPU-only miner described in
> [`nvidia_locked_qsdm_blockchain_architecture.md`](../../../nvidia_locked_qsdm_blockchain_architecture.md).
> The previous "one-command install" scripts
> (`install-qsdmminer-console.sh`, `install-qsdmminer-console.ps1`) and
> the `ghcr.io/<owner>/qsdm-miner-console` Docker image have been
> withdrawn. Once the `v2` protocol activates, proofs from CPU-only
> miners will no longer be accepted by mainnet validators. Plan your
> deployment around an NVIDIA GPU with CUDA support; see
> `Dockerfile.miner` for the GPU reference image.

Every release artefact accepts `--version` and prints a single line
identifying itself, e.g.:

```text
qsdmminer-console v0.1.0 (abc1234, 2026-04-22T10:00:00Z, go1.25.9, linux/amd64)
```

Include this line in any bug report. Binaries built locally from source
show `dev` / `unknown` for tag, SHA, and build date — that is the
expected signal that the binary did not come from the release pipeline.

### 3.5 Monitoring

The miner exposes `qsdm_miner_*` Prometheus metrics (with
`qsdm_miner_*` dual-emitted during the deprecation window per
[`REBRAND_NOTES.md`](./REBRAND_NOTES.md)). Validator-side you'll see
your accepted proofs in `/api/v1/mining/recent-proofs` and — if
you turn on NGC attestation — in `/api/v1/trust/attestations/recent`.

---

## 4. NGC attestation — free NVIDIA tier

> **TL;DR.** If you have any NVIDIA GPU and a free NGC account, you
> can publish signed proofs that tie your node to specific silicon.
> It is a transparency badge, not a consensus requirement.

### 4.1 What it buys you

- An entry in `/api/v1/trust/attestations/recent` with your node-id,
  coarse region, GPU arch, and a freshness timestamp.
- An "Attested" count on the public [trust widget](https://qsdm.tech/trust.html).
- Optional strictness: with `nvidia_lock_gate_p2p=true` set on your
  **own** validator, you can refuse libp2p transactions from
  non-attested peers on your own node's ingress. This is a local
  policy, not a network rule — see
  [`NVIDIA_LOCK_CONSENSUS_SCOPE.md`](./NVIDIA_LOCK_CONSENSUS_SCOPE.md).

### 4.2 What it does **not** buy you

- Consensus votes.
- Fee priority.
- Mining priority.
- Block rewards.

The protocol never rejects a block, a transaction, or a mining proof
for a missing NGC bundle. Ever. See
[`NVIDIA_LOCK_CONSENSUS_SCOPE.md`](./NVIDIA_LOCK_CONSENSUS_SCOPE.md)
for the enforcement table.

### 4.3 Free NGC tier is enough

- Sign up at [ngc.nvidia.com/setup](https://ngc.nvidia.com/setup) —
  free account, free API key.
- The sidecar image `nvcr.io/nvidia/pytorch:*-py3` is on the public
  NGC catalog — free to pull.
- NVIDIA Container Toolkit (driver-side host install) — free.

You never need a paid NVIDIA AI Enterprise subscription to
participate in attestation transparency.

### 4.4 Run it

Follow [`../apps/qsdm-nvidia-ngc/QUICKSTART.md`](../../../apps/qsdm-nvidia-ngc/QUICKSTART.md).
Five steps, ~10 minutes on a live node:

1. Generate a shared ingest secret (`openssl rand -hex 32`).
2. Turn ingest ON on your validator (`QSDM_NGC_INGEST_SECRET=…`
   in the systemd drop-in, then `systemctl restart`).
3. Fill `ngc.env` with your NGC CLI API key + the same secret.
4. `docker compose up -d` — the sidecar posts bundles every
   ~60 s.
5. `curl https://<your-validator>/api/v1/trust/attestations/recent`
   should show your node-id within two cycles.

On the CPU profile (`docker compose --profile cpu up -d`), bundles
are posted without a GPU, which exercises the full ingest path but
reports `gpu_fingerprint.available=false` and doesn't satisfy
`nvidia_lock=true`. On the GPU profile, bundles carry the real
CUDA device properties.

---

## 5. End-to-end: validator + miner + attestation

This is the topology a serious operator ends up with. It is also the
topology we run for the reference deployment.

```
  ┌──────────────────────────────────────────────┐
  │  VALIDATOR (VPS)                             │
  │  ─ Ubuntu 24.04, 4 vCPU, 8 GB RAM            │
  │  ─ qsdm/validator:latest (no CUDA)           │
  │  ─ bootstrap_peers = [api.qsdm.tech/…]      │
  │  ─ :4001 tcp libp2p + :8080 http → Caddy     │
  │  ─ QSDM_NGC_INGEST_SECRET=****           │
  └───────▲─────────────────────────┬────────────┘
          │                         │
 libp2p   │                         │ HTTPS (your-node.example)
 gossip   │                         │
          │                         ▼
  ┌───────┴────────────┐   ┌────────────────────────────┐
  │  The mesh          │   │  NGC SIDECAR (GPU host)    │
  │  (api.qsdm.tech,   │   │  ─ apps/qsdm-nvidia-ngc │
  │   other peers)     │   │  ─ posts proofs every ~60s │
  └────────────────────┘   └────────────────────────────┘
                                    ▲
                                    │ CUDA device probe
                                    │
                           ┌────────┴────────┐
                           │  MINER (GPU rig)│
                           │  ─ qsdmminer    │
                           │  ─ --validator= │
                           │    https://your-│
                           │    node.example │
                           │  ─ RTX 3050+    │
                           └─────────────────┘
```

A few practical notes:

- **The sidecar and the miner do not have to share a host.** The
  sidecar only needs a GPU to produce attestable bundles; the miner
  needs a GPU to be competitive. If you have one GPU rig, run both.
  If you have two, run one per box.
- **Your validator does not need a GPU.** The sidecar posts bundles
  over HTTPS — the validator only verifies HMAC and ingests.
- **Your miner does not need to trust `api.qsdm.tech`.** Point it at
  `https://your-validator.example/` so proofs land on a node you
  control.
- **All three talk over regular internet.** Standard firewalling
  rules: expose `:4001` on the validator to the world, expose
  `:8080` behind TLS only to your trusted miners + the public, keep
  the sidecar outbound-only.

---

## 6. Checklists

### 6.1 Validator launch checklist

- [ ] VPS provisioned, Ubuntu 24.04, 4+ vCPU, 8+ GB RAM, public IP.
- [ ] `bootstrap_peers` set to a current mesh multiaddr.
- [ ] `:4001/tcp` open inbound; `:8080` behind Caddy TLS.
- [ ] Systemd unit file from
      [`VALIDATOR_QUICKSTART.md §5`](./VALIDATOR_QUICKSTART.md) enabled.
- [ ] `/api/v1/status` returns `connected_peers > 0` and `sync_lag`
      decreasing.
- [ ] `/api/v1/health` is `200`.
- [ ] Prometheus scrape on `/api/metrics/prometheus` reports
      `qsdm_chain_tip_height` advancing.
- [ ] (v2 mining) Prometheus alert rules from
      [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](../../deploy/prometheus/alerts_qsdm.example.yml)
      loaded — specifically the four `qsdm-v2-mining-*` groups
      (slashing, enrollment, liveness). Alertmanager routing keyed on
      the `subsystem: v2-mining` label. CI smoke test
      (`promtool check rules`) runs on every push that touches
      `QSDM/deploy/prometheus/**` via `.github/workflows/validate-deploy.yml`.
- [ ] (Optional) NGC sidecar running, `attestations/recent` shows
      your node.

### 6.2 Miner launch checklist

- [ ] `qsdmminer --self-test` passes in <10 s.
- [ ] Target validator's `/api/v1/status` returns `node_role =
      validator` and `chain_tip` advancing.
- [ ] Target validator's `/api/v1/mining/work` returns `200`.
- [ ] `--address` is a QSDM address you control (derived from a
      wallet seed you have backed up — lost seed = lost rewards).
- [ ] Systemd unit running under a non-root user (`qsdm:qsdm`).
- [ ] Logs show `proof ACCEPTED` within your expected window (or
      known `wrong-epoch` / `header-mismatch` noise during clock
      drift — see
      [`MINER_QUICKSTART.md §3.2`](./MINER_QUICKSTART.md)).

### 6.3 NGC sidecar launch checklist

- [ ] Ingest secret set on the node
      (`QSDM_NGC_INGEST_SECRET`), 256-bit random, never reused
      across validators you do not own.
- [ ] `ngc.env` filled, `NGC_CLI_API_KEY` from the free tier,
      ingest secret matches the node's.
- [ ] `docker compose up -d` (CPU or GPU profile).
- [ ] `/api/v1/trust/attestations/recent` shows your node-id within
      two cycles (~2 min).
- [ ] Trust summary at
      [`qsdm.tech/trust.html`](https://qsdm.tech/trust.html) reflects
      your attested count.

---

## 7. Troubleshooting cheat-sheet

| Symptom | Likely cause | First thing to check |
|---|---|---|
| Validator starts but `connected_peers = 0` forever | Stale `bootstrap_peers` multiaddr | Re-fetch from `qsdm.tech/validators` or the project issue tracker; peer-ids rotate with the libp2p key |
| Validator refuses to start, logs `roleguard` error | `mining_enabled=true` in a validator binary | See [`NODE_ROLES.md §2`](./NODE_ROLES.md); validator builds cannot enable mining |
| Miner `--self-test` fails | Build mismatch / corrupt tree | Rebuild from a clean `git clone`, open an issue with the exit code |
| Miner runs but no `proof ACCEPTED` | Easy: wrong validator / clock drift / too slow; hard: you ARE submitting but `too-late` | Inspect stderr rejection reasons per [`MINER_QUICKSTART.md §3.2`](./MINER_QUICKSTART.md) |
| NGC sidecar logs `401` on POST | Ingest secret mismatch between node and sidecar | Verify both sides — secrets must be byte-identical, no trailing whitespace |
| NGC sidecar logs `429 Retry-After` | Node is rate-limiting challenge fetches (many-validators case) | Set `QSDM_NGC_CHALLENGE_JITTER_MAX_SEC=8` per [`ngc.env.example`](../../../apps/qsdm-nvidia-ngc/ngc.env.example) |
| Trust page shows `— of —` permanently | TrustAggregator warm-up or upstream peer unreachable | Wait 30 s after a redeploy (we `sleep 15` in `remote_apply_paramiko.py` for exactly this); then check `/api/v1/trust/attestations/summary` directly |

---

## 8. Further reading

- [`NODE_ROLES.md`](./NODE_ROLES.md) — canonical role split
  rationale.
- [`VALIDATOR_QUICKSTART.md`](./VALIDATOR_QUICKSTART.md) — validator
  install reference.
- [`MINER_QUICKSTART.md`](./MINER_QUICKSTART.md) — miner install
  reference.
- [`MINING_PROTOCOL.md`](./MINING_PROTOCOL.md) — proof format,
  epochs, difficulty, acceptance window.
- [`CELL_TOKENOMICS.md`](./CELL_TOKENOMICS.md) — emission schedule,
  halving, treasury cap.
- [`NVIDIA_LOCK_CONSENSUS_SCOPE.md`](./NVIDIA_LOCK_CONSENSUS_SCOPE.md)
  — why NVIDIA-lock is a transparency signal, not a consensus rule.
- [`../../../apps/qsdm-nvidia-ngc/QUICKSTART.md`](../../../apps/qsdm-nvidia-ngc/QUICKSTART.md)
  — NGC sidecar runbook.
- [`REBRAND_NOTES.md`](./REBRAND_NOTES.md) — QSDM → QSDM migration
  table (env vars, metrics, headers).

> **Corrections welcome.** If this guide contradicts any of the
> documents above, the documents above are authoritative — open a
> PR against this file. The goal of `OPERATOR_GUIDE.md` is to be the
> single entry point, not a source of truth for protocol details.
