import { isTaskBlockedByEmptyBounty } from './taskRuntimePolicy';

describe('task runtime bounty policy', () => {
  it('blocks an ordinary task whose bounty cannot cover another round', () => {
    expect(
      isTaskBlockedByEmptyBounty({
        isQsdmSystemTask: false,
        totalBountyAmount: 0,
        bountyAmountPerRound: 1,
      })
    ).toBe(true);
  });

  it('keeps QSDM system tasks runnable while their Hive bounty is empty', () => {
    expect(
      isTaskBlockedByEmptyBounty({
        isQsdmSystemTask: true,
        totalBountyAmount: 0,
        bountyAmountPerRound: 1,
      })
    ).toBe(false);
  });
});
