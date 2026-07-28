'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const {
    CellStreamWallet,
    CellStreamServiceMeter,
    streamActionSigningBytes,
    streamReceiptSigningBytes,
} = require('./cell-stream-runtime.js');

const payer = '11'.repeat(32);
const provider = '22'.repeat(32);
const deviceHash = '33'.repeat(32);
const sessionPublicKey = '44'.repeat(32);

class MemoryStorage {
    constructor() {
        this.values = new Map();
    }

    getItem(key) {
        return this.values.get(key) || null;
    }

    setItem(key, value) {
        this.values.set(key, value);
    }
}

function openConfig(now) {
    return {
        streamId: 'vpn-device-001',
        provider,
        serviceId: 'qsdm-vpn',
        deviceIdHash: deviceHash,
        priceDust: 200000000,
        pricePeriodSeconds: 2592000,
        budgetDust: 200000000,
        maxActiveSeconds: 2592000,
        expiresAt: new Date(now + 40 * 24 * 60 * 60 * 1000).toISOString(),
    };
}

function createFakeWallet(remote, actions) {
    let actionNumber = 0;
    const client = {
        async getStream() {
            if (!remote.value) {
                const error = new Error('not found');
                error.status = 404;
                throw error;
            }
            return { stream: { ...remote.value } };
        },
    };
    return {
        address: payer,
        client,
        async submitAction(draft, options = {}) {
            const envelope = options.preparedEnvelope || {
                action: {
                    ...draft,
                    id: `action-${++actionNumber}`,
                    sender: payer,
                    nonce: actionNumber,
                    timestamp: new Date(1800000000000 + actionNumber * 1000).toISOString(),
                },
                signature: 'aa',
                public_key: 'bb',
            };
            if (options.onPrepared) await options.onPrepared(envelope);
            const action = envelope.action;
            actions.push(action);
            if (action.action === 'open') {
                remote.value = {
                    stream_id: action.stream_id,
                    payer,
                    provider: action.provider,
                    service_id: action.service_id,
                    device_id_hash: action.device_id_hash,
                    session_public_key: action.session_public_key,
                    price_dust: action.price_dust,
                    price_period_seconds: action.price_period_seconds,
                    budget_dust: action.budget_dust,
                    max_active_seconds: action.max_active_seconds,
                    expires_at: action.expires_at,
                    status: 'active',
                    cumulative_active_seconds: 0,
                    last_receipt_sequence: 0,
                    last_action_id: action.id,
                };
            } else {
                if (action.action === 'pause') remote.value.status = 'paused';
                if (action.action === 'resume') remote.value.status = 'active';
                if (action.action === 'close') remote.value.status = 'closed';
                remote.value.last_action_id = action.id;
            }
            return {
                envelope,
                submission: { status: 'accepted' },
                stream: { ...remote.value },
            };
        },
    };
}

function createSessionSigner() {
    return {
        publicKeyHex: sessionPublicKey,
        async sign(bytes) {
            assert.ok(bytes instanceof Uint8Array);
            return new Uint8Array(64).fill(0x5a);
        },
    };
}

test('canonical signing bytes match the Go JSON field order', () => {
    const receipt = {
        stream_id: 'vpn-device-001',
        sequence: 1,
        cumulative_active_seconds: 30,
        observed_at: '2027-01-15T08:00:00.000Z',
        signature: '55'.repeat(64),
    };
    assert.equal(
        Buffer.from(streamReceiptSigningBytes(receipt)).toString('utf8'),
        '{"stream_id":"vpn-device-001","sequence":1,' +
        '"cumulative_active_seconds":30,"observed_at":"2027-01-15T08:00:00.000Z"}'
    );

    const action = {
        id: 'action-1',
        sender: payer,
        stream_id: 'vpn-device-001',
        action: 'receipt',
        receipt,
        nonce: 7,
        timestamp: '2027-01-15T08:00:01.000Z',
    };
    assert.equal(
        Buffer.from(streamActionSigningBytes(action)).toString('utf8'),
        JSON.stringify(action)
    );
});

test('receipt signing bytes produce a valid Ed25519 session signature', () => {
    const keyPair = crypto.generateKeyPairSync('ed25519');
    const receipt = {
        stream_id: 'vpn-device-001',
        sequence: 1,
        cumulative_active_seconds: 30,
        observed_at: '2027-01-15T08:00:00.000Z',
    };
    const bytes = Buffer.from(streamReceiptSigningBytes(receipt));
    const signature = crypto.sign(null, bytes, keyPair.privateKey);
    assert.equal(signature.byteLength, 64);
    assert.equal(crypto.verify(null, bytes, keyPair.publicKey, signature), true);
});

