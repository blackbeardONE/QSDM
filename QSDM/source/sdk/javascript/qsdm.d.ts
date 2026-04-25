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
    getTransaction(txID: string): Promise<Record<string, unknown>>;
    getRecentTransactions(address: string, limit?: number): Promise<unknown[]>;

    getLiveness(): Promise<HealthStatus>;
    getReadiness(): Promise<HealthStatus>;
    getHealth(): Promise<HealthStatus>;
    getNodeStatus(): Promise<NodeStatus>;

    getPeers(): Promise<unknown[]>;
    getNetworkTopology(): Promise<Record<string, unknown>>;

    getMetricsJSON(): Promise<Record<string, unknown>>;
    getMetricsPrometheus(): Promise<string>;
}

export default QSDMClient;
