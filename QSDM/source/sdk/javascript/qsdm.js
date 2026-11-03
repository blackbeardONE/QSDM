/**
 * QSDM JavaScript SDK — feature parity with sdk/go.
 *
 * Works in browsers and Node.js (18+ which ships fetch globally, or any Node + a
 * fetch polyfill). Exposes the same surface as the Go client:
 *
 *   const { QSDMClient, ApiError, isNotFound, isUnauthorized } = require('qsdm-sdk');
 *   const c = new QSDMClient('https://api.qsdm.tech');
 *   const balance = await c.getBalance('addr');
 *
 * Sending CELL: nodes accept a transfer from a client only as an envelope
 * signed with ML-DSA-87, and since block 816,600 it must name
 * Proof-of-Entanglement parents (HTTP 422 otherwise). The SDK never holds a
 * private key. In a browser with the QSDM Wallet extension, use
 * sendTransferWithProvider(); with your own signer, use
 * client.sendTransfer({ ..., signTransfer }); an envelope signed elsewhere
 * (qsdmcli wallet sign-tx) goes to client.submitSignedTransfer().
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

// --- signed transfers: shared rules ---------------------------------------

// Proof-of-Entanglement limits; they mirror pkg/poe in the node.
const POE_MIN_PARENTS = 2;
// ML-DSA-87 sizes (FIPS 204). The node refuses other sizes.
const MLDSA87_PUBLIC_KEY_BYTES = 2592;
const MLDSA87_SIGNATURE_BYTES = 4627;

const WALLET_ADDRESS_PATTERN = /^[0-9a-f]{64}$/;
const PARENT_ID_PATTERN = /^[0-9A-Za-z_-]{16,128}$/;
const LONE_SURROGATE_PATTERN = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/;
// Go's encoding/json escapes <, >, &, U+2028 and U+2029 inside strings as
// a backslash, "u" and four hex digits; JSON.stringify leaves them as they
// are. (Built with fromCharCode so the separators never appear raw here.)
const GO_ESCAPED_CHARACTERS = new RegExp('[<>&' + String.fromCharCode(0x2028, 0x2029) + ']', 'g');
const BACKSLASH = String.fromCharCode(92);

const UNSIGNED_SEND_REMOVED =
    'qsdm: sendTransaction() is no longer supported: QSDM nodes accept CELL transfers ' +
    'from clients only as ML-DSA-87 signed envelopes with Proof-of-Entanglement parents ' +
    '(POST /api/v1/wallet/submit-signed). Use sendTransferWithProvider() with the QSDM ' +
    'Wallet extension, client.sendTransfer({ ..., signTransfer }) with your own signer, or ' +
    'client.submitSignedTransfer() for an envelope signed elsewhere. ' +
    "POST /api/v1/wallet/send is an operator-only route that spends from the node's own wallet.";

const TRANSFER_HINTS = {
    poe_parents: 'fetch fresh parents (getPoEParents, GET /api/v1/chain/parents) and sign the transfer again at the same nonce',
    signature: 'sign transferSigningBytes() of exactly the envelope you submit, with ML-DSA-87 and an empty context',
    nonce: 'read GET /api/v1/wallet/nonce again and sign with its next value',
    duplicate: 'wait for the next block, then follow the transfer with getReceipt() or prepare a new one',
    insufficient_balance: "the sender's CELL balance is below amount + fee",
    unavailable: 'the node is not accepting transfers right now; retry later',
    rate_limited: 'wait before submitting again',
    unauthorized: 'this node does not serve the public submit route',
};

function parseJSONObject(text) {
    if (typeof text !== 'string' || !text) return null;
    try {
        const value = JSON.parse(text);
        return value && typeof value === 'object' ? value : null;
    } catch (_) {
        return null;
    }
}

function classifyTransferRejection(status, message, duplicate) {
    const lower = message.toLowerCase();
    if (status === 400) return 'invalid';
    if (status === 401 || status === 403) return 'unauthorized';
    if (status === 402) return 'insufficient_balance';
    if (status === 409) return duplicate ? 'duplicate' : 'nonce';
    if (status === 422) {
        if (lower.includes('proof-of-entanglement')) return 'poe_parents';
        if (lower.includes('signature')) return 'signature';
        return 'policy';
    }
    if (status === 429) return 'rate_limited';
    if (status === 501 || status === 503) return 'unavailable';
    return 'other';
}

/**
 * A node's refusal of a signed transfer (POST /api/v1/wallet/submit-signed).
 * `reason` is one of: invalid (400), unauthorized (401/403),
 * insufficient_balance (402), duplicate (409, status "duplicate"),
 * nonce (409), poe_parents (422 "proof-of-entanglement: ..."),
 * signature (422), policy (other 422), rate_limited (429),
 * unavailable (501/503) or other.
 */
