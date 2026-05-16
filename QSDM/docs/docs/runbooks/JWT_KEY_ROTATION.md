# JWT / API-key HMAC rotation runbook

**Audit row:** `rotation-01` (severity: high, category: secret_rotation).

This runbook covers zero-downtime rotation of the HMAC secret that
backs both JWT tokens and per-request `X-Signature` verification on
QSDM validators that are not linked against a Dilithium backend, or
that have explicit Dilithium-fallback wiring exercised.

The mechanism is **dual-accept with a verify-only secondary key**. In
steady state the validator holds one secret (`primary`). During a
rotation window it holds two (`primary` = new, `secondary` = old);
both are accepted on verify, only the primary is used to sign new
tokens and request signatures. When the longest in-flight token /
signature expires, the operator clears the secondary and the
rotation is complete.

Implementation:

| Surface              | Source                                                                            |
| -------------------- | --------------------------------------------------------------------------------- |
| JWT verifier         | [`pkg/api/auth.go::AuthManager.ValidateToken`](../../../source/pkg/api/auth.go)   |
| Request signature    | [`pkg/api/security.go::RequestSigner.VerifyRequest`](../../../source/pkg/api/security.go) |
| Primary key env var  | `QSDM_JWT_HMAC_SECRET`                                                            |
| Secondary key env var | `QSDM_JWT_HMAC_SECRET_SECONDARY`                                                 |
| JWT secondary metric | `qsdm_security_jwt_secondary_key_hits_total`                                      |
| Request-sig secondary metric | `qsdm_security_request_signature_secondary_key_hits_total`                |
| Tests (contract)     | [`pkg/api/rotation_dual_accept_test.go`](../../../source/pkg/api/rotation_dual_accept_test.go) |

## Threat model

What this rotation is for:

- **Routine periodic rotation** — bringing the key age below 90 days
  on a calendar cadence, with zero impact on logged-in users or
  in-flight signed requests.
- **Reactive rotation** — operator has reason to believe the
  primary key may have leaked (e.g. an environment-file commit slipped
  past pre-commit hooks, a backup tape went missing, a kernel-keyring
  dump was observed). The dual-accept window keeps active sessions
  alive while every client is migrated.

What this rotation is NOT for:

- **Active compromise**. If the operator has direct evidence that the
  primary key is being used to mint forged tokens RIGHT NOW (e.g. a
  spike in `qsdm_security_auth_invalid_token_total` correlated with
  the leak), do an **emergency cutover** (skip the window, go
  primary-only on the new key in one step). Live sessions break;
  attacker is locked out immediately. Accept the support load.

## Procedure (zero-downtime)

### T0 — steady state

```
QSDM_JWT_HMAC_SECRET=<keyA>
QSDM_JWT_HMAC_SECRET_SECONDARY=    (unset)
```

`qsdm_security_jwt_secondary_key_hits_total` is `0` and
`qsdm_security_request_signature_secondary_key_hits_total` is `0`.

### T1 — generate `keyB` and start the window

```bash
# Strong key: 48 bytes from /dev/urandom, base64-url so it survives systemd
# env-file quoting. Length-mismatch is a common foot-gun.
NEW_KEY=$(head -c 48 /dev/urandom | base64 -w0 | tr '+/' '-_' | tr -d '=')
echo "$NEW_KEY"
```

Edit `/etc/systemd/system/qsdm.service.d/rotation.conf` (create if
absent):

```ini
[Service]
# rotation-01 window started YYYY-MM-DD by <operator>.
# Primary signs new tokens; secondary still verifies old tokens until
# every issued one has expired (see TTL ceiling below). Close the
# window by removing this file + daemon-reload + restart.
Environment=QSDM_JWT_HMAC_SECRET=<keyB>
Environment=QSDM_JWT_HMAC_SECRET_SECONDARY=<keyA>
```

```bash
systemctl daemon-reload
systemctl restart qsdm
# Confirm both keys are wired (look for the WARN line from cmd/qsdm/main.go)
journalctl -u qsdm -n 20 --no-pager | grep -i 'rotation-01'
```

You should see:

```
WARN  rotation-01: JWT/API-key VERIFY-ONLY secondary key is active;
       cutover gate is qsdm_security_jwt_secondary_key_hits_total
       going flat for >= max-token-TTL
```

### T1 onwards — monitor the dual-accept window

```bash
# Quick check from the public scrape endpoint.
curl -s https://api.qsdm.tech/api/metrics/prometheus \
    | grep -E 'qsdm_security_(jwt|request_signature)_secondary_key_hits_total'
```

Expected pattern over the window:

- Both counters rise immediately as existing sessions / signed
  requests verify against the secondary.
- Rate decays as clients refresh tokens / re-sign requests under
  the new primary (every fresh `/auth/login` and every new client
  RequestSigner picks up keyB).
- Once **every** access token signed before T1 has expired
  (`expires_in` default = 24h) AND every refresh token has either
  expired (7d default) or been refreshed under keyB, the JWT
  counter goes flat.
- The request-signature counter goes flat as soon as every active
  signed-request client has re-issued under keyB. Typically much
  faster than the JWT side because request-signatures are per-call,
  not session-scoped.

### T2 — cutover

Wait for **both** counters to be flat for at least 1 hour above the
maximum refresh-token TTL (default 7 days + 1h safety = ~169h).

Remove the drop-in:

```bash
rm /etc/systemd/system/qsdm.service.d/rotation.conf
systemctl daemon-reload
systemctl restart qsdm
journalctl -u qsdm -n 6 --no-pager
```

