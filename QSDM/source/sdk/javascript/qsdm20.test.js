// QSDM-20 tokens in the JavaScript SDK. Run with: node --test qsdm20.test.js
//
// The vectors come from ../go/testdata/qsdm20_vectors.json, which the node's
// own test (pkg/chain TestQSDM20SDKVectors) computes with the node's code
// and keeps byte-equal to the file. Matching them means signing exactly the
// 32 bytes a node verifies.

const test = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const http = require('node:http');
const path = require('node:path');
const qsdm = require('./qsdm.js');

const vectors = JSON.parse(fs.readFileSync(path.join(__dirname, '..', 'go', 'testdata', 'qsdm20_vectors.json'), 'utf8'));

const TOKEN = 'cd'.repeat(32);
const PAYEE = '11'.repeat(32);
const PUBLIC_KEY_BYTES = 2592;
const SIGNATURE_BYTES = 4627;

function hex(bytes) {
    return Buffer.from(bytes).toString('hex');
}

// A stand-in for an ML-DSA-87 key: the "signature" is SHA-256 chained to the
// signature size over (public key, message), so the fake node can check
// exactly which bytes were signed.
function fakeKey(seed) {
    const publicKey = new Uint8Array(PUBLIC_KEY_BYTES).fill(seed);
    return {
        publicKey,
        address: crypto.createHash('sha256').update(publicKey).digest('hex'),
        sign: (message) => fakeSignature(publicKey, message),
    };
}

function fakeSignature(publicKey, message) {
    let block = crypto.createHash('sha256').update(Buffer.from(publicKey)).update(Buffer.from(message)).digest();
    const out = [];
    let length = 0;
    while (length < SIGNATURE_BYTES) {
        out.push(block);
        length += block.length;
        block = crypto.createHash('sha256').update(block).digest();
    }
    return new Uint8Array(Buffer.concat(out).subarray(0, SIGNATURE_BYTES));
}

function startServer(handler) {
    return new Promise((resolve) => {
        const srv = http.createServer(handler);
        srv.listen(0, '127.0.0.1', () => {
            const { address, port } = srv.address();
            resolve({ srv, baseURL: `http://${address}:${port}` });
        });
    });
}

function stopServer(srv) {
    return new Promise((resolve) => srv.close(() => resolve()));
}

function readBody(req) {
    return new Promise((resolve) => {
        const chunks = [];
        req.on('data', (c) => chunks.push(c));
        req.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    });
}

test('QSDM-20 helpers are exported', () => {
    for (const name of ['validateQSDM20Action', 'qsdm20TokenId', 'qsdm20SigningPreimage',
        'qsdm20SigningDigest', 'qsdm20TxId', 'walletAddress']) {
        assert.equal(typeof qsdm[name], 'function', name);
    }
    assert.equal(qsdm.QSDM20_CONTRACT_ID, 'qsdm/token/v1');
    assert.equal(BigInt(qsdm.QSDM20_MAX_UNITS), (BigInt(1) << BigInt(256)) - BigInt(1));
});

test('token ids match the node', () => {
    assert.ok(vectors.token_ids.length >= 4);
    for (const v of vectors.token_ids) {
        assert.equal(qsdm.qsdm20TokenId(v.creator, v.nonce), v.id, `${v.creator.slice(0, 8)} ${v.nonce}`);
        assert.equal(qsdm.qsdm20TokenId(v.creator, BigInt(v.nonce)), v.id);
    }
    assert.throws(() => qsdm.qsdm20TokenId('AB'.repeat(32), 1), /creator/);
    assert.throws(() => qsdm.qsdm20TokenId(TOKEN, '18446744073709551616'), /2\^64/);
    assert.throws(() => qsdm.qsdm20TokenId(TOKEN, 2 ** 53), /nonce/);
    assert.throws(() => qsdm.qsdm20TokenId(TOKEN, '01'), /nonce/);
});

