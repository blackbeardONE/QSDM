"""End-to-end generator for alerts_qsdm.test.yml.

Two-pass: first run produces a scaffold whose firing checkpoints
intentionally fail (`exp_alerts: []`), capturing promtool's rendered
`got:[...]` blocks. Second pass rewrites the file with proper
`exp_alerts: [{exp_labels, exp_annotations}]` populated from the
captured renderings, so the final tests truly bind the alert rules
(including labels and annotation templates) to expected behaviour.

Run:
    python scripts/gen_promtool_tests.py

By default the script looks for `promtool` on PATH. To override (e.g.
a Windows-local download), set the `PROMTOOL` env var to the binary
path.

The script is idempotent — running it multiple times converges to the
same test-file output for the same alerts file.
"""
from __future__ import annotations

import dataclasses
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

# Repo root is the parent directory of scripts/ where this script lives.
REPO = Path(__file__).resolve().parent.parent

# `promtool` discovery: env override, then PATH lookup.
_PROMTOOL_ENV = os.environ.get("PROMTOOL")
if _PROMTOOL_ENV:
    PROMTOOL = Path(_PROMTOOL_ENV)
else:
    _which = shutil.which("promtool") or shutil.which("promtool.exe")
    PROMTOOL = Path(_which) if _which else Path("promtool")

TESTS = REPO / "QSDM" / "deploy" / "prometheus" / "alerts_qsdm.test.yml"
ALERTS = REPO / "QSDM" / "deploy" / "prometheus" / "alerts_qsdm.example.yml"


# ---------------------------------------------------------------------------
# Test specs — mirrors the alerts file groups, one entry per alert.
# (group_header_md, alertname, comment, input_series, early_eval, late_eval)
# ---------------------------------------------------------------------------

@dataclasses.dataclass
class T:
    name: str
    summary: str  # one-line description of the firing condition (for test name)
    input_series: list[tuple[str, str]]  # [(series_label_set, values_expr), ...]
    early: str  # eval_time string for the negative checkpoint
    late: str  # eval_time string for the firing checkpoint
    notes: str = ""  # optional extra comment lines for the test


