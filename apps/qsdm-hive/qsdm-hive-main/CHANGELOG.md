## [1.4.2]

- Capacity reporting: Mother Hive now distinguishes each Agent's offered CPU, GPU, and RAM capacity from Relay per-job safety limits.
- RAM sharing: Agent RAM advertisements can represent the configured percentage of real system memory instead of being capped at one GiB.
- Relay clarity: Edge Control shows the real RAM amount next to its percentage and labels CPU/GPU controls as per-job allowances.
- Bundled tools: Windows and Linux packages include QSDM Edge Agent and Edge Control 1.3.7 with the corrected aggregate-capacity model.
- Security: Updated the Go support modules to include the fix for GO-2026-5970 in `golang.org/x/text`.

## [1.4.1]

- Multi-Hive Relay: One local Relay can now serve several named QSDM Hives with separate, revocable credentials.
- Tenant isolation: Jobs, request IDs, cancellation, receipts, payout bindings, settlement proofs, and acknowledgements are isolated per Mother Hive.
- Safer revocation: Revoking one Hive cancels its unfinished work without disconnecting other Hives or Agents.
- Pairing UX: Edge Control creates `QSDM-EDGE-3` codes and shows every paired Hive; Hive displays the paired name and identity.
- Compatibility: Existing single-Hive Relay setups continue through the legacy tenant while operators migrate to named pairing codes.

## [1.4.0]

- Unified wallet: The active wallet in Hive is now presented as the user's single QSDM account across Hive and connected websites; the extension never creates or stores a second wallet.
- One-time setup: Hive automatically registers its secure browser bridge for the current Windows or Linux user without administrator access or manual extension-ID configuration.
- Stable identity: The bundled Chromium extension has a pinned ID, and Hive validates that ID before registering the native host.
- Website connections: The extension popup shows the current site and supports direct connect or disconnect, while Hive retains exact-origin permissions and separate approvals for signatures and CELL transfers.
- Package hardening: Hive bundles only the browser extension runtime, security notes, and diagnostic registration helpers instead of old archives and test assets.

## [1.3.99]

- Browser wallet: Added the QSDM Hive Wallet extension and a localhost-only, authenticated wallet-provider broker for permissioned website connections and transaction approvals.
- Wallet security: Browser code never receives or stores the QSDM keystore or passphrase; Hive retains signer custody and uses OS-protected local secret storage.
- Wallet recovery: An unreadable OS-protected passphrase is quarantined without deleting the keystore, Hive continues starting, and the existing wallet can be unlocked with its passphrase without re-uploading the JSON file.
- Native messaging: Bundled the QSDM browser native host and provider assets with Windows and Linux packages so installed Hive releases can serve the extension without source-tree dependencies.
- Developer runtime: Added an Electron 43-compatible CommonJS bootstrap for TypeScript path aliases during local development.

## [1.3.98]

- Linux release validation: Configure Electron's packaged `chrome-sandbox` helper with the required root ownership and setuid permissions in CI before running the hidden renderer smoke test. Sandbox enforcement remains enabled.

## [1.3.97]

- Runtime security: Upgraded the embedded desktop runtime from end-of-life Electron 23 to supported Electron 43.1.1, with Node 22.12+ and electron-builder 26.15.3 as the release baseline.
- Dependency security: Removed legacy `web3.storage`, `localtunnel`, AWS SDK v2, and browser crypto compatibility paths; the complete Hive and packaged-runtime graphs now pass a live audit against the official npm registry with zero advisories.
- Network safety: Legacy local-tunnel setup now fails closed and leaves public reachability to the QSDM home or canonical gateway instead of installing an unmanaged tunnel client.
- Sandbox compatibility: Migrated user-selected branding paths to Electron's preload-only `webUtils` API and aligned controller event types with current Electron definitions.
- Release validation: Windows and Linux workflows now launch the packaged renderer and require a mounted React root plus the sandboxed preload bridge before uploading artifacts.
- Release operations: Authenticode signing remains explicitly opt-in through `SIGNPATH_ENABLED=true`, while QSDM-native ML-DSA release verification remains mandatory.

## [1.3.96]

- Release integrity: Hive now authenticates atomic Windows and Linux release envelopes with a pinned QSDM ML-DSA-87 public key.
- Update safety: Updater metadata and downloaded installers must match the signed filename, platform, size, SHA-256, version, and validity window before installation.
- Release operations: Added offline-key initialization, deterministic manifest signing, immediate signature verification, and fail-closed publisher gates for both platforms.
- Version policy: Exact-version enforcement now derives its approved version only from an authenticated QSDM release manifest.

## [1.3.95]

