import { Keypair } from 'vendor/qsdm-chain/web3';
import * as bip39 from 'bip39';
import {
  KPL_STAKING_DERIVATION_PATH,
  MAIN_WALLET_DERIVATION_PATH,
  STAKING_DERIVATION_PATH,
} from 'config/node';
import { derivePath } from 'ed25519-hd-key';

const deriveKeypair = (seedHex: string, derivationPath: string) =>
  Keypair.fromSeed(derivePath(derivationPath, seedHex).key);

export const deriveMainWalletFromMnemonic = (mnemonic: string) => {
  const seedHex = bip39.mnemonicToSeedSync(mnemonic, '').toString('hex');
  return deriveKeypair(seedHex, MAIN_WALLET_DERIVATION_PATH);
};

export const deriveNodeWalletsFromMnemonic = (mnemonic: string) => {
  const seedHex = bip39.mnemonicToSeedSync(mnemonic, '').toString('hex');

  return {
    mainWallet: deriveKeypair(seedHex, MAIN_WALLET_DERIVATION_PATH),
    stakingWallet: deriveKeypair(seedHex, STAKING_DERIVATION_PATH),
    kplStakingWallet: deriveKeypair(seedHex, KPL_STAKING_DERIVATION_PATH),
  };
};
