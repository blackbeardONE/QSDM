import { Event } from 'electron';

import {
  buildQsdmCoreApiUrl,
  QSDM_CELL_DECIMALS,
  QSDM_TASK_RUNTIME_MODE,
  QSDM_WALLET_ADDRESS,
} from 'config/qsdm';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import { getQsdmCanonicalChainSafety } from 'main/services/qsdmCanonicalChain';
import { qsdmGetJson } from 'main/services/qsdmHttpRead';
import { selectQsdmWalletAddress } from 'main/services/qsdmWalletAddress';
import sdk from 'main/services/sdk';
import { QsdmWalletBalanceResponse } from 'models/api/qsdm';
import { PublicKey } from 'vendor/qsdm-chain/web3';

const balanceCache: Record<string, { balance: number; timestamp: number }> = {};
const CACHE_TIME = 5000;

const getCachedBalance = (pubkey: string) => {
  const cached = balanceCache[pubkey];
  if (!cached || Date.now() - cached.timestamp >= CACHE_TIME) return undefined;

  return cached.balance;
};

const getStaleCachedBalance = (pubkey: string) => balanceCache[pubkey]?.balance;

const setCachedBalance = (pubkey: string, balance: number) => {
  balanceCache[pubkey] = { balance, timestamp: Date.now() };
};

const getErrorMessage = (error: unknown) => {
  if (error instanceof Error) return error.message;
  return String(error);
};

const getQsdmNativeBalance = async (pubkey: string) => {
  const safety = await getQsdmCanonicalChainSafety();
  if (!safety.safe) {
    throw new Error(
      safety.detail || 'QSDM canonical network could not be verified'
    );
  }
  const taskActionSender = getQsdmTaskActionSender();
  const requestedAddress =
    pubkey === QSDM_WALLET_ADDRESS || pubkey === taskActionSender ? pubkey : '';
  const qsdmAddress = selectQsdmWalletAddress({
    requestedAddress,
    signerAddress: taskActionSender,
    configuredAddress: QSDM_WALLET_ADDRESS,
  });

  if (!qsdmAddress) return undefined;

  const url = new URL(buildQsdmCoreApiUrl('/wallet/balance'));
  url.searchParams.set('address', qsdmAddress);

  const response = await qsdmGetJson<QsdmWalletBalanceResponse>(
    url.toString(),
    { timeout: 4000 }
  );

  return Math.round(
    Number(response.balance || 0) * 10 ** QSDM_CELL_DECIMALS
  );
};

const getAccountBalance = async (_: Event, pubkey: string): Promise<number> => {
  const cachedBalance = getCachedBalance(pubkey);
  if (cachedBalance !== undefined) return cachedBalance;

  try {
    if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
      const qsdmBalance = await getQsdmNativeBalance(pubkey);
      if (qsdmBalance !== undefined) {
        setCachedBalance(pubkey, qsdmBalance);
        return qsdmBalance;
      }
    }

    const balance = await sdk.k2Connection.getBalance(
      new PublicKey(pubkey),
      'processed'
    );
    setCachedBalance(pubkey, balance);
    return balance;
  } catch (error) {
    const staleBalance = getStaleCachedBalance(pubkey);
    console.warn(
      staleBalance === undefined
        ? `Account balance lookup failed for ${pubkey}; no confirmed balance is cached yet. ${getErrorMessage(
            error
          )}`
        : `Account balance lookup failed for ${pubkey}; retaining the last confirmed balance. ${getErrorMessage(
            error
          )}`
    );
    return staleBalance ?? 0;
  }
};

export default getAccountBalance;