test('CellStreamWallet reads the nonce, signs canonical bytes, and submits', async () => {
    let submitted = null;
    let signedJSON = '';
    const client = {
        async getStreamActionNonce(address) {
            assert.equal(address, payer);
            return { sender: payer, action_nonce: 7, present: true };
        },
        async submitStreamAction(envelope) {
            submitted = envelope;
            return { status: 'accepted', action_id: envelope.action.id };
        },
        async getStream() {
            throw new Error('confirmation disabled');
        },
    };
    const wallet = new CellStreamWallet({
        client,
        address: payer,
        clock: () => 1800000000000,
        idFactory: () => 'action-fixed',
        confirmationTimeoutMs: 0,
        async signAction(_action, bytes) {
            signedJSON = Buffer.from(bytes).toString('utf8');
            return { signature: 'aa', publicKey: 'bb' };
        },
    });
    const result = await wallet.submitAction({
        stream_id: 'vpn-device-001',
        action: 'pause',
    });
    assert.equal(result.envelope.action.nonce, 7);
    assert.equal(result.envelope.action.id, 'action-fixed');
    assert.equal(signedJSON, JSON.stringify(result.envelope.action));
    assert.deepEqual(submitted, result.envelope);
});

test('CellStreamWallet accepts the first consensus action nonce', async () => {
    const client = {
        async getStreamActionNonce(address) {
            assert.equal(address, payer);
            return { sender: payer, action_nonce: 0, present: true };
        },
        async submitStreamAction(envelope) {
            return { status: 'accepted', action_id: envelope.action.id };
        },
        async getStream() {
            throw new Error('confirmation disabled');
        },
    };
    const wallet = new CellStreamWallet({
        client,
        address: payer,
        clock: () => 1800000000000,
        idFactory: () => 'action-first',
        confirmationTimeoutMs: 0,
        async signAction() {
            return { signature: 'aa', publicKey: 'bb' };
        },
    });
    const result = await wallet.submitAction({
        stream_id: 'vpn-device-001',
        action: 'pause',
    });
    assert.equal(Object.hasOwn(result.envelope.action, 'nonce'), false);
});

test('CellStreamWallet legacy nonce fallback uses current, never transfer next', async () => {
    const client = {
        async getWalletNonce() {
            return { sender: payer, nonce: 4, next: 5 };
        },
        async submitStreamAction(envelope) {
            return { status: 'accepted', action_id: envelope.action.id };
        },
        async getStream() {
            throw new Error('confirmation disabled');
        },
    };
    const wallet = new CellStreamWallet({
        client,
        address: payer,
        clock: () => 1800000000000,
        idFactory: () => 'action-legacy',
        confirmationTimeoutMs: 0,
        async signAction() {
            return { signature: 'aa', publicKey: 'bb' };
        },
    });
    const result = await wallet.submitAction({
        stream_id: 'vpn-device-001',
        action: 'pause',
    });
    assert.equal(result.envelope.action.nonce, 4);
});

test('service lifecycle counts only active time and emits cumulative receipts', async () => {
    let now = 1800000000000;
    const storage = new MemoryStorage();
    const remote = { value: null };
    const actions = [];
    const receipts = [];
    const wallet = createFakeWallet(remote, actions);
    const meter = new CellStreamServiceMeter({
        wallet,
        storage,
        storageKey: 'lifecycle',
        sessionSigner: createSessionSigner(),
        receiptIntervalSeconds: 30,
        autoSchedule: false,
        clock: () => now,
        async receiptSubmitter(receipt) {
            receipts.push({ ...receipt });
            remote.value.last_receipt_sequence = receipt.sequence;
            remote.value.cumulative_active_seconds = receipt.cumulative_active_seconds;
            return { confirmed: true };
        },
    });

    await meter.initialize();
    await meter.onServiceStarted(openConfig(now));
    now += 30000;
    await meter.checkpoint();
    assert.equal(receipts.length, 1);
    assert.equal(receipts[0].sequence, 1);
    assert.equal(receipts[0].cumulative_active_seconds, 30);

    now += 5000;
    await meter.onServiceStopped();
    assert.equal(receipts[1].sequence, 2);
    assert.equal(receipts[1].cumulative_active_seconds, 35);
    assert.equal(actions.at(-1).action, 'pause');

    now += 600000;
    await meter.onServiceStarted();
    now += 10000;
    await meter.close();

    assert.equal(receipts[2].sequence, 3);
    assert.equal(receipts[2].cumulative_active_seconds, 45);
    assert.equal(actions.at(-2).action, 'resume');
    assert.equal(actions.at(-1).action, 'close');
    assert.equal(meter.snapshot().stream.chainStatus, 'closed');
    assert.equal(meter.snapshot().stream.cumulativeActiveSeconds, 45);
});

