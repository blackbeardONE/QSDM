"""End-to-end generator for alerts_qsdm.test.yml.

Two-pass: first run produces a scaffold whose firing checkpoints
intentionally fail (`exp_alerts: []`), capturing promtool's rendered
`got:[...]` blocks. Second pass rewrites the file with proper
`exp_alerts: [{exp_labels, exp_annotations}]` populated from the
captured renderings, so the final tests truly bind the alert rules
(including labels and annotation templates) to expected behaviour.

Test specs live in
``QSDM/deploy/prometheus/alerts_qsdm.test.spec.yml``. That file is
the human-edited declarative source: one entry per alert, naming
the input series, eval-time checkpoints, and any explanatory notes.
The generator validates 1:1 coverage between alertnames in the
alerts file and entries in the spec file at every run, so the
common drift modes (add an alert without a spec, remove an alert
without removing the spec) fail loudly.

Run:
    python scripts/gen_promtool_tests.py

By default the script looks for `promtool` on PATH. To override (e.g.
a Windows-local download), set the `PROMTOOL` env var to the binary
path.

The script is idempotent — running it multiple times converges to the
same test-file output for the same alerts file + spec file pair.
"""
from __future__ import annotations

import dataclasses
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    print(
        "ERROR: PyYAML not installed. Install with: pip install PyYAML",
        file=sys.stderr,
    )
    sys.exit(2)

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
SPEC = REPO / "QSDM" / "deploy" / "prometheus" / "alerts_qsdm.test.spec.yml"


# ---------------------------------------------------------------------------
# Test specs — loaded from alerts_qsdm.test.spec.yml (one entry per alert).
# `T` is the in-memory shape the rest of the generator already operates on;
# the YAML loader populates it from the declarative spec file.
# ---------------------------------------------------------------------------

@dataclasses.dataclass
class T:
    name: str
    summary: str  # one-line description of the firing condition (for test name)
    input_series: list[tuple[str, str]]  # [(series_label_set, values_expr), ...]
    early: str  # eval_time string for the negative checkpoint
    late: str  # eval_time string for the firing checkpoint
    notes: str = ""  # optional extra comment lines for the test


def _load_groups_from_spec(spec_path: Path) -> list[tuple[str, list[T]]]:
    """Parse the YAML spec file into the same shape the rest of the
    generator uses (a list of (header, [T]) pairs).

    Validates structural invariants: every group has a header + tests
    list; every test has the required fields; series/values pairs are
    well-formed. Schema errors raise SystemExit with a precise pointer
    so spec-file edits fail fast instead of producing a malformed
    test file.
    """
    if not spec_path.exists():
        sys.exit(
            f"ERROR: spec file not found: {spec_path.relative_to(REPO)}\n"
            f"  Expected the declarative test spec at the path above."
        )
    try:
        raw = yaml.safe_load(spec_path.read_text(encoding="utf-8"))
    except yaml.YAMLError as e:
        sys.exit(f"ERROR: spec file is not valid YAML: {e}")
    if not isinstance(raw, dict) or "groups" not in raw:
        sys.exit(
            "ERROR: spec file must be a mapping with a top-level "
            "`groups:` key."
        )
    groups_raw = raw["groups"] or []
    if not isinstance(groups_raw, list):
        sys.exit("ERROR: spec.groups must be a list.")
    out: list[tuple[str, list[T]]] = []
    for gi, g in enumerate(groups_raw):
        if not isinstance(g, dict):
            sys.exit(f"ERROR: spec.groups[{gi}] must be a mapping.")
        header = g.get("header", "")
        if not isinstance(header, str):
            sys.exit(f"ERROR: spec.groups[{gi}].header must be a string.")
        tests_raw = g.get("tests") or []
        if not isinstance(tests_raw, list):
            sys.exit(f"ERROR: spec.groups[{gi}].tests must be a list.")
        ts: list[T] = []
        for ti, t in enumerate(tests_raw):
            loc = f"spec.groups[{gi}].tests[{ti}]"
            if not isinstance(t, dict):
                sys.exit(f"ERROR: {loc} must be a mapping.")
            for required in ("name", "summary", "early", "late", "input_series"):
                if required not in t:
                    sys.exit(f"ERROR: {loc} missing required field `{required}`.")
            name = str(t["name"])
            summary = str(t["summary"])
            early = str(t["early"])
            late = str(t["late"])
            notes = str(t.get("notes") or "").rstrip("\n")
            series_raw = t["input_series"]
            if not isinstance(series_raw, list) or not series_raw:
                sys.exit(
                    f"ERROR: {loc}.input_series must be a non-empty list."
                )
            series: list[tuple[str, str]] = []
            for si, s in enumerate(series_raw):
                if (
                    not isinstance(s, dict)
                    or "series" not in s
                    or "values" not in s
                ):
                    sys.exit(
                        f"ERROR: {loc}.input_series[{si}] must be a "
                        "mapping with `series:` and `values:` keys."
                    )
                series.append((str(s["series"]), str(s["values"])))
            ts.append(
                T(
                    name=name,
                    summary=summary,
                    input_series=series,
                    early=early,
                    late=late,
                    notes=notes,
                )
            )
        # Strip a single trailing newline from block-scalar headers
        # so downstream rendering matches the prior in-script form.
        out.append((header.rstrip("\n"), ts))
    return out


