# QSDM Account and Wallet Sync

Status: implementation contract; consumer account service is not deployed.

QSDM Account will make the same wallet easier to use across the web wallet,
QSDM Hive, and approved websites. It must not turn QSDM into a custodial
wallet service.

## Current behavior

- The web wallet stores an AES-256-GCM encrypted QSDM keystore in the current
  browser's local storage.
- QSDM Hive stores the active keystore and signs locally after user approval.
- The browser extension connects websites to Hive. It does not store a
  keystore or passphrase.
- QSDM Core's existing address/password login protects validator dashboard
  APIs. It is not a consumer identity service, and the public home gateway
  intentionally does not expose it.
- Email login, Telegram login, cross-device vault sync, and password-reset
  email are not deployed yet.

## Product decision

An account login authenticates the user to the sync service. It does not
decrypt the wallet and does not authorize a CELL transfer.

The sync service may store:

- the user's verified email address or Telegram subject;
- a public QSDM wallet address;
- an opaque, versioned encrypted-keystore blob;
- registered device public keys and revocation state;
- security events and session metadata.

The sync service must never receive or store:

- a QSDM private key;
- a wallet passphrase;
- recovery words;
- an unencrypted keystore;
- a signing approval that can be replayed for another origin or action.

Resetting an account password restores access to the encrypted backup, not to
the funds. The wallet passphrase, QSDM recovery words, or an already paired
Hive device is still required to unlock the wallet.

## User flow

### First device

1. Sign in with a verified email and password, or with Telegram.
2. Create a QSDM wallet or import an existing wallet locally.
3. Confirm a wallet passphrase and recovery backup.
4. Upload only the encrypted keystore and public wallet address.

### Additional device

1. Sign in to the same QSDM Account.
2. Download the encrypted keystore.
3. Unlock it with the wallet passphrase, recovery words, or approval from a
   paired Hive device.
4. Register a new per-device public key.

### Connected website

1. The website requests the active public wallet through the QSDM extension.
2. The extension identifies the exact website origin.
3. Hive displays the requested action and origin.
4. Hive signs locally only after approval.

Account login alone never satisfies steps 3 or 4.

## Authentication

### Email and password

The account service requires all of the following before public activation:

- verified email ownership;
- Argon2id password hashing with per-user salts;
- generic login and reset responses that do not reveal whether an account
  exists;
- rate limits by account and network source;
- short-lived, rotating sessions in `Secure`, `HttpOnly`, `SameSite` cookies;
- CSRF protection for state-changing browser requests;
- one-time, expiring email verification and password-reset tokens stored only
  as hashes;
- session and device revocation;
- an outbound transactional email provider. Email forwarding for
  `ops@qsdm.tech` is not an outbound mail service.

### Telegram

Use Telegram OpenID Connect Authorization Code Flow with PKCE. A QSDM Telegram
bot must be configured in BotFather with the exact QSDM origins and callback
URLs. The backend exchanges the authorization code, verifies the ID-token
signature against Telegram's JWKS, and validates issuer, audience, expiry,
nonce, and state before creating a QSDM session.

Telegram identity is an account-login method only. It cannot sign a QSDM
transaction or replace wallet recovery.

## Encrypted vault contract

The uploaded object is opaque to the account service:

```json
{
  "version": 1,
  "wallet_address": "<64 lowercase hex characters>",
  "keystore_format": "qsdm-keystore",
  "ciphertext": "<client-encrypted keystore blob>",
  "revision": 1,
  "updated_at": "<server timestamp>"
}
```

Writes use an expected revision or ETag so two devices cannot silently replace
one another's newer backup. The server enforces a small maximum blob size,
content-type checks, per-account quotas, and an immutable security audit event
for every create, replace, download, and delete operation.

## Service boundary

Consumer identity must be a separate service, for example
`accounts.qsdm.tech`. Do not add consumer passwords, Telegram tokens, or vault
blobs to validator consensus state. Validators verify signed wallet actions;
they do not need a user's email or Telegram identity.

Initial API surface:

```text
POST   /v1/accounts/register
POST   /v1/accounts/login
POST   /v1/accounts/logout
POST   /v1/accounts/email/verify
POST   /v1/accounts/password/reset/start
POST   /v1/accounts/password/reset/finish
GET    /v1/accounts/telegram/start
GET    /v1/accounts/telegram/callback
GET    /v1/session
GET    /v1/vault
PUT    /v1/vault
GET    /v1/devices
POST   /v1/devices/pair
DELETE /v1/devices/{device_id}
```

## Release gates

Do not add working login buttons to the public wallet until these gates pass:

1. A dedicated account service and persistent database are deployed with
   encrypted backups.
2. Outbound verification/reset email is operational.
3. A QSDM Telegram bot, OIDC client, and exact callback allowlist are active.
4. Session, CSRF, rate-limit, account-lockout, vault-conflict, and device-
   revocation tests pass.
5. An external security review covers account takeover, vault replacement,
   recovery abuse, and signing-origin confusion.
6. The privacy policy and incident runbook cover the new account data.

Until then, the secure production paths remain the local web-wallet vault and
the Hive-backed browser extension.
