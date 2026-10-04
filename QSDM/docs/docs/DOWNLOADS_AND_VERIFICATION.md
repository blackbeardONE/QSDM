# Downloads & verification

Where to get QSDM software and how to check that a file really comes from the
QSDM project before you run it.

## Current downloads

All downloads are linked from **[qsdm.tech/download.html](https://qsdm.tech/download.html)**.

| Product | Version | Platform | File | SHA-256 |
|---|---|---|---|---|
| QSDM Hive | 1.4.21 | Windows x64 | `qsdm-hive-1.4.21-win-x64.exe` | `b3f408f861eccfd03d049e3344bb6a9d49ce777b20ad8bd4bd22843b27b48e6b` |
| QSDM Hive | 1.4.21 | Linux x86-64 | `qsdm-hive-1.4.21-linux-x86_64.AppImage` | `25623568dc04f46ffb2e9d3b016c499ba0d7f0d013e1f713699bd130a63e9a05` |
| QSDM Hive | 1.4.21 | Linux x86-64 | `qsdm-hive-1.4.21-linux-x64.tar.gz` | `bc624e5381bf27bd17b311f1350fd5be5f39bfec37328e87eb60bbf3d4023240` |

Hive 1.4.21 files are served from the release channel
`https://qsdm.tech/downloads/hive-v2/`, together with checksum files
(`SHA256SUMS-win.txt`, `qsdm-hive-1.4.21-linux-SHA256SUMS.txt`) and the signed
release manifests (`qsdm-hive-release-windows-v2.json`,
`qsdm-hive-release-linux-v2.json`).

Other packages on the download page (browser wallet extension, Edge Agent, Core
validator and home gateway builds) come with their own `SHA256SUMS` files.

> [!NOTE]
> The Core validator package on the download page (v0.4.7-rc.9) is an operator
> build. It is not the release that runs the public pilot network today
> (`hardened-legacy-20261002-d7ffcd4-hl2`, see [Network status](NETWORK_STATUS.md)).

> [!IMPORTANT]
> **Upgrading from Hive 1.4.20 (Windows) or 1.4.17 (Linux):** install 1.4.21 by
> hand once. Those versions trust the previous release key, which was rotated, so
> they cannot verify 1.4.21 on their own. Your wallet and settings are kept.

## Check the SHA-256 (everyone)

Compare the result with the table above or with the checksum file. If it does
not match, delete the file and download it again from qsdm.tech.

**Windows (PowerShell):**

```powershell
Get-FileHash -Algorithm SHA256 .\qsdm-hive-1.4.21-win-x64.exe
```

**Linux:**

```bash
curl -fsSLO https://qsdm.tech/downloads/hive-v2/qsdm-hive-1.4.21-linux-SHA256SUMS.txt
sha256sum -c --ignore-missing qsdm-hive-1.4.21-linux-SHA256SUMS.txt
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
| Public key | [`QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json`](https://github.com/blackbeardONE/QSDM/blob/main/QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json) |
| In use since | Hive 1.4.21 (October 2026) |
| Previous key | `10ab9c57…07a1`, **rotated** in October 2026; it no longer signs new releases |

The `key_id` is the SHA-256 of the raw public key bytes, so you can recompute it
from the public key file.

Signed manifests are re-issued at least every 90 days. The current Hive 1.4.21
manifests are valid until 2 January 2027.

## Check the signature yourself (advanced)

You need Python 3 and `qsdmcli` (built from this repository, `QSDM/source/cmd/qsdmcli`).

```bash
# 1. The signed manifest and the release public key
curl -fsSLO https://qsdm.tech/downloads/hive-v2/qsdm-hive-release-linux-v2.json
curl -fsSL https://raw.githubusercontent.com/blackbeardONE/QSDM/main/QSDM/deploy/release-trust/qsdm-hive-release-key-v2.json \
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
[code signing policy](https://github.com/blackbeardONE/QSDM/blob/main/CODE_SIGNING_POLICY.md)
for the plan.

QSDM will never ask you to install a root certificate, disable antivirus, or run
a download that is not linked from qsdm.tech.

## Report a bad download

If a hash or signature does not match, do not run the file. Report it as
described in the [security policy](https://github.com/blackbeardONE/QSDM/blob/main/SECURITY.md).
