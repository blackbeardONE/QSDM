/* eslint-disable react/no-unescaped-entities */
import { addDays, differenceInSeconds } from 'date-fns';
import React, { useEffect, useState } from 'react';
import { useMutation, useQueryClient } from 'react-query';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { Button, ErrorMessage, LoadingSpinner } from 'renderer/components/ui';
import { QueryKeys } from 'renderer/services';

import { useUserAppConfig } from '../../hooks';

interface RecoverLostTokensResult {
  status: 'skipped' | 'completed';
  message: string;
  recoveredRewards: number;
  recoveredStakes: number;
  actions?: string[];
  finalBalance?: number;
}

export function RecoverTokens() {
  const queryClient = useQueryClient();
  const handleRecoverTokens = async () => {
    return window.main.recoverLostTokens();
  };

  const { userConfig } = useUserAppConfig();

  const lastClaimDate = userConfig?.lastLostTokensClaimDate;

  const { mutate, isLoading, data, error } = useMutation<RecoverLostTokensResult>(
    handleRecoverTokens,
    {
      onSuccess: () => {
        queryClient.invalidateQueries([QueryKeys.UserSettings]);
      },
    }
  );

  const [countdown, setCountdown] = useState('');

  useEffect(() => {
    const calculateCountdown = () => {
      if (lastClaimDate) {
        const nextClaimDate = addDays(new Date(lastClaimDate), 7);
        const currentDate = new Date();
        const secondsRemaining = differenceInSeconds(
          nextClaimDate,
          currentDate
        );

        if (secondsRemaining > 0) {
          const days = Math.floor(secondsRemaining / (60 * 60 * 24));
          const hours = Math.floor(
            (secondsRemaining % (60 * 60 * 24)) / (60 * 60)
          );
          const minutes = Math.floor((secondsRemaining % (60 * 60)) / 60);
          const seconds = Math.floor(secondsRemaining % 60);

          setCountdown(`${days}d ${hours}h ${minutes}m ${seconds}s`);
        } else {
          setCountdown('');
        }
      }
    };

    calculateCountdown();
    const timer = setInterval(calculateCountdown, 1000);

    return () => {
      clearInterval(timer);
    };
  }, [lastClaimDate]);

  const isClaimDisabled = !!countdown;

  return (
    <div className="flex flex-col gap-4">
      <div className="text-sm">
        <h1 className="mb-2 font-semibold">
          Lost some {NATIVE_TOKEN_SYMBOL}? No worries.
        </h1>
        <p>
          Your tokens aren't lost, they're just on an adventure. Click to bring
          them back home.
        </p>
      </div>
      <div className="flex items-center gap-4">
        {isLoading ? (
          <span className="w-64 h-14">
            <LoadingSpinner className="w-10 h-10 mx-auto" />
          </span>
        ) : (
          <Button
            label="Claim Now"
            onClick={() => mutate()}
            className="w-44 font-semibold h-10 bg-gray-primary text-purple-5"
            disabled={isLoading || isClaimDisabled}
            loading={isLoading}
          />
        )}

        {countdown && (
          <span className="text-sm text-gray-500">
            Next claim available in: {countdown}
          </span>
        )}
      </div>

      {data ? <RecoveryMessage result={data} /> : null}
      {error ? (
        <ErrorMessage error={(error as any).message} className="text-sm" />
      ) : null}
    </div>
  );
}

export function RecoveryMessage({
  result,
}: {
  result: RecoverLostTokensResult;
}) {
  const className =
    result.status === 'completed'
      ? 'text-sm text-finnieEmerald-light'
      : 'text-sm text-yellow-400';

  return <div className={className}>{result.message}</div>;
}
