import crypto from 'crypto';

import bs58 from 'bs58';
import nacl from 'tweetnacl';

export type TransactionSignature = string;
export type MemcmpFilter = {
  memcmp: {
    offset: number;
    bytes: string;
  };
};
export type AccountInfo<T> = {
  data: T;
  executable: boolean;
  lamports: number;
  owner: PublicKey;
  rentEpoch?: number;
};
export type ParsedAccountData = {
  parsed?: any;
  program?: string;
  space?: number;
};

const QSDM_LEGACY_TRANSACTION_ERROR =
  'Legacy transaction path is disabled in QSDM Hive. Use the signed QSDM CELL action loop.';

const isInternalQsdmLabel = (value: string) =>
  value.startsWith('Qsdm') || value.startsWith('qsdm-');

const toBytes = (value: PublicKeyInit): Uint8Array => {
  if (value instanceof PublicKey) return value.toBytes();
  if (typeof value === 'string') {
    try {
      const decoded = bs58.decode(value);
      if (decoded.length === 32 || isInternalQsdmLabel(value)) {
        return decoded;
      }
      throw new Error('Invalid public key length');
    } catch {
      if (!isInternalQsdmLabel(value)) {
        throw new Error('Invalid public key input');
      }
      return crypto.createHash('sha256').update(value).digest();
    }
  }
  return Uint8Array.from(value);
};

export type PublicKeyInit = PublicKey | string | Uint8Array | Buffer;

export class PublicKey {
  private readonly bytes: Uint8Array;

  private readonly label?: string;

  constructor(value: PublicKeyInit) {
    this.label = typeof value === 'string' ? value : undefined;
    const bytes = toBytes(value);
    this.bytes =
      bytes.length === 32
        ? Uint8Array.from(bytes)
        : crypto.createHash('sha256').update(Buffer.from(bytes)).digest();
  }

  toBase58() {
    return this.label || bs58.encode(this.bytes);
  }

  toString() {
    return this.toBase58();
  }

  toBuffer() {
    return Buffer.from(this.bytes);
  }

  toBytes() {
    return Uint8Array.from(this.bytes);
  }

  equals(other: PublicKeyInit) {
    return this.toBase58() === new PublicKey(other).toBase58();
  }

  static isOnCurve(value: PublicKeyInit) {
    return toBytes(value).length > 0;
  }

  static async findProgramAddress(
    seeds: Array<Uint8Array | Buffer>,
    programId: PublicKeyInit
  ): Promise<[PublicKey, number]> {
    return PublicKey.findProgramAddressSync(seeds, programId);
  }

  static findProgramAddressSync(
    seeds: Array<Uint8Array | Buffer>,
    programId: PublicKeyInit
  ): [PublicKey, number] {
    const hash = crypto.createHash('sha256');
    seeds.forEach((seed) => hash.update(Buffer.from(seed)));
    hash.update(new PublicKey(programId).toBuffer());
    hash.update('qsdm-program-derived-address');
    return [new PublicKey(hash.digest().subarray(0, 32)), 255];
  }
}

export class Keypair {
  public readonly publicKey: PublicKey;

  public readonly secretKey: Uint8Array;

  private constructor(secretKey: Uint8Array, publicKey: Uint8Array) {
    this.secretKey = Uint8Array.from(secretKey);
    this.publicKey = new PublicKey(publicKey);
  }

  static generate() {
    const pair = nacl.sign.keyPair();
    return new Keypair(pair.secretKey, pair.publicKey);
  }

  static fromSecretKey(secretKey: Uint8Array | Buffer | number[]) {
    const bytes = Uint8Array.from(secretKey);
    if (bytes.length === 32) {
      return Keypair.fromSeed(bytes);
    }
    const pair = nacl.sign.keyPair.fromSecretKey(bytes.slice(0, 64));
    return new Keypair(pair.secretKey, pair.publicKey);
  }

  static fromSeed(seed: Uint8Array | Buffer | number[]) {
    const seedBytes = Uint8Array.from(seed).slice(0, 32);
    if (seedBytes.length !== 32) {
      throw new Error('QSDM Keypair seed must be 32 bytes.');
    }
    const pair = nacl.sign.keyPair.fromSeed(seedBytes);
    return new Keypair(pair.secretKey, pair.publicKey);
  }
}

export type AccountMeta = {
  pubkey: PublicKey;
  isSigner: boolean;
  isWritable: boolean;
};

export class TransactionInstruction {
  keys: AccountMeta[];

  programId: PublicKey;

  data: Buffer;

  constructor({
    keys = [],
    programId,
    data = Buffer.alloc(0),
  }: {
    keys?: AccountMeta[];
    programId: PublicKeyInit;
    data?: Buffer | Uint8Array;
  }) {
    this.keys = keys;
    this.programId = new PublicKey(programId);
    this.data = Buffer.from(data);
  }
}

