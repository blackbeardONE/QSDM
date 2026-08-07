# QSDM Privacy Policy

Last updated: August 2, 2026

This policy describes data handling by the open-source QSDM Hive desktop
application, the QSDM Hive Wallet browser extension, and the reference services
operated at `qsdm.tech`. A separately operated validator, task provider, Relay,
website, or integration may have its own policy.

The public version of this policy is available at
<https://qsdm.tech/privacy.html>.

## Data kept on the computer

Hive stores account profiles, encrypted QSDM keystore files, settings, task
state, logs, miner enrollment state, and optional Relay credentials in the
operating system's application-data directories. Wallet passphrases and private
keys are intended to remain on the user's computer and are not account-recovery
data held by QSDM. Losing the keystore JSON and passphrase can permanently lose
access to the wallet.

Hive's analytics functions are disabled and do not transmit product-usage
events. Hive contains no advertising tracker.

## QSDM Hive Wallet browser extension

The extension has one purpose: connect supported websites to the active QSDM
wallet secured by QSDM Hive and route user-initiated wallet connection,
balance, signing, and CELL transfer requests to Hive for explicit local
approval.

For this purpose, the extension may handle the requesting website origin, the
public wallet address and balance, an allowlisted wallet method and its
parameters, and the resulting approval or response. Chrome classifies the
current website origin as web history, the public wallet address as an account
identifier, and balances and transaction requests as financial information.
The extension exchanges this structured data with the QSDM Hive native
messaging host on the user's computer. It does not receive or store the Hive
PIN, wallet private key, keystore JSON, passphrase, or other wallet recovery
material.

The extension does not build or store a general browsing history, inspect page
content, monitor user activity, sell user data, or use remote executable code.
It handles only the current origin needed for the visible wallet-connection
feature. Site connection approvals and extension settings are stored locally
and can be revoked or cleared by the user.

The provider loads automatically only on the official QSDM, QSDM Online, and
Sky Fang HTTPS domains. On another HTTPS website, temporary page access is
granted only after the user explicitly opens the extension for that tab. The
store package does not request blanket host access.

For the Chrome Web Store Privacy practices declaration, QSDM Hive Wallet
discloses these categories: **Personally identifiable information** (the public
wallet address as an account identifier), **Financial and payment information**
(wallet balance and transaction requests), and **Web history** (only the current
website origin required for connection). It does not collect the other listed
categories.

QSDM Hive Wallet's use and transfer of information received from browser APIs
adheres to the Chrome Web Store User Data Policy, including the Limited Use
requirements. Browser data is used only to provide or improve the extension's
single wallet-connection purpose. It is not used for personalized advertising,
creditworthiness, or lending decisions.

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

## Retention and disclosure

Local data remains on the user's computer until the user removes it, clears the
relevant setting, or uninstalls the software. Reference web and API security
logs are retained only as reasonably needed to operate, secure, and diagnose
the services, subject to the infrastructure's configured retention.

QSDM does not sell personal information. Information is disclosed only as
needed to provide a user-requested feature, to service providers operating the
reference infrastructure, to comply with applicable law, or to protect users
and the service from fraud, abuse, or security threats.

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
process in [SECURITY.md](SECURITY.md). Privacy questions can be sent to
`ops@qsdm.tech`. General project contact and current documentation are
available at <https://qsdm.tech>.
