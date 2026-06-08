import { Connection, PublicKey as KplPublicKey } from 'vendor/qsdm-chain/web3';
import { Connection as QsdmConnection, PublicKey } from 'vendor/qsdm-chain/qsdmWeb3Adapter';

export const checkTokenBalance = async (
  connection: QsdmConnection & Connection,
  publicKey: PublicKey & KplPublicKey,
  mint: PublicKey & KplPublicKey
) => {
  const tokenAccounts = await connection.getTokenAccountsByOwner(publicKey, {
    mint,
  });
  if (tokenAccounts.value.length === 0) {
    console.log('No token account found for this mint');
    return 0;
  }

  const accountInfo = await connection.getAccountInfo(
    tokenAccounts.value[0].pubkey
  );
  const balance = await connection.getTokenAccountBalance(
    tokenAccounts.value[0].pubkey
  );

  console.log('Account info:', accountInfo);
  console.log('Raw balance:', balance.value.amount);
  console.log('UI Amount:', balance.value.uiAmount);

  return balance.value;
};
