// Type declarations for the QSDM JavaScript SDK.

export interface CoinInfo {
    name: string;
    symbol: string;
    decimals: number;
    smallestUnit: string;
}

export interface BrandingInfo {
    name: string;
    fullTitle: string;
}

export interface TokenomicsInfo {
    capDust: number;
    capCell: number;
    emittedDust: number;
    emittedCell: number;
    remainingDust: number;
    blockRewardDust: number;
    blockRewardCell: number;
    currentEpoch: number;
    nextHalvingHeight: number;
    nextHalvingEtaSeconds: number;
    targetBlockTimeSeconds: number;
    blocksPerEpoch: number;
}

export interface NodeStatus {
    nodeId: string;
    version: string;
    uptime: string;
    chainTip?: number;
    peers?: number;
    nodeRole?: string;
    network?: string;
    coin?: CoinInfo;
    branding?: BrandingInfo;
    tokenomics?: TokenomicsInfo;
    extra: Record<string, unknown>;
}

export interface HealthStatus {
    status: string;
    [key: string]: unknown;
}

export interface ClientOptions {
    fetch?: typeof fetch;
    timeoutMs?: number;
}

export interface WalletNonceResponse {
    sender: string;
    /** Last nonce already used by the wallet. */
    nonce: number;
    /** Nonce to sign the next transfer with (nonce + 1). */
    next: number;
}

/**
 * A CELL transfer as `POST /api/v1/wallet/submit-signed` accepts it. Field
 * names and order follow the node's pkg/wallet.TransactionData; see
 * {@link transferSigningBytes} for the signed bytes.
 */
export interface TransferEnvelope {
    /** Client-chosen ID; 16-128 characters of [0-9A-Za-z_-] keep it usable as a parent. */
    id: string;
    /** Must equal hex(sha256(public_key)). */
    sender: string;
    /** 64-character lowercase hex wallet address. */
    recipient: string;
    /** CELL, positive, at most 8 decimals. */
    amount: number;
    /** CELL, zero or more, at most 8 decimals. */
    fee: number;
    geotag: string;
    /** Proof-of-Entanglement parents (2-10 committed transaction IDs). Signed. */
    parent_cells: string[] | null;
    /** `next` from GET /api/v1/wallet/nonce. Omitted from the signed bytes when 0. */
    nonce?: number;
    /** RFC 3339. */
    timestamp: string;
    /** Hex ML-DSA-87 signature (4,627 bytes). */
    signature?: string;
    /** Hex ML-DSA-87 public key (2,592 bytes). Not signed. */
    public_key?: string;
}

export interface SignedTransferEnvelope extends TransferEnvelope {
    signature: string;
    public_key: string;
}

/** Body of a successful POST /api/v1/wallet/submit-signed. */
export interface SubmitTransferResponse {
    transaction_id: string;
    /** "pending" (HTTP 202, queued for the next block) or "accepted" (HTTP 200, settled by the node). */
    status: 'pending' | 'accepted' | string;
    /** "block-pending", "p2p" or "local-only". */
    broadcast?: string;
}

export interface ChainParent {
    id: string;
    height: number;
}

/** Body of GET /api/v1/chain/parents. */
export interface ChainParentsResponse {
    tip: number;
    poe_activation_height: number;
    poe_active: boolean;
    min_parents: number;
    max_parents: number;
    window_blocks: number;
    /** The value to copy into `parent_cells`. */
    parents: string[];
    recent: ChainParent[];
}

export interface ReceiptLog {
    topic: string;
    data?: Record<string, unknown>;
    index: number;
}

/** Body of GET /api/v1/receipts/{tx_id}. */
export interface Receipt {
    tx_id: string;
    block_height: number;
    block_hash?: string;
    /** 1 = applied, 0 = failed. */
    status: number;
    gas_used: number;
    fee: number;
    logs?: ReceiptLog[];
    error?: string;
    timestamp: string;
    contract_id?: string;
    index_in_block: number;
}

