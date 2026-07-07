import { decrypt, encrypt } from '@metamask/browser-passworder';

export const encryptSeedPhraseWithNewPin = async (
  oldPin: string,
  newPin: string,
  encryptedSecretPhrase: string,
  key: string
) => {
  /**
   * Decrypt the local Hive recovery phrase with the old pin.
   */
  try {
    const decryptedSecretPhrase = await decrypt(oldPin, encryptedSecretPhrase);

    /**
     * Encrypt the local Hive recovery phrase with the new pin.
     */
    return encrypt(newPin, decryptedSecretPhrase);
  } catch (error) {
    throw new Error(
      `Decryption of the Hive recovery phrase with old pin failed for account ${key}`
    );
  }
};
