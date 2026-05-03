"""Generate Grafana dashboard JSONs from alerts_qsdm.example.yml.

For each unique runbook referenced from the alerts file we emit a
dedicated dashboard JSON containing one stat panel per alert that
points at that runbook. We also emit a master "alerts overview"
dashboard with every alert in collapsible rows grouped by runbook.

The dashboards are *deliberately* uniform: there's one panel layout
for every alert (a `stat` panel showing the alert's LHS metric
expression with a colour threshold derived from the alert's RHS
constant). This is the simplest possible artefact that gives an
on-call operator real value at incident time:

  * Dashboard URL on the alert    → operator clicks PagerDuty link
  * Lands on the runbook dashboard → sees the firing panel red
  * Visually compares to peers    → distinguishes "single bad
                                     instance" from "fleet-wide"

It is NOT a hand-tuned dashboard — for that, the operator should
copy these as a starting point and customise. That's a deliberate
scope choice: a generated baseline that always matches the alerts
file is more useful than a hand-crafted dashboard that drifts.

Run:
    python scripts/gen_grafana_dashboards.py

The script is idempotent — re-running on the same alerts file
produces byte-identical output. Stable panel IDs are derived from
the alphabetical ordering of alertnames so dashboard URLs (which
embed `?viewPanel=<id>`) remain stable across regenerations even
when alerts are added or removed elsewhere in the file.

Adding a new alert
------------------
1. Add the rule to alerts_qsdm.example.yml with a runbook_url
   annotation.
2. Run `python scripts/gen_grafana_dashboards.py` — the new alert
   automatically appears as a panel in the relevant per-runbook
   dashboard and in the master overview.
3. The runbook coverage lint (scripts/check_runbook_coverage.py)
   then validates the new alert has a dashboard_url annotation
   pointing at the regenerated file.
"""
from __future__ import annotations

import json
import os
import re
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


REPO = Path(__file__).resolve().parent.parent
ALERTS = REPO / "QSDM" / "deploy" / "prometheus" / "alerts_qsdm.example.yml"
DASHBOARDS_DIR = REPO / "QSDM" / "deploy" / "grafana" / "dashboards"

# Master dashboard filename + uid. The per-runbook dashboards use
# `qsdm-runbook-<slug>` for both filename stem and uid.
MASTER_FILENAME = "qsdm-alerts-overview.json"
MASTER_UID = "qsdm-alerts-overview"


# ---------------------------------------------------------------------------
# Alerts file loading + grouping
# ---------------------------------------------------------------------------


def load_alerts() -> list[dict]:
    """Return a flat list of {name, expr, severity, runbook_url, runbook_file}.

    Order follows the alerts file. Alerts without a runbook_url
    annotation are skipped (they wouldn't have a dashboard
    section to live under).
    """
    data = yaml.safe_load(ALERTS.read_text(encoding="utf-8"))
    out: list[dict] = []
    for grp in data.get("groups") or []:
        for rule in grp.get("rules") or []:
            name = rule.get("alert")
            expr = rule.get("expr")
            severity = (rule.get("labels") or {}).get("severity", "warning")
            ann = rule.get("annotations") or {}
            runbook_url = ann.get("runbook_url", "")
            if not (name and expr and runbook_url):
                continue
            runbook_file = _runbook_basename(runbook_url)
            out.append(
                {
                    "name": str(name),
                    "expr": str(expr),
                    "severity": str(severity),
                    "runbook_url": str(runbook_url),
                    "runbook_file": runbook_file,
                }
            )
    return out


def _runbook_basename(url: str) -> str:
    """Extract the .md filename from a runbook_url, e.g.
    'https://.../OPERATOR_HYGIENE_INCIDENT.md#section' -> 'OPERATOR_HYGIENE_INCIDENT.md'.
    """
    path = url.split("#", 1)[0]
    return path.rsplit("/", 1)[-1] if "/" in path else path


def group_by_runbook(alerts: list[dict]) -> dict[str, list[dict]]:
    """Return {runbook_file: [alerts]} preserving alerts-file order."""
    out: dict[str, list[dict]] = {}
    for a in alerts:
        out.setdefault(a["runbook_file"], []).append(a)
    return out


def _slug(name: str) -> str:
    """Convert e.g. 'OPERATOR_HYGIENE_INCIDENT.md' -> 'operator-hygiene-incident'."""
    base = name.removesuffix(".md")
    s = base.lower()
    s = re.sub(r"[^a-z0-9]+", "-", s).strip("-")
    return s


# ---------------------------------------------------------------------------
# PromQL expression splitting (for threshold extraction)
# ---------------------------------------------------------------------------


# Comparison operators that map cleanly to a Grafana threshold step
# (numeric crossover point with a single firing direction).
# Equality (`==` / `!=`) is intentionally excluded: stepped thresholds
# can't express "equal", so for those expressions we render the full
# boolean PromQL (0/1) and skip the threshold line.
_THRESHOLD_OPS = (">=", "<=", ">", "<")
_ALL_CMP_OPS = (">=", "<=", "==", "!=", ">", "<")

# PromQL set operators. When any of these appear at top level the
# expression is compound (e.g. `a > 1 and b < 2`) and splitting at
# a single comparison would conflate the two clauses. We render
# the full boolean expression instead — operationally clearer than
# a misleadingly-thresholded LHS.
_SET_OPS = ("and", "or", "unless")


def _has_top_level_set_op(expr: str) -> bool:
    """Return True iff `and`/`or`/`unless` appears at top level."""
    depth = 0
    quote: str | None = None
    i = 0
    n = len(expr)
    while i < n:
        ch = expr[i]
        if quote is not None:
            if ch == "\\" and i + 1 < n:
                i += 2
                continue
            if ch == quote:
                quote = None
            i += 1
            continue
        if ch in "\"'":
            quote = ch
            i += 1
            continue
        if ch in "([{":
            depth += 1
            i += 1
            continue
        if ch in ")]}":
            depth -= 1
            i += 1
            continue
        if depth == 0 and (ch.isspace() or i == 0):
            for op in _SET_OPS:
                # Set ops must be whitespace-bounded on both sides
                # (PromQL keywords, not identifiers). Check both
                # leading boundary (current ch is whitespace, or i==0)
                # and trailing boundary (next char is whitespace).
                start = i if ch.isspace() else i
                # When ch is whitespace, the keyword candidate starts
                # at i+1; when i==0, it starts at 0.
                kw_start = start + 1 if ch.isspace() else start
                if expr.startswith(op, kw_start):
                    end = kw_start + len(op)
                    if end >= n or expr[end].isspace() or expr[end] in "({":
                        return True
        i += 1
    return False


def split_at_rightmost_comparison(
    expr: str,
) -> tuple[str, str, str] | None:
    """Split a PromQL expression at its rightmost top-level comparison.

    Returns (lhs, op, rhs) or None if no top-level comparison exists.
    Tracks paren / bracket / brace depth and string quoting so e.g.
    `sum(rate(m{a="b"}[5m])) > 0.5` correctly returns
    (`sum(rate(m{a="b"}[5m]))`, `>`, `0.5`).
    """
    depth = 0
    quote: str | None = None
    matches: list[tuple[int, str]] = []
    i = 0
    while i < len(expr):
        ch = expr[i]
        if quote is not None:
            if ch == "\\" and i + 1 < len(expr):
                i += 2
                continue
            if ch == quote:
                quote = None
            i += 1
            continue
        if ch in "\"'":
            quote = ch
            i += 1
            continue
        if ch in "([{":
            depth += 1
            i += 1
            continue
        if ch in ")]}":
            depth -= 1
            i += 1
            continue
        if depth == 0:
            for op in _ALL_CMP_OPS:
                if expr.startswith(op, i):
                    matches.append((i, op))
                    i += len(op)
                    break
            else:
                i += 1
                continue
            continue
        i += 1
    if not matches:
        return None
    pos, op = matches[-1]
    return expr[:pos].strip(), op, expr[pos + len(op):].strip()


def parse_threshold(expr: str) -> tuple[str, str, float | None]:
    """Return (panel_expr, op, threshold_value).

    Threshold extraction is conservative — we only split when the
    result will give the operator a clean threshold-coloured stat
    panel. The fallback (full expr, no threshold) is always safe:
    PromQL boolean comparisons evaluate to 0 or 1, so the panel
    just shows "alert is/isn't firing".

    Splits ARE applied when:
      * The rightmost top-level operator is `>`, `<`, `>=`, `<=`.
      * The RHS parses as a numeric literal.
      * The expression has no top-level `and` / `or` / `unless`
        (compound expressions render as full boolean — splitting
        at one half would mislead the operator about the other).

    Examples:
        rate(m[5m]) > 0.5                  -> ("rate(m[5m])",  ">", 0.5)
        increase(m[15m]) > 0               -> ("increase(...)",">", 0.0)
        (a - b) > 1 + sum(c)               -> (full expr,      "",  None)
        a >= 4 and b > 0.5                 -> (full expr,      "",  None)
        sum(rate(m[20m])) == 0             -> (full expr,      "",  None)
    """
    if _has_top_level_set_op(expr):
        return expr, "", None
    parts = split_at_rightmost_comparison(expr)
    if parts is None:
        return expr, "", None
    lhs, op, rhs = parts
    if op not in _THRESHOLD_OPS:
        return expr, "", None
    rhs_clean = rhs.strip()
    try:
        v = float(rhs_clean)
    except ValueError:
        return expr, "", None
    return lhs, op, v


# ---------------------------------------------------------------------------
# Dashboard JSON construction
# ---------------------------------------------------------------------------

DS_PROMETHEUS = "${DS_PROMETHEUS}"


# Stable per-alert panel IDs derived from the alphabetical alertname
# ordering across the entire alerts file. Used in URLs so a panel
# keeps its `?viewPanel=N` value across regenerations.
def _stable_panel_ids(alerts: list[dict]) -> dict[str, int]:
    return {
        name: i + 1
        for i, name in enumerate(sorted(a["name"] for a in alerts))
    }


def _severity_color(severity: str) -> str:
    """Map severity → Grafana threshold colour for the firing state."""
    return {
        "critical": "red",
        "warning": "orange",
        "info": "yellow",
    }.get(severity.lower(), "orange")


def _normalise_promql(expr: str) -> str:
    """Collapse whitespace runs to single spaces.

    YAML block-folded `expr:` values from the alerts file may have
    embedded newlines (e.g. when an alert expression is split
    across multiple YAML lines). PromQL is whitespace-agnostic,
    but rendering the embedded newlines in dashboard JSON looks
    ugly to anyone opening the dashboard in the Grafana UI.
    """
    return re.sub(r"\s+", " ", expr).strip()


def _alert_panel(
    alert: dict, panel_id: int, grid_x: int, grid_y: int
) -> dict:
    """Build one stat panel for a single alert."""
    panel_expr, op, threshold = parse_threshold(alert["expr"])
    panel_expr = _normalise_promql(panel_expr)
    fire_color = _severity_color(alert["severity"])

    # Threshold steps: Grafana steps are "from this value upward",
    # so the first step always has value=null (covers `[-inf, ...)`).
    steps: list[dict]
    if threshold is None:
        # No clean numeric threshold extractable. The panel
        # expression is the full PromQL comparison, which evaluates
        # to 0 (not firing) or 1 (firing). Colour 0 green, 1+ severity.
        steps = [
            {"color": "green", "value": None},
            {"color": fire_color, "value": 1},
        ]
    elif op in ("<", "<="):
        # Below-threshold = firing. Severity colour covers
        # `[-inf, threshold)`, green covers `[threshold, inf)`.
        steps = [
            {"color": fire_color, "value": None},
            {"color": "green", "value": threshold},
        ]
    else:
        # Above-threshold = firing (`>` / `>=`). Green covers
        # `[-inf, threshold)`, severity covers `[threshold, inf)`.
        steps = [
            {"color": "green", "value": None},
            {"color": fire_color, "value": threshold},
        ]

    description_lines = [
        f"**Alert**: `{alert['name']}`",
        f"**Severity**: `{alert['severity']}`",
        f"**Trigger**: `{_normalise_promql(alert['expr'])}`",
        f"**Runbook**: {alert['runbook_url']}",
    ]
    description = "\n\n".join(description_lines)

    return {
        "id": panel_id,
        "type": "stat",
        "title": alert["name"],
        "description": description,
        "datasource": {"type": "prometheus", "uid": DS_PROMETHEUS},
        "gridPos": {"h": 6, "w": 6, "x": grid_x, "y": grid_y},
        "targets": [
            {
                "datasource": {"type": "prometheus", "uid": DS_PROMETHEUS},
                "expr": panel_expr,
                "instant": False,
                "range": True,
                "refId": "A",
                "legendFormat": "{{instance}}",
            }
        ],
        "options": {
            "reduceOptions": {
                "values": False,
                "calcs": ["lastNotNull"],
                "fields": "",
            },
            "orientation": "auto",
            "textMode": "auto",
            "colorMode": "background",
            "graphMode": "area",
            "justifyMode": "auto",
        },
        "fieldConfig": {
            "defaults": {
                "thresholds": {
                    "mode": "absolute",
                    "steps": steps,
                },
                "color": {"mode": "thresholds"},
                "unit": "short",
            },
            "overrides": [],
        },
    }


def _row_panel(panel_id: int, grid_y: int, title: str, collapsed: bool) -> dict:
    return {
        "id": panel_id,
        "type": "row",
        "title": title,
        "collapsed": collapsed,
        "gridPos": {"h": 1, "w": 24, "x": 0, "y": grid_y},
        "panels": [],
    }


def _dashboard_skeleton(uid: str, title: str, tags: list[str]) -> dict:
    return {
        "annotations": {
            "list": [
                {
                    "builtIn": 1,
                    "datasource": {"type": "grafana", "uid": "-- Grafana --"},
                    "enable": True,
                    "hide": True,
                    "iconColor": "rgba(0, 211, 255, 1)",
                    "name": "Annotations & Alerts",
                    "type": "dashboard",
                }
            ]
        },
        "description": (
            "Auto-generated by scripts/gen_grafana_dashboards.py from "
            "alerts_qsdm.example.yml. Edit the alerts file (or the "
            "generator) and re-run, do not hand-edit this JSON — your "
            "changes will be overwritten on the next regeneration."
        ),
        "editable": True,
        "fiscalYearStartMonth": 0,
        "graphTooltip": 0,
        "id": None,
        "links": [],
        "liveNow": False,
        "panels": [],
        "refresh": "30s",
        "schemaVersion": 39,
        "style": "dark",
        "tags": tags,
        "templating": {"list": []},
        "time": {"from": "now-1h", "to": "now"},
        "timepicker": {},
        "timezone": "",
        "title": title,
        "uid": uid,
        "version": 1,
        "weekStart": "",
        "__inputs": [
            {
                "name": "DS_PROMETHEUS",
                "label": "Prometheus",
                "description": "",
                "type": "datasource",
                "pluginId": "prometheus",
                "pluginName": "Prometheus",
            }
        ],
        "__requires": [
            {
                "type": "datasource",
                "id": "prometheus",
                "name": "Prometheus",
                "version": "1.0.0",
            },
        ],
    }


def _layout_panels_two_per_row(
    alerts: list[dict],
    panel_ids: dict[str, int],
    start_y: int,
) -> tuple[list[dict], int]:
    """Lay out alert panels four-per-row on the 24-column grid.

    Each stat panel is 6 columns wide and 6 rows tall, so we fit
    four per row. Returns (panels, next_y).
    """
    panels: list[dict] = []
    y = start_y
    cols_per_row = 4
    for i, a in enumerate(alerts):
        col = i % cols_per_row
        if col == 0 and i > 0:
            y += 6
        panels.append(_alert_panel(a, panel_ids[a["name"]], col * 6, y))
    if alerts:
        y += 6
    return panels, y


# ---------------------------------------------------------------------------
# Per-runbook + master dashboards
# ---------------------------------------------------------------------------


def build_runbook_dashboard(
    runbook_file: str,
    alerts: list[dict],
    panel_ids: dict[str, int],
) -> dict:
    title = f"QSDM — {runbook_file.removesuffix('.md').replace('_', ' ').title()}"
    uid = f"qsdm-runbook-{_slug(runbook_file)}"
    db = _dashboard_skeleton(uid, title, tags=["qsdm", "qsdm-runbook"])
    panels, _ = _layout_panels_two_per_row(alerts, panel_ids, start_y=0)
    db["panels"] = panels
    return db


def build_master_dashboard(
    grouped: dict[str, list[dict]],
    panel_ids: dict[str, int],
) -> dict:
    title = "QSDM — alerts overview"
    db = _dashboard_skeleton(MASTER_UID, title, tags=["qsdm", "qsdm-alerts"])
    panels: list[dict] = []
    # Reserve panel IDs above the alert ID range for row markers.
    row_id_base = max(panel_ids.values()) + 100 if panel_ids else 1000
    y = 0
    for ri, runbook_file in enumerate(sorted(grouped.keys())):
        row_alerts = grouped[runbook_file]
        row_title = (
            f"{runbook_file}  ({len(row_alerts)} alert"
            f"{'s' if len(row_alerts) != 1 else ''})"
        )
        panels.append(
            _row_panel(
                row_id_base + ri,
                grid_y=y,
                title=row_title,
                collapsed=False,
            )
        )
        y += 1
        sub_panels, y = _layout_panels_two_per_row(
            row_alerts, panel_ids, start_y=y
        )
        panels.extend(sub_panels)
    db["panels"] = panels
    return db


# ---------------------------------------------------------------------------
# Output
# ---------------------------------------------------------------------------


def _write_dashboard(path: Path, dashboard: dict) -> None:
    # Stable, deterministic JSON: sort keys, fixed indent, trailing newline.
    text = json.dumps(dashboard, indent=2, sort_keys=True, ensure_ascii=False)
    path.write_text(text + "\n", encoding="utf-8")


def main() -> int:
    alerts = load_alerts()
    if not alerts:
        sys.exit(
            "ERROR: no alerts with runbook_url found in "
            f"{ALERTS.relative_to(REPO)}; nothing to generate."
        )
    grouped = group_by_runbook(alerts)
    panel_ids = _stable_panel_ids(alerts)

    DASHBOARDS_DIR.mkdir(parents=True, exist_ok=True)

    # Track files we generated — anything else under dashboards/ that
    # follows our naming convention but isn't in this set is a
    # stale leftover (e.g. a runbook was renamed). We don't auto-
    # delete here (operator may have hand-customised), but we
    # do warn at the end.
    generated: set[Path] = set()

    # Per-runbook dashboards.
    for runbook_file in sorted(grouped.keys()):
        slug = _slug(runbook_file)
        out_path = DASHBOARDS_DIR / f"qsdm-runbook-{slug}.json"
        db = build_runbook_dashboard(
            runbook_file, grouped[runbook_file], panel_ids
        )
        _write_dashboard(out_path, db)
        generated.add(out_path)

    # Master overview.
    out_master = DASHBOARDS_DIR / MASTER_FILENAME
    _write_dashboard(out_master, build_master_dashboard(grouped, panel_ids))
    generated.add(out_master)

    print(
        f"Wrote {len(grouped)} per-runbook dashboard(s) + 1 master overview "
        f"({sum(len(v) for v in grouped.values())} total alerts) to "
        f"{DASHBOARDS_DIR.relative_to(REPO)}"
    )

    # Stale-file warning (not fatal — operator may have hand-tuned).
    expected = {p.name for p in generated}
    actual = {
        p.name
        for p in DASHBOARDS_DIR.glob("qsdm-runbook-*.json")
    } | {MASTER_FILENAME}
    stale = sorted(actual - expected)
    if stale:
        print()
        print("WARNING: dashboards-dir contains files not in the alerts file:")
        for s in stale:
            print(f"  - {s}")
        print(
            "  These may be stale leftovers from a renamed runbook. "
            "Review and delete manually if no longer needed."
        )

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
