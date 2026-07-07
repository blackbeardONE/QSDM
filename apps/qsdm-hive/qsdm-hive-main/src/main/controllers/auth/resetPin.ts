import { Event } from 'electron';

import { deriveMainWalletFromMnemonic } from 'main/node/helpers/deriveWallets';

import { getAllAccounts } from '../getAllAccounts';

export const resetPin = async (
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  _: Event,
  payload: { seedPhraseString: string }
) => {
  const allAcounts = await getAllAccounts(_, false);

  const mainWallet = deriveMainWalletFromMnemonic(payload.seedPhraseString);
  const accountExists = allAcounts.find((account) => {
    return account.mainPublicKey === mainWallet.publicKey.toBase58();
  });

  if (accountExists) {
    return true;
  }

  return false;
};
