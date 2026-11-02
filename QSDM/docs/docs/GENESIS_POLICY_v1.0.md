# QSDM Genesis Policy v1.0

Decisions (owner, 2026-10-16): **D1-a 90 M hard cap, no genesis treasury** (owner's words: "90M cap").
D2-A, D3-a and D5-a follow the packet's recommendations under the owner's standing instruction "do everything by
yourself"; the owner may change any of them before signing. Audit row `tok-01` stays **pending** until counsel's
written opinion (D5-a).

**QSDM Genesis Policy v1.0** (2026-10-16. Owner decision D1-a recorded 2026-10-16; in force once the owner signs the hash below.)

1. **Supply.** The maximum supply of Cell (CELL) is **90,000,000 CELL** (9,000,000,000,000,000 dust).
   All of it is issued by protocol mining under the integer emission schedule in
   `pkg/chain/emission.go`. There is **no genesis treasury allocation**, and no founder, insider, team,
   advisor, sale or airdrop allocation. The earlier 10,000,000 CELL treasury proposal is withdrawn. Any
   future proposal to add an allocation needs a new published policy version, a counsel opinion and the
   governance process, and it may never mint CELL on a chain that has already launched without it.
2. **Emission.** Target block time 10 seconds. Emission epochs of 126,230,400 seconds (12,623,040 blocks).
   The per-epoch allocation halves each epoch, starting at 45,000,000 CELL. Rewards are computed with
   integer arithmetic only. Block-time changes after genesis do not change the cap.
3. **Fees.** Until independent validators produce blocks, 100% of transaction fees are burned (not
   credited to any account). Any change to the fee destination is a consensus change. It needs a new
   policy version and a published activation height.
4. **Issuance path.** Before mainnet, the synthetic `qsdm-system-funder` reserve is replaced by capped,
   consensus-native issuance that cannot exceed the schedule in (2) or the cap in (1).
5. **Genesis path.** Mainnet continues the current chain (**Option D2-A**). A deterministic, network-wide
   migration at a published fork height, announced at least 60,480 blocks (7 days) ahead:
   (a) removes the synthetic funder balance;
   (b) burns or explicitly classifies every historic development credit and every balance that
   classified issuance does not explain, with aggregate figures published;
   (c) keeps balances that are explained by mining rewards and ordinary signed transfers.
   The migration manifest (aggregate-only, from `qsdm-ledger-fork-plan`) and its SHA-256 are published,
   and every validator must apply the same manifest hash. If the manifest cannot reach a "ready" state,
   the fallback is a clean genesis (Option D2-B) under a new policy version.
6. **Custody.** Core never holds a treasury key. Program wallets follow TREASURY_POLICY §1-§7 and are
   funded only from existing CELL.
7. **Disclosure.** This policy, the migration manifest hash, the fork height and the counsel engagement
   (firm name with the firm's consent; the opinion itself stays privileged) are published in
   `CELL_TOKENOMICS.md` front-matter and in the `tok-01` audit row.
8. **Status.** Until counsel's written opinion is received and TREASURY_POLICY §9 gates 3-5 close, the
   network stays a pilot network (pre-mainnet).


---
Signature block (owner): name Joedel Lopez Dalioan, role project owner, date 2026-10-19
SHA-256 of the text above this line: b6c1889717afe45d52f1e89b8fee6fd27d8a40a5cf9d169915b378f34d0d3b5c, computed by `sed '/^---$/,$d' GENESIS_POLICY_v1.0.md | sha256sum`
Signature: detached ML-DSA-87 over that hash with the owner's offline key, or an owner-signed git commit/tag (fallback).
Approval record: the owner approved this text in writing on 2026-10-19. The detached signature has not been made yet; when it is, it will be published next to this file.
