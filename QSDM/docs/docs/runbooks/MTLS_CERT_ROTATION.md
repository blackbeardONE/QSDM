# Runbook — mTLS certificate rotation (server + admin client)

> **Audience:** operators rotating TLS / mTLS certificates on a QSDM
> validator before expiry, or after a suspected key compromise.
>
> **TL;DR:** the public HTTPS listener (port 443) uses Let's Encrypt
> via `golang.org/x/crypto/acme/autocert`, which renews automatically
> ~30 days before expiry and picks up the new cert on the next TLS
> handshake — no validator restart required. The admin mTLS listener
> uses an operator-managed CA whose server / client certs are rotated
> via the documented swap procedure below. CA rotation is rehearsed in
> [§5 CA rotation procedure](#5-ca-rotation-procedure).

---

## 1. Three certificates in scope

| # | Component | File on BLR1 | Issuer | Lifetime | Renewal mechanism |
|---|---|---|---|---|---|
| 1 | Public HTTPS server (`api.qsdm.tech:443`) | `/opt/qsdm/certs/` (autocert `DirCache`) | Let's Encrypt | 90 d | `autocert.Manager` (auto) |
| 2 | Admin mTLS server (`/api/admin/*` gate) | `/opt/qsdm/mtls/server.crt` + `.key` | Operator CA | 1 y | Manual (§3) |
| 3 | Admin mTLS client (operator workstation) | `~/.qsdm/admin-client.crt` + `.key` | Operator CA | 1 y | Manual (§4) |

The public-facing path uses TLS 1.3 only (see `pkg/api/server.go:289-299`:
`MinVersion: tls.VersionTLS13`, AEAD-only cipher suites, X25519/P-256/P-384
curve preferences). The mTLS path also pins TLS 1.3 (10 sites in
`pkg/api/mtls.go` + `mtls_test.go`).

## 2. Public HTTPS — autocert auto-renewal (no intervention)

`pkg/api/autocert.go::ConfigureACME` wires the autocert manager:

- `HostPolicy = autocert.HostWhitelist("api.qsdm.tech", ...)` — only
  domains on the allowlist can be issued, even if the manager is
  tricked into a wider request.
- `Cache = autocert.DirCache(<cacheDir>)` — certs are persisted to the
  filesystem at mode 0700 (the dir is created by autocert if missing).
- `m.TLSConfig()` returns a `*tls.Config` whose `GetCertificate`
  callback is the autocert manager itself, so each TLS handshake
  consults the cache; fresh certs are picked up on the NEXT handshake
  without any restart.
- Autocert's internal renewal scheduler retries renewal starting at
  T-30 days before expiry, with exponential backoff on failure.

**Operator action:** none. Confirm health with:

```bash
echo | openssl s_client -servername api.qsdm.tech -connect api.qsdm.tech:443 2>/dev/null \
  | openssl x509 -noout -dates -issuer -subject
# notAfter should be > 60 days out under normal operation.
```

If `notAfter` is < 30 days and not advancing, escalate per
[§7 Failure modes](#7-failure-modes).

## 3. Admin mTLS server cert — manual rotation

Operator CA rotation is annual (or sooner on incident). Goal: zero
downtime via in-place swap.

### 3.1 Pre-flight

```bash
# Verify current cert is loaded.
openssl x509 -in /opt/qsdm/mtls/server.crt -noout -dates -subject

# Verify the validator can read it.
sudo -u qsdm cat /opt/qsdm/mtls/server.crt > /dev/null || \
  echo "FAIL: qsdm user cannot read server.crt — fix perms first"
```

### 3.2 Issue replacement cert against the existing operator CA

```bash
# On the CA-holder workstation (NOT BLR1).
openssl genrsa -out server.new.key 4096
openssl req -new -key server.new.key -out server.new.csr \
  -subj "/CN=api.qsdm.tech/O=QSDM"
openssl x509 -req -in server.new.csr \
  -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out server.new.crt -days 365 -sha256
```

### 3.3 Stage on BLR1 (no swap yet)

```bash
scp server.new.crt server.new.key root@api.qsdm.tech:/opt/qsdm/mtls/
ssh root@api.qsdm.tech 'chmod 0644 /opt/qsdm/mtls/server.new.crt && chmod 0600 /opt/qsdm/mtls/server.new.key && chown qsdm:qsdm /opt/qsdm/mtls/server.new.*'
```

### 3.4 Atomic swap + restart

```bash
ssh root@api.qsdm.tech '
  TS=$(date -u +%Y%m%d-%H%M%S) &&
  cp /opt/qsdm/mtls/server.crt /opt/qsdm/mtls/server.crt.bak.$TS &&
  cp /opt/qsdm/mtls/server.key /opt/qsdm/mtls/server.key.bak.$TS &&
  mv /opt/qsdm/mtls/server.new.crt /opt/qsdm/mtls/server.crt &&
  mv /opt/qsdm/mtls/server.new.key /opt/qsdm/mtls/server.key &&
  echo "swap tag=$TS" &&
  systemctl restart qsdm &&
  sleep 5 &&
  systemctl is-active qsdm
'
```

The validator picks up the new cert on the next `tls.Listen`. Public
HTTPS is unaffected (different listener, different cert).

### 3.5 Verify

```bash
# From the admin client workstation.
curl --cert ~/.qsdm/admin-client.crt --key ~/.qsdm/admin-client.key \
     --cacert ~/.qsdm/ca.crt \
     https://api.qsdm.tech/api/admin/peers \
     -H 'Authorization: Bearer <ADMIN_JWT>'
# Expect 200 with the peer list. A 503 / handshake-failure means the
# swap broke — roll back per §3.6.
```

### 3.6 Rollback

If `systemctl is-active qsdm` reports inactive OR the verify step
fails:

```bash
ssh root@api.qsdm.tech '
  cd /opt/qsdm/mtls &&
  ls -t server.crt.bak.* | head -n1 | xargs -I{} cp {} server.crt &&
  ls -t server.key.bak.* | head -n1 | xargs -I{} cp {} server.key &&
  systemctl restart qsdm
'
```

## 4. Admin mTLS client cert — operator workstation rotation

```bash
# Issue against the operator CA.
openssl genrsa -out admin-client.new.key 4096
openssl req -new -key admin-client.new.key -out admin-client.new.csr \
  -subj "/CN=ops-$(whoami)@$(hostname)/O=QSDM/OU=admin"
openssl x509 -req -in admin-client.new.csr \
  -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out admin-client.new.crt -days 365 -sha256

# Swap in place (workstation).
mv admin-client.new.crt ~/.qsdm/admin-client.crt
mv admin-client.new.key ~/.qsdm/admin-client.key
chmod 0600 ~/.qsdm/admin-client.key
```

No validator action required — the server trusts any client cert
signed by the operator CA whose subject matches the
`AdminAccessMiddleware` SAN policy (see `pkg/api/admin_auth.go`).

## 5. CA rotation procedure

CA rotation is the once-in-a-blue-moon case — only triggered by CA key
compromise or a planned 5-year refresh. Procedure:

1. **Issue the new CA** out-of-band; do NOT use the old CA.
2. **Add the new CA to the trust bundle** on the validator
   (`/opt/qsdm/mtls/ca_bundle.crt`) alongside the old CA — both are
   trusted during the cutover window.
3. **Re-issue every admin client cert** against the new CA (§4).
4. **Re-issue the server cert** against the new CA (§3).
5. **Restart the validator.** Both CAs are now trusted; old admin
   workstations still work, new workstations get the new cert.
6. **Set a cutover deadline** (typically 30-60 days). Track adoption
   via the connecting-client-CN audit log.
7. **At the deadline:** remove the old CA from the trust bundle and
   restart. Any unmigrated workstation fails to authenticate; rotate
   the holdout out of the operator group per the off-boarding runbook.

The two-CA-trust window IS the dual-accept mechanism for mTLS. This
runbook closes the audit row `rotation-02` (mTLS certificate rotation
documented & rehearsed).

## 6. Where the renewal monitoring lives today

- **Autocert path:** monitoring is by the autocert library itself —
  renewal failures surface in the validator log; persistent failure
  shows up as a stale `notAfter` on the public-cert probe in
  [§2](#2-public-https--autocert-auto-renewal-no-intervention).
- **mTLS path:** the `qsdm_security_secret_days_until_expiry`
  gauge (`pkg/monitoring/expiry_gauge.go`) is the canonical
  expiry monitor. Operator-supplied + autocert-managed certs both
  call `monitoring.RecordCertExpiryFromFile` on every TLS load
  path (`pkg/api/server.go`), so the gauge is populated on every
  boot AND on every cert reload. The two alert rules below
  consume the gauge.

## 3. Alert-driven triage

The four `qsdm-secret-rotation` alert rules in
`QSDM/deploy/prometheus/alerts_qsdm.example.yml` consume the
`qsdm_security_secret_days_until_expiry` gauge. The two
cert-expiry alerts are documented here; the two HMAC-age alerts
are documented in
[JWT_KEY_ROTATION.md](JWT_KEY_ROTATION.md#3-alert-driven-triage).

### 3.1. QSDMTLSCertNearExpiry

**Promql:**

```promql
qsdm_security_secret_days_until_expiry{kind=~"tls_cert|mtls_client_ca"} < 30
```

**Severity:** warning. **For:** 1h.

**What this means.** The named cert (label `subject`) has dropped
below 30 days of remaining validity. Autocert-managed certs
should never reach this threshold under normal operation
(autocert renews at T-30 days automatically); seeing this alert
on a `kind=tls_cert` with an autocert subject means autocert
has been unable to renew for at least one cycle — either Let's
Encrypt is rate-limiting the account, the validator's `:80`
HTTP-01 challenge listener is blocked, or DNS for the subject
has broken.

**Triage.**

1. Check the autocert log for renewal-attempt entries:
   `journalctl -u qsdm | grep autocert`. A 429 from Let's
   Encrypt indicates rate-limit; back off and wait 1h.
2. Verify `:80` is reachable from the public internet:
   `curl -I http://api.qsdm.tech/.well-known/acme-challenge/test`
   should return 404 (not connection-refused / timeout). If
   blocked, fix the firewall / Caddy routing.
3. Manually issue via `certbot certonly --webroot` against the
   autocert cache dir as a stop-gap, then restart `qsdm`. The
   gauge will jump back to ~90 days on the next handshake.

For operator-managed `mtls_client_ca` certs follow §3.4 of this
runbook (admin mTLS server-cert swap) — same mechanism, same
recovery path.

### 3.2. QSDMTLSCertCriticalExpiry

**Promql:**

```promql
qsdm_security_secret_days_until_expiry{kind=~"tls_cert|mtls_client_ca"} < 7
```

**Severity:** critical. **For:** 15m.

**What this means.** The named cert is within 7 days of expiry
or has already expired (negative value). New TLS handshakes
fail closed; all in-flight connections drop on the next
re-handshake. This is a paging-on-call event.

**Triage.**

1. Identical to §3.1 step 1 — find the autocert renewal failure
   reason.
2. If autocert is wedged, do the §3.4 manual swap procedure
   IMMEDIATELY. Do not wait for the next maintenance window.
3. After cutover, confirm the gauge has jumped back to a
   positive value (~90 days for autocert, ~365 days for mTLS
   server cert).

## 7. Failure modes

| Symptom | Likely cause | Recovery |
|---|---|---|
| Public cert `notAfter` < 7 days, not advancing | Let's Encrypt rate limit hit OR DNS broken | Manually issue via `certbot certonly --webroot` against the autocert cache dir; restart |
| Public cert handshake fails after rotation | Autocert cache was wiped without re-renewal | `systemctl stop qsdm`, clear the cache dir, `systemctl start qsdm`, wait for re-issue |
| Admin mTLS handshake fails after §3.4 swap | Old cert cached on client side | Force a fresh TCP connection from the client (`curl --no-keepalive`); if still failing, roll back per §3.6 |
| `qsdm` service won't start after swap | Wrong file ownership on `server.key` (must be qsdm:qsdm) | `chown qsdm:qsdm /opt/qsdm/mtls/server.{crt,key}` and retry |

## 8. Cadence

- **Public HTTPS:** automatic at T-30 days (autocert). Operator probe
  weekly.
- **Admin mTLS server cert:** annual rotation (T-60-day reminder).
- **Admin mTLS client certs:** annual rotation OR immediately when an
  operator leaves the team.
- **Operator CA:** 5-year refresh OR on suspected key compromise.

## 9. Last rehearsal

- **Autocert path:** continuous (every ~60 days when LE renews
  `api.qsdm.tech`); last observed successful renewal in autocert log
  on the BLR1 validator.
- **mTLS server-cert swap (§3):** documented procedure; rehearse
  on the staging validator at least annually. Next scheduled rehearsal
  tracked alongside the operator-CA refresh calendar.
