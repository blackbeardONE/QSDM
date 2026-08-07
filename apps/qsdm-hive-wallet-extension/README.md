# QSDM Wallet Extension

The extension lets websites use the active wallet in QSDM Hive. It is a secure
provider, not a second wallet vault: it never stores a private key, keystore
JSON, or passphrase.

## User flow

1. Create or import a wallet once in **QSDM Hive > Settings > Wallet**.
2. Keep Hive running in the notification area.
3. Open a supported website and select **Connect QSDM Wallet**.
4. Open the QSDM extension once if the site is outside the QSDM, QSDM Online,
   or Sky Fang domains.
5. Approve the website once in Hive.

The website remains connected to that wallet until the user disconnects it in
the extension or revokes it under **Hive > Settings > Wallet > Connected
Sites**. Signatures and CELL transfers always require a fresh Hive approval.

There is no separate extension account, password, recovery phrase, or wallet
import. This avoids creating another copy of the user's wallet secrets.

## Installation

Packaged Hive releases register the native browser bridge automatically for
the current user. This requires no administrator access. The extension has the
stable Chromium ID `habkkkednignfkoffhpbjahcjbikkahh`.

Until the extension is published in browser stores, install it once:

1. Open the browser extensions page and enable developer mode.
2. Choose **Load unpacked** and select the bundled `wallet-extension` folder.
3. Start or restart QSDM Hive.

Chrome, Edge, Chromium, and Brave are supported. Users upgrading from the old
random-ID development build should remove it and load the current bundled
extension once. Daily use is automatic after that migration.

The scripts in `native-host` remain available for development diagnostics;
normal packaged installs do not require running them manually.

Official QSDM, QSDM Online, and Sky Fang pages receive the provider
automatically. Other HTTPS pages receive temporary access only after the user
clicks the extension for that tab. The store build does not request blanket
access to every website.

### Chrome Web Store package

Create the upload ZIP with:

```powershell
pwsh -NoProfile -File .\package-extension.ps1
```

The source manifest keeps a development key so **Load unpacked** has a stable
ID. The packaging script removes that key because Chrome Web Store assigns its
own production ID and rejects upload packages containing `manifest.key`.

The Chrome Web Store assigned production ID
`homapiejinlbjdhhdegcbnldkpkodepo` during the first upload. Hive authorizes
that ID, the pinned development ID, and interim self-hosted CRX ID
`nmmhneekhgaegpmbnhiacglhoncicflc` explicitly. Do not add wildcard or
operator-supplied origins to the native-host allowlist; accepting arbitrary
extension IDs would weaken the wallet boundary.

The CRX is only for Linux Chromium or managed-browser deployment. Normal
Windows and macOS Chrome installations must use the Chrome Web Store. Build
the CRX with the separately protected private key; never commit that key:

```powershell
pwsh -NoProfile -File .\package-crx.ps1 `
  -PrivateKeyPath C:\secure\qsdm-wallet-interim.pem
```

## Website API

```js
const [address] = await window.qsdm.request({
  method: "qsdm_requestAccounts",
});

const signature = await window.qsdm.request({
  method: "qsdm_signMessage",
  params: { message: "QSDM ownership challenge" },
});
```

Supported methods are `qsdm_requestAccounts`, `qsdm_accounts`,
`qsdm_getBalance`, `qsdm_signMessage`, `qsdm_sendTransaction`, and
`qsdm_disconnect`.

## Verification

After building the Windows native tools, run:

```powershell
node tests/run-acceptance.mjs
```

The isolated test validates the pinned extension ID, provider, native host,
popup, permissions, signing request, transfer request, and disconnect flow. It
does not open a private wallet or broadcast CELL.

With Hive running, this read-only probe checks the live local bridge:

```powershell
node tests/probe-live-broker.mjs
```
