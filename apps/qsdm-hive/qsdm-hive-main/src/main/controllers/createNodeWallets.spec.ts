import { Event } from 'electron';
import fs from 'fs';

import { Keypair } from 'vendor/qsdm-chain/web3';
import * as bip39 from 'bip39';
import { namespaceInstance } from 'main/node/helpers/Namespace';

import createNodeWallets from './createNodeWallets';

// jest.mock('bip39', () => {
//   const actual = jest.requireActual('bip39');
//   return {
//     validateMnemonic: jest.fn().mockImplementation(actual.validateMnemonic),
//     mnemonicToSeedSync: jest.fn().mockImplementation(actual.mnemonicToSeedSync),
//   };
// });

jest.spyOn(Keypair, 'fromSeed');
jest.spyOn(bip39, 'mnemonicToSeedSync');
jest.mock('fs', () => ({
  existsSync: jest.fn(),
  mkdirSync: jest.fn(),
  readdirSync: jest.fn().mockReturnValue([]),
  readFileSync: jest.fn(),
  writeFileSync: jest.fn(),
}));
jest.mock('main/node/helpers/Namespace', () => ({
  namespaceInstance: {
    storeGet: jest.fn().mockReturnValue('{}'),
    storeSet: jest.fn(),
  },
}));
jest.mock('utils', () => ({
  throwDetailedError: jest.fn().mockImplementation((payload) => payload),
}));
jest.mock('../node/helpers/getAppDataPath', () => ({
  getAppDataPath: jest.fn().mockReturnValue('/path/to/appdata'),
}));
jest.mock('ed25519-hd-key', () => ({
  derivePath: jest.fn().mockReturnValue({
    key: Uint8Array.from(
      Buffer.from('DedFVZVtUH/juF0K1UIdgkYhRdzMvuVkYg4NizbZpjI=', 'base64')
    ),
  }),
}));

describe('createNodeWallets', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe('invalid payload', () => {
    it('returns detailed error', async () => {
      let payload = {};
      let result = await createNodeWallets({} as Event, payload as any);
      expect(result).toEqual({
        detailed: 'Please provide a Hive recovery phrase to create a local Hive profile',
        type: 'NO_MNEMONIC',
      });

      payload = {
        mnemonic:
          'struggle noble ocean glance december wreck problem cereal spoil menu way onion',
      };
      result = await createNodeWallets({} as Event, payload as any);
      expect(result).toEqual({
        detailed: 'Please provide an account name to generate wallets',
        type: 'NO_VALID_ACCOUNT_NAME',
      });
    });
  });

  describe('valid payload', () => {
    it('create new dir if /namespace and /wallets are not existed', async () => {
      const payload = {
        mnemonic:
          'struggle noble ocean glance december wreck problem cereal spoil menu way onion',
        accountName: 'example_account_name',
        encryptedSecretPhrase: 'example_encrypted_secret_phrase',
      };
      fs.existsSync = jest.fn(() => false);
      await createNodeWallets({} as Event, payload);

      expect(fs.mkdirSync).toHaveBeenCalledTimes(2);
    });

    it('does not create new dir if /namespace and /wallets are existed', async () => {
      const payload = {
        mnemonic:
          'struggle noble ocean glance december wreck problem cereal spoil menu way onion',
        accountName: 'example_account_name',
        encryptedSecretPhrase: 'example_encrypted_secret_phrase',
      };
      fs.existsSync = jest.fn(() => true);
      await createNodeWallets({} as Event, payload);

      expect(fs.mkdirSync).toHaveBeenCalledTimes(0);
    });

    it('return detailed error if accountName is invalid', async () => {
      const payload = {
        mnemonic:
          'struggle noble ocean glance december wreck problem cereal spoil menu way onion',
        accountName: 'example_account_name',
        encryptedSecretPhrase: 'example_encrypted_secret_phrase',
      };

      const result = await createNodeWallets({} as Event, payload);

      expect(result).toEqual({
        detailed:
          'Please provide a valid account name, got "example_account_name"',
        type: 'NO_VALID_ACCOUNT_NAME',
      });
    });

    describe('create staking wallet', () => {
      it('returns detailed error if staking wallet already exists', async () => {
        const payload = {
          mnemonic:
            'struggle noble ocean glance december wreck problem cereal spoil menu way onion',
          accountName: 'User12345678',
          encryptedSecretPhrase: 'example_encrypted_secret_phrase',
        };

        fs.existsSync = jest.fn(() => true);

        const result = await createNodeWallets({} as Event, payload);

        expect(result).toEqual({
          detailed:
            'Staking wallet with same account name "User12345678" already exists',
          type: 'NO_VALID_ACCOUNT_NAME',
        });
      });

      it('creates staking wallet and main wallet', async () => {
        const payload = {
          mnemonic:
            'struggle noble ocean glance december wreck problem cereal spoil menu way onion',
          accountName: 'User12345678',
          encryptedSecretPhrase: 'example_encrypted_secret_phrase',
        };

        fs.existsSync = jest.fn(() => false);
        const result = await createNodeWallets({} as Event, payload);

        expect(bip39.mnemonicToSeedSync).toHaveBeenCalledWith(
          'struggle noble ocean glance december wreck problem cereal spoil menu way onion',
          ''
        );
        expect(Keypair.fromSeed).toHaveBeenCalled();
        expect(fs.writeFileSync).toHaveBeenCalledTimes(3);
        expect(namespaceInstance.storeSet).toHaveBeenCalledWith(
          'ENCRYPTED_SEED_PHRASE_MAP',
          '{"CDH9MAF1YxiiunHuNzHxWpBK37jurjLYeW9bjyqNYLXM":"example_encrypted_secret_phrase"}'
        );
        expect(result).toEqual({
          mainAccountPubKey: 'CDH9MAF1YxiiunHuNzHxWpBK37jurjLYeW9bjyqNYLXM',
          stakingWalletPubKey: 'CDH9MAF1YxiiunHuNzHxWpBK37jurjLYeW9bjyqNYLXM',
          kplStakingWalletPubKey:
            'CDH9MAF1YxiiunHuNzHxWpBK37jurjLYeW9bjyqNYLXM',
        });
      });
    });
  });
});
