# MINER_QUICKSTART — Mine QSDM on mainnet (v2 NVIDIA-locked)

> **Status:** As of v0.3.2 the live QSDM chain at `https://api.qsdm.tech`
> runs **v2 only** at consensus (`FORK_V2_HEIGHT = 0`, see
> [`MINING_PROTOCOL_V2.md §10.4`](./MINING_PROTOCOL_V2.md) and the
> ratified decision record in §13.4). Every block at every height
> accepts **only** `Proof.Version = 2` proofs carrying a
> consensus-checked `nvidia-cc-v1` (datacenter) or `nvidia-hmac-v1`
> (consumer) attestation. A v1 proof is rejected at the verifier with
> `ReasonBadVersion`; an empty / unparseable / stale / signature-invalid
> attestation is rejected with `ReasonAttestation`.
>
> The mainnet posture is also self-advertised by
> [`GET /api/v1/status`](#self-detect):
>
>     "mining": {
>       "protocol_versions_accepted": [2],
>       "fork_v2_active":            true,
>       "attestation_types_required":["nvidia-cc-v1","nvidia-hmac-v1"],
>       "min_enroll_stake_dust":     1000000000
>     }
>
> The CPU reference miner (`cmd/qsdmminer`, v1-only) is **no longer a
> public release artefact** as of v0.3.2 and is no longer linked from
> the landing page. It stays in-tree for protocol audit and
> local-devnet bring-up only — see [Appendix A](#appendix-a-v1-audit--local-devnet-builds).
> Both miner binaries refuse to start a v1 mining loop against a
> v2-active validator unless `--allow-v1` is explicitly passed.

This document walks a home operator through:

1. [Pre-requisites](#1-requirements) — what hardware, software, and on-chain state you need before you start.
2. [Reward address](#2-reward-address) — generate a self-custody QSDM keystore.
3. [HMAC key + enrollment](#3-hmac-key--on-chain-enrollment) — register your `(node_id, gpu_uuid, hmac_key)` on chain and bond the 10 CELL stake.
4. [Mine](#4-mine) — start `qsdmminer-console --protocol=v2` and watch the live panel.
5. [Lifecycle commands](#5-lifecycle-commands) — unenroll, slash, browse the registry, stream events.
6. [Troubleshooting](#6-troubleshooting) and [Reporting bugs](#7-reporting-bugs).
7. [Appendix A](#appendix-a-v1-audit--local-devnet-builds) — building `cmd/qsdmminer` for v1 audit / local-devnet only.

It assumes you have already read [`NODE_ROLES.md`](./NODE_ROLES.md) and the v2 spec in [`MINING_PROTOCOL_V2.md`](./MINING_PROTOCOL_V2.md). If you are running a validator, read `VALIDATOR_QUICKSTART.md` instead — mining and validation are separate binaries on separate machines.

---

## 1. Requirements

To mine on mainnet you need:

- **An NVIDIA GPU you control.** Either a datacenter card (Hopper /
  Blackwell with NVIDIA Confidential Compute → the `nvidia-cc-v1`
  attestation path), or a consumer NVIDIA card (Turing / Ampere / Ada /
  Blackwell consumer → the `nvidia-hmac-v1` path described in this
  doc). Non-NVIDIA GPUs and pure-CPU rigs cannot produce a valid v2
  attestation and their proofs are rejected at consensus.
- **Go 1.25+** to build the miner from source (until the cosign-signed
  `qsdmminer-console` binaries are downloaded from the GitHub release
  page directly, see step 4). No CGO required.
- **A reward address you own** (this doc, [§2](#2-reward-address)).
- **10 CELL** liquid on that address, to bond as the enrollment stake.
  The stake is debited when the enroll tx is included in a block and
  released after a 30-day unbond window. **Funding caveat:** the
  chain reset at v2 activation means CELL supply is fresh; outside
  operators need a route to acquire CELL before they can enroll. The
  current bootstrap surface is documented in
  [`MINING_PROTOCOL_V2.md §10.6 (chain reset funding)`](./MINING_PROTOCOL_V2.md)
  and re-summarised in [§3](#3-hmac-key--on-chain-enrollment) below.
- **Network access** to a validator HTTP endpoint you trust. For
  mainnet this is `https://api.qsdm.tech`; for local devnet, whatever
  your `cmd/qsdm` is bound to.
- **~3 GiB free RAM** for the active mining-epoch DAG (see
  `MINING_PROTOCOL.md §3.3`).

<a id="self-detect"></a>

### 1a. Self-detect the validator's posture

Before doing anything else, query the validator and confirm v2 is what
you expect:

```bash
curl -s https://api.qsdm.tech/api/v1/status | jq '.mining'
```

You should see `fork_v2_active: true` and `protocol_versions_accepted:
[2]`. Both miner binaries (`qsdmminer-console` and `qsdmminer`) run
this exact probe automatically at startup and refuse to enter the
mining loop in mismatched configurations — see the [`preflight`
package](../../source/pkg/mining/preflight/) for the full decision
table.

## 2. Reward address

Two equivalent paths; both produce a passphrase-encrypted JSON keystore in `pkg/keystore` v1 format (PBKDF2-HMAC-SHA-256 with 600 000 iterations → AES-256-GCM). The ML-DSA-87 (FIPS 204) keypair is generated **locally** in either case — neither flow exposes the private key to a validator or any third party.

**Path A — CLI (recommended for cold storage):**

```bash
cd QSDM/source
go build -o qsdmcli ./cmd/qsdmcli

# Prompts twice for a passphrase, writes to ~/.qsdm/wallet.json (mode 0600),
# prints ONLY the address to stdout so the line can be piped into a miner.
./qsdmcli wallet new
# → 7a3b…1c4d   (your reward address)

# Optional: inspect what's on disk without revealing the private key
./qsdmcli wallet show
```

**Path B — browser:** visit **<https://qsdm.tech/wallet.html>**, type a passphrase, click *Generate*. The page runs `wallet.wasm` locally, hands you a `qsdm-wallet-<address>.json` download. Same file format as the CLI: drop it on disk and `qsdmcli wallet show --in <file>` reads it back. The browser page never POSTs the passphrase or the private key anywhere — verify in DevTools → Network. SHA-384 Subresource Integrity is enforced on `wallet.js`, `wasm_exec.js`, and `wallet.wasm` so a CDN-side swap of any of the three would break loudly rather than silently sign keystores with rogue code.

In both cases: **back up the JSON file AND the passphrase.** Losing either makes the address unrecoverable. There is no server-side recovery.

## 3. HMAC key + on-chain enrollment

v2 mining requires a registered `(node_id, gpu_uuid, hmac_key)` tuple
on chain, with `MIN_ENROLL_STAKE = 10 CELL` bonded to the node_id.

### 3.1 Generate an HMAC key

Use the miner's built-in helper rather than `openssl rand`; it writes
mode 0600 and refuses to overwrite an existing key (rotation is
deliberate):

```bash
go build -o qsdmminer-console ./cmd/qsdmminer-console
./qsdmminer-console --gen-hmac-key=$HOME/.qsdm/hmac.key
```

The helper prints a copy-pasteable `qsdmcli enroll …` snippet
pre-populated with the new key's hex form on success.

### 3.2 Get your GPU UUID

```bash
nvidia-smi --query-gpu=uuid,name,compute_cap,driver_version --format=csv,noheader
# GPU-12345678-1234-1234-1234-123456789abc, NVIDIA GeForce RTX 4090, 8.9, 572.16
```

The GPU UUID is the consensus-binding identifier — it is signed into
your enrollment record and into every proof's attestation bundle.

### 3.3 Acquire 10 CELL for the bond

> **Funding caveat — read this before you enroll.** As of v0.3.2 the
> live mainnet (`api.qsdm.tech`) has **no end-user funding path for
> new operators**. Newly-created wallets start at 0 CELL. The 10 CELL
> enrollment stake therefore has to come from somewhere; see
> [§Appendix B](#appendix-b-enrollment-funding-status) for the
> currently-shipped routes (initial-operator allocation, peer
> transfer) and the open work item (public bootstrap faucet). If you
> do not have a funding path lined up, **stop here** and file the
> enrollment-funding issue — submitting `qsdmcli enroll` against an
> account with < 10 CELL produces a confusing `insufficient_balance`
> rejection from the validator's admission gate.

### 3.4 Submit the enroll transaction

```bash
go build -o qsdmcli ./cmd/qsdmcli
export QSDM_API_URL=https://api.qsdm.tech/api/v1

./qsdmcli enroll \
  --sender=qsdm1YOURADDR \
  --node-id=rig-77 \
  --gpu-uuid=$(nvidia-smi --query-gpu=uuid --format=csv,noheader | head -1) \
  --hmac-key=$(cat $HOME/.qsdm/hmac.key) \
  --nonce=<your-current-account-nonce>
```

The CLI builds a canonical `EnrollPayload` through
`pkg/mining/enrollment.EncodeEnrollPayload` (the exact codec the
mempool admission gate uses for verification) and POSTs it to
`/api/v1/mining/enroll`. `--stake` defaults to
`mining.MinEnrollStakeDust` (10 CELL = 1_000_000_000 dust). The
validator returns HTTP 202 Accepted with a `tx_id`. The bond is
debited at block-inclusion time. Confirm with
`./qsdmcli enrollment-status rig-77` once it's mined — you should
see `phase: active`.

The full lifecycle (unbond, slash, watch) is documented in
[§5](#5-lifecycle-commands) further down.

## 4. Mine

### 4.1 Install (`qsdmminer-console`)

```bash
git clone https://github.com/blackbeardONE/QSDM.git
cd QSDM/source
go build -o qsdmminer-console ./cmd/qsdmminer-console
```

Or, once cosign-signed release binaries exist for your `(os, arch)`,
download from the GitHub release page and verify:

```bash
cosign verify-blob \
  --certificate qsdmminer-console-linux-amd64.pem \
  --signature   qsdmminer-console-linux-amd64.sig \
  --certificate-identity-regexp 'https://github.com/.+/.github/workflows/release-container.yml@refs/tags/v.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  qsdmminer-console-linux-amd64
```

### 4.2 Verify the binary — self-test

```bash
./qsdmminer-console --self-test
```

10-second smoke test: builds a synthetic 4-batch work-set + small
in-memory DAG, solves under easy difficulty, verifies against the
in-process `pkg/mining` verifier. Same gate that runs in CI on every
push. If this fails, **stop** — open an issue.

### 4.3 Start mining v2

```bash
./qsdmminer-console --protocol=v2 \
  --validator=https://api.qsdm.tech \
  --address=qsdm1YOURADDR \
  --hmac-key-path=$HOME/.qsdm/hmac.key \
  --node-id=rig-77 \
  --gpu-uuid=$(nvidia-smi --query-gpu=uuid --format=csv,noheader | head -1) \
  --gpu-arch=ada \
  --gpu-name="NVIDIA GeForce RTX 4090" \
  --compute-cap=8.9 \
  --cuda-version=12.8 \
  --driver-ver=572.16
```

On first run the binary will:

1. Probe `/api/v1/status`; print the validator's mining posture and
   either proceed or refuse (the preflight check; see §1a above).
2. Resolve the v2 config; abort with an actionable error if any field
   is missing or the HMAC key file is unreadable.
3. Poll `/api/v1/mining/enrollment/rig-77` and surface
   `phase=active|pending_unbond|revoked|not_found` in the live panel.
4. Enter the v2 mining loop: fetch challenge → solve → wrap proof in
   an `nvidia-hmac-v1` bundle → POST `/api/v1/mining/submit`.

The panel shows the enrollment phase, last challenge age, and shares
accepted / rejected — see [§5c](#5c-mining-v2-from-the-console-miner)
for the full panel layout.

### 4.4 Save your config

After the first successful run, write your settings to
`~/.qsdm/miner.toml` so subsequent runs need no flags:

```bash
./qsdmminer-console --setup
# Walks the interactive v2 wizard; saves to ~/.qsdm/miner.toml (0o600)
```

Or hand-edit the file. The schema is documented in the `Config`
struct at the top of `cmd/qsdmminer-console/main.go`.

## 5. Lifecycle commands

The on-chain enrollment record is mutable state — you can read it,
unbond it, get slashed against it, and stream phase-change events from
it. All four lifecycle operations are surfaced as `qsdmcli`
subcommands so operators do not need to hand-build canonical payloads
or remember endpoint paths.

The subsections below — enroll, check, unbond, slash, browse, watch —
are the canonical operator surface. Step 3.4 above used `qsdmcli
enroll` once; the same binary covers the rest.

### 5.1 `qsdmcli` subcommands

Build the CLI once:

```bash
cd QSDM/source
go build -o qsdmcli ./cmd/qsdmcli
```

Then point it at any validator that exposes the v2 mining HTTP surface (anything past commit `7f45be7` running `cmd/qsdm`):

```bash
export QSDM_API_URL=https://testnet.qsdm.tech/api/v1
```

### Enroll a NodeID

```bash
./qsdmcli enroll \
  --sender=qsdm1YOURADDR \
  --node-id=rig-77 \
  --gpu-uuid=GPU-12345678-1234-1234-1234-123456789abc \
  --hmac-key=$(openssl rand -hex 32) \
  --nonce=<your-current-account-nonce>
```

The CLI builds a canonical `EnrollPayload` through `pkg/mining/enrollment.EncodeEnrollPayload` (the exact codec the mempool admission gate uses for verification) and POSTs it to `/api/v1/mining/enroll`. `--stake` defaults to `mining.MinEnrollStakeDust` (10 CELL = 1_000_000_000 dust) — the value the v2 spec ratifies as the minimum bond.

The validator returns HTTP 202 Accepted with a `tx_id`. The bond is debited from your account at block-inclusion time, and the resulting `EnrollmentRecord` is keyed by `node-id`.

### Check status

```bash
./qsdmcli enrollment-status rig-77
```

Returns the sanitized `EnrollmentRecordView`:

- `phase`: `active`, `pending_unbond`, or `revoked`
- `slashable`: whether the bond is still locked (and therefore the rig can still be punished)
- `gpu_uuid`, `owner`, `enrolled_height`, `bond_dust`, `unbond_height`

`hmac_key` is omitted by design — the value is public chain state, but the read endpoint follows least-privilege defaults so a casual `curl` does not surface live operator secrets.

### Begin unbond

```bash
./qsdmcli unenroll \
  --sender=qsdm1YOURADDR \
  --node-id=rig-77 \
  --reason="upgrading to 5090"
```

This starts the 7-day unbond window. The bond is **not** released immediately — auto-sweep happens at maturity inside the block producer's `OnSealedBlock` hook. Until the sweep, the record stays in `phase=pending_unbond` and remains `slashable=true`. After sweep, balance is credited back and the record moves to `revoked`.

### Submit slashing evidence

If you observe a peer forging an attestation or double-mining, post evidence so the chain can punish them:

```bash
./qsdmcli slash \
  --sender=qsdm1WATCHER \
  --node-id=rig-cheater \
  --evidence-kind=forged-attestation \
  --evidence-file=./evidence.bin \
  --amount=500000000 \
  --memo="caught at height 24117"
```

`--evidence-kind` ∈ `{forged-attestation, double-mining, freshness-cheat}`. Use `--evidence-file -` to read evidence bytes from stdin (handy for piping a slasher tool's output) or `--evidence-hex=<HEX>` for short inline blobs.

The reward (`SlashRewardCap = 200 bps` of the slashed amount, capped) is credited to your sender on inclusion. If the offender's bond falls below `mining.MinEnrollStakeDust` after the slash, `RevokeIfUnderBonded` automatically transitions the record to `revoked` so they cannot keep mining on a stub bond.

### Building evidence with `slash-helper`

The `--evidence-file` argument above expects raw, canonical-JSON-wrapped bytes that the chain-side `forgedattest` / `doublemining` decoders will accept. Building those by hand is a footgun: a `json.Marshal(proof)` silently drops four binary fields (`HeaderHash`, `BatchRoot`, `Nonce`, `MixDigest` are all tagged `json:"-"`), so the wrong helper produces evidence that admits-fine but ends up rejected mid-flight as `verifier_failed`, costing you the submission fee with nothing to show for it.

`qsdmcli slash-helper` owns exactly the `EncodeEvidence` calls the chain consumes, so the bytes it emits ARE the bytes consensus accepts. Three subcommands:

```bash
# Forged attestation — one offending proof:
./qsdmcli slash-helper forged-attestation \
  --proof=offending-proof.json \
  --fault-class=hmac_mismatch \
  --node-id=rig-cheater \
  --memo="caught by watcher #4" \
  --out=evidence.bin \
  --print-cmd

# Double mining — two equivocating proofs at the same height:
./qsdmcli slash-helper double-mining \
  --proof-a=fork-validator-1.json \
  --proof-b=fork-validator-3.json \
  --node-id=rig-cheater \
  --memo="fan-out across two validators" \
  --out=evidence.bin

# Inspect an evidence blob someone else built:
./qsdmcli slash-helper inspect \
  --kind=forged-attestation \
  --evidence-file=./evidence.bin
```

Pass `-` as a path to read a proof or evidence blob from stdin so you can pipe directly:

```bash
./qsdmcli slash-helper forged-attestation --proof=p.json | \
  ./qsdmcli slash --sender=qsdm1WATCHER --node-id=rig-cheater \
                  --evidence-kind=forged-attestation --evidence-file=- \
                  --amount=1000000000
```

`--print-cmd` (build subcommands) emits a placeholder `qsdmcli slash …` invocation to **stderr** after the evidence bytes are written, so the snippet doesn't corrupt your `--out=-` stdout pipe. Pre-flight checks fire before encoding to save you a round trip:

| Check | Subcommand | Surfaces |
| --- | --- | --- |
| `proof.version >= 2` | both | non-v2 proofs are not slashable as forged-attestation / double-mining |
| `bundle.node_id == --node-id` | both | binds the slasher's claim to the bundle the offender signed |
| same `(Epoch, Height)` | double-mining | a height/epoch mismatch isn't equivocation |
| distinct canonical bytes | double-mining | two copies of one proof aren't equivocation either |
| Decode round-trip | both | encoder bug detection — refuses to emit bytes the verifier would reject |

The encoder also canonicalises `(proof_a, proof_b)` order in `double-mining` so two slashers who independently observe the same equivocation pair produce **byte-identical** evidence — preserving the chain-side per-fingerprint replay protection in `slash_apply.go`.

### Reading the slash receipt

Every slash that reaches the applier — applied or rejected — produces a receipt that the validator caches in a bounded in-memory store. Look it up by tx id to confirm the chain accepted (or rejected) your submission without scraping logs:

```bash
./qsdmcli slash-receipt <tx-id>
```

A successful applied receipt looks like:

```json
{
  "tx_id": "8f3c…",
  "outcome": "applied",
  "recorded_at": "2026-04-26T22:55:35Z",
  "height": 1421,
  "slasher": "qsdm1WATCHER",
  "node_id": "rig-cheater",
  "evidence_kind": "forged-attestation",
  "slashed_dust": 500000000,
  "rewarded_dust": 10000000,
  "burned_dust": 490000000,
  "auto_revoked": true,
  "auto_revoke_remaining_dust": 100000000
}
```

A rejected receipt carries the reason tag (`verifier_failed`, `evidence_replayed`, `node_not_enrolled`, etc.) and an `error` string for debugging. `404` from this endpoint means the receipt is unknown OR has aged out of the bounded store (default cap: 10000 receipts, FIFO eviction); `503` means the node is v1-only.

### Browsing the enrollment registry

Operators, indexers, and dashboards that need to see the whole on-chain registry — not just one record — can page through it via `qsdmcli enrollments`:

```bash
# First page (server default page size, no filter):
./qsdmcli enrollments

# Filter to currently-active rigs only, 100 per page:
./qsdmcli enrollments --phase=active --limit=100

# Resume from a previous next_cursor:
./qsdmcli enrollments --cursor=rig-077 --limit=100

# Walk every page and print one aggregate envelope:
./qsdmcli enrollments --all --phase=pending_unbond
```

Each response is an `EnrollmentListPageView`:

```json
{
  "records": [ /* EnrollmentRecordView */ ],
  "next_cursor": "rig-077",
  "has_more": true,
  "total_matches": 137,
  "phase": "active"
}
```

`--all` follows `next_cursor` until `has_more` is `false`, concatenating records into a single envelope. Pagination is **cursor-based** (not offset) so a record enrolled or revoked between pages does not silently shift the page boundaries — the cursor is the exclusive lower bound on `node_id`, sorted lexicographically. `phase` ∈ `{active, pending_unbond, revoked}` (omit for "every record"). The handler clamps `--limit` to `MaxListLimit = 500` so a single call cannot drain the registry; use `--all` for full dumps. `503` means the node is v1-only (no lister wired).

### Streaming phase-change events with `qsdmcli watch`

`qsdmcli enrollments` is a one-shot snapshot. For dashboards / alerting / fleet operators who need to see lifecycle transitions **as they happen**, `qsdmcli watch enrollments` polls the same endpoints in a loop and prints one line per detected change:

```bash
# Stream every active rig; default 30s cadence, human format on stdout:
./qsdmcli watch enrollments --phase=active

# Watch one specific rig (single-node mode hits /enrollment/{node_id} instead):
./qsdmcli watch enrollments --node-id=rig-77

# JSON-Lines for log shippers (Loki, ELK, etc.); 10-second cadence:
./qsdmcli watch enrollments --json --interval=10s | tee enrollments.jsonl

# Cron-friendly: snapshot once and exit, including every existing record:
./qsdmcli watch enrollments --once --include-existing --json
```

Five event kinds are emitted (`new`, `transition`, `stake_delta`, `dropped`, `error`), all sharing one wire shape. Example human-format output:

```text
2026-04-28T03:51:42Z NEW         node=alpha-rtx4090-01  phase=active                       stake=10.0000 CELL  enrolled_at=1234567
2026-04-28T03:52:12Z TRANSITION  node=beta-rtx3090-02   phase=active->pending_unbond       matures_at=1235000
2026-04-28T03:55:42Z STAKE_DELTA node=alpha-rtx4090-01  phase=active  stake=10.0000 CELL->5.0000 CELL  delta=5.0000 CELL
2026-04-28T03:56:12Z DROPPED     node=gamma-rtx5090-03  last_phase=pending_unbond
```

Same data under `--json`:

```json
{"ts":"2026-04-28T03:51:42Z","event":"new","node_id":"alpha-rtx4090-01","phase":"active","stake_dust":1000000000,"slashable":true,"enrolled_at_height":1234567}
{"ts":"2026-04-28T03:52:12Z","event":"transition","node_id":"beta-rtx3090-02","phase":"pending_unbond","prev_phase":"active","unbond_matures_at_height":1235000}
{"ts":"2026-04-28T03:55:42Z","event":"stake_delta","node_id":"alpha-rtx4090-01","phase":"active","stake_dust":500000000,"prev_stake_dust":1000000000,"delta_dust":-500000000,"slashable":true}
{"ts":"2026-04-28T03:56:12Z","event":"dropped","node_id":"gamma-rtx5090-03","prev_phase":"pending_unbond"}
```

Operational notes:

- **Polling-only, no key required.** `qsdmcli watch` never submits a transaction — safe to run on a low-trust admin host pointing at a public RPC node.
- **Diff-based.** First poll holds the initial snapshot in memory and emits nothing (or one `new` per record under `--include-existing`); every subsequent poll diffs against the previous and emits one event per change. Process restart re-snapshots from scratch.
- **Deterministic ordering.** Within one poll cycle, events are sorted by `node_id` ASC so two consecutive runs over identical data produce byte-identical output. Useful for diffing log captures across runs.
- **Exit codes.** `0` on `Ctrl-C` / `SIGTERM` (operator-driven exit). Non-zero **only** on initial-snapshot failure (e.g. validator unreachable from the start, validator returns 503 = v1-only). Subsequent poll failures are emitted as `error` events on stderr (or stdout under `--json`) and the loop continues.
- **Cadence floor.** `--interval` is clamped to ≥ 5 seconds; the read endpoints are hot in-memory map lookups so sub-second polling is not necessary and just pressures the validator.
- **Single-node vs list mode.** `--node-id` and `--phase` are mutually exclusive. Single-node mode polls `/api/v1/mining/enrollment/{node_id}` and treats `404` as "no record" (emits `dropped` if a record was previously seen, nothing otherwise). List mode walks `/api/v1/mining/enrollments` with cursor pagination and supports `--phase` server-side filtering.

### Streaming slash-receipt events with `qsdmcli watch slashes`

The symmetric tool for the slashing surface. `qsdmcli watch slashes` polls `/api/v1/mining/slash/{tx_id}` for a caller-supplied set of slash transaction ids and prints one event per resolution / eviction / outcome change. Use case: an operator submits a slash with `qsdmcli slash` (or assembles evidence with `qsdmcli slash-helper`), captures the returned `tx_id`, and wants the watcher to surface "did it apply?" without manually polling.

```bash
# Track a single slash; default 30s cadence, human format on stdout:
./qsdmcli watch slashes --tx-id=tx-deadbeef-001

# Track several at once (repeatable flag):
./qsdmcli watch slashes \
  --tx-id=tx-deadbeef-001 \
  --tx-id=tx-deadbeef-002 \
  --tx-id=tx-cafef00d-003

# Read tx ids from a file (one per line; '#' starts a comment); '-' = stdin:
./qsdmcli watch slashes --tx-ids-file=./pending-slashes.txt --json

# CI / cron pattern: snapshot once, exit cleanly when every tx is terminal:
./qsdmcli watch slashes --tx-id=tx-001 --tx-id=tx-002 --exit-on-resolved --json

# Verbose mode: echo a `slash_pending` event each cycle for unresolved tx ids
# (useful when debugging "why isn't my slash landing?"):
./qsdmcli watch slashes --tx-id=tx-001 --include-pending --interval=10s
```

Five event kinds are emitted (`slash_resolved`, `slash_pending`, `slash_evicted`, `slash_outcome_change`, `error`), all sharing the same JSON wire shape as `watch enrollments`. Example human-format output:

```text
2026-04-28T04:20:42Z SLASH_RESOLVED      tx=tx-deadbeef-001  outcome=applied   node=rig-77  kind=forged-attestation  height=42  slashed=5.0000 CELL  rewarded=0.1000 CELL  burned=4.9000 CELL  auto_revoked=true
2026-04-28T04:21:12Z SLASH_RESOLVED      tx=tx-deadbeef-002  outcome=rejected  node=rig-99  kind=double-mining  height=43  reason=verifier_failed  err=verifier said no
2026-04-28T04:25:42Z SLASH_PENDING       tx=tx-cafef00d-003
2026-04-28T05:30:00Z SLASH_EVICTED       tx=tx-old-004        last_outcome=applied
```

Same data under `--json`:

```json
{"ts":"2026-04-28T04:20:42Z","event":"slash_resolved","node_id":"rig-77","tx_id":"tx-deadbeef-001","outcome":"applied","height":42,"evidence_kind":"forged-attestation","slasher":"alice","slashed_dust":500000000,"rewarded_dust":10000000,"burned_dust":490000000,"auto_revoked":true,"auto_revoke_remaining_dust":100000000}
{"ts":"2026-04-28T04:21:12Z","event":"slash_resolved","node_id":"rig-99","tx_id":"tx-deadbeef-002","outcome":"rejected","height":43,"evidence_kind":"double-mining","slasher":"bob","reject_reason":"verifier_failed","error":"verifier said no"}
{"ts":"2026-04-28T04:25:42Z","event":"slash_pending","tx_id":"tx-cafef00d-003"}
{"ts":"2026-04-28T05:30:00Z","event":"slash_evicted","tx_id":"tx-old-004","prev_outcome":"applied"}
```

Operational notes:

- **Polling-only, no key required.** Same posture as `watch enrollments` — safe on a low-trust admin host.
- **Inputs.** At least one tx id must be supplied via `--tx-id` (repeatable) **or** `--tx-ids-file`. Both can be combined; the flag-supplied ids are merged with the file contents and de-duplicated. Maximum 1000 distinct tx ids per watcher process; for larger fleets run multiple processes.
- **Default first-poll behaviour.** If a tx id is already terminal at startup (operator restarted the watcher after a slash had landed), one `slash_resolved` event fires immediately. Pending tx ids are silently tracked — no events fire for them until they resolve. Pass `--include-pending` to override this and echo a `slash_pending` event every cycle for unresolved ids.
- **`--exit-on-resolved`.** Returns `0` once every tracked tx id has reached a terminal outcome (`applied` or `rejected`). Mutually exclusive with `--include-pending` (the combination is a footgun). Ideal for CI pipelines that submit a slash and need to wait for the apply.
- **Eviction surfacing.** The validator's `SlashReceiptStore` is bounded (default cap 10 000 receipts, FIFO). If a previously-resolved tx ages out of the store, the next poll surfaces a `slash_evicted` event so the operator stops expecting the receipt to be queryable. Under chain reorg the same event fires (extremely rare on a healthy single-chain network).
- **Outcome change.** The `slash_outcome_change` kind is defensive: receipts are immutable once recorded, so this should never fire on a healthy network. Surfaces a chain reorg, a buggy receipt store, or a node syncing from a stale checkpoint.
- **Per-cycle partial failures are non-fatal.** A transient HTTP error on one tx id does not tear down the loop; the id is silently dropped from this cycle and retried next. Only a *total* failure (every id errors) triggers an `error` event on stderr / stdout-under-`--json`. The initial cycle is the exception: total failure there exits non-zero so misconfigured watcher invocations fail loudly.
- **Cadence floor.** `--interval` is clamped to ≥ 5 seconds, same as `watch enrollments`.

### Streaming arch-spoof / hashrate-band rejection bursts with `qsdmcli watch archspoof`

The third operator-facing watcher in the family. While `watch enrollments` and `watch slashes` follow lifecycle changes for resources the operator owns (rigs, slash transactions), `watch archspoof` is a **fleet-wide attestation-rejection stream**: it polls `/api/metrics/prometheus` and emits one event every time the validator increments `qsdm_attest_archspoof_rejected_total{reason}` (the §4.6 arch-spoof gate) or `qsdm_attest_hashrate_rejected_total{arch}` (the §4.6.3 hashrate-band gate).

This is the **per-event complement** to the Prometheus alert rules in `QSDM/deploy/prometheus/alerts_qsdm.example.yml`: alerts say "the rate is too high"; the watcher says "here is each individual hit, in order, as they happen". Operators running on-call rotations typically pair the two views.

```bash
# Stream every rejection bucket; default 30s cadence, human format on stdout:
./qsdmcli watch archspoof

# JSON-Lines for log shippers; 10-second cadence:
./qsdmcli watch archspoof --json --interval=10s | tee rejections.jsonl

# Filter to the critical bucket only — cc_subject_mismatch means a proof
# passed cert-chain pin + AIK signature but the leaf cert subject
# contradicts the claimed gpu_arch (cryptographic anomaly):
./qsdmcli watch archspoof --reason=cc_subject_mismatch

# Watch a specific arch's hashrate rejections:
./qsdmcli watch archspoof --arch=hopper,blackwell

# Snapshot once and exit, including every existing non-zero counter:
./qsdmcli watch archspoof --once --include-existing --json

# Override the metrics URL (split data-plane / metrics-plane deployments):
QSDM_METRICS_URL=https://metrics.example.com/api/metrics/prometheus \
  ./qsdmcli watch archspoof
```

Sample human output:

```text
2026-04-28T04:00:42Z ARCHSPOOF_BURST          reason=unknown_arch  delta=+3  total=42
2026-04-28T04:01:12Z ARCHSPOOF_BURST          reason=cc_subject_mismatch  delta=+1  total=2
2026-04-28T04:01:42Z HASHRATE_BURST           arch=hopper  delta=+5  total=18
```

Same data under `--json`:

```json
{"ts":"2026-04-28T04:00:42Z","event":"archspoof_burst","reason":"unknown_arch","delta_count":3,"total_count":42}
{"ts":"2026-04-28T04:01:12Z","event":"archspoof_burst","reason":"cc_subject_mismatch","delta_count":1,"total_count":2}
{"ts":"2026-04-28T04:01:42Z","event":"hashrate_burst","arch":"hopper","delta_count":5,"total_count":18}
```

Operational notes:

- **No write surface.** Like every `watch *` subcommand, this never submits a transaction — safe on a low-trust admin host.
- **Auth.** `/api/metrics/prometheus` accepts either a dashboard JWT (Bearer, the path `qsdmcli` uses) or a metrics-scrape secret header. Set the JWT via the standard `QSDM_TOKEN` plumbing or run unauthenticated against an internal node.
- **Counter rollback handling.** Counters monotonically increase under normal operation; a decrease across two polls (process restart wiping in-memory state) snaps the snapshot to the new baseline silently. The watcher errs toward under-counting one cycle rather than synthesising a fake "burst" the moment the validator restarts.
- **Filters validate at parse time.** `--reason` only accepts `unknown_arch`, `gpu_name_mismatch`, `cc_subject_mismatch` (per `MINING_PROTOCOL_V2 §4.6.4`); `--arch` only accepts the canonical NVIDIA family names plus `unknown`. Typos surface immediately rather than as silent no-matches across hours of polling.
- **Per-event detail with `--detailed`.** Counter mode (the default) is label-coarse on purpose: it surfaces `(reason, arch)` deltas, not per-rejection identity. To see *who* got bounced — the proof's `miner_addr`, the bundle-reported `gpu_name`, the leaf cert subject CN, and the verifier's `RejectError` detail — pass `--detailed`. This switches the watcher from polling `/api/metrics/prometheus` to polling `/api/v1/attest/recent-rejections`, and emits one `archspoof_rejection` event per actual store record:

  ```bash
  # Stream the per-record detail. Bearer auth, cursor-paginated:
  ./qsdmcli watch archspoof --detailed

  # Drain everything currently in the ring at startup, then stream new ones:
  ./qsdmcli watch archspoof --detailed --include-existing --json

  # Combine with --reason to filter the stream server-side. Single-value
  # filters forward to the API; multi-value sets fall back to client-side:
  ./qsdmcli watch archspoof --detailed --reason=cc_subject_mismatch
  ```

  As of 2026-04-29 the `gpu_name` field (HMAC paths) and `cert_subject` field (CC paths) populate automatically — no operator action required. The verifier extracts them from the per-type verifier's structured `*archcheck.RejectionDetail` wrapper via `errors.As`, so a `--detailed` event for an HMAC step-8 rejection always carries the rejected `gpu_name` (e.g. `"NVIDIA GeForce RTX 4090"` when an Ada card lazily claimed `gpu_arch=hopper`), and a CC step-9 event always carries the rejected leaf cert's `Subject.CommonName`.

  Sample human output:

  ```
  2026-04-29T13:21:07Z ARCHSPOOF_REJECTION         seq=42  reason=cc_subject_mismatch  arch=ada  miner=qsdm1critical  height=9000  cert_cn=NVIDIA H100 80GB  detail=leaf cn contradicts claimed gpu_arch
  ```

  Sample JSON-Lines output:

  ```json
  {"timestamp":"2026-04-29T13:21:07Z","event":"archspoof_rejection","seq":42,"reason":"cc_subject_mismatch","arch":"ada","height":9000,"miner_addr":"qsdm1critical","cert_subject":"NVIDIA H100 80GB","detail":"leaf cn contradicts claimed gpu_arch"}
  ```

  `--detailed` requires a v2-aware validator with the recent-rejections store wired (every node bootstrapped via `internal/v2wiring.Wire()` qualifies). Older nodes return `503 Service Unavailable` from the endpoint and the watcher exits with a clear message hinting to drop `--detailed` for counter mode.

- **§4.6 telemetry — recent-rejection ring truncation (2026-04-29).** The recent-rejections ring defensively clamps three operator-facing fields before storing them so a malicious miner stuffing the proof envelope cannot blow up validator memory:

  | Field | Cap (runes) | Source |
  |---|---|---|
  | `detail` | 200 | verifier `RejectError.Detail` |
  | `gpu_name` | 256 | HMAC bundle's reported GPU name |
  | `cert_subject` | 256 | CC leaf cert `Subject.CommonName` |

  Every `Store.Record()` call now exports three Prometheus series per field — pre-truncation observation count, truncated-this-time count, and a process-lifetime `runes_max` gauge:

  ```text
  qsdm_attest_rejection_field_runes_observed_total{field="detail"}        345
  qsdm_attest_rejection_field_truncated_total{field="detail"}              4
  qsdm_attest_rejection_field_runes_max{field="detail"}                  217
  ```

  The truncation rate is the rate-quotient:

  ```promql
  rate(qsdm_attest_rejection_field_truncated_total{field="detail"}[5m])
  /
  rate(qsdm_attest_rejection_field_runes_observed_total{field="detail"}[5m])
  ```

  Empty fields skip the recorder entirely so HMAC-only paths (no `cert_subject`) and CC-only paths (no `gpu_name`) do not pollute the denominator. Two example alert rules ship in [`QSDM/deploy/prometheus/alerts_qsdm.example.yml`](../../deploy/prometheus/alerts_qsdm.example.yml) under `qsdm-v2-attest-recent-rejections`: one fires at >25% sustained truncation rate, one is an info-only leading indicator that fires when `runes_max` is within 10% of the cap.

  If sustained truncation indicates a real cap bump (rather than a hostile miner), edit the per-field constants in [`pkg/mining/attest/recentrejects/recentrejects.go`](../../source/pkg/mining/attest/recentrejects/recentrejects.go) — the values `maxDetailRunes`, `maxGPUNameRunes`, `maxCertSubjectRunes` are pinned in one place specifically so a future change is a one-line edit + a CHANGELOG note.

- **§4.6 ring durability — restart no longer wipes forensic record (2026-04-29).** The recent-rejections ring was volatile by design until 2026-04-29; every restart wiped the entire history of arch-spoof / hashrate-band / CC-subject rejections. Production validators now persist the ring to a JSONL log under the state directory, and Wire() replays it at boot.

  Wiring: pass `Config.RecentRejectionsPath` to `internal/v2wiring.Wire()`. The recommended location follows the same pattern as the governance snapshot:

  ```toml
  # qsdm.toml
  [state]
  dir            = "/var/lib/qsdm"
  recent_rejections_path = "/var/lib/qsdm/recentrejects.jsonl"
  ```

  When set, every `Store.Record()` call additionally appends one JSON-encoded record to the file. The file is bounded by a soft cap (1024 records ≈ 256 KiB); when the file exceeds 2× the soft cap, the next Append triggers an in-place compaction (atomic-rename rewrite keeping the most recent 1024 records). Worst-case on-disk footprint is ≈ 512 KiB before compaction, ≈ 256 KiB after. Per-call open/close keeps the syscall budget at ≈ 10 µs/record (0.1% CPU at 100 rejections/s).

  Crash-recovery framing is automatic: if the prior process was hard-killed mid-write, the next Append prepends a leading newline so the corrupt fragment cannot run together with this record. `LoadAll` skips malformed JSON lines so boot succeeds even with a partial-write tail.

  Empty path = legacy in-memory-only posture (no filesystem dependency, fine for ephemeral testnets and CI).

  Persistence health is observable via two surfaces:
  - `qsdm_attest_rejection_persist_errors_total` — Prometheus counter that increments on every `Persister.Append` failure (disk full, permission flap, compaction error). The in-memory ring continues to receive records regardless. Alert on `rate(qsdm_attest_rejection_persist_errors_total[5m]) > 0` for any sustained period.
  - `recentrejects.Store.PersistErrorCount()` — Go-side accessor returning the same counter for in-process inspection (e.g. by an `auditreport` tool that wants to surface persistence health without an HTTP scrape).

  File-permission posture is restrictive (mode 0600); the file contains operator-facing forensic data which mirrors the same posture as the `chainparams` governance snapshot.

- **Counter mode is still useful.** The default mode (no `--detailed`) is the right choice for steady-state alerting and dashboards: it never falls behind even if the ring overflows, and it's the same data Prometheus alert rules trigger on. `--detailed` is the right choice for incident response and forensic correlation — pair the two on adjacent terminal panes.

- **Cadence floor.** `--interval` is clamped to ≥ 5 seconds, same as the other watchers.

### Tooling notes

- All three write subcommands accept `--id` for an idempotent client-supplied tx id; if omitted, `qsdmcli` generates a 16-byte random hex id.
- `--fee` defaults to `0.001 CELL` and must be `> 0` to clear the slashing admission gate.
- The CLI does not sign envelopes today (matching the existing `qsdmcli tx` shape); the validator-side `AccountStore` identifies sender by string and enforces nonce ordering. When Dilithium-signed envelopes land (per [`MINING_PROTOCOL_V2.md §13`](./MINING_PROTOCOL_V2.md#13-historical-decision-record) and the wallet roadmap), `qsdmcli` will gain a single signing call inside `buildEnvelope()` — no flag changes.

### 5.2 Mining v2 from the console miner (panel reference)

Once your enrollment record is on-chain, `qsdmminer-console` can mine v2 directly. The full loop is wired and tested end-to-end (`v2_integration_test.go`): every solved share fetches a fresh `/api/v1/mining/challenge` from the validator, builds an `nvidia-hmac-v1` attestation bundle bound to that challenge, and POSTs a `Version=2` proof. The console reuses the same `pkg/mining/v2client` module the eventual native CUDA miner will, so the end-to-end shape will not change when GPU support lands.

### Generating an HMAC key

Use the binary's built-in helper instead of `openssl rand`:

```bash
./qsdmminer-console --gen-hmac-key=$HOME/.qsdm/hmac.key
```

The file is written `0o600` (POSIX) with a single hex line, so it is readable by `loadHMACKeyFromFile`. The command refuses to overwrite an existing file — rotating a key is a deliberate, manual step. After writing, the helper prints a copy-pasteable `qsdmcli enroll …` snippet pre-populated with the new key's hex form.

### One-shot v2 setup wizard

Re-running `--setup` now offers an opt-in v2 sub-wizard:

```text
Enable v2 NVIDIA-locked protocol? (yes/no) [no]: yes
  HMAC key file path [/home/alice/.qsdm/hmac.key]:
  no key at /home/alice/.qsdm/hmac.key — generating a fresh 32-byte HMAC key…
  wrote /home/alice/.qsdm/hmac.key (0o600)
  NodeID (operator-chosen tag):                rig-77
  GPU UUID (`nvidia-smi -L`):                  GPU-1234…abc
  GPU arch (ada/ampere/hopper/blackwell) [ada]: ada
  …

v2 mining is enabled in the config. To bond your key on-chain, run:

  qsdmcli enroll \
    --validator https://testnet.qsdm.tech \
    --sender   qsdm1YOURADDR \
    --node-id  rig-77 \
    --gpu-uuid GPU-1234…abc \
    --hmac-key 5d3a...
```

The wizard never submits the enroll transaction itself — bonding 10 CELL is a real on-chain side effect, so it stays a deliberate manual step. After the enroll tx is mined, restart `qsdmminer-console` and it picks up `protocol = "v2"` from the saved config.

### Mining loop with v2 enabled

```bash
./qsdmminer-console --protocol=v2 \
  --hmac-key-path=$HOME/.qsdm/hmac.key \
  --node-id=rig-77 --gpu-uuid=GPU-1234…abc --gpu-arch=ada
```

The console panel grows three extra lines when v2 is active:

```text
  v2 NVIDIA       node=rig-77 arch=ada attestations=42 challenge=4s ago
  v2 enroll       phase=active stake=10.000 CELL slashable=yes polled=12s ago
```

`challenge=Ns ago` is the wall-clock age of the most recent successfully built attestation; if it climbs past the consensus `mining.FreshnessWindow` (60 s) it means the validator's challenge endpoint is stalling and submissions will start failing. In `--plain` mode, the same information shows up as `[v2]` events in the log stream.

The `v2 enroll` row is painted by the **background enrollment poller**, which polls `GET /api/v1/mining/enrollment/{node_id}` every 30 s (configurable via `--enrollment-poll`). It surfaces:

| Phase | Color | Meaning |
|---|---|---|
| `active` | green | Bond is locked, validator will accept v2 proofs from this node_id. |
| `pending_unbond` | yellow | Manual unbond initiated — stake still slashable until `unbond_matures_at_height`. |
| `revoked` | red | Slashed, fully drained, or unbond matured. **Mining will be rejected.** |
| `not_found` | red | Validator has no record for this `node_id`. Either the enroll tx hasn't been mined yet or you typed the wrong tag. |
| `unconfigured` | cyan | Validator is v1-only (503 from `/enrollment/`); the read endpoint isn't wired here. |
| `unknown` | cyan | First poll hasn't completed yet, or transient HTTP error. The dashboard remembers the last successful phase between cycles, so a flapping validator does NOT clear the row. |

Phase **transitions** between successful cycles emit a separate event into the panel's "Last event" line:

- `not_found → active` (`[info]`): your enroll tx landed.
- `active → pending_unbond` (`[err]`): either you unbonded, or you got auto-revoked by a slash. Check `qsdmcli slash` activity around the same height.
- `* → revoked` (`[err]`): terminal state. The miner keeps running but its proofs will be rejected; restart after re-enrolling.

Disable the poller with `--enrollment-poll=0` if you're running against a validator without the v2 read surface (the row will stay at `phase=—` and not spam errors). Intervals below 5 s are silently rounded up to 5 s to prevent accidental DDoS during operator debugging.

If the validator's `/api/v1/mining/challenge` endpoint is unreachable (503, network outage), the loop emits an `EvError` with `v2 prepare:` and refuses to fall back to v1 — silently submitting v1 proofs to a forked validator would waste solve cycles and hide the misconfiguration. Once the endpoint recovers, the next iteration succeeds without manual intervention.

## 6. Troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| `[preflight] REFUSING TO MINE` at startup | Validator advertises v2 active but binary was launched in v1 mode (no `--protocol=v2`, or `cmd/qsdmminer` against mainnet). | Pass `--protocol=v2` with full v2 config; OR for local audit only, pass `--allow-v1`. |
| `v2 prepare: enrollment not active` | The `/api/v1/mining/enrollment/{node_id}` poller sees `phase != active`. | Run `qsdmcli enrollment-status <node-id>`; if `not_found`, your enroll tx hasn't mined; if `pending_unbond` / `revoked`, re-enroll. |
| `attestation_stale` rejections from `/api/v1/mining/submit` | Local clock skew >60 s (the `FreshnessWindow`), or validator's `/challenge` endpoint is stalling. | Re-sync NTP; check the `challenge=Ns ago` figure in the live panel. |
| `attestation_invalid` rejections | HMAC key mismatch — your enrollment record holds a different key than the file `--hmac-key-path` points at. | Verify `sha256sum $HOME/.qsdm/hmac.key` matches the enrollment record; re-enroll if mis-keyed. |
| `self-test FAILED: solve: context deadline exceeded` | `--self-test-difficulty` is too high for this CPU. | Re-run with `--self-test-difficulty=2` (default). |
| Every submit returns `reject_reason=wrong-epoch` | Your binary and the validator disagree on `BlocksPerEpoch`. | Confirm both sides are on the same QSDM release tag; rebuild. |
| `fetch work: status 503` loops forever | The validator does not have mining wired up yet. | Point `--validator` at a different node, or wait for testnet staging. |
| Miner crashes on startup with OOM | DAG size > host RAM. | Currently only the production DAG size is supported; add more RAM or wait for the `--dag-size-override` flag (tracked post-audit). |

## 7. Reporting bugs

The console miner is the **protocol truth** implementation for v2 mining. Any disagreement between it and a validator is a protocol issue, not a miner-configuration issue. Please file bugs at the QSDM repository with:

1. Output of `qsdmminer-console --version` — one line carrying the release tag, short git SHA, build date, Go toolchain, and OS/arch.
2. Validator URL (may be redacted) and the `mining` block from `curl /api/v1/status`.
3. Relevant `journalctl` / stderr extract including the failed proof and the server's reject reason.
4. Whether `qsdmminer-console --self-test` still passes on the same binary.
5. Output of `qsdmcli enrollment-status <your-node-id>` if the failure is enrollment-related.

Cross-reference: [`MINING_PROTOCOL_V2.md §7 (Verifier)`](./MINING_PROTOCOL_V2.md) for the consensus-checked rejection taxonomy.

---

## Appendix A. v1 audit / local-devnet builds

> **Mainnet operators: this section is not for you.** It documents
> how to build the in-tree v1 reference miner for protocol audit and
> local-devnet bring-up. The v1 binary submits `Proof.Version = 1`
> proofs, which the mainnet verifier rejects with `ReasonBadVersion`.
> Both miner binaries refuse to start a v1 mining loop against a
> v2-active validator without `--allow-v1`.

The legitimate uses for the v1 path are:

- **Protocol audit.** Reading `cmd/qsdmminer` plus `pkg/mining/pow.go`
  is the canonical reference for the original SHA3-256 DAG walk
  described in [`MINING_PROTOCOL.md`](./MINING_PROTOCOL.md). The v1
  consensus implementation stays in-tree under
  `ComputeMixDigestV1` so v1 historical blocks (if any chain ever
  produced them) remain byte-replayable.
- **Local devnet.** A `cmd/qsdm` instance started with
  `SetForkV2Height(math.MaxUint64)` (the default) accepts only v1
  proofs. Useful for integration tests that don't want to plumb
  through the full v2 attestation surface.
- **CI canary.** `qsdmminer --self-test` runs in `qsdm-split-profile.yml`
  on every push as a deterministic round-trip canary for the v1 PoW
  code path.

### A.1 Build

```bash
git clone https://github.com/blackbeardONE/QSDM.git
cd QSDM/source
go build -o qsdmminer ./cmd/qsdmminer
```

Pure Go, no CGO, no liboqs, no CUDA. Cross-compiles:

```bash
GOOS=linux GOARCH=amd64   go build -o qsdmminer-linux-amd64   ./cmd/qsdmminer
GOOS=windows GOARCH=amd64 go build -o qsdmminer.exe           ./cmd/qsdmminer
GOOS=darwin GOARCH=arm64  go build -o qsdmminer-darwin-arm64  ./cmd/qsdmminer
```

### A.2 Self-test

```bash
./qsdmminer --self-test
# self-test: solved in N attempts, proof_id=<hex>…
# self-test OK: proof solved and verified end-to-end via pkg/mining
```

### A.3 Run against a local v1 devnet

```bash
./qsdmminer \
  --validator=http://127.0.0.1:8080 \
  --address=qsdm1YOURADDR \
  --batch-count=1 \
  --poll=2s
```

The binary's startup preflight calls `/api/v1/status`. A v1 devnet
will respond with `"fork_v2_active": false` and the miner proceeds.
A v2 validator (e.g. `api.qsdm.tech`) will respond with
`"fork_v2_active": true` and the miner refuses to enter the loop:

```text
[preflight] REFUSING TO MINE: validator QSDM · CELL reports the v2 NVIDIA-locked
fork is ACTIVE at tip=41648. v1 proofs are rejected at the verifier with
ReasonBadVersion. …
```

If you genuinely need to fire v1 proofs at a v2 validator (the only
legitimate case is a forensic test of the rejection path), pass
`--allow-v1`:

```bash
./qsdmminer --validator=https://api.qsdm.tech --address=…  --allow-v1
# [preflight] WARNING: --allow-v1 override set. Continuing with v1 anyway.
# All submitted proofs WILL be rejected.
```

### A.4 Why v1 isn't shipped as a release binary

The release-container.yml workflow stopped shipping `qsdmminer-*` as
public release assets in v0.3.2 to prevent operators from accidentally
downloading a guaranteed-reject binary off the GitHub release page.
See the workflow's comment block for the rationale.

---

## Appendix B. Enrollment-funding status

This appendix is an honest snapshot of the funding surface for v0.3.2
mainnet (`api.qsdm.tech`). It will be revised as the picture changes.

### B.1 What the chain currently does

- **Block emission.** A fresh `EmissionSchedule.BlockRewardDust(h)`
  CELL is credited to the winning miner address on every block that
  contains an accepted v2 proof. The schedule is the canonical 90 M
  CELL cap with 4-year halvings (see `pkg/chain/emission`).
- **System funder.** `internal/blockdriver` seeds the internal
  `FunderAddress = "qsdm-system-funder"` account with
  `1e15` dust (= 10,000,000 CELL) at validator startup. This account
  is the source the block driver pays miners *from*; it is not a
  human-controllable address and never grants balance to a
  `qsdm1*` wallet on its own.
- **`/api/v1/wallet/mint` — REMOVED in v0.3.3 (returns 410 Gone).**
  In v0.3.2 and earlier this public endpoint accepted
  `{recipient, amount}` POSTs, logged a `mint_*` transaction to
  storage, and returned HTTP 200 with `status:"minted"` — but
  **never credited the recipient's account balance**, because no
  code path connected the handler to the wallet service's
  `AddBalance` operation. A balance query on the recipient after
  a "successful" mint POST always returned zero.
  v0.3.3 (Session 91) replaced the handler with **HTTP 410 Gone**
  + a structured `migration` JSON block pointing callers to CELL
  peer-transfer routes. Secondary token minting exists only as early
  scaffolding and is not part of the public QSDM ecosystem strategy.
  The
  `qsdm_wallet_mint_total{result="gone"}` Prometheus counter
  surfaces any caller that still targets the removed endpoint.
- **`/api/v1/wallet/balance`.** Read-only, public, returns the
  current account balance as a `float64` CELL number. Used by the
  browser wallet's *Check balance* tab.

### B.2 Currently-shipped routes to 10 CELL

| Route | Status | Notes |
|-------|--------|-------|
| **Initial-operator allocation** | None on the live chain as of v0.3.2. | The genesis ceremony output for the v2-reset chain did not include a multi-operator allocation. Any CELL emitted to date has gone to the single validator-operator's miner address. |
| **Reward from your own v2 proofs** | Available *once enrolled* — but enrollment requires 10 CELL. | The bootstrap problem this appendix exists to flag. |
| **Transfer from an existing CELL holder (validator-signed)** | Available via `POST /api/v1/wallet/send` (requires JWT). | The validator signs the transfer from **its own wallet** (`pkg/wallet/wallet.go::CreateTransaction` always sets `Sender = ws.address`), so the JWT subject is metadata only. Fine for single-operator nodes; not a self-custody path. |
| **Transfer from an existing CELL holder (self-custody)** | Available via `POST /api/v1/wallet/submit-signed` (v0.4.0, **no JWT — the cryptographic identity is the envelope's `public_key`**). | The holder builds + ML-DSA-87-signs the envelope locally (browser wallet's *Send transaction* tab today; a `qsdmcli wallet sign-tx` subcommand is planned for v0.4.1 — meanwhile a CLI user can construct the canonical envelope JSON by hand and pipe it through `qsdmcli wallet sign --message-file -`), POSTs it to the validator, and the server verifies `sender == hex(sha256(public_key))` plus the canonical-payload signature before debiting. See [`V040_WALLET_SEND_DESIGN.md`](V040_WALLET_SEND_DESIGN.md) and audit row `api-06`. **Known v0.4.0 gaps:** no per-account nonce (cross-`tx_id` replay possible) + non-atomic debit; both close in v0.4.1 before incentivised-testnet exposure. |
| **Public bootstrap faucet** | **NOT YET SHIPPED** as of v0.3.3. | No faucet code lives in the repo today (verified by `grep -ri faucet QSDM/`). Tracked as a v0.4.1+ item (depends on `submit-signed` gaps closing first so a faucet can't be drained by replay). |

### B.3 Practical outcome for a fresh outside operator

If you are a brand-new operator with no pre-existing CELL holdings
and no relationship with an existing holder, the live mainnet
currently does not provide a path to participate. The honest answer
is one of:

1. **Wait for the faucet** (no committed ship date; depends on
   `submit-signed` replay-protection landing in v0.4.1 first).
2. **Coordinate with an existing holder** off-chain to receive 10
   CELL by transfer. v0.4.0+ lets the holder do this **without
   uploading their private key** — they open the browser wallet's
   *Send transaction* tab at `qsdm.tech/wallet/`, sign the envelope
   locally with their passphrase-encrypted keystore, and POST it
   to the validator. The validator verifies the ML-DSA-87
   signature against the envelope's own `public_key` and never
   sees the private key. This is what the v2 spec calls "social
   bootstrap" and it is the de-facto path for v0.3.x → v0.4.x.
3. **Run a local devnet** instead of the public mainnet — the v2
   verifier accepts whatever `FORK_V2_HEIGHT` you pin via
   `SetForkV2Height`, so you can stand up a private chain that
   gates v2 at a height you control while you wait. The same
   `qsdmminer-console --protocol=v2` flow works against a local
   validator.

This is a known gap and the project's highest-priority operator-
funding item. If you read this and need a funding path, please file
the enrollment-funding issue — visibility on demand drives the
prioritisation.
