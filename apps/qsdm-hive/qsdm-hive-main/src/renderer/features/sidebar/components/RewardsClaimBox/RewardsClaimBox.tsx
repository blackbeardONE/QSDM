import React from 'react';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { GetTaskNodeInfoResponse } from 'models';
import { useMultipleKplTokenMetadata } from 'renderer/features/tokens/hooks/useMultipleKPLTokenMetadata';

import { BalancesCarousel } from '../AvailableBalanceInfoBox/AvailableBalanceInfoBox';
import { InfoBox } from '../InfoBox';

import { ClaimRewardAction } from './ClaimRewardAction';

type PropsType = {
  onRewardClaimClick: () => void;
  rewardClaimable?: boolean;
  rewardsAmount?: GetTaskNodeInfoResponse['pendingRewards'];
  earnedRewardsAmount?: GetTaskNodeInfoResponse['allTimeRewards'];
  isClaimingRewards?: boolean;
};

export function RewardsClaimBox({
  rewardsAmount = {},
  earnedRewardsAmount = {},
  rewardClaimable = false,
  onRewardClaimClick,
  isClaimingRewards,
}: PropsType) {
  const hasPendingRewards = Object.values(rewardsAmount).some(
    (amount) => amount > 0
  );
  const rewardsToDisplay = hasPendingRewards
    ? rewardsAmount
    : earnedRewardsAmount;
  const hasDisplayedRewards = Object.values(rewardsToDisplay).some(
    (amount) => amount > 0
  );

  const stakesToDisplay = Object.entries(rewardsToDisplay).filter(
    ([mintAddress, value]) => value > 0 && mintAddress !== 'CELL'
  );
  const { data: kplList, isLoading } = useMultipleKplTokenMetadata(
    stakesToDisplay.map((e) => e[0])
  );

  const formattedStakesToDisplay = stakesToDisplay.map(
    ([mintAddress, value], index) => ({
      symbol: kplList?.[index]?.symbol || NATIVE_TOKEN_SYMBOL,
      balance: value,
      mintAddress,
      logoURI: kplList?.[index]?.logoURI,
      decimals: kplList?.[index]?.decimals || 9,
    })
  );

  return (
    <InfoBox className="flex flex-col items-center p-2 xl:px-4 lgh:py-4 lgh:gap-4">
      <div className="flex items-center justify-between w-full">
        <div className="flex flex-col items-start w-full">
          <span className="text-sm text-green-2 pb-1">Rewards</span>
          <BalancesCarousel
            cellBalance={rewardsToDisplay.CELL || 0}
            isLoadingCellBalance={isLoading}
            kplTokenItems={formattedStakesToDisplay as any}
          />
        </div>
      </div>
      <div className="h-13 flex items-center justify-center">
        {rewardClaimable ? (
          <ClaimRewardAction
            isClaimingRewards={isClaimingRewards}
            onRewardClaimClick={onRewardClaimClick}
          />
        ) : hasDisplayedRewards ? (
          <span className="text-sm mt-4 text-green-2">Auto-claimed</span>
        ) : (
          <span className="text-sm mt-4">Add a task to earn</span>
        )}
      </div>
    </InfoBox>
  );
}
