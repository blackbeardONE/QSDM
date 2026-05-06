# Stage B Deploy — Windows build → BLR1 Linux validator

> **Audience.** A single operator running the production
> validator on the BLR1 DigitalOcean VPS, who builds Go binaries
> on a Windows workstation. **Goal:** ship a non-CGO binary
> built from Stage B (commit `c2598d5` or later) so the live
> deploy stops firing `qsdm_stub_active{kind=~"dilithium|wallet|poe"}`.
>
> Not a generic Linux deploy guide. For the full operator
> handbook see [`OPERATOR_GUIDE.md`](OPERATOR_GUIDE.md). For
> Ubuntu-from-source see [`UBUNTU_DEPLOYMENT.md`](UBUNTU_DEPLOYMENT.md).

This deploy is a **single-binary swap with rollback**. The
QSDM tree is small, the `cmd/qsdm` binary is self-contained
(no shared libraries on the !cgo build path), and `systemctl`
on Ubuntu cleanly restarts the unit. Total operator time
end-to-end is ~5 minutes including the smoke check.

> **History — one-time `qsdmplus` → `qsdm` rebrand (2026-05-06).**
> Before 2026-05-06 the live deploy was an Apr-23 CGO+liboqs
> build under the legacy `qsdmplus.service` unit, user `qsdmplus`,
> at `/opt/qsdmplus/qsdmplus`. That binary predated the
> `qsdm_stub_active` / `qsdm_binary_capabilities` collectors,
> so no stub-active alerts were ever firing — but the deployed
> tree had drifted ~13 days behind `origin/main` with stale
> branding. On 2026-05-06 a one-time rebrand migration was run
> against BLR1 (snapshot at `/root/qsdmplus.snapshot.<ts>.tgz`,
> old unit + tree preserved disabled for emergency rollback);
> from that point on this runbook applies as-is. SQLite state
> was lost in the migration because the new non-CGO binary uses
> file-storage fallback (`SQLite requires CGO`); the validator
> had `transactions_processed: 0` for 13 days so this was a
> no-op in practice. New libp2p hostID:
> `12D3KooWJynBfebCrUZxqEQQ727trwdwgw9BhJsZkBE1eTdzCE5D`.
>
> **Pre-Stage-B baseline being replaced** *(by future deploys)*.
> The currently-live binary on `/opt/qsdm/qsdm` is built from
> Stage B at commit `bb89450` and reports
> `qsdm_binary_capabilities{dilithium="circl",mesh3d="cpu_fallback",wasm="wazero"} 1`.
> All seven `qsdm_stub_active{kind=…}` rows are pinned at `0`.
> Subsequent deploys following this runbook should preserve
> that state.

---

## 0. Prerequisites — verify once

On the **Windows build host** (this workstation):

```powershell
# Go toolchain version (Stage B was built on go1.22+):
& "C:\Program Files\Go\bin\go.exe" version

# Repo HEAD includes Stage B:
cd E:\Projects\QSDM+
git log --oneline -1 -- QSDM/source/pkg/crypto/dilithium_circl.go
# → c2598d5 (or later) feat: Stage B — retire dilithium/wallet/poe stubs ...
```

If `git log` does NOT show `c2598d5` or later, run `git pull`
first. If you've made local changes, the build below picks
them up — that's fine, just be aware.

On the **VPS (BLR1)**: nothing to install. systemd is already
managing `qsdm.service`; the binary at `/opt/qsdm/qsdm` will
be replaced in place.

---

## 1. Cross-compile on Windows (build host)

