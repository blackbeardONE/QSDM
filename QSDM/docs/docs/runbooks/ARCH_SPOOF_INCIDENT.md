# Attestation Arch-Spoof Incident — Operator Runbook

Triage flow for the 3 alerts in the
`qsdm-v2-attest-archspoof` group:

| Alert | Severity | Default `for:` | Anchor |
|---|---|---|---|
| `QSDMAttestArchSpoofUnknownArchBurst`     | warning      | 10m | [§3.1](#31-mode-a--qsdmattestarchspoofunknownarchburst) |
| `QSDMAttestArchSpoofGPUNameMismatch`      | warning      | 10m | [§3.2](#32-mode-b--qsdmattestarchspoofgpunamemismatch) |
| `QSDMAttestArchSpoofCCSubjectMismatch`    | **critical** | 1m  | [§3.3](#33-mode-c--qsdmattestarchspoofccsubjectmismatch) |

> **Why a dedicated arch-spoof runbook?** These are the
> chain's **adversarial-detection** signals — three
> distinct rejection reasons, each catching a different
> class of "miner is lying about their hardware" attack.
> The reasons escalate from *typo* (Mode A) to *enrolled
> operator economic cheating* (Mode B) to *cryptographic
> anomaly* (Mode C). Mode C in particular fires after
> a proof has already passed cert-chain pin AND AIK
> signature verification — a non-zero increment is, by
> construction, either a fabricated AIK leaf or an
> NVIDIA-issued cert being misused. **Mode C pages
> immediately on a single fire** (no rate threshold);
> all three other §4.6 alert categories use sustained
> rates.

Companion observability: `qsdm_attest_archspoof_rejected_total{reason}`
emitted by `pkg/monitoring/archcheck_metrics.go`. Three
closed-enum reasons, one per mode.

---

## 1. Glossary (60-second skim)

- **GPU arch** — closed-enum field on a v2 attestation
  payload identifying the claimed hardware generation
  (`turing` / `ampere` / `ada` / `hopper` / `blackwell`
  / etc.). The reward-weight table is keyed off arch;
  spoofing arch is therefore directly economic.
- **HMAC bundle** — an attestation envelope HMAC'd
  under the operator's enrollment key. The HMAC binds
  `gpu_name` to the bundle; an enrolled operator
  cannot rewrite `gpu_name` after submission without
  re-signing.
- **CC bundle** — Confidential-Computing path proof,
  carrying an NVIDIA-issued AIK cert chain. Reaches a
  separate verifier path from the HMAC bundle.
- **§3.3 step-8 cross-check** — the verifier step that
  matches `gpu_name` against the regex pattern set for
  the claimed `gpu_arch`. Catches consumer-Ada cards
  claiming Hopper (the canonical Mode B shape).
- **§4.6.5 cert-subject check** — the verifier step
  that parses the CC leaf cert's
  `Subject.CommonName` for positive product evidence
  (e.g. "NVIDIA H100") and compares against the
  claimed arch. Mode C fires only when this contradicts.
- **AIK** — Attestation Identity Key, the GPU-resident
  key that signs the bundle. NVIDIA-issued; cert-pinned
  to the genesis-block CA root.

---

## 2. First-90-seconds checklist

1. **Identify which mode fired.** The alert label
   `reason` (one of `unknown_arch` / `gpu_name_mismatch`
   / `cc_subject_mismatch`) names the mode 1:1.

2. **Mode C overrides everything else.** If
   `QSDMAttestArchSpoofCCSubjectMismatch` is firing —
   even if the others are too — treat as a security
   incident, capture state, escalate to the security
   on-call. Do not just clear / silence the alert.

3. **Cross-reference the rejection-ring tile.** The
   dashboard's **🛑 Attestation Rejections** tile shows
   the most recent §4.6 rejections by NodeID. If a
   single NodeID dominates, the offender is identified;
   if the rejections are spread across many NodeIDs,
   suspect an external probe / scan rather than an
   enrolled-operator cheat.

4. **For Modes A and B, do NOT immediately slash.** The
   slashing pipeline (`SLASHING_INCIDENT.md`) is the
   right escalation eventually, but a single spike of
   Mode A is almost always a typo'd client and a
   single spike of Mode B can be a hardware-swap
   timing issue. Confirm sustained activity from one
   NodeID first.

---

## 3. Modes

### 3.1. Mode A — `QSDMAttestArchSpoofUnknownArchBurst`

`rate(qsdm_attest_archspoof_rejected_total{reason="unknown_arch"}[5m]) > 0.1`
for 10m. Severity: warning.

#### What triggered it

A miner is submitting attestations whose `gpu_arch`
field is **not** in the validator's closed-enum
allowlist. Single-shot occurrences are typically a
client typo or a brand-new GPU generation that hasn't
shipped to the server yet; sustained rate is the page
trigger (≥30 rejections in 5m).

#### Triage

```promql
# Confirm the burst — by source if you can:
rate(qsdm_attest_archspoof_rejected_total{reason="unknown_arch"}[5m])

# Decompose by NodeID via the rejection-ring tile or:
GET /api/v1/attest/recent-rejections?limit=50
# then `jq` for the most-frequent miner_addr.
```

| Source distribution | Probable cause | Action |
|---|---|---|
| Concentrated on 1 NodeID | Single miner running an unreleased GPU arch (e.g. `"rubin"`, hypothetical post-Blackwell) auto-retrying. Their client is broken; their server is rejecting | Reach out to the operator; instruct them to check the alias map in the latest release notes |
| Concentrated on 1 NodeID with `*` retries | Auto-retry loop hammering the gate. Operator hasn't noticed the rejection logs | Same as above; the auto-retry is incidentally a probe-shaped attack signature |
| Spread across many NodeIDs | External prober scanning the v2 attestation surface | Check the source-IP distribution in ingress logs; consider a libp2p / mempool rate-limit upstream |
| Burst correlated with a release | Client-server skew on alias canonicalisation (e.g. miner emits `"ada"` but server hasn't landed the alias yet) | Confirm release-version skew; pin minimum client version in MINER_QUICKSTART; ship the alias if the miner-side spelling is reasonable |

#### Mitigation

- **Typo'd client (single source):** operator-side fix.
  No chain-side action.
- **Probe (broad source):** add the source IP / IP
  range to the firewall block list.
- **Release skew:** add the alias to the closed enum
  allowlist; a one-line code change + CHANGELOG.
  Consumer-side: pin minimum client version.

---

### 3.2. Mode B — `QSDMAttestArchSpoofGPUNameMismatch`

`rate(qsdm_attest_archspoof_rejected_total{reason="gpu_name_mismatch"}[5m]) > 0.05`
for 10m. Severity: warning.

#### What triggered it

An *enrolled operator* is submitting a proof whose
HMAC-bound `gpu_name` doesn't match the regex pattern
set for the claimed `gpu_arch`. The HMAC binds the name
to the bundle, so this isn't a client bug an operator
can accidentally trip — it's a **deliberate or
config-drift attempt to claim a higher reward weight
than the hardware deserves**.

#### Why the threshold is lower than Mode A

Mode A's threshold (0.1/s) is calibrated against
typos. Mode B's threshold (0.05/s) is calibrated
against attempts: a single rejection might be a
hardware-swap timing issue, but sustained activity
means the operator's stake bond is at active risk.
Honest operators rarely trigger this by accident
because the economic cost (slash exposure) makes
mistakes expensive.

#### Triage

```bash
# Identify the offender via the rejection-ring tile or:
curl -s http://127.0.0.1:8080/api/v1/attest/recent-rejections?limit=50 \
  | jq '.records[] | select(.reason=="gpu_name_mismatch") | .miner_addr' \
  | sort | uniq -c | sort -rn

# Cross-reference enrollment status for the dominant
# miner_addr:
curl -s http://127.0.0.1:8080/api/v1/mining/enrollment/<NodeID> | jq .
```

| Pattern | Probable cause | Action |
|---|---|---|
| One NodeID, sustained | **Cheat attempt.** Consumer Ada (RTX 4090) claiming `gpu_arch=hopper`; consumer Turing claiming `ampere`; etc. | Confirm via §3.3 step-8 cross-check error message in logs; the proof's `gpu_name` and claimed `gpu_arch` are both in the rejection record. Escalate to the slashing pipeline if sustained — see [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md). |
| One NodeID, single-burst | Hardware swap on an existing NodeID without re-enrollment (the bundle is HMAC-signed under the OLD GPU's identity) | Reach out to the operator; have them unenroll and re-enroll under the new hardware. The temporary rejection during the swap is honest |
| Multiple NodeIDs simultaneously | Coordinated cheat-ring OR a release regression (newly-shipped client emits a misformatted `gpu_name`) | Check release notes; if no release in flight, treat as a coordinated attack and escalate to security on-call |

#### Mitigation — slashing path

Sustained Mode B from one NodeID **is the canonical
forged-attestation slashing case**. The operator's
stake bond is the economic disincentive; the slashing
pipeline is the enforcement mechanism. See
[`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md) for the
slash-submission triage and verifier-rollback decision
tree.

#### Mitigation — operator-side fix (hardware swap case)

- Operator unenrolls the old NodeID (waits for unbond
  window via [`ENROLLMENT_INCIDENT.md`](ENROLLMENT_INCIDENT.md)
  Mode C cooldown).
- Re-enrolls with the new GPU's identity. Bundle is
  re-HMAC'd; rejections stop on next proof.

---

### 3.3. Mode C — `QSDMAttestArchSpoofCCSubjectMismatch`

`increase(qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"}[15m]) > 0`
for 1m. Severity: **critical**. **Pages on a single
fire.**

#### Why this is critical-severity

To reach the §4.6.5 cert-subject check, the proof has
already passed:

1. **Cert chain rooted in a genesis-pinned NVIDIA CA**
   (§3.2 step 3) — the leaf cert is genuinely
   NVIDIA-issued; not self-signed.
2. **AIK ECDSA signature over the canonical preimage**
   (§3.2 step 4) — the leaf cert's private key signed
   the bundle; the AIK is genuinely the one the cert
   was issued to.
3. **PCR firmware/driver floor** (§3.2 step 8) — the
   GPU's measured boot state passes the firmware
   floor.

…yet the leaf cert's `Subject.CommonName` contains
*positive product evidence* (e.g. "NVIDIA H100") that
contradicts the proof's claimed `gpu_arch`. There are
exactly two possibilities:

1. **Fabricated AIK leaf.** An entity holding an
   NVIDIA-issued AIK cert has crafted a bundle with a
   mismatched arch claim. This is a **cryptographic
   anomaly**: either the cert chain pin has a hole
   (genesis-block CA root has been compromised, or a
   pinned CA has been re-issued by NVIDIA), or the
   AIK private key has leaked from a real GPU and is
   being used to sign bundles for non-existent
   hardware.
2. **Operator misconfiguration.** The miner binary is
   reading `gpu_arch` from a stale config file after
   a hardware swap; the AIK / cert / bundle are
   genuine, but the claimed arch field was
   hand-edited and never refreshed. Less severe than
   (1) but still operationally serious.

In both cases, the §4.6.5 check is the **last line of
defence** between the spoof and the reward-weight
table. A non-zero increment IS the signal — there is
no "noise floor" baseline for this metric.

#### Symptoms

- `qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"}`
  increments — even by 1 — within the last 15 min.
- The rejection record carries `reason=cc_subject_mismatch`
  and embeds the offending `Subject.CommonName` plus
  the claimed `gpu_arch` in the error message.
- The validator's reject log will contain a wrapped
  `archcheck.ErrArchCertSubjectMismatch` line.

#### Triage

```bash
# Find the rejection record:
journalctl -u qsdm --since "30 minutes ago" \
  | grep -i "ErrArchCertSubjectMismatch"

# Or via the rejection-ring API (the embedded detail
# carries the offending Subject CN + the claimed arch):
curl -s http://127.0.0.1:8080/api/v1/attest/recent-rejections?limit=50 \
  | jq '.records[] | select(.reason=="cc_subject_mismatch")'
```

Capture for forensics:

- The full rejection record (Subject CN, claimed
  `gpu_arch`, NodeID).
- The miner's enrollment record
  (`/api/v1/mining/enrollment/<NodeID>`).
- The miner's recent slash history
  (`/api/mining/slash-receipts?limit=50`).
- The cert chain itself (server-side log will have
  it; preserve before log-rotation).

#### Decision tree

```
Is the offending Subject CN consistent with NVIDIA's
canonical product naming (e.g. "NVIDIA H100", "H100 PCIe")?
  ├── Yes:
  │     The AIK / cert is genuinely H100-class hardware,
  │     but the claimed gpu_arch is NOT hopper.
  │     ──> Likely operator misconfiguration after a
  │         hardware swap / refresh. Reach out to the
  │         operator. If they confirm the hardware is
  │         indeed H100 and a stale config caused the
  │         mismatch, no slashing — but DO require them
  │         to redeploy with the corrected config and
  │         re-attest.
  │     ──> If the operator does NOT confirm OR cannot
  │         be reached, escalate to security: the AIK
  │         private key may have leaked.
  │
  └── No (Subject CN names a different product than
      either the claimed gpu_arch OR what the miner
      previously claimed):
        ──> Cryptographic anomaly. Possibilities:
              - AIK key leaked from a real GPU and is
                being used to sign bundles for fake
                hardware.
              - Genesis-block-pinned CA root has been
                compromised or re-issued.
              - The signing process itself has been
                tampered with at the miner-binary
                layer.
        ──> Page security on-call IMMEDIATELY. Do not
            unilaterally slash without security review;
            slashing on a fabricated-AIK case may
            destroy the only forensic copy of the
            offending bundle if the miner unenrolls
            in response.
```

#### Mitigation

- **Operator-misconfiguration branch:** operator-side
  fix; the validator's rejection is correct, no
  chain-side action needed beyond a follow-up audit.
- **Cryptographic-anomaly branch:** **stop the slash
  trigger and escalate**. Calling `ApplySlashTx`
  destroys the bond, but this case may require
  preserving the offending bundle untouched as
  evidence for an external (NVIDIA, security
  partner) investigation. Coordinate with the
  security on-call before pulling the slash trigger.

---

## 4. Cross-mode escalation

| Concurrent alerts | Most likely root | Action |
|---|---|---|
| Mode A only, single NodeID | Typo / unreleased arch | Operator-side fix |
| Mode A only, broad sources | Probe / scan | Firewall block |
| Mode B only, single NodeID, sustained | Cheat attempt | [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md) escalation |
| Mode A + Mode B, single NodeID | Same operator first tried unknown arch then settled on a known-but-mismatched arch | Same as Mode B |
| Mode C alone | Cryptographic anomaly OR config drift after hardware swap | §3.3 decision tree |
| Mode C + Mode B, same NodeID | Same operator; the CC path tripped Mode C and the HMAC path tripped Mode B | Treat as Mode C — the cryptographic signal dominates |

---

## 5. Reference

- **Source files:**
  - [`pkg/monitoring/archcheck_metrics.go`](../../../source/pkg/monitoring/archcheck_metrics.go)
    — `qsdm_attest_archspoof_rejected_total{reason}`
    counter
  - [`pkg/mining/verifier.go`](../../../source/pkg/mining/verifier.go)
    — top-level §4.6 verifier (records the rejection
    counters)
  - `pkg/mining/attest/archcheck/...` — the per-reason
    cross-check implementations (one file each for
    unknown arch, gpu_name pattern, cert-subject
    parse)
- **API endpoints:**
  - `GET /api/v1/attest/recent-rejections` — paginated
    rejection-ring records (the wrapped error detail
    carries the offending field values)
- **Prometheus series:**
  - `qsdm_attest_archspoof_rejected_total{reason="unknown_arch"}`
  - `qsdm_attest_archspoof_rejected_total{reason="gpu_name_mismatch"}`
  - `qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"}`
- **Closed-enum reasons:**
  - `unknown_arch` — `gpu_arch` not in allowlist
  - `gpu_name_mismatch` — HMAC bundle's `gpu_name`
    contradicts the claimed arch
  - `cc_subject_mismatch` — CC leaf cert Subject CN
    contradicts the claimed arch
- **Companion runbooks:**
  - [`SLASHING_INCIDENT.md`](SLASHING_INCIDENT.md) —
    sustained Mode B is the canonical
    forged-attestation slashing case
  - [`REJECTION_FLOOD.md`](REJECTION_FLOOD.md) — the
    rejection ring is the per-rejection forensic store
    used in §3 triage
  - [`ENROLLMENT_INCIDENT.md`](ENROLLMENT_INCIDENT.md)
    — Mode B's hardware-swap branch flows through the
    unenroll → unbond → re-enroll cycle
  - [`OPERATOR_HYGIENE_INCIDENT.md`](OPERATOR_HYGIENE_INCIDENT.md)
    — Mode C of the hygiene runbook is
    `QSDMAttestHashrateOutOfBand`, the per-arch
    hashrate-band check. Sustained activity from one
    NodeID across both runbooks (arch-spoof Mode B
    + hashrate Mode C) is the canonical
    "miner cheating across multiple axes" pattern;
    cross-reference to `SLASHING_INCIDENT.md`. The
    hygiene runbook also covers the related
    `arch="unknown"` case (lands in default switch
    branch and trivially fails the hashrate band
    check)

---

## 6. Alert ↔ Mode quick-reference

| Alert                                       | Mode | Severity     | Triage section |
| ------------------------------------------- | ---- | ------------ | -------------- |
| `QSDMAttestArchSpoofUnknownArchBurst`       | A    | warning      | §3.1           |
| `QSDMAttestArchSpoofGPUNameMismatch`        | B    | warning      | §3.2           |
| `QSDMAttestArchSpoofCCSubjectMismatch`      | C    | **critical** | §3.3           |
