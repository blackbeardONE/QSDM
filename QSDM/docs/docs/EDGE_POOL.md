# QSDM Hive Mother Mode: Agents and Relay

QSDM pooled edge compute uses an explicit path:

```text
Agent computers -> QSDM Relay -> QSDM Hive (Mother Hive role) -> QSDM Core
```

## Topology

- **Agents** are walletless, outbound-only workers that execute fixed QSDM CPU, NVIDIA GPU, or RAM jobs.
- **Relay** authenticates agents, applies resource ceilings, verifies results, stores receipts, and signs aggregate proofs.
- **QSDM Hive** is the only desktop client and owns the active CELL wallet. **Mother Hive** is the role it assumes while coordinating a paired Relay.
- Relay and QSDM Hive may run on the same computer or separate trusted machines. Edge Control is a setup utility, not another client.

The target gross workload-revenue split is 70% to Agent contributors, 15% to the Mother Hive operator, and 15% to the CELL ecosystem reserve. The ecosystem share requires a dedicated public pooled-compute reserve wallet; no such address is configured on QSDM Core yet. Automatic settlement is disabled until Agents bind payout wallets, Relay proofs are chain-verifiable, the ecosystem reserve address is published, and workload escrow is funded.

## Security boundaries

The agent is not a remote administration tool.

- No remote shell, arbitrary command, script, file-browser, or executable-download endpoint exists.
- Agents and the QSDM Hive Mother role use different 256-bit HMAC credentials. Agent credentials cannot read Mother-role proofs, and the Hive credential cannot register as an Agent.
- Requests use HMAC-SHA256, timestamps, single-use nonces, signed short-lived jobs, and bounded bodies. Aggregate receipt proofs are signed for QSDM Hive.
- CPU iterations, GPU operations, RAM MiB, runtime, worker count, and proof receipt count have hard limits.
- RAM buffers are cleared after each job.
- A non-loopback Relay listener requires `--allow-lan`.
- Keep both role-specific tokens private. Restrict TCP 7740 to the trusted private subnet.

This protocol is intended for a trusted laboratory. Do not expose the Relay as an anonymous public worker enrollment service.

## Relay resource policy

`--cpu-units`, `--gpu-units`, and `--ram-mib` define per-job ceilings. `--cpu-percent`, `--gpu-percent`, and `--ram-percent` apply 1-100% of those ceilings before an agent's own lower contribution limit is applied. Percentages limit QSDM work units; they are not operating-system utilization throttles. Agent polling also affects duty cycle.

## Downloads

QSDM Hive 1.3.87 and newer includes Edge Control 1.3.2, Agent 1.3.2, and the CUDA helper. Additional trusted computers can use:

