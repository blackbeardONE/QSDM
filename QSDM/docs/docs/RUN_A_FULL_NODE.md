# Run a QSDM full node

A full node keeps the whole chain and verifies every block itself: the block hash, the producer's ML-DSA-87
signature, the producer transition schedule and the state root. It does not produce blocks, mine or vote. It follows
the chain by pulling blocks from the public API over HTTPS; every block is verified locally, so the API is trusted
for availability only. It needs no inbound port and no peer-to-peer connection.

A new node starts from a **verified snapshot**, checked against a **checkpoint you pin**, and then catches up. The
whole procedure was tested end to end on 2026-10-19: a fresh machine imported the snapshot, caught up with the
producer within minutes, and its block hashes and state roots equalled the producer's at every height compared.
[Verified snapshot bootstrap](SNAPSHOT_BOOTSTRAP.md) explains how the import checks the snapshot and what you trust.

## Quick install (one command)

`install-full-node.sh` does steps 1 to 7 below on a fresh Linux x86_64 server (Ubuntu 22.04 or later, or Debian 12).
It makes every check those steps make, takes the values to pin from qsdm.tech and never from a download, and stops
before it installs anything if a check fails.

Get the script and read it first (it is plain bash):

```bash
curl -fsSLO https://raw.githubusercontent.com/blackbeardONE/QSDM/main/QSDM/scripts/fullnode/install-full-node.sh
less install-full-node.sh
```

Then run it as root, or as a user who can use `sudo`:

```bash
bash install-full-node.sh --from-website
```

`--from-website` reads the values to pin from https://qsdm.tech/fullnode.html and prints them: compare them with the
page in your browser. To give the values yourself instead, pass each one as a flag (`--help` lists them). If the
block maker has changed since the release, the page also shows the block-maker schedule, and the script puts it
into the node's `core.toml` (see "When the block maker changes" below).

- If a check fails, the script says what did not match and what to do. Once that is fixed, run the same command
  again: it carries on where it stopped, and it never deletes chain data.
- `--dry-run` prints the plan and changes nothing.
- The release signature is checked only when the script finds `qsdmcli` (or you pass `--qsdmcli PATH`). Without it,
  the `qsdm` binary is still checked against the SHA-256 on qsdm.tech. `--require-signature` makes the signature
  check compulsory.
- It stops early if the server has too little memory or disk (see "What you need" below).
- At the end it compares your node's blocks with the network (step 7). If the network's API does not answer (it
  answers 503 for a few minutes while the block producer restarts), it tries again every 30 seconds for up to 15
  minutes. Run the same command again later to repeat that check.

The project ran it on 8 November 2026 on a second server of its own (Ubuntu 24.04, 12 GB of memory), with
`--from-website --require-signature`: the installer's first run on a real server. The installer had the node running
about 6 minutes after it started, 131 seconds of them for the snapshot import. The node then caught up the 174,000
blocks after the snapshot over HTTPS at about 730 a minute. The installer's last check stopped early: the network's
API answered 503 while the block producer restarted for release rc1108. The node kept running, and the installer now
waits for the API. On 9 November 2026 the node moved to fullnode-rc1108 by replacing its `qsdm` binary, as the guide
describes: it answered again 4.5 minutes after the stop, at the network's tip and with the same blocks. Its memory
(RssAnon) was 2.9 GiB before, after 177,817 blocks appended on fullnode-rc1107, and 2.0 GiB after the restart.

The manual steps below are what the script does. Use them if you prefer to do it by hand.

## What you need