```powershell
cd E:\Projects\QSDM+\QSDM\source

# Cross-compile flags. Linux amd64 because the BLR1 droplet is
# x86_64 (Ubuntu 22.04). CGO disabled because we're targeting
# the pure-Go circl backend — no liboqs install dance on the
# VPS, no .so dependencies in the binary.
$env:CGO_ENABLED = "0"
$env:GOOS        = "linux"
$env:GOARCH      = "amd64"

# Strip the Windows path-version suffix from the output name
# so the file is unambiguously the post-deploy artefact.
go build -trimpath -ldflags="-s -w" -o qsdm-linux-amd64 ./cmd/qsdm

# Sanity: file should be ~40-60 MB, ELF, statically linked.
Get-Item .\qsdm-linux-amd64 | Format-List Name,Length,LastWriteTime
```

Optional: confirm the binary embeds the Stage B commit:

```powershell
# go's BuildSettings include the VCS hash with -buildvcs=true (default on).
go version -m .\qsdm-linux-amd64 | Select-String "vcs.revision"
```

The hash should match `git rev-parse HEAD` on this machine.

---

## 2. Stage the new binary on the VPS (don't activate yet)

```powershell
# Upload alongside the running binary, NOT on top of it.
# Naming it qsdm.new makes the rollback trivial in §5.
scp .\qsdm-linux-amd64 root@206.189.132.232:/opt/qsdm/qsdm.new
```

> **Authentication.** Uses the ed25519 key at `~/.ssh/id_ed25519`
> on this machine, which is in `/root/.ssh/authorized_keys` on
> BLR1. If the key prompt fails, the password fallback in
> `vps.txt` is for the DigitalOcean web console only — don't
> paste it into a terminal scp command.

SSH in and verify the upload:

```bash
ssh root@206.189.132.232
ls -lah /opt/qsdm/qsdm /opt/qsdm/qsdm.new
# Expect both files present. qsdm.new should be ~the size you
# saw in §1, with today's timestamp.
file /opt/qsdm/qsdm.new
# Expect: ELF 64-bit LSB executable, x86-64, statically linked
```

If the size is wildly different from the live binary
(e.g. <10 MB), **stop**: the `go build` produced a stub binary,
likely because `GOOS`/`GOARCH` weren't set. Repeat §1.

---

## 3. The swap (atomic, ~3 seconds of downtime)

Still in the SSH session:

```bash
# Capture the live binary as the rollback artefact. Resist the
# urge to trust an older /opt/qsdm/qsdm.bak from a previous
# deploy — rebuild this snapshot every time.
cp -p /opt/qsdm/qsdm /opt/qsdm/qsdm.prev

# Stop the unit. systemd waits for SIGTERM cleanup; on this
# binary the WAL flush + libp2p disconnect take <2s.
systemctl stop qsdm
systemctl status qsdm --no-pager | head -5
# Expect: Active: inactive (dead)

# Atomic swap. mv is atomic on the same filesystem; no
# half-replaced state is ever visible to systemd.
mv /opt/qsdm/qsdm.new /opt/qsdm/qsdm
chmod +x /opt/qsdm/qsdm
chown root:root /opt/qsdm/qsdm

# Bring the unit back up.
systemctl start qsdm
sleep 3
systemctl status qsdm --no-pager | head -10
# Expect: Active: active (running)
```

If `systemctl start` fails or the unit goes to `failed` within
the next minute, jump straight to §5 (rollback). Don't try to
debug a broken validator on the live deploy — roll back first,
investigate after.

---

## 4. Smoke check — verify Stage B is actually live

The whole point of this deploy is that the three CRITICAL
stub-active alerts auto-resolve. Four quick checks confirm
that, in increasing order of confidence:

**4.0 The binary identity is Stage B (zero-latency check).**

```bash
# The metrics endpoint lives on the dashboard port (:8081),
# not the log viewer (:8080) and not the API server (:8443).
# Auth is by Bearer token = QSDM_DASHBOARD_METRICS_SCRAPE_SECRET
# (set in /etc/systemd/system/qsdm.service.d/secrets.conf).
SECRET=$(awk -F'=' '/QSDM_DASHBOARD_METRICS_SCRAPE_SECRET=/ && /^Environment="QSDM_DASH/ {gsub(/"$/,"",$3); print $3; exit}' /etc/systemd/system/qsdm.service.d/secrets.conf)
curl -sS -H "Authorization: Bearer $SECRET" \
     http://127.0.0.1:8081/api/metrics/prometheus \
  | grep -E '^qsdm_binary_capabilities\{'
```