class TransferError extends ApiError {
    constructor(status, url, bodyText) {
        super(status, url, bodyText);
        this.name = 'TransferError';
        // Two JSON shapes: errors are {error, message, status: <code>}, a
        // duplicate is {transaction_id, status: "duplicate", broadcast}.
        const parsed = parseJSONObject(bodyText);
        let serverMessage = typeof bodyText === 'string' ? bodyText.trim() : '';
        if (parsed && typeof parsed.message === 'string' && parsed.message) {
            serverMessage = parsed.message;
        }
        this.transactionId = parsed && typeof parsed.transaction_id === 'string'
            ? parsed.transaction_id
            : undefined;
        this.reason = classifyTransferRejection(
            status,
            serverMessage,
            Boolean(parsed && parsed.status === 'duplicate')
        );
        if (this.reason === 'duplicate') {
            serverMessage = 'the node already has a transfer with this ID, or another transfer ' +
                'with this sender nonce is waiting for the next block';
        }
        this.serverMessage = serverMessage;
        const hint = TRANSFER_HINTS[this.reason];
        this.message = `qsdm: transfer refused by ${url} (HTTP ${status}, ${this.reason})` +
            (serverMessage ? `: ${truncate(serverMessage, 256)}` : '') +
            (hint ? ` -- ${hint}` : '');
    }
}

/**
 * True when a transfer was refused because its Proof-of-Entanglement
 * parents are missing, malformed or stale (HTTP 422). Prepare the transfer
 * again (fresh parents) and sign it again.
 */
function isPoERejection(err) {
    return err instanceof TransferError && err.reason === 'poe_parents';
}

function requireWalletAddress(value, name) {
    if (typeof value !== 'string' || !WALLET_ADDRESS_PATTERN.test(value)) {
        throw new TypeError(`qsdm: transfer ${name} must be a 64-character lowercase hex wallet address`);
    }
    return value;
}

// The chain moves whole dust (1 dust = 0.00000001 CELL) and drops further digits.
function hasAtMost8Decimals(value) {
    return Number(value.toFixed(8)) === value;
}

function requireCellAmount(value, name, allowZero) {
    if (typeof value !== 'number' || !Number.isFinite(value) ||
        value < 0 || (!allowZero && value === 0)) {
        throw new TypeError(`qsdm: transfer ${name} must be ${allowZero ? 'zero or ' : ''}a positive number of CELL`);
    }
    if (!hasAtMost8Decimals(value)) {
        throw new TypeError(`qsdm: transfer ${name} ${value} must have at most 8 decimal places`);
    }
    return value;
}

function requireEnvelopeString(value, name) {
    if (typeof value !== 'string') {
        throw new TypeError(`qsdm: transfer ${name} must be a string`);
    }
    if (LONE_SURROGATE_PATTERN.test(value)) {
        throw new TypeError(`qsdm: transfer ${name} is not valid Unicode text`);
    }
    return value;
}

