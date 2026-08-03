# QSDM Wallet Extension

The extension lets websites use the active wallet in QSDM Hive. It is a secure
provider, not a second wallet vault: it never stores a private key, keystore
JSON, or passphrase.

## User flow

1. A supported website opens `https://qsdm.tech/wallet-start.html?login=new`.
2. When the provider is installed, the website asks the extension to open
   `home.html#/onboarding/welcome?login=new`. It never hard-codes or navigates
   to a browser-specific extension ID.
3. If the provider is missing, the handoff opens the official QSDM extension
   download instead.
4. Select **Use QSDM Hive Wallet** and approve the requesting website in Hive.

The website remains connected to that public address until the user disconnects
it in the extension or revokes it under **Hive > Settings > Wallet > Connected
Sites**. Signatures and CELL transfers always require a fresh Hive approval.

There is no separate extension account, password, recovery phrase, or wallet
import. This avoids creating another copy of the user's wallet secrets.

The onboarding page offers **Telegram** and **Email** through the HTTPS QSDM
Account dashboard. Email uses a short-lived one-time link instead of a reusable
password. Telegram uses Authorization Code + PKCE and server-side ID-token
verification. The extension itself collects neither identifier and never
embeds Telegram, SMTP, or account-service credentials. Google and Apple login
are not included.

QSDM Account synchronizes verified identity and linked public wallet addresses.
It does not synchronize site approvals, private keys, keystores, passphrases,
or recovery phrases. Those remain local to Hive.

## Installation

Packaged Hive releases register the native browser bridge automatically for
the current user. This requires no administrator access. The extension has the
stable Chromium ID `habkkkednignfkoffhpbjahcjbikkahh` and Firefox ID
`qsdm-wallet@qsdm.tech`.

The consumer release path is one named browser-store listing per browser:

- Google Chrome: Chrome Web Store
- Microsoft Edge: Microsoft Edge Add-ons
- Brave: the approved Chrome Web Store listing
- Mozilla Firefox: Firefox Add-ons

Those listings are not published yet. Until approval, Chrome, Edge, Chromium,
and Brave developers can download the advanced Chromium ZIP and install it
once:

1. Open the browser extensions page and enable developer mode.
2. Extract the ZIP, choose **Load unpacked**, and select the extracted folder.
3. Start or restart QSDM Hive.

Chrome, Edge, Chromium, and Brave are supported through that manual flow.
Firefox 128 or newer is supported by the Firefox package, but normal Firefox
releases require Mozilla signing for a persistent installation. The unsigned
Firefox ZIP is a store-submission and temporary-testing artifact, not a normal
consumer installer. Users upgrading from the old random-ID Chromium build
should remove it and load the current package once.

`package-extension.ps1` produces a universal development ZIP, a legacy
Chromium ZIP, explicit Chrome, Edge, and Brave store-submission ZIPs, and a
Firefox store-submission ZIP. The three Chromium-family submission files have
the same verified payload but distinct names so an operator cannot confuse the
target store. The browser-specific packages omit manifest keys that belong only
to the other browser family. See [STORE_SUBMISSION.md](STORE_SUBMISSION.md).

The Chrome Web Store assigned production ID
`homapiejinlbjdhhdegcbnldkpkodepo` during the first upload. Hive authorizes
that ID, the pinned development ID, and interim CRX ID
`nmmhneekhgaegpmbnhiacglhoncicflc` explicitly. Never add a wildcard or an
operator-supplied origin to the native-host manifest.

A self-hosted CRX is not the general Windows installer: consumer Chrome on
Windows and macOS accepts direct extension installation only through the Chrome
Web Store. Edge and managed Chromium deployments can use CRX packages, while
normal Firefox releases require a Mozilla-signed XPI. QSDM therefore keeps ZIP
archives under Advanced manual installation and activates each named store
button only after that store has approved the release.

The separately signed interim CRX is for Linux Chromium and managed-browser
deployment only. Build it with `package-crx.ps1` and a protected private key;
never commit or publish the PEM file. Its Linux self-update feed is
`https://qsdm.tech/downloads/qsdm-hive-wallet-extension-updates.xml`.

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

Supported website methods are `qsdm_requestAccounts`, `qsdm_accounts`,
`qsdm_getBalance`, `qsdm_signMessage`, `qsdm_sendTransaction`, and
`qsdm_disconnect`. `qsdm_openOnboarding` is an extension-local navigation
request: it opens the internal onboarding page and never reaches Hive or QSDM
Core.

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
