# QSDM workspace

> **Rebrand notice (Major Update):** this monorepo is migrating from the transitional name **QSDM+** back to **QSDM**, and introducing the native coin **Cell (CELL)**. Existing folder names (`apps/qsdmplus-landing/`, `apps/qsdmplus-nvidia-ngc/`) and configuration identifiers (`qsdmplus.*` configs, `QSDMPLUS_*` env vars, `X-QSDMPLUS-*` headers) continue to work during the deprecation window. See `QSDM/docs/docs/REBRAND_NOTES.md` for the full migration table.

Monorepo layout:

| Path | What it is |
|------|------------|
| **`QSDM/`** | **QSDM ledger node** — Go implementation (consensus, storage, quantum-safe crypto, wallet/token API). This is the cryptocurrency / chain layer (native coin: **Cell / CELL**). |
| **`apps/`** | **Products and sidecars** that use or complement the node — not required to run the core ledger. |
| **`apps/qsdmplus-landing/`** | Static marketing / explainer site (folder name retained during rebrand deprecation window; served at `qsdm.tech`). |
| **`apps/qsdmplus-nvidia-ngc/`** | Optional NVIDIA NGC GPU proof sidecar — consensus-optional, used for validator attestation (see repo root `nvidia_locked_qsdmplus_blockchain_architecture.md`). |
| **`apps/game-integration/`** | Notes for external game clients (e.g. env, API URLs); see `NEXT_STEPS.md`. |

Architecture and roadmap for the node live under **`QSDM/docs/`** and **`QSDM/README.md`**. Major Update execution plan: `Major Update.md`. Current phase progress: `NEXT_STEPS.md`.