test('signing preimages, digests and ids match the node', () => {
    assert.ok(vectors.transactions.length >= 8);
    for (const v of vectors.transactions) {
        const tx = { sender: v.sender, nonce: v.nonce, fee_dust: v.fee_dust, payload: v.payload };
        assert.equal(qsdm.qsdm20SigningPreimage(tx), v.preimage, v.name);
        const digest = qsdm.qsdm20SigningDigest(tx);
        assert.ok(digest instanceof Uint8Array && digest.length === 32);
        assert.equal(hex(digest), v.digest, v.name);
        assert.equal(qsdm.qsdm20TxId(digest), v.tx_id);
        assert.equal(qsdm.qsdm20TxId(v.digest), v.tx_id);
        // feeDust spelled the JavaScript way and bigint values give the same digest.
        const bigints = { sender: v.sender, nonce: BigInt(v.nonce), feeDust: BigInt(v.fee_dust), payload: v.payload };
        assert.equal(hex(qsdm.qsdm20SigningDigest(bigints)), v.digest, v.name);
        if (v.payload.action === 'create') {
            assert.equal(qsdm.qsdm20TokenId(v.sender, v.nonce), v.token, v.name);
        } else {
            assert.equal(v.payload.token, v.token);
        }
        // validateQSDM20Action keeps the payload as the node reads it.
        assert.deepEqual(qsdm.validateQSDM20Action(v.payload), v.payload, v.name);
    }
});

test('validateQSDM20Action refuses every payload the node refuses', () => {
    assert.ok(vectors.invalid_payloads.length >= 30);
    for (const v of vectors.invalid_payloads) {
        assert.throws(() => qsdm.validateQSDM20Action(v.payload), TypeError, v.name);
    }
    // Client-side strictness beyond the node: a lone surrogate would be
    // stored as U+FFFD, not as the name that was signed.
    assert.throws(() => qsdm.validateQSDM20Action({
        action: 'create', symbol: 'G', name: 'a\ud800b', decimals: 0, max_supply: '1', initial_supply: '1', mint: 'none',
    }), /name/);
    assert.throws(() => qsdm.validateQSDM20Action({ action: 'constructor', token: TOKEN }), /action must be/);
    assert.throws(() => qsdm.validateQSDM20Action(null), /action object/);
});

test('validateQSDM20Action takes bigint amounts and drops empty fields', () => {
    assert.deepEqual(qsdm.validateQSDM20Action({ action: 'transfer', token: TOKEN, to: PAYEE, amount: BigInt(1000), symbol: '' }),
        { action: 'transfer', token: TOKEN, to: PAYEE, amount: '1000' });
    assert.deepEqual(qsdm.validateQSDM20Action({
        action: 'create', symbol: 'GROOT', name: 'Groot', decimals: 0, max_supply: BigInt(10), initial_supply: '0',
        mint: 'issuer', mint_limit: null, token: '',
    }), { action: 'create', symbol: 'GROOT', name: 'Groot', decimals: 0, max_supply: '10', initial_supply: '0', mint: 'issuer' });
    assert.throws(() => qsdm.validateQSDM20Action({ action: 'burn', token: TOKEN, amount: 5 }), /not a number/);
    assert.throws(() => qsdm.validateQSDM20Action({ action: 'burn', token: TOKEN, amount: BigInt(-1) }), /negative/);
});

// Every padding case of the hash: preimage lengths over more than 64
// consecutive values, checked against Node's own SHA-256.
test('the built-in SHA-256 matches node:crypto', () => {
    for (let digits = 1; digits <= 19; digits += 1) {
        for (let n = 1; n <= 48; n += 1) {
            for (const unit of ['a', 'é', '€', '🚀']) {
                const tx = {
                    sender: PAYEE,
                    nonce: 0,
                    fee_dust: '9'.repeat(digits),
                    payload: { action: 'create', symbol: 'A', name: unit.repeat(n), decimals: 0, max_supply: '1', initial_supply: '1', mint: 'none' },
                };
                const want = crypto.createHash('sha256').update(qsdm.qsdm20SigningPreimage(tx), 'utf8').digest('hex');
                assert.equal(hex(qsdm.qsdm20SigningDigest(tx)), want, `${digits} ${n} ${unit}`);
            }
        }
    }
    for (let seed = 0; seed < 8; seed += 1) {
        const key = crypto.randomBytes(PUBLIC_KEY_BYTES);
        assert.equal(qsdm.walletAddress(key), crypto.createHash('sha256').update(key).digest('hex'));
        assert.equal(qsdm.walletAddress(key.toString('hex').toUpperCase()), crypto.createHash('sha256').update(key).digest('hex'));
    }
    assert.throws(() => qsdm.walletAddress(new Uint8Array(10)), /2592/);
});