Expected on a Stage B+ binary:

```text
qsdm_binary_capabilities{dilithium="circl",mesh3d="cpu_fallback",wasm="wazero"} 1
```

What each label tells you:

- `dilithium="circl"` — pure-Go ML-DSA-87 (Stage B). If you see
  `dilithium="liboqs"` you're on a CGO build, which is fine
  for a different deployment topology but **not** what this
  runbook deploys (BLR1 has no liboqs system package).
- `wasm="wazero"` — pure-Go WASMSDK (wasm Stage B).
- `mesh3d="cpu_fallback"` — expected on the BLR1 VPS (no
  NVIDIA hardware). `mesh3d="cuda"` is fine on a GPU host
  but indicates a different binary.

This metric flips on the first `/metrics` scrape (no `for: 5m`
delay). If any label is unexpected, **rollback now** (§5) —
you almost certainly redeployed a stale or wrong-tag binary.

**4.1 Process logs show the new build's circl init.**

```bash
journalctl -u qsdm --since "1 minute ago" --no-pager | head -40
```

Look for the v2-mining startup banner and the absence of any
`(CGO disabled, signature verification skipped)` message. The
historical stub printed that line on every transaction; Stage B
does not.

**4.2 The Prometheus stub-active gauges are all 0.**

```bash
# Same Bearer-auth scheme as §4.0. The endpoint lives on :8081
# (dashboard), not :8080 (log viewer) or :8443 (API server).
curl -sS -H "Authorization: Bearer $SECRET" \
     http://127.0.0.1:8081/api/metrics/prometheus \
  | grep -E '^qsdm_stub_active\{' \
  | sort
```

Expected output: every `qsdm_stub_active{kind="..."}` sample
ends with ` 0`. Four kinds are now structurally pinned at `0`
on any binary built from current head: `dilithium`, `wallet`,
`poe` (Stage B, commit `c2598d5`), and `wasm_sdk` (wasm Stage
B, on top of `c2598d5`). Two kinds may legitimately be `1` if
the corresponding feature isn't wired on this node — that's
fine and isn't part of this deploy's scope:

- `mesh3d_cuda` — CUDA acceleration unavailable; CPU mesh
  validator runs in its place. Expected on every non-NVIDIA
  build host.
- `cc` (nvidia-cc-v1) — Confidential Computing attestation
  not yet implemented (Phase 2c-iv pending). Will stay at
  `1` until a real verifier replaces the StubVerifier; not
  blocking this deploy.

**4.3 Block production is still happening.**

```bash
# qsdm exposes a height counter; watch it tick.
journalctl -u qsdm -f --no-pager | grep -E "block height|mined|tx accepted"
# Wait for one new line. Ctrl-C when you see one.
```

Or via the API (chain height lives on the API server `:8443`,
not on the log viewer or dashboard ports):

```bash
# /api/v1/chain/height requires JWT auth on the production
# config; for a local smoke check use the unauthenticated
# /api/v1/health/live endpoint to confirm the API is up.
curl -sS http://127.0.0.1:8443/api/v1/health/live | jq .
```

If §4.1, §4.2, §4.3 all check out, the deploy is done. Move to
§6 to clean up.

---

## 5. Rollback (only if §3 or §4 fails)

```bash
systemctl stop qsdm
mv /opt/qsdm/qsdm.prev /opt/qsdm/qsdm
chmod +x /opt/qsdm/qsdm
systemctl start qsdm
sleep 3
systemctl status qsdm --no-pager | head -5
# Expect: Active: active (running) again, on the pre-Stage-B
# binary.
```

