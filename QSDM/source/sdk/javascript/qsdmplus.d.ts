// Type declarations for the QSDM+ JavaScript SDK.
// Mirrors the Go SDK surface under sdk/go/qsdmplus.go.

export interface CoinInfo {
    name: string;
    symbol: string;
    decimals: number;
    smallestUnit: string;
}

export interface BrandInfo {
    name: string;
    legacyName?: string;
    fullTitle?: string;
}

export interface TokenomicsInfo {
    capDust: number;
    capCell: string;
    emittedDust: number;
    emittedCell: string;
    remainingDust: number;
    blockRewardDust: number;
    blockRewardCell: string;
    currentEpoch: number;
    nextHalvingHeight: number;
    nextHalvingEtaSeconds: number;
    targetBlockTimeSeconds: number;
    blocksPerEpoch: number;
}

export interface NodeStatus {
    nodeId?: string;
    version?: string;
    uptime?: string;
    chainTip?: number;
    peers?: number;
    nodeRole?: string;
    network?: string;
    coin?: CoinInfo;
    branding?: BrandInfo;
    tokenomics?: TokenomicsInfo;
    extra: Record<string, unknown>;
}

export interface HealthStatus {
    status: string;
    timestamp?: string;
    version?: string;
}

export interface ClientOptions {
    /** Override fetch (useful for Node < 18 or testing). */
    fetch?: typeof fetch;
    /** Per-request timeout in ms (0 = disabled). Default 30_000. */
    timeoutMs?: number;
}

export class ApiError extends Error {
    status: number;
    url: string;
    body: string;
    constructor(status: number, url: string, body: string);
}

export function isNotFound(err: unknown): boolean;
export function isUnauthorized(err: unknown): boolean;

export class QSDMPlusClient {
    constructor(baseURL: string, opts?: ClientOptions);

    baseURL: string;
    token: string | null;
    apiKey: string | null;
    timeoutMs: number;

    setToken(token: string): void;
    setAPIKey(apiKey: string): void;

    getBalance(address: string): Promise<number>;
    sendTransaction(from: string, to: string, amount: number): Promise<string>;
    getTransaction(txID: string): Promise<Record<string, unknown>>;
    getRecentTransactions(address: string, limit?: number): Promise<Record<string, unknown>>;

    getLiveness(): Promise<HealthStatus>;
    getReadiness(): Promise<HealthStatus>;
    getHealth(): Promise<HealthStatus>;

    getNodeStatus(): Promise<NodeStatus>;
    getPeers(): Promise<Record<string, unknown>[]>;
    getNetworkTopology(): Promise<Record<string, unknown>>;

    getMetricsJSON(): Promise<Record<string, unknown>>;
    getMetricsPrometheus(): Promise<string>;
}

export default QSDMPlusClient;