test('sendQSDM20 reads the nonce, signs the digest and submits the body the node reads', async () => {
    const key = fakeKey(5);
    let submitted = null;
    let rawBody = '';
    const { srv, baseURL } = await startServer(async (req, res) => {
        if (req.method === 'GET' && req.url === `/api/v1/mining/account?address=${key.address}`) {
            res.writeHead(200, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({ address: key.address, balance: 12.5, nonce: 41, present: true }));
            return;
        }
        if (req.method === 'POST' && req.url === '/api/v1/qsdm20/submit') {
            rawBody = await readBody(req);
            submitted = JSON.parse(rawBody);
            const digest = qsdm.qsdm20SigningDigest(submitted);
            assert.equal(submitted.signature, hex(fakeSignature(Buffer.from(submitted.public_key, 'hex'), digest)));
            res.writeHead(200, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify({
                tx_id: qsdm.qsdm20TxId(digest), action: submitted.payload.action, sender: submitted.sender,
                nonce: submitted.nonce, token: qsdm.qsdm20TokenId(submitted.sender, submitted.nonce),
                status: 'accepted', mempool_status: 'submitted',
            }));
            return;
        }
        res.writeHead(404);
        res.end('not found');
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        const payload = {
            action: 'create', symbol: 'GROOT', name: 'Groot <Coin> & co', decimals: 8,
            max_supply: '1000000', initial_supply: BigInt(250), mint: 'issuer',
        };
        let signedDigest = null;
        const response = await client.sendQSDM20({
            sender: key.address,
            payload,
            feeDust: 2500,
            signQSDM20: async (digest, unsigned) => {
                signedDigest = hex(digest);
                assert.equal(unsigned.nonce, '41');
                return { signature: key.sign(digest), publicKey: key.publicKey };
            },
        });
        assert.equal(response.token, qsdm.qsdm20TokenId(key.address, 41));
        assert.equal(response.mempool_status, 'submitted');
        assert.equal(submitted.nonce, 41);
        assert.equal(submitted.fee_dust, 2500);
        assert.equal(submitted.payload.initial_supply, '250');
        assert.equal(signedDigest, hex(qsdm.qsdm20SigningDigest(submitted)));
        assert.match(rawBody, /"nonce":41,"fee_dust":2500,/);
        assert.deepEqual(Object.keys(submitted), ['sender', 'nonce', 'fee_dust', 'payload', 'public_key', 'signature']);
    } finally {
        await stopServer(srv);
    }
});

test('submitQSDM20 writes 64-bit nonces exactly and checks the answered id', async () => {
    const key = fakeKey(6);
    let rawBody = '';
    let answer = null;
    const { srv, baseURL } = await startServer(async (req, res) => {
        rawBody = await readBody(req);
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify(answer));
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        const unsigned = await client.prepareQSDM20({
            sender: key.address, nonce: '18446744073709551615', feeDust: BigInt(7),
            payload: { action: 'renounce', token: TOKEN },
        });
        assert.deepEqual(unsigned, { sender: key.address, nonce: '18446744073709551615', fee_dust: '7', payload: { action: 'renounce', token: TOKEN } });
        const digest = qsdm.qsdm20SigningDigest(unsigned);
        const signed = { ...unsigned, public_key: hex(key.publicKey), signature: key.sign(digest) };
        answer = { tx_id: qsdm.qsdm20TxId(digest), status: 'accepted' };
        const response = await client.submitQSDM20(signed);
        assert.equal(response.tx_id, qsdm.qsdm20TxId(digest));
        assert.match(rawBody, /"nonce":18446744073709551615,"fee_dust":7,/);

        answer = { tx_id: 'qsdm20-00', status: 'accepted' };
        await assert.rejects(client.submitQSDM20(signed), /answered tx_id/);
        await assert.rejects(client.submitQSDM20(unsigned), /not signed/);
    } finally {
        await stopServer(srv);
    }
});

test('prepare, send and submit refuse what cannot work', async () => {
    const key = fakeKey(7);
    const other = fakeKey(8);
    let account = { address: key.address, balance: 0, nonce: 0, present: false };
    const { srv, baseURL } = await startServer((req, res) => {
        if (req.url.startsWith('/api/v1/mining/account')) {
            res.writeHead(200, { 'Content-Type': 'application/json' });
            res.end(JSON.stringify(account));
            return;
        }
        res.writeHead(422, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ error: 'chain: QSDM-20 token transactions are not active' }));
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        const payload = { action: 'transfer', token: TOKEN, to: PAYEE, amount: '1' };
        await assert.rejects(client.prepareQSDM20({ sender: key.address, payload }), /no CELL account/);
        account = { address: other.address, balance: 1, nonce: 3, present: true };
        await assert.rejects(client.prepareQSDM20({ sender: key.address, payload }), /inconsistent account/);
        await assert.rejects(client.prepareQSDM20({ sender: 'AB', payload }), /sender/);
        await assert.rejects(client.prepareQSDM20({ sender: key.address, payload: { ...payload, amount: '0' } }), /amount/);
        await assert.rejects(client.sendQSDM20({ sender: key.address, payload }), /signQSDM20/);

        account = { address: key.address, balance: 1, nonce: 3, present: true };
        await assert.rejects(client.sendQSDM20({
            sender: key.address,
            payload,
            signQSDM20: (digest) => ({ signature: other.sign(digest), public_key: other.publicKey }),
        }), /does not belong/);
        await assert.rejects(client.sendQSDM20({
            sender: key.address,
            payload,
            signQSDM20: (digest) => ({ signature: key.sign(digest).subarray(1), public_key: key.publicKey }),
        }), /4627/);
        await assert.rejects(client.sendQSDM20({
            sender: key.address,
            payload,
            signQSDM20: (digest) => ({ signature: key.sign(digest), public_key: key.publicKey }),
        }), (err) => err instanceof qsdm.ApiError && err.status === 422 && /not active/.test(err.body));
    } finally {
        await stopServer(srv);
    }
});

test('the QSDM-20 reads use the documented paths and cursors', async () => {
    const seen = [];
    const { srv, baseURL } = await startServer((req, res) => {
        seen.push(req.url);
        res.writeHead(200, { 'Content-Type': 'application/json' });
        res.end(JSON.stringify({ state_root: 'r', ok: true }));
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        await client.getQSDM20Tokens();
        await client.getQSDM20Tokens({ cursor: `5:${TOKEN}`, limit: 2 });
        await client.getQSDM20Token(TOKEN);
        await client.getQSDM20Holders(TOKEN, { limit: 50 });
        await client.getQSDM20Balances(PAYEE);
        await client.getAccount(PAYEE);
        assert.deepEqual(seen, [
            '/api/v1/qsdm20/tokens',
            `/api/v1/qsdm20/tokens?cursor=5%3A${TOKEN}&limit=2`,
            `/api/v1/qsdm20/tokens/${TOKEN}`,
            `/api/v1/qsdm20/tokens/${TOKEN}/holders?limit=50`,
            `/api/v1/qsdm20/balances/${PAYEE}`,
            `/api/v1/mining/account?address=${PAYEE}`,
        ]);
    } finally {
        await stopServer(srv);
    }
});
