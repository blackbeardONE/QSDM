# API versioning — HTTP vs mining protocol

> **Referenced by** the visitor-facing
> [`qsdm.tech/api.html`](https://qsdm.tech/api.html) "API status" page,
> the **Build → API status** link in every landing-page footer, and
> the **Reference** section of the docs portal. If you followed any of
> those into here, the one-paragraph TL;DR below is the answer.

This document clarifies why QSDM ships a "v1" HTTP URL prefix on a
network that "deprecated v1" in v0.3.2. It is the normative reference
for the messaging on the public `api.html` page and the inline
notes in landing-page footers and the docs portal.

## The TL;DR

QSDM has **two** versioned surfaces, and they version **independently**:

| Surface | Current version | Stability | Where the version lives |
|---|---|---|---|
| **HTTP API** | **v1** (stable) | Stable; no v2 planned in the v0.4.x cycle | URL prefix: `https://api.qsdm.tech/api/v1/*` |
| **Mining protocol** | **v2 only** at consensus | v1 retired at mainnet `FORK_V2_HEIGHT = 0`; rejected at admission | Wire field on proofs / blocks; surfaced on `/api/v1/status.mining.protocol_versions_accepted` |

The "v1 deprecation" shipped in `v0.3.2` (commit
[`f727fef`](https://github.com/blackbeardONE/QSDM/commit/f727fef)) was
about the **mining protocol's CPU-only PoW path**, not the HTTP URL
prefix. Three concrete deliverables landed in that session
(see `RELEASE_NOTES_v0.3.0.md` § Session 86 for the long-form):

1. `/api/v1/status` self-advertises the v2 posture in a new `mining`
   block (`protocol_versions_accepted: [2]`, `fork_v2_active: true`).
2. Both miners (`cmd/qsdmminer` and `cmd/qsdmminer-console`) preflight
   the status endpoint at startup and refuse to mine when a v1 caller
   meets a v2-active validator.
3. `cmd/qsdmminer` is no longer cross-compiled or cosign-signed by
   `release-container.yml` — only `qsdmminer-console --protocol=v2`
   is published as a release artefact.

## The two "v1"s, side by side

### HTTP API — `/api/v1/*` (stable, current)

Every public endpoint lives under `/api/v1/*`. This includes:

- **Self-custody wallet flow.**
  - `POST /api/v1/wallet/submit-signed` — v0.4.0; with replay
    protection + atomic debit added in v0.4.1.
  - `GET  /api/v1/wallet/nonce` — v0.4.1.
  - `GET  /api/v1/wallet/balance` — pre-v0.4.0, unchanged.
- **Trust / attestation transparency.**
  - `GET  /api/v1/trust/attestations/summary`
  - `GET  /api/v1/trust/attestations/recent`
- **Mining.**
  - `GET  /api/v1/mining/work`
  - `POST /api/v1/mining/submit`
  - `GET  /api/v1/mining/penalty`
- **Health and live posture.**
  - `GET  /api/v1/health` — liveness probe (200 OK if up).
  - `GET  /api/v1/status` — node id, chain tip, peer count,
    mining posture (includes `protocol_versions_accepted`).

The `v1` in the URL is the API URL prefix, not a deprecation flag.
It signals stable wire compatibility — any change that would break a
v1 client ships behind a new top-level prefix, and there is no plan
to do that in the v0.4.x cycle. Test
`TestDeriveMetricsURL_RejectsMissingV1Suffix` in `cmd/qsdmcli`
explicitly rejects `/api/v2` as a base URL because no such surface
exists in the running validator.

#### One endpoint structurally retired

`POST /api/v1/wallet/mint` returns **HTTP 410 Gone** with a migration
block since v0.3.3 (commit
[`03edf41`](https://github.com/blackbeardONE/QSDM/commit/03edf41)).
This was a supply-inflation surface from the seed-faucet era —
publicly callable, returned `status:"minted"`, and did not actually
credit the recipient. Any client still hitting it is observable on
the dashboard via `qsdm_wallet_mint_total{result="gone"}`. The route
itself is still on `/api/v1/*`; we did not bump the API prefix to
do the retirement.

### Mining protocol — v2 only at consensus

The mining protocol is the wire format of proofs the validator accepts.
It is independent of the HTTP API. Mainnet activated
`FORK_V2_HEIGHT = 0` at the Phase-4 chain reset, which means every v1
proof (CPU-only PoW) is rejected at admission with
`ReasonBadVersion`. The v1 reference miner `cmd/qsdmminer` is no
longer cross-compiled or signed by `release-container.yml` —
only `qsdmminer-console --protocol=v2` is published.

The preflight gate (Session 86):

- v2-active validator + v1 caller → refuse + exit 3 with a banner
  pointing operators at `qsdmminer-console --protocol=v2` and the
  `MINER_QUICKSTART.md`.
- v1 validator + v1 caller → proceed; banner explains.
- Probe failure (network, parse error, older validator without the
  `mining` block) → fail-OPEN with a warning so a degraded
  `/api/v1/status` doesn't lock out local devnet usage.
- `--allow-v1` (CLI flag) or `allow_v1 = true` (miner.toml) bypasses
  the refusal for forensic / replay use, with a loud "all submitted
  proofs WILL be rejected" warning printed to stderr.

The probe shape was deliberately implemented without a dependency on
`pkg/api.StatusResponse` to avoid an import cycle and to make the
miner brittle-resistant to future status-schema growth.

## Why the URL stays at v1

A version bump on the URL prefix is the most expensive backwards-
incompatible change we can make. It breaks every client SDK, every
documentation example, every operator bookmark, and every external
tutorial that links to `/api/v1/*`. We will pay that cost when the
wire format meaningfully diverges — for example, a major envelope
schema change that pre-v0.4.x clients cannot understand. That has
not happened in any v0.4.x release:

- **v0.4.0** added a new endpoint (`POST /wallet/submit-signed`) on
  the existing prefix. v0.3.x clients keep working unchanged.
- **v0.4.1** added another new endpoint (`GET /wallet/nonce`) and
  extended the `POST /wallet/submit-signed` envelope with an
  optional `nonce` field. Pre-v0.4.1 envelopes (no `nonce` field)
  continue to land via the v0.4.0 code path — see
  `V041_REPLAY_PROTECTION_DESIGN.md` § "Backwards compatibility"
  for the gate logic.

Both releases are strictly additive on the wire. Bumping `/api/v1/*`
to `/api/v2/*` would have signalled a breaking change that didn't
exist, and would have forced every client SDK release to retag for
a no-op change. We chose stability of the URL prefix instead.

When (if) we eventually need `/api/v2/*`, the migration plan in this
document will look like:

1. Stand up `/api/v2/*` alongside `/api/v1/*` for at least one
   release.
2. Deprecate `/api/v1/*` via a `Deprecation:` header and a docs flip,
   while keeping it serving live traffic.
3. Sunset `/api/v1/*` only after measured client adoption of
   `/api/v2/*` exceeds a threshold (currently undefined).

Until step 1 ships, **`/api/v1/*` is the only HTTP API and is fully
supported**.

## Where this messaging is surfaced

- [`https://qsdm.tech/api.html`](https://qsdm.tech/api.html) —
  visitor-facing summary with a live posture widget.
- **Build → API status** link in the footer of every landing page
  (`index.html`, `wallet.html`, `validators.html`, `trust.html`,
  `chain.html`, `download.html`).
- This document — the long-form technical reference, accessible from
  the docs portal **Reference → API versioning** entry.

If you find any other surface that disagrees with this document
(marketing copy, blog post, third-party SDK README), please file an
issue: <https://github.com/blackbeardONE/QSDM/issues>.

## See also

- [`API_REFERENCE.md`](API_REFERENCE.md) — the full endpoint
  reference for `/api/v1/*`.
- [`API_SECURITY.md`](API_SECURITY.md) — auth, replay, and rate-limit
  semantics.
- [`MINING_PROTOCOL_V2.md`](MINING_PROTOCOL_V2.md) — the mining-protocol
  v2 spec, including the `FORK_V2_HEIGHT` posture this page references.
- [`MINER_QUICKSTART.md`](MINER_QUICKSTART.md) — the v2-mainnet
  operator flow, including Appendix A. v1 audit / local-devnet
  builds and Appendix B. Enrollment-funding status.
- [`V041_REPLAY_PROTECTION_DESIGN.md`](V041_REPLAY_PROTECTION_DESIGN.md)
  — how the v0.4.1 envelope extension stays backwards-compatible on
  the v1 URL.