def _alertnames_in_alerts_file(alerts_path: Path) -> list[str]:
    """Return the ordered list of alertnames from alerts_qsdm.example.yml.

    Used by the coverage validator below. Reads the YAML rather than
    regex-matching so renames / re-indents in the alerts file don't
    silently break the validator.
    """
    try:
        data = yaml.safe_load(alerts_path.read_text(encoding="utf-8"))
    except yaml.YAMLError as e:
        sys.exit(f"ERROR: alerts file is not valid YAML: {e}")
    if not isinstance(data, dict):
        sys.exit("ERROR: alerts file root must be a mapping.")
    names: list[str] = []
    for grp in data.get("groups") or []:
        for rule in grp.get("rules") or []:
            n = rule.get("alert")
            if isinstance(n, str):
                names.append(n)
    return names


def _validate_coverage(groups: list[tuple[str, list[T]]]) -> None:
    """Fail loudly if the spec doesn't 1:1 cover the alerts file.

    Catches the two everyday drift modes:
      - new alert added with no matching spec (alerts_only)
      - spec entry left behind after an alert was removed/renamed
        (spec_only)
    Also catches duplicate spec entries for the same alertname.
    """
    spec_names: list[str] = []
    for _, ts in groups:
        for t in ts:
            spec_names.append(t.name)

    duplicates = sorted({n for n in spec_names if spec_names.count(n) > 1})
    if duplicates:
        sys.exit(
            "ERROR: spec file has duplicate test entries for "
            f"alertname(s): {duplicates}"
        )

    alerts_names = _alertnames_in_alerts_file(ALERTS)
    alerts_set = set(alerts_names)
    spec_set = set(spec_names)

    missing_specs = sorted(alerts_set - spec_set)
    orphan_specs = sorted(spec_set - alerts_set)

    if missing_specs or orphan_specs:
        msg: list[str] = ["ERROR: alerts ↔ spec coverage mismatch."]
        if missing_specs:
            msg.append("  Alerts missing a spec entry (add to spec file):")
            for n in missing_specs:
                msg.append(f"    - {n}")
        if orphan_specs:
            msg.append(
                "  Spec entries with no matching alert (remove or rename):"
            )
            for n in orphan_specs:
                msg.append(f"    - {n}")
        sys.exit("\n".join(msg))


GROUPS: list[tuple[str, list[T]]] = _load_groups_from_spec(SPEC)
_validate_coverage(GROUPS)


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
#  This file is generated by scripts/gen_promtool_tests.py from
#  the declarative spec at alerts_qsdm.test.spec.yml. The
#  generator validates 1:1 coverage between alertnames in the
#  alerts file and entries in the spec, then runs promtool to
#  capture the rendered Labels and Annotations and embed them
#  here verbatim. Hand edits to THIS file are not preserved
#  across regenerations — edit alerts_qsdm.test.spec.yml and
#  re-run the generator instead.
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