The `rotation-01` warn line should NOT appear. The two secondary-hit
counters remain at the final value seen during the window (they are
monotonic) but stop incrementing. Confirm:

```bash
# Try a known old-key token; it should now return 401.
curl -sS https://api.qsdm.tech/api/v1/protected \
    -H "Authorization: Bearer <old-key-token>" \
    -w '%{http_code}\n' -o /dev/null
# 401  (post-cutover: old keyA tokens are rejected)
```

## Foot-gun guard: same-key secondary

If the operator accidentally sets

```
QSDM_JWT_HMAC_SECRET=<keyA>
QSDM_JWT_HMAC_SECRET_SECONDARY=<keyA>
```

the validator detects same-as-primary and **clears the secondary**
(see `SetJWTHMACFallbackSecondarySecret` and
`SetSecondaryHMACSecret`). This is intentional: a same-key window
would mean the secondary-hit counter never increments, defeating
the gating check above. If the operator wanted to keep the existing
key as both primary and "secondary" they would also have no real
rotation in flight, so the clear is the safe behaviour.

## 3. Alert-driven triage

The two HMAC-age `qsdm-secret-rotation` alert rules in
`QSDM/deploy/prometheus/alerts_qsdm.example.yml` consume the
`qsdm_security_secret_days_until_expiry` gauge for the
`kind=jwt_primary|jwt_secondary|request_sig_primary|request_sig_secondary`
series. The two cert-expiry alerts in the same group are
documented in
[MTLS_CERT_ROTATION.md](MTLS_CERT_ROTATION.md#3-alert-driven-triage).

### 3.1. QSDMJWTPrimaryKeyAgedOut

**Promql:**

```promql
qsdm_security_secret_days_until_expiry{kind=~"jwt_primary|request_sig_primary"} <= -90
```

**Severity:** warning. **For:** 6h.

**What this means.** The primary HMAC key for the named surface
has been in place for at least 90 days. QSDM's rotation policy
is 90 days; the alert fires until a new primary is installed.
This is the normal cadence-driven rotation trigger — not an
incident.

**Triage / action.**

1. Mint a new primary key (32 bytes of `crypto/rand`):
   `openssl rand -hex 32`.
2. Follow the dual-accept procedure documented in
   [§T1 — Generate keyB and start the window](#t1--generate-keyb-and-start-the-window)
   above: install the new value as the primary
   (`QSDM_JWT_HMAC_SECRET`) and the old value as the secondary
   (`QSDM_JWT_HMAC_SECRET_SECONDARY`).
3. `SetJWTHMACFallbackSecret` calls `RecordSecretSetTime`, which
   resets the gauge to ~0 days. The alert auto-resolves on the
   next scrape (typically within 30s).
4. Run the rotation window per the existing procedure; clear
   the secondary at the documented cutover.

### 3.2. QSDMJWTSecondaryKeyWindowLeftOpen

**Promql:**

```promql
qsdm_security_secret_days_until_expiry{kind=~"jwt_secondary|request_sig_secondary"} <= -7
```

**Severity:** warning. **For:** 1h.

**What this means.** A rotation window (i.e.
`QSDM_JWT_HMAC_SECRET_SECONDARY` set non-empty) has been open
for more than 7 days. The rotation procedure prescribes a
window of 24h-7d depending on the longest in-flight token TTL
(24h for access-tokens-only fleets, 7d if refresh tokens are in
play). After 7 days the operator has almost certainly forgotten
to clear the secondary.

**Triage / action.**

1. Verify the secondary-hit counter
   (`qsdm_security_jwt_secondary_key_hits_total` /
   `qsdm_security_request_signature_secondary_key_hits_total`)
   has been flat for at least `max-token-TTL`. If not, the
   window is legitimately open longer than 7 days — extend the
   alert silence and continue monitoring.
2. If the counter IS flat, clear the secondary with the
   documented cutover procedure (§T2). Setting the secondary
   to empty calls `ClearSecretExpiry` under the hood, which
   removes the gauge series and auto-resolves the alert.

## Emergency cutover (compromise scenario)

Skip the dual-accept window. One step:

```bash
# Replace QSDM_JWT_HMAC_SECRET with the new key directly, leave
# QSDM_JWT_HMAC_SECRET_SECONDARY unset.
systemctl edit qsdm.service   # set Environment=QSDM_JWT_HMAC_SECRET=<keyB>
systemctl restart qsdm
```

Every live session is invalidated. Document the incident in
`docs/incidents/`. Notify users on the dashboard banner.

## Verification

After every rotation, the operator's checklist:

- [ ] Both secondary-hit counters incremented above zero during the
      window (proves dual-accept code path was exercised, not
      bypassed by a misconfiguration).
- [ ] Both counters went flat before cutover.
- [ ] Post-cutover smoke test (old-key token returns 401, new-key
      token returns 200).
- [ ] `journalctl -u qsdm | grep -i rotation-01` post-cutover shows
      no WARN line.
- [ ] `/etc/systemd/system/qsdm.service.d/rotation.conf` removed.

## Related runbooks

- [`MTLS_CERT_ROTATION.md`](MTLS_CERT_ROTATION.md) — mTLS certificate
  rotation (different key material, different rotation semantics
  because TLS does its own version negotiation).
- [`SCYLLA_AUTH_ROTATION.md`](SCYLLA_AUTH_ROTATION.md) — Scylla
  database credential rotation (requires a rolling restart because
  `gocql` does not hot-reload).
- [`BRIDGE_SECRET_ROTATION.md`](BRIDGE_SECRET_ROTATION.md) — bridge
  atomic-swap secret handling (no rotation needed: every secret is
  per-swap, sourced from `crypto/rand`).
