# Apps (non-core)

Everything here is **optional** relative to the **`QSDM/`** node (the **QSDM+** ledger). The node builds and runs without these folders.

- **`qsdmplus-landing/`** — Public-facing static site.
- **`qsdmplus-nvidia-ngc/`** — Dockerized validator / gossip / GPU proofs; pairs with `QSDMPLUS_NGC_INGEST_SECRET` (preferred) or `QSDM_NGC_INGEST_SECRET` on the node.
- **`game-integration/`** — Checklists for hooking an external game or app to the QSDM HTTP API (`NEXT_STEPS.md`).

To promote an app to its own repository later, copy one folder and add a client SDK from `QSDM/source/sdk/`.
