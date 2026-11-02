# QSDM Account

QSDM Account is the optional identity layer for the QSDM website and supported
integrations. It gives a user one dashboard for verified identity and linked
public CELL wallet addresses without turning the website into a wallet vault.

> **Sign-in today:** wallet sign-in with QSDM Hive is the only login method
> enabled on qsdm.tech. Email and Telegram sign-in are supported by the service
> but are currently turned off.

QSDM Account is separate from the node's operator login. Node account
registration (`/api/v1/auth/register`) is disabled during the current recovery
period; this does not affect QSDM Account.

## User flow

1. Open `https://qsdm.tech/account/` or select **QSDM Account** in the QSDM
   Wallet extension.
2. Sign in with your QSDM Hive wallet: Hive shows a short-lived sign-in
   challenge and signs it only after you approve. Email and Telegram sign-in
   are supported by the service but are currently turned off on qsdm.tech.
3. Select **Link active wallet**. The browser extension asks QSDM Hive to sign
   a five-minute, account-bound ownership challenge.
4. Hive displays the signing approval. After approval, the account dashboard
   stores the public wallet address and can show its public CELL balance.
5. With Hive and the QSDM Wallet extension running, **Active Hive wallet** can
   read the live balance and request a CELL transfer. Hive separately displays
   and approves the exact recipient and amount before signing.

When more than one sign-in method is enabled, the **Sign-in methods** section
can attach another provider to the same account without creating a second
wallet dashboard. Email verification links are bound to the active account.
Telegram linking is bound to both the active browser session and its CSRF token
before the OIDC flow starts.

QSDM does not silently merge existing profiles. If an email address, Telegram
identity, or wallet is already linked to another account, the operation stops
with a conflict and neither account is changed. This prevents an identity
provider login from transferring wallets or account data implicitly.

Wallet sign-in creates a profile owned by that wallet. If the wallet is already
linked to a profile that uses another sign-in method, the sign-in stops with a
conflict and no profiles are merged.

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

There is no QSDM Account password in this design. When email sign-in is
enabled, email magic links remove a reusable password database while still
requiring control of the mailbox. A new email sign-in link invalidates the
older pending link for that email. A new email identity-link request
invalidates the older pending request for that account.

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
The active-wallet transfer panel does not change this boundary: it calls the
local browser provider, and no signing authority is stored in the account
service or browser session.

Sessions use `Secure`, `HttpOnly`, `SameSite=Lax` cookies. State-changing API
calls also require the account's CSRF token. The service is not exposed
directly; it runs behind the public web server, applies request-size limits and
rate limits, and returns `Cache-Control: no-store` on account APIs. Short-lived
Telegram login state and wallet-link challenges are kept only in memory,
expired on access, and bounded to 4,096 records each. A new account-bound
Telegram flow or wallet challenge replaces the older record for that account.

Session revocation and account deletion are persisted before the service tells
the browser they succeeded. If the encrypted account store cannot be updated,
the in-memory removal is rolled back and the operation fails closed.

## Operator setup

Operator setup for the qsdm.tech account service is documented privately. The
service source is `QSDM/source/cmd/qsdm-account`.

## Current scope

The current release supports wallet sign-in, sign-out, browser-session review
and revocation, self-service account deletion, public wallet linking and
unlinking, public balance display, and a local-Hive balance and CELL-transfer
panel. Email and Telegram sign-in (and attaching them as alternate methods for
one account) are implemented but turned off on qsdm.tech today. Automatic
merging of old duplicate profiles, Google and Apple sign-in, cloud wallet
custody, multiple currencies, and automatic synchronization of website
approvals are deliberately excluded.
