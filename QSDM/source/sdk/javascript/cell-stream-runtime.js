'use strict';

const CELL_STREAM_RUNTIME_VERSION = 1;
const DEFAULT_RECEIPT_INTERVAL_SECONDS = 30;
const DEFAULT_CHECKPOINT_INTERVAL_MS = 5000;
const DEFAULT_CONFIRMATION_TIMEOUT_MS = 60000;
const DEFAULT_CONFIRMATION_POLL_MS = 1000;

function requireFunction(value, name) {
    if (typeof value !== 'function') {
        throw new Error(`${name} must be a function`);
    }
}

function requireIdentifier(value, name) {
    if (typeof value !== 'string' ||
        value.length === 0 ||
        value.length > 128 ||
        value.trim() !== value ||
        !/^[A-Za-z0-9_.:-]+$/.test(value)) {
        throw new Error(`${name} is not a valid CELL Stream identifier`);
    }
    return value;
}

function requireWalletAddress(value, name) {
    if (typeof value !== 'string' || !/^[0-9a-f]{64}$/.test(value)) {
        throw new Error(`${name} must be a lower-case 32-byte QSDM wallet address`);
    }
    return value;
}

function requireHex(value, bytes, name) {
    const pattern = new RegExp(`^[0-9a-f]{${bytes * 2}}$`);
    if (typeof value !== 'string' || !pattern.test(value)) {
        throw new Error(`${name} must be lower-case ${bytes}-byte hex`);
    }
    return value;
}

function requireSafeInteger(value, name, allowZero = false) {
    if (!Number.isSafeInteger(value) || value < (allowZero ? 0 : 1)) {
        throw new Error(`${name} must be a ${allowZero ? 'non-negative' : 'positive'} safe integer`);
    }
    return value;
}

function requireTimestamp(value, name) {
    if (typeof value !== 'string' || !Number.isFinite(Date.parse(value))) {
        throw new Error(`${name} must be an RFC3339 timestamp`);
    }
    return value;
}

