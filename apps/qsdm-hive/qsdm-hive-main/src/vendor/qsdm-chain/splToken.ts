import {
  PublicKey,
  PublicKeyInit,
  TransactionInstruction,
} from './web3';

export const TOKEN_PROGRAM_ID = new PublicKey(
  'QsdmToken1111111111111111111111111111111111'
);

const deriveAssociatedAddress = (
  mint: PublicKeyInit,
  owner: PublicKeyInit
): PublicKey => {
  const mintKey = new PublicKey(mint).toBase58();
  const ownerKey = new PublicKey(owner).toBase58();
  return new PublicKey(`qsdm-associated-${mintKey}-${ownerKey}`);
};

export async function getMint(..._args: any[]) {
  return {
    decimals: 0,
    supply: BigInt(0),
    isInitialized: true,
    mintAuthority: null,
    freezeAuthority: null,
  };
}

export async function getOrCreateAssociatedTokenAccount(
  _connection: unknown,
  _payer: unknown,
  mint: PublicKeyInit,
  owner: PublicKeyInit,
  ..._args: any[]
) {
  return {
    address: deriveAssociatedAddress(mint, owner),
    mint: new PublicKey(mint),
    owner: new PublicKey(owner),
    amount: BigInt(0),
  };
}

export function createTransferInstruction(
  source: PublicKeyInit,
  destination: PublicKeyInit,
  owner: PublicKeyInit,
  amount: number | bigint,
  ..._args: any[]
) {
  return new TransactionInstruction({
    keys: [
      { pubkey: new PublicKey(source), isSigner: false, isWritable: true },
      { pubkey: new PublicKey(destination), isSigner: false, isWritable: true },
      { pubkey: new PublicKey(owner), isSigner: true, isWritable: false },
    ],
    programId: TOKEN_PROGRAM_ID,
    data: Buffer.from(JSON.stringify({ op: 'token-transfer', amount: String(amount) })),
  });
}
