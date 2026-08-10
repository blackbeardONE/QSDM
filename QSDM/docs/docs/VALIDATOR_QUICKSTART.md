# Validator Quickstart — VPS operator runbook

This runbook takes a VPS operator from a fresh Ubuntu 22.04 host to a
running QSDM validator node in about 25 minutes. The validator is CPU-only,
participates in BFT + Proof-of-Entanglement consensus, and earns transaction
fees in Cell (`dust`-denominated).

> **Scope.** This is the runbook for the **validator** role only. It will
> not help you mine Cell. For mining, see `MINER_QUICKSTART.md` (Phase 4
> deliverable) and `NODE_ROLES.md §4.2`.
>
> **Standing up a SECOND validator on an existing host?** Use the
> self-contained
> [`../../deploy/bring-up-validator.sh`](../../deploy/bring-up-validator.sh)
> script instead — it scopes every port, user, systemd unit, and data dir
> to a `--index N` so two validators never collide. This document assumes
> you are installing the first validator on a fresh box.

---

## 0. Checklist before you start

- [ ] A Linux VPS (Ubuntu 22.04 LTS recommended; Debian 12 also works).
  Minimum: 4 vCPU, 8 GB RAM, 100 GB NVMe, static public IPv4.
- [ ] Root or passwordless-sudo access.
- [ ] A domain name (optional but strongly recommended; required for TLS
  termination in §5).
- [ ] Inbound TCP 4001 (libp2p), 8080 (API), 8081 (dashboard). Port 9000
  (log viewer) should NOT be exposed to the internet.
- [ ] You have read [`NODE_ROLES.md`](./NODE_ROLES.md) and understand that
  **validators do not mine**.

---

## 1. Install Docker

```bash
sudo apt-get update
sudo apt-get install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
  -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
echo \
  "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] \
  https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io
sudo systemctl enable --now docker
```

Verify:

```bash
docker run --rm hello-world
```

---

## 2. Pull the validator image

The validator image is the `validator_only` build (see
[`Dockerfile.validator`](../../Dockerfile.validator)). It cannot be made to
mine; the CUDA path is not linked in.

```bash
docker pull qsdm/validator:latest
docker image inspect qsdm/validator:latest \
  --format '{{.Config.Env}}' | tr ',' '\n'
```

You should see `QSDM_NODE_ROLE=validator` and `QSDM_MINING_ENABLED=false`
in the output.

> **Building from source (optional).** If you prefer to build the image
> yourself:
>
> ```bash
> git clone https://github.com/blackbeardONE/QSDM.git
> cd QSDM
> docker build -f QSDM/Dockerfile.validator -t qsdm/validator:local QSDM/
> ```

### Native release package

Each Core release also publishes production-capable native packages for
Linux amd64 and Windows amd64. The packages include SQLite support, embedded
build metadata, an unpacked-binary checksum, and conservative install/update
and rollback scripts. Download them from the matching GitHub release:

```text
qsdm-validator-<version>-linux-amd64.tar.gz
qsdm-validator-<version>-windows-amd64.zip
qsdm-home-gateway-linux-amd64
qsdm-home-gateway-windows-amd64.exe
```

Verify the archive against the release-level `SHA256SUMS` and Sigstore
certificate before unpacking. Then read the package `README.md`. The scripts
replace only the executable and preserve chain state, identity material,
wallets, configuration, and timestamped rollback binaries.

The separately signed home-gateway binary is optional. It provides the narrow
outbound tunnel used by a home validator without exposing the validator,
dashboard, wallet, or admin API directly.

### Updating the source-checkout home validator on Windows

Home-server checkouts use `scripts/update_local_validator.ps1` instead of the
administrator-only package installer. First perform a verification-only run:

