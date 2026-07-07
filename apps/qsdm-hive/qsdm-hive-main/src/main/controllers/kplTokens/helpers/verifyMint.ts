import { Connection, PublicKey as KplPublicKey } from 'vendor/qsdm-chain/web3';
import { getMint } from 'vendor/qsdm-chain/splToken';
import { Connection as QsdmConnection, PublicKey } from 'vendor/qsdm-chain/qsdmWeb3Adapter';

export const verifyMintAddress = async (
  connection: Connection & QsdmConnection,
  mintAddress: PublicKey & KplPublicKey
) => {
  try {
    const mintInfo = await getMint(connection, new PublicKey(mintAddress));
    console.log('Mint info:', mintInfo);
    return true;
  } catch (error) {
    console.error('Invalid mint address:', error);
    return false;
  }
};
