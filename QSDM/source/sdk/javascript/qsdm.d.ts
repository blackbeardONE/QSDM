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
