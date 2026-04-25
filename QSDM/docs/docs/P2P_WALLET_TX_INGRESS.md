# P2P wallet transaction ingress (parent cells, mesh companion, dedupe)

This note is for operators running `qsdm` with libp2p pubsub wallet traffic and optional mesh wire.

## Parent cells (wallet JSON)

- API `POST /api/v1/wallet/send` validates `parent_cells` entries like transaction IDs (length and character rules in `pkg/api/validation.go`).
- The demo wallet loop uses recent stored transaction IDs when available; otherwise it uses deterministic synthetic IDs (`pkg/wallet/parents.go`) so they meet the same validation rules.
- PoE / consensus uses `parent_cells` as opaque byte inputs for signature verification; keep them stable and unique per spend where possible.

## Mesh companion (`qsdm_mesh3d_v1`)

- When `QSDM_PUBLISH_MESH_COMPANION` is `1` / `true` / `yes`, the API (and the demo wallet loop after a successful gossip publish) may send a **second** pubsub frame: a `qsdm_mesh3d_v1` envelope whose payload is the **same** wallet JSON bytes.
- Downstream nodes decode mesh wire in `transaction.DispatchInboundP2P` → `HandlePhase3MeshTx`, which stores the **inner** wallet JSON to storage.

## Dedupe (same logical transaction twice)

- **In-process:** `pkg/walletp2p` (`Reserve` / `Release` / `NoteIngested`) tracks recent wallet JSON `id` values so duplicate ingress is dropped before expensive validation. `cmd/qsdm/transaction` uses `Reserve` on legacy pubsub JSON and mesh inner JSON; **`pkg/networking.TxGossipIngress` calls `NoteIngested` when gossip validation accepts**, so the same id is not processed again on the legacy wallet/mesh path. Metric: `qsdm_p2p_wallet_ingress_dedupe_skip_total`.
- **SQLite / Scylla:** `StoreTransaction` skips insert (and balance side-effects) when a row with the same `tx_id` already exists.
- **File storage:** when JSON includes `id`, files are named `wallet_tx_<sanitized_id>.dat`; a second store with the same id is a no-op.

## Retrieval

- `GetTransaction` / recent-tx APIs use storage indexes on `tx_id` (SQLite/Scylla). File-backed storage does not index by id; use SQLite or Scylla for production-style lookup.

## Related env vars

| Variable | Purpose |
|----------|---------|
| `QSDM_PUBLISH_MESH_COMPANION` | Also publish mesh wire after wallet JSON (API + demo loop). |
| `QSDM_WASM_PREFLIGHT_MODULE` | Path to `wasm_module.wasm` for wazero `validate_raw` preflight on P2P JSON. |