- [Windows x86-64 Edge Control bundle](https://qsdm.tech/downloads/qsdm-edge-agent-1.3.2-windows-x86_64.zip)
- [Linux x86-64 Edge Control bundle](https://qsdm.tech/downloads/qsdm-edge-agent-1.3.2-linux-x86_64.tar.gz)
- [Edge Control checksums](https://qsdm.tech/downloads/qsdm-edge-agent-1.3.2-SHA256SUMS.txt)

## Edge Control GUI

Open `QSDM Edge Control.exe` on Windows or run `./qsdm-edge-control` on Linux. Choose **Relay** on the coordinating computer, set its private-network limits, and copy the generated Agent pairing code. On every worker computer, choose **Agent**, paste that code, select the resources to share, and press **Start Agent**.

Edge Control generates a separate Mother Hive pairing code for the coordinating Hive. Open the dedicated **Mother Hive** page in Hive and paste that code there. Hive stores the role credential with private file permissions, connects to a Relay on the same or another trusted computer, and displays Relay health, Agents, pooled resources, jobs, and receipts. Agent pairing codes are rejected. The Edge Control UI is local-only on `127.0.0.1:7741`; credentials are shown only on explicit request.

The remaining commands in this guide are the advanced automation path.

## Build

From the repository root on the Windows build workstation:

```powershell
pwsh -NoProfile -File QSDM/scripts/build_edge_pool.ps1
```

The script builds Windows and Linux x86-64 agents and the Windows CUDA helper. The CUDA helper requires CUDA 12.9 and Visual C++ build tools on the build workstation.

Linux Hive packages must be built on Linux so executable modes and the Electron runtime are preserved. The `QSDM Hive Linux` GitHub Actions workflow builds and verifies the AppImage and tarball natively; Windows cross-built Linux archives are not release artifacts.

## Relay and QSDM Hive setup

```powershell
qsdm-edge-agent.exe token --out agent.token
qsdm-edge-agent.exe token --out mother-hive.token
qsdm-edge-agent.exe relay `
  --listen 0.0.0.0:7740 `
  --allow-lan `
  --agent-token-file agent.token `
  --mother-token-file mother-hive.token `
  --cpu-percent 50 `
  --gpu-percent 40 `
  --ram-percent 25
```

Copy only `agent.token` to Agents. Copy only `mother-hive.token` to the QSDM Hive computer at `%APPDATA%\QSDM\edge-pool\mother-hive.token` on Windows or `$HOME/.config/QSDM/edge-pool/mother-hive.token` on Linux. The internal filename remains unchanged for compatibility. For a remote Relay, set `QSDM_EDGE_RELAY_URL` and `QSDM_EDGE_RELAY_TOKEN_FILE` for Hive.

If a Relay credential is configured, the QSDM Hive Mother role fails closed when the Relay is unavailable or has no verified receipt. It does not substitute local work for pooled work.

On Linux, install the Relay as a supervised user service:

```bash
./qsdm-edge-agent install-relay-service \
  --listen 0.0.0.0:7740 \
  --allow-lan \
  --agent-token-file "$HOME/.config/QSDM/edge-pool/agent.token" \
  --mother-token-file "$HOME/.config/QSDM/edge-pool/mother-hive.token" \
  --cpu-percent 50 --gpu-percent 40 --ram-percent 25
./qsdm-edge-agent relay-service-status
```

Restrict TCP 7740 to the private laboratory subnet. Existing coordinator receipt journals remain in place during migration.

## Agent setup

```powershell
qsdm-edge-agent.exe configure-agent `
  --out qsdm-edge-agent.json `
  --relay http://RELAY-HOST:7740 `
  --token-file C:\ProgramData\QSDM\agent.token `
  --worker-id lab-pc-02 `
  --resources cpu,ram `
  --ram-mib 256

qsdm-edge-agent.exe agent --config qsdm-edge-agent.json --silent --background
```

On Linux, use the supervised user service for normal deployment:

```bash
./qsdm-edge-agent install-service --config ./qsdm-edge-agent.json
./qsdm-edge-agent service-status
journalctl --user -u qsdm-edge-agent.service -f
```

The service restarts after failures and the agent automatically re-registers if the Relay restarts. Completion delivery is idempotent: a retried result returns the same durable receipt and cannot increase aggregate units twice. Enabling `loginctl enable-linger "$USER"` is optional and may require approval from the Linux administrator.

For NVIDIA GPU sharing, include `gpu` and configure the packaged helper:

```powershell
qsdm-edge-agent.exe configure-agent `
  --out qsdm-edge-agent.json `
  --relay http://RELAY-HOST:7740 `
  --token-file C:\ProgramData\QSDM\agent.token `
  --worker-id lab-pc-02 `
  --resources cpu,ram,gpu `
  --ram-mib 256 `
  --gpu-helper C:\Program Files\QSDM\qsdm-edge-gpu-helper.exe
```

The current helper requires NVIDIA Turing or newer, CUDA compute capability 7.5+, and a working NVIDIA driver.

## Status

```powershell
qsdm-edge-agent.exe status `
  --relay http://RELAY-HOST:7740 `
  --mother-token-file mother-hive.token
```

The output lists workers, receipts, QSDM Hive Mother-role last-seen time, and the enforced Relay resource policy.

## Compatibility

`coordinator`, `--coordinator`, and the coordinator service commands remain legacy aliases. A single `--token-file` also remains available for migration, but new deployments should use the Relay names and separate credentials.