Total rollback time from "this is broken" to "back on the old
binary" is ~10 seconds. The pre-Stage-B binary will resume
firing the three CRITICAL alerts — that's the known-bad
baseline you came from, not a new regression.

After rollback, capture forensics before retrying:

```bash
journalctl -u qsdm --since "10 minutes ago" --no-pager > /tmp/qsdm-stageb-fail.log
cp /opt/qsdm/qsdm.bak /opt/qsdm/qsdm.stageb-attempt   # if .bak exists
```

scp the log back to your workstation, investigate, then redo
§1 once the build is fixed.

---

## 6. Cleanup (after §4 passes)

```bash
# Keep the rollback artefact for 24h in case a delayed failure
# mode shows up (e.g. a peer interaction that didn't happen in
# the smoke check). After 24h, delete it.
ls -la /opt/qsdm/qsdm.prev
# In your calendar: "rm /opt/qsdm/qsdm.prev" tomorrow.
```

On the Windows build host:

```powershell
# The cross-compiled binary stays in the source dir until the
# next deploy. It's git-ignored (see QSDM/source/.gitignore).
ls .\qsdm-linux-amd64
```

---

## 7. What changed on the wire

This is a soft consensus change in one direction: the validator
now produces and verifies real FIPS 204 ML-DSA-87 signatures
where the stub binary produced SHA-256 hashes (wallet path) or
accepted everything (PoE path).

