import { Event } from 'electron';
import fs from 'fs';

import { QSDM_TASK_RUNTIME_MODE } from 'config/qsdm';
import {
  Transaction,
  sendAndConfirmTransaction,
  SystemProgram,
  LAMPORTS_PER_SOL,
  Keypair,
  PublicKey,
} from 'vendor/qsdm-chain/web3';
import sdk from 'main/services/sdk';
import { submitQsdmWalletTransferIntent } from 'main/services/qsdmWalletTransfer';
import { ErrorType, NetworkErrors, TransferCellParam } from 'models';
import { throwDetailedError } from 'utils';

import { getMainSystemWalletPath } from '../../node/helpers';

export const transferCellFromMainWallet = async (
  event: Event,
  payload: TransferCellParam
): Promise<void> => {
  const { accountName, amount, toWalletAddress } = payload;

  if (QSDM_TASK_RUNTIME_MODE === 'qsdm-native') {
    await submitQsdmWalletTransferIntent({
      amount,
      recipient: toWalletAddress.trim(),
    });
    return;
  }

  console.log('Transferring funds from Main wallet');

  const mainSystemAccountPath = getMainSystemWalletPath(accountName);
  let mainSystemAccount;
  if (!fs.existsSync(mainSystemAccountPath)) {
    return throwDetailedError({
      detailed: `No wallet found at location: ${mainSystemAccountPath}`,
      type: ErrorType.NO_ACCOUNT_KEY,
    });
  }

  try {
    mainSystemAccount = Keypair.fromSecretKey(
      Uint8Array.from(
        JSON.parse(
          fs.readFileSync(mainSystemAccountPath, 'utf-8')
        ) as Uint8Array
      )
    );
  } catch (e: any) {
    console.error(e);
    return throwDetailedError({
      detailed: `Error during retrieving wallet from ${mainSystemAccountPath}: ${e}`,
      type: ErrorType.NO_ACCOUNT_KEY,
    });
  }

  try {
    // Means account already exists
    const createSubmitterAccTransaction = new Transaction().add(
      SystemProgram.transfer({
        fromPubkey: mainSystemAccount.publicKey,
        toPubkey: new PublicKey(toWalletAddress),
        lamports: amount * LAMPORTS_PER_SOL,
      })
    );
    await sendAndConfirmTransaction(
      sdk.k2Connection,
      createSubmitterAccTransaction,
      [mainSystemAccount]
    );
  } catch (e: any) {
    console.error(e);
    let errorType = ErrorType.GENERIC;
    if (e.message.toLowerCase().includes(NetworkErrors.TRANSACTION_TIMEOUT)) {
      errorType = ErrorType.TRANSACTION_TIMEOUT;
    } else if (e.message.toLowerCase().includes('invalid public key input')) {
      errorType = ErrorType.INVALID_WALLET_ADDRESS;
    }
    return throwDetailedError({
      detailed: e.message,
      type: errorType,
    });
  }
};