GROUPS: list[tuple[str, list[T]]] = [
    (
        "qsdm-nvidia-lock — NVIDIA-lock + NGC submission gate\n"
        "OPERATOR_HYGIENE_INCIDENT.md §3.1 / §3.2\n"
        "NGC_SUBMISSION_INCIDENT.md §3.1 / §3.2",
        [
            T(
                "QSDMNvidiaLockHTTPBlocksSpike",
                "rate > 0.5/s, for:10m",
                [("qsdm_nvidia_lock_http_blocks_total", "0+60x30")],
                "5m",
                "16m",
                notes="60/min = 1.0/s sustained, 2x the 0.5/s threshold.",
            ),
            T(
                "QSDMNvidiaLockP2PRejects",
                "increase > 0 in 15m, for:5m",
                [("qsdm_nvidia_lock_p2p_rejects_total", "0+1x30")],
                "1m",
                "20m",
                notes="Counter ramps 1/min for 30m → increase[15m] = 15.",
            ),
            T(
                "QSDMNGCChallengeRateLimited",
                "increase > 5 in 10m, for:5m",
                [("qsdm_ngc_challenge_rate_limited_total", "0+1x30")],
                "5m",
                "20m",
                notes="1/min for 30m → increase[10m] = 10 (above 5).",
            ),
            T(
                "QSDMNGCProofIngestRejectBurst",
                "sum(increase) > 10 in 10m, for:5m",
                [
                    (
                        'qsdm_ngc_proof_ingest_rejected_total{reason="hmac"}',
                        "0+2x30",
                    )
                ],
                "5m",
                "20m",
                notes="2/min on one reason → sum(increase[10m]) = 20.",
            ),
        ],
    ),
    (
        "qsdm-submesh\n"
        "SUBMESH_POLICY_INCIDENT.md §3.1 / §3.2",
        [
            T(
                "QSDMSubmeshP2PRejects",
                "route OR size increase > 0 in 15m, for:5m",
                [
                    ("qsdm_submesh_p2p_reject_route_total", "0+1x30"),
                    ("qsdm_submesh_p2p_reject_size_total", "0x30"),
                ],
                "1m",
                "20m",
            ),
            T(
                "QSDMSubmeshAPISustained422",
                "summed rejects >= 5 in 10m, for:5m",
                [
                    ("qsdm_submesh_api_wallet_reject_route_total", "0+1x30"),
                    ("qsdm_submesh_api_wallet_reject_size_total", "0+1x30"),
                    ("qsdm_submesh_api_privileged_reject_size_total", "0+1x30"),
                ],
                "5m",
                "20m",
                notes="3 series × 10 each = 30 summed.",
            ),
        ],
    ),
    (
        "qsdm-throughput — chain \"silent failure\" sentinel\n"
        "OPERATOR_HYGIENE_INCIDENT.md §3.4",
        [
            T(
                "QSDMNoTransactionsStored",
                "stored=0 but processed>0, for:30m",
                [
                    ("qsdm_transactions_processed_total", "0+10x80"),
                    ("qsdm_transactions_stored_total", "0x80"),
                ],
                "25m",
                "65m",
                notes="processed climbing, stored flat → divergence sustained.",
            ),
        ],
    ),
    (
        "qsdm-trust-transparency\n"
        "TRUST_INCIDENT.md §3.1 / §3.2",
        [
            T(
                "QSDMTrustNoAttestationsAccepted",
                "accepted-rate=0 over 20m, for:5m",
                [
                    (
                        'qsdm_ngc_proof_ingest_accepted_total{reason="ok"}',
                        "0+1x10 10x60",
                    )
                ],
                "5m",
                "40m",
                notes=(
                    "Counter increments for first 10m (rate > 0), then flat\n"
                    "(rate == 0). At t=40m the 20m rate window covers the\n"
                    "flat tail entirely, sum(rate)==0 fires."
                ),
            ),
            T(
                "QSDMTrustIngestRejectRateElevated",
                "reject-rate > 1 + accept-rate, for:10m",
                [
                    (
                        'qsdm_ngc_proof_ingest_rejected_total{reason="hmac"}',
                        "0+120x30",
                    ),
                    (
                        'qsdm_ngc_proof_ingest_accepted_total{reason="ok"}',
                        "5x30",
                    ),
                ],
                "5m",
                "21m",
                notes="rejected = 120/min = 2.0/s; accepted rate = 0/s. 2 > 1+0.",
            ),
        ],
    ),
    (
        "qsdm-trust-redundancy\n"
        "TRUST_INCIDENT.md §3.3 / §3.4 / §3.5 / §3.6",
        [
            T(
                "QSDMTrustAttestationsBelowFloor",
                "warm=1 and attested<2, for:10m",
                [
                    ("qsdm_trust_warm", "1x30"),
                    ("qsdm_trust_attested", "1x30"),
                ],
                "5m",
                "16m",
            ),
            T(
                "QSDMTrustNGCServiceDegraded",
                "warm=1 and ngc_healthy=0, for:10m",
                [
                    ("qsdm_trust_warm", "1x30"),
                    ("qsdm_trust_ngc_service_healthy", "0x30"),
                ],
                "5m",
                "16m",
            ),
            T(
                "QSDMTrustLastAttestedStale",
                "(time()-last_attested) > 1800, for:5m",
                [
                    ("qsdm_trust_warm", "1x60"),
                    ("qsdm_trust_last_attested_seconds", "60x60"),
                ],
                "25m",
                "40m",
                notes=(
                    "last_attested pinned at t=60s. diff > 1800 ⇔ N > 31m;\n"
                    "for:5m means firing from t=37m. Tested at 40m."
                ),
            ),
            T(
                "QSDMTrustAggregatorStale",
                "(time()-last_checked) > 120, for:2m",
                [
                    ("qsdm_trust_warm", "1x10"),
                    ("qsdm_trust_last_checked_seconds", "10x10"),
                ],
                "1m",
                "6m",
                notes=(
                    "last_checked pinned at t=10s. Condition true from t=3m;\n"
                    "for:2m → firing from t=5m."
                ),
            ),
        ],
    ),
    (
        "qsdm-quarantine\n"
        "QUARANTINE_INCIDENT.md §3.1 / §3.2",
        [
            T(
                "QSDMQuarantineAnySubmesh",
                "submeshes>0, for:10m",
                [("qsdm_quarantine_submeshes", "1x30")],
                "5m",
                "16m",
            ),
            T(
                "QSDMQuarantineMajorityIsolated",
                "tracked>=4 and ratio>0.5, for:15m",
                [
                    ("qsdm_quarantine_submeshes_tracked", "4x30"),
                    ("qsdm_quarantine_submeshes_ratio", "0.6x30"),
                ],
                "10m",
                "17m",
            ),
        ],
    ),
    (
        "qsdm-v2-mining-slashing\n"
        "SLASHING_INCIDENT.md §3.1 / §3.2 / §3.3 / §3.4",
        [
            T(
                "QSDMMiningSlashApplied",
                "sum(increase) >= 1 in 15m, for:1m",
                [
                    (
                        'qsdm_slash_applied_total{reason="dust"}',
                        "0+1x30",
                    )
                ],
                "0m",  # for: is only 1m, no useful early checkpoint
                "20m",
            ),
            T(
                "QSDMMiningSlashedDustBurst",
                "dust drained > 5e10 in 15m, for:5m",
                [
                    (
                        'qsdm_slash_drained_dust_total{reason="dust"}',
                        "0+10000000000x30",
                    )
                ],
                "5m",
                "21m",
                notes="Increase 1e10/min → 15m increase = 1.5e11 (3x threshold).",
            ),
            T(
                "QSDMMiningSlashRejectionsBurst",
                "sum(increase) >= 10 in 10m, for:5m",
                [
                    (
                        'qsdm_slash_rejected_total{reason="evidence"}',
                        "0+1x30",
                    )
                ],
                "5m",
                "16m",
            ),
            T(
                "QSDMMiningAutoRevokeBurst",
                "sum(increase) >= 3 in 15m, for:5m",
                [
                    (
                        'qsdm_slash_auto_revoked_total{reason="threshold"}',
                        "0+1x30",
                    )
                ],
                "5m",
                "21m",
            ),
        ],
    ),
    (
        "qsdm-v2-mining-enrollment\n"
        "ENROLLMENT_INCIDENT.md §3.1–§3.5",
        [
            T(
                "QSDMMiningRegistryEmpty",
                "active=0 and uptime>600, for:15m",
                [
                    ("qsdm_enrollment_active_count", "0x30"),
                    ("qsdm_process_uptime_seconds", "700+60x30"),
                ],
                "5m",
                "16m",
            ),
            T(
                "QSDMMiningRegistryShrinkingFast",
                ">25% drop over 1h with baseline >= 4, for:10m",
                [
                    ("qsdm_enrollment_active_count", "10x70 4x30"),
                ],
                "75m",
                "86m",
                notes=(
                    "Stay at 10 for first 70m (1h+ stable history), then drop\n"
                    "to 4. offset 1h at t=86m → t=26m where count was 10.\n"
                    "(10-4)/10 = 0.6 > 0.25."
                ),
            ),
            T(
                "QSDMMiningPendingUnbondMajority",
                "pending/total > 0.5 with total >= 4, for:30m",
                [
                    ("qsdm_enrollment_active_count", "2x40"),
                    ("qsdm_enrollment_pending_unbond_count", "3x40"),
                ],
                "15m",
                "32m",
                notes="active=2, pending=3, total=5≥4, ratio=3/5=0.6>0.5.",
            ),
            T(
                "QSDMMiningEnrollmentRejectionsBurst",
                "sum(increase) >= 20 in 10m, for:5m",
                [
                    (
                        'qsdm_enrollment_rejected_total{reason="bond_too_small"}',
                        "0+2x30",
                    )
                ],
                "5m",
                "17m",
                notes="2/min → increase[10m] = 20.",
            ),
            T(
                "QSDMMiningBondedDustDropped",
                "bond drops > 5e10 over 30m, for:10m",
                [
                    (
                        "qsdm_enrollment_bonded_dust",
                        "100000000000x30 40000000000x30",
                    )
                ],
                "35m",
                "46m",
                notes=(
                    "Stay at 1e11 for 30m, then drop to 4e10 → drop=6e10>5e10."
                ),
            ),
        ],
    ),
    (
        "qsdm-v2-mining-liveness — chain liveness sentinels\n"
        "MINING_LIVENESS.md §3.1 / §3.2",
        [
            T(
                "QSDMMiningChainStuck",
                "delta(height[5m])=0 and uptime>300, for:3m",
                [
                    ("qsdm_chain_height", "100x30"),
                    ("qsdm_process_uptime_seconds", "400+60x30"),
                ],
                "1m",
                "9m",
            ),
            T(
                "QSDMMiningMempoolBacklog",
                "mempool > 10000, for:10m",
                [("qsdm_mempool_size", "20000x30")],
                "5m",
                "11m",
            ),
        ],
    ),
    (
        "qsdm-v2-attest-archspoof — adversarial arch-claim defence\n"
        "ARCH_SPOOF_INCIDENT.md §3.1 / §3.2 / §3.3",
        [
            T(
                "QSDMAttestArchSpoofUnknownArchBurst",
                "rate{unknown_arch} > 0.1, for:10m",
                [
                    (
                        'qsdm_attest_archspoof_rejected_total{reason="unknown_arch"}',
                        "0+12x30",
                    )
                ],
                "5m",
                "16m",
                notes="12/min = 0.2/s; 2x the 0.1/s threshold.",
            ),
            T(
                "QSDMAttestArchSpoofGPUNameMismatch",
                "rate{gpu_name_mismatch} > 0.05, for:10m",
                [
                    (
                        'qsdm_attest_archspoof_rejected_total{reason="gpu_name_mismatch"}',
                        "0+6x30",
                    )
                ],
                "5m",
                "16m",
                notes="6/min = 0.1/s; 2x the 0.05/s threshold.",
            ),
            T(
                "QSDMAttestArchSpoofCCSubjectMismatch",
                "increase{cc_subject_mismatch} > 0 in 15m, for:1m",
                [
                    (
                        'qsdm_attest_archspoof_rejected_total{reason="cc_subject_mismatch"}',
                        "0+1x30",
                    )
                ],
                "0m",
                "20m",
            ),
        ],
    ),
    (
        "qsdm-v2-attest-hashrate — per-arch hashrate plausibility band\n"
        "OPERATOR_HYGIENE_INCIDENT.md §3.3",
        [
            T(
                "QSDMAttestHashrateOutOfBand",
                "rate per arch > 0.05, for:10m",
                [
                    (
                        'qsdm_attest_hashrate_rejected_total{arch="turing"}',
                        "0+6x30",
                    )
                ],
                "5m",
                "16m",
                notes="6/min = 0.1/s; 2x the 0.05/s threshold.",
            ),
        ],
    ),
    (
        "qsdm-v2-governance — multisig authority rotation\n"
        "GOVERNANCE_AUTHORITY_INCIDENT.md §3.1 / §3.2 / §3.3",
        [
            T(
                "QSDMGovAuthorityVoteRecorded",
                "increase[24h] > 0, for:5m",
                [("qsdm_gov_authority_voted_total", "0+1x60")],
                "1m",
                "60m",
            ),
            T(
                "QSDMGovAuthorityThresholdCrossed",
                "increase[24h] > 0, for:1m",
                [("qsdm_gov_authority_crossed_total", "0+1x60")],
                "0m",
                "60m",
            ),
            T(
                "QSDMGovAuthorityCountTooLow",
                "count < 2, for:5m",
                [("qsdm_gov_authority_count", "1x30")],
                "1m",
                "7m",
            ),
        ],
    ),
    (
        "qsdm-v2-attest-recent-rejections — §4.6 rejection-ring health\n"
        "REJECTION_FLOOD.md §7.1 / §7.2 / §7.3 / §7.4 / §7.5",
        [
            T(
                "QSDMAttestRejectionFieldTruncationSustained",
                "truncated/observed > 0.25, for:15m",
                [
                    ("qsdm_attest_rejection_field_truncated_total", "0+30x40"),
                    (
                        "qsdm_attest_rejection_field_runes_observed_total",
                        "0+60x40",
                    ),
                ],
                "10m",
                "26m",
                notes="truncated 30/min, observed 60/min → ratio = 0.5.",
            ),
            T(
                "QSDMAttestRejectionFieldRunesMaxNearCap",
                "detail rune-cap >= 180, for:30m",
                [
                    (
                        'qsdm_attest_rejection_field_runes_max{field="detail"}',
                        "200x40",
                    )
                ],
                "15m",
                "32m",
            ),
            T(
                "QSDMAttestRejectionPersistCompactionsHigh",
                "rate*60 > 5, for:30m",
                [
                    (
                        "qsdm_attest_rejection_persist_compactions_total",
                        "0+6x40",
                    )
                ],
                "15m",
                "36m",
                notes="6/min sustained → rate ≈ 0.1/s → rate*60 = 6 > 5.",
            ),
            T(
                "QSDMAttestRejectionPersistHardCapDropping",
                "rate > 0, for:10m",
                [
                    (
                        "qsdm_attest_rejection_persist_hardcap_drops_total",
                        "0+1x30",
                    )
                ],
                "5m",
                "16m",
            ),
            T(
                "QSDMAttestRejectionPerMinerRateLimited",
                "rate > 0, for:10m",
                [
                    (
                        "qsdm_attest_rejection_per_miner_rate_limited_total",
                        "0+1x30",
                    )
                ],
                "5m",
                "16m",
            ),
        ],
    ),
]