/** Body of GET /api/v1/receipts. */
export interface ReceiptsResponse {
    tip: number;
    from: number;
    to: number;
    limit: number;
    receipts: Receipt[];
}

export interface TransferRequest {
    sender: string;
    recipient: string;
    amount: number;
    fee?: number;
    geotag?: string;
    /** Generated when omitted. */
    id?: string;
    /** Current time when omitted. */
    timestamp?: string;
    /** Read from GET /api/v1/wallet/nonce when omitted. */
    nonce?: number;
    /** Read with getPoEParents() when omitted; an explicit [] is kept. */
    parentCells?: string[];
}

export type TransferSignerResult =
    | {
        signature: string | Uint8Array;
        public_key?: string | Uint8Array;
        publicKey?: string | Uint8Array;
    }
    | SignedTransferEnvelope
    | string;

export interface SendTransferRequest extends TransferRequest {
    /**
     * Signs with a key the caller holds: an ML-DSA-87 (FIPS 204) signature
     * over `signingBytes` with an empty context string.
     */
    signTransfer(
        envelope: TransferEnvelope,
        signingBytes: Uint8Array
    ): TransferSignerResult | Promise<TransferSignerResult>;
}

export type TransferRejectionReason =
    | 'invalid'
    | 'unauthorized'
    | 'insufficient_balance'
    | 'duplicate'
    | 'nonce'
    | 'signature'
    | 'poe_parents'
    | 'policy'
    | 'rate_limited'
    | 'unavailable'
    | 'other';

/** The `window.qsdm` provider injected by the QSDM Wallet extension. */
export interface QSDMWalletProvider {
    readonly isQsdmHive: true;
    readonly version?: string;
    request(args: { method: string; params?: unknown }): Promise<unknown>;
    on?(event: string, callback: (value: unknown) => void): QSDMWalletProvider;
    removeListener?(event: string, callback: (value: unknown) => void): QSDMWalletProvider;
}

export interface ProviderTransferResult extends SubmitTransferResponse {
    /** The connected wallet address, when the helper connected first. */
    sender?: string;
}

export interface StreamUsageReceipt {
    stream_id: string;
    sequence: number;
    cumulative_active_seconds: number;
    observed_at: string;
    signature: string;
}

export interface StreamAction {
    id: string;
    sender: string;
    stream_id: string;
    action: 'open' | 'receipt' | 'pause' | 'resume' | 'settle' | 'close';
    provider?: string;
    service_id?: string;
    device_id_hash?: string;
    session_public_key?: string;
    price_dust?: number;
    price_period_seconds?: number;
    budget_dust?: number;
    max_active_seconds?: number;
    expires_at?: string;
    receipt?: StreamUsageReceipt;
    nonce?: number;
    timestamp: string;
}

export interface StreamActionEnvelope {
    action: StreamAction;
    signature: string;
    public_key: string;
}

export interface StreamState {
    stream_id: string;
    payer: string;
    provider: string;
    service_id: string;
    device_id_hash: string;
    session_public_key: string;
    price_dust: number;
    price_period_seconds: number;
    budget_dust: number;
    max_active_seconds: number;
    expires_at: string;
    status: 'active' | 'paused' | 'closed';
    cumulative_active_seconds: number;
    paused_duration_seconds: number;
    last_receipt_sequence: number;
    last_receipt_observed_at?: string;
    accrued_dust: number;
    settled_dust: number;
    refunded_dust: number;
    remaining_budget_dust: number;
    unsettled_dust: number;
    opened_at: string;
    last_paused_at?: string;
    last_resumed_at?: string;
    closed_at?: string;
    last_action: string;
    last_action_id: string;
    last_action_at: string;
    action_count: number;
}

export interface StreamsResponse {
    runtime: string;
    source: string;
    state_root: string;
    streams: StreamState[];
}