function bytesToHex(value, expectedBytes, name) {
    if (typeof value === 'string') {
        return requireHex(value.toLowerCase(), expectedBytes, name);
    }
    if (!(value instanceof Uint8Array)) {
        throw new Error(`${name} must be a Uint8Array or lower-case hex string`);
    }
    if (value.byteLength !== expectedBytes) {
        throw new Error(`${name} must be ${expectedBytes} bytes`);
    }
    return Array.from(value, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

function encodeUTF8(value) {
    if (typeof TextEncoder === 'undefined') {
        throw new Error('CELL Streams requires TextEncoder support');
    }
    return new TextEncoder().encode(value);
}

function secureRandomHex(bytes) {
    const cryptoObject = typeof globalThis !== 'undefined' ? globalThis.crypto : null;
    if (!cryptoObject || typeof cryptoObject.getRandomValues !== 'function') {
        throw new Error('secure randomness is unavailable; provide idFactory');
    }
    const out = new Uint8Array(bytes);
    cryptoObject.getRandomValues(out);
    return bytesToHex(out, bytes, 'random identifier');
}

function defaultActionIDFactory({ action, nowMs }) {
    return `stream-${action}-${nowMs.toString(36)}-${secureRandomHex(12)}`;
}

function canonicalStreamReceipt(receipt, includeSignature = true) {
    const out = {
        stream_id: requireIdentifier(receipt.stream_id, 'receipt.stream_id'),
        sequence: requireSafeInteger(receipt.sequence, 'receipt.sequence'),
        cumulative_active_seconds: requireSafeInteger(
            receipt.cumulative_active_seconds,
            'receipt.cumulative_active_seconds'
        ),
        observed_at: requireTimestamp(receipt.observed_at, 'receipt.observed_at'),
    };
    if (includeSignature) {
        out.signature = requireHex(receipt.signature, 64, 'receipt.signature');
    }
    return out;
}

function streamReceiptSigningBytes(receipt) {
    return encodeUTF8(JSON.stringify(canonicalStreamReceipt(receipt, false)));
}

function canonicalStreamAction(input) {
    const action = {
        id: requireIdentifier(input.id, 'action.id'),
        sender: requireWalletAddress(input.sender, 'action.sender'),
        stream_id: requireIdentifier(input.stream_id, 'action.stream_id'),
        action: input.action,
    };
    if (!['open', 'receipt', 'pause', 'resume', 'settle', 'close'].includes(action.action)) {
        throw new Error(`unsupported CELL Stream action ${JSON.stringify(action.action)}`);
    }
    if (input.provider !== undefined && input.provider !== '') {
        action.provider = requireWalletAddress(input.provider, 'action.provider');
    }
    if (input.service_id !== undefined && input.service_id !== '') {
        action.service_id = requireIdentifier(input.service_id, 'action.service_id');
    }
    if (input.device_id_hash !== undefined && input.device_id_hash !== '') {
        action.device_id_hash = requireHex(input.device_id_hash, 32, 'action.device_id_hash');
    }
    if (input.session_public_key !== undefined && input.session_public_key !== '') {
        action.session_public_key = requireHex(
            input.session_public_key,
            32,
            'action.session_public_key'
        );
    }
    if (input.price_dust !== undefined && input.price_dust !== 0) {
        action.price_dust = requireSafeInteger(input.price_dust, 'action.price_dust');
    }
    if (input.price_period_seconds !== undefined && input.price_period_seconds !== 0) {
        action.price_period_seconds = requireSafeInteger(
            input.price_period_seconds,
            'action.price_period_seconds'
        );
    }
    if (input.budget_dust !== undefined && input.budget_dust !== 0) {
        action.budget_dust = requireSafeInteger(input.budget_dust, 'action.budget_dust');
    }
    if (input.max_active_seconds !== undefined && input.max_active_seconds !== 0) {
        action.max_active_seconds = requireSafeInteger(
            input.max_active_seconds,
            'action.max_active_seconds'
        );
    }
    if (input.expires_at !== undefined && input.expires_at !== '') {
        action.expires_at = requireTimestamp(input.expires_at, 'action.expires_at');
    }
    if (input.receipt !== undefined && input.receipt !== null) {
        action.receipt = canonicalStreamReceipt(input.receipt, true);
    }
    if (input.nonce !== undefined && input.nonce !== 0) {
        action.nonce = requireSafeInteger(input.nonce, 'action.nonce');
    }
    action.timestamp = requireTimestamp(input.timestamp, 'action.timestamp');
    return action;
}

function streamActionSigningBytes(action) {
    return encodeUTF8(JSON.stringify(canonicalStreamAction(action)));
}

function cloneJSON(value) {
    return value === undefined ? undefined : JSON.parse(JSON.stringify(value));
}

function stateFromResponse(response) {
    if (!response || typeof response !== 'object') return null;
    return response.stream && typeof response.stream === 'object'
        ? response.stream
        : response;
}

function defaultSleep(ms) {
    return new Promise((resolve) => setTimeout(resolve, ms));
}

class CellStreamWallet {
    constructor(options = {}) {
        if (!options.client ||
            typeof options.client.getWalletNonce !== 'function' ||
            typeof options.client.submitStreamAction !== 'function' ||
            typeof options.client.getStream !== 'function') {
            throw new Error('CellStreamWallet requires a QSDM client with nonce and stream methods');
        }
        requireFunction(options.signAction, 'signAction');

        this.client = options.client;
        this.address = requireWalletAddress(options.address, 'address');
        this.signAction = options.signAction;
        this.idFactory = options.idFactory || defaultActionIDFactory;
        this.clock = options.clock || (() => Date.now());
        this.sleep = options.sleep || defaultSleep;
        this.confirmationTimeoutMs = options.confirmationTimeoutMs === undefined
            ? DEFAULT_CONFIRMATION_TIMEOUT_MS
            : requireSafeInteger(options.confirmationTimeoutMs, 'confirmationTimeoutMs', true);
        this.confirmationPollMs = options.confirmationPollMs === undefined
            ? DEFAULT_CONFIRMATION_POLL_MS
            : requireSafeInteger(options.confirmationPollMs, 'confirmationPollMs');
        this._tail = Promise.resolve();
    }

    async prepareAction(draft) {
        if (!draft || typeof draft !== 'object') {
            throw new Error('CELL Stream action draft is required');
        }
        const nonceResponse = await this.client.getWalletNonce(this.address);
        const nonce = nonceResponse && nonceResponse.next;
        requireSafeInteger(nonce, 'wallet nonce');
        const nowMs = this.clock();
        requireSafeInteger(nowMs, 'clock time', true);
        const timestamp = new Date(nowMs).toISOString();
        const id = draft.id || this.idFactory({
            action: draft.action,
            streamID: draft.stream_id,
            nowMs,
        });
        const action = canonicalStreamAction({
            ...draft,
            id,
            sender: this.address,
            nonce,
            timestamp,
        });
        const signingBytes = streamActionSigningBytes(action);
        const signed = await this.signAction(action, signingBytes);
        if (!signed || typeof signed !== 'object') {
            throw new Error('signAction did not return a signature and public key');
        }
        const signature = String(signed.signature || '').trim().toLowerCase();
        const publicKey = String(signed.publicKey || signed.public_key || '').trim().toLowerCase();
        if (!/^[0-9a-f]+$/.test(signature) || signature.length % 2 !== 0) {
            throw new Error('signAction returned an invalid hex signature');
        }
        if (!/^[0-9a-f]+$/.test(publicKey) || publicKey.length % 2 !== 0) {
            throw new Error('signAction returned an invalid hex public key');
        }
        return {
            action,
            signature,
            public_key: publicKey,
        };
    }

    async submitPrepared(envelope) {
        if (!envelope || !envelope.action) {
            throw new Error('prepared CELL Stream envelope is required');
        }
        return this.client.submitStreamAction(envelope);
    }

    async waitForAction(envelope) {
        if (this.confirmationTimeoutMs === 0) return null;
        const deadline = this.clock() + this.confirmationTimeoutMs;
        let lastError = null;
        while (this.clock() <= deadline) {
            if (typeof this.client.getTransaction === 'function') {
                try {
                    await this.client.getTransaction(envelope.action.id);
                    try {
                        const response = await this.client.getStream(
                            envelope.action.stream_id
                        );
                        return stateFromResponse(response);
                    } catch (_streamError) {
                        return null;
                    }
                } catch (error) {
                    lastError = error;
                    if (error && error.status !== undefined &&
                        error.status !== 404 &&
                        error.status !== 501) {
                        // Transient gateway errors are retried below.
                    }
                }
            }
            try {
                const response = await this.client.getStream(envelope.action.stream_id);
                const stream = stateFromResponse(response);
                if (stream && stream.last_action_id === envelope.action.id) {
                    return stream;
                }
                if (stream &&
                    stream.status === 'closed' &&
                    envelope.action.action !== 'close') {
                    throw new Error('CELL Stream closed before the action was confirmed');
                }
            } catch (error) {
                lastError = error;
                if (error && error.status !== undefined && error.status !== 404) {
                    // Transient gateway errors are retried until the confirmation deadline.
                }
            }
            await this.sleep(this.confirmationPollMs);
        }
        const detail = lastError && lastError.message ? `: ${lastError.message}` : '';
        throw new Error(
            `CELL Stream action ${envelope.action.id} was not confirmed before timeout${detail}`
        );
    }

    submitAction(draft, options = {}) {
        const run = async () => {
            const envelope = options.preparedEnvelope || await this.prepareAction(draft);
            if (options.onPrepared) {
                await options.onPrepared(envelope);
            }
            const submission = await this.submitPrepared(envelope);
            const stream = await this.waitForAction(envelope);
            return { envelope, submission, stream };
        };
        const current = this._tail.then(run, run);
        this._tail = current.then(() => undefined, () => undefined);
        return current;
    }
}

function normalizeOpenConfig(config, sessionPublicKey) {
    if (!config || typeof config !== 'object') {
        throw new Error('CELL Stream open configuration is required');
    }
    const normalized = {
        streamId: requireIdentifier(config.streamId, 'streamId'),
        provider: requireWalletAddress(config.provider, 'provider'),
        serviceId: requireIdentifier(config.serviceId, 'serviceId'),
        deviceIdHash: requireHex(config.deviceIdHash, 32, 'deviceIdHash'),
        sessionPublicKey: requireHex(sessionPublicKey, 32, 'sessionSigner.publicKeyHex'),
        priceDust: requireSafeInteger(config.priceDust, 'priceDust'),
        pricePeriodSeconds: requireSafeInteger(
            config.pricePeriodSeconds,
            'pricePeriodSeconds'
        ),
        budgetDust: requireSafeInteger(config.budgetDust, 'budgetDust'),
        maxActiveSeconds: requireSafeInteger(config.maxActiveSeconds, 'maxActiveSeconds'),
        expiresAt: requireTimestamp(config.expiresAt, 'expiresAt'),
    };
    const budgetLimit = (
        BigInt(normalized.budgetDust) * BigInt(normalized.pricePeriodSeconds)
    ) / BigInt(normalized.priceDust);
    const activeLimit = BigInt(normalized.maxActiveSeconds);
    const limit = budgetLimit < activeLimit ? budgetLimit : activeLimit;
    if (limit < 1n || limit > BigInt(Number.MAX_SAFE_INTEGER)) {
        throw new Error('CELL Stream configuration has an unusable billable-second limit');
    }
    normalized.billingLimitSeconds = Number(limit);
    return normalized;
}

class CellStreamServiceMeter {
    constructor(options = {}) {
        if (!options.wallet ||
            typeof options.wallet.submitAction !== 'function' ||
            !options.wallet.address) {
            throw new Error('CellStreamServiceMeter requires a CellStreamWallet');
        }
        if (!options.storage ||
            typeof options.storage.getItem !== 'function' ||
            typeof options.storage.setItem !== 'function') {
            throw new Error('CellStreamServiceMeter requires durable storage');
        }
        if (!options.sessionSigner || typeof options.sessionSigner !== 'object') {
            throw new Error('CellStreamServiceMeter requires a secure session signer');
        }
        requireFunction(options.sessionSigner.sign, 'sessionSigner.sign');
        requireFunction(options.receiptSubmitter, 'receiptSubmitter');

        this.wallet = options.wallet;
        this.storage = options.storage;
        this.storageKey = options.storageKey || 'qsdm.cell-stream.runtime.v1';
        this.sessionSigner = options.sessionSigner;
        this.sessionPublicKey = requireHex(
            String(options.sessionSigner.publicKeyHex || '').toLowerCase(),
            32,
            'sessionSigner.publicKeyHex'
        );
        this.receiptSubmitter = options.receiptSubmitter;
        this.clock = options.clock || (() => Date.now());
        this.setInterval = options.setInterval || globalThis.setInterval;
        this.clearInterval = options.clearInterval || globalThis.clearInterval;
        this.receiptIntervalSeconds = options.receiptIntervalSeconds === undefined
            ? DEFAULT_RECEIPT_INTERVAL_SECONDS
            : requireSafeInteger(options.receiptIntervalSeconds, 'receiptIntervalSeconds');
        this.checkpointIntervalMs = options.checkpointIntervalMs === undefined
            ? DEFAULT_CHECKPOINT_INTERVAL_MS
            : requireSafeInteger(options.checkpointIntervalMs, 'checkpointIntervalMs');
        this.autoSchedule = options.autoSchedule !== false;
        this.onError = typeof options.onError === 'function' ? options.onError : () => {};
        this.onLimitReached = typeof options.onLimitReached === 'function'
            ? options.onLimitReached
            : () => {};

        this.state = null;
        this.initialized = false;
        this.requiresRecovery = false;
        this.timer = null;
        this.heartbeatInFlight = false;
    }

    async initialize() {
        if (this.initialized) return this.snapshot();
        const raw = await Promise.resolve(this.storage.getItem(this.storageKey));
        if (raw) {
            let state;
            try {
                state = JSON.parse(raw);
            } catch (error) {
                throw new Error(`stored CELL Stream runtime state is invalid: ${error.message}`);
            }
            this._validateStoredState(state);
            this.state = state;
            this.requiresRecovery = Boolean(
                state.localActive ||
                state.pendingWalletEnvelope ||
                state.pendingReceipt
            );
            state.localActive = false;
            state.activeAnchorMs = null;
            await this._save();
        }
        this.initialized = true;
        return this.snapshot();
    }

    snapshot() {
        if (!this.state) {
            return {
                initialized: this.initialized,
                requiresRecovery: this.requiresRecovery,
                stream: null,
            };
        }
        const accrued = (
            BigInt(this.state.cumulativeActiveSeconds) *
            BigInt(this.state.config.priceDust)
        ) / BigInt(this.state.config.pricePeriodSeconds);
        const remaining = BigInt(this.state.config.budgetDust) > accrued
            ? BigInt(this.state.config.budgetDust) - accrued
            : 0n;
        return {
            initialized: this.initialized,
            requiresRecovery: this.requiresRecovery,
            estimatedAccruedDust: accrued.toString(),
            estimatedRemainingBudgetDust: remaining.toString(),
            stream: cloneJSON(this.state),
        };
    }

    async onServiceStarted(config) {
        await this._ensureInitialized();
        if (!this.state) {
            const normalized = normalizeOpenConfig(config, this.sessionPublicKey);
            this.state = {
                version: CELL_STREAM_RUNTIME_VERSION,
                config: normalized,
                payer: this.wallet.address,
                chainStatus: 'opening',
                localActive: false,
                activeAnchorMs: null,
                fractionalActiveMs: 0,
                cumulativeActiveSeconds: 0,
                confirmedActiveSeconds: 0,
                lastConfirmedSequence: 0,
                pendingReceipt: null,
                pendingWalletEnvelope: null,
                limitReached: false,
                updatedAt: new Date(this.clock()).toISOString(),
            };
            await this._save();
            await this._submitWalletAction({
                stream_id: normalized.streamId,
                action: 'open',
                provider: normalized.provider,
                service_id: normalized.serviceId,
                device_id_hash: normalized.deviceIdHash,
                session_public_key: normalized.sessionPublicKey,
                price_dust: normalized.priceDust,
                price_period_seconds: normalized.pricePeriodSeconds,
                budget_dust: normalized.budgetDust,
                max_active_seconds: normalized.maxActiveSeconds,
                expires_at: normalized.expiresAt,
            });
        } else {
            if (config && config.streamId && config.streamId !== this.state.config.streamId) {
                throw new Error('stored CELL Stream does not match the requested streamId');
            }
            await this._recoverPendingWalletAction();
            await this._syncRemoteState();
            if (this.state.chainStatus === 'closed') {
                throw new Error('closed CELL Stream cannot be restarted');
            }
            if (this.state.chainStatus === 'paused') {
                await this._submitWalletAction({
                    stream_id: this.state.config.streamId,
                    action: 'resume',
                });
            }
        }
        if (this.state.limitReached) {
            throw new Error('CELL Stream active-use limit has been reached');
        }
        this.requiresRecovery = false;
        this.state.localActive = true;
        this.state.activeAnchorMs = this.clock();
        await this._save();
        this._startTimer();
        return this.snapshot();
    }

    async onServiceStopped(options = {}) {
        await this._ensureInitialized();
        if (!this.state) return this.snapshot();
        await this.checkpoint({ flush: false });
        this.state.localActive = false;
        this.state.activeAnchorMs = null;
        this._stopTimer();
        await this._save();
        await this.flushReceipt({ force: true });
        const action = options.close === true ? 'close' : 'pause';
        if (this.state.chainStatus !== 'closed' &&
            !(action === 'pause' && this.state.chainStatus === 'paused')) {
            await this._submitWalletAction({
                stream_id: this.state.config.streamId,
                action,
            });
        }
        this.requiresRecovery = false;
        await this._save();
        return this.snapshot();
    }

    async recover(serviceIsActive) {
        await this._ensureInitialized();
        if (!this.state) return this.snapshot();
        if (typeof serviceIsActive !== 'boolean') {
            throw new Error('recover requires the actual service active state');
        }
        await this._recoverPendingWalletAction();
        await this._syncRemoteState();
        if (this.state.pendingReceipt && this.state.chainStatus === 'active') {
            await this._submitPendingReceipt();
        }
        if (serviceIsActive) {
            if (this.state.chainStatus === 'closed') {
                throw new Error('service is active but its CELL Stream is closed');
            }
            if (this.state.chainStatus === 'paused') {
                await this._submitWalletAction({
                    stream_id: this.state.config.streamId,
                    action: 'resume',
                });
            }
            this.state.localActive = true;
            this.state.activeAnchorMs = this.clock();
            this.requiresRecovery = false;
            await this._save();
            this._startTimer();
        } else {
            this.state.localActive = false;
            this.state.activeAnchorMs = null;
            this._stopTimer();
            if (this.state.chainStatus === 'active') {
                await this.flushReceipt({ force: true });
                await this._submitWalletAction({
                    stream_id: this.state.config.streamId,
                    action: 'pause',
                });
            }
            this.requiresRecovery = false;
            await this._save();
        }
        return this.snapshot();
    }

    async checkpoint(options = {}) {
        await this._ensureInitialized();
        if (!this.state || !this.state.localActive) return this.snapshot();
        const now = this.clock();
        const anchor = this.state.activeAnchorMs;
        if (!Number.isSafeInteger(now) || !Number.isSafeInteger(anchor)) {
            throw new Error('CELL Stream runtime clock returned an invalid value');
        }
        if (now < anchor) {
            this.state.activeAnchorMs = now;
            await this._save();
            throw new Error('CELL Stream runtime clock moved backwards; no usage was added');
        }
        const elapsedMs = now - anchor;
        const totalMs = this.state.fractionalActiveMs + elapsedMs;
        const addedSeconds = Math.floor(totalMs / 1000);
        this.state.fractionalActiveMs = totalMs % 1000;
        this.state.activeAnchorMs = now;
        if (addedSeconds > 0) {
            const next = this.state.cumulativeActiveSeconds + addedSeconds;
            this.state.cumulativeActiveSeconds = Math.min(
                next,
                this.state.config.billingLimitSeconds
            );
            this.state.limitReached =
                this.state.cumulativeActiveSeconds >= this.state.config.billingLimitSeconds;
        }
        await this._save();
        if (options.flush !== false &&
            this.state.cumulativeActiveSeconds - this.state.confirmedActiveSeconds >=
            this.receiptIntervalSeconds) {
            await this.flushReceipt();
        }
        return this.snapshot();
    }

    async flushReceipt(options = {}) {
        await this._ensureInitialized();
        if (!this.state) return null;
        if (this.state.pendingReceipt) {
            return this._submitPendingReceipt();
        }
        const delta = (
            this.state.cumulativeActiveSeconds -
            this.state.confirmedActiveSeconds
        );
        if (delta <= 0) return null;
        if (options.force !== true && delta < this.receiptIntervalSeconds) {
            return null;
        }
        if (this.state.chainStatus !== 'active') {
            throw new Error('cannot submit usage while the CELL Stream is not active');
        }
        const receipt = {
            stream_id: this.state.config.streamId,
            sequence: this.state.lastConfirmedSequence + 1,
            cumulative_active_seconds: this.state.cumulativeActiveSeconds,
            observed_at: new Date(this.clock()).toISOString(),
        };
        const signingBytes = streamReceiptSigningBytes(receipt);
        const signatureValue = await this.sessionSigner.sign(signingBytes, cloneJSON(receipt));
        receipt.signature = bytesToHex(
            signatureValue,
            64,
            'sessionSigner signature'
        );
        this.state.pendingReceipt = canonicalStreamReceipt(receipt, true);
        await this._save();
        return this._submitPendingReceipt();
    }

    async close() {
        return this.onServiceStopped({ close: true });
    }

    async dispose() {
        this._stopTimer();
        if (this.state) await this._save();
    }

    async _submitPendingReceipt() {
        const receipt = cloneJSON(this.state.pendingReceipt);
        if (!receipt) return null;
        const result = await this.receiptSubmitter(receipt, this.snapshot());
        if (!result || result.confirmed !== true) {
            throw new Error('provider accepted no durable confirmation for the usage receipt');
        }
        this.state.lastConfirmedSequence = receipt.sequence;
        this.state.confirmedActiveSeconds = receipt.cumulative_active_seconds;
        this.state.pendingReceipt = null;
        await this._save();
        return result === undefined ? receipt : result;
    }

    async _submitWalletAction(draft) {
        const existing = this.state.pendingWalletEnvelope;
        const result = await this.wallet.submitAction(draft, {
            preparedEnvelope: existing || undefined,
            onPrepared: async (envelope) => {
                this.state.pendingWalletEnvelope = cloneJSON(envelope);
                await this._save();
            },
        });
        const action = result.envelope.action.action;
        this.state.pendingWalletEnvelope = null;
        if (action === 'open' || action === 'resume') this.state.chainStatus = 'active';
        if (action === 'pause') this.state.chainStatus = 'paused';
        if (action === 'close') this.state.chainStatus = 'closed';
        await this._save();
        return result;
    }

    async _recoverPendingWalletAction() {
        if (!this.state.pendingWalletEnvelope) return;
        const envelope = cloneJSON(this.state.pendingWalletEnvelope);
        await this._submitWalletAction({
            stream_id: envelope.action.stream_id,
            action: envelope.action.action,
        });
    }

    async _syncRemoteState() {
        let response;
        try {
            response = await this.wallet.client.getStream(this.state.config.streamId);
        } catch (error) {
            if (error && error.status === 404 && this.state.chainStatus === 'opening') return;
            throw error;
        }
        const remote = stateFromResponse(response);
        if (!remote) throw new Error('QSDM Core returned no CELL Stream state');
        if (remote.payer !== this.state.payer ||
            remote.provider !== this.state.config.provider ||
            remote.service_id !== this.state.config.serviceId ||
            remote.device_id_hash !== this.state.config.deviceIdHash ||
            remote.session_public_key !== this.state.config.sessionPublicKey ||
            remote.price_dust !== this.state.config.priceDust ||
            remote.price_period_seconds !== this.state.config.pricePeriodSeconds ||
            remote.budget_dust !== this.state.config.budgetDust ||
            remote.max_active_seconds !== this.state.config.maxActiveSeconds ||
            remote.expires_at !== this.state.config.expiresAt) {
            throw new Error('remote CELL Stream identity does not match durable local state');
        }
        if (!['active', 'paused', 'closed'].includes(remote.status)) {
            throw new Error('remote CELL Stream has an invalid status');
        }
        const remoteSequence = requireSafeInteger(
            Number(remote.last_receipt_sequence || 0),
            'remote last_receipt_sequence',
            true
        );
        const remoteSeconds = requireSafeInteger(
            Number(remote.cumulative_active_seconds || 0),
            'remote cumulative_active_seconds',
            true
        );
        if (remoteSequence < this.state.lastConfirmedSequence ||
            remoteSeconds < this.state.confirmedActiveSeconds) {
            throw new Error('remote CELL Stream state is behind a durable confirmation');
        }
        if (remoteSeconds > this.state.config.billingLimitSeconds) {
            throw new Error('remote CELL Stream usage exceeds the authorized local limit');
        }
        this.state.chainStatus = remote.status;
        this.state.lastConfirmedSequence = remoteSequence;
        this.state.confirmedActiveSeconds = remoteSeconds;
        this.state.cumulativeActiveSeconds = Math.max(
            this.state.cumulativeActiveSeconds,
            this.state.confirmedActiveSeconds
        );
        if (this.state.pendingReceipt &&
            this.state.pendingReceipt.sequence <= this.state.lastConfirmedSequence) {
            this.state.pendingReceipt = null;
        }
        await this._save();
    }

    async _heartbeat() {
        if (this.heartbeatInFlight) return;
        this.heartbeatInFlight = true;
        try {
            await this.checkpoint();
            if (this.state && this.state.limitReached && this.state.localActive) {
                await this.onServiceStopped();
                await this.onLimitReached(this.snapshot());
            }
        } catch (error) {
            await this.onError(error, this.snapshot());
        } finally {
            this.heartbeatInFlight = false;
        }
    }

    _startTimer() {
        if (!this.autoSchedule || this.timer !== null) return;
        requireFunction(this.setInterval, 'setInterval');
        this.timer = this.setInterval(() => {
            void this._heartbeat();
        }, this.checkpointIntervalMs);
    }

    _stopTimer() {
        if (this.timer === null) return;
        requireFunction(this.clearInterval, 'clearInterval');
        this.clearInterval(this.timer);
        this.timer = null;
    }

    async _ensureInitialized() {
        if (!this.initialized) await this.initialize();
    }

    async _save() {
        if (!this.state) return;
        this.state.updatedAt = new Date(this.clock()).toISOString();
        await Promise.resolve(
            this.storage.setItem(this.storageKey, JSON.stringify(this.state))
        );
    }

    _validateStoredState(state) {
        if (!state || state.version !== CELL_STREAM_RUNTIME_VERSION) {
            throw new Error('stored CELL Stream runtime version is unsupported');
        }
        if (!state.config || state.config.sessionPublicKey !== this.sessionPublicKey) {
            throw new Error(
                'stored CELL Stream requires its original secure session key'
            );
        }
        const normalizedConfig = normalizeOpenConfig({
            streamId: state.config.streamId,
            provider: state.config.provider,
            serviceId: state.config.serviceId,
            deviceIdHash: state.config.deviceIdHash,
            priceDust: state.config.priceDust,
            pricePeriodSeconds: state.config.pricePeriodSeconds,
            budgetDust: state.config.budgetDust,
            maxActiveSeconds: state.config.maxActiveSeconds,
            expiresAt: state.config.expiresAt,
        }, this.sessionPublicKey);
        if (state.config.billingLimitSeconds !== normalizedConfig.billingLimitSeconds) {
            throw new Error('stored CELL Stream billing limit is inconsistent');
        }
        state.config = normalizedConfig;
        requireWalletAddress(state.payer, 'stored payer');
        requireSafeInteger(
            state.cumulativeActiveSeconds,
            'stored cumulativeActiveSeconds',
            true
        );
        requireSafeInteger(
            state.confirmedActiveSeconds,
            'stored confirmedActiveSeconds',
            true
        );
        requireSafeInteger(
            state.lastConfirmedSequence,
            'stored lastConfirmedSequence',
            true
        );
        if (state.confirmedActiveSeconds > state.cumulativeActiveSeconds) {
            throw new Error('stored CELL Stream counters are inconsistent');
        }
        if (state.cumulativeActiveSeconds > state.config.billingLimitSeconds) {
            throw new Error('stored CELL Stream usage exceeds its authorized limit');
        }
        requireSafeInteger(state.fractionalActiveMs, 'stored fractionalActiveMs', true);
        if (state.fractionalActiveMs >= 1000) {
            throw new Error('stored CELL Stream fractional time is invalid');
        }
        if (!['opening', 'active', 'paused', 'closed'].includes(state.chainStatus)) {
            throw new Error('stored CELL Stream status is invalid');
        }
        if (state.pendingReceipt) {
            state.pendingReceipt = canonicalStreamReceipt(state.pendingReceipt, true);
            if (state.pendingReceipt.sequence !== state.lastConfirmedSequence + 1 ||
                state.pendingReceipt.cumulative_active_seconds >
                    state.cumulativeActiveSeconds) {
                throw new Error('stored pending CELL Stream receipt is inconsistent');
            }
        }
        if (state.pendingWalletEnvelope) {
            state.pendingWalletEnvelope.action = canonicalStreamAction(
                state.pendingWalletEnvelope.action
            );
            const signature = String(state.pendingWalletEnvelope.signature || '');
            const publicKey = String(state.pendingWalletEnvelope.public_key || '');
            if (!/^[0-9a-f]+$/.test(signature) ||
                signature.length % 2 !== 0 ||
                !/^[0-9a-f]+$/.test(publicKey) ||
                publicKey.length % 2 !== 0) {
                throw new Error('stored pending CELL Stream envelope is invalid');
            }
        }
    }
}

if (typeof module !== 'undefined' && module.exports) {
    module.exports = {
        CELL_STREAM_RUNTIME_VERSION,
        CellStreamWallet,
        CellStreamServiceMeter,
        canonicalStreamAction,
        streamActionSigningBytes,
        canonicalStreamReceipt,
        streamReceiptSigningBytes,
    };
}