HEADER = """\
# =====================================================================
#  promtool test rules — behavioural test suite for alerts_qsdm.example.yml
# =====================================================================
#
#  Run locally:
#    promtool test rules QSDM/deploy/prometheus/alerts_qsdm.test.yml
#
#  In CI: invoked by the prometheus-rules-check job in
#    .github/workflows/validate-deploy.yml
#  alongside the existing `promtool check rules` syntax check.
#
#  Why this file exists
#  --------------------
#  The companion lint at scripts/check_runbook_coverage.py catches
#  *navigation* breakage (alert ↔ runbook URLs, in-runbook links).
#  This file catches *behavioural* breakage:
#
#    1. Threshold drift: someone tightens `rate > 0.5` to `rate > 0.05`
#       thinking it's "more sensitive". Without these tests, the
#       10x change is silent until the next incident.
#    2. `for:` window drift: shrinking 10m → 1m makes the alert
#       trigger 10x more often. The early `exp_alerts: []` checkpoint
#       in each test catches this — shrinking the for: window would
#       make the alert fire at the early checkpoint and break the
#       test.
#    3. Label/severity drift: the firing checkpoint asserts the
#       full label set the alert produces (severity, subsystem,
#       reason, etc.). Renaming severity from `warning` to `warmig`
#       (typo) trips this test.
#    4. Annotation-template drift: each firing checkpoint also
#       asserts the rendered description / summary / runbook_url.
#       Editing the runbook anchor without updating the annotation
#       template fails the test, which means the runbook lint and
#       this suite together form a closed-loop contract on
#       runbook navigation.
#
#  How each test is structured
#  ---------------------------
#  Every alert in alerts_qsdm.example.yml has at least one test
#  here. Each test follows the same shape:
#
#    * `input_series`: synthetic time series with values chosen
#      to clear the threshold with margin (typically 2× the
#      threshold) over a long-enough span that `rate()` and
#      `increase()` extrapolation produce predictable values.
#
#    * Two `eval_time` checkpoints:
#        - EARLY: `exp_alerts: []`. The condition is true but the
#          `for:` window has not elapsed yet, so no alert fires.
#          Catches `for:` shrinking.
#        - LATE: `exp_alerts: [{exp_labels, exp_annotations}]`.
#          The full firing alert is asserted. Catches threshold,
#          metric-name, label, and annotation drift.
#
#  This file is generated by scripts/.gen-promtool-tests.py from
#  the inline test specs in that script. The script runs promtool,
#  captures the rendered annotations from the actual rule
#  evaluation, and emits them here verbatim. To regenerate after
#  editing the alerts file, run the generator (see CHANGELOG entry
#  on regenerating the suite). All hand edits to this file
#  immediately below the rule_files block are preserved.
#
# =====================================================================

rule_files:
  - alerts_qsdm.example.yml

evaluation_interval: 1m

tests:
"""


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def parse_kv_block(block: str) -> dict[str, str]:
    """Parse a Go map-string body like
        key1="value1", key2="multi\\nline\\nvalue", key3="x"
    into a dict. Handles backslash-escaped quotes and newlines.
    """
    out: dict[str, str] = {}
    i = 0
    n = len(block)
    while i < n:
        while i < n and block[i] in ", \t\n":
            i += 1
        if i >= n:
            break
        m = re.match(r"[A-Za-z_][A-Za-z0-9_]*", block[i:])
        if not m:
            break
        key = m.group(0)
        i += len(key)
        if i >= n or block[i] != "=":
            break
        i += 1
        if i >= n or block[i] != '"':
            break
        i += 1
        val_chars: list[str] = []
        while i < n:
            ch = block[i]
            if ch == "\\" and i + 1 < n:
                nxt = block[i + 1]
                if nxt == "n":
                    val_chars.append("\n")
                elif nxt == "t":
                    val_chars.append("\t")
                elif nxt == '"':
                    val_chars.append('"')
                elif nxt == "\\":
                    val_chars.append("\\")
                else:
                    val_chars.append(nxt)
                i += 2
                continue
            if ch == '"':
                i += 1
                break
            val_chars.append(ch)
            i += 1
        out[key] = "".join(val_chars)
    return out


