# Mother Hive Internet Federation

Status: design proposal. The private-LAN Virtual Compute Runtime is implemented first; internet federation is not enabled by the current release.

## Answer

A Mother Hive can consume capacity from a pool in another location, including a different organization or network. It must not target an arbitrary Hive by IP address. Both the provider and consumer must deliberately enroll in QSDM federation, publish compatible policies, authenticate every lease, and accept Core-enforced settlement.

```text
Provider Agents -> Provider Relay -> outbound encrypted tunnel
                                      |
                               Federation Gateway
                                      |
Consumer QSDM Hive -> local runtime -> signed compute lease -> QSDM Core
```

The Federation Gateway routes authenticated envelopes. It does not hold Agent credentials, wallet keys, workload plaintext, or CELL. Provider Relays keep Agent enrollment private and open only outbound connections, which works behind NAT and avoids public Agent ports.

## Roles

- **Provider owner** controls the Agent group and resource limits.
- **Provider Relay** advertises bounded capacity and executes accepted leases.
- **Consumer Mother Hive** submits supported jobs and pays for completed work.
- **Federation Gateway** matches offers and carries encrypted envelopes over TLS 1.3 or QUIC.
- **QSDM Core** records identities, reservations, receipt replay state, escrow, and settlement.

A Hive can enable provider mode, consumer mode, or both. Version 1 must forbid recursive forwarding: a consumer cannot re-advertise capacity imported from another provider. This keeps accounting one hop and prevents loops or double-counted resources.

## Protocol

1. The provider publishes a wallet-signed `ComputeOffer`: Relay ID, supported workload IDs, CPU/GPU/RAM ceilings, region, price, expiry, and privacy policy.
2. The consumer selects an offer and submits a wallet-signed `ComputeLeaseIntent` with workload ID, budget, deadline, maximum price, nonce, and idempotency key.
3. QSDM Core reserves the maximum payment from an already-funded consumer balance or task pool. No reservation means no work.
4. The Gateway sends the encrypted lease to the provider's outbound Relay session. The provider validates identity, limits, workload digest, expiry, and reservation proof.
5. The Relay schedules the job on one eligible Agent. Agents still execute only reviewed capability versions; federation does not add a shell or arbitrary binary endpoint.
6. The Relay returns a signed result and durable receipt. The consumer verifies the result contract and submits or acknowledges the receipt.
7. QSDM Core rejects replayed job, proof, or receipt IDs and atomically settles the provider, Mother Hive operator, and ecosystem shares. Failed, expired, or cancelled leases release unused reservation.

## Identity And Credentials

- Reuse neither the Agent HMAC token nor the current private Mother token.
- Each Relay has a dedicated federation signing identity and short-lived session certificate bound to its QSDM wallet and Relay ID.
- Every consumer lease is signed by its active QSDM wallet.
- Gateway sessions use TLS 1.3 or QUIC with certificate pinning and periodic rotation.
- Job payloads use per-lease encryption between consumer and provider. The Gateway sees routing metadata only.
- Nonces, timestamps, expiries, body hashes, offer versions, and idempotency keys are mandatory.

## Controls In Hive

Provider mode needs explicit controls for resource percentages, allowed workload IDs, maximum job duration, concurrent jobs, region visibility, price floor, data-retention policy, and an emergency stop. Consumer mode needs provider selection, maximum spend, workload budget, data classification, job progress, cancellation, receipt verification, and dispute state.

No setting should imply that remote RAM or GPU becomes a local operating-system device. A compatible application submits a supported workload through the local Virtual Compute Runtime, which routes locally or through an accepted federation lease.

## Abuse And Failure Controls

- Funded reservation or stake before dispatch prevents free-work spam.
- Per-wallet, per-Relay, per-IP, and per-workload quotas limit floods.
- Signed capability manifests pin exact workload versions and resource bounds.
- Provider allowlists can restrict consumers; consumer policies can restrict providers and regions.
- Payload size, runtime, memory, GPU operations, and concurrency remain hard capped.
- Cancellation is cooperative before lease and fail-closed after a settlement receipt exists.
- Provider and consumer reputation derives only from Core-confirmed receipts and disputes.
- Sensitive workloads require an explicit data policy; version 1 should permit only public or non-sensitive inputs.
- Relay loss, Gateway loss, or Core uncertainty stops new leases. Existing work cannot settle twice after reconnection.

## Rollout

1. **Local runtime:** ship discovery, bounded workbench controls, receipts, and application API on one paired Relay.
2. **Private remote pilot:** connect two operator-owned sites with an outbound tunnel and fixed allowlists; no public marketplace.
3. **Core leases:** add offer, reservation, lease, cancellation, receipt, and settlement consensus records with replay tests.
4. **Federation Gateway:** deploy redundant stateless routers, certificate rotation, quotas, and health reporting.
5. **Public opt-in:** enable provider discovery only after economic-abuse, privacy, failover, and independent security reviews pass.

Until phases 2-5 are implemented, remote Mother Hives should use a trusted private VPN and the existing Relay path. Do not expose port 7740 or the loopback Compute Gateway to the public internet.
