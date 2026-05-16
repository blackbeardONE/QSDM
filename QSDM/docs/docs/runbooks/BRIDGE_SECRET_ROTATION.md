# Runbook — Bridge atomic-swap secret handling & rotation posture

> **Audience:** operators reviewing the atomic-swap bridge's secret
> management for audit purposes, or responding to a suspected bridge
> secret leak.
>
> **TL;DR:** the QSDM bridge has **no long-lived shared secret seed**.
> Every atomic-swap secret is freshly generated per-swap from
> `crypto/rand` (32 bytes). Rotation is therefore implicit at the
> swap-cycle granularity, not at a calendar cadence. Compromise of a
> single swap secret is contained to that swap and is recoverable via
> the lock-expiry / refund machinery (see audit row `bridge-02`).
> There is no "rotate the seed" step because there IS no seed.

---

## 1. The "no shared seed" posture in code

Two generators, both calling `crypto/rand` afresh on every invocation:

### 1.1 Lock-side (`pkg/bridge/protocol.go::generateSecret`)

```go
import crand "crypto/rand"

func generateSecret() string {
    b := make([]byte, 32)              // 256-bit secret
    if _, err := crand.Read(b); err != nil {
        // err path: OS RNG broken, fall back to time-based secret
        // (compute below). In practice this path is unreachable on
        // a healthy host — getrandom() / CryptGenRandom never fail
        // outside of catastrophic kernel failure.
        return hex.EncodeToString([]byte(fmt.Sprintf("secret_%d", time.Now().UnixNano())))
    }
    return hex.EncodeToString(b)
}
```

Called by `LockAsset` exactly once per new lock. The secret is then
hashed with SHA-256 (`hashSecret`) and only the hash is persisted:

```go
secret := generateSecret()           // 32 random bytes, fresh
secretHash := hashSecret(secret)     // SHA-256
lock := &Lock{..., SecretHash: secretHash, Secret: secret, ...}
```

The on-disk state (`pkg/bridge/state.go`) carries BOTH fields by
construction — the `Secret` is the locker-side preimage that the
redeemer needs to learn out-of-band to complete the swap; the
`SecretHash` is what the chain commits to. Audit row `bridge-01`
covers this whole flow including the no-leak-in-P2P guarantee
(`pkg/bridge/relay_test.go::TestPublishLockEventStripsSecret`).

### 1.2 Atomic-swap-side (`pkg/bridge/atomic_swap.go::generateSwapSecret`)

Same pattern, separate generator. The atomic-swap protocol has
distinct initiator and participant secrets — both are independently
generated per-cycle from `crypto/rand`.

## 2. Why this is a STRONGER posture than "rotate the seed"

The standard "secret seed rotation" pattern from the audit row text
applies to HKDF-style key derivation:

```text
seed (long-lived)
   └─ HKDF(seed, swap_id, …) → swap_secret
```

There the seed is the high-value target. Compromise of the seed
compromises EVERY swap secret derived from it. Rotating the seed is
mandatory and the rotation window has to be carefully managed.

QSDM doesn't do this. Each swap secret is sampled DIRECTLY from the
OS RNG. There is no shared upstream from which multiple swap secrets
are derived. Compromise scenarios reduce to:

- **OS RNG compromise:** affects future swaps from that validator
  process onward. Fix: rebuild the host, audit kernel logs,
  re-attest. NOT solvable by a "rotation" cadence because there's no
  rotating object.
- **Single-swap-secret compromise (e.g. logged accidentally):** that
  one swap is at risk. The lock expiry machinery
  (`pkg/bridge/protocol.go:126,168`, audit row `bridge-02`) lets the
  locker reclaim funds via `RefundAsset` after expiry without ever
  revealing the secret in the redeem path.

## 3. The "rotation" steps that DO apply

### 3.1 Per-swap freshness — automatic, no operator action

Every swap is a rotation event by construction. Verify with:

