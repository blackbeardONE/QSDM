# PULSE: the QSDM mining pool rule

PULSE (Payout, Utility & Ledger Settlement Engine) lets NVIDIA miners mine
together and be paid every block, without anyone holding their CELL. The pool
software is open source (MIT) in
[qsdm-mining-bootstrap](https://github.com/qsdm-tech/qsdm-mining-bootstrap);
the project runs a pool at [qsdm.tech/pool](https://qsdm.tech/pool).

## How it works

- **Join.** A miner joins a pool with one signed transaction (`qsdm/pulse/v1`,
  `join`). Its GPU stays enrolled to its own wallet and it still signs every
  proof itself, as in solo mining.
- **Escrow.** From then on the producer pays the member's mining rewards to a
  keyless escrow account (`qsdm-pulse-escrow-<pool>:<member>`). No key exists
  for it: nobody can spend from it, the pool included.
- **Settle.** The pool keeps a share window (TIDES-style: the newest shares up
  to 8 x the network difficulty) and signs a settlement batch with ML-DSA-87.
  The chain checks the batch (an authorized pool, its key, the sequence, the
  proofs it names are escrowed and not settled before) and pays every member
  directly by share weight. The fee goes to the pool's fee address; balances
  under the payout floor carry forward; rounding dust goes to the fee address.
- **Leave and reclaim.** A member can leave at any time. A reward the pool does
  not settle within 8,640 blocks (about one day) can be reclaimed by the member.

## The rules the chain enforces

| Rule | Value |
|---|---|
| Activation | block **1,210,000** (about 26-27 November 2026), the release default of rc1114 |
| Authorized pools | the pool at qsdm.tech/pool (ID `37c1aaf5...3e2d`) |
| Fee ceiling | 3 % (300 basis points), for every pool |
| Per-payee cap | 2,000 CELL per 8,640-block epoch, as in solo mining; the rest carries forward |
| Reclaim timeout | 8,640 blocks |
| Replay | a settlement, a pooled proof or a sequence number is accepted once |

A pooled reward never fills a deferred bond: the producer pools only fully
bonded members. Every node must use the same values, so leave the `[consensus]
pulse_*` keys and `QSDM_PULSE_*` variables unset. Details:
[Consensus rules](CONSENSUS_RULES.md#the-pulse-rule-from-block-1210000).

## The hosted pool

The pool at [qsdm.tech/pool](https://qsdm.tech/pool) charges **1 %**, published
on the page and capped by the network at 3 %; a fee change takes effect only
after 30 days' notice on that page. The payout floor is 0.1 CELL. Miners join
from QSDM Hive (the pool option of the miner) or with the command-line miner.
