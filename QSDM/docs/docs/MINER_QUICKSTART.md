# MINER_QUICKSTART — CPU reference miner for QSDM / Cell

> **Status:** Two CPU miner binaries ship in-tree today — `qsdmminer` (audit-clean single-file reference) and `qsdmminer-console` (friendly console UI with a setup wizard, live stats panel, and config persistence). Both run on CPU, single-threaded, intentionally slow. The CUDA production miner ships under `pkg/mining/cuda` after the Major Update §11 Phase 6 external security audit. Mainnet Cell emission is **gated** on that audit completing; this quickstart is for testnet participation and protocol validation.

This document walks a home operator through:

1. Installing either miner binary (the friendly console front-end is recommended for first-time users; the reference binary is the protocol-truth).
2. Self-testing it in 10 seconds to confirm the install works end-to-end.
3. Pointing it at a live QSDM validator and submitting real proofs.

It assumes you have already read `NODE_ROLES.md` and `MINING_PROTOCOL.md`. If you are running a validator, read `VALIDATOR_QUICKSTART.md` instead — mining and validation are separate binaries on separate machines.

If you just want to mine with zero flag-memorisation, skip to [§2.5 Friendly console miner](#25-friendly-console-miner-recommended-for-home-operators).

---

## 1. Requirements

- Any 64-bit desktop/laptop with **Go 1.25+** (for building from source). No CGO required.
- A QSDM reward address you control (`--address` flag value).
- Network access to a validator HTTP endpoint you trust. For testnet this is typically `https://testnet.qsdm.tech` or an IP supplied by the testnet coordinator.
- Disk: the reference miner keeps one 2 GiB DAG per active mining epoch in RAM (see `MINING_PROTOCOL.md §3.3`). Plan for ~3 GiB free memory during normal operation.

The reference miner does **not** require a GPU. It runs on CPU at a handful of hashes per second — this is expected. It will not find mainnet blocks at this speed; it will find testnet blocks under easy difficulty, and it will prove the end-to-end pipeline works.

## 2. Install

### 2.1 From source

```bash
git clone https://github.com/blackbeardONE/QSDM.git
cd QSDM/source
go build -o qsdmminer ./cmd/qsdmminer
```

The binary is pure Go; no CGO, no liboqs, no CUDA. It cross-compiles trivially:

```bash
# Linux amd64 binary from a Windows/macOS build host:
GOOS=linux GOARCH=amd64 go build -o qsdmminer-linux-amd64 ./cmd/qsdmminer

# Windows amd64 binary from a Linux build host:
GOOS=windows GOARCH=amd64 go build -o qsdmminer.exe ./cmd/qsdmminer
```

### 2.2 Verify the build — self-test

```bash
./qsdmminer --self-test
```

Expected output in under 10 seconds on a laptop:

```
self-test: solved in N attempts, proof_id=<hex>…
self-test OK: proof solved and verified end-to-end via pkg/mining
```

The `--self-test` flag builds a synthetic 4-batch work-set and a small in-memory DAG, solves a proof under easy difficulty, then verifies it against the in-process `pkg/mining` verifier. If it passes, your binary is protocol-conformant. If it fails, **do not continue** — open an issue with the exit code and stderr.

This self-test is also the Phase 4.5 acceptance gate in `Major Update.md`; it runs unchanged in CI.

## 2.5 Friendly console miner (testnet / reference only)

> ⚠️ **Deprecation notice — NVIDIA-lock in progress.** The project is
> pivoting to the protocol described in
> [`nvidia_locked_qsdm_blockchain_architecture.md`](../../../nvidia_locked_qsdm_blockchain_architecture.md):
> once the `v2` protocol activates, CPU-only miners will no longer produce
> proofs that mainnet validators accept. The `qsdmminer-console` binary is
> kept for testnet replay and for algorithmic reference, but **do not
> expect it to earn mainnet Cell rewards after the hard fork.** The
> previous "one-command install" (`curl | bash`, PowerShell `iwr | iex`,
> `ghcr.io/.../qsdm-miner-console`) has been withdrawn as part of that
> pivot.

If you are running a testnet node and want a friendlier CLI than `qsdmminer`, build **`qsdmminer-console`** from source. It shares the same protocol code as `qsdmminer` (identical `pkg/mining` primitives, identical on-wire behaviour), and adds three ergonomic differences:

1. **First-run setup wizard.** Run the binary with no flags and it prompts for `Validator URL`, `Reward address`, `Batch count per proof`, and `Poll interval`. Answers are saved to `~/.qsdm/miner.toml` (on Windows: `%USERPROFILE%\.qsdm\miner.toml`) so future runs need no flags.
2. **Live console panel.** An in-place ASCII/ANSI panel shows the reward address, validator, current epoch, rolling hashrate, accepted/rejected counts, uptime, and the last event. Pipe stdout to a file or TTY-less shell and the binary auto-detects the missing terminal and falls back to a one-line-per-event log.
3. **`--plain`** flag for `systemd` / CI / `journalctl` users who want log lines, not a panel, without depending on `isatty` detection.

### Build from source

```bash
cd QSDM/source
go build -o qsdmminer-console ./cmd/qsdmminer-console
```

Run the setup wizard and keep mining after the save:

```bash
./qsdmminer-console
# wizard prompts, then enters the live panel
```

Re-run the wizard to change settings later:

```bash
./qsdmminer-console --setup
# saves ~/.qsdm/miner.toml then exits
./qsdmminer-console          # resume with new settings
```

Typical wizard session:

```
QSDM — setting up Cell console miner
Answers are saved to /home/alice/.qsdm/miner.toml
Press Enter to accept the [default] shown in brackets.

  Validator URL [https://testnet.qsdm.tech]: 
  Reward address (CELL): qsdm1YOURADDR
  Batch count per proof [1]: 
  Poll interval (e.g. 2s, 500ms) [2s]: 

Saved /home/alice/.qsdm/miner.toml
```

Once it drops into the panel you'll see something like:

```
  QSDM miner console · protocol v1
  ─────────────────────────────────────────────
  Reward address   qsdm1YOU…ADDR
  Validator        https://testnet.qsdm.tech  [connected]

  Epoch            3  (DAG ready · N=67108864)

  Hashrate         4.83  H/s
  Proofs           0 accepted, 0 rejected
  Uptime           00:02:14

  Last event       work received: height=181442 (3s ago)

  Ctrl-C to stop. Config: /home/alice/.qsdm/miner.toml
```

Flags override the saved config for one run without rewriting the file:

```bash
./qsdmminer-console --validator=https://other.example.com
./qsdmminer-console --plain                         # log mode
./qsdmminer-console --self-test                     # same gate as qsdmminer
./qsdmminer-console --config /etc/qsdm/miner.toml   # custom config path
./qsdmminer-console --version                       # print release tag, git SHA, build date
```

### Identifying your binary when filing a bug

Every release artefact (`qsdmminer`, `qsdmminer-console`, `trustcheck`,
`genesis-ceremony`) accepts `--version` and emits a single line of the
form:

```text
qsdmminer-console v0.1.0 (abc1234, 2026-04-22T10:00:00Z, go1.25.9, linux/amd64)
```

For binaries built yourself (`go build` without release-time `-ldflags`)
the tag shows as `dev` and the SHA / build date as `unknown`. That is
expected — the release pipeline is the only thing that injects those
values. Always include the `--version` line when filing a miner bug.

The console miner is the **recommended starting point** for home operators. The single-file `qsdmminer` binary (§2 above) is the protocol-truth reference and remains the preferred choice for conformance testing and CI — it is intentionally read-only-the-spec minimal with no UX layered on top.

## 3. Connect to a validator

### 3.1 Discover a validator URL

Pick one of:

- The testnet coordinator's announced URL (check `qsdm.tech` / testnet forum).
- A peer you trust. Any QSDM node advertising `/api/v1/status` with `node_role` of `validator` or `both` accepts mining traffic.
- Your own validator on localhost if you run both roles.

Confirm liveness:

```bash
curl -s https://<validator>/api/v1/status | jq '.node_role, .network'
# -> "validator"
# -> "testnet"  (or "mainnet" once that exists and is audited)
```

Also confirm the validator exposes the mining endpoints:

```bash
curl -s -o /dev/null -w "%{http_code}\n" https://<validator>/api/v1/mining/work
# 200 if the validator has mining enabled; 503 if it hasn't installed a
# MiningService yet — in which case you need a different validator or
# wait for testnet staging to finish.
```

### 3.2 Run the miner

```bash
./qsdmminer \
  --validator=https://<validator> \
  --address=qsdm1<your-reward-address> \
  --batch-count=1 \
  --poll=2s
```

- `--validator`: base URL. HTTP or HTTPS both work; HTTPS is strongly encouraged on the public internet.
- `--address`: the QSDM address that receives the Cell reward when your proof is accepted and the block is finalised.
- `--batch-count`: number of work-set batches your proof claims to have validated. Start at `1` (matches the protocol minimum). The server clamps to its `batch_count_maximum`; the miner logs a message if your flag exceeds that.
- `--poll`: how often the miner refetches `/api/v1/mining/work` after each solve / on transient errors. Lower = fresher header hashes, more HTTP traffic.
- `--progress=true` (default): prints hashrate to stderr every 10 s.

Example session:

```
QSDM: miner starting: validator=https://testnet.qsdm.tech address=qsdm1… batch_count=1 GOMAXPROCS=8
QSDM: new mining epoch 3 (building DAG, N=67108864)
QSDM: DAG built in 42.1s
QSDM: hashrate: 4.83 H/s (48 attempts total)
…
QSDM: proof ACCEPTED height=181442 epoch=3 attempts=3841 id=a13f9b…
```

Under the reference-CPU hashrate a solved share may take many hours on mainnet-comparable difficulty. Testnet difficulty is configured much lower. If the miner never announces an ACCEPTED proof within your expected window:

1. Check the validator is producing blocks at all (`/api/v1/status` → `chain_tip` should advance).
2. Check rejection reasons in stderr. The most common first-run rejections are:
   - `wrong-epoch` or `non-canonical`: your clock is out of sync or a build mismatch; rebuild from a clean tree.
   - `header-mismatch`: you are racing a recently-finalised block. The miner retries automatically; this is normal.
   - `too-late`: you solved the proof but the 6-block grace window (`MINING_PROTOCOL.md §9`) elapsed first. Consider `--poll` lower, or a faster validator round-trip.

### 3.3 Running unattended (Linux systemd)

Save as `/etc/systemd/system/qsdmminer.service`:

```ini
[Unit]
Description=QSDM CPU reference miner
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=qsdm
ExecStart=/usr/local/bin/qsdmminer \
  --validator=https://testnet.qsdm.tech \
  --address=qsdm1YOURADDR \
  --batch-count=1
Restart=on-failure
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Then:

```bash
sudo useradd --system --shell /usr/sbin/nologin qsdm || true
sudo cp qsdmminer /usr/local/bin/qsdmminer
sudo systemctl daemon-reload
sudo systemctl enable --now qsdmminer
journalctl -u qsdmminer -f
```

### 3.4 Running unattended (Windows Task Scheduler or NSSM)

Use NSSM or the built-in Task Scheduler. The binary is a plain console process; log files get the same content `journalctl` would capture.

## 4. What you will NOT get from the reference miner

- **Competitive hashrate.** One CPU core at a few H/s. Use `pkg/mining/cuda` (Phase 6) once audited.
- **Multi-GPU support.** Not applicable to CPU.
- **Pool support.** Solo mining only. Pool clients talk to this protocol the same way, but no pool exists yet.
- **Autostart on Cell halvings.** The reward drops automatically via the emission schedule (`pkg/chain/emission`); the miner does not special-case halving boundaries.

## 5. Security & privacy posture

- The miner sends your reward address over every `/api/v1/mining/submit` call. Treat that address as public; it is.
- The miner **does not** send NGC attestation unless you explicitly extend it to do so. That is a Phase 6 post-audit feature; reference miners are un-attested.
- The miner performs no measurement of your host hardware, network, or clock beyond what is needed to solve a proof. It does not phone home.
- If you run the miner against a validator you do not control, treat the validator like any other untrusted RPC endpoint: it can censor your proofs and lie about difficulty. Cross-check `/api/v1/status` on at least two independent validators if you care about the latter.

## 5b. v2 mining lifecycle — `qsdmcli` subcommands

Once the v2 protocol activates, mining requires an on-chain enrollment record (NodeID + GPU UUID + bonded stake). The four lifecycle operations — enroll, unenroll, slash, and read — are surfaced as `qsdmcli` subcommands so operators do not need to hand-build canonical payloads or remember endpoint paths.

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

  Sample human output:

  ```
  2026-04-29T13:21:07Z ARCHSPOOF_REJECTION         seq=42  reason=cc_subject_mismatch  arch=ada  miner=qsdm1critical  height=9000  cert_cn=NVIDIA H100 80GB  detail=leaf cn contradicts claimed gpu_arch
  ```

  Sample JSON-Lines output:

  ```json
  {"timestamp":"2026-04-29T13:21:07Z","event":"archspoof_rejection","seq":42,"reason":"cc_subject_mismatch","arch":"ada","height":9000,"miner_addr":"qsdm1critical","cert_subject":"NVIDIA H100 80GB","detail":"leaf cn contradicts claimed gpu_arch"}
  ```

  `--detailed` requires a v2-aware validator with the recent-rejections store wired (every node bootstrapped via `internal/v2wiring.Wire()` qualifies). Older nodes return `503 Service Unavailable` from the endpoint and the watcher exits with a clear message hinting to drop `--detailed` for counter mode.

- **Counter mode is still useful.** The default mode (no `--detailed`) is the right choice for steady-state alerting and dashboards: it never falls behind even if the ring overflows, and it's the same data Prometheus alert rules trigger on. `--detailed` is the right choice for incident response and forensic correlation — pair the two on adjacent terminal panes.

- **Cadence floor.** `--interval` is clamped to ≥ 5 seconds, same as the other watchers.

### Tooling notes

- All three write subcommands accept `--id` for an idempotent client-supplied tx id; if omitted, `qsdmcli` generates a 16-byte random hex id.
- `--fee` defaults to `0.001 CELL` and must be `> 0` to clear the slashing admission gate.
- The CLI does not sign envelopes today (matching the existing `qsdmcli tx` shape); the validator-side `AccountStore` identifies sender by string and enforces nonce ordering. When Dilithium-signed envelopes land (per [`MINING_PROTOCOL_V2.md §13`](./MINING_PROTOCOL_V2.md#13-historical-decision-record) and the wallet roadmap), `qsdmcli` will gain a single signing call inside `buildEnvelope()` — no flag changes.

## 5c. Mining v2 from the console miner

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
| `self-test FAILED: solve: context deadline exceeded` | `--self-test-difficulty` is too high for this CPU. | Re-run with `--self-test-difficulty=2` (default). |
| Every submit returns `reject_reason=wrong-epoch` | Your binary and the validator disagree on `BlocksPerEpoch`. | Confirm both sides are on the same QSDM release tag; rebuild. |
| Every submit returns `reject_reason=batch-root` | Your locally-canonicalised work-set differs from the validator's. | File an issue — canonicalisation divergence is a protocol bug. |
| `fetch work: status 503` loops forever | The validator does not have mining wired up yet. | Point `--validator` at a different node, or wait for testnet staging. |
| Miner crashes on startup with OOM | DAG size > host RAM. | Currently only the production DAG size is supported; add more RAM or wait for the `--dag-size-override` flag (tracked post-audit). |

## 7. Reporting bugs

The reference miner is the **protocol truth** implementation. Any disagreement between it and a validator is a protocol issue, not a miner-configuration issue. Please file bugs at the QSDM repository with:

1. Output of `qsdmminer --version` (or `qsdmminer-console --version`) — one line carrying the release tag, short git SHA, build date, Go toolchain, and OS/arch.
2. Validator URL (may be redacted).
3. Relevant `journalctl` / stderr extract including the failed proof and the server's reject reason.
4. Whether `qsdmminer --self-test` still passes on the same binary.

Cross-reference: `MINING_PROTOCOL.md §12` conformance checklist. Any checklist item not met by your observation is automatically a bug in one of: this miner, the validator, or the protocol doc itself.