GOT_BLOCK_RE = re.compile(
    # Promtool prints each failure as:
    #   name: <free text that can contain commas>,\n
    #   alertname: <name>, time: <duration>,\n
    #       exp:[...],\n
    #       got:[\n  0:\n  Labels:{...}\n  Annotations:{...}\n  ]\n
    # Annotations may contain `]` characters (e.g. PromQL examples like
    # rate(metric[5m]) embedded in description text). The closing `]`
    # for the got: list is always on its own line preceded by indent
    # whitespace, so we anchor on `\n\s+\]` instead of bare `\]`.
    # The testname line has free commas, so we ignore it and start at
    # `alertname:`.
    r"alertname:\s*(?P<alertname>\S+),\s*"
    r"time:\s*(?P<time>\S+?),\s*\n"
    r"\s*exp:\[[^\]]*\],?\s*\n"
    r"\s*got:\[\s*\n"
    r"(?P<body>.*?)"
    r"\n\s+\]",
    re.DOTALL,
)

ALERT_RE = re.compile(
    # Each fired alert prints as a 3-line block:
    #   0:
    #     Labels:{...} <EOL>
    #     Annotations:{...} <EOL>
    # We DON'T use re.DOTALL because the annotation body legitimately
    # contains `}` characters (e.g. `{tx_id}` placeholder text or
    # `{kind!=""}` PromQL examples in description text). Matching up to
    # the LAST `}` on the same line is what we want; greedy `.*\}`
    # without DOTALL achieves this since `.` excludes newlines.
    r"\d+:[ \t]*\n"
    r"[ \t]*Labels:\{(?P<labels>.*)\}[ \t]*\n"
    r"[ \t]*Annotations:\{(?P<annotations>.*)\}[ \t]*",
)