| | |
|---|---|
| OS and CPU | Linux on x86_64 |
| RAM | 6 GB for fullnode-rc1111, fullnode-rc1110, fullnode-rc1109 and fullnode-rc1108 (and fullnode-rc1107): the import and a start each peak at about 2 GiB at 880 376 blocks. A fullnode-rc1111, fullnode-rc1110, fullnode-rc1109 or fullnode-rc1108 node's memory stays flat while it runs; a fullnode-rc1107 node holds about 0.5 GiB more for every 100 000 blocks it appends until its next restart (11.6 days at 10 seconds per block). The installer stops below 5.5 GiB (below 11 GiB for the older fullnode-rc1019). See [Memory](#memory). |
| Disk | about 15 GB during setup (download, unpacked snapshot, state), about 6 GB afterwards, growing with the chain |
| Network | outbound HTTPS to `api.qsdm.tech`; no inbound port |
| Tools | `curl`, `sha256sum`, `tar`, Python 3; `qsdmcli` for the release signature check (optional, see step 2) |

### Memory

The import and every start load the whole chain into memory, and the node keeps it there while it runs, so the
memory a node needs grows with the chain. Go's garbage collector lets the heap grow to about twice what is in use
before it collects, so the peak is about twice the heap, and a running node can use about as much as a start.

