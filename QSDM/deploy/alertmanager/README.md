# Alertmanager (QSDM)

Routes Prometheus alerts (defined in `../prometheus/alerts_qsdm.example.yml`) to
PagerDuty / Slack / email, and renders **both** `runbook_url` and
`dashboard_url` annotations into every notification surface so the on-call
operator gets a one-click path to the markdown runbook **and** the live
Grafana panel.

## Files

| File | Purpose |
|------|---------|
| `alertmanager.example.yml` | Reference Alertmanager config (routing tree, receivers, inhibit rules). |
| `templates/qsdm.tmpl` | Notification templates referenced by the receivers (Slack, PagerDuty, email). |

## Routing tree (at a glance)

```
incoming alert
      │
      ├─ severity=critical  ──► pagerduty-critical (continue: true)
      │                    ╰──► slack-critical
      ├─ severity=warning   ──► slack-warning
      ├─ severity=info      ──► slack-info-quiet  (long repeat, no page)
      └─ unlabelled         ──► slack-default     (fallback, alerts-file bug)
```

`continue: true` on the first critical-matching route is what produces the
**fan-out** (one alert lands in *both* PagerDuty and Slack). Removing it
collapses critical alerts to PagerDuty only.

`severity` and `subsystem` labels come from
`../prometheus/alerts_qsdm.example.yml`. The label vocabulary is:

| Label | Possible values |
|-------|------------------|
| `severity` | `critical`, `warning`, `info` |
| `subsystem` | `v2-mining`, `v2-attest`, `v2-governance` (or unset on the older alert groups) |

## Inhibit rules

Suppress redundant lower-priority alerts while a higher-priority alert is
firing for the same scope (no double-paging the on-call):

| Suppresses | While this is firing |
|------------|----------------------|
| All `severity=warning` for an instance | Any `severity=critical` for the same instance + alertname class |
| `QSDMMiningSlashApplied` (warning) | `QSDMMiningSlashedDustBurst` (critical) |
| `QSDMQuarantineAnySubmesh` (warning) | `QSDMQuarantineMajorityIsolated` (critical) |
| `QSDMTrustAttestationsBelowFloor`, `QSDMTrustNGCServiceDegraded` | `QSDMTrustNoAttestationsAccepted` |

Adjust or extend in the `inhibit_rules:` section of `alertmanager.example.yml`.

## Templates surface BOTH URLs

Every receiver in `alertmanager.example.yml` calls four shared templates
defined in `templates/qsdm.tmpl`:

| Template | Used in | Effect |
|----------|---------|--------|
| `qsdm.title` | Slack title, PagerDuty `description`, email subject | One-line summary with severity emoji + alertname + status |
| `qsdm.text` | Slack attachment text, email body | Multi-line body with `*Runbook*:` and `*Dashboard*:` Slack-mrkdwn hyperlinks |
| `qsdm.slack.titlelink` | Slack `title_link` | Click-through on the message title — defaults to `dashboard_url`, falls back to `runbook_url`, then `#` |
| `qsdm.slack.color` | Slack attachment color | `danger` (critical), `warning` (warning), grey (info), `good` (resolved) |

Slack receivers additionally configure two `actions:` buttons that link
to `runbook_url` and `dashboard_url` directly (so on-call doesn't have
to scroll past the description). PagerDuty receivers populate
`client_url`, `links[]`, and `details:` with both URLs.

## Wiring up

### 1. Install Alertmanager