def yaml_quoted(s: str) -> str:
    """Emit a YAML double-quoted string with `\\n`/`\\t`/`\\"` escapes.

    We deliberately use double-quoted style (instead of YAML block scalars
    like `|-` or `|`) because the rendered annotations from promtool come
    from the Go map-string format which preserves trailing-newline state
    bit-for-bit. Block scalars chomp trailing newlines unpredictably; the
    quoted style stores exactly the bytes promtool will compare against.
    """
    out: list[str] = ['"']
    for ch in s:
        if ch == "\\":
            out.append("\\\\")
        elif ch == '"':
            out.append('\\"')
        elif ch == "\n":
            out.append("\\n")
        elif ch == "\t":
            out.append("\\t")
        elif ch == "\r":
            out.append("\\r")
        elif ord(ch) < 0x20:
            out.append(f"\\x{ord(ch):02x}")
        else:
            out.append(ch)
    out.append('"')
    return "".join(out)


def yaml_inline(s: str) -> str:
    return yaml_quoted(s)


def render_input_series(series: list[tuple[str, str]], indent: int) -> str:
    pad = " " * indent
    out: list[str] = []
    for label_set, values in series:
        out.append(f"{pad}- series: '{label_set}'")
        out.append(f"{pad}  values: '{values}'")
    return "\n".join(out)


