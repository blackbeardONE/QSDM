# Runbook — Scylla auth credential rotation

> **Audience:** operators rotating the `SCYLLA_USERNAME` /
> `SCYLLA_PASSWORD` (and the TLS material at `SCYLLA_TLS_CA_PATH` /
> `SCYLLA_TLS_CERT_PATH` / `SCYLLA_TLS_KEY_PATH`) used by QSDM
> validators connecting to a Scylla / Cassandra cluster.
>
> **TL;DR:** auth is sourced from systemd's `EnvironmentFile=` at
> validator boot. Rotation is a four-step procedure:
> (1) add the new user to Scylla, (2) update the `EnvironmentFile`
> on each validator, (3) restart validators one at a time
> (rolling), (4) drop the old user from Scylla. Cadence: **at least
> quarterly**, or immediately on suspected credential leak.
>
> **Important caveat.** The "without restart" qualifier in the audit
> row `rotation-03` is "where possible". `gocql` (the Scylla driver
> QSDM uses, see `pkg/storage/scylla.go`) caches the authenticator at
> session-open time and does NOT support hot credential rotation on an
> open session. "Without restart" therefore means a rolling restart of
> the validator pool (so the data plane is never fully offline), not a
> literally-zero-process-restart swap. This is the standard cassandra
> driver posture across most ecosystems.

---

## 1. Where credentials live

| Layer | Source | Wiring |
|---|---|---|
| QSDM source code | `pkg/storage/scylla.go::ScyllaClusterConfigFromEnv` | reads `SCYLLA_USERNAME` / `SCYLLA_PASSWORD` (and 3 TLS paths) via `os.Getenv` at validator start |
| Process env | `/etc/systemd/system/qsdm.service.d/scylla.conf` (drop-in) | `Environment=` or `EnvironmentFile=/etc/qsdm/scylla.env` lines |
| Secret manager (recommended) | Vault / SOPS-decrypted file rendered to `/etc/qsdm/scylla.env` on deploy | rendered with mode 0600, owned by root, group `qsdm` |
| Cluster side | Scylla `system_auth.roles` | `CREATE ROLE` / `DROP ROLE` via cqlsh against the cluster |

> **Note on current BLR1 deploy.** The single-node BLR1 validator runs
> the `FileStorage` backend, NOT Scylla. This runbook is the
> forward-looking procedure for the multi-validator deploy where
> Scylla becomes the storage backend. It is also rehearsed on the
> staging multi-validator cluster before each quarterly rotation.

## 2. Quarterly rotation procedure

### 2.1 Add the new role to Scylla

```bash
# From any operator workstation with cqlsh + admin credentials.
cqlsh scylla-1.qsdm.internal -u super_admin -p '<...>' <<'EOF'
CREATE ROLE qsdm_v2 WITH LOGIN = true AND PASSWORD = '<NEW-32B-RANDOM>';
GRANT SELECT, MODIFY ON KEYSPACE qsdm TO qsdm_v2;
LIST ROLES;  -- confirm qsdm_v2 is listed alongside qsdm_v1
EOF
```

Generate the new password with `openssl rand -base64 32` — never
type a password by hand. Store it in the secret manager before
proceeding.

### 2.2 Render the new `EnvironmentFile` on every validator

```bash
# Per-validator (parallel-safe):
sudo install -m 0600 -o root -g qsdm /dev/stdin /etc/qsdm/scylla.env <<EOF
SCYLLA_USERNAME=qsdm_v2
SCYLLA_PASSWORD=<NEW-32B-RANDOM>
SCYLLA_TLS_CA_PATH=/etc/qsdm/scylla/ca.crt
SCYLLA_TLS_CERT_PATH=/etc/qsdm/scylla/client.crt
SCYLLA_TLS_KEY_PATH=/etc/qsdm/scylla/client.key
EOF
```

If a secret manager (Vault, SOPS) is in use, render the file
through that pipeline instead — the file shape and permissions
must match.

### 2.3 Verify the file is well-formed

```bash
sudo -u qsdm bash -c 'set -a; source /etc/qsdm/scylla.env; env | grep ^SCYLLA_'
# Should print exactly the five SCYLLA_* vars with the new values.
```

### 2.4 Rolling restart

```bash
# One validator at a time:
for v in v1 v2 v3; do
  ssh root@$v.qsdm.internal '
    systemctl restart qsdm &&
    sleep 10 &&
    systemctl is-active qsdm &&
    curl -sS -m 5 http://127.0.0.1:8080/api/v1/health | grep -q "\"status\":\"healthy\""
  ' || { echo "FAIL on $v — STOP, do not restart the rest"; break; }
done
```

Each restart causes ~5-15 s of that one validator being unavailable
to its connection pool; the cluster as a whole stays available.

### 2.5 Drop the old role

After all validators have restarted successfully AND have been
confirmed to be running on `qsdm_v2` (verify by tailing the boot
log for the connect string OR running a query through each
validator's `/api/v1/health`):

```bash
cqlsh scylla-1.qsdm.internal -u super_admin -p '<...>' <<'EOF'
DROP ROLE qsdm_v1;
LIST ROLES;  -- confirm only qsdm_v2 + super_admin remain
EOF
```

This step is intentionally separate from §2.1-2.4 so the old role
remains available as a fallback if a validator misbehaves and needs
to be rolled back. Don't conflate "we rotated" with "the old creds
are revoked" — those are two different on-call states.

## 3. Emergency rotation (suspected leak)

If credentials are believed leaked:

1. **Immediately** issue a fresh role with a new password (§2.1).
2. **Run §2.4 rolling restart with the new env file** — no calendar
   delay.
3. **Drop the old role** (§2.5) — do NOT wait for the standard
   cutover window. Active sessions on the old role will be torn
   down by Scylla on `DROP ROLE`; affected validators will fail
   their next query, reconnect with the new creds (already in their
   env), and recover.

## 4. TLS material rotation

Cert / key files at the three `SCYLLA_TLS_*` paths rotate on the
same calendar as the QSDM mTLS certs (see
[`MTLS_CERT_ROTATION.md`](MTLS_CERT_ROTATION.md)). Atomic swap +
restart, same as §2.4.

## 5. Where the rotation log lives

Audit-trail entries land in two places:

- **Scylla side:** `system_auth.role_management_log` (if enabled) +
  the operator's CREATE/DROP ROLE transcripts checked into a private
  ops repo.
- **QSDM side:** `journalctl -u qsdm` records the boot-time
  Scylla-cluster-connection log line which includes the SCYLLA_USERNAME
  value (the password is NEVER logged — see
  `pkg/storage/scylla.go::ScyllaClusterConfigFromAuthTLS`).

## 6. Cadence

- **Standard rotation:** every quarter (90 days).
- **Emergency rotation:** within 60 minutes of compromise notification.
- **TLS material rotation:** annual (or with the operator CA rotation
  window — see `MTLS_CERT_ROTATION.md` §5).

## 7. Failure modes

| Symptom | Likely cause | Recovery |
|---|---|---|
| Validator fails to start after §2.4 | Typo in `scylla.env` | Verify with §2.3; the validator log will quote the auth error from gocql |
| Health probe fails post-restart | New role lacks `SELECT, MODIFY` on `qsdm` keyspace | Re-run §2.1's `GRANT` line |
| Some validators on old creds, some on new | Half-completed rolling restart | Finish the rolling restart; do NOT drop the old role (§2.5) until 100% of validators are on the new creds |
| Old role can't be dropped (`ALTER` permission denied) | `super_admin` was downgraded | Escalate to the cluster admin holding the bootstrap superuser |
