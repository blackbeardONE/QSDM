type EmptyBountyPolicyInput = {
  isQsdmSystemTask: boolean;
  totalBountyAmount: number;
  bountyAmountPerRound: number;
};

export const isTaskBlockedByEmptyBounty = ({
  isQsdmSystemTask,
  totalBountyAmount,
  bountyAmountPerRound,
}: EmptyBountyPolicyInput) =>
  !isQsdmSystemTask && totalBountyAmount < bountyAmountPerRound;
