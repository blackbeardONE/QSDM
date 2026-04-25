/**
 * QSDM JavaScript SDK — feature parity with sdk/go.
 *
 * Works in browsers and Node.js (18+ which ships fetch globally, or any Node + a
 * fetch polyfill). Exposes the same surface as the Go client:
 *
 *   const { QSDMClient, ApiError, isNotFound, isUnauthorized } = require('qsdm');
 *   const c = new QSDMClient('http://node:8080');
 *   c.setToken(jwt);
 *   const balance = await c.getBalance('addr');
 *
 * The Prometheus-text call returns a raw string; all other calls return parsed JSON.
 *
 * Native coin: Cell (CELL), 8 decimals, smallest unit "dust".
 */

class ApiError extends Error {
    constructor(status, url, bodyText) {
        super(`qsdm: ${url} returned ${status}: ${truncate(bodyText, 256)}`);
        this.name = 'ApiError';
        this.status = status;
        this.url = url;
        this.body = bodyText;
    }
}

function truncate(s, n) {
    if (!s) return '';
    return s.length <= n ? s : s.slice(0, n) + '…';
}

function isNotFound(err) {
    return err instanceof ApiError && err.status === 404;
}

function isUnauthorized(err) {
    return err instanceof ApiError && (err.status === 401 || err.status === 403);
}

class QSDMClient {
    /**
     * @param {string} baseURL — e.g. http://localhost:8080
     * @param {{ fetch?: typeof fetch, timeoutMs?: number }} [opts]
     */
    constructor(baseURL, opts = {}) {
        if (typeof baseURL !== 'string' || baseURL.length === 0) {
            throw new Error('QSDMClient: baseURL is required');
        }
        this.baseURL = baseURL.replace(/\/+$/, '');
        this.token = null;
        this.apiKey = null;
        this._fetch = opts.fetch || (typeof fetch !== 'undefined' ? fetch : null);
        if (!this._fetch) {
            throw new Error('QSDMClient: fetch is not available; pass opts.fetch');
        }
        this.timeoutMs = typeof opts.timeoutMs === 'number' ? opts.timeoutMs : 30000;
    }

    setToken(token) { this.token = token; }
    setAPIKey(apiKey) { this.apiKey = apiKey; }

    // --- wallet + tx ---

    async getBalance(address) {
        const q = encodeURIComponent(address);
        const out = await this._request('GET', `/api/v1/wallet/balance?address=${q}`);
        return out.balance;
    }

    async sendTransaction(from, to, amount) {
        const out = await this._request('POST', '/api/v1/wallet/send', { from, to, amount });
        return out.transaction_id;
    }

    async getTransaction(txID) {
        return this._request('GET', `/api/v1/transaction/${encodeURIComponent(txID)}`);
    }

    async getRecentTransactions(address, limit = 10) {
        const q = encodeURIComponent(address);
        return this._request('GET', `/api/v1/wallet/transactions?address=${q}&limit=${limit}`);
    }

    // --- health + node + network ---

    async getLiveness()   { return this._request('GET', '/api/v1/health/live'); }
    async getReadiness()  { return this._request('GET', '/api/v1/health/ready'); }
    async getHealth()     { return this._request('GET', '/api/v1/health'); }

    async getNodeStatus() {
        const raw = await this._request('GET', '/api/v1/status');
        return {
            nodeId:   raw.node_id,
            version:  raw.version,
            uptime:   raw.uptime,
            chainTip: typeof raw.chain_tip === 'number' ? raw.chain_tip : undefined,
            peers:    typeof raw.peers === 'number' ? raw.peers : undefined,
            nodeRole:   typeof raw.node_role === 'string' ? raw.node_role : undefined,
            network:    typeof raw.network === 'string' ? raw.network : undefined,
            coin:       raw.coin && typeof raw.coin === 'object' ? {
                name:         raw.coin.name,
                symbol:       raw.coin.symbol,
                decimals:     raw.coin.decimals,
                smallestUnit: raw.coin.smallest_unit,
            } : undefined,
            branding:   raw.branding && typeof raw.branding === 'object' ? {
                name:       raw.branding.name,
                fullTitle:  raw.branding.full_title,
            } : undefined,
            tokenomics: raw.tokenomics && typeof raw.tokenomics === 'object' ? {
                capDust:                raw.tokenomics.cap_dust,
                capCell:                raw.tokenomics.cap_cell,
                emittedDust:            raw.tokenomics.emitted_dust,
                emittedCell:            raw.tokenomics.emitted_cell,
                remainingDust:          raw.tokenomics.remaining_dust,
                blockRewardDust:        raw.tokenomics.block_reward_dust,
                blockRewardCell:        raw.tokenomics.block_reward_cell,
                currentEpoch:           raw.tokenomics.current_epoch,
                nextHalvingHeight:      raw.tokenomics.next_halving_height,
                nextHalvingEtaSeconds:  raw.tokenomics.next_halving_eta_seconds,
                targetBlockTimeSeconds: raw.tokenomics.target_block_time_seconds,
                blocksPerEpoch:         raw.tokenomics.blocks_per_epoch,
            } : undefined,
            extra: raw,
        };
    }

    async getPeers() {
        const out = await this._request('GET', '/api/v1/network/peers');
        return Array.isArray(out.peers) ? out.peers : [];
    }

    async getNetworkTopology() {
        return this._request('GET', '/api/v1/network/topology');
    }

    // --- metrics ---

    async getMetricsJSON()       { return this._request('GET', '/api/metrics'); }
    async getMetricsPrometheus() { return this._requestText('GET', '/api/metrics/prometheus'); }

    // --- internals ---

    _authHeaders() {
        const h = {};
        if (this.token) {
            h['Authorization'] = `Bearer ${this.token}`;
        } else if (this.apiKey) {
            h['X-API-Key'] = this.apiKey;
        }
        return h;
    }

    async _request(method, path, body) {
        const { response, text, url } = await this._send(method, path, body, 'application/json');
        if (!response.ok) {
            throw new ApiError(response.status, url, text);
        }
        if (!text) return null;
        try {
            return JSON.parse(text);
        } catch (e) {
            throw new Error(`qsdm: failed to decode JSON from ${url}: ${e.message}`);
        }
    }

    async _requestText(method, path) {
        const { response, text, url } = await this._send(method, path, null, 'text/plain');
        if (!response.ok) {
            throw new ApiError(response.status, url, text);
        }
        return text;
    }

    async _send(method, path, body, accept) {
        const url = this.baseURL + path;
        const headers = { 'Accept': accept, ...this._authHeaders() };
        const init = { method, headers };
        if (body !== null && body !== undefined) {
            headers['Content-Type'] = 'application/json';
            init.body = JSON.stringify(body);
        }

        let timer = null;
        if (typeof AbortController !== 'undefined' && this.timeoutMs > 0) {
            const ctrl = new AbortController();
            init.signal = ctrl.signal;
            timer = setTimeout(() => ctrl.abort(), this.timeoutMs);
        }

        try {
            const response = await this._fetch(url, init);
            const text = await response.text();
            return { response, text, url };
        } finally {
            if (timer) clearTimeout(timer);
        }
    }
}

if (typeof module !== 'undefined' && module.exports) {
    module.exports = QSDMClient;
    module.exports.QSDMClient = QSDMClient;
    module.exports.ApiError = ApiError;
    module.exports.isNotFound = isNotFound;
    module.exports.isUnauthorized = isUnauthorized;
}