fullnode-rc1111, the release qsdm.tech/fullnode.html names and the installer installs, is fullnode-rc1110 with one
more rule: a transaction ID names one transaction, whatever its contract, and from block 1,160,000 (about
21 November 2026) a block that repeats a committed ID is invalid ([Consensus rules](CONSENSUS_RULES.md#transaction-id-uniqueness-from-block-1160000)), so install it
before that height. fullnode-rc1110 (13 November 2026) is
fullnode-rc1109 with a security fix: only a task's catalog manager may migrate the task, and from block 1,150,000
(about 19 November 2026) a block with any other migrate is invalid ([Consensus rules](CONSENSUS_RULES.md#task-migrate-authorization-from-block-1150000)), so install it
before that height. fullnode-rc1109 (12 November 2026) is
fullnode-rc1108 built with Go 1.26.11 (it fixes GO-2026-6859 in Go's web server) and golang.org/x/net v0.62.0, and
its start reads eight journal lines per worker ahead instead of one (the journal load at height 880 377 took 25.0
seconds instead of 28.0 on 6 threads, at the same peak memory). fullnode-rc1108 (8 November 2026) is fullnode-rc1107
(7 November 2026) with one change, in the third point below. All three are lighter than fullnode-rc1019 in five
ways:

- it shares the values that repeat across blocks in memory (the producer's public key, which every block it signs
  carries, and repeated strings);
- a start verifies each block's ML-DSA-87 signature once, on every core, instead of twice on one;
- once a block's signature is verified at the start, the node keeps it only in the journal
  (`qsdm_chain.ndjson`), with its position and a 16-byte digest in memory. It reads it back, and checks it against
  that digest, only to send the block to another node (`GET /api/v1/chain/blocks` and the peer-to-peer catch-up).
  Blocks the node appends while it runs keep theirs in memory until it restarts with fullnode-rc1107; with
  fullnode-rc1108 they too keep them only in the journal, once the next block is written there;
- since 7 November a start prepares each producer's public key once instead of once per signature (the pure-Go
  ML-DSA-87 code derives a 57 KB matrix from the key every time it is unpacked), and it decodes the receipts file on
  every core. At height 880 376 that took the journal load from 42 to 43 seconds to 26 to 29 on 4 threads and from
  28 to 30 seconds to 17 to 20 on 16, and the memory the load allocates in all from 32.9 GiB to 11.6 GiB. The start
  rows of the table below were measured before this change;
- since 7 November the import reads the snapshot's journal the way a start reads it: it verifies each block once,
  on every core, and keeps every signature but the tip's only in the journal, where it used to load every block
  with its signature and then verify the whole chain again on one core. It runs every check it ran before, and a
  snapshot it refuses gets the same error and exit code as before. The export's check of what it wrote and the
  offline `--hl1-tail-replay` read the journal the same way.

Measured with `cmd/qsdm/restore_bench_test.go` on a copy of the public chain at height 880 376 (880 377 blocks, a
5.0 GB journal), with Go's default settings, on Windows x86-64 with a 16-thread laptop CPU; the import with
fullnode-rc1107's code on a slower desktop (two Xeon E5-2670 from 2012, 32 threads, 32 GB, Windows x86-64) with Go
on 4 threads, where the import before its last change took 204 seconds and peaked at 5.5 GiB:

| | fullnode-rc1019 | fullnode-rc1107 |
|---|---:|---:|
| Start (the restore every start runs), peak | 8.2 GiB | 1.9 GiB |
| Go heap after the start | 4.1 GiB | 1.0 GiB |
| Time of a start | about 2.5 minutes | about 30 seconds |
| Import, peak | 8.3 GiB | 1.8 GiB |
| Go heap during the import, peak | not measured | 1.7 GiB |
| Time of an import | about 2.5 minutes | about 80 seconds on the slower desktop |
| Heap per further block | about 8.4 KB | about 0.6 KB |
| Start peak per further 100 000 blocks | about 1.6 GiB | about 0.12 GiB |
| Start peak at about 1 020 000 blocks | about 10.4 GiB | about 2.1 GiB |

The start and the import use every core, so they take longer on a server with fewer: on 1 thread the import took
about 3.6 minutes on the desktop and peaked at 2.0 GiB (before this change, which checked every signature on one
core anyway, 3.3 minutes and 5.5 GiB); on 32 threads it took 43 seconds. With fullnode-rc1107 the import holds
the chain the way a start holds it, verified signatures in the journal only: on the desktop, on 4 threads, the
import and a start both peaked at 1.8 GiB. So the import no longer sets the memory a server needs; a start, which
every restart runs, and the running node do. On a server a start takes longer than on the laptop: the project's
full node in Japan (6 vCPUs, 12 GB) restarted on fullnode-rc1108 on 9 November at block 1 058 195 and answered
after 4 minutes 23 seconds, 37 seconds of them for the journal prefix check and 3 minutes 36 seconds to load and
check the chain; on 10 November, right after another 6.7 GB had been read from its disk (the disk cache was cold),
the load took 7 minutes. A start checks the signature of every block, so it grows with the chain. fullnode-rc1109
reads the journal further ahead of its workers (eight lines each instead of one): on the laptop, on 6 threads, the
load at 880 377 blocks took 25.0 seconds instead of 28.0. The project's full node in Japan answered again 2 minutes
51 seconds after its switch to fullnode-rc1109 on 12 November, at block 1 087 781. The import rows were measured with the harness's
import phase, which runs every check of the import but not its staging copy (the import first copies the snapshot's
files into the state directory and hashes them) or the checkpoint pin; the staging copy and an import at a second
height were not measured. The last three rows are derived, not measured at a second height: from the sizes of the
values that the newest 80 000 restored blocks keep in memory, times the measured ratio of peak to heap (about 2).
A fullnode-rc1107 node also holds the signatures of the blocks it appended since its last start: about 0.5 GiB of
heap per 100 000 blocks. At 10 seconds per block, 100 000 blocks take 11.6 days. fullnode-rc1108 keeps them only
in the journal too, once the next block is written there: in a test that appends 2 000 signed blocks, each block
kept about 0.6 KB of heap instead of about 5.4 KB.

To run fullnode-rc1019 in less memory, upgrade it to fullnode-rc1111 (see [Keeping up to date](#keeping-up-to-date)),
or set Go's soft memory limit for the service: run
`sudo systemctl edit qsdm-follower`, add `Environment=GOMEMLIMIT=...` under `[Service]`, then
`sudo systemctl restart qsdm-follower`. Choose a value below the server's memory and well above the heap the node
holds: 4.1 GiB at height 880 376 plus about 0.8 GiB per 100 000 blocks (about 5.2 GiB at 1 020 000). For example,
on a server with 12 GB, `GOMEMLIMIT=9GiB` keeps the process near 9 GiB while the heap still has room to grow.
Under a limit the garbage collector runs more often, and as the heap the node needs approaches the limit it spends
more and more CPU on collection: raise the limit or add memory before that. The installer and the unit below do
not set it.

## Trust anchors (take them from qsdm.tech, not from the download)

The download page **https://qsdm.tech/fullnode.html** publishes, separately from the files:

- the release key `key_id` that signs the full-node release (release key v2, the same key as the QSDM Hive releases);
- the `sha256` of the `qsdm` binary of the current full-node release;
- the snapshot checkpoint: height, block hash, state root and producer;
- after a change of block maker, the block-maker schedule (see "When the block maker changes").

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
as much memory as a start: about 1.8 GiB for the snapshot at height 880 376 with fullnode-rc1107, fullnode-rc1108 and fullnode-rc1109 (8.3 GiB with
fullnode-rc1019; see [Memory](#memory)).
[Verified snapshot bootstrap](SNAPSHOT_BOOTSTRAP.md#3-trust-model) lists every check.

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
from time to time, or let the watch below repeat it every 5 minutes.

## If something does not match

Stop the node and keep its state directory as it is. Collect `qsdm --version`, `GET /api/v1/status` from both sides,
the height with both hashes and state roots, and the last few hundred log lines, and report them through the security
contact in [SECURITY.md](../../../SECURITY.md). Your node checks every block itself: if it refuses a block, it may be
the one that is right.

## Watching your node

`watch-full-node.py` repeats step 7 every 5 minutes and tells you when something is wrong. It needs only Python 3 and
systemd, runs as a throwaway unprivileged user, and only reads: the node's API, the public API and the memory of the
node's process. It never stops, restarts or changes the node. Take it from the commit that qsdm.tech/fullnode.html
names for it, and compare its SHA-256 with the one the page gives:

```bash
curl -fsSLO https://raw.githubusercontent.com/blackbeardONE/QSDM/<commit>/QSDM/scripts/fullnode/watch-full-node.py
sha256sum watch-full-node.py
sudo python3 watch-full-node.py install --name "my node"
```

With `--webhook-file FILE` (a file whose first line is a Discord webhook URL) it also posts to Discord; the URL is
kept in a root-only file and never printed. Without it, the results go only to the journal
(`journalctl -u qsdm-fullnode-watch`). `sudo python3 /opt/qsdm-fullnode-watch/watch-full-node.py status` shows the
last check, the open problems and the node's memory over the last week.

It reports a problem when:

- the node's API has not answered for 15 minutes, or at once when systemd marks `qsdm-follower` failed;
- the node's tip has not moved for 15 minutes while the network is ahead of it;
- the node and the network have different blocks 2 or 100 blocks below the tip they share, also when read again 20
  seconds later (then do what "If something does not match" says);
- the public API has not answered for 15 minutes, or the network's tip has not moved for 15 minutes.

A node that stays more than 30 blocks behind for 15 minutes (while it catches up after the snapshot, for example) and
blocks that cannot be compared for 15 minutes are warnings. A problem is posted when it starts, every 6 hours while
it lasts, and when it ends. A daily summary at 05:00 UTC gives both tips and the node's memory (RssAnon), with the
figures from a day and a week before. A producer restart, when the public API answers 503 for a few minutes, stays
under the 15 minutes. The settings are in `/etc/qsdm-fullnode-watch/watch.env` (the `QSDM_WATCH_*` names at the top
of the script); `sudo python3 /opt/qsdm-fullnode-watch/watch-full-node.py uninstall` removes it.

## When the block maker changes

The block maker (the producer) signs every block with its own key, and a full node accepts a block only from the key
that its block-maker schedule names for that height: the `[consensus.producer_transition]` table in `core.toml`
([Producer transition](PRODUCER_TRANSITION.md)). When block production moves to another server, for example to the
standby block maker and back, the new block maker signs from the next block on with a new key, and every node needs
the new schedule. The block maker changed on 13 November 2026: the project's full node in Japan made blocks 1,095,602
to 1,095,784, and the producer has signed with a new key since block 1,095,785. A node with the schedule of the
full-node releases (up to `fullnode-rc1111`) stops at block 1,095,601 until it has the new one: four block makers,
digest `fd23fbcae12315a4…`, on qsdm.tech/fullnode.html and in
[Producer transition](PRODUCER_TRANSITION.md#the-public-schedule).

Until your node has it, it stays at the old block maker's last block: its tip stops moving, its log says
`external block producer is not authorized` for the next block, and the watch reports `node-stalled`. Nothing is lost:
with the new schedule the node carries on from where it stopped.

qsdm.tech/fullnode.html then shows the new schedule under "Block makers", as the exact text of the table, with its
digest. For a node set up with `install-full-node.sh`:

```bash
bash install-full-node.sh --update-schedule --from-website
```

It reads the schedule from the page and checks that it continues your node's: the same journal prefix pin and the
same earlier block makers from the same blocks, with new ones only added at the end. It refuses anything else and
then changes nothing. It keeps your `core.toml` as `core.toml.before-<UTC time>`, puts the new schedule into the table,
names the last block maker in `authorized_block_producers` (see [Private P2P mode](PRIVATE_P2P.md), section 4) and
restarts the node. Nothing else in the file changes. If systemd then reports the node as failed, the old file goes back
and the node is restarted with it. Last, it checks your node against the network as in step 7 and says when your node
has passed the new block maker's first block. `--dry-run` shows the plan first. To use a schedule you saved from the
page, pass `--producer-schedule FILE` instead of `--from-website`. A new install with `--from-website` takes the
schedule from the page by itself.

By hand: replace the `[consensus.producer_transition]` table of `/etc/qsdm-follower/core.toml` (its header line and
every line of it, up to the next table) with the schedule from the page, set `authorized_block_producers` to the last
`producer_id` in it, and restart the node (`sudo systemctl restart qsdm-follower`). The node checks the schedule when it
starts and refuses one that breaks its rules.

## Security notes

- Keep the API on loopback (`[api] bind_address = "127.0.0.1"`, as in `fullnode-core.toml`) or behind an
  authenticating reverse proxy.
- Run as the unprivileged service user and keep `/var/lib/qsdm-follower` at mode 0700.
- The checkpoint you pin is the trust anchor for all history below it. Take it from qsdm.tech and confirm it with the
  public API.
- The block-maker schedule decides whose blocks your node accepts. Take it from qsdm.tech only. The installer refuses a
  schedule that changes an earlier block maker, so the history your node holds stays as it is; but a new block maker
  is exactly what a schedule adds, and a wrong one would make your node follow the wrong chain from that block on.

## Keeping up to date

New node releases and fresh snapshots are announced on qsdm.tech/fullnode.html. The snapshot is only needed for the
first start; afterwards the node keeps its own copy of the chain. To upgrade the binary, verify the new release as in
step 2, stop the node, replace `/opt/qsdm-follower/qsdm` and start it again:

```bash
sudo systemctl stop qsdm-follower
sudo install -m 0755 ~/qsdm-release/qsdm-follower-<release>/qsdm /opt/qsdm-follower/qsdm
sudo systemctl start qsdm-follower
```

A node installed from fullnode-rc1019, fullnode-rc1107 or fullnode-rc1108 upgrades to fullnode-rc1109 this way: its
configuration and state directory stay as they are. From fullnode-rc1019, its next start loads the chain in about a
quarter of the memory; from fullnode-rc1019 or fullnode-rc1107, its memory then stays flat while it runs; from any of
them, it gets Go 1.26.11 and the faster start. Check step 7 again
afterwards; `GET /api/v1/status` then names the new release.
