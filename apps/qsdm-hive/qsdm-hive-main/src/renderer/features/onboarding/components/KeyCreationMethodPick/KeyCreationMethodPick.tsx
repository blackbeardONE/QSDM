import { Button } from 'vendor/qsdm-styleguide';
import { encrypt } from '@metamask/browser-passworder';
import React, { ChangeEventHandler, memo, useState } from 'react';
import { useMutation, useQueryClient } from 'react-query';
import { useNavigate } from 'react-router-dom';

import CreateAccountSvg from 'assets/svgs/onboarding/create-new-account-icon.svg';
import { ErrorMessage } from 'renderer/components/ui/ErrorMessage';
import {
  LoadingSpinner,
  LoadingSpinnerSize,
} from 'renderer/components/ui/LoadingSpinner';
import { useAccounts } from 'renderer/features/settings';
import {
  QueryKeys,
  createNodeWallets,
  generateSeedPhrase,
  setActiveAccount,
} from 'renderer/services';
import { AppRoute } from 'renderer/types/routes';

import { useOnboardingContext } from '../../context/onboarding-context';

function KeyCreationMethodPick() {
  const [accountName, setAccountName] = useState<string>('');
  const [error, setError] = useState<Error | string>('');
  const navigate = useNavigate();
  const { setNewSeedPhrase, setSystemKey, appPin } = useOnboardingContext();
  const { accounts } = useAccounts();

  const createNewKey = async (accountName: string) => {
    const seedPhrase = await generateSeedPhrase();

    const encryptedSecretPhrase: string = await encrypt(appPin, seedPhrase);

    const resp = await createNodeWallets(
      seedPhrase,
      accountName,
      encryptedSecretPhrase
    );
    return {
      seedPhrase,
      mainAccountPubKey: resp.mainAccountPubKey,
      accountName,
    };
  };

  const queryCache = useQueryClient();

  const seedPhraseGenerateMutation = useMutation(createNewKey, {
    onSuccess: async ({ seedPhrase, mainAccountPubKey, accountName }) => {
      await setActiveAccount(accountName);
      setNewSeedPhrase(seedPhrase);
      setSystemKey(mainAccountPubKey);
      queryCache.invalidateQueries([QueryKeys.MainAccount]);
      navigate(AppRoute.OnboardingBackupKeyNow, {
        state: { mainAccountPubKey },
      });
    },
    onError: (error) => {
      setError(error as Error);
    },
  });

  const handleChangeInput: ChangeEventHandler<HTMLInputElement> = ({
    target: { value: enteredAccountName },
  }) => {
    setError('');
    setAccountName(enteredAccountName);
    const enteredNameIsDuplicate = accounts?.some(
      (account) => account.accountName === enteredAccountName
    );
    if (enteredNameIsDuplicate) {
      setError('You already have an account registered with that name');
    }
  };

  const handleClickCreate = () => {
    if (accountName) {
      seedPhraseGenerateMutation.mutate(accountName);
    } else {
      setError('Please provide an account name to create a new account');
    }
  };

  const isCreatingAccount = seedPhraseGenerateMutation.isLoading;

  return (
    <div className="w-full flex flex-col justify-center gap-8 md2h:gap-20 transition-all duration-300 ease-in-out">
      <div className="flex flex-col pl-1 text-lg text-center">
        <p className="mb-4">Create a local Hive profile for this device.</p>
        <p className="font-light">
          CELL wallet import and recovery uses your QSDM keystore JSON and
          passphrase.
        </p>
      </div>

      <div className="w-fit mx-auto">
        <div className="px-1 leading-8 text-left text-sm">Account name</div>
        <input
          className="w-[40vw] max-w-[600px] px-6 py-2 rounded-md bg-finnieBlue-light-tertiary"
          type="text"
          value={accountName}
          onChange={handleChangeInput}
        />
        <div className="h-12 px-6 -mb-12 mx-auto w-fit">
          {error && <ErrorMessage error={error} />}
        </div>
      </div>

      <div className="mt-10 md2h:mt-18">
        <div className="flex flex-col items-center gap-5 md2h:gap-8">
          <div className="flex flex-col items-center gap-1.5 md2h:gap-3.5">
            <Button
              onClick={handleClickCreate}
              label="Create Hive Profile"
              labelClassesOverrides="text-finnieBlue"
              buttonClassesOverrides="flex justify-center w-[295px]"
              iconLeft={
                <CreateAccountSvg className="text-finnieBlue w-4 h-4" />
              }
              disabled={!!error}
            />
            <div className=" text-xs text-[#FFC78F]">
              Recommended for this device.
            </div>
          </div>
        </div>
      </div>
      {isCreatingAccount && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black bg-opacity-50">
          <div className="flex flex-col items-center justify-center gap-4">
            <LoadingSpinner size={LoadingSpinnerSize.XLarge} />
            <div className="text-4xl text-white">Creating Account...</div>
          </div>
        </div>
      )}
    </div>
  );
}

export default memo(KeyCreationMethodPick);
