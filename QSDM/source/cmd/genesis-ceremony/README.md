# `genesis-ceremony` — DRY-RUN of the QSDM mainnet genesis ceremony

> **This binary does not produce real genesis allocations.** Every artefact
> it emits carries `"dry_run": true` at the top level, and the verifier
> refuses to bless any bundle where that flag is false. It exists so
> validator operators, auditors, and counsel (`tok-01`) can inspect a
> realistic ceremony artefact before mainnet.

## What the ceremony does

N participants run an N-of-N commit-reveal randomness beacon:

1. Each participant generates a fresh keypair and a 32-byte secret
   `reveal`.
2. Each participant publishes `sha3-256(reveal)` as their *commit*
   (revealing nothing yet).
3. Once all commits are in, every participant publishes their `reveal`.
4. The genesis seed is `sha3-256(concat(reveals in ID order))`.
5. Every participant signs the canonical genesis hash
   (`hash(ceremony_id || network || treasury_addr || commit_root ||
   genesis_seed || params)`) with their private key.
6. The final bundle is the concatenation of commits, reveals, the seed,
   and every signature.

Anyone with the bundle can:

- Recompute each commit from its reveal.
- Recompute the genesis seed and genesis hash.
- Verify every signature against each participant's published pubkey.
- Confirm the tokenomics invariant
  `TotalSupplyCell == TreasuryAllocationCell + MiningEmissionCell`.

Run `genesis-ceremony -mode verify -in bundle.json` to execute all of
the above in one step.

## Differences from the production ceremony

| Surface | Dry-run | Production |
|---------|---------|------------|
| Signing | ed25519 (stdlib) | ML-DSA-87 via liboqs (matches validator signing) |
| Phases | single round commit→reveal | commit → timelock-encrypted reveal → open |
| Participant set | synthetic `participant-01 .. -NN` | on-chain validator set at snapshot height |
| Private keys | generated ephemerally in one process | generated on air-gapped hardware per participant |
| Anchoring | bundle JSON only | bundle hash pinned in genesis block |
| Audit trail | local `--out` file | co-published to GHCR, IPFS, and foundation archive |

The production ceremony driver is a separate binary. When it lands, it
will share this binary's canonical serialization shape (§Bundle shape
below) so any tooling written against dry-run bundles continues to work.

## Usage

### Run

```powershell
# from QSDM/source/
go build -o genesis.exe ./cmd/genesis-ceremony
.\genesis.exe -mode run -participants 7 -network qsdm-dryrun -out bundle.json
```

Output (`bundle.json`) is a pretty-printed JSON blob suitable for `jq`
and for checking into public archives during a ceremony rehearsal.

### Verify

```powershell
.\genesis.exe -mode verify -in bundle.json
# genesis-ceremony: verify OK (ceremony_id=..., 7 participants, seed=...)
```

Verification fails (exit code 2) on any of:

- tampered commit / reveal / signature
- participant list not in ID order
- `TotalSupplyCell != TreasuryAllocationCell + MiningEmissionCell`
- schema version drift
- `dry_run != true` (the tool refuses to verify production bundles — use
  the production verifier for those)

### Schema

```powershell
.\genesis.exe -mode schema
```

Emits an empty skeleton bundle with the current schema version and
default tokenomics params. Useful for downstream tooling that wants to
pin the expected shape in its own tests.

## Bundle shape (`schema_version = 1`)

```json
{
  "dry_run": true,
  "schema_version": 1,
  "ceremony_id": "<sha3-256 hex of ceremony identity>",
  "started_at": "<RFC3339 nanos>",
  "finished_at": "<RFC3339 nanos>",
  "network": "qsdm-dryrun-local",
  "params": {
    "total_supply_cell": 100000000,
    "treasury_allocation_cell": 10000000,
    "mining_emission_cell": 90000000,
    "coin_decimals": 8,
    "smallest_unit_name": "dust",
    "target_block_time_secs": 10,
    "halving_every_years": 4
  },
  "participants": [
    {
      "id": "participant-01",
      "pubkey_hex": "...",
      "commit_hex": "sha3-256(reveal)",
      "reveal_hex": "32 random bytes",
      "signature_hex": "ed25519.Sign(genesis_hash)"
    }
    // ...
  ],
  "commit_root_hex":   "sha3-256 over participant commits",
  "reveal_concat_hex": "concat(reveals in participant ID order)",
  "genesis_seed_hex":  "sha3-256(reveal_concat)",
  "genesis_hash_hex":  "sha3-256 over canonical ceremony-identity fields",
  "treasury_address":  "qsdm-treasury-...",
  "bundle_hash_hex":   "sha3-256 over genesis_hash || signatures || finished_at",
  "produced_by":       "cmd/genesis-ceremony (dry-run)",
  "notes":             ["DRY-RUN: ...", "..."]
}
```

## References

- [`Major Update.md`](../../../docs/docs/history/MAJOR_UPDATE_EXECUTED.md)
  §9 Phase 0 (counsel sign-off) and the "mainnet genesis ceremony" row
  in [`NEXT_STEPS.md`](../../../../NEXT_STEPS.md).
- [`CELL_TOKENOMICS.md`](../../../docs/docs/CELL_TOKENOMICS.md) for the
  supply parameters embedded in every bundle.
- [`REBRAND_NOTES.md`](../../../docs/docs/REBRAND_NOTES.md) §4 for the
  Phase-0 working values this dry-run pins.
- [`AUDIT_PACKET_MINING.md`](../../../docs/docs/AUDIT_PACKET_MINING.md) §6
  for the reproducible-build recipe used by all QSDM command binaries.