function canonicalTransfer(envelope) {
    if (!envelope || typeof envelope !== 'object') {
        throw new TypeError('qsdm: a transfer envelope object is required');
    }
    const amount = envelope.amount;
    const fee = envelope.fee === undefined ? 0 : envelope.fee;
    for (const [name, value] of [['amount', amount], ['fee', fee]]) {
        if (typeof value !== 'number' || !Number.isFinite(value)) {
            throw new TypeError(`qsdm: transfer ${name} must be a finite number`);
        }
    }
    let parents = null;
    if (envelope.parent_cells !== undefined && envelope.parent_cells !== null) {
        if (!Array.isArray(envelope.parent_cells)) {
            throw new TypeError('qsdm: transfer parent_cells must be an array of strings');
        }
        parents = envelope.parent_cells.map((parent, i) => requireEnvelopeString(parent, `parent ${i}`));
    }
    // Field order is the signature contract: pkg/wallet.TransactionData.
    const out = {
        id: requireEnvelopeString(envelope.id, 'id'),
        sender: requireEnvelopeString(envelope.sender, 'sender'),
        recipient: requireEnvelopeString(envelope.recipient, 'recipient'),
        amount,
        fee,
        geotag: requireEnvelopeString(envelope.geotag === undefined ? '' : envelope.geotag, 'geotag'),
        parent_cells: parents,
    };
    const nonce = envelope.nonce === undefined || envelope.nonce === null ? 0 : envelope.nonce;
    if (!Number.isSafeInteger(nonce) || nonce < 0) {
        throw new TypeError('qsdm: transfer nonce must be a non-negative safe integer');
    }
    if (nonce !== 0) out.nonce = nonce; // omitempty in the node
    out.signature = '';
    out.timestamp = requireEnvelopeString(envelope.timestamp, 'timestamp');
    return out;
}

/**
 * The exact bytes an ML-DSA-87 transfer signature covers: the envelope with
 * `signature` set to "" and `public_key` removed, encoded as the node's Go
 * encoding/json encodes it (field order of pkg/wallet.TransactionData; <, >,
 * &, U+2028 and U+2029 escaped; numbers as JSON.stringify writes them, which
 * matches Go). Sign these bytes with an empty ML-DSA context string.
 *
 * @param {object} envelope
 * @returns {Uint8Array}
 */
function transferSigningBytes(envelope) {
    const json = JSON.stringify(canonicalTransfer(envelope)).replace(
        GO_ESCAPED_CHARACTERS,
        (c) => BACKSLASH + 'u' + c.charCodeAt(0).toString(16).padStart(4, '0')
    );
    return encodeUTF8(json);
}

function encodeUTF8(value) {
    if (typeof TextEncoder === 'undefined') {
        throw new Error('qsdm: TextEncoder is required');
    }
    return new TextEncoder().encode(value);
}

function bytesEqual(a, b) {
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i += 1) {
        if (a[i] !== b[i]) return false;
    }
    return true;
}

function toHex(value, expectedBytes, name) {
    if (value instanceof Uint8Array) {
        if (value.length !== expectedBytes) {
            throw new TypeError(`qsdm: ${name} is ${value.length} bytes, want ${expectedBytes} (ML-DSA-87)`);
        }
        return Array.from(value, (byte) => byte.toString(16).padStart(2, '0')).join('');
    }
    if (typeof value !== 'string' || !/^[0-9a-fA-F]*$/.test(value) ||
        value.length !== expectedBytes * 2) {
        throw new TypeError(`qsdm: ${name} must be ${expectedBytes} bytes (ML-DSA-87) as hex or a Uint8Array`);
    }
    return value.toLowerCase();
}