export interface StreamResponse {
    runtime: string;
    source: string;
    state_root: string;
    stream: StreamState;
}

export interface StreamActionSubmitResponse {
    action_id: string;
    stream_id: string;
    action: string;
    sender: string;
    status: string;
    mempool_status: string;
}

export interface StreamActionNonceResponse {
    runtime: string;
    source: 'chain';
    sender: string;
    action_nonce: number;
    present: boolean;
}

export interface CellStreamStorage {
    getItem(key: string): string | null | Promise<string | null>;
    setItem(key: string, value: string): void | Promise<void>;
    removeItem?(key: string): void | Promise<void>;
}

export interface CellStreamSessionSigner {
    publicKeyHex: string;
    sign(
        signingBytes: Uint8Array,
        receipt: Omit<StreamUsageReceipt, 'signature'>
    ): Uint8Array | string | Promise<Uint8Array | string>;
}

export interface CellStreamActionSignerResult {
    signature: string;
    publicKey?: string;
    public_key?: string;
}

export interface CellStreamWalletOptions {
    client: QSDMClient;
    address: string;
    signAction(
        action: StreamAction,
        signingBytes: Uint8Array
    ): CellStreamActionSignerResult | Promise<CellStreamActionSignerResult>;
    idFactory?(input: {
        action: StreamAction['action'];
        streamID: string;
        nowMs: number;
    }): string;
    clock?: () => number;
    sleep?: (milliseconds: number) => Promise<void>;
    confirmationTimeoutMs?: number;
    confirmationPollMs?: number;
}

export interface CellStreamOpenConfig {
    streamId: string;
    provider: string;
    serviceId: string;
    deviceIdHash: string;
    priceDust: number;
    pricePeriodSeconds: number;
    budgetDust: number;
    maxActiveSeconds: number;
    expiresAt: string;
}

export interface CellStreamMeterSnapshot {
    initialized: boolean;
    requiresRecovery: boolean;
    estimatedAccruedDust?: string;
    estimatedRemainingBudgetDust?: string;
    stream: Record<string, unknown> | null;
}

export interface CellStreamServiceMeterOptions {
    wallet: CellStreamWallet;
    storage: CellStreamStorage;
    storageKey?: string;
    sessionSigner: CellStreamSessionSigner;
    receiptSubmitter(
        receipt: StreamUsageReceipt,
        snapshot: CellStreamMeterSnapshot
    ): unknown | Promise<unknown>;
    clock?: () => number;
    setInterval?: typeof globalThis.setInterval;
    clearInterval?: typeof globalThis.clearInterval;
    receiptIntervalSeconds?: number;
    checkpointIntervalMs?: number;
    autoSchedule?: boolean;
    onError?(error: unknown, snapshot: CellStreamMeterSnapshot): void | Promise<void>;
    onLimitReached?(snapshot: CellStreamMeterSnapshot): void | Promise<void>;
}

export const CELL_STREAM_RUNTIME_VERSION: number;

export function canonicalStreamAction(action: StreamAction): StreamAction;
export function streamActionSigningBytes(action: StreamAction): Uint8Array;
export function canonicalStreamReceipt(
    receipt: StreamUsageReceipt,
    includeSignature?: boolean
): StreamUsageReceipt | Omit<StreamUsageReceipt, 'signature'>;
export function streamReceiptSigningBytes(
    receipt: StreamUsageReceipt | Omit<StreamUsageReceipt, 'signature'>
): Uint8Array;

