# Operator runbooks

The incident runbooks for QSDM's own infrastructure are maintained privately and are not part of the public documentation.

The remaining files in this folder describe an earlier design of the node's alerting and incident handling (written before the September 2026 recovery). They are kept for reference only and do not describe how the pilot network (pre-mainnet) is operated today. For the current state of the network see [Network status](../NETWORK_STATUS.md).

## For wallet users

- [Migrating mining rewards off a friendly-name address](WALLET_FRIENDLY_NAME_MIGRATION.md)

## Reference files (earlier design)

- `ARCH_SPOOF_INCIDENT.md`: attestation architecture-spoof alerts
- `CONTRACTS_BRIDGE_INCIDENT.md`: smart contracts and atomic-swap bridge
- `ENROLLMENT_INCIDENT.md`: mining enrollment
- `GOVERNANCE_AUTHORITY_INCIDENT.md`: governance authority rotation
- `HOT_RELOAD_INCIDENT.md`: configuration hot-reload
- `MINING_LIVENESS.md`: mining chain liveness
- `NETWORKING_INCIDENT.md`: libp2p peer graph
- `QUARANTINE_INCIDENT.md`: quarantine subsystem
- `REJECTION_FLOOD.md`: attestation rejection flood
- `REPUTATION_INCIDENT.md`: peer reputation
- `SLASHING_INCIDENT.md`: mining slashing
- `STORAGE_INCIDENT.md`: storage backend
- `STUB_DEPLOYMENT_INCIDENT.md`: stub deployment checks
- `SUBMESH_POLICY_INCIDENT.md`: submesh policy
- `WALLET_INCIDENT.md`: wallet API

To report a security issue, see the [security policy](https://github.com/blackbeardONE/QSDM/blob/main/SECURITY.md).
