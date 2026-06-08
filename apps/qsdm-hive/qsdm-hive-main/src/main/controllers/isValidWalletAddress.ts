import { PublicKey } from 'vendor/qsdm-chain/web3';

const QSDM_HEX_ADDRESS_PATTERN = /^[0-9a-fA-F]{32,128}$/;

export function isValidWalletAddress(
  event: Event,
  payload: { address: string }
) {
  const address = payload.address.trim();

  if (QSDM_HEX_ADDRESS_PATTERN.test(address)) {
    return true;
  }

  try {
    const publicKey = new PublicKey(address);
    return PublicKey.isOnCurve(publicKey.toBuffer());
  } catch (error) {
    return false;
  }
}
