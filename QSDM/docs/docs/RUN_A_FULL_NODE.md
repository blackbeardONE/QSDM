# Run a QSDM full node

A full node keeps the whole chain and verifies every block itself: the block hash, the producer's ML-DSA-87
signature, the producer transition schedule and the state root. It does not produce blocks, mine or vote. It follows
the chain by pulling blocks from the public API over HTTPS; every block is verified locally, so the API is trusted
for availability only. It needs no inbound port and no peer-to-peer connection.

A new node starts from a **verified snapshot**, checked against a **checkpoint you pin**, and then catches up. The
whole procedure was tested end to end on 2026-10-19: a fresh machine imported the snapshot, caught up with the
producer within minutes, and its block hashes and state roots equalled the producer's at every height compared.

## What you need

| | |
|---|---|
| OS and CPU | Linux on x86_64 |
| RAM | 10 to 12 GB (every start loads the whole chain into memory) |
| Disk | about 15 GB during setup (download, unpacked snapshot, state), about 6 GB afterwards, growing with the chain |
| Network | outbound HTTPS to `api.qsdm.tech`; no inbound port |
| Tools | `curl`, `sha256sum`, `tar`, Python 3; `qsdmcli` for the release signature check (optional, see step 2) |

## Trust anchors (take them from qsdm.tech, not from the download)

The download page **https://qsdm.tech/fullnode.html** publishes, separately from the files:

- the release key `key_id` that signs the full-node release (release key v2, the same key as the QSDM Hive releases);
- the `sha256` of the `qsdm` binary of the current full-node release;
- the snapshot checkpoint: height, block hash, state root and producer.

Everything below is checked against those values and against the public API. Do not take them from the files you
download: they travel with the bytes.

## 1. Download

From the GitHub release named on qsdm.tech/fullnode.html, download:

- `qsdm-follower-<release>.tar.gz` (the node binary, its manifest, signature and checksums);
- `qsdm-snapshot-<M>.tar.gz.part-00`, `.part-01`, … and `SNAPSHOT-SHA256SUMS`;
- `fullnode-core.toml` (the node configuration with the checkpoint pinned) and `qsdm-release-key-v2.json` (public key).

## 2. Verify the node release

```bash
mkdir -p ~/qsdm-release && tar -C ~/qsdm-release -xzf qsdm-follower-<release>.tar.gz
cd ~/qsdm-release/qsdm-follower-<release>
sha256sum -c SHA256SUMS                      # every line OK
sha256sum qsdm                               # must equal the qsdm sha256 published on qsdm.tech/fullnode.html
```

With `qsdmcli` (from a QSDM Hive installation, or built from source), also check the release signature:

```bash
python3 verify-follower-release.py . --public-key-json ~/qsdm-release-key-v2.json \
    --expected-key-id <KEY_ID from qsdm.tech/fullnode.html> --qsdmcli /path/to/qsdmcli
# the last line must be: verify-follower-release: VERIFIED <release> qsdm <sha256>
```

Anything else: stop.

## 3. Get and verify the snapshot

```bash
cat qsdm-snapshot-<M>.tar.gz.part-* > qsdm-snapshot-<M>.tar.gz
sha256sum -c --ignore-missing SNAPSHOT-SHA256SUMS            # the .tar.gz line must be OK
sudo mkdir -p /srv/qsdm-snapshot && sudo tar -C /srv/qsdm-snapshot -xzf qsdm-snapshot-<M>.tar.gz
( cd /srv/qsdm-snapshot/qsdm-snapshot-<M> && sha256sum -c SHA256SUMS )
```

Check the checkpoint three ways: the values on qsdm.tech/fullnode.html, the public API, and `fullnode-core.toml`:

```bash
curl -fsS "https://api.qsdm.tech/api/v1/chain/blocks?from=<M>&to=<M>&limit=1" | python3 -c \
  'import json,sys; b=json.load(sys.stdin)["blocks"][0]; print(b["height"], b["hash"], b["state_root"], b["producer_id"])'
grep -A5 '^\[snapshot_bootstrap\]' fullnode-core.toml
```

All three must name the same height, block hash, state root and producer. If they differ, stop.

## 4. Install

