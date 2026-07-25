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
    nonce: number;
    next: number;
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

export function isNotFound(err: unknown): boolean;
export function isUnauthorized(err: unknown): boolean;

export class QSDMClient {
    readonly baseURL: string;
    constructor(baseURL: string, opts?: ClientOptions);

    setToken(token: string): void;
    setAPIKey(apiKey: string): void;

    getBalance(address: string): Promise<number>;
    getWalletNonce(address: string): Promise<WalletNonceResponse>;
    sendTransaction(from: string, to: string, amount: number): Promise<string>;

    /**
     * Retrieve a transaction by ID.
     *
     * Endpoint: `GET /api/v1/transactions/{tx_id}` (plural; fixed in
     * 0.3.1). Earlier SDK builds (≤0.3.0) called the singular form
     * which returns 404 in production.
     */
    getTransaction(txID: string): Promise<Record<string, unknown>>;

    /**
     * @deprecated Since 0.3.1. `/api/v1/wallet/transactions` is not
     * registered on the public `pkg/api` server. There is no
     * per-address recent-transactions endpoint on the public surface
     * today; use `GET /api/v1/receipts` (paginated chain transparency
     * feed) and filter client-side, or maintain an off-chain index.
     * Production calls return `ApiError` with `status: 404`. Pending
     * removal in 0.4.0.
     */
    getRecentTransactions(address: string, limit?: number): Promise<unknown[]>;

    getStreams(filters?: {
        payer?: string;
        provider?: string;
        status?: string;
        serviceId?: string;
    }): Promise<StreamsResponse>;
    getStream(streamID: string): Promise<StreamResponse>;
    submitStreamAction(envelope: StreamActionEnvelope): Promise<StreamActionSubmitResponse>;

    getLiveness(): Promise<HealthStatus>;
    getReadiness(): Promise<HealthStatus>;
    getHealth(): Promise<HealthStatus>;
    getNodeStatus(): Promise<NodeStatus>;

    /**
     * @deprecated Since 0.3.1. `/api/v1/network/peers` is not
     * registered on the public `pkg/api` server. Closest analogues
     * are `/api/admin/peers` (admin-only, mTLS-required) and the
     * dashboard's `/api/topology`; neither is reachable from a
     * JWT-bearer SDK client. Use {@link QSDMClient.getNetworkTopology}
     * for the same data instead. Production calls return `ApiError`
     * with `status: 404`. Pending removal in 0.4.0.
     */
    getPeers(): Promise<unknown[]>;

    getNetworkTopology(): Promise<Record<string, unknown>>;

    /**
     * @deprecated Since 0.3.1. `/api/metrics` is registered only on
     * the operator dashboard server (`requireAuth`-gated), not on
     * the public `pkg/api` server the SDK targets. Production calls
     * against a `pkg/api` node return `ApiError` with `status: 404`.
     * Pending removal in 0.4.0.
     */
    getMetricsJSON(): Promise<Record<string, unknown>>;

    /**
     * @deprecated Since 0.3.1. See {@link QSDMClient.getMetricsJSON}
     * — same dashboard-vs-public-API mismatch. Production calls
     * against a `pkg/api` node return `ApiError` with `status: 404`.
     * Pending removal in 0.4.0.
     */
    getMetricsPrometheus(): Promise<string>;
}

export default QSDMClient;
