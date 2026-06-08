import { Event } from 'electron';

import axios from 'axios';
import { PublicKey } from 'vendor/qsdm-chain/web3';
import {
  buildQsdmCoreApiUrl,
  QSDM_CELL_DECIMALS,
  QSDM_TASK_RUNTIME_MODE,
  QSDM_WALLET_ADDRESS,
} from 'config/qsdm';
import { getQsdmTaskActionSender } from 'main/services/qsdmTaskActionSigner';
import sdk from 'main/services/sdk';
import { QsdmWalletBalanceResponse } from 'models/api/qsdm';

const balanceCache: Record<string, { balance: number; timestamp: number }> = {};
const CACHE_TIME = 5000;

const getCachedBalance = (pubkey: string) => {
  const cached = balanceCache[pubkey];
  if (!cached || Date.now() - cached.timestamp >= CACHE_TIME) return undefined;

  return cached.balance;
};

const setCachedBalance = (pubkey: string, balance: number) => {
  balanceCache[pubkey] = { balance, timestamp: Date.now() };
};

const getErrorMessage = (error: unknown) => {
  if (error instanceof Error) return error.message;
  return String(error);
};

const getQsdmNativeBalance = async (pubkey: string) => {
  const taskActionSender = getQsdmTaskActionSender();
  const qsdmAddress =
    pubkey === QSDM_WALLET_ADDRESS || pubkey === taskActionSender
      ? pubkey
      : QSDM_WALLET_ADDRESS || taskActionSender;

  if (!qsdmAddress) return undefined;

  const url = new URL(buildQsdmCoreApiUrl('/wallet/balance'));
  url.searchParams.set('address', qsdmAddress);

  const response = await axios.get<QsdmWalletBalanceResponse>(url.toString(), {
    timeout: 2500,
  });

  return Math.round(Number(response.data.balance || 0) * 10 ** QSDM_CELL_DECIMALS);
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
    console.warn(
      `Account balance lookup failed for ${pubkey}; using 0 until the next refresh.`,
      getErrorMessage(error)
    );
    return cachedBalance ?? 0;
  }
};

export default getAccountBalance;
