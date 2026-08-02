# QSDM Wallet Extension

The extension lets websites use the active wallet in QSDM Hive. It is a secure
provider, not a second wallet vault: it never stores a private key, keystore
JSON, or passphrase.

## User flow

1. Create or import a wallet once in **QSDM Hive > Settings > Wallet**.
2. Keep Hive running in the notification area.
3. Open `https://qsdm.tech/wallet.html` or another supported website and select
   **Connect Hive wallet**.
4. Approve the website once in Hive.

The website remains connected to that public address until the user disconnects
it in the extension or revokes it under **Hive > Settings > Wallet > Connected
Sites**. Signatures and CELL transfers always require a fresh Hive approval.

There is no separate extension account, password, recovery phrase, or wallet
import. This avoids creating another copy of the user's wallet secrets.

## Installation

Packaged Hive releases register the native browser bridge automatically for
the current user. This requires no administrator access. The extension has the
stable Chromium ID `habkkkednignfkoffhpbjahcjbikkahh` and Firefox ID
`qsdm-wallet@qsdm.tech`.

Until the extension is published in browser stores, Chrome, Edge, Chromium, and
Brave users can download the Chromium ZIP and install it once:

1. Open the browser extensions page and enable developer mode.
2. Extract the ZIP, choose **Load unpacked**, and select the extracted folder.
3. Start or restart QSDM Hive.

Chrome, Edge, Chromium, and Brave are supported through that manual flow.
Firefox 128 or newer is supported by the Firefox package, but normal Firefox
releases require Mozilla signing for a persistent installation. The unsigned
Firefox ZIP is a store-submission and temporary-testing artifact, not a normal
consumer installer. Users upgrading from the old random-ID Chromium build
should remove it and load the current package once.

`package-extension.ps1` produces a universal development ZIP plus separate
Chromium and Firefox store-submission ZIPs. The browser-specific packages omit
manifest keys that belong only to the other browser family.

A self-hosted CRX is not the general Windows installer: consumer Chrome on
Windows and macOS accepts direct extension installation only through the Chrome
Web Store. Edge and managed Chromium deployments can use CRX packages, while
normal Firefox releases require a Mozilla-signed XPI. QSDM therefore presents
one Chrome/Edge-family download today and will route the install action to each
browser's store after approval.

The scripts in `native-host` remain available for development diagnostics;
normal packaged installs do not require running them manually.

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