test('crash recovery never bills process downtime', async () => {
    let now = 1800000000000;
    const storage = new MemoryStorage();
    const remote = { value: null };
    const actions = [];
    const receipts = [];
    const wallet = createFakeWallet(remote, actions);
    const options = {
        wallet,
        storage,
        storageKey: 'crash',
        sessionSigner: createSessionSigner(),
        receiptIntervalSeconds: 30,
        autoSchedule: false,
        clock: () => now,
        async receiptSubmitter(receipt) {
            receipts.push({ ...receipt });
            remote.value.last_receipt_sequence = receipt.sequence;
            remote.value.cumulative_active_seconds = receipt.cumulative_active_seconds;
            return { confirmed: true };
        },
    };

    const first = new CellStreamServiceMeter(options);
    await first.initialize();
    await first.onServiceStarted(openConfig(now));
    now += 7000;
    await first.checkpoint({ flush: false });
    await first.dispose();

    const restored = new CellStreamServiceMeter(options);
    const loaded = await restored.initialize();
    assert.equal(loaded.requiresRecovery, true);
    assert.equal(loaded.stream.cumulativeActiveSeconds, 7);

    now += 60 * 60 * 1000;
    await restored.recover(false);
    assert.equal(receipts.length, 1);
    assert.equal(receipts[0].cumulative_active_seconds, 7);
    assert.equal(restored.snapshot().stream.cumulativeActiveSeconds, 7);
    assert.equal(actions.at(-1).action, 'pause');
});

test('an uncertain receipt is persisted and retried with the same signature', async () => {
    let now = 1800000000000;
    let failReceipt = true;
    const storage = new MemoryStorage();
    const remote = { value: null };
    const actions = [];
    const attempts = [];
    const wallet = createFakeWallet(remote, actions);
    const options = {
        wallet,
        storage,
        storageKey: 'receipt-retry',
        sessionSigner: createSessionSigner(),
        autoSchedule: false,
        clock: () => now,
        async receiptSubmitter(receipt) {
            attempts.push({ ...receipt });
            if (failReceipt) throw new Error('provider unavailable');
            remote.value.last_receipt_sequence = receipt.sequence;
            remote.value.cumulative_active_seconds = receipt.cumulative_active_seconds;
            return { confirmed: true };
        },
    };

    const first = new CellStreamServiceMeter(options);
    await first.initialize();
    await first.onServiceStarted(openConfig(now));
    now += 10000;
    await assert.rejects(
        first.onServiceStopped(),
        /provider unavailable/
    );
    assert.ok(first.snapshot().stream.pendingReceipt);
    assert.equal(first.snapshot().stream.localActive, false);

    failReceipt = false;
    const restored = new CellStreamServiceMeter(options);
    await restored.initialize();
    await restored.recover(false);

    assert.equal(attempts.length, 2);
    assert.deepEqual(attempts[1], attempts[0]);
    assert.equal(restored.snapshot().stream.pendingReceipt, null);
    assert.equal(actions.at(-1).action, 'pause');
});

test('durable state contains no wallet or session private key material', async () => {
    const now = 1800000000000;
    const storage = new MemoryStorage();
    const remote = { value: null };
    const wallet = createFakeWallet(remote, []);
    const meter = new CellStreamServiceMeter({
        wallet,
        storage,
        storageKey: 'no-secrets',
        sessionSigner: {
            publicKeyHex: sessionPublicKey,
            privateKeyForTest: 'must-not-be-persisted',
            async sign() {
                return '66'.repeat(64);
            },
        },
        receiptSubmitter: async () => ({ confirmed: true }),
        autoSchedule: false,
        clock: () => now,
    });
    await meter.initialize();
    await meter.onServiceStarted(openConfig(now));
    const raw = storage.getItem('no-secrets');
    assert.ok(raw.includes(sessionPublicKey));
    assert.ok(!raw.includes('must-not-be-persisted'));
});