- Release channel: Publishes Windows 1.3.95 on an isolated manifest; automatic updates remain disabled until the manual transition after SignPath publisher approval.
- Packaging: Host-native builds now rebuild bundled tools, reject stale miner or Edge versions, and block partial direct Electron publishing.
- Runtime cleanup: Removed the disabled Grok browser-automation flow and its obsolete native `sleep` dependency chain.
- Mother Hive federation: Added expiring QSDM-EDGE-2 HTTPS invitations with derived credentials, random offer IDs, immutable context validation, workload allowlists, and automatic one-time migration away from permanent legacy federation credentials.
- Mother Hive recovery: Expired or damaged Relay pairings can always be disconnected and replaced.
- Release alignment: Bundles Edge Control and Agent 1.3.5 and publishes matching Windows/Linux metadata.
- Website: Fixed narrow-screen header and hero overflow and aligned active product documentation with the shipped versions.

## [1.2.1]

- Task Upgrades: Allow upgrading tasks even with low balance if the previous version is still staked.
- VIP Status: Automated adjustments based on real-time VIP token balance.
- Task Fetching: Improved reliability when remote servers are temporarily unavailable.
- UI Enhancements: Fixed notification tray overlaps and ensured version number visibility on all window sizes.
- Onboarding: Refined UI for a smoother initial setup.
- Metadata Source: Updated task metadata sourcing for better availability.
- Zoom Level: Enforced consistent zoom level across the app.

## [1.2.0]

- Notifications: Introduced a modernized notification style with improved spacing.
- Rewards Distribution: Enhanced logic for more reliable task rewards distribution.

## [1.1.5]

- Task Extension Helper: Introduced a tool for quick and secure retrieval of API keys, starting with support for Yifojjeth tasks.

## [1.1.4]

- Bonus Task View: Added a dedicated view for ended Bonus task seasons.
- KPL Balances: Enabled vertical scrolling for extensive KPL token lists.
- Markdown Support: Task descriptions now support Markdown formatting.
- VIP Theme Fixes: Corrected thumbnail shadow issues in VIP theme.

## [1.1.3]

- Orca Logs: Optimized to reduce log file size by saving only key logs.
- Wallet Display: Active wallet now expands by default for better visibility.
- Network Tunneling: Auto-enforced for secure connections during networking features.
- KPL Tokens: Implemented fallback display for tokens without metadata.
- Memory Logging: Added logging for memory usage to aid in testing and optimization.
- Task Variables: Enabled auto-save in the background.
- UX Enhancements: Improved token item interactions and redesigned the Claim Rewards button with a glowing animation.

## [1.1.2]

- Task Upgrade Flow: Streamlined UI for upgrading tasks.
- Extension Repair: Simplified process to restore proper variable pairings in tasks.
- Onboarding: Rolled out improvements for a smoother user experience.
- VIP Theme: Fixed notification contrast issues.
- Alerts: Prevented duplicate executable modified alerts.
- KPL Rewards: Adjusted UX for clearer display of miner rewards.

## [1.1.1]

- Notifications: Resolved missing titles in external notifications.
- Orca Enhancements: Improved installation process and status detection for Mac and Linux users.
- RPC Status Widget: Redesigned for clarity and consistency.
- Onboarding Flow: Polished for a better first-time experience.
- UI Touch-ups: Various subtle improvements across the app.

## [1.1.0]

- ORCA Add-on: Introduced a sandboxed environment for secure task execution.
- VIP Skin: Launched an exclusive app skin for VIP users.
- Notifications: Enhanced layout for externally triggered notifications.
- Staking Banner: Added a bottom banner for Haji.ro staking updates.
- UI Fixes: Polished various elements for improved user experience.

## [1.0.4]

- Task States: Implemented caching to reduce RPC node load.
- Bonus Task: Prepared groundwork for a new Bonus Task feature.

## [1.0.3]

- KPL Rewards UX: Adjusted progress bar for clearer reward distribution.
- Banner State: Made bottom banner state persistent across sessions.
- Task Restart: Added a restart option for faulty tasks.
- Animations: Introduced subtle animations for smoother interactions.

## [1.0.2]

- Stake Modal: Fixed predefined stake value issue.
- CELL Balances: Displayed dollar values using CoinGecko API with caching.
- Rewards Bar: Resolved negative value display and added "Reconnecting" state.
- Responsiveness: Improved layouts for various window sizes.
- Navbar Layout: Fixed misalignment issues for new users.

## [1.0.1]

- Migration Flow: Enhanced UX for users with CELL tokens but insufficient KPL balance.
- Bug Fixes: Resolved issues with node initialization, referral banner, and balance display.
- UI Updates: Removed token launch counter widget and re-added task search filters.
- Network Switch: Enabled mainnet switch for ready users.

## [1.0.0]

- Mainnet Migration: Transitioned Desktop Node to mainnet, including token migration and vesting.
- Transaction Checks: Added confirmation checks for staking, unstaking, and claiming.
- Task Visibility: Restored switch to show/hide non-verified tasks.
