# Hive packaging checks

From this Hive directory, run `npm ci`, then `npm run runtime:install`. The latter installs the locked packages declared in `release/app/package.json` into its own node_modules, skips lifecycle scripts and browser downloads, and does not change dependency versions.

Run `npm run verify:runtime` before packaging. `package:electron` does this before cleaning/building, and electron-builder's beforePack hook also enforces it for direct invocations and Linux scripts. Having a package in the parent development node_modules is insufficient: the production app must carry its own declared runtime dependency tree.

The afterPack hook verifies each runtime package and entry point in app.asar, then starts the packaged Electron executable with ELECTRON_RUN_AS_NODE=1 to require all declared runtime dependencies. It fails packaging on missing transitive dependencies. This probe does not start the Hive application or access the user's profile.

Regression checks: `node --test .erb/scripts/verify-runtime-dependencies.test.cjs`.

A packaged app smoke test with a fresh QSDM_HIVE_APPDATA_ROOT and QSDM_HIVE_SMOKE_TEST=1 is still required before signing, publishing or installing. Verify smoke-result.json reports ok. Do not treat a successful installer build or a valid release signature as evidence the application can start.