[Download the Alertmanager release](https://github.com/prometheus/alertmanager/releases)
matching the version pinned in `../../../.github/workflows/validate-deploy.yml`
(`AMTOOL_VERSION`). For Linux:

```bash
wget https://github.com/prometheus/alertmanager/releases/download/v0.27.0/alertmanager-0.27.0.linux-amd64.tar.gz
tar xf alertmanager-0.27.0.linux-amd64.tar.gz
sudo mv alertmanager-0.27.0.linux-amd64/alertmanager /usr/local/bin/
sudo mv alertmanager-0.27.0.linux-amd64/amtool /usr/local/bin/
```

### 2. Copy + edit the example config

```bash
sudo mkdir -p /etc/alertmanager/templates
sudo cp QSDM/deploy/alertmanager/alertmanager.example.yml /etc/alertmanager/alertmanager.yml
sudo cp QSDM/deploy/alertmanager/templates/qsdm.tmpl /etc/alertmanager/templates/qsdm.tmpl
```

Then edit `/etc/alertmanager/alertmanager.yml` and replace the four
`REPLACE_ME` tokens:

- `REPLACE_ME_PAGERDUTY_INTEGRATION_KEY` — from PagerDuty's "Events API V2" integration tab
- `https://hooks.slack.com/services/REPLACE/ME/SLACK_*_WEBHOOK` — one webhook URL per channel from Slack's "Incoming Webhooks" app
- `REPLACE_ME_SMTP_USERNAME` / `REPLACE_ME_SMTP_PASSWORD` — your SMTP relay credentials (only needed if you wire `email-fallback` into the routing tree)

### 3. Validate the config

```bash
amtool check-config /etc/alertmanager/alertmanager.yml
```

Expected output:

```
Checking '/etc/alertmanager/alertmanager.yml'  SUCCESS
Found:
 - global config
 - route
 - 4 inhibit rules
 - 6 receivers
 - 1 templates
  SUCCESS
```

### 4. Verify the routing tree

```bash
amtool config routes test --config.file=/etc/alertmanager/alertmanager.yml \
  severity=critical alertname=QSDMQuarantineMajorityIsolated
# expected: pagerduty-critical,slack-critical

amtool config routes test --config.file=/etc/alertmanager/alertmanager.yml \
  severity=warning alertname=QSDMNvidiaLockHTTPBlocksSpike
# expected: slack-warning
```

### 5. Start Alertmanager

```bash
alertmanager \
  --config.file=/etc/alertmanager/alertmanager.yml \
  --storage.path=/var/lib/alertmanager \
  --web.listen-address=:9093
```

### 6. Point Prometheus at it

In `../prometheus/prometheus.qsdm.example.yml`:

```yaml
alerting:
  alertmanagers:
    - static_configs:
        - targets:
            - alertmanager:9093
```

Replace `alertmanager:9093` with the actual host:port of your Alertmanager
instance.

## End-to-end smoke test (without sending real notifications)

`scripts/smoketest_alertmanager.py` (when run with `alertmanager` and
`amtool` on PATH) spins up a temporary Alertmanager pointed at a local
listener, pushes synthetic alerts through it, and asserts that:

1. The routing tree dispatches each severity to the expected receiver(s).
2. Critical alerts fan out to **both** `pagerduty-critical` and `slack-critical`.
3. The rendered Slack JSON body for every receiver contains:
   - The literal `*Runbook*:` and `*Dashboard*:` Slack-mrkdwn lines.
   - Both URLs verbatim (substring match).
   - Two action buttons whose `url:` resolves to the alert's annotations.

Run it from the repo root:

```bash
python scripts/smoketest_alertmanager.py
```

Exit 0 on success, exit 1 with per-check `[FAIL]` lines on regression.

## CI

`../../../.github/workflows/validate-deploy.yml` runs `amtool check-config`
against this directory on every push that touches `QSDM/deploy/alertmanager/**`.
The Alertmanager binary version is pinned via the `AMTOOL_VERSION`
workflow variable; `scripts/git_hook_pre_commit.py` reads the same
variable and warns when the locally installed `amtool` doesn't match.

## v1-only deployments

If you've stripped the `qsdm-v2-*` rule groups from
`../prometheus/alerts_qsdm.example.yml` (because you're running a v1 node),
you can also strip the `severity: info` route (and corresponding
`slack-info-quiet` receiver) — the only `info` alerts are v2-governance
events. Critical/warning routes still apply to the v1 alerts.