export class CellStreamWallet {
    readonly client: QSDMClient;
    readonly address: string;
    constructor(options: CellStreamWalletOptions);
    prepareAction(action: Partial<StreamAction>): Promise<StreamActionEnvelope>;
    submitPrepared(envelope: StreamActionEnvelope): Promise<StreamActionSubmitResponse>;
    waitForAction(envelope: StreamActionEnvelope): Promise<StreamState | null>;
    submitAction(
        action: Partial<StreamAction>,
        options?: {
            preparedEnvelope?: StreamActionEnvelope;
            onPrepared?(envelope: StreamActionEnvelope): void | Promise<void>;
        }
    ): Promise<{
        envelope: StreamActionEnvelope;
        submission: StreamActionSubmitResponse;
        stream: StreamState | null;
    }>;
}

export class CellStreamServiceMeter {
    constructor(options: CellStreamServiceMeterOptions);
    initialize(): Promise<CellStreamMeterSnapshot>;
    snapshot(): CellStreamMeterSnapshot;
    onServiceStarted(config?: CellStreamOpenConfig): Promise<CellStreamMeterSnapshot>;
    onServiceStopped(options?: { close?: boolean }): Promise<CellStreamMeterSnapshot>;
    recover(serviceIsActive: boolean): Promise<CellStreamMeterSnapshot>;
    checkpoint(options?: { flush?: boolean }): Promise<CellStreamMeterSnapshot>;
    flushReceipt(options?: { force?: boolean }): Promise<unknown>;
    close(): Promise<CellStreamMeterSnapshot>;
    dispose(): Promise<void>;
}

export class ApiError extends Error {
    readonly status: number;
    readonly url: string;
    readonly body: string;
    constructor(status: number, url: string, bodyText: string);
}

/** A node's refusal of a signed transfer (POST /api/v1/wallet/submit-signed). */
export class TransferError extends ApiError {
    readonly reason: TransferRejectionReason;
    /** The node's explanation. */
    readonly serverMessage: string;
    /** Set when the node reports a duplicate. */
    readonly transactionId?: string;
    constructor(status: number, url: string, bodyText: string);
}

export function isNotFound(err: unknown): boolean;
export function isUnauthorized(err: unknown): boolean;
/** True for a 422 refusal because of missing, malformed or stale Proof-of-Entanglement parents. */
export function isPoERejection(err: unknown): boolean;

/**
 * The exact bytes an ML-DSA-87 transfer signature covers: the envelope with
 * `signature` set to "" and `public_key` removed, encoded as the node's Go
 * encoding/json encodes it.
 */
export function transferSigningBytes(envelope: TransferEnvelope): Uint8Array;

/** `window.qsdm` from the QSDM Wallet extension, or null. */
export function getWalletProvider(scope?: { qsdm?: unknown }): QSDMWalletProvider | null;

/** Waits for the extension's `qsdm#initialized` event; resolves null after `timeoutMs` (default 3000). */
export function waitForWalletProvider(options?: {
    timeoutMs?: number;
    scope?: {
        qsdm?: unknown;
        addEventListener?(type: string, listener: () => void): void;
        removeEventListener?(type: string, listener: () => void): void;
    };
}): Promise<QSDMWalletProvider | null>;

/**
 * Sends CELL through the QSDM Wallet extension: QSDM Hive asks the user,
 * builds the envelope (nonce, Proof-of-Entanglement parents, fee 0), signs
 * it with the wallet's ML-DSA-87 key and submits it to the node Hive uses.
 * Connects the site first (`qsdm_requestAccounts`) unless `connect` is false.
 */
export function sendTransferWithProvider(
    provider: QSDMWalletProvider,
    transfer: { recipient: string; amount: number },
    options?: { connect?: boolean }
): Promise<ProviderTransferResult>;

/** A 64-bit unsigned value: a non-negative safe integer, a bigint or a decimal string. */
export type Uint64Like = number | bigint | string;

/**
 * A QSDM-20 action (QSDM/docs/docs/QSDM_20.md). Each action carries exactly
 * the fields of its row in the specification. Amounts are whole base units
 * as decimal strings or bigints (a token with 8 decimals counts 1.5 tokens
 * as "150000000"); numbers are refused.
 */
