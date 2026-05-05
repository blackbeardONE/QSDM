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

> **Pre-Stage-B baseline being replaced.** The currently-live
> binary on `/opt/qsdm/qsdm` is built from a stub-era commit:
> non-CGO, no `dilithium_circl` tag, so `crypto.NewDilithium()`
> returned `nil` and the wallet/PoE/dilithium kinds were all
> firing CRITICAL alerts at boot. After this deploy those three
> rows go to `0` and stay there.

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
stub-active alerts auto-resolve. Three quick checks confirm
that, in increasing order of confidence:

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
# The scrape endpoint is open-read on this node (strict auth
# disabled — see vps.txt §[2]).
curl -s http://127.0.0.1:8080/api/metrics/prometheus \
  | grep -E '^qsdm_stub_active\{' \
  | sort
```

Expected output: every `qsdm_stub_active{kind="..."}` sample
ends with ` 0`. The kinds that previously flipped to `1` at
boot under the stub binary — `dilithium`, `wallet`, `poe` —
are now structurally pinned at `0`. Other kinds (e.g.
`mesh3d_cuda`, `cc`, `wasm_sdk`) may legitimately be `1` if
the corresponding feature isn't wired on this node; that's
fine and isn't part of this deploy's scope.

**4.3 Block production is still happening.**

```bash
# qsdm exposes a height counter; watch it tick.
journalctl -u qsdm -f --no-pager | grep -E "block height|mined|tx accepted"
# Wait for one new line. Ctrl-C when you see one.
```

Or via the API:

```bash
curl -s http://127.0.0.1:8080/api/v1/chain/height | jq .
# Expect: {"height": <some-number>}, run twice with 30s gap,
# expect height to advance.
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
