# QSDM VPN

**Product site:** `https://qsdm.online/`

**Operator dashboard:** `https://vpn.qsdm.online/login`

**Current Android release:** `1.0.0` build `7`

QSDM VPN is the QSDM private-network-access product. It gives users a focused
Android experience for activating service, synchronizing an assigned VPN
profile, checking session and quota state, and connecting through the
infrastructure selected by their operator.

QSDM VPN is a separate product surface from the CELL ledger and QSDM Hive. Its
public website is `qsdm.online`; the public CELL network remains on
`qsdm.tech`, and Hive remains the Windows and Linux client for CELL wallets,
tasks, mining, and edge participation.

## User workflow

1. Download the current Android APK from `https://qsdm.online/download/`.
2. Verify the release information and SHA-256 checksum shown on that page.
3. Install QSDM VPN on Android 7.0 or newer.
4. Activate with the token issued for the exact Device ID shown by the app.
5. Synchronize the profile assigned to the account and device, then connect.

The current public release page identifies the artifact as
`QSDM-VPN-1.0.0-build7.apk` and provides the authoritative download link,
size, platform requirement, and checksum. Those values can change with a new
release, so operators should use the live download page for the latest version.

## Operator dashboard

The dashboard is the service control plane for authorized operators and
resellers. Its published scope includes:

- users and role-aware administration;
- devices and profile assignments;
- licenses, activation tokens, and voucher workflows;
- quotas and visible session usage;
- VPN server records;
- payments and support activity; and
- auditable operator actions.

## Security and privacy boundary

QSDM VPN controls account access, device activation, profile assignment, and
operational visibility. VPN traffic is carried by the server infrastructure
selected for the service. The control service states that it does not log the
content of pages, messages, or files carried through the tunnel, while server
operators and infrastructure providers still process packets needed to deliver
the connection.

The service may process account, license, device, connection, assignment,
session-time, and data-volume records for authentication, quotas, security,
support, and applicable legal obligations. Consult the live policies before
deployment or resale:

- `https://qsdm.online/privacy/`
- `https://qsdm.online/terms/`

## Product links

- [QSDM VPN website](https://qsdm.online/)
- [Android download](https://qsdm.online/download/)
- [Operator dashboard](https://vpn.qsdm.online/login)
- [Privacy policy](https://qsdm.online/privacy/)
- [Terms](https://qsdm.online/terms/)
- [QSDM Network](QSDM_NETWORK.md)
- [QSDM Hive](QSDM_HIVE.md)