export interface QSDM20Action {
    action: 'create' | 'transfer' | 'mint' | 'burn' | 'renounce';
    /** create: 1 to 12 characters A to Z and 0 to 9. */
    symbol?: string;
    /** create: 1 to 48 characters, no control characters. */
    name?: string;
    /** create: 0 to 18. */
    decimals?: number;
    max_supply?: string | bigint;
    initial_supply?: string | bigint;
    mint?: 'none' | 'issuer' | 'open';
    /** create in `open` mode only: the most one mint may create. */
    mint_limit?: string | bigint;
    /** transfer, mint, burn, renounce: the token id. */
    token?: string;
    /** transfer, mint: the receiving address. */
    to?: string;
    /** transfer, mint, burn. */
    amount?: string | bigint;
}

/** An unsigned QSDM-20 transaction as prepareQSDM20 returns it. */
export interface QSDM20UnsignedTransaction {
    sender: string;
    /** Decimal string. */
    nonce: string;
    /** The fee in dust (1 CELL = 100,000,000 dust), burned. Decimal string. */
    fee_dust: string;
    payload: QSDM20Action;
}

export interface QSDM20SignedTransaction extends QSDM20UnsignedTransaction {
    public_key: string;
    signature: string;
}

export interface QSDM20SubmitResponse {
    tx_id: string;
    action: string;
    sender: string;
    nonce: number;
    /** The token acted on; for a create, the id the new token gets. */
    token: string;
    status: string;
    /** "submitted", or "duplicate" when the node already had it. */
    mempool_status: string;
}

export interface AccountInfo {
    address: string;
    balance: number;
    /** The account nonce a token transaction carries. */
    nonce: number;
    /** False for an address that never received CELL. */
    present: boolean;
}

export interface QSDM20Token {
    id: string;
    symbol: string;
    name: string;
    decimals: number;
    issuer: string;
    mint: 'none' | 'issuer' | 'open';
    mint_limit?: string;
    max_supply: string;
    minted: string;
    burned: string;
    supply: string;
    holders: number;
    created_height: number;
}

export interface QSDM20TokensPage {
    state_root: string;
    tokens: QSDM20Token[];
    next_cursor?: string;
    has_more: boolean;
}

export interface QSDM20TokenInfo {
    state_root: string;
    token: QSDM20Token;
}

export interface QSDM20HoldersPage {
    state_root: string;
    token: string;
    holders: Array<{ address: string; balance: string }>;
    next_cursor?: string;
    has_more: boolean;
}

export interface QSDM20Balances {
    state_root: string;
    address: string;
    balances: Array<{ token: string; symbol: string; name: string; decimals: number; balance: string }>;
}

export interface SendQSDM20Request {
    sender: string;
    payload: QSDM20Action;
    feeDust?: Uint64Like;
    /** Read from GET /api/v1/mining/account when omitted. */
    nonce?: Uint64Like;
    /** Signs the 32-byte digest with ML-DSA-87 (empty context) using the sender's key. */
    signQSDM20(
        digest: Uint8Array,
        unsigned: QSDM20UnsignedTransaction
    ):
        | { signature: string | Uint8Array; public_key?: string | Uint8Array; publicKey?: string | Uint8Array }
        | Promise<{ signature: string | Uint8Array; public_key?: string | Uint8Array; publicKey?: string | Uint8Array }>;
}

export const QSDM20_CONTRACT_ID: 'qsdm/token/v1';
/** 2^256 - 1 as a decimal string, the largest amount any field may carry. */
export const QSDM20_MAX_UNITS: string;

/**
 * Applies the QSDM-20 field rules and returns the action with only its own
 * fields and amounts as decimal strings. Throws a TypeError otherwise.
 */
