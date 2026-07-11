# Mother Hive Internet Federation

Status: private federation pilot implemented in Hive/Edge Control. Public provider discovery, Core escrow leases, and marketplace settlement remain future phases.

## Answer

A Mother Hive can consume capacity from a pool in another location, including a different organization or network. It must not target an arbitrary Hive by IP address. Both the provider and consumer must deliberately enroll in QSDM federation, publish compatible policies, authenticate every job, and accept Core-enforced settlement.

```text
Current private pilot:
Provider Agents -> Provider Relay over HTTPS <- Consumer QSDM Hive
                         |
                         +-> signed settlement proof -> QSDM Core

Full protocol target:
Provider Relay -> outbound encrypted tunnel -> Federation Gateway
                                                    |
Consumer QSDM Hive -> signed compute lease ---------+-> QSDM Core
```

The private pilot connects Hive directly to an explicitly invited HTTPS Relay. The future Federation Gateway will route authenticated envelopes without holding Agent credentials, wallet keys, workload plaintext, or CELL. Provider Relays will keep Agent enrollment private and open only outbound connections, which works behind NAT and avoids public Agent ports.

## Roles

- **Provider owner** controls the Agent group and resource limits.
- **Provider Relay** advertises bounded capacity and executes accepted leases.
- **Consumer Mother Hive** submits supported jobs and pays for completed work.
- **Federation Gateway** is a future service that will match offers and carry encrypted envelopes over TLS 1.3 or QUIC.
- **QSDM Core** currently records receipt replay state and settlement; future Core leases add reservations and escrow.

A Hive can enable provider mode, consumer mode, or both. Version 1 must forbid recursive forwarding: a consumer cannot re-advertise capacity imported from another provider. This keeps accounting one hop and prevents loops or double-counted resources.

## Private Pilot Flow

1. The provider runs a Relay behind HTTPS, usually through the QSDM edge route or a private reverse proxy.
2. Edge Control generates a separate **Internet federation invitation** only when the Relay address is HTTPS. Current invitations use the `QSDM-EDGE-2` format and contain a derived credential, never the permanent Mother Hive key.
3. The invitation contains the Relay URL, a dedicated Mother Hive token, workload IDs, provider name, a cryptographically random offer ID, and a 24-hour expiry. Hive and Relay reject invitations that exceed the 25-hour clock-skew ceiling.
4. The consumer pastes that invitation into Hive's Mother Hive page.
5. Hive stores the derived token in its private config area, marks the connection as `internet-federation`, and shows the provider, offer, expiry, allowed wallet metadata, and workload IDs.
6. Hive sends the immutable federation context with every signed Relay request. The Relay derives the expected credential from its private Mother Hive key, rejects expired contexts, and rejects compute resources outside the invitation workload list.
7. Applications still submit work only through Hive's authenticated loopback Compute Gateway. The remote Relay receives only approved CPU/GPU/RAM workload requests.

Legacy `QSDM-EDGE-1` federation invitations exposed the same long-lived credential used by a local Mother Hive. Current Hive builds reject those invitations; generate a new invitation in current Edge Control. Private-LAN `QSDM-EDGE-1` Agent and Mother Hive pairing remains supported.

When an HTTPS Relay first upgrades to federation v2, Edge Control rotates its Mother Hive key once and records a private migration marker. This revokes previously copied v1 federation credentials. A same-machine one-click Mother Hive config follows the rotated token file automatically; any copied private Mother Hive code must be paired again.

This pilot is intentional: it proves cross-location routing without opening Agent ports, exposing the loopback gateway, or granting arbitrary remote execution.

Use a dedicated Relay for one fixed-trust provider/consumer relationship during the pilot. Do not share the same Relay across unrelated consumers; per-consumer job visibility, cancellation isolation, pricing, reservations, and disputes belong to the future Core-lease phase.

## Full Protocol

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

1. **Local runtime:** shipped discovery, bounded workbench controls, receipts, and application API on one paired Relay.
2. **Private remote pilot:** shipped HTTPS federation invitations and remote-mode Hive status; use fixed trust relationships, not a public marketplace.
3. **Core leases:** add offer, reservation, lease, cancellation, receipt, and settlement consensus records with replay tests.
4. **Federation Gateway:** deploy redundant stateless routers, certificate rotation, quotas, and health reporting.
5. **Public opt-in:** enable provider discovery only after economic-abuse, privacy, failover, and independent security reviews pass.

Until phases 3-5 are implemented, remote Mother Hives should use a trusted private VPN or an explicitly invited HTTPS Relay. Do not expose port 7740 or the loopback Compute Gateway to the public internet.
