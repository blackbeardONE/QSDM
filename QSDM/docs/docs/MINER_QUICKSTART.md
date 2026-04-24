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
> [`nvidia_locked_qsdmplus_blockchain_architecture.md`](../../../nvidia_locked_qsdmplus_blockchain_architecture.md):
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
