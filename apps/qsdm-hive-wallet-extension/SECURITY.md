# QSDM Hive Wallet Extension Security

## Custody boundary

The browser extension is a provider transport, not a wallet vault. It never
stores or receives a private key, keystore JSON, or passphrase. Wallet creation
and import happen only in **QSDM Hive > Settings > Wallet**.

Hive stores the encrypted ML-DSA keystore in its private application-data
directory. On supported operating-system secret backends, the working
passphrase is encrypted with Electron `safeStorage` and materialized as a
private, process-lifetime temporary file only when the native CLI signer needs
it. Hive removes those temporary files during shutdown.

## Browser boundary

- The source manifest pins development extension ID
  `habkkkednignfkoffhpbjahcjbikkahh`; Hive registers only explicitly trusted
  development, interim CRX, and store IDs.
- Packaged Hive releases refresh current-user native-host registration on each
  start. Registration does not require administrator access.
- The extension requests `nativeMessaging`, `activeTab`, and `scripting`.
  `activeTab` and `scripting` provide temporary provider injection only after
  the user clicks the extension on a website outside the built-in allowlist.
- Automatic provider injection is restricted to QSDM, QSDM Online, and Sky
  Fang HTTPS domains. The Chrome Web Store package has no broad host pattern.
- Websites receive `window.qsdm`, which exposes a fixed allowlist of methods.
- Remote sites must use HTTPS. Plain HTTP is accepted only on loopback hosts
  for local development.
- Site access is scoped to the exact origin and active wallet address.
- Connections can be reviewed and revoked in Hive under **Connected Sites**.
- Connecting, signing, and sending CELL require a visible Hive approval.

## Native bridge

The browser starts `qsdm-hive-wallet-host` through the browser's native
messaging facility. The host accepts length-prefixed JSON on standard input and
forwards it only to a random loopback port owned by Hive. Hive authenticates
the host with a fresh 256-bit token stored in a private per-user file and
rotated on every Hive start. The broker does not bind to a LAN or public
interface.

Native-host manifests allow only explicitly trusted extension IDs. The Chrome
Web Store upload package omits the development `manifest.key`, so Chrome can
assign the store item ID. That assigned ID must be added to Hive's allowlist
before publication. Hive must never use a wildcard browser-extension origin.

## Explicit limitations

This design does not protect a wallet after the operating-system account or
Hive process itself is compromised. A user must still read approval prompts;
approving a malicious message or transfer authorizes that exact operation.
Browser-store publication and release signing are separate supply-chain
controls and must be completed before calling the extension a public release.
