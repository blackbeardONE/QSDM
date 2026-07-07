import React from 'react';
import { useMutation } from 'react-query';
import { useNavigate } from 'react-router-dom';

import { AppRoute } from 'renderer/types/routes';

import { SeedPhraseInput } from '../SeedPhraseInput/SeedPhraseInput';

export function RetrieveNodeAccess() {
  const navigate = useNavigate();

  const resetPinMutation = async (seedPhrase: string) => {
    const seedPhraseMatchesOneOfTheAccounts = await window.main.resetPin({
      seedPhraseString: seedPhrase,
    });
    if (!seedPhraseMatchesOneOfTheAccounts) {
      throw new Error(
        'Hive recovery phrase does not match any local profiles'
      );
    }
  };

  const {
    mutate: resetPin,
    isLoading,
    isError,
    error,
  } = useMutation(resetPinMutation, {
    onSuccess: () => {
      navigate(AppRoute.MyNode, { state: { noBackButton: true } });
    },
  });

  const handleSeedPhraseSubmit = (seedPhrase: string) => {
    resetPin(seedPhrase);
  };

  return (
    <div className="qsdm-cell-screen flex flex-col items-center justify-center w-full h-full">
      <div className="z-50 flex flex-col items-center">
        <div className="pb-4 text-xs font-bold uppercase text-[#f7bf42]">
          QSDM Hive / CELL Network
        </div>
        <div>
          <h1 className="pb-4 text-4xl font-semibold text-white">
            Retrieve Node Access
          </h1>
          <p className="pb-6 text-lg text-white">
            Enter your Hive recovery phrase to retrieve local node access
          </p>
        </div>
        <SeedPhraseInput
          confirmActionLabel="Retrieve Node Access"
          onSeedPhraseSubmit={handleSeedPhraseSubmit}
          // eslint-disable-next-line @typescript-eslint/no-explicit-any
          externalError={isError ? (error as any).message : undefined}
          isLoading={isLoading}
        />

        <button
          onClick={() => navigate(AppRoute.Unlock)}
          className="mt-8 text-white underline"
          disabled={isLoading}
        >
          Unlock with PIN
        </button>
      </div>
    </div>
  );
}
