# Hive packaging checks

From this Hive directory, run `npm ci`, then `npm run runtime:install`. The latter installs the locked packages declared in `release/app/package.json` into its own node_modules, skips lifecycle scripts and browser downloads, and does not change dependency versions.

Run `npm run verify:runtime` before packaging. `package:electron` does this before cleaning/building, and electron-builder's beforePack hook also enforces it for direct invocations and Linux scripts. Having a package in the parent development node_modules is insufficient: the production app must carry its own declared runtime dependency tree.

The afterPack hook verifies each runtime package and entry point in app.asar, then starts the packaged Electron executable with ELECTRON_RUN_AS_NODE=1 to require all declared runtime dependencies. It fails packaging on missing transitive dependencies. This probe does not start the Hive application or access the user's profile.

Regression checks: `node --test .erb/scripts/verify-runtime-dependencies.test.cjs`.

A packaged app smoke test with a fresh QSDM_HIVE_APPDATA_ROOT and QSDM_HIVE_SMOKE_TEST=1 is still required before signing, publishing or installing. Verify smoke-result.json reports ok. Do not treat a successful installer build or a valid release signature as evidence the application can start.

## macOS

On a Mac with Go and Node.js installed, `npm run package` runs `package:mac`. It builds `qsdmcli` for Intel and Apple silicon (`QSDM/deploy/scripts/build_hive_macos_native.sh`), checks the runtime dependencies, builds Hive, and writes one universal app (Intel and Apple silicon) to `release/build-mac/`, as a `.dmg` and a `.zip`. The app needs macOS 12 (Monterey) or newer.

On a Mac, Hive is the wallet and runs tasks. It does not mine: mining needs Windows or Linux with an NVIDIA GPU. The Mac app carries `qsdmcli` but not the miner, its CUDA solver or the Edge tools, and Hive refuses to start the miner or the GPU Edge Worker on a Mac. The afterPack hook fails the build if a `miner` or `edge` folder ever gets into the app. The browser-wallet bridge is not built for macOS yet, so the QSDM Hive Wallet extension cannot reach Hive on a Mac.

The app is signed ad hoc (`"identity": "-"`), not with an Apple Developer ID, and it is not notarized. The first time, macOS will not open it with a double-click:

- macOS 12 to 14: right-click (or Control-click) QSDM Hive in Applications, choose **Open**, then **Open** again.
- macOS 15 and newer: open it once, then go to System Settings > Privacy & Security and choose **Open Anyway**.

That is how pull-request and local builds come out. The release build is signed and notarized, and opens with a double-click:

- **Where:** in GitHub, Actions > QSDM Hive macOS > Run workflow, on `main`, with **signed** ticked. It runs only on `main`.
- **Signing:** the job `release-macos` builds the same universal app and signs it with the Developer ID certificate (every binary, `qsdmcli` included, with the hardened runtime and a secure timestamp).
- **Notarization:** electron-builder's own notarization notarizes and staples the app. It gets the app-specific password of the `APPLE_ID_PASS` secret as `APPLE_APP_SPECIFIC_PASSWORD`. `.erb/scripts/notarize.js` then skips, so the app is notarized once. The job then signs, notarizes and staples the `.dmg` as well. Apple usually answers in minutes, but a new team's first submissions can take hours, so the job may run for up to 6 hours (GitHub's maximum for hosted runners). Apple sends no email for these: to see where Apple is, run Actions > QSDM Hive notary status (`.github/workflows/qsdm-hive-notary-status.yml`), which lists the recent submissions with their status and builds nothing.
- **Privacy:** the certificate holder's name is masked in the public log. An individual Apple account's Developer ID carries the person's name.
- **Checks:** it fails unless Gatekeeper's own check (`spctl`) accepts both the app and the `.dmg` as "Notarized Developer ID". It then launches the app.
- **Intel:** the job `release-macos-intel` checks the checksums and Gatekeeper again on an Intel runner, copies the app out of the mounted `.dmg` and launches it.
- **Output:** the `.dmg`, the `.zip` and `SHA256SUMS-mac.txt` are kept for 30 days as the `qsdm-hive-macos-release-<commit>` artifact.

It needs five repository secrets, set by the Apple account's holder and never written anywhere else:

| Secret | What it is |
|---|---|
| `MAC_CSC_LINK` | The Developer ID Application certificate with its private key, exported from Keychain Access as a `.p12`, in base64 (`base64 -i file.p12`). |
| `MAC_CSC_KEY_PASSWORD` | The password the `.p12` was exported with. |
| `APPLE_ID` | The Apple ID of the developer account. |
| `APPLE_ID_PASS` | An app-specific password for that Apple ID (account.apple.com > Sign-In and Security > App-Specific Passwords), used only to notarize. |
| `APPLE_TEAM_ID` | The 10-character Team ID (developer.apple.com > Account > Membership details). |

The job stops before it builds anything when a secret is missing, or when the `.p12` is not a Developer ID Application certificate of that team.

Hive does not update itself on a Mac: hive-v2 has no signed Mac release. A Mac build checks its version against the signed Linux release; when that release names another version, Hive shows the update screen with a link to the download page. The release host does not publish Mac builds yet.

The `QSDM Hive macOS` workflow (`.github/workflows/qsdm-hive-macos.yml`) runs for pull requests that touch Hive. On an Apple silicon and an Intel runner it builds the universal app, runs the macOS specs, starts the packaged app (the smoke test, then a normal start with a screenshot) and keeps the `.dmg` and `.zip` as the `qsdm-hive-macos-<commit>` artifact.