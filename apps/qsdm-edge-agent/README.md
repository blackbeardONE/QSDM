# QSDM Agent and Relay for QSDM Hive

`qsdm-edge-agent` pools bounded CPU, NVIDIA GPU, and RAM work from trusted computers:

```text
Agent computers -> QSDM Relay -> QSDM Hive (Mother Hive role) -> QSDM Core
```

QSDM Hive is the only desktop client. Agents are outbound-only and walletless, while Edge Control is only a local setup utility. The Relay enforces resource ceilings, verifies fixed QSDM jobs, stores receipts, and reports aggregate proofs. Mother Hive is the role assumed by the active QSDM Hive that owns the CELL wallet.

## Security model

- Agents and the QSDM Hive Mother role use separate 256-bit HMAC credentials. Neither credential can impersonate the other role.
- Requests carry a timestamp and single-use nonce. Relay jobs are signed and expire after a short lease.
- Agents execute only built-in CPU, RAM, or CUDA algorithms. There is no remote shell, script runner, file browser, or arbitrary command endpoint.
- CPU units, RAM MiB, GPU units, request sizes, job time, and worker counts are capped.
- The Relay persists verified receipts and aggregates them into a resource proof for the QSDM Hive Mother role.
- The default Relay listener is localhost. A LAN listener requires explicit `--allow-lan`.
- Keep both token files private and allow TCP 7740 only on the private network firewall profile.

This is not a public anonymous enrollment mechanism. Do not expose the Relay directly to the internet.

## Downloads

- Windows x86-64 bundle: `https://qsdm.tech/downloads/qsdm-edge-agent-1.3.4-windows-x86_64.zip`
- Linux x86-64 bundle: `https://qsdm.tech/downloads/qsdm-edge-agent-1.3.4-linux-x86_64.tar.gz`
- Checksums: `https://qsdm.tech/downloads/qsdm-edge-agent-1.3.4-SHA256SUMS.txt`

QSDM Hive 1.3.91 and newer bundles Edge Control 1.3.4, Agent 1.3.4, and the CUDA helper. Standalone bundles are for additional laboratory computers.

## Edge Control GUI

Windows users open `QSDM Edge Control.exe`. Linux users run `./qsdm-edge-control`. The local control window provides:

- Agent and Relay role selection.
- One-field Agent pairing codes instead of manual token paths.
- CPU, NVIDIA GPU, and RAM percentage controls.
- Connected-computer, receipt, and recent-activity status.
- One-click QSDM Hive Mother-role configuration on the Relay computer.
- Start-at-sign-in support without administrator rights.

The control window binds only to `127.0.0.1:7741`, uses a private session token, and never exposes its API to the LAN. The CLI below remains available for automation and advanced operators.

## Relay and QSDM Hive

Generate separate credentials and tune the Relay work ceilings:

```powershell
qsdm-edge-agent.exe token --out agent.token
qsdm-edge-agent.exe token --out mother-hive.token
qsdm-edge-agent.exe relay --listen 0.0.0.0:7740 --allow-lan `
  --agent-token-file agent.token --mother-token-file mother-hive.token `
  --cpu-percent 50 --gpu-percent 40 --ram-percent 25
```

Restrict inbound TCP 7740 to the laboratory subnet. Percentages scale QSDM work units, not operating-system utilization. An agent's lower contribution limit always wins.

Copy only `mother-hive.token` to the computer running QSDM Hive at `%APPDATA%\QSDM\edge-pool\mother-hive.token` or `$HOME/.config/QSDM/edge-pool/mother-hive.token`. The filename is retained for protocol compatibility. For a remote Relay, set `QSDM_EDGE_RELAY_URL` and `QSDM_EDGE_RELAY_TOKEN_FILE`.

Linux computer A uses the same protocol:

```bash
chmod 0755 qsdm-edge-agent qsdm-edge-gpu-helper
./qsdm-edge-agent token --out "$HOME/.config/QSDM/edge-pool/agent.token"
./qsdm-edge-agent token --out "$HOME/.config/QSDM/edge-pool/mother-hive.token"
./qsdm-edge-agent relay \
  --listen 0.0.0.0:7740 \
  --allow-lan \
  --agent-token-file "$HOME/.config/QSDM/edge-pool/agent.token" \
  --mother-token-file "$HOME/.config/QSDM/edge-pool/mother-hive.token"
```

For persistent Linux operation, install the Relay as a supervised user service:

```bash
./qsdm-edge-agent install-relay-service \
  --listen 0.0.0.0:7740 \
  --allow-lan \
  --agent-token-file "$HOME/.config/QSDM/edge-pool/agent.token" \
  --mother-token-file "$HOME/.config/QSDM/edge-pool/mother-hive.token"
./qsdm-edge-agent relay-service-status
```

The service preserves the existing receipt journal below `$HOME/.config/QSDM/edge-pool/coordinator`. Restrict inbound TCP 7740 to the trusted laboratory subnet.

## Agent computers

Copy the agent binary and token file to each trusted computer. Give every computer a unique worker ID:

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

For NVIDIA GPU sharing, add `gpu` and the packaged helper path:

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

The current CUDA helper requires NVIDIA Turing or newer, compute capability 7.5+, and a working NVIDIA driver. GPU Edge Worker is shared compute and is separate from QSDM protocol mining.

`--background` detaches the process without a visible console window. `--silent` writes only to the log file. Agent 1.3.4 re-registers after a Relay restart and retries a completed result until its exactly-once receipt is acknowledged.

Linux agents can run silently in the same way:

```bash
./qsdm-edge-agent configure-agent \
  --out qsdm-edge-agent.json \
  --relay http://RELAY-HOST:7740 \
  --token-file /etc/qsdm/agent.token \
  --worker-id lab-pc-02 \
  --resources cpu,ram,gpu \
  --ram-mib 256 \
  --gpu-helper ./qsdm-edge-gpu-helper

./qsdm-edge-agent agent --config qsdm-edge-agent.json --silent --background
```

For a persistent Linux deployment, install the configuration as a supervised user service instead of using `--background`:

```bash
./qsdm-edge-agent install-service --config ./qsdm-edge-agent.json
./qsdm-edge-agent service-status
journalctl --user -u qsdm-edge-agent.service -f
```

The installer copies the agent, token, configuration, and optional GPU helper into stable per-user locations, enables restart-on-failure, and starts the service. Run `loginctl enable-linger "$USER"` if your Linux administrator permits this user service to run before login. To remove only the service while retaining its private configuration and token, run `qsdm-edge-agent uninstall-service`.

## Status

```powershell
qsdm-edge-agent.exe status --relay http://RELAY-HOST:7740 --mother-token-file mother-hive.token
```

## Mother Hive application jobs

Hive 1.3.91 opens an authenticated, loopback-only Compute Gateway while the Mother Hive task is running. Applications can submit bounded work without receiving the Relay's Mother credential:

```powershell
qsdm-edge-agent.exe compute submit --request-id local-app-0001 --resource cpu --units 100000
qsdm-edge-agent.exe compute list
qsdm-edge-agent.exe compute status --id COMPUTE_JOB_ID
```

The default gateway is `http://127.0.0.1:7742`; the client discovers Hive's private application token automatically. Only the built-in CPU, GPU, and RAM algorithms are accepted. The pool is an explicit job API, not transparent operating-system RAM or a local CUDA device.

## CELL accounting

For each Core-accepted Relay batch, the gross workload revenue split is 70% to the contributor-owner wallet bound by Mother Hive, 15% to the Mother Hive operator, and 15% to the CELL ecosystem reserve. Agent PCs remain walletless. Payout requires a funded task reward pool, an authorized Relay ID, and chain-valid signed receipts; a verified receipt does not mint CELL by itself.

The old `coordinator` commands, `--coordinator` option, and one-token setup remain migration aliases.