// Accepts {signature, public_key|publicKey} or a full signed envelope (an
// object, or its JSON as qsdmcli and the web wallet's WASM signer print it).
function attachTransferSignature(unsigned, result) {
    let signed = result;
    if (typeof signed === 'string') {
        signed = parseJSONObject(signed);
    }
    if (!signed || typeof signed !== 'object') {
        throw new TypeError('qsdm: signTransfer must return { signature, public_key } or a signed envelope');
    }
    const signature = toHex(signed.signature, MLDSA87_SIGNATURE_BYTES, 'signature');
    const publicKey = toHex(
        signed.public_key !== undefined ? signed.public_key : signed.publicKey,
        MLDSA87_PUBLIC_KEY_BYTES,
        'public_key'
    );
    if (signed.id !== undefined &&
        !bytesEqual(transferSigningBytes(signed), transferSigningBytes(unsigned))) {
        throw new Error('qsdm: the signer returned an envelope that differs from the prepared transfer; not submitting it');
    }
    return { ...unsigned, signature, public_key: publicKey };
}

function newTransferID() {
    const cryptoObject = typeof globalThis !== 'undefined' ? globalThis.crypto : undefined;
    if (!cryptoObject || typeof cryptoObject.getRandomValues !== 'function') {
        throw new Error('qsdm: secure randomness is unavailable; pass an id to prepareTransfer');
    }
    const random = new Uint8Array(8);
    cryptoObject.getRandomValues(random);
    const hex = Array.from(random, (byte) => byte.toString(16).padStart(2, '0')).join('');
    return `sdk_js_${Date.now()}_${hex}`;
}

function pickPoEParents(ids) {
    const picked = [];
    for (const id of ids) {
        if (picked.length === POE_MIN_PARENTS) break;
        if (typeof id === 'string' && PARENT_ID_PATTERN.test(id) && !picked.includes(id)) {
            picked.push(id);
        }
    }
    if (picked.length !== POE_MIN_PARENTS) {
        throw new Error('qsdm: node offered no usable Proof-of-Entanglement parents');
    }
    return picked;
}

// --- QSDM Wallet extension (window.qsdm) -----------------------------------

/**
 * The provider the QSDM Wallet extension injects as `window.qsdm`, or null.
 * @param {object} [scope] defaults to globalThis
 */
function getWalletProvider(scope) {
    const target = scope !== undefined ? scope : (typeof globalThis !== 'undefined' ? globalThis : undefined);
    const candidate = target ? target.qsdm : undefined;
    return candidate && candidate.isQsdmHive === true && typeof candidate.request === 'function'
        ? candidate
        : null;
}

/**
 * Resolves with the extension's provider once it is injected (the extension
 * fires `qsdm#initialized`), or with null after `timeoutMs` (default 3000).
 * @param {{ timeoutMs?: number, scope?: object }} [options]
 */
function waitForWalletProvider(options = {}) {
    const scope = options.scope !== undefined
        ? options.scope
        : (typeof globalThis !== 'undefined' ? globalThis : undefined);
    const timeoutMs = typeof options.timeoutMs === 'number' ? options.timeoutMs : 3000;
    const found = getWalletProvider(scope);
    if (found || !scope || typeof scope.addEventListener !== 'function') {
        return Promise.resolve(found);
    }
    return new Promise((resolve) => {
        let timer = null;
        const finish = () => {
            if (timer !== null) clearTimeout(timer);
            scope.removeEventListener('qsdm#initialized', finish);
            resolve(getWalletProvider(scope));
        };
        scope.addEventListener('qsdm#initialized', finish);
        timer = setTimeout(finish, timeoutMs);
    });
}

/**
 * Sends CELL through the QSDM Wallet extension. QSDM Hive shows the
 * recipient and amount, and after the user approves it builds the envelope
 * (nonce, Proof-of-Entanglement parents, fee 0), signs it with the wallet's
 * ML-DSA-87 key and submits it to the QSDM node Hive is configured for. The
 * key never leaves Hive and the website cannot choose the fee, nonce or
 * parents.
 *
 * Unless `options.connect` is false it first calls `qsdm_requestAccounts`,
 * which asks the user to connect the site only if it is not connected yet;
 * the extension refuses transfers from a site that is not connected.
 *
 * @param {object} provider  `window.qsdm` (see getWalletProvider)
 * @param {{ recipient: string, amount: number }} transfer
 * @param {{ connect?: boolean }} [options]
 * @returns {Promise<{ transaction_id: string, status: string, broadcast?: string, sender?: string }>}
 */
