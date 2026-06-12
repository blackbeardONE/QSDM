# Runbook — Migrating mining rewards off a friendly-name address

> **Audience:** operators whose miner CLI / service was bootstrapped against a
> non-hex "friendly name" reward address (e.g. `qsdm1miner-rtx3050`).
> Symptom: `qsdm.tech/wallet.html` keeps showing **0 CELL** even after days
> of mining, but `GET /api/v1/mining/account?address=<friendly-name>` shows
> the real accumulated balance.
>
> **TL;DR:** friendly-name addresses can RECEIVE rewards but cannot
> cryptographically SIGN a `submit-signed` transaction (no ML-DSA-87
> keypair backs them). To recover, generate a real keypair-backed wallet
> and reconfigure the miner to credit it going forward. Historical funds
> at the friendly-name stay locked until a future on-chain migration tool
> lands.

---

## 1. Diagnose

Run both probes against the live validator:

```bash
ADDR=qsdm1miner-rtx3050   # or whatever your miner config has

curl -sS "https://api.qsdm.tech/api/v1/wallet/balance?address=$ADDR" | jq
# { "address": "qsdm1miner-rtx3050",
#   "balance": 45315.83207005032,
#   "source":  "mining-ledger" }     ← non-zero with source=mining-ledger
#                                       confirms you're on the friendly-name
#                                       hot-path.

curl -sS "https://api.qsdm.tech/api/v1/mining/account?address=$ADDR" | jq
# { "address": "qsdm1miner-rtx3050",
#   "balance": 45315.83207005032,
#   "nonce": 4,
#   "present": true }
```

If your reward address starts with `qsdm1`, `rig-`, or any non-hex
prefix, **you are on a friendly name and cannot sign transactions from
it**. Real keypair-backed addresses are exactly **64 lowercase hex
characters** (`hex(sha256(public_key))`).

## 2. Generate a real wallet

Two interchangeable paths — both produce the same `pkg/keystore` v1
format (PBKDF2-SHA-256 → AES-256-GCM).

### Path A: browser (qsdm.tech/wallet.html)

1. Open <https://qsdm.tech/wallet.html>.
2. In the new **"Your QSDM Wallet"** panel at the top, click
   **Create a new wallet**.
3. Pick a strong passphrase (12+ chars) and confirm.
4. Open the **Settings** tab and click **Download wallet.json** —
   stash that file somewhere safe (you'll need it to restore the wallet
   to another browser).
5. Copy the address shown next to your balance. It will look like:
   `605ab7550bd6c74ce3e5b394c1f6334cea0f6e2951938ae7fc5c775a7e1ac7e2`.

### Path B: CLI (`qsdmcli wallet new`)

```bash
./qsdmcli wallet new --out $HOME/.qsdm/wallet.json
# Passphrase: ********
# Confirm:    ********
ADDR=$(./qsdmcli wallet show --keystore=$HOME/.qsdm/wallet.json \
        | awk '/^address/{print $2}')
echo "$ADDR"
```

The CLI keystore is byte-compatible with the browser one — you can
import the same `wallet.json` into either flow.

## 3. Reconfigure the miner

### `qsdmminer-console` (CLI, v2 protocol)

Replace the `--address=…` flag in your start command with the new
64-hex address. Restart the miner. Future blocks will credit the new
address.

```bash
./qsdmminer-console --protocol=v2 \
  --validator=https://api.qsdm.tech \
  --address=605ab7550bd6c74ce3e5b394c1f6334cea0f6e2951938ae7fc5c775a7e1ac7e2 \
  --hmac-key-path=$HOME/.qsdm/hmac.key \
  --node-id=rig-77 \
  --gpu-uuid=$(nvidia-smi --query-gpu=uuid --format=csv,noheader | head -1) \
  --gpu-arch=ada
```

### QSDM Hive task miner

For consumer setups, use QSDM Hive as the task surface and wallet
manager. Hive starts the QSDM Miner task with the active QSDM wallet
as the reward address. If the miner service was previously installed
with a friendly-name reward address, update the service config at
`%USERPROFILE%\.qsdm\miner.toml` so `reward_address` is the new
64-hex wallet address, then restart the `QSDMMiner` service.