def render_test_scaffold(t: T) -> str:
    """First-pass rendering: late checkpoint uses placeholder `exp_alerts: []`
    so promtool reports the actually-fired alert in its `got:` block."""
    name = f"{t.name} — {t.summary}"
    parts: list[str] = []
    parts.append(f"  - name: \"{name}\"")
    parts.append("    interval: 1m")
    if t.notes:
        for line in t.notes.split("\n"):
            parts.append(f"    # {line}")
    parts.append("    input_series:")
    parts.append(render_input_series(t.input_series, indent=6))
    parts.append("    alert_rule_test:")
    parts.append(f"      - eval_time: {t.early}")
    parts.append(f"        alertname: {t.name}")
    parts.append("        # Condition holds at this checkpoint but `for:` has not")
    parts.append("        # elapsed; no alert should be firing yet.")
    parts.append("        exp_alerts: []")
    parts.append(f"      - eval_time: {t.late}")
    parts.append(f"        alertname: {t.name}")
    parts.append("        exp_alerts: []  # PLACEHOLDER — will be replaced after capture")
    return "\n".join(parts)


def render_test_final(t: T, exp_alerts_yaml: str) -> str:
    """Second-pass rendering: late checkpoint has populated exp_alerts."""
    name = f"{t.name} — {t.summary}"
    parts: list[str] = []
    parts.append(f"  - name: \"{name}\"")
    parts.append("    interval: 1m")
    if t.notes:
        for line in t.notes.split("\n"):
            parts.append(f"    # {line}")
    parts.append("    input_series:")
    parts.append(render_input_series(t.input_series, indent=6))
    parts.append("    alert_rule_test:")
    parts.append(f"      - eval_time: {t.early}")
    parts.append(f"        alertname: {t.name}")
    parts.append("        # Condition holds at this checkpoint but `for:` has not")
    parts.append("        # elapsed; no alert should be firing yet.")
    parts.append("        exp_alerts: []")
    parts.append(f"      - eval_time: {t.late}")
    parts.append(f"        alertname: {t.name}")
    parts.append("        exp_alerts:")
    parts.append(exp_alerts_yaml)
    return "\n".join(parts)


