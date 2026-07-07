# QSDM Security Policy

## Supported releases

Only the latest published QSDM Core and QSDM Hive releases receive security
updates. Older or locally modified binaries must not be used for production
funds, mining identities, treasury operations, or wallet recovery.

## Reporting a vulnerability

Do not open a public issue containing exploit details, credentials, private
keys, wallet backups, or personal information. Use this repository's GitHub
Security tab to submit a private vulnerability report. If private reporting is
unavailable, contact the repository owner through GitHub without including the
sensitive details until a private channel is established.

Include the affected version, component, reproduction conditions, impact, and
the smallest safe proof needed to validate the report. Never test against
wallets, nodes, or accounts you do not own or have explicit permission to use.

## Repository secrecy boundary

This repository is public source code. The following must remain outside Git:

- wallet keystores, passphrases, recovery material, and private inventories;
- signer bearer tokens, API keys, SSH keys, VPS credentials, and `.env` files;
- signed or unsigned treasury envelopes and local ledger/runtime state;
- encrypted custody archives, even when the archive itself has a password;
- private external repositories, deployment mirrors, and workstation copies.

Public wallet addresses, public keys, transaction IDs, audited funding plans,
and redacted evidence may be committed when disclosure is intentional.

`python scripts/check_secrets.py --worktree` scans the publishable tree locally.
The pre-commit hook scans staged content, and GitHub Actions scans every tracked
file. These controls reduce risk but do not replace human review or key
rotation after an exposure.

## Custody incidents

If a secret may have entered Git history, treat it as compromised immediately:

1. disable or rotate the credential before rewriting history;
2. move funds to a newly generated wallet when private-key exposure is possible;
3. preserve a private incident record with timestamps and affected commits;
4. remove the material from every reachable branch, tag, release, and cache;
5. publish a scoped advisory after users have a safe upgrade or migration path.

Deleting a file in a later commit does not remove it from Git history.