- **Wallet transactions** signed by this node post-deploy carry
  real ML-DSA-87 signatures. Other validators running stub
  binaries will reject them as invalid (their SHA-256 stub
  can't verify ML-DSA-87). If you're running a single-node
  setup (currently true), this is moot. If you ever bring up
  a second validator, deploy Stage B there too BEFORE peering.
- **Inbound transactions** from peers running stub binaries
  will be rejected by this node post-deploy because the SHA-256
  "signatures" don't pass real ML-DSA-87 verification. Again,
  in a single-node setup this is moot.
- **The trust aggregator** (`qsdm-ngc-attest.service` on the
  OCI sidecar at `vps-oci-sgp1-attest`) submits proof bundles
  signed under the BLR1 ingest's HMAC, not via the wallet
  signer — that path is unaffected by Stage B.

---

## 8. Reference — what the deploy proved

After §4 passes, you've verified end-to-end:

- The Stage B binary builds clean from current head on Windows
  with the Go cross-compile target.
- The binary runs on Ubuntu 22.04 / amd64 without any liboqs
  / OpenSSL runtime dependency (pure-Go via cloudflare/circl).
- systemd integration unchanged — same unit file, same env
  drop-in (`/etc/systemd/system/qsdm.service.d/secrets.conf`).
- The `qsdm_stub_active` gauges for `dilithium`, `wallet`, and
  `poe` are pinned at `0` on a real production scrape, not
  just a unit test.
- Block production continues across the unit restart — no
  state corruption from the binary swap.

If a future Stage C or unrelated change reverts any of these,
this runbook is the regression-detection checklist.

---

## 9. Caddy metrics route — fixed 2026-05-06

> **Status:** *applied on BLR1 as part of the Stage B rollout.*

Earlier the Caddyfile at `/etc/caddy/Caddyfile` proxied
`qsdm.tech/api/metrics/prometheus` to `127.0.0.1:8443`, which
worked on the legacy QSDM+ binary (it served metrics on the
API port) but **not** on Stage B, where the metrics endpoint
lives on `:8081`. Hitting the external scrape URL returned
`401 invalid or expired token` (the API server's JWT auth).

The fix adds an explicit `@scrape path /api/metrics/prometheus`
matcher that routes to `:8081` in both the
`api.qsdm.tech, node.qsdm.tech { … }` block and the
`qsdm.tech { … }` block, before the catch-all `:8443` route.
The corrected file is checked in at
[`QSDM/deploy/Caddyfile`](../../../QSDM/deploy/Caddyfile);
the in-place edit on BLR1 has the original preserved at
`/etc/caddy/Caddyfile.bak.<ts>`.

After editing, `caddy reload` does **not** work because the
Caddyfile has `admin off` (which disables the local admin
API on `:2019`). Use `systemctl restart caddy` instead — the
restart is sub-second and the only externally-observable
effect is the metrics route now resolving correctly.

Smoke check from outside:

```bash
SECRET=…  # value of QSDM_DASHBOARD_METRICS_SCRAPE_SECRET
curl -sS -H "Authorization: Bearer $SECRET" \
     https://qsdm.tech/api/metrics/prometheus | wc -l   # expect 388 lines
curl -sS -H "Authorization: Bearer $SECRET" \
     https://api.qsdm.tech/api/metrics/prometheus | wc -l
curl -sS -H "Authorization: Bearer $SECRET" \
     https://dashboard.qsdm.tech/api/metrics/prometheus | wc -l
```

All three must return identical line counts. Catch-all paths
(`/api/v1/health`, etc.) still go to `:8443`.

## 10. Observability stack (Prometheus + Alertmanager + Grafana)

> **Status:** *deployed on BLR1 alongside qsdm.service on
> 2026-05-06.*

The companion installer at
[`QSDM/deploy/scripts/install_observability.sh`](../../../QSDM/deploy/scripts/install_observability.sh)
lays down a native systemd-managed Prometheus + Alertmanager +
Grafana stack on the same host as `qsdm.service`. All three
bind `127.0.0.1` only; reach them via SSH local-forward:

```bash
ssh -L 9090:127.0.0.1:9090 -L 9093:127.0.0.1:9093 \
    -L 3000:127.0.0.1:3000 root@206.189.132.232
```

| Component | Port | Path | Auth |
| --- | --- | --- | --- |
| Prometheus | `:9090` | `/etc/prometheus/{prometheus.yml,alerts_qsdm.yml}` | none (loopback) |
| Alertmanager | `:9093` | `/etc/alertmanager/alertmanager.yml` | none (loopback) |
| Grafana | `:3000` | `/etc/grafana/`, `/var/lib/grafana/dashboards/qsdm/` | admin + password from `/etc/grafana/.admin-password` |

**On BLR1 the v2-* alert groups are stripped** (`qsdm-v2-mining-*`,
`qsdm-v2-attest-*`, `qsdm-v2-governance`) per the README's
v1-only guidance — the validator is a single-node v1 deploy,
so leaving those groups in produced 5 perpetually-pending
alerts on legitimate-zero state. The full file is preserved
at `/etc/prometheus/alerts_qsdm.yml.with-v2.bak` and at
`/opt/qsdm-deploy/prometheus/alerts_qsdm.example.yml`.
Re-enable when v2 mining/governance comes online by copying
the example file back over `alerts_qsdm.yml` and reloading
Prometheus (`systemctl reload prometheus`).

**Alertmanager is wired with a no-receiver default**
(`/etc/alertmanager/alertmanager.yml` routes all alerts to a
`null-receiver` with no delivery configs). Alerts are visible
in the Alertmanager UI at `http://127.0.0.1:9093` and the
v2 API at `/api/v2/alerts`, but are not delivered externally.
To wire real Slack/PagerDuty/email, copy
`/opt/qsdm-deploy/alertmanager/alertmanager.example.yml`
over the active config and substitute the four `REPLACE_ME`
tokens (see `QSDM/deploy/alertmanager/README.md`).

Re-runs of `install_observability.sh` are idempotent: the
binary install steps skip if the binaries are already on
`PATH`, and the systemd unit / config writes are
content-based overwrites. The only one-time action is the
initial admin password generation; subsequent runs leave
`/etc/grafana/.admin-password-set` and don't re-key.