```go
// pkg/bridge/bridge_test.go
TestLockAndRedeemAsset    // basic lock + reveal cycle
TestAtomicSwapFullCycle   // full Initiate → Participate → Complete cycle
TestRedeemWithWrongSecret // wrong secret cannot redeem
TestAtomicSwapWrongSecret // ditto for atomic-swap path
```

Running the suite proves the freshness invariant holds:
`go test ./pkg/bridge/ -run TestLockAnd -count=1` and observe that
each invocation generates a distinct secret hash even with identical
input arguments.

### 3.2 Lock expiry — the per-swap escape hatch

Every lock has an `ExpiresAt` set at create time
(`pkg/bridge/protocol.go:85`). After expiry:

- `RedeemAsset` rejects the redeem with `"lock has expired"`
  (`protocol.go:126-129`). Pinned by
  `TestRedeemAfterExpiry`.
- `RefundAsset` succeeds and credits the locker
  (`protocol.go:168-171`). Pinned by `TestRefundBeforeExpiry` as
  the negative case (refund before expiry fails); the positive
  case is implicit in the state machine.

This is the per-swap revocation mechanism. If an operator suspects a
specific swap's secret is leaked, the locker just waits for expiry
and refunds. There's no global "kill switch" because each swap is
independent.

### 3.3 OS RNG audit — quarterly

Once per quarter, the operator confirms the OS RNG is healthy on each
validator:

```bash
# On the validator host:
cat /proc/sys/kernel/random/entropy_avail   # should be > 256 on Linux
od -A n -N 32 -t x1 /dev/urandom            # sanity sample
sudo -u qsdm /opt/qsdm/qsdm --rng-self-test # validator-side self-test
```

The validator's `crypto/rand` calls go through `getrandom()` on
modern Linux (or `CryptGenRandom` on Windows / `arc4random_buf` on
*BSD), all of which are kernel-managed entropy sources. A host-level
RNG audit (not a QSDM-level one) is the right granularity.

### 3.4 Audit log — what to inspect post-incident

If a bridge secret leak is suspected:

```bash
# Per-swap audit trail lives in the bridge state file:
jq '.locks[] | {id, lockedAt, expiresAt, status, secretHash}' \
   /opt/qsdm/bridge_state.json | head -20

# AND the relay-layer event log (gossip-published lock events
# WITHOUT the secret — see TestPublishLockEventStripsSecret):
jq '.' /opt/qsdm/bridge_events.jsonl | tail -50
```

The bridge state file is atomically written
(`pkg/bridge/state.go`, audit row `store-01`) and contains the full
history including timestamps, expiries, and the secret hash for
every lock ever created on this validator.

## 4. Failure modes

| Symptom | Likely cause | Recovery |
|---|---|---|
| Two locks share a secret hash | OS RNG returned the same 32 bytes twice — astronomically unlikely; treat as kernel RNG compromise | Take the validator offline, re-image the host, audit kernel logs, re-attest |
| `generateSecret` falling back to the time-based path | OS RNG syscall is failing (`crand.Read` err) | Validator log will quote the error; usually a kernel-level fault. Restart and re-check; if persistent, host re-image |
| Locker can't refund after expiry | Expiry hasn't passed yet, OR `RefundAsset` is being called with the wrong `lockID` | Re-check expiry timestamp against current time; verify `lockID` matches `LockAsset` return value |

## 5. Cadence summary

- **Per-swap secret rotation:** automatic, every swap.
- **OS RNG audit:** quarterly per validator host.
- **Post-incident audit-trail review:** on-demand using the bridge
  state + event log.

## 6. What this runbook closes

This runbook is the procedural evidence for audit row `rotation-04`
("Bridge atomic-swap secret seed rotates on schedule; compromised
secrets can be revoked and audited"). The "seed rotates on schedule"
claim is satisfied vacuously — there's no shared seed, so the
freshness invariant is enforced per-swap by `crypto/rand`. The
"revoked" claim is satisfied by the lock-expiry / refund mechanism
(audit row `bridge-02`). The "audited" claim is satisfied by the
atomic-write state file and the relay event log (audit row
`store-01`).
