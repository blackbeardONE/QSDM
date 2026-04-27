# QSDM

**QSDM** (Quantum-Secure Dynamic Mesh ledger) is a post-quantum-secure
ledger with a two-tier node model — CPU-only validators run the PoE + BFT
consensus, and miners run an additive, Mesh3D-tied Proof-of-Work that
mints the native coin, **Cell (CELL)**. Two CPU miner binaries ship
today (`qsdmminer` and the friendlier `qsdmminer-console`); a CUDA
production miner is planned to ship after external security review.

Transaction signatures use **ML-DSA-87** (NIST FIPS 204) — the
standardised post-quantum replacement for classical Ed25519 / Ed448 —
so transactions signed today remain unforgeable against cryptographically
relevant quantum adversaries tomorrow.

> **Rebrand notice.** This monorepo is migrating from the transitional
> name **QSDM** back to **QSDM**. Existing folder names
> (`apps/qsdm-landing/`, `apps/qsdm-nvidia-ngc/`) and
> configuration identifiers (`qsdm.*` configs, `QSDM_*` env
> vars, `X-QSDM-*` headers) continue to work during the
> deprecation window. See
> [`QSDM/docs/docs/REBRAND_NOTES.md`](QSDM/docs/docs/REBRAND_NOTES.md)
> for the full migration table.

## Repository layout

| Path | What it is |
|------|------------|
| [**`QSDM/`**](QSDM/) | **Ledger node** — Go implementation (consensus, storage, ML-DSA-87 signatures, wallet/token API). This is the cryptocurrency / chain layer; native coin is **Cell (CELL)**. |
| [**`QSDM/docs/docs/`**](QSDM/docs/docs/) | User-facing documentation: API reference, mining protocol, node-role split, quickstart guides, rebrand notes, roadmap, deployment guides. |
| [**`apps/`**](apps/) | **Products and sidecars** that use the node but are not required to run the core ledger. |
| [**`apps/qsdm-landing/`**](apps/qsdm-landing/) | Static marketing site served at `qsdm.tech`. (Folder name retained during the rebrand deprecation window.) |
| [**`apps/qsdm-nvidia-ngc/`**](apps/qsdm-nvidia-ngc/) | Optional NVIDIA NGC GPU attestation sidecar — opt-in, per-operator API policy, **not** a consensus rule. See [`QSDM/docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md`](QSDM/docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md). |

## Start here

- **Operator wiki (end-to-end, pick role → hardware → bootstrap → attestation):** [`QSDM/docs/docs/OPERATOR_GUIDE.md`](QSDM/docs/docs/OPERATOR_GUIDE.md) ⭐ start here if you are new
- **Live bootstrap peers for Phase 4 testnet:** [`qsdm.tech/validators.html`](https://qsdm.tech/validators.html)
- **Run a validator (CPU-only):** [`QSDM/docs/docs/VALIDATOR_QUICKSTART.md`](QSDM/docs/docs/VALIDATOR_QUICKSTART.md)
- **Run a miner (CPU reference + console UI; CUDA planned):** [`QSDM/docs/docs/MINER_QUICKSTART.md`](QSDM/docs/docs/MINER_QUICKSTART.md)
- **Run the NGC attestation sidecar (free NVIDIA NGC tier):** [`apps/qsdm-nvidia-ngc/QUICKSTART.md`](apps/qsdm-nvidia-ngc/QUICKSTART.md)
- **API reference:** [`QSDM/docs/docs/API_REFERENCE.md`](QSDM/docs/docs/API_REFERENCE.md) and [`QSDM/docs/docs/openapi.yaml`](QSDM/docs/docs/openapi.yaml)
- **Protocol specs:** [`QSDM/docs/docs/MINING_PROTOCOL_V2.md`](QSDM/docs/docs/MINING_PROTOCOL_V2.md) (canonical v2 spec), [`QSDM/docs/docs/MINING_PROTOCOL.md`](QSDM/docs/docs/MINING_PROTOCOL.md) (frozen v1), [`QSDM/docs/docs/NODE_ROLES.md`](QSDM/docs/docs/NODE_ROLES.md), [`QSDM/docs/docs/CELL_TOKENOMICS.md`](QSDM/docs/docs/CELL_TOKENOMICS.md)
- **Release notes:** [`CHANGELOG.md`](CHANGELOG.md)

> **New?** The operator wiki is a 10-minute read that explicitly answers
> the three questions every new node operator asks: *do I need an NVIDIA
> GPU, do I need a paid NGC plan, and do I have to sync to your VPS?*
> Spoiler: **no, no, and no — but NVIDIA is the first-class path today
> and `api.qsdm.tech` is the recommended bootstrap peer for Phase 4.**

## Trust surface (live reference node)

The reference deployment at `https://api.qsdm.tech/` publishes two
endpoints that make its own coverage legible:

- `GET /api/v1/trust/attestations/summary` — aggregate
  `attested / total_public` ratio across the validator set.
- `GET /api/v1/trust/attestations/recent` — list of recent peer
  attestations with coarse region/GPU-arch metadata only (no PII).

Consumed by the [landing page](apps/qsdm-landing/) and the
dashboard. See
[`QSDM/docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md`](QSDM/docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md)
for why NVIDIA-lock is a transparency signal, not a consensus rule.

## License

[MIT](LICENSE) © 2024-2026 Joedel Lopez Dalioan (Blackbeard).

The ledger node and sidecars are permissively licensed. Vendored
third-party dependencies under `QSDM/source/wasmer-go-patched/` retain
their own licences (see [`QSDM/source/wasmer-go-patched/LICENSE`](QSDM/source/wasmer-go-patched/LICENSE)).
