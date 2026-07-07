import React, { useCallback, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { PinInput } from 'renderer/components/PinInput';
import { Button } from 'renderer/components/ui';
import { Popover } from 'renderer/components/ui/Popover/Popover';
import { usePinUtils } from 'renderer/features/auth/hooks';
import {
  useMainAccount,
  useUserAppConfig,
} from 'renderer/features/settings/hooks';
import { MAINNET_RPC_URL } from 'renderer/features/shared/constants';
import {
  getMainAccountPublicKey,
  openBrowserWindow,
  switchNetwork,
} from 'renderer/services';
import { Theme } from 'renderer/types/common';
import { AppRoute } from 'renderer/types/routes';

import { useOnboardingContext } from '../../context/onboarding-context';

function CreatePin() {
  const { encryptPin } = usePinUtils();
  const [termsAccepted, setTermsAccepted] = useState(false);
  const [pin, setPin] = useState('');
  const [pinConfirm, setPinConfirm] = useState('');
  const [focus, setFocus] = useState(true);
  const navigate = useNavigate();
  const { data: mainAccountPubKey } = useMainAccount();
  const isExistingAccountPinRepair = !!mainAccountPubKey;
  const { handleSaveUserAppConfigAsync } = useUserAppConfig();

  const { setAppPin } = useOnboardingContext();

  const handlePinCreate = async () => {
    const hashedPin = await encryptPin(pin);
    const hasLocalAccount = await getMainAccountPublicKey()
      .then(Boolean)
      .catch(() => false);

    /**
     * sets raw pin value to context
     */
    setAppPin(pin);

    await switchNetwork(MAINNET_RPC_URL).catch((error) => {
      console.warn('Network switch failed during PIN setup', error);
    });

    await handleSaveUserAppConfigAsync({
      settings: {
        /**
         * Saves **hashed** pin value to user config
         */
        pin: hashedPin,
        onboardingCompleted: hasLocalAccount ? true : undefined,
        hasCopiedReferralCode: false,
        hasStartedTheMainnetMigration: true,
        hasFinishedTheMainnetMigration: true,
      },
    });

    const nextRoute = hasLocalAccount
      ? AppRoute.AppInit
      : AppRoute.OnboardingPickKeyCreationMethod;
    navigate(nextRoute, { replace: true });
  };

  const pinIsMatching = useMemo(() => pin === pinConfirm, [pin, pinConfirm]);
  const pinsLengthIsMatching = useMemo(
    () => pin.length === 6 && pinConfirm.length === 6,
    [pin, pinConfirm]
  );

  const canLogIn = useCallback(() => {
    if (pinIsMatching && termsAccepted && pin.length === 6) {
      return true;
    }
    return false;
  }, [pin, pinIsMatching, termsAccepted]);

  const disableLogin = !canLogIn();

  const openTermsWindow = () => {
    openBrowserWindow('https://qsdm.tech/terms');
  };

  const handlePinSubmit = (pin: string) => {
    setFocus(false);
    setPin(pin);
  };

  return (
    <div className="relative w-full h-full overflow-hidden flex flex-col items-center justify-center text-center gap-[5vh] md2h:gap-[10vh] transition-all duration-300 ease-in-out">
      <div className="z-50 gap-4 md2h:gap-10 flex flex-col items-center justify-center transition-all duration-300 ease-in-out">
        <div className="text-lg tracking-wide">
          {isExistingAccountPinRepair ? 'Create a new local ' : 'Create an '}
          <Popover
            theme={Theme.Light}
            tooltipContent={
              <p className="max-w-[450px] xl:max-w-xl">
                You&apos;ll use this PIN to unlock your node. If you forget it,
                create a new local PIN and restore your CELL wallet with the
                QSDM keystore JSON and passphrase.
              </p>
            }
          >
            <span className="underline underline-offset-4 text-finniePurple">
              Access PIN
            </span>
          </Popover>{' '}
          {isExistingAccountPinRepair
            ? 'to unlock your existing QSDM account.'
            : 'to secure the Node.'}
        </div>
        <PinInput focus onComplete={handlePinSubmit} />
      </div>

      <div className="gap-4 md2h:gap-10 flex flex-col items-center justify-center transition-all duration-300 ease-in-out">
        <div className="">Confirm your Access PIN.</div>
        <PinInput
          focus={!pinConfirm && !focus}
          onChange={(pin) => setPinConfirm(pin)}
          key={pin}
        />
        <div className="pt-4 text-xs text-finnieOrange">
          {!pinIsMatching && pinsLengthIsMatching ? (
            <span>
              Oops! These PINs don’t match. Double check it and try again.
            </span>
          ) : (
            <span>
              If you forget your PIN, restore your QSDM wallet with its
              keystore JSON and passphrase.
            </span>
          )}
        </div>
      </div>
      <div className="flex flex-col items-start pt-4">
        <div className="relative items-center inline-block">
          <input
            id="link-checkbox"
            type="checkbox"
            className="w-3 h-3 terms-checkbox accent-finniePurple"
            onKeyDown={(e) =>
              e.key === 'Enter' && setTermsAccepted(!termsAccepted)
            }
            checked={termsAccepted}
            onChange={() => setTermsAccepted(!termsAccepted)}
          />
          <label htmlFor="link-checkbox" className="ml-4 text-sm font-medium ">
            I agree with the{' '}
            <button
              className="underline text-finniePurple"
              onClick={openTermsWindow}
            >
              Terms of Service
            </button>
            .
          </label>
        </div>
        <Button
          disabled={disableLogin}
          label={isExistingAccountPinRepair ? 'Save PIN and unlock' : 'Log in'}
          onClick={handlePinCreate}
          className="mt-6 mr-3 bg-finnieGray-light text-finnieBlue w-60"
        />
      </div>
    </div>
  );
}

export default CreatePin;
