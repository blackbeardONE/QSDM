import { Event } from 'electron';

import { PublicKey } from 'vendor/qsdm-chain/web3';
import { TOKEN_PROGRAM_ID } from 'vendor/qsdm-chain/splToken';
import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import sdk from 'main/services/sdk';
import { KPLBalanceResponse } from 'models/api';

const balanceCache: Record<
  string,
  { value: KPLBalanceResponse[]; timestamp: number }
> = {};

const CACHE_TIME = 5000;

export const getKPLBalance = async (
  _: Event,
  pubkey: string
): Promise<KPLBalanceResponse[]> => {
  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    return [];
  }

  try {
    const shouldRetrieveFromCache =
      balanceCache[pubkey] &&
      Date.now() - balanceCache[pubkey].timestamp < CACHE_TIME;
    if (shouldRetrieveFromCache) {
      return balanceCache[pubkey].value;
    } else {
      const publicKey = new PublicKey(pubkey);
      const tokenAccounts =
        await sdk.k2Connection.getParsedTokenAccountsByOwner(publicKey, {
          programId: TOKEN_PROGRAM_ID,
        });
      if (tokenAccounts === null || tokenAccounts?.value === null) {
        return [];
      }
      const newValue: KPLBalanceResponse[] = [];
      tokenAccounts.value.forEach((accountInfo) => {
        const tokenAmount =
          accountInfo.account.data?.parsed.info.tokenAmount.amount;
        const mintAddress = accountInfo.account.data?.parsed.info.mint;
        const associateTokenAddress = accountInfo.pubkey;
        const publicKeyString = associateTokenAddress.toString();
        newValue.push({
          mint: mintAddress,
          balance: tokenAmount,
          associateTokenAddress: publicKeyString,
        });
      });
      balanceCache[pubkey] = { value: newValue, timestamp: Date.now() };
      return newValue;
    }
  } catch (error) {
    console.warn('KPL balance lookup failed; treating token balances as empty.', error);
    return balanceCache[pubkey]?.value || [];
  }
};
