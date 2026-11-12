# QSDM Privacy Policy

Last updated: November 12, 2026

This policy describes data handling by the open-source QSDM Hive desktop
application and the reference services operated at `qsdm.tech`. A separately
operated validator, task provider, Relay, or integration may have its own
policy.

## Data kept on the computer

Hive stores account profiles, encrypted QSDM keystore files, settings, task
state, logs, miner enrollment state, and optional Relay credentials in the
operating system's application-data directories. From the first Hive release
after 1.4.22, Hive also keeps a copy of the last signed release envelope it
verified from `qsdm.tech` in a `release-envelope-cache` folder there, so that
it keeps working when the website cannot be
reached. That file is public release data and holds nothing about you. Wallet
passphrases and private
keys are intended to remain on the user's computer and are not account-recovery
data held by QSDM. Losing the keystore JSON and passphrase can permanently lose
access to the wallet.

Hive's analytics functions are disabled and do not transmit product-usage
events. Hive contains no advertising tracker.

## Crash records and diagnostics reports

From the first Hive release after 1.4.22, Hive keeps crash records on your
computer and can save a diagnostics report. When part of Hive crashes, Hive
writes a short record of it to a `diagnostics` folder in its application-data
directory. Before it writes, Hive removes the secrets it can find: recovery
phrases, private keys and other key material, tokens, passwords, email
addresses, and your home folder path. Nothing is sent anywhere, and Hive keeps
the records until you delete them.

If you need help, open the menu of the Hive tray icon and choose **Save a
diagnostics report…** (on a Mac it is also in the Help menu). Hive saves one
text file, in the place you pick, with the crash records, Hive's current log,
and basic version, system, and graphics details, with the same secrets
removed. Read it, then attach it to your support request if you choose to.
**Open the diagnostics folder** in the same menu shows the records.

## Optional QSDM Account data

When a user chooses QSDM Account, the reference account service processes an
email address or Telegram OpenID identifier to create a browser session. Email
uses a short-lived one-time sign-in link; Telegram responses are verified
server-side. Identity display values are encrypted at rest. Lookup values,
one-time tokens, and session tokens are stored as keyed hashes. The service
also stores public wallet addresses that the user links through an ML-DSA
ownership signature.

QSDM Account does not receive wallet private keys, keystore JSON, passphrases,
recovery phrases, or signing authority. It does not synchronize a website's
local Hive approval. A user who does not choose QSDM Account can continue using
Hive and the self-custody wallet without an account profile.

## Data sent when network features are used

Hive sends only the information needed for a feature the user starts or
configures. Depending on that feature, this can include:

- public wallet addresses and public keys;
- signed transactions, task actions, proofs, and anti-replay values;
- chain, balance, task, reward, and update requests;
- miner enrollment identifiers, hardware capability information, and mining
  proofs when the user enables mining;
- bounded resource descriptions and signed workload receipts when the user
  enables Edge Agent, Relay, or Mother Hive participation; and
- a public key, signature, and short-lived link code when the user explicitly
  links a supported integration such as Sky Fang.

Private wallet keys and wallet passphrases must not be included in those
requests. Reference web and API infrastructure may retain ordinary security
and operational logs such as source IP address, request time, endpoint, status,
and rate-limit events. These logs are used to operate, secure, and diagnose the
service and are not sold for advertising.

Optional external notification storage is contacted only when an operator
configures the required credentials. A custom Core, gateway, task provider,
Relay, or integration receives the requests directed to it and is controlled
by its operator rather than by this repository.

## User controls

Mining, task execution, and resource sharing are opt-in controls in Hive. Users
can stop tasks, disconnect a Relay, close their account session, or uninstall
Hive. Uninstalling does not automatically destroy wallet backups or every local
application-data file, because doing so could destroy funds. Users should back
up the keystore first and then remove remaining application data manually when
they intentionally want it erased.

Public ledger transactions and accepted proofs are replicated records. They
cannot generally be deleted without invalidating the ledger.

## Security and contact

Do not send wallet private keys, keystore files, passphrases, API tokens, or
other secrets in a bug report. Report security issues through the private
process in [SECURITY.md](SECURITY.md). General project contact and current
documentation are available at <https://qsdm.tech>.
