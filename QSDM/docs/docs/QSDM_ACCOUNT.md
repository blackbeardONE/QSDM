# QSDM Account

QSDM Account is the optional identity layer for the QSDM website and supported
integrations. It gives a user one dashboard for verified identity and linked
public CELL wallet addresses without turning the website into a wallet vault.

## User flow

1. Open `https://qsdm.tech/account/` directly or select **Continue with
   Telegram** or **Continue with Email** in the QSDM Wallet extension.
2. Verify the identity. Email sends a short-lived one-time link; Telegram uses
   its OpenID Connect Authorization Code flow with PKCE.
3. Select **Link active wallet**. The browser extension asks QSDM Hive to sign
   a five-minute, account-bound ownership challenge.
4. Hive displays the signing approval. After approval, the account dashboard
   stores the public wallet address and can show its public CELL balance.

After the first sign-in, the **Sign-in methods** section can attach the other
provider to the same account. For example, a profile first opened through an
email link can add Telegram without creating a second wallet dashboard. Email
verification links are bound to the active account. Telegram linking is bound
to both the active browser session and its CSRF token before the OIDC flow
starts.

QSDM does not silently merge existing profiles. If an email address, Telegram
identity, or wallet is already linked to another account, the operation stops
with a conflict and neither account is changed. This prevents an identity
provider login from transferring wallets or account data implicitly.

A user can unlink that public address from the dashboard at any time. Unlinking
does not alter the local keystore or move CELL.

The **Security and devices** section lists active browser-session dates without
exposing cookie values or stored token hashes. A user can sign out every other
browser while keeping the current one active. Each account can have at most 10
active browser sessions; a successful new sign-in removes the oldest session
when that limit is reached. The account can also be deleted without contacting
support by typing `DELETE`. Account deletion removes the identity, all browser
sessions, pending account email links, pending Telegram link flows, and public
wallet links. It does not delete Hive, a keystore, or CELL, and it cannot erase
accepted ledger transactions.

There is no QSDM Account password in this design. Email magic links remove a
reusable password database while still requiring control of the mailbox. A new
email sign-in link invalidates the older pending link for that email. A new
email identity-link request invalidates the older pending request for that
account.

## Security boundary

QSDM Account stores:

- an opaque account ID;
- encrypted email or Telegram display values;
- keyed hashes used to find identities;
- keyed hashes of short-lived login tokens and browser sessions; and
- linked public wallet addresses and timestamps.

It never stores:

- an ML-DSA private key or keystore JSON;
- a wallet passphrase or recovery phrase;
- a transaction-signing capability; or
- a website's local Hive approval.

The account service accepts a wallet link only when the submitted public key
derives the claimed QSDM address and its ML-DSA signature verifies over the
exact one-time challenge. One public wallet cannot be attached to two accounts.
Likewise, one email or Telegram identity cannot be attached to two accounts.

Sessions use `Secure`, `HttpOnly`, `SameSite=Lax` cookies. State-changing API
calls also require the account's CSRF token. The service binds to loopback
behind Caddy, applies request-size limits and rate limits, and returns
`Cache-Control: no-store` on account APIs. Short-lived Telegram login state and
wallet-link challenges are kept only in memory, expired on access, and bounded
to 4,096 records each. A new account-bound Telegram flow or wallet challenge
replaces the older record for that account.

Session revocation and account deletion are persisted before the service tells
the browser they succeeded. If the encrypted account store cannot be updated,
the in-memory removal is rolled back and the operation fails closed.

## Operator setup

Build the separate service from `QSDM/source`:

```bash
CGO_ENABLED=0 go build -trimpath -o qsdm-account ./cmd/qsdm-account
```

Install the binary, the unit in `QSDM/deploy/systemd/qsdm-account.service`, and
a private copy of `qsdm-account.conf.example`. Generate the at-rest encryption
key outside Git:

```bash
openssl rand -base64 32
```

Store that result as `QSDM_ACCOUNT_DATA_KEY` in `/etc/qsdm/account.conf` with
mode `0600`. Configure at least one login provider:

