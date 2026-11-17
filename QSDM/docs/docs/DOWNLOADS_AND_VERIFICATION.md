# Downloads & verification

Where to get QSDM software and how to check that a file really comes from the
QSDM project before you run it.

## Current downloads

All downloads are linked from **[qsdm.tech/download.html](https://qsdm.tech/download.html)**.

| Product | Version | Platform | File | Size | SHA-256 |
|---|---|---|---|---|---|
| QSDM Hive | 1.4.23 | Windows x64 | `qsdm-hive-1.4.23-win-x64.exe` | 128.6 MB | `9e3acc156cfc74607d35d6a8ed6d152d5431a905f04d7b3cb24ae58d9ab3c791` |
| QSDM Hive | 1.4.23 | Linux x86-64 | `qsdm-hive-1.4.23-linux-x86_64.AppImage` | 165.7 MB | `9383375347d7662bd03c3cdfe79f3095a9c9ae27c5d9cffffdd715f838913e05` |
| QSDM Hive | 1.4.23 | Linux x86-64 | `qsdm-hive-1.4.23-linux-x64.tar.gz` | 156.8 MB | `5d0fa427aaf1016b65a413efc2f13545e7b5da83842b05b817f21b0cdfe79320` |

Hive 1.4.23 (published 15 November 2026) is served from the release channel
`https://qsdm.tech/downloads/hive-v2/`, together with checksum files
(`SHA256SUMS-win.txt`, `qsdm-hive-1.4.23-linux-SHA256SUMS.txt`) and the signed
release manifests (`qsdm-hive-release-windows-v2.json`,
`qsdm-hive-release-linux-v2.json`).

Other packages on the download page (browser wallet extension, Edge Agent, Core
validator and home gateway builds) come with their own `SHA256SUMS` files.

> [!NOTE]
> The Core validator package on the download page (v0.4.7-rc.9) is an operator
> build. It is not the release that runs the public pilot network today
> (`hardened-legacy-20261116-d7ffcd4-hl2-edge-go12611-ids-pqe-rc1114`, see [Network status](NETWORK_STATUS.md)).

**Full node.** The GitHub release `fullnode-rc1114` (November 2026) carries
the producer's exact `qsdm`. The one-command installer,
`QSDM/scripts/fullnode/install-full-node.sh`, installs the release that
[qsdm.tech/fullnode.html](https://qsdm.tech/fullnode.html) names. A full node
needs 6 GB of RAM. See [Run a full node](RUN_A_FULL_NODE.md).

> [!IMPORTANT]
> **Upgrading from Hive 1.4.20 (Windows) or 1.4.17 (Linux):** install 1.4.23 by
> hand once. Those versions trust the previous release key, which was rotated, so
> they cannot verify 1.4.23 on their own. Your wallet and settings are kept.
> Hive 1.4.17 stopped working after 9 November 2026; 1.4.20 stops after
> 19 December 2026.

## Check the SHA-256 (everyone)

Compare the result with the table above or with the checksum file. If it does
not match, delete the file and download it again from qsdm.tech.

**Windows (PowerShell):**

```powershell
Get-FileHash -Algorithm SHA256 .\qsdm-hive-1.4.23-win-x64.exe
```

**Linux:**

```bash
curl -fsSLO https://qsdm.tech/downloads/hive-v2/qsdm-hive-1.4.23-linux-SHA256SUMS.txt
sha256sum -c --ignore-missing qsdm-hive-1.4.23-linux-SHA256SUMS.txt
```

## What Hive checks for you

Hive does not trust an update just because it was downloaded from qsdm.tech.
Before installing an update it:

1. downloads the signed release manifest for its platform;
2. checks the **ML-DSA-87** signature against the release public key built into
   Hive, and rejects any other `key_id`;
3. rejects a manifest that has expired or that claims to be valid for more than
   120 days;
4. checks the name, size and SHA-256 of the updater metadata and the installer
   listed in the manifest.

If any step fails, Hive does not install the update.

## The release key

| | |
|---|---|
| Key | QSDM Hive release key **v2** |
| Algorithm | ML-DSA-87 (NIST FIPS 204) |
| `key_id` | `4081bf2c4755f4c5c1565b4fac75e14a7e0b52042ac3d34bf8a7f525866c64a9` |
| Public key | [`QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json`](https://github.com/qsdm-tech/QSDM/blob/main/QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json) |
| In use since | Hive 1.4.21 (October 2026) |
| Previous key | `10ab9c57…07a1`, **rotated** in October 2026; it no longer signs new releases |

The `key_id` is the SHA-256 of the raw public key bytes, so you can recompute it
from the public key file.

Signed manifests are re-issued at least every 90 days. Each manifest states
its own `issued_at` and `expires_at` dates; check them as described below.

## Check the signature yourself (advanced)

You need Python 3 and `qsdmcli` (built from this repository, `QSDM/source/cmd/qsdmcli`).

```bash
# 1. The signed manifest and the release public key
curl -fsSLO https://qsdm.tech/downloads/hive-v2/qsdm-hive-release-linux-v2.json
curl -fsSL https://raw.githubusercontent.com/qsdm-tech/QSDM/main/QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json \
  -o qsdm-hive-release-key-v2.json

# 2. Split the envelope and print the key ids
python3 - <<'PY'
import base64, hashlib, json
env = json.load(open("qsdm-hive-release-linux-v2.json"))
key = json.load(open("qsdm-hive-release-key-v2.json"))
open("manifest.json", "wb").write(base64.b64decode(env["manifest_base64"]))
open("manifest.sig", "w").write(env["signature"])
open("release-key-v2.hex", "w").write(key["public_key"])
print("envelope key_id :", env["key_id"])
print("computed key_id :", hashlib.sha256(bytes.fromhex(key["public_key"])).hexdigest())
PY

# 3. Verify the ML-DSA-87 signature
qsdmcli wallet verify --public-key-file release-key-v2.hex \
  --message-file manifest.json --signature-file manifest.sig
```

Both key ids must be `4081bf2c…c64a9` and the signature must verify. Then open
`manifest.json`: it lists every release file with its size and SHA-256, and the
`issued_at` / `expires_at` dates. The installer's SHA-256 must match the entry
in the manifest. For Windows use `qsdm-hive-release-windows-v2.json`.

## Windows SmartScreen

Windows installers are **not Authenticode-signed** yet, so Windows may show
"Windows protected your PC". `Get-AuthenticodeSignature` reports `NotSigned`.
The ML-DSA-87 release signature above protects updates inside Hive, but it does
not give Windows a verified publisher. Check the SHA-256, then choose
**More info → Run anyway**. See the
[code signing policy](https://github.com/qsdm-tech/QSDM/blob/main/CODE_SIGNING_POLICY.md)
for the plan.

QSDM will never ask you to install a root certificate, disable antivirus, or run
a download that is not linked from qsdm.tech.

## Report a bad download

If a hash or signature does not match, do not run the file. Report it as
described in the [security policy](https://github.com/qsdm-tech/QSDM/blob/main/SECURITY.md).