export function validateQSDM20Action(action: QSDM20Action): QSDM20Action;
/** The id of the token `creator` creates with its transaction at `nonce`. */
export function qsdm20TokenId(creator: string, nonce: Uint64Like): string;
/** The length-prefixed encoding a QSDM-20 signature covers. */
export function qsdm20SigningPreimage(tx: {
    sender: string;
    nonce: Uint64Like;
    fee_dust?: Uint64Like;
    feeDust?: Uint64Like;
    payload: QSDM20Action;
}): string;
/** The 32 bytes a QSDM-20 transaction's ML-DSA-87 signature covers. */
export function qsdm20SigningDigest(tx: {
    sender: string;
    nonce: Uint64Like;
    fee_dust?: Uint64Like;
    feeDust?: Uint64Like;
    payload: QSDM20Action;
}): Uint8Array;
/** "qsdm20-" plus the hex digest: the id the node gives the transaction. */
export function qsdm20TxId(digest: Uint8Array | string): string;
/** The QSDM wallet address of an ML-DSA-87 public key: the hex SHA-256 of the key. */
export function walletAddress(publicKey: Uint8Array | string): string;

export class QSDMClient {
    readonly baseURL: string;
    constructor(baseURL: string, opts?: ClientOptions);

    /** Operator JWT, sent as Authorization: Bearer. Public routes do not need it. */
    setToken(token: string): void;
    /** Sent as X-API-Key. QSDM nodes do not read this header; kept for gateways that do. */
    setAPIKey(apiKey: string): void;

    getBalance(address: string): Promise<number>;
    getWalletNonce(address: string): Promise<WalletNonceResponse>;

    /**
     * @deprecated Since 0.4.0. Always rejects without contacting the node.
     * `POST /api/v1/wallet/send` is operator-only (operator JWT plus a
     * request signature made with the node's own key), reads
     * `{recipient, amount, fee, geotag, parent_cells}` rather than
     * `{from, to, amount}`, and spends from the node's own wallet. Public
     * nodes accept only ML-DSA-87 signed transfers with
     * Proof-of-Entanglement parents: use {@link sendTransferWithProvider},
     * {@link QSDMClient.sendTransfer} or {@link QSDMClient.submitSignedTransfer}.
     */
    sendTransaction(from: string, to: string, amount: number): Promise<never>;

    /** GET /api/v1/chain/parents (public). `limit` sets the length of `recent`. */
    getChainParents(options?: { limit?: number }): Promise<ChainParentsResponse>;
    /**
     * The two parents for a new transfer: GET /api/v1/chain/parents, falling
     * back to successful receipts only if that route is missing (404/405/501).
     */
    getPoEParents(): Promise<string[]>;
    /** GET /api/v1/receipts/{tx_id} (public); 404 until the transaction is in a block. */
    getReceipt(txID: string): Promise<Receipt>;
    /** GET /api/v1/receipts (public), newest first. */
    getReceipts(query?: { from?: number; to?: number; limit?: number }): Promise<ReceiptsResponse>;
    /** Builds an unsigned envelope, reading the nonce and parents from the node unless given. */
    prepareTransfer(request: TransferRequest): Promise<TransferEnvelope>;
    /** POST /api/v1/wallet/submit-signed (public). Throws {@link TransferError} on refusal. */
    submitSignedTransfer(envelope: SignedTransferEnvelope): Promise<SubmitTransferResponse>;
    /** prepareTransfer + request.signTransfer + submitSignedTransfer, one attempt. */
    sendTransfer(request: SendTransferRequest): Promise<SubmitTransferResponse>;

    /**
     * Retrieve a transaction by ID from `GET /api/v1/transactions/{tx_id}`
     * (plural; fixed in 0.3.1).
     *
     * @deprecated Since 0.4.0. The route is not public: a public node
     * answers 401 without an operator JWT. Use
     * {@link QSDMClient.getReceipt} to follow a transaction.
     */
    getTransaction(txID: string): Promise<Record<string, unknown>>;

