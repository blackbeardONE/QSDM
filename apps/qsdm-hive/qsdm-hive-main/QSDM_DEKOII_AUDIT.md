# QSDM Hive Koii Dependency Audit

Status date: 2026-06-02

## Fixed Now

- Account creation no longer uses `@_koii/sdk/k2` or `K2Tool.importWallet`.
- Seed phrase import uses the same local wallet creation path, so it no longer needs `https://testnet.koii.network` to create/import an account.
- PIN reset now derives the main wallet locally from the seed phrase.
- The runtime package no longer declares `@_koii/sdk`.
- The app package and lock file no longer declare external Koii/Solana packages.
- Stale installed package folders for `@_koii/*`, `@koii-network/*`, and `@solana/*` were removed from `node_modules`; `npm ls` now reports no installed external Koii/Solana packages.
- Runtime URLs for task RPC, explorer links, IPFS fallback gateways, VIP/referral, S3, token metadata, and dynamic DNS now point at QSDM-owned or neutral public infrastructure instead of Koii services.
- Hive now reports `CELL` as both the display token and protocol token.
- Internal source names for token/base-unit helpers, task-type helpers, transfer endpoints, theme keys, dropdown test IDs, and local task metadata were renamed to QSDM/CELL language.
- Remaining old brand asset filenames were renamed to QSDM filenames.
- The local Web3 compatibility adapter was renamed to `qsdmWeb3Adapter`; external Koii/Solana package scans remain empty.
- App runtime version bumped to `1.3.17`.

## Still Compatibility-Bound

Hive is no longer package-coupled to external Koii/Solana libraries and the non-asset source/package scans are clean for old brand/package/service names. Remaining compatibility surfaces are implementation-level rather than consumer-facing:

- CSS class names and UI copy that still reference Finnie/Koii concepts
- Local compatibility shims under `src/vendor/qsdm-chain` still expose Web3-like primitives while the QSDM-native action loop replaces the old transaction path.
- A few neutral base64 image blobs may contain accidental text matches; they are not service URLs, package names, imports, or visible UI copy.

## Migration Target

To make Hive genuinely QSDM-owned, continue replacing inherited CSS/class naming and visual debt while preserving compatibility at the data boundary. Cosmetic cleanup should happen after the QSDM gateway/core task flows stay stable under restart, stake, and reward verification.
