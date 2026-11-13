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

A signed and notarized build needs an Apple Developer ID certificate and the `APPLE_ID`, `APPLE_ID_PASS` and `APPLE_TEAM_ID` values that `.erb/scripts/notarize.js` reads; none of these exist yet.

Hive does not update itself on a Mac: hive-v2 has no signed Mac release. A Mac build checks its version against the signed Linux release; when that release names another version, Hive shows the update screen with a link to the download page. The release host does not publish Mac builds yet.

The `QSDM Hive macOS` workflow (`.github/workflows/qsdm-hive-macos.yml`) runs for pull requests that touch Hive. On an Apple silicon and an Intel runner it builds the universal app, runs the macOS specs, starts the packaged app (the smoke test, then a normal start with a screenshot) and keeps the `.dmg` and `.zip` as the `qsdm-hive-macos-<commit>` artifact.