```bash
sudo useradd --system --home-dir /var/lib/qsdm-follower --shell /usr/sbin/nologin qsdm-follower
sudo install -d -o qsdm-follower -g qsdm-follower -m 0700 /var/lib/qsdm-follower
sudo install -d -m 0755 /opt/qsdm-follower /etc/qsdm-follower
sudo install -m 0755 ~/qsdm-release/qsdm-follower-<release>/qsdm /opt/qsdm-follower/qsdm
sudo install -m 0640 -o root -g qsdm-follower fullnode-core.toml /etc/qsdm-follower/core.toml
printf 'CONFIG_FILE=/etc/qsdm-follower/core.toml\nQSDM_CHAIN_SYNC_URLS=https://api.qsdm.tech/api/v1\n' | \
  sudo install -m 0640 -o root -g qsdm-follower /dev/stdin /etc/qsdm-follower/core.env
sudo chmod -R a+rX /srv/qsdm-snapshot
```

Never set `QSDM_NETWORK_BLOCK_PRODUCER`, `QSDM_SOLO_VALIDATOR_MODE` or `QSDM_LEGACY_MINING_MODE`: a full node never
produces or mines, and the node refuses sync URLs in mining mode.

## 5. Import the snapshot

The state directory must be empty. Run the import as the service user with a clean environment:

```bash
sudo runuser -u qsdm-follower -- sh -c 'cd /var/lib/qsdm-follower && exec env -i PATH=/usr/bin:/bin \
    CONFIG_FILE=/etc/qsdm-follower/core.toml LOG_FILE=/var/lib/qsdm-follower/import.log \
    /opt/qsdm-follower/qsdm --snapshot-import --from /srv/qsdm-snapshot/qsdm-snapshot-<M>'
echo "exit $?"
```

The import verifies everything again against your pinned checkpoint: the hash chain from genesis, the producer's
signature on the checkpoint block, the producer transition and the state root. It takes several minutes and about
10 GB of RAM.

- **Exit 0** prints a report with `"authorized_by": "producer_transition"` and three installed files. Then delete
  `/srv/qsdm-snapshot` and the downloaded archive.
- **Exit 78** prints the reason and installs nothing (usually a wrong checkpoint, the wrong release or a damaged
  download). Fix it and retry.

## 6. Start the node

```ini
# /etc/systemd/system/qsdm-follower.service
[Unit]
Description=QSDM full node (HTTP sync)
After=network-online.target
Wants=network-online.target

[Service]
User=qsdm-follower
Group=qsdm-follower
WorkingDirectory=/var/lib/qsdm-follower
EnvironmentFile=/etc/qsdm-follower/core.env
Environment=LOG_FILE=/var/lib/qsdm-follower/qsdm.log
ExecStart=/opt/qsdm-follower/qsdm
Restart=on-failure
RestartSec=30
RestartPreventExitStatus=78 86

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now qsdm-follower
journalctl -u qsdm-follower -f
```

The node restores the snapshot state, then catches up over HTTPS. These warnings are expected: the API runs in plain
HTTP mode (it listens on 127.0.0.1 only), the pure-Go WebAssembly runtime is used, the node creates its own consensus
key on first start (a local identity that holds no funds; keep it private), and it runs without P2P peers by design.

## 7. Check it against the network

```bash
blk() { curl -s "$1/chain/blocks?from=$2&to=$2&limit=1" | python3 -c 'import json,sys; b=json.load(sys.stdin)["blocks"][0]; print(b["height"], b["hash"], b["state_root"])'; }
T=$(curl -s http://127.0.0.1:8080/api/v1/status | python3 -c 'import json,sys; print(json.load(sys.stdin)["chain_tip"])')
for h in $((T - 100)) $((T - 1)); do blk http://127.0.0.1:8080/api/v1 "$h"; blk https://api.qsdm.tech/api/v1 "$h"; done
```

Each pair of lines must be identical, and your tip should follow the public tip within a block or two. Repeat this
from time to time; a monitor that alerts on a mismatch is a good idea.

## If something does not match

Stop the node and keep its state directory as it is. Collect `qsdm --version`, `GET /api/v1/status` from both sides,
the height with both hashes and state roots, and the last few hundred log lines, and report them through the security
contact in [SECURITY.md](../../../SECURITY.md). Your node checks every block itself: if it refuses a block, it may be
the one that is right.

## Security notes

- Keep the API on loopback (`[api] bind_address = "127.0.0.1"`, as in `fullnode-core.toml`) or behind an
  authenticating reverse proxy.
- Run as the unprivileged service user and keep `/var/lib/qsdm-follower` at mode 0700.
- The checkpoint you pin is the trust anchor for all history below it. Take it from qsdm.tech and confirm it with the
  public API.

## Keeping up to date

New node releases and fresh snapshots are announced on qsdm.tech/fullnode.html. The snapshot is only needed for the
first start; afterwards the node keeps its own copy of the chain. To upgrade the binary, verify the new release as in
step 2, stop the node, replace `/opt/qsdm-follower/qsdm` and start it again.
