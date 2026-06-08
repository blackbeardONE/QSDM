import { CheckSuccessFill, Icon } from 'vendor/qsdm-styleguide';
import React, { useState } from 'react';
import { useQueryClient } from 'react-query';
import { useLocation, useNavigate } from 'react-router-dom';

import { NATIVE_TOKEN_SYMBOL } from 'config/nativeToken';
import { Button } from 'renderer/components/ui';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { useFundNewAccountModal } from 'renderer/features/common';
import { QueryKeys } from 'renderer/services';
import { trackEvent } from 'renderer/services/analytics';
import { AppRoute } from 'renderer/types/routes';

function CreateNewKey() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [claimError, setClaimError] = useState('');
  const [isClaiming, setIsClaiming] = useState(false);

  const location = useLocation();
  const mainAccountPubKey = location?.state?.mainAccountPubKey || '';

  const claimCell = async () => {
    setIsClaiming(true);
    setClaimError('');
    let qsdmAddress = '';
    try {
      const result = await window.main.claimQsdmCellFaucet();
      qsdmAddress = result.address;
      await queryClient.invalidateQueries([QueryKeys.QsdmCellAccount]);
      await queryClient.invalidateQueries([QueryKeys.AccountBalance]);
      navigate(AppRoute.OnboardingSeeBalance);
    } catch (error) {
      setClaimError(error instanceof Error ? error.message : String(error));
    } finally {
      setIsClaiming(false);
    }
    trackEvent('claim_qsdm_cell_faucet', {
      walletAddress: mainAccountPubKey,
      qsdmAddress,
      behavior: 'claim_local_cell',
    });
  };

  const { showModal: showFundAccountModal } = useFundNewAccountModal({
    accountPublicKey: mainAccountPubKey,
  });
  const sendFunds = () => {
    navigate(AppRoute.OnboardingSeeBalance);
    showFundAccountModal();
  };

  return (
    <div className="text-lg leading-8 flex flex-col gap-[2vh] md2h:gap-[5vh] transition-all duration-300 ease-in-out">
      <div className="flex flex-col items-center justify-start w-full gap-4 mb-4 text-2xl 2xl:text-3xl md2h:text-3xl font-semibold transition-all duration-300 ease-in-out">
        <Icon
          source={CheckSuccessFill}
          className="w-8 h-8 m-2 text-finnieEmerald-light"
        />
        <span>New Account Created!</span>
      </div>

      <div className="w-full text-center text-sm 2xl:text-base md2h:text-base text-light underline-offset-2 transition-all duration-300 ease-in-out">
        <p className="font-bold mb-4">Next step: Fuel up with tokens</p>

        <div className="mb-4">
          <span>You need </span>
          <Popover
            tooltipContent={`You’ll need $${NATIVE_TOKEN_SYMBOL} (native token) as a deposit,
also known as a "stake", to run tasks on our network.`}
          >
            <span className="underline cursor-default">
              ${NATIVE_TOKEN_SYMBOL}
            </span>
          </Popover>
          <span> tokens to run your first task!</span>
        </div>

        <div className="w-[40vw]">
          <span>
            <Popover
              tooltipContent={`The QSDM Hive Faucet is a portal that allows users to receive small amounts of ${NATIVE_TOKEN_SYMBOL} tokens for free.`}
            >
              Visit our{' '}
              <button
                className="underline hover:cursor-pointer hover:text-finnieTeal-100"
                onClick={() => claimCell()}
              >
                QSDM Hive Faucet
              </button>
            </Popover>
            <span>
              {' '}
              to get free tokens or ask a friend for some {NATIVE_TOKEN_SYMBOL}{' '}
              to send funds to your account.
            </span>
          </span>
        </div>
      </div>

      <div className="flex flex-col items-center justify-between w-full gap-4 mt-6">
        <div className="flex flex-col items-center gap-4">
          <Button
            className="font-semibold bg-finnieGray-light text-finnieBlue-light w-[260px] h-[48px]"
            label={isClaiming ? 'Claiming...' : `Claim ${NATIVE_TOKEN_SYMBOL}`}
            onClick={claimCell}
          />
          {claimError && (
            <div className="w-[260px] text-xs text-center text-finnieRed">
              {claimError}
            </div>
          )}
          <Button
            className="font-semibold border text-white border-white bg-transparent w-[260px] h-[48px]"
            label="Send Funds"
            onClick={sendFunds}
          />
        </div>
        <Button
          label="Back Up QSDM Wallet"
          onClick={() => navigate(AppRoute.OnboardingBackupKeyNow)}
          className="font-semibold bg-transparent text-white w-auto h-[48px] px-6 py-[14px] underline hover:border-2 hover:border-white"
        />
        <div className="w-[25vw] h-8 text-center text-finnieTeal-100 text-xs 2xl:text-sm md2h:text-sm transition-all duration-300 ease-in-out">
          <span className="font-bold">Back It Up Anytime. </span>
          <span>
            QSDM wallet recovery uses the keystore JSON file plus passphrase.
            You can export both again from Settings &gt; Wallet.
          </span>
        </div>
      </div>
    </div>
  );
}

export default CreateNewKey;
