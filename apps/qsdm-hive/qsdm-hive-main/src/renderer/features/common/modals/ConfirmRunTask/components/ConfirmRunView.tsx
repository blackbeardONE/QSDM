import React from 'react';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { Button } from 'renderer/components/ui';

type ConfirmRunViewProps = {
  taskName: string;
  isPrivate?: boolean;
  stakeAmountToDisplay: number;
  ticker?: string;
  onConfirm: () => void;
};

export function ConfirmRunView({
  taskName,
  isPrivate,
  stakeAmountToDisplay,
  ticker,
  onConfirm,
}: ConfirmRunViewProps) {
  return (
    <>
      <p className="my-3 mx-auto">
        Are you sure you want to run <strong>{taskName}</strong>?
      </p>

      {isPrivate && (
        <div className="text-finnieRed text-md font-light mx-auto text-center">
          <p>
            This task <span className="font-bold">has not been verified</span>{' '}
            by the QSDM Hive team and community.
          </p>
          <p className="font-bold">Run with caution.</p>
        </div>
      )}

      <Button
        label="Run Task"
        className="w-56 h-12 m-auto font-semibold bg-finnieGray-tertiary text-finnieBlue-light"
        onClick={onConfirm}
      />

      <p className="text-sm font-light text-finnieEmerald-light my-3 mx-auto">
        Current Stake: {stakeAmountToDisplay} {ticker || NATIVE_TOKEN_SYMBOL}
      </p>
    </>
  );
}
