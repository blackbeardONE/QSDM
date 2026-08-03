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

There is no QSDM Account password in this design. Email magic links remove a
reusable password database while still requiring control of the mailbox.

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
`Cache-Control: no-store` on account APIs.

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

Then install the updated Caddy route and use the fail-closed installer:

```bash
sudo QSDM/deploy/scripts/install_account_service.sh \
  ./qsdm-account ./account.conf
```

Do not enable the public dashboard until the health check succeeds and at least
one provider reports enabled from `/api/account/config`.

After Caddy is reloaded, verify both the loopback service and its public route
without exposing configuration secrets:

```bash
sudo /opt/qsdm/verify-account-service
```

CI performs the same tests, vet, stripped Linux builds, Windows cross-build,
checksums, and local health smoke in `.github/workflows/qsdm-account.yml`.

### Production activation gate

The verifier proves that the process, configuration shape, and reverse proxy
are healthy. It cannot prove ownership of an SMTP account or Telegram bot. Do
not expose `/account/` as a finished product until an operator has also:

1. completed a real sign-in through every enabled provider;
2. received and consumed an email magic link when email is enabled;
3. completed Telegram's callback when Telegram is enabled;
4. linked and unlinked a test QSDM wallet through a visible Hive approval; and
5. backed up and test-restored the encrypted store and its separately held key.

If any check fails, leave the service stopped and keep the account route out of
the active Caddy configuration. Never replace a failed provider with a test
credential or a hard-coded bypass.

## Backup and recovery

Back up `/var/lib/qsdm-account/accounts.json` and the data-encryption key as two
separately protected items. Both are required to recover account identity data.
They are not wallet backups and cannot recover or spend CELL.

## Current scope

The first release supports sign-in, attaching email and Telegram as alternate
methods for one account, sign-out, public wallet linking and unlinking, public
balance display, and local Hive wallet management. Automatic merging of old
duplicate profiles, Google and Apple sign-in, cloud wallet custody, and
automatic synchronization of website approvals are deliberately excluded.