export class Transaction {
  instructions: TransactionInstruction[] = [];

  feePayer?: PublicKey;

  constructor(params: { feePayer?: PublicKeyInit } = {}) {
    if (params.feePayer) {
      this.feePayer = new PublicKey(params.feePayer);
    }
  }

  add(...instructions: TransactionInstruction[]) {
    this.instructions.push(...instructions);
    return this;
  }
}

export const LAMPORTS_PER_SOL = 1_000_000_000;

export const SYSVAR_CLOCK_PUBKEY = new PublicKey(
  'SysvarC1ock11111111111111111111111111111111'
);

export const SystemProgram = {
  programId: new PublicKey('11111111111111111111111111111111'),
  transfer(params: {
    fromPubkey: PublicKeyInit;
    toPubkey: PublicKeyInit;
    lamports: number;
  }) {
    return new TransactionInstruction({
      keys: [
        {
          pubkey: new PublicKey(params.fromPubkey),
          isSigner: true,
          isWritable: true,
        },
        {
          pubkey: new PublicKey(params.toPubkey),
          isSigner: false,
          isWritable: true,
        },
      ],
      programId: SystemProgram.programId,
      data: Buffer.from(JSON.stringify({ op: 'transfer', lamports: params.lamports })),
    });
  },
  createAccount(params: {
    fromPubkey: PublicKeyInit;
    newAccountPubkey: PublicKeyInit;
    lamports: number;
    space: number;
    programId: PublicKeyInit;
  }) {
    return new TransactionInstruction({
      keys: [
        {
          pubkey: new PublicKey(params.fromPubkey),
          isSigner: true,
          isWritable: true,
        },
        {
          pubkey: new PublicKey(params.newAccountPubkey),
          isSigner: true,
          isWritable: true,
        },
      ],
      programId: params.programId,
      data: Buffer.from(
        JSON.stringify({
          op: 'createAccount',
          lamports: params.lamports,
          space: params.space,
        })
      ),
    });
  },
};

export class Connection {
  readonly endpoint: string;

  readonly config?: unknown;

  constructor(endpoint: string, config?: unknown) {
    this.endpoint = endpoint;
    this.config = config;
  }

  async getBalance(..._args: any[]): Promise<number> {
    return 0;
  }

  async getAccountInfo(..._args: any[]): Promise<AccountInfo<Buffer> | null> {
    return null;
  }

  async getMinimumBalanceForRentExemption(..._args: any[]): Promise<number> {
    return 0;
  }

  async getProgramAccounts(
    ..._args: any[]
  ): Promise<Array<{ pubkey: PublicKey; account: AccountInfo<Buffer> }>> {
    return [];
  }

  async getParsedProgramAccounts(..._args: any[]): Promise<unknown[]> {
    return [];
  }

  async getParsedAccountInfo(..._args: any[]): Promise<{ value: null }> {
    return { value: null };
  }

  async getTokenAccountBalance(
    ..._args: any[]
  ): Promise<{ value: { amount: string; uiAmount: number } }> {
    return { value: { amount: '0', uiAmount: 0 } };
  }

  async getParsedTokenAccountsByOwner(
    ..._args: any[]
  ): Promise<{
    value: Array<{ pubkey: PublicKey; account: { data: ParsedAccountData } }>;
  }> {
    return { value: [] };
  }

  async getTokenAccountsByOwner(
    ..._args: any[]
  ): Promise<{ value: Array<{ pubkey: PublicKey; account: AccountInfo<Buffer> }> }> {
    return { value: [] };
  }

  async getLatestBlockhash(
    ..._args: any[]
  ): Promise<{ blockhash: string; lastValidBlockHeight: number }> {
    return { blockhash: 'qsdm-local-blockhash', lastValidBlockHeight: 0 };
  }

  async getRecentBlockhash(..._args: any[]): Promise<{ blockhash: string; feeCalculator: { lamportsPerSignature: number } }> {
    return {
      blockhash: 'qsdm-local-blockhash',
      feeCalculator: { lamportsPerSignature: 0 },
    };
  }

  async sendRawTransaction(..._args: any[]): Promise<string> {
    throw new Error(QSDM_LEGACY_TRANSACTION_ERROR);
  }

  async confirmTransaction(..._args: any[]): Promise<{ value: { err: null } }> {
    return { value: { err: null } };
  }

  async getSignatureStatus(..._args: any[]): Promise<{ value: { confirmationStatus: string; err: null } | null }> {
    return { value: { confirmationStatus: 'confirmed', err: null } };
  }
}

export async function sendAndConfirmTransaction(
  ..._args: any[]
): Promise<TransactionSignature> {
  throw new Error(QSDM_LEGACY_TRANSACTION_ERROR);
}
