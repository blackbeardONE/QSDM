# QSDM Hive Wallet Extension

QSDM Hive Wallet exposes a small `window.qsdm` provider to HTTPS websites.
The extension never stores a QSDM private key, keystore JSON, or passphrase.
All privileged operations are forwarded through the browser native-messaging
API to QSDM Hive and require approval in Hive.

## Wallet creation and import

The extension is deliberately not a second wallet vault. Users open
**QSDM Hive > Settings > Wallet**, then either create a native QSDM wallet or
import an encrypted QSDM keystore JSON with its passphrase. Hive keeps the
active signer in operating-system-protected storage where available. The
extension discovers only the active public address and asks Hive to approve
each connection, signature, or CELL transfer.

This means users import a wallet once in Hive. They do not upload the keystore
to every website or browser profile, and websites never receive the keystore,
private key, or passphrase.

## Development install

1. Build the `qsdm-hive-wallet-host` native executable through the normal Hive
   Windows or Linux native build.
2. Open the browser extensions page, enable developer mode, and load this
   directory as an unpacked extension.
3. Copy the generated extension ID.
4. Run `native-host/install-windows.ps1 -ExtensionId <id>` on Windows or
   `native-host/install-linux.sh <id>` on Linux from the packaged extension
   resource directory.
5. Start QSDM Hive and open the extension popup.

This first package targets Chromium browsers (Chrome and Edge). Production
distribution must use a fixed Chrome Web Store or Edge Add-ons ID.
The native-host manifest must allow only that published extension ID.

## Windows acceptance test

Run the isolated end-to-end check after building the Windows native tools:

```powershell
node tests/run-acceptance.mjs
```

The test launches Chrome or Edge with a temporary profile, discovers the
unpacked extension ID, registers the native host for the current user, and
exercises the real extension and native-messaging executable against a local
mock broker. It verifies connect, account lookup, balance, message signing,
transaction forwarding, disconnect, unsupported-method rejection, account
events, and the popup. No private wallet is opened and no CELL is broadcast.
Use `--browser <path>` to choose a browser or `--headful` if a browser build
does not permit extensions in unified headless mode.

With QSDM Hive running, verify the compiled native host against the live local
broker without requesting a signature or broadcasting CELL:

```powershell
node tests/probe-live-broker.mjs
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