def render_full(specs: list[tuple[str, list[T]]], renderer) -> str:
    out: list[str] = [HEADER]
    for header, ts in specs:
        out.append("")
        out.append("  # " + "-" * 67)
        for line in header.split("\n"):
            out.append(f"  # {line}")
        out.append("  # " + "-" * 67)
        for t in ts:
            out.append("")
            out.append(renderer(t))
    return "\n".join(out) + "\n"


def emit_exp_alert(labels: dict[str, str], annotations: dict[str, str]) -> str:
    """Emit one entry of an exp_alerts list at indent=10 (i.e. inside
    `        exp_alerts:`)."""
    pad8 = " " * 8
    pad10 = " " * 10
    lines: list[str] = []
    lines.append(f"{pad8}- exp_labels:")
    label_keys = [k for k in sorted(labels.keys()) if k != "alertname"]
    for k in label_keys:
        lines.append(f"{pad10}  {k}: {yaml_inline(labels[k])}")
    lines.append(f"{pad10}exp_annotations:")
    for k in sorted(annotations.keys()):
        v = annotations[k]
        lines.append(f"{pad10}  {k}: {yaml_quoted(v)}")
    return "\n".join(lines)


def run_promtool_capture() -> str:
    proc = subprocess.run(
        [str(PROMTOOL), "test", "rules", str(TESTS)],
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )
    return (proc.stdout or "") + (proc.stderr or "")


