import React from 'react';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { getCellFromBaseUnits } from 'utils';

type PropsType = {
  owner: string;
  totalBounty: number;
  nodesParticipating: number;
  totalCELLStaked: number;
  currentTopStake: number;
  myCurrentStake: number;
};

function PropertyRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center mb-1 text-left justify-left">
      <div className="w-[200px]">{`${label}:`}</div>
      <div className="w-[200px] font-semibold text-left text-finnieEmerald-light">
        {value}
      </div>
    </div>
  );
}

export function TaskDetails({
  owner,
  totalBounty,
  nodesParticipating,
  totalCELLStaked,
  currentTopStake,
  myCurrentStake,
}: PropsType) {
  return (
    <div>
      <PropertyRow label="Owner" value={owner} />
      <PropertyRow
        label="Total bounty"
        value={`${getCellFromBaseUnits(totalBounty)} ${NATIVE_TOKEN_SYMBOL}`}
      />
      <PropertyRow
        label="Nodes participating"
        value={`${nodesParticipating}`}
      />
      <PropertyRow
        label={`Total ${NATIVE_TOKEN_SYMBOL} staked`}
        value={`${getCellFromBaseUnits(totalCELLStaked)} ${NATIVE_TOKEN_SYMBOL}`}
      />
      <PropertyRow
        label="Current top stake"
        value={`${getCellFromBaseUnits(currentTopStake)} ${NATIVE_TOKEN_SYMBOL}`}
      />
      <PropertyRow
        label="My current stake"
        value={`${getCellFromBaseUnits(myCurrentStake)} ${NATIVE_TOKEN_SYMBOL}`}
      />
    </div>
  );
}