async function sendTransferWithProvider(provider, transfer = {}, options = {}) {
    if (!provider || typeof provider.request !== 'function') {
        throw new TypeError('qsdm: a QSDM wallet provider (window.qsdm from the QSDM Wallet extension) is required');
    }
    const recipient = requireWalletAddress(transfer.recipient, 'recipient');
    const amount = requireCellAmount(transfer.amount, 'amount', false);
    let sender;
    if (options.connect !== false) {
        const accounts = await provider.request({ method: 'qsdm_requestAccounts' });
        sender = Array.isArray(accounts) && typeof accounts[0] === 'string' ? accounts[0] : undefined;
    }
    const result = await provider.request({
        method: 'qsdm_sendTransaction',
        params: { recipient, amount },
    });
    const out = result && typeof result === 'object' ? { ...result } : {};
    if (sender) out.sender = sender;
    return out;
}

// ---------------------------------------------------------------------------

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

    /** Operator JWT, sent as Authorization: Bearer. Public routes do not need it. */
    setToken(token) { this.token = token; }

    /**
     * Sent as X-API-Key when no token is set. QSDM nodes do not read this
     * header (they authenticate operators only with Authorization: Bearer);
     * it is kept for gateways that do.
     */
    setAPIKey(apiKey) { this.apiKey = apiKey; }

    // --- wallet + tx ---

    /** CELL balance of an address (GET /api/v1/wallet/balance, public). */
    async getBalance(address) {
        const q = encodeURIComponent(address);
        const out = await this._request('GET', `/api/v1/wallet/balance?address=${q}`);
        return out.balance;
    }

    /**
     * Last used and next nonce of a wallet (GET /api/v1/wallet/nonce?sender=,
     * public). Sign the next transfer with `next`. The node counts only
     * committed transfers, so a transfer still waiting for its block holds
     * `next` until that block is sealed.
     */
    async getWalletNonce(address) {
        const q = encodeURIComponent(address);
        return this._request('GET', `/api/v1/wallet/nonce?sender=${q}`);
    }

    /**
     * @deprecated 0.4.0. Always rejects without contacting the node. It
     * posted an unsigned {from, to, amount} body to POST /api/v1/wallet/send,
     * which no public client can use: that route is not on the node's
     * public allowlist (it needs an operator JWT plus X-Timestamp/X-Nonce/
     * X-Signature headers signed with the node's own request-signing key),
     * read-only followers refuse it with 503, it reads {recipient, amount,
     * fee, geotag, parent_cells} (so this body never matched it), and it
     * spends from the node's own wallet, never from `from`. Since 2026-10-16
     * the chain accepts only ML-DSA-87 signed transfers, and since block
     * 816,600 they must carry Proof-of-Entanglement parents. Use
     * sendTransferWithProvider(), sendTransfer() or submitSignedTransfer().
     */
    async sendTransaction(from, to, amount) {
        const err = new Error(UNSIGNED_SEND_REMOVED);
        err.code = 'QSDM_UNSIGNED_SEND_REMOVED';
        throw err;
    }

    // --- signed transfers ---

    /**
     * The node's Proof-of-Entanglement parent source
     * (GET /api/v1/chain/parents, public). `limit` sets the length of
     * `recent` (node default 8, maximum 32). The node answers 503 until it
     * has two committed transactions.
     * @param {{ limit?: number }} [options]
     */
    async getChainParents(options = {}) {
        const query = Number.isInteger(options.limit) && options.limit > 0 ? `?limit=${options.limit}` : '';
        return this._request('GET', `/api/v1/chain/parents${query}`);
    }

    /**
     * The parents to put in a new transfer: the node's two newest committed
     * transaction IDs. Same rule as qsdmcli (--auto-parents) and QSDM Hive:
     * GET /api/v1/chain/parents, and only if that route is missing (404, 405
     * or 501 on a node released before it) the newest successful receipts of
     * GET /api/v1/receipts. Any other error is thrown.
     * @returns {Promise<string[]>}
     */
    async getPoEParents() {
        let response;
        try {
            response = await this.getChainParents();
        } catch (err) {
            if (!(err instanceof ApiError) || ![404, 405, 501].includes(err.status)) {
                throw err;
            }
            const list = await this.getReceipts({ limit: 32 });
            const receipts = list && Array.isArray(list.receipts) ? list.receipts : [];
            return pickPoEParents(
                receipts.filter((receipt) => receipt && receipt.status === 1).map((receipt) => receipt.tx_id)
            );
        }
        return pickPoEParents(response && Array.isArray(response.parents) ? response.parents : []);
    }

    /**
     * Receipt of one committed transaction (GET /api/v1/receipts/{tx_id},
     * public). Use it to follow a submitted transfer: the node answers 404
     * (see isNotFound) until the transfer is in a block. `status` is 1 when
     * the transaction was applied and 0 when it failed.
     */
    async getReceipt(txID) {
        return this._request('GET', `/api/v1/receipts/${encodeURIComponent(txID)}`);
    }

    /**
     * Recent receipts, newest first (GET /api/v1/receipts, public). Omitted
     * fields let the node choose: `to` defaults to the tip, `limit` to 20
     * (the node caps it at 200) and `from` to to-limit+1.
     * @param {{ from?: number, to?: number, limit?: number }} [query]
     */
    async getReceipts(query = {}) {
        const pairs = [];
        for (const key of ['from', 'to', 'limit']) {
            if (Number.isInteger(query[key]) && query[key] >= 0) pairs.push(`${key}=${query[key]}`);
        }
        return this._request('GET', `/api/v1/receipts${pairs.length > 0 ? `?${pairs.join('&')}` : ''}`);
    }

    /**
     * Builds an unsigned transfer envelope. Reads `next` from
     * GET /api/v1/wallet/nonce and the parents from getPoEParents() unless
     * `nonce` / `parentCells` are given (an explicit empty `parentCells` is
     * kept, only for nodes that do not enforce Proof-of-Entanglement), and
     * fills `id` and `timestamp` when they are missing.
     *
     * Parents expire (they must be committed in the last 8,640 blocks, about
     * 24 hours), so prepare, sign and submit together.
     *
     * @param {{ sender: string, recipient: string, amount: number, fee?: number,
     *           geotag?: string, id?: string, timestamp?: string, nonce?: number,
     *           parentCells?: string[] }} request
     */
    async prepareTransfer(request = {}) {
        const sender = requireWalletAddress(request.sender, 'sender');
        const recipient = requireWalletAddress(request.recipient, 'recipient');
        const amount = requireCellAmount(request.amount, 'amount', false);
        const fee = requireCellAmount(request.fee === undefined ? 0 : request.fee, 'fee', true);
        const envelope = {
            id: request.id === undefined ? newTransferID() : requireEnvelopeString(request.id, 'id'),
            sender,
            recipient,
            amount,
            fee,
            geotag: requireEnvelopeString(request.geotag === undefined ? '' : request.geotag, 'geotag'),
            parent_cells: null,
            nonce: 0,
            timestamp: request.timestamp === undefined
                ? new Date().toISOString()
                : requireEnvelopeString(request.timestamp, 'timestamp'),
        };
        if (request.nonce === undefined || request.nonce === null || request.nonce === 0) {
            const nonce = await this.getWalletNonce(sender);
            // Fail closed on a node that answers for another wallet or with
            // an inconsistent pair, as qsdmcli does.
            if (!nonce || nonce.sender !== sender ||
                !Number.isSafeInteger(nonce.next) || nonce.next !== nonce.nonce + 1) {
                throw new Error(`qsdm: node returned an inconsistent nonce: ${JSON.stringify(nonce)}`);
            }
            envelope.nonce = nonce.next;
        } else if (Number.isSafeInteger(request.nonce) && request.nonce > 0) {
            envelope.nonce = request.nonce;
        } else {
            throw new TypeError('qsdm: transfer nonce must be a positive safe integer');
        }
        if (request.parentCells === undefined || request.parentCells === null) {
            envelope.parent_cells = await this.getPoEParents();
        } else if (Array.isArray(request.parentCells)) {
            envelope.parent_cells = request.parentCells.map((parent, i) => requireEnvelopeString(parent, `parent ${i}`));
        } else {
            throw new TypeError('qsdm: parentCells must be an array of strings');
        }
        return envelope;
    }

    /**
     * Posts a signed envelope to POST /api/v1/wallet/submit-signed. The
     * route is public: the ML-DSA-87 signature is the authorization.
     * Resolves with { transaction_id, status, broadcast }: status "pending"
     * (HTTP 202, queued for the next block) or "accepted" (HTTP 200, applied
     * by a node that settles transfers itself). A refusal throws a
     * TransferError (see isPoERejection for stale or missing parents).
     */
    async submitSignedTransfer(envelope) {
        if (!envelope || typeof envelope !== 'object' || !envelope.signature || !envelope.public_key) {
            throw new TypeError('qsdm: the transfer envelope is not signed (signature and public_key are required)');
        }
        const { response, text, url } = await this._send(
            'POST', '/api/v1/wallet/submit-signed', envelope, 'application/json'
        );
        if (!response.ok) {
            throw new TransferError(response.status, url, text);
        }
        if (!text) return null;
        try {
            return JSON.parse(text);
        } catch (e) {
            throw new Error(`qsdm: failed to decode JSON from ${url}: ${e.message}`);
        }
    }

    /**
     * Prepares, signs and submits one transfer. `signTransfer(envelope,
     * signingBytes)` signs with a key the caller holds: it returns
     * { signature, public_key } (hex strings or Uint8Arrays), or the full
     * signed envelope (object or JSON string, as qsdmcli and the web wallet's
     * WASM signer produce). A returned envelope must sign exactly the
     * prepared transfer.
     *
     * One attempt: after isPoERejection(err) or a nonce/duplicate refusal,
     * call it again (every call reads a fresh nonce and fresh parents). A
     * sender can have one transfer waiting per block, so after a conflict
     * wait for the next block (about 10 seconds).
     */
    async sendTransfer(request = {}) {
        if (typeof request.signTransfer !== 'function') {
            throw new TypeError('qsdm: sendTransfer needs a signTransfer(envelope, signingBytes) function');
        }
        const unsigned = await this.prepareTransfer(request);
        const signingBytes = transferSigningBytes(unsigned);
        const result = await request.signTransfer({ ...unsigned }, signingBytes);
        return this.submitSignedTransfer(attachTransferSignature(unsigned, result));
    }

    /**
     * Retrieve a transaction by ID from GET /api/v1/transactions/{tx_id}
     * (plural; SDK builds up to 0.3.0 called the singular path, fixed in
     * 0.3.1).
     *
     * @deprecated 0.4.0. The route is not public: it is registered in
     * pkg/api but not on the public allowlist, so a public node answers 401
     * without an operator JWT. Use getReceipt() (GET /api/v1/receipts/{tx_id},
     * public) to follow a transaction.
     */
    async getTransaction(txID) {
        return this._request('GET', `/api/v1/transactions/${encodeURIComponent(txID)}`);
    }

    /**
     * @deprecated 0.3.1. No QSDM node registers /api/v1/wallet/transactions,
     * so the call always fails (401 without credentials, 404 with an
     * operator JWT). For recent transactions use getReceipts() and filter,
     * or keep your own index.
     */
    async getRecentTransactions(address, limit = 10) {
        const q = encodeURIComponent(address);
        return this._request('GET', `/api/v1/wallet/transactions?address=${q}&limit=${limit}`);
    }

    // --- CELL Streams ---

    /**
     * Return the exact consensus nonce required by the sender's next
     * qsdm/streams/v1 action. Do not use getWalletNonce().next here:
     * transfer envelopes and contract actions intentionally use different
     * wire conventions.
     */
    async getStreamActionNonce(address) {
        const q = encodeURIComponent(address);
        return this._request('GET', `/api/v1/streams/nonce?sender=${q}`);
    }

    /**
     * List consensus CELL metered-payment streams.
     * @param {{payer?: string, provider?: string, status?: string, serviceId?: string}} [filters]
     */
    async getStreams(filters = {}) {
        const pairs = [];
        if (filters.payer) pairs.push(`payer=${encodeURIComponent(filters.payer)}`);
        if (filters.provider) pairs.push(`provider=${encodeURIComponent(filters.provider)}`);
        if (filters.status) pairs.push(`status=${encodeURIComponent(filters.status)}`);
        if (filters.serviceId) pairs.push(`service_id=${encodeURIComponent(filters.serviceId)}`);
        const query = pairs.length > 0 ? `?${pairs.join('&')}` : '';
        return this._request('GET', `/api/v1/streams${query}`);
    }

    async getStream(streamID) {
        return this._request('GET', `/api/v1/streams/${encodeURIComponent(streamID)}`);
    }

    /**
     * Submit an envelope produced by `qsdmcli wallet sign-stream-action`
     * or an equivalent QSDM wallet signer. The route is public (the
     * signature is the authorization), but a node that admits only signed
     * wallet transfers answers 503; the public pilot pauses stream actions
     * during its recovery.
     */
    async submitStreamAction(envelope) {
        return this._request('POST', '/api/v1/streams/actions/submit-signed', envelope);
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

    /**
     * @deprecated 0.3.1. No QSDM node registers /api/v1/network/peers, so
     * the call always fails (401 without credentials, 404 with an operator
     * JWT). Peer lists exist only on administrative routes
     * (/api/admin/peers, mTLS) and the operator dashboard. For the number of
     * connected peers use getNodeStatus().peers.
     */
    async getPeers() {
        const out = await this._request('GET', '/api/v1/network/peers');
        return Array.isArray(out.peers) ? out.peers : [];
    }

    /**
     * @deprecated 0.4.0. /api/v1/network/topology is registered in pkg/api
     * but not on the public allowlist, so a public node answers 401 without
     * an operator JWT. There is no public topology route; use
     * getNodeStatus().peers for the peer count.
     */
    async getNetworkTopology() {
        return this._request('GET', '/api/v1/network/topology');
    }

    // --- metrics ---

    /**
     * @deprecated 0.3.1. /api/metrics exists only on the operator dashboard
     * server (login required), not on the node API, so against a node the
     * call always fails (401 without credentials, 404 with an operator JWT).
     * There is no public metrics endpoint.
     */
    async getMetricsJSON()       { return this._request('GET', '/api/metrics'); }

    /**
     * @deprecated 0.3.1. Like getMetricsJSON, /api/metrics/prometheus exists
     * only on the operator dashboard server (scrape token or login
     * required), not on the node API.
     */
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
    module.exports.TransferError = TransferError;
    module.exports.isNotFound = isNotFound;
    module.exports.isUnauthorized = isUnauthorized;
    module.exports.isPoERejection = isPoERejection;
    module.exports.transferSigningBytes = transferSigningBytes;
    module.exports.getWalletProvider = getWalletProvider;
    module.exports.waitForWalletProvider = waitForWalletProvider;
    module.exports.sendTransferWithProvider = sendTransferWithProvider;
    Object.assign(module.exports, require('./cell-stream-runtime.js'));
}