```powershell
pwsh -NoProfile -File .\scripts\update_local_validator.ps1 `
  -PackageArchivePath C:\QSDM-Release\qsdm-validator-v0.4.7-rc.10-windows-amd64.zip `
  -ReleaseManifestPath C:\QSDM-Release\SHA256SUMS `
  -ReleaseManifestSignaturePath C:\QSDM-Release\SHA256SUMS.sig `
  -ReleaseManifestCertificatePath C:\QSDM-Release\SHA256SUMS.cert.pem `
  -ExpectedVersion v0.4.7-rc.10 `
  -ExpectedRevision REPLACE_WITH_RELEASE_REVISION `
  -CosignPath C:\Tools\cosign.exe `
  -VerifyOnly
```

Use patched Cosign `2.6.2+` or `3.0.4+`. The updater rejects older Cosign
versions. When verification succeeds, run the same command without
`-VerifyOnly`.

The update path is deliberately bounded:

- it verifies the Sigstore identity of the release-level `SHA256SUMS`, the
  archive hash, the package's inner binary hash, and embedded version/revision;
- it takes an exclusive update lock and stops only PIDs recorded by the
  managed validator and watchdog;
- it records the selected executable in `validator-active.json`, so file
  timestamps cannot silently select another binary;
- it preserves chain state, node identity, configuration, wallets, and task
  state;
- it gives every process and health check a deadline;
- it restores the previous verified binary automatically if readiness, node
  identity, chain height, peers, task actions, executable path, or checksum do
  not match;
- it restarts the watchdog directly and does not query, recreate, or start a
  Windows scheduled task during the update.

The latest machine-readable outcome is stored at
`source\.cache\local-validator\update-results\latest.json`. Timestamped
rollback evidence is under `source\.cache\local-validator\release-backups`.
Use `-PruneExpiredBackups` only when the default five-backup and 30-day
retention rule is appropriate.

Scheduled-task installation and repair remain separate operator actions in
`scripts/install_local_stack_task.ps1`. Keeping that work outside the binary
replacement transaction prevents a slow or unavailable Task Scheduler API
from leaving an update open indefinitely.

---

## 3. Create the host directories

```bash
sudo useradd -r -s /usr/sbin/nologin qsdm || true
sudo mkdir -p /var/lib/qsdm/{data,logs,config}
sudo chown -R qsdm:qsdm /var/lib/qsdm
```

---

## 4. Minimal validator configuration

Create `/var/lib/qsdm/config/config.toml`:

```toml
# QSDM validator config — see pkg/config for the full field reference.

[node]
role            = "validator"
mining_enabled  = false

[api]
port              = 8080
enable_tls        = false   # we terminate TLS at Caddy/nginx in §5
rate_limit_max    = 120
rate_limit_window = 60

[dashboard]
port = 8081

[network]
port            = 4001
bootstrap_peers = [
  # Replace with the official bootstrap list published at
  # https://qsdm.tech/validators once mainnet is live. For testnets, point
  # these at the genesis validator(s) you coordinated with.
]

[storage]
type        = "sqlite"
sqlite_path = "/app/data/qsdm.db"

[consensus]
# Every upgraded validator signs immediately. Keep enforcement off until all
# validators agree on one future activation height.
require_signed_votes = false
signed_message_activation_height = 0
signer_key_path = "qsdm_consensus_signer.json"

[logging]
log_file  = "/app/logs/qsdm.log"
log_level = "info"
```

Run a config check:

```bash
docker run --rm -v /var/lib/qsdm/config:/app/config:ro qsdm/validator:latest \
  qsdm-validator --check-config --config /app/config/config.toml
```

You should see:

```
QSDM: Configuration loaded successfully
QSDM: Node role: validator (build profile: validator_only, mining_enabled=false)
```

If you see an error mentioning `roleguard`, re-read §1 of
[`NODE_ROLES.md`](./NODE_ROLES.md); it means your config tried to enable
mining in a validator binary, which is unsupported.

Back up /app/data/qsdm_consensus_signer.json after first start. It is a
validator-only hot key, not a CELL wallet. Follow
[SIGNED_CONSENSUS_ROLLOUT.md](./SIGNED_CONSENSUS_ROLLOUT.md) before enabling
signature enforcement.

---

## 5. Run the validator under systemd

Create `/etc/systemd/system/qsdm-validator.service`:

```ini
[Unit]
Description=QSDM validator
Requires=docker.service
After=docker.service network-online.target

[Service]
Restart=always
RestartSec=5
ExecStartPre=-/usr/bin/docker rm -f qsdm-validator
ExecStart=/usr/bin/docker run --rm --name qsdm-validator \
  -p 4001:4001 \
  -p 127.0.0.1:8080:8080 \
  -p 127.0.0.1:8081:8081 \
  -v /var/lib/qsdm/data:/app/data \
  -v /var/lib/qsdm/logs:/app/logs \
  -v /var/lib/qsdm/config:/app/config:ro \
  -e QSDM_NODE_ROLE=validator \
  -e QSDM_MINING_ENABLED=false \
  qsdm/validator:latest \
  qsdm-validator --config /app/config/config.toml
ExecStop=/usr/bin/docker stop qsdm-validator

[Install]
WantedBy=multi-user.target
```

Note the `127.0.0.1:` prefix on the API and dashboard ports — they are NOT
exposed to the internet directly. Terminate TLS in front (Caddy or nginx)
and proxy to those loopback ports.

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now qsdm-validator
sudo journalctl -u qsdm-validator -f
```

You should see `QSDM: Starting application...`, the config-load line, the
role-guard OK line, and then the listeners binding.

---

## 6. TLS and reverse proxy (Caddy example)

If your validator has the hostname `validator.example.com`, a minimal
Caddyfile is:

```
validator.example.com {
    encode zstd gzip

    # Public API
    handle /api/* {
        reverse_proxy 127.0.0.1:8080
    }

    # Operator dashboard — restrict to your own IPs.
    @operator {
        remote_ip 203.0.113.0/24
    }
    handle /dashboard/* {
        reverse_proxy @operator 127.0.0.1:8081
    }

    # Redirect everything else to qsdm.tech (optional).
    handle {
        redir https://qsdm.tech 302
    }
}
```

After Caddy is up, verify:

```bash
curl -s https://validator.example.com/api/v1/status | jq
```

You should get a JSON body with `node_role: "validator"`,
`coin.symbol: "CELL"`, `branding.name: "QSDM"`, `branding.legacy_name:
"QSDM"`, and a non-empty `version`.

---

## 7. Monitoring

- `/api/v1/health/live` — liveness (process is alive). Point your uptime
  checker here.
- `/api/v1/health/ready` — readiness (backend reachable).
- `/api/v1/status` — public metadata (node_role, coin, branding, uptime).
  Use this to assert in CI that your validator reports the correct role.
- `/api/metrics/prometheus` — full Prometheus scrape. Requires either a
  Bearer token OR the `X-QSDM-Metrics-Scrape-Secret` header (legacy
  `X-QSDM-Metrics-Scrape-Secret` still accepted during the deprecation
  window; see [`REBRAND_NOTES.md`](./REBRAND_NOTES.md)).

---

## 8. Key-hygiene rules

- The validator generates an ML-DSA-87 signing key on first boot and
  stores it under `/var/lib/qsdm/data`. Back this directory up (encrypted
  at rest) before exposing the validator to the public network. Loss of
  the key means loss of validator identity.
- Do NOT copy `/var/lib/qsdm/data` to a second host to "clone" a
  validator. Two validators with the same identity will both be slashed
  (double-sign) when slashing is enabled post-genesis.
- If you rotate keys, follow the rotation procedure that will be published
  in `VALIDATOR_KEY_ROTATION.md` (Phase 4 deliverable). Until that lands,
  treat the validator key as immutable.

---

## 9. What comes next

- Register your validator on the public directory
  (`qsdm.tech/validators`) once mainnet genesis is complete.
- Subscribe to `https://qsdm.tech/security-advisories` for patch
  announcements.
- Keep the host's `liboqs` inside the container; never `apt install
  liboqs-dev` on the host — the container ships its own pinned build.
- When the optional NVIDIA NGC attestation story ships (Phase 5),
  validators may OPT IN to publish attestations for transparency. NGC
  attestation is **never** required for consensus and never influences
  your ability to propose blocks.