The legacy `qsdmminer-gui.exe` is no longer a consumer path. Do not use
it for new setups; use QSDM Hive or `qsdmminer-console`.

## 4. Verify forward credit

Within ~10 seconds of restart (one block period), the new address
should start accumulating. Probe both endpoints:

```bash
NEW_ADDR=605ab7550bd6c74ce3e5b394c1f6334cea0f6e2951938ae7fc5c775a7e1ac7e2
watch -n 10 "curl -sS 'https://api.qsdm.tech/api/v1/wallet/balance?address=$NEW_ADDR' | jq"
```

After 60-90 seconds you should see `balance > 0` with
`source: "mining-ledger"`. Once that confirms, the migration is
successful for **new** rewards.

## 5. About the historical balance

The CELL parked at `qsdm1miner-rtx3050` (or whatever your friendly name
was) is **on-chain credited** but **cryptographically unspendable**. The
v0.4.0+ `submit-signed` flow enforces:

    sender_in_envelope == hex(sha256(envelope.public_key))

and the friendly-name string fails that check by construction (it's not
a SHA-256 hex). No private key can ever satisfy the constraint.

### Recovery options

| Option | Effort | Trust requirement |
|---|---|---|
| Wait for an admin migration tool | Low | A future BLR1-deploy session adds a multi-sig-gated `/api/admin/migrate-friendly-name` endpoint that injects an unsigned `system funder` style tx to move funds onto your new keypair-backed wallet. Tracked in the next bridge / admin cluster pass. |
| Accept the loss for now | Zero | Treat the friendly-name balance as a sunk cost. Future mining starts fresh at the new wallet. |
| Stand up a new chain | High | Only viable for solo-validator deploys; loses all enrollment history. Not recommended. |

The first option is the recommended path. Don't ship the migration
endpoint as a side-effect of an unrelated session — it's a
chain-state-mutation surface that needs its own design doc, multi-sig
review (per `authz-02`), and a dedicated test suite covering
nonce semantics, replay-safety, and RBAC-deny scenarios.

## 6. Prevent recurrence

For any new miner you bring up:

1. **Generate a wallet FIRST** (step 2 above) and copy the 64-hex
   address.
2. **Validate the address shape** before pasting it into the miner
   config. The address must match `^[0-9a-f]{64}$`.
3. **Reject friendly names** at config-load time. If you operate
   multiple rigs, consider an `enroll_address_regex` policy in a
   per-host setup script that rejects anything but a 64-hex literal.
4. **Verify within one block** of starting the miner — run the
   `/wallet/balance` probe and confirm `source: "mining-ledger"` is
   reported (the new server-side fallback added in this session means
   `balance: 0, source: "storage"` is the correct posture for a brand-new
   address that hasn't earned anything yet, and `balance: > 0,
   source: "mining-ledger"` confirms credit is landing).

## Background — why is this a hazard at all?

QSDM's chain-side address field is a free-form `string` (see
`pkg/mempool/Tx.Sender` and `Tx.Recipient`). The chain happily credits
anything you put in `Recipient`, including a label like
`qsdm1miner-rtx3050`. The genesis-prefund mechanism (`QSDM_GENESIS_PREFUND_ADDR`
in the validator's systemd config) doubles down on this — operators
historically used friendly names there so the prefund line in the
genesis tx was human-readable on a chain-explorer view.

What changed in v0.4.0 (Session 95) was that **spending** got a strict
cryptographic constraint: `submit-signed` enforces the sender side of a
tx to be a SHA-256 hash of a real public key. Crediting still accepts
anything; spending requires the strict shape. The end result is the
locked-funds asymmetry this runbook addresses.

A future protocol-level fix would either:
- Map friendly-names to keypair-hashes via an on-chain `alias` table
  (governance proposal); or
- Reject friendly-name recipients at block-apply time once any prefund
  flow that still depends on them is fully migrated.

Both are out of scope for an operator runbook — track them in the
governance roadmap.
