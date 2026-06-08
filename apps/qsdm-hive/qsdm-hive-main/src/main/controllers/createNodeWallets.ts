import { Event } from 'electron';
import fs from 'fs';

import { validateMnemonic } from 'bip39';
import { SystemDbKeys } from 'config/systemDbKeys';
import { namespaceInstance } from 'main/node/helpers/Namespace';
import { deriveNodeWalletsFromMnemonic } from 'main/node/helpers/deriveWallets';
import { ErrorType } from 'models';
import { CreateNodeWalletsParam, CreateNodeWalletsResponse } from 'models/api';
import { throwDetailedError } from 'utils';

import { getAppDataPath } from '../node/helpers/getAppDataPath';

const createNodeWallets = async (
  event: Event,
  payload: CreateNodeWalletsParam
): Promise<CreateNodeWalletsResponse> => {
  const { mnemonic, accountName, encryptedSecretPhrase } = payload;
  if (!mnemonic) {
    return throwDetailedError({
      detailed: 'Please provide a Hive recovery phrase to create a local Hive profile',
      type: ErrorType.NO_MNEMONIC,
    });
  }

  if (!validateMnemonic(mnemonic)) {
    return throwDetailedError({
      detailed: 'Please provide a valid Hive recovery phrase',
      type: ErrorType.NO_VALID_MNEMONIC,
    });
  }

  if (!accountName) {
    return throwDetailedError({
      detailed: 'Please provide an account name to generate wallets',
      type: ErrorType.NO_VALID_ACCOUNT_NAME,
    });
  }
  if (!fs.existsSync(`${getAppDataPath()}/namespace`))
    fs.mkdirSync(`${getAppDataPath()}/namespace`);
  if (!fs.existsSync(`${getAppDataPath()}/wallets`))
    fs.mkdirSync(`${getAppDataPath()}/wallets`);

  if (!/^[0-9a-zA-Z ... ]+$/.test(accountName)) {
    return throwDetailedError({
      detailed: `Please provide a valid account name, got "${accountName}"`,
      type: ErrorType.NO_VALID_ACCOUNT_NAME,
    });
  }
  try {
    const { stakingWallet, kplStakingWallet, mainWallet } =
      deriveNodeWalletsFromMnemonic(mnemonic);

    // Creating stakingWallet
    const stakingWalletFilePath = `${getAppDataPath()}/namespace/${accountName}_stakingWallet.json`;
    if (fs.existsSync(stakingWalletFilePath)) {
      return throwDetailedError({
        detailed: `Staking wallet with same account name "${accountName}" already exists`,
        type: ErrorType.NO_VALID_ACCOUNT_NAME,
      });
    }

    // Creating KPL staking wallet
    const kplStakingWalletFilePath = `${getAppDataPath()}/namespace/${accountName}_kplStakingWallet.json`;
    if (fs.existsSync(kplStakingWalletFilePath)) {
      return throwDetailedError({
        detailed: `KPL staking wallet with same account name "${accountName}" already exists`,
        type: ErrorType.NO_VALID_ACCOUNT_NAME,
      });
    }

    console.log({
      kplStakingWalletPubKey: kplStakingWallet.publicKey.toBase58(),
    });

    // Creating MainAccount
    const mainWalletFilePath = `${getAppDataPath()}/wallets/${accountName}_mainSystemWallet.json`;
    if (fs.existsSync(mainWalletFilePath)) {
      return throwDetailedError({
        detailed: `Main wallet with same account name "${accountName}" already exists`,
        type: ErrorType.NO_VALID_ACCOUNT_NAME,
      });
    }

    const stakingWalletFileContent = JSON.stringify(
      Array.from(stakingWallet.secretKey)
    );
    const kplStakingWalletFileContent = JSON.stringify(
      Array.from(kplStakingWallet.secretKey)
    );
    const mainWalletFileContent = JSON.stringify(
      Array.from(mainWallet.secretKey)
    );
    const existingWalletFiles = fs.readdirSync(`${getAppDataPath()}/wallets`);
    const walletAlreadyExists = existingWalletFiles.some((file) => {
      const fileContent = fs.readFileSync(
        `${getAppDataPath()}/wallets/${file}`
      );
      return fileContent.equals(Buffer.from(mainWalletFileContent));
    });
    // Verify a wallet created from the same Hive recovery phrase doesn't exist.
    if (walletAlreadyExists) {
      return throwDetailedError({
        detailed: 'A Hive profile with the same recovery phrase already exists',
        type: ErrorType.DUPLICATE_ACCOUNT,
      });
    }

    console.log(
      'Generating Staking wallet from Hive recovery phrase',
      stakingWallet.publicKey.toBase58()
    );
    console.log(
      'Generating KPL Staking wallet from Hive recovery phrase',
      kplStakingWallet.publicKey.toBase58()
    );
    console.log(
      'Generating Main wallet from Hive recovery phrase',
      mainWallet.publicKey.toBase58()
    );
    fs.writeFileSync(stakingWalletFilePath, stakingWalletFileContent);
    fs.writeFileSync(kplStakingWalletFilePath, kplStakingWalletFileContent);
    fs.writeFileSync(mainWalletFilePath, mainWalletFileContent);

    // Add the encrypted Hive recovery phrase to local app storage.
    const allEncryptedSecretPhraseString: string | undefined =
      await namespaceInstance.storeGet(SystemDbKeys.EncryptedSecretPhraseMap);

    try {
      const allEncryptedSecretPhrase: Record<string, string> =
        allEncryptedSecretPhraseString
          ? (JSON.parse(allEncryptedSecretPhraseString) as Record<
              string,
              string
            >)
          : {};

      const publicKey = mainWallet.publicKey.toBase58();

      allEncryptedSecretPhrase[publicKey] = encryptedSecretPhrase;

      const stringifiedAllEncryptedSecretPhrase = JSON.stringify(
        allEncryptedSecretPhrase
      );
      await namespaceInstance.storeSet(
        SystemDbKeys.EncryptedSecretPhraseMap,
        stringifiedAllEncryptedSecretPhrase
      );
    } catch (error: any) {
      console.error(error);
      return throwDetailedError({
        detailed: `Error during saving Hive recovery phrase: ${error}`,
        type: ErrorType.GENERIC,
      });
    }

    return {
      stakingWalletPubKey: stakingWallet.publicKey.toBase58(),
      kplStakingWalletPubKey: kplStakingWallet.publicKey.toBase58(),
      mainAccountPubKey: mainWallet.publicKey.toBase58(),
    };
  } catch (err: any) {
    console.log('ERROR during Account creation', err);
    if (err instanceof Error && err.message.includes('"type":')) {
      throw err;
    }
    return throwDetailedError({
      detailed: err instanceof Error ? err.message : String(err),
      type: ErrorType.GENERIC,
    });
  }
};

export default createNodeWallets;