    /**
     * @deprecated Since 0.3.1. No QSDM node registers
     * `/api/v1/wallet/transactions`, so the call always fails (401 without
     * credentials, 404 with an operator JWT). Use
     * {@link QSDMClient.getReceipts} and filter, or keep your own index.
     */
    getRecentTransactions(address: string, limit?: number): Promise<unknown[]>;

    getStreamActionNonce(address: string): Promise<StreamActionNonceResponse>;
    getStreams(filters?: {
        payer?: string;
        provider?: string;
        status?: string;
        serviceId?: string;
    }): Promise<StreamsResponse>;
    getStream(streamID: string): Promise<StreamResponse>;
    /**
     * Public route; a node that admits only signed wallet transfers answers
     * 503 (the public pilot pauses stream actions during its recovery).
     */
    submitStreamAction(envelope: StreamActionEnvelope): Promise<StreamActionSubmitResponse>;

    /** GET /api/v1/mining/account (public): consensus CELL balance and account nonce. */
    getAccount(address: string): Promise<AccountInfo>;
    /**
     * Builds an unsigned QSDM-20 transaction, reading the sender's account
     * nonce unless `nonce` is given. The sender needs a CELL account.
     */
    prepareQSDM20(request: {
        sender: string;
        payload: QSDM20Action;
        feeDust?: Uint64Like;
        nonce?: Uint64Like;
    }): Promise<QSDM20UnsignedTransaction>;
    /** POST /api/v1/qsdm20/submit (public). Throws {@link ApiError} on refusal (422 for a broken rule). */
    submitQSDM20(tx: QSDM20SignedTransaction): Promise<QSDM20SubmitResponse>;
    /** prepareQSDM20 + request.signQSDM20 + submitQSDM20, one attempt. */
    sendQSDM20(request: SendQSDM20Request): Promise<QSDM20SubmitResponse>;
    /** GET /api/v1/qsdm20/tokens (public), newest first. */
    getQSDM20Tokens(options?: { cursor?: string; limit?: number }): Promise<QSDM20TokensPage>;
    /** GET /api/v1/qsdm20/tokens/{id} (public). */
    getQSDM20Token(id: string): Promise<QSDM20TokenInfo>;
    /** GET /api/v1/qsdm20/tokens/{id}/holders (public), largest balance first. */
    getQSDM20Holders(id: string, options?: { cursor?: string; limit?: number }): Promise<QSDM20HoldersPage>;
    /** GET /api/v1/qsdm20/balances/{address} (public). */
    getQSDM20Balances(address: string): Promise<QSDM20Balances>;

    getLiveness(): Promise<HealthStatus>;
    getReadiness(): Promise<HealthStatus>;
    getHealth(): Promise<HealthStatus>;
    getNodeStatus(): Promise<NodeStatus>;

    /**
     * @deprecated Since 0.3.1. No QSDM node registers
     * `/api/v1/network/peers`, so the call always fails (401 without
     * credentials, 404 with an operator JWT). Use
     * `getNodeStatus().peers` for the number of connected peers.
     */
    getPeers(): Promise<unknown[]>;

    /**
     * @deprecated Since 0.4.0. `/api/v1/network/topology` is not on the
     * node's public allowlist: a public node answers 401 without an
     * operator JWT. Use `getNodeStatus().peers` for the peer count.
     */
    getNetworkTopology(): Promise<Record<string, unknown>>;

    /**
     * @deprecated Since 0.3.1. `/api/metrics` exists only on the operator
     * dashboard server (login required), not on the node API, so the call
     * always fails against a node. There is no public metrics endpoint.
     */
    getMetricsJSON(): Promise<Record<string, unknown>>;

    /**
     * @deprecated Since 0.3.1. Like {@link QSDMClient.getMetricsJSON},
     * `/api/metrics/prometheus` exists only on the operator dashboard
     * server (scrape token or login required).
     */
    getMetricsPrometheus(): Promise<string>;
}

export default QSDMClient;
