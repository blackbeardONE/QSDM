import React from 'react';
import { toast } from 'react-hot-toast';
import { useMutation } from 'react-query';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { LoadingSpinner } from 'renderer/components/ui/LoadingSpinner';
import { redeemTokensInNewNetwork } from 'renderer/services';

export function ForceRedeemMigrationTokens() {
  const { mutate: redeemTokens, isLoading } = useMutation(
    redeemTokensInNewNetwork,
    {
      onSuccess: (tokens) => {
        const toastMessage = tokens
          ? `We found some! Adding ${tokens} ${NATIVE_TOKEN_SYMBOL} to your balance.`
          : `All migrated ${NATIVE_TOKEN_SYMBOL} has been claimed already.`;
        toast.success(toastMessage);
      },
    }
  );

  const handleForceRedeem = () => {
    redeemTokens();
  };

  return (
    <div className="text-sm mb-2 flex gap-3">
      <span>
        Got all migrated {NATIVE_TOKEN_SYMBOL} after the latest QSDM network
        migration?
      </span>
      {isLoading ? (
        <LoadingSpinner className="h-8 w-8 -mt-2" />
      ) : (
        <span>
          <button
            onClick={handleForceRedeem}
            className="underline text-finnieEmerald-light underline-offset-2 font-semibold"
          >
            Check Now
          </button>
        </span>
      )}
    </div>
  );
}