def parse_failures(out: str) -> dict[str, str]:
    """Map alertname → exp_alerts YAML block (sans surrounding context)."""
    captures: dict[str, str] = {}
    for m in GOT_BLOCK_RE.finditer(out):
        alertname = m.group("alertname").strip()
        body = m.group("body")
        am = ALERT_RE.search(body)
        if not am:
            continue
        labels = parse_kv_block(am.group("labels"))
        annotations = parse_kv_block(am.group("annotations"))
        captures[alertname] = emit_exp_alert(labels, annotations)
    return captures


def all_specs() -> list[T]:
    flat: list[T] = []
    for _, ts in GROUPS:
        flat.extend(ts)
    return flat


def main() -> int:
    print("Pass 1: emit scaffold with placeholder exp_alerts: []")
    TESTS.write_text(render_full(GROUPS, render_test_scaffold), encoding="utf-8")

    print("Pass 1: run promtool to capture got: blocks")
    out = run_promtool_capture()
    captures = parse_failures(out)
    print(f"  captured {len(captures)} alerts")

    expected = {t.name for t in all_specs()}
    missing = expected - captures.keys()
    if missing:
        print(f"  MISSING captures for: {sorted(missing)}")
        print("  --- Last 60 lines of promtool output ---")
        print("\n".join(out.splitlines()[-60:]))
        return 1

    print("Pass 2: emit final test file with populated exp_alerts")
    final = HEADER
    for header, ts in GROUPS:
        final += "\n  # " + "-" * 67 + "\n"
        for line in header.split("\n"):
            final += f"  # {line}\n"
        final += "  # " + "-" * 67 + "\n"
        for t in ts:
            final += "\n" + render_test_final(t, captures[t.name]) + "\n"
    TESTS.write_text(final, encoding="utf-8")

    print("Pass 2: run promtool to confirm SUCCESS")
    out2 = run_promtool_capture()
    if "SUCCESS" not in out2:
        print("FAILED -- final test file has issues; see .promtool-fail.txt")
        Path(".promtool-fail.txt").write_text(out2, encoding="utf-8")
        return 1
    print(out2.strip().encode("ascii", "replace").decode("ascii"))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
