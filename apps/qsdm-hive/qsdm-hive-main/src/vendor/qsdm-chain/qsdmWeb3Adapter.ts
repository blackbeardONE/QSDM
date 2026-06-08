export {
  Connection,
  Keypair,
  PublicKey,
  Transaction,
  TransactionInstruction,
  SystemProgram,
  LAMPORTS_PER_SOL,
  SYSVAR_CLOCK_PUBKEY,
  sendAndConfirmTransaction,
} from './web3';

export type Signer = {
  publicKey: import('./web3').PublicKey;
  secretKey?: Uint8Array;
};