- **Email:** SMTP submission with STARTTLS, sender address, and credentials.
- **Telegram:** create or select the QSDM bot in BotFather, open \*\*Bot Settings
  > Web Login\*\*, and allow
  > `https://qsdm.tech/api/account/telegram/callback`. Keep the displayed client
  > ID and secret only in the server environment file. Retain the default RS256
  > signing algorithm used by this verifier.

Before installation, the installer runs the candidate binary in
`--check-config` mode through a transient systemd unit. This applies the real
`EnvironmentFile` parsing rules, rejects obvious weak encryption keys, and
proves that an existing encrypted account store opens with the supplied key.
It does not bind a port or print any provider secret. A failed preflight leaves
the currently installed service unchanged.

Account store version 2 includes an authenticated encrypted key-check record.
Version 1 stores remain readable after every encrypted identity field validates
with the configured key; the next successful account change upgrades the file
to version 2. Atomic replacement keeps the previous file intact when a save
fails, including on Windows. Store loading also rejects duplicate account IDs,
identity ownership, wallet ownership, token hashes, and records that reference
missing accounts. Back up the store before the first upgrade.

Install the service first. This intentionally does not expose its public API:

```bash
sudo QSDM/deploy/scripts/install_account_service.sh \
  ./qsdm-account ./account.conf
```

Do not enable the public dashboard until the health check succeeds and at least
one provider reports enabled from `/api/account/config`.

Activate the public route only after the local check succeeds:

```bash
sudo /opt/qsdm/install-account-proxy-route
```

This command merges only the account route and redirect into the live
Caddyfile, preserving server-only imports and unrelated routes. It validates a
candidate before replacing the file, keeps a uniquely named backup, reloads
Caddy, and restores the previous file automatically if reload or public
verification fails. Re-running it is safe and verifies the existing route.

The activation command verifies both the loopback service and public route
without exposing configuration secrets. It can also be run directly later:

```bash
sudo /opt/qsdm/verify-account-service
```

For the production activation test, request a real email link and validate the
Telegram OIDC route when those providers are enabled:

```bash
sudo /opt/qsdm/verify-account-service \
  --activation-email operator-test@example.org \
  --check-telegram
```

Omit the option for any provider that is intentionally disabled. The email
check succeeds only after the SMTP server accepts the real message. The
Telegram check validates the authorization destination, PKCE parameters,
callback URL, and live RS256 signing-key set. Neither check completes the user
interaction: consume the email link and complete one Telegram login manually.

CI performs the same tests, vet, stripped Linux builds, Windows cross-build,
checksums, and local health smoke in `.github/workflows/qsdm-account.yml`.

### Production activation gate

The standard verifier proves that the process, configuration shape, reverse
proxy, and public security headers are healthy. Its provider options also
prove SMTP acceptance and Telegram routing/key availability. They cannot prove
that a human controls the target inbox or Telegram identity. Do not expose
`/account/` as a finished product until an operator has also:

1. completed a real sign-in through every enabled provider;
2. received and consumed an email magic link when email is enabled;
3. completed Telegram's callback when Telegram is enabled;
4. linked and unlinked a test QSDM wallet through a visible Hive approval; and
5. revoked a second test browser session and confirmed the current one remains;
6. deleted a disposable test account and confirmed its sessions and public
   wallet links no longer work; and
7. backed up and test-restored the encrypted store and its separately held key.

If any check fails, leave the service stopped and keep the account route out of
the active Caddy configuration. Never replace a failed provider with a test
credential or a hard-coded bypass.

## Backup and recovery

Back up `/var/lib/qsdm-account/accounts.json` and the data-encryption key as two
separately protected items. Both are required to recover account identity data.
They are not wallet backups and cannot recover or spend CELL.

## Current scope

The first release supports sign-in, attaching email and Telegram as alternate
methods for one account, sign-out, browser-session review and revocation,
self-service account deletion, public wallet linking and unlinking, public
balance display, and local Hive wallet management. Automatic merging of old
duplicate profiles, Google and Apple sign-in, cloud wallet custody, and
automatic synchronization of website approvals are deliberately excluded.
