# QSDM Genesis Policy v1.2

> Published 20 November 2026. The new rules take effect at **block 1,210,000** (about 26 November 2026), the same
> block at which the PULSE pool settlement starts. Every full node must run a release with these rules before that
> block. Compared with v1.1, clauses 1, 2 and 6 change, and clause 9 is new.

Decision (owner, 15 November 2026; effective date set 20 November 2026): the network, not a person, funds the shared
programs. From block 1,210,000 the block reward becomes **4.56 CELL**. Each reward actually paid is split **85 % miner,
8 % Operations, 5 % Play Rewards, 2 % Security**. The Security share returns to the miner while that fund holds
**100,000 CELL** or more. The 90,000,000 CELL cap does not change.

**QSDM Genesis Policy v1.2** (20 November 2026. Supersedes v1.1 of 2026-11-01. In force once the owner signs the hash
below.)

1. **Supply.** The maximum supply of Cell (CELL) is **90,000,000 CELL** (9,000,000,000,000,000 dust). All of it is
   issued by protocol mining under the integer emission schedule in `pkg/chain/emission.go` and
   `pkg/chain/network_rewards.go`.
   - There is **no genesis treasury allocation**, and no founder, insider, team, advisor, sale or airdrop allocation.
   - The program funds in clause 9 receive **shares of the scheduled emission**. They are paid out of each block
     reward, never on top of it. No CELL is created beyond the schedule or the cap.
   - Any future proposal to add an allocation needs a new published policy version, the owner's written decision and
     the governance process. It may never mint CELL on a chain that has already launched without it.
2. **Emission.** Target block time 10 seconds. The epoch allocations are unchanged: 45,000,000 CELL for epoch 0,
   then half of the previous epoch's allocation each time. Rewards are computed with integer arithmetic only.
   - **Below the activation height** (H = **1,210,000**), epochs last 126,230,400 seconds (12,623,040 blocks) and the
     reward is the epoch allocation divided by the blocks in an epoch (3.56490987 CELL in epoch 0). About
     4,313,537 CELL are issued below H.
   - **From H**, the reward is **4.56 CELL**. It halves when the current epoch's allocation is used up, not after a
     fixed time. The block that uses up an allocation pays exactly what is left of it. Each epoch therefore issues
     exactly its allocation, and the total stays below the cap.
   - At 10-second blocks epoch 0 ends at about block 10,132,469 (3.2 years after genesis), and each later epoch lasts
     about 3.13 years instead of 4. About 96.9 % of the cap is issued after about 15.7 years instead of 20.
   - Block-time changes do not change the cap.
3. **Fees.** Unchanged: until independent validators produce blocks, 100 % of transaction fees are burned. Any
   change to the fee destination is a consensus change, with a new policy version and a published activation height.
4. **Issuance path.** Unchanged: before mainnet, the synthetic `qsdm-system-funder` reserve is replaced by capped,
   consensus-native issuance that cannot exceed the schedule in (2) or the cap in (1). The split in clause 9 applies
   to that issuance too.
5. **Genesis path.** Unchanged (Option D2-A, the migration at a published fork height; see v1.1 clause 5).
6. **Custody.** Core never holds a treasury key. Program wallets follow TREASURY_POLICY §1-§7.
   - The three network funds of clause 9 are funded only by their protocol shares.
   - Their keys are held by the owner on the owner's own devices (single-signer wallets), never on a server.
7. **Disclosure.** As in v1.1, plus:
   - the three fund addresses, H and the fingerprint of the four values (logged by every node at boot) are published
     in `CELL_TOKENOMICS.md` and on qsdm.tech;
   - every fund's balance is public on the chain;
   - a monthly report lists each fund's income and every payment, with its transaction ID.
8. **Status.** Unchanged: the network stays a pilot network until the owner declares mainnet in writing and
   TREASURY_POLICY §9 gates 3-5 close.
9. **Network-funded rewards** (new). From H, every node applies the same four rules, in block production, validation
   and replay. Blocks below H replay as before.
   1. **Split.** Each mining reward actually paid is split:
      - **85 %** to the miner (for a PULSE pool member, into the member's pool escrow, as before);
      - **8 %** to the **Operations fund**: infrastructure, servers, audits and the costs of running the network.
        Every payment is listed in the monthly report (clause 7);
      - **5 %** to the **Play Rewards fund**: rewards for players of **all QSDM games**, with one chest per game.
        **Bannerfall is first.** Claims follow each game's published rules. The fund has no ceiling;
      - **2 %** to the **Security fund**: bug bounties and security work.

      The fund shares are rounded down to the dust, and the miner receives the remainder, so the parts always add up
      to the reward exactly.
   2. **Security ceiling.** While the Security fund holds **100,000 CELL** or more, its 2 % share goes to the miner.
      The share resumes when payments bring the fund below 100,000 CELL.
   3. **Owner cap.** The per-owner epoch cap (2,000 CELL per 8,640 blocks) counts the miner's 85 % share only, so the
      fund shares never use up a miner's cap. The cap stays a configuration value of the producer.
   4. **Addresses.** The three fund addresses are part of the activation. A node refuses to start with H set and any
      of the three addresses missing, and every validator must use the same four values.
      - Operations `13d786706accfbe77c5ddf6fc6757e1cca07bd01aff0cad3dcf9411d92cf11c9`
      - Play Rewards `b8fcf6a1f352aacd5b6abf03b77c499ca9b110905809a86f631c6c72eb525dff`
      - Security `4146b9a5cdb287c46c1ce67f6e377af7d5bc0c5f83cbef7403f0346b5433e301` (the bug bounty wallet)

   Changing a share, the ceiling or an address needs a new policy version and a new published activation height.

---
Signature block (owner): Blackbeard, CEO, qsdm.tech, role project owner, date 2026-11-20
SHA-256 of the text above this line: [computed at signing: `sed '/^---$/,$d' GENESIS_POLICY_v1.2.md | sha256sum`]
Signature: detached ML-DSA-87 over that hash with the owner's offline key, or an owner-signed git commit/tag (fallback).
