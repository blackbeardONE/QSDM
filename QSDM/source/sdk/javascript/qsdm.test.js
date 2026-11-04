// Node.js built-in test for the QSDM SDK.
// Run with: node --test qsdm.test.js
//
// Covers every public method on QSDMClient plus the package-level helpers
// (`ApiError`, `TransferError`, `isNotFound`, `isUnauthorized`,
// `isPoERejection`, `transferSigningBytes`, the wallet-provider helpers) and
// configuration knobs (`setToken`, `setAPIKey`, baseURL trim, per-request
// timeout).

const test = require('node:test');
const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const http = require('node:http');
const qsdm = require('./qsdm.js');

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

// --- exports + smoke -------------------------------------------------------

test('QSDMClient and helpers are exported', () => {
    assert.equal(typeof qsdm.QSDMClient, 'function');
    assert.equal(typeof qsdm.ApiError, 'function');
    assert.equal(typeof qsdm.TransferError, 'function');
    assert.equal(typeof qsdm.isNotFound, 'function');
    assert.equal(typeof qsdm.isUnauthorized, 'function');
    assert.equal(typeof qsdm.isPoERejection, 'function');
    assert.equal(typeof qsdm.transferSigningBytes, 'function');
    assert.equal(typeof qsdm.getWalletProvider, 'function');
    assert.equal(typeof qsdm.waitForWalletProvider, 'function');
    assert.equal(typeof qsdm.sendTransferWithProvider, 'function');
    assert.equal(typeof qsdm.CellStreamWallet, 'function');
    assert.equal(typeof qsdm.CellStreamServiceMeter, 'function');
});

test('ApiError carries status / url / body and supports the type guards', () => {
    const err404 = new qsdm.ApiError(404, 'http://x/y', 'not found');
    assert.equal(err404.status, 404);
    assert.equal(err404.url, 'http://x/y');
    assert.equal(err404.body, 'not found');
    assert.ok(qsdm.isNotFound(err404));
    assert.ok(!qsdm.isUnauthorized(err404));

    const err401 = new qsdm.ApiError(401, 'http://x/y', 'unauth');
    assert.ok(qsdm.isUnauthorized(err401));
    assert.ok(!qsdm.isNotFound(err401));

    const err403 = new qsdm.ApiError(403, 'http://x/y', 'forbidden');
    assert.ok(qsdm.isUnauthorized(err403));

    assert.ok(!qsdm.isNotFound(new Error('plain')));
    assert.ok(!qsdm.isUnauthorized(new Error('plain')));
});

test('constructor rejects empty baseURL and trims trailing slashes', () => {
    assert.throws(() => new qsdm.QSDMClient(''), /baseURL is required/);
    assert.throws(() => new qsdm.QSDMClient(null), /baseURL is required/);

    const c1 = new qsdm.QSDMClient('http://node:8080/');
    assert.equal(c1.baseURL, 'http://node:8080');
    const c2 = new qsdm.QSDMClient('http://node:8080///');
    assert.equal(c2.baseURL, 'http://node:8080');
});

// --- wallet + tx -----------------------------------------------------------

test('getBalance issues a GET to /api/v1/wallet/balance and unwraps the JSON', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(req.method, 'GET');
        assert.ok(req.url.startsWith('/api/v1/wallet/balance'));
        assert.ok(req.url.includes('address=addr-1'));
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ balance: 1.23 }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        const v = await c.getBalance('addr-1');
        assert.equal(v, 1.23);
    } finally {
        await stopServer(srv);
    }
});

test('getWalletNonce returns the next wallet action nonce', async () => {
    const address = '11'.repeat(32);
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(
            req.url,
            `/api/v1/wallet/nonce?sender=${address}`
        );
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ sender: address, nonce: 8, next: 9 }));
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        const out = await client.getWalletNonce(address);
        assert.equal(out.sender, address);
        assert.equal(out.nonce, 8);
        assert.equal(out.next, 9);
    } finally {
        await stopServer(srv);
    }
});

test('getStreamActionNonce returns the current contract action nonce', async () => {
    const address = '22'.repeat(32);
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(
            req.url,
            `/api/v1/streams/nonce?sender=${address}`
        );
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({
            runtime: 'qsdm-native',
            source: 'chain',
            sender: address,
            action_nonce: 0,
            present: false,
        }));
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        const out = await client.getStreamActionNonce(address);
        assert.equal(out.sender, address);
        assert.equal(out.action_nonce, 0);
        assert.equal(out.present, false);
    } finally {
        await stopServer(srv);
    }
});

test('sendTransaction (deprecated, unsigned) rejects without contacting the node', async () => {
    let requests = 0;
    const { srv, baseURL } = await startServer((req, res) => {
        requests += 1;
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ transaction_id: 'tx-42' }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        await assert.rejects(
            () => c.sendTransaction('a', 'b', 5),
            (err) => {
                assert.equal(err.code, 'QSDM_UNSIGNED_SEND_REMOVED');
                assert.match(err.message, /submit-signed/);
                assert.match(err.message, /sendTransferWithProvider/);
                return true;
            }
        );
        assert.equal(requests, 0);
    } finally {
        await stopServer(srv);
    }
});

test('getTransaction hits /api/v1/transactions/{id} (plural; pinned to actual handler path)', async () => {
    // Path pin matches pkg/api/handlers.go:269-270 mux registration.
    // 0.3.0 and earlier hit the singular /api/v1/transaction/{id},
    // which 404s in production. Fixed in 0.3.1 — this assertion is
    // the regression guard.
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(req.url, '/api/v1/transactions/tx-7');
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ id: 'tx-7', amount: 3.14 }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        const out = await c.getTransaction('tx-7');
        assert.equal(out.id, 'tx-7');
        assert.equal(out.amount, 3.14);
    } finally {
        await stopServer(srv);
    }
});

test('getRecentTransactions passes address + limit query params', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        assert.ok(req.url.includes('address=addr-9'));
        assert.ok(req.url.includes('limit=25'));
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ transactions: [{ id: 't1' }, { id: 't2' }] }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        const out = await c.getRecentTransactions('addr-9', 25);
        assert.ok(out && Array.isArray(out.transactions));
        assert.equal(out.transactions.length, 2);
    } finally {
        await stopServer(srv);
    }
});

test('CELL stream methods use the public stream endpoints', async () => {
    const seen = [];
    const { srv, baseURL } = await startServer((req, res) => {
        let body = '';
        req.on('data', (chunk) => { body += chunk; });
        req.on('end', () => {
            seen.push({ method: req.method, url: req.url, body });
            res.setHeader('Content-Type', 'application/json');
            if (req.method === 'POST') {
                res.end(JSON.stringify({ status: 'accepted', action_id: 'a1' }));
            } else if (req.url.startsWith('/api/v1/streams/stream-1')) {
                res.end(JSON.stringify({ stream: { stream_id: 'stream-1' } }));
            } else {
                res.end(JSON.stringify({ streams: [] }));
            }
        });
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        await client.getStreams({ payer: 'payer 1', status: 'active', serviceId: 'qsdm-vpn' });
        await client.getStream('stream-1');
        const envelope = { action: { id: 'a1' }, signature: 'aa', public_key: 'bb' };
        await client.submitStreamAction(envelope);
        assert.equal(seen[0].method, 'GET');
        assert.ok(seen[0].url.includes('/api/v1/streams?'));
        assert.ok(seen[0].url.includes('payer=payer%201'));
        assert.ok(seen[0].url.includes('service_id=qsdm-vpn'));
        assert.equal(seen[1].url, '/api/v1/streams/stream-1');
        assert.equal(seen[2].method, 'POST');
        assert.equal(seen[2].url, '/api/v1/streams/actions/submit-signed');
        assert.deepEqual(JSON.parse(seen[2].body), envelope);
    } finally {
        await stopServer(srv);
    }
});

// --- health + node + network ----------------------------------------------

test('liveness / readiness / health hit the right endpoints', async () => {
    const seen = [];
    const { srv, baseURL } = await startServer((req, res) => {
        seen.push(req.url);
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ status: 'ok' }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        await c.getLiveness();
        await c.getReadiness();
        await c.getHealth();
        assert.deepEqual(seen, [
            '/api/v1/health/live',
            '/api/v1/health/ready',
            '/api/v1/health',
        ]);
    } finally {
        await stopServer(srv);
    }
});

test('getNodeStatus maps snake_case JSON into the typed projection', async () => {
    const raw = {
        node_id: 'n1',
        version: '1.2.3',
        uptime: '5m',
        chain_tip: 42,
        peers: 7,
        node_role: 'validator',
        network: 'mainnet',
        coin: { name: 'Cell', symbol: 'CELL', decimals: 8, smallest_unit: 'dust' },
        branding: { name: 'QSDM', full_title: 'Quantum-Secure Dynamic Mesh' },
        tokenomics: {
            cap_dust: 1, cap_cell: 1, emitted_dust: 1, emitted_cell: 1,
            remaining_dust: 1, block_reward_dust: 1, block_reward_cell: 1,
            current_epoch: 1, next_halving_height: 1, next_halving_eta_seconds: 1,
            target_block_time_seconds: 10, blocks_per_epoch: 100,
        },
        custom_extra_field: 'preserved',
    };
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(req.url, '/api/v1/status');
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify(raw));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        const st = await c.getNodeStatus();
        assert.equal(st.nodeId, 'n1');
        assert.equal(st.chainTip, 42);
        assert.equal(st.coin.symbol, 'CELL');
        assert.equal(st.coin.smallestUnit, 'dust');
        assert.equal(st.branding.fullTitle, 'Quantum-Secure Dynamic Mesh');
        assert.equal(st.tokenomics.targetBlockTimeSeconds, 10);
        assert.equal(st.extra.custom_extra_field, 'preserved');
    } finally {
        await stopServer(srv);
    }
});

test('getPeers returns an array (or [] on missing field)', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        res.setHeader('Content-Type', 'application/json');
        if (req.url === '/api/v1/network/peers') {
            res.end(JSON.stringify({ peers: ['p1', 'p2'] }));
        } else {
            res.end(JSON.stringify({ /* peers field missing */ }));
        }
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        const peers = await c.getPeers();
        assert.deepEqual(peers, ['p1', 'p2']);
    } finally {
        await stopServer(srv);
    }
});

test('getNetworkTopology returns the raw topology JSON', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(req.url, '/api/v1/network/topology');
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ cells: [], links: [] }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        const t = await c.getNetworkTopology();
        assert.ok(Array.isArray(t.cells));
        assert.ok(Array.isArray(t.links));
    } finally {
        await stopServer(srv);
    }
});

// --- metrics ---------------------------------------------------------------

test('getMetricsJSON parses /api/metrics as JSON', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ qsdm_uptime_seconds: 99 }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        const m = await c.getMetricsJSON();
        assert.equal(m.qsdm_uptime_seconds, 99);
    } finally {
        await stopServer(srv);
    }
});

test('getMetricsPrometheus returns the raw exposition text', async () => {
    const expo = '# HELP qsdm_x test\n# TYPE qsdm_x gauge\nqsdm_x 1\n';
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(req.url, '/api/metrics/prometheus');
        res.setHeader('Content-Type', 'text/plain; version=0.0.4');
        res.end(expo);
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        const out = await c.getMetricsPrometheus();
        assert.equal(typeof out, 'string');
        assert.ok(out.includes('qsdm_x 1'));
    } finally {
        await stopServer(srv);
    }
});

// --- auth headers ----------------------------------------------------------

test('setToken injects a Bearer Authorization header', async () => {
    let seen = '';
    const { srv, baseURL } = await startServer((req, res) => {
        seen = req.headers['authorization'] || '';
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ balance: 0 }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        c.setToken('jwt-xyz');
        await c.getBalance('addr');
        assert.equal(seen, 'Bearer jwt-xyz');
    } finally {
        await stopServer(srv);
    }
});

test('setAPIKey injects X-API-Key (only when no bearer token is set)', async () => {
    let bearer = '';
    let apiKey = '';
    const { srv, baseURL } = await startServer((req, res) => {
        bearer = req.headers['authorization'] || '';
        apiKey = req.headers['x-api-key'] || '';
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ balance: 0 }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        c.setAPIKey('k-abc');
        await c.getBalance('addr');
        assert.equal(bearer, '');
        assert.equal(apiKey, 'k-abc');
    } finally {
        await stopServer(srv);
    }
});

// --- error paths -----------------------------------------------------------

test('non-2xx responses throw ApiError with body and status set', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        res.statusCode = 404;
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ error: 'no such address' }));
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        await assert.rejects(
            () => c.getBalance('missing'),
            (err) => {
                assert.ok(err instanceof qsdm.ApiError);
                assert.equal(err.status, 404);
                assert.ok(qsdm.isNotFound(err));
                assert.ok(err.body.includes('no such address'));
                return true;
            }
        );
    } finally {
        await stopServer(srv);
    }
});

test('per-request timeout aborts the fetch and rejects', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        // Never respond — let the client time out.
        setTimeout(() => res.end('{}'), 5000);
    });
    try {
        const c = new qsdm.QSDMClient(baseURL, { timeoutMs: 50 });
        await assert.rejects(() => c.getBalance('addr'));
    } finally {
        await stopServer(srv);
    }
});

// --- signed transfers -------------------------------------------------------

const PARENT_A = 'solo-heartbeat-2253090-1791611907090994535';
const PARENT_B = 'solo-heartbeat-2253089-1791611897111559959';
const PAYEE = '11'.repeat(32);

// Stands in for an ML-DSA-87 key: the "signature" is SHA-256 chained to the
// ML-DSA-87 size, so the fake node can check exactly which bytes were signed.
function fakeSigner(seed) {
    const publicKey = new Uint8Array(2592).fill(seed);
    const address = crypto.createHash('sha256').update(publicKey).digest('hex');
    const sign = (bytes) => {
        let block = crypto.createHash('sha256').update(publicKey).update(bytes).digest();
        const parts = [];
        let length = 0;
        while (length < 4627) {
            parts.push(block);
            length += block.length;
            block = crypto.createHash('sha256').update(block).digest();
        }
        return new Uint8Array(Buffer.concat(parts).subarray(0, 4627));
    };
    return { publicKey, address, sign };
}

function readBody(req) {
    return new Promise((resolve) => {
        let body = '';
        req.on('data', (chunk) => { body += chunk; });
        req.on('end', () => resolve(body));
    });
}

// A fake node serving the routes a signed transfer needs. Each submitted
// envelope is recorded; unless `submit` overrides the answer, the node checks
// that the signature covers the envelope's signing bytes.
async function startFakeNode({ parents = [PARENT_A, PARENT_B], nonce = 4, submit } = {}) {
    const seen = [];
    const submitted = [];
    const started = await startServer(async (req, res) => {
        seen.push(`${req.method} ${req.url}`);
        const body = await readBody(req);
        res.setHeader('Content-Type', 'application/json');
        const url = new URL(req.url, 'http://node');
        if (req.method === 'GET' && url.pathname === '/api/v1/wallet/nonce') {
            res.end(JSON.stringify({ sender: url.searchParams.get('sender'), nonce, next: nonce + 1 }));
        } else if (req.method === 'GET' && url.pathname === '/api/v1/chain/parents') {
            res.end(JSON.stringify({
                tip: 900000, poe_activation_height: 816600, poe_active: true,
                min_parents: 2, max_parents: 10, window_blocks: 8640, parents, recent: [],
            }));
        } else if (req.method === 'POST' && url.pathname === '/api/v1/wallet/submit-signed') {
            const envelope = JSON.parse(body);
            submitted.push(envelope);
            if (submit) {
                res.statusCode = submit.status;
                res.end(submit.body);
                return;
            }
            const publicKey = Buffer.from(envelope.public_key, 'hex');
            const expected = fakeSigner(publicKey[0]).sign(qsdm.transferSigningBytes(envelope));
            assert.equal(envelope.signature, Buffer.from(expected).toString('hex'));
            assert.equal(envelope.sender, crypto.createHash('sha256').update(publicKey).digest('hex'));
            res.statusCode = 202;
            res.end(JSON.stringify({ transaction_id: envelope.id, status: 'pending', broadcast: 'block-pending' }));
        } else {
            res.statusCode = 404;
            res.end('not found');
        }
    });
    return { ...started, seen, submitted };
}

test('transferSigningBytes matches the bytes the node verifies', () => {
    // Expected strings printed by the node's pkg/wallet
    // TransactionData.CanonicalBytes for the same envelopes; the Go SDK's
    // transfer_test.go pins the same three vectors.
    const sender = '5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7';
    // In goJSON text a | stands for a backslash: Go writes <, >, &, U+2028
    // and U+2029 inside strings as a backslash, "u" and four hex digits.
    const backslash = String.fromCharCode(92);
    const goJSON = (text) => text.split('|').join(backslash);
    const separators = String.fromCharCode(0x2028, 0x2029);
    const cases = [
        {
            envelope: {
                id: 'sdk_go_1790000000000_0123456789abcdef', sender, recipient: '11'.repeat(32),
                amount: 1.25, fee: 0, geotag: '', parent_cells: [PARENT_A, PARENT_B], nonce: 2,
                signature: 'aa', public_key: 'bb', timestamp: '2026-11-03T06:20:09.07Z',
            },
            want: String.raw`{"id":"sdk_go_1790000000000_0123456789abcdef","sender":"5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7","recipient":"1111111111111111111111111111111111111111111111111111111111111111","amount":1.25,"fee":0,"geotag":"","parent_cells":["solo-heartbeat-2253090-1791611907090994535","solo-heartbeat-2253089-1791611897111559959"],"nonce":2,"signature":"","timestamp":"2026-11-03T06:20:09.07Z"}`,
        },
        {
            envelope: {
                id: 'tx<&>id-0000000001', sender, recipient: '22'.repeat(32),
                amount: 1e-7, fee: 0.29, geotag: '<eu>&' + separators + 'é"' + backslash,
                timestamp: '2026-11-03T06:20:09.123456789Z',
            },
            want: goJSON('{"id":"tx|u003c|u0026|u003eid-0000000001","sender":"5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7","recipient":"2222222222222222222222222222222222222222222222222222222222222222","amount":1e-7,"fee":0.29,"geotag":"|u003ceu|u003e|u0026|u2028|u2029é|"||","parent_cells":null,"signature":"","timestamp":"2026-11-03T06:20:09.123456789Z"}'),
        },
        {
            envelope: {
                id: 'sdk_js_1790000000000_fedcba9876543210', sender, recipient: '33'.repeat(32),
                amount: 123456789.12345678, fee: 0.00000001, geotag: 'SG', parent_cells: [],
                nonce: 9007199254740991, timestamp: '2026-11-03T06:20:09Z',
            },
            want: String.raw`{"id":"sdk_js_1790000000000_fedcba9876543210","sender":"5d28b82565421e3666ef7a493e0d39fc89d3450009a32cbda72aa89c7cb820e7","recipient":"3333333333333333333333333333333333333333333333333333333333333333","amount":123456789.12345678,"fee":1e-8,"geotag":"SG","parent_cells":[],"nonce":9007199254740991,"signature":"","timestamp":"2026-11-03T06:20:09Z"}`,
        },
    ];
    for (const { envelope, want } of cases) {
        const bytes = qsdm.transferSigningBytes(envelope);
        assert.ok(bytes instanceof Uint8Array);
        assert.equal(Buffer.from(bytes).toString('utf8'), want);
    }
    assert.throws(
        () => qsdm.transferSigningBytes({ ...cases[0].envelope, geotag: 'bad \uD800 text' }),
        /not valid Unicode/
    );
});

test('sendTransfer reads nonce and PoE parents, signs, and submits (parents round trip)', async () => {
    const signer = fakeSigner(7);
    const node = await startFakeNode();
    try {
        const client = new qsdm.QSDMClient(node.baseURL);
        let signedBytes = null;
        const out = await client.sendTransfer({
            sender: signer.address,
            recipient: PAYEE,
            amount: 2.5,
            fee: 0.01,
            signTransfer: (envelope, signingBytes) => {
                assert.equal(envelope.signature, undefined);
                signedBytes = signingBytes;
                return { signature: signer.sign(signingBytes), publicKey: signer.publicKey };
            },
        });
        assert.equal(out.status, 'pending');
        assert.equal(out.broadcast, 'block-pending');
        assert.equal(node.submitted.length, 1);
        const envelope = node.submitted[0];
        assert.equal(out.transaction_id, envelope.id);
        assert.deepEqual(envelope.parent_cells, [PARENT_A, PARENT_B]);
        assert.equal(envelope.nonce, 5);
        assert.equal(envelope.amount, 2.5);
        assert.equal(envelope.fee, 0.01);
        assert.match(envelope.id, /^sdk_js_\d+_[0-9a-f]{16}$/);
        assert.match(envelope.timestamp, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/);
        assert.deepEqual(
            Buffer.from(signedBytes).toString('utf8'),
            Buffer.from(qsdm.transferSigningBytes(envelope)).toString('utf8')
        );
        assert.deepEqual(node.seen, [
            `GET /api/v1/wallet/nonce?sender=${signer.address}`,
            'GET /api/v1/chain/parents',
            'POST /api/v1/wallet/submit-signed',
        ]);
    } finally {
        await stopServer(node.srv);
    }
});

test('sendTransfer accepts a signed envelope from an external signer and refuses an altered one', async () => {
    const signer = fakeSigner(9);
    const node = await startFakeNode();
    try {
        const client = new qsdm.QSDMClient(node.baseURL);
        // The shape qsdmcli wallet sign-tx and the web wallet's WASM signer print.
        const out = await client.sendTransfer({
            sender: signer.address,
            recipient: PAYEE,
            amount: 1,
            signTransfer: (envelope, bytes) => JSON.stringify({
                ...envelope,
                signature: Buffer.from(signer.sign(bytes)).toString('hex'),
                public_key: Buffer.from(signer.publicKey).toString('hex'),
            }),
        });
        assert.equal(out.status, 'pending');
        await assert.rejects(
            () => client.sendTransfer({
                sender: signer.address,
                recipient: PAYEE,
                amount: 1,
                signTransfer: (envelope, bytes) => ({
                    ...envelope,
                    amount: 100,
                    signature: signer.sign(bytes),
                    public_key: signer.publicKey,
                }),
            }),
            /differs from the prepared transfer/
        );
        await assert.rejects(
            () => client.sendTransfer({
                sender: signer.address,
                recipient: PAYEE,
                amount: 1,
                signTransfer: () => ({ signature: 'abcd', public_key: signer.publicKey }),
            }),
            /4627 bytes/
        );
        assert.equal(node.submitted.length, 1);
    } finally {
        await stopServer(node.srv);
    }
});

test('a 422 Proof-of-Entanglement refusal is a TransferError that says what to do', async () => {
    const signer = fakeSigner(3);
    const node = await startFakeNode({
        nonce: 0,
        submit: {
            status: 422,
            body: JSON.stringify({
                error: 'Unprocessable Entity',
                message: 'proof-of-entanglement: proof-of-entanglement violation: parent is not a committed ' +
                    'transaction in the reference window: parent 0; sign again with parents from GET /api/v1/chain/parents',
                status: 422,
            }),
        },
    });
    try {
        const client = new qsdm.QSDMClient(node.baseURL);
        await assert.rejects(
            () => client.sendTransfer({
                sender: signer.address,
                recipient: PAYEE,
                amount: 1,
                signTransfer: (_envelope, bytes) => ({ signature: signer.sign(bytes), public_key: signer.publicKey }),
            }),
            (err) => {
                assert.ok(err instanceof qsdm.TransferError);
                assert.ok(err instanceof qsdm.ApiError);
                assert.equal(err.status, 422);
                assert.equal(err.reason, 'poe_parents');
                assert.ok(qsdm.isPoERejection(err));
                assert.ok(!qsdm.isNotFound(err));
                assert.match(err.serverMessage, /^proof-of-entanglement:/);
                assert.match(err.message, /fetch fresh parents/);
                return true;
            }
        );
        assert.deepEqual(node.submitted[0].parent_cells, [PARENT_A, PARENT_B]);
        assert.equal(node.submitted[0].nonce, 1);
    } finally {
        await stopServer(node.srv);
    }
});

test('submitSignedTransfer classifies every refusal', async () => {
    const signer = fakeSigner(1);
    const envelope = {
        id: 'sdk_js_1_0000000000000000', sender: signer.address, recipient: PAYEE, amount: 1, fee: 0,
        geotag: '', parent_cells: [PARENT_A, PARENT_B], nonce: 1, timestamp: '2026-11-03T00:00:00Z',
    };
    envelope.signature = Buffer.from(signer.sign(qsdm.transferSigningBytes(envelope))).toString('hex');
    envelope.public_key = Buffer.from(signer.publicKey).toString('hex');
    const cases = [
        [400, { error: 'Bad Request', message: 'envelope.sender does not match hex(sha256(public_key))', status: 400 }, 'invalid'],
        [402, { error: 'Payment Required', message: 'insufficient canonical CELL balance for amount + fee', status: 402 }, 'insufficient_balance'],
        [409, { transaction_id: 'tx-dup-000000000001', status: 'duplicate', broadcast: 'block-pending' }, 'duplicate'],
        [409, { error: 'Conflict', message: 'nonce gap: envelope nonce 7; next required nonce is 5', status: 409 }, 'nonce'],
        [422, { error: 'Unprocessable Entity', message: 'signature does not verify under envelope.public_key', status: 422 }, 'signature'],
        [429, { error: 'Too Many Requests', message: 'rate limit exceeded', status: 429 }, 'rate_limited'],
        [503, { error: 'Service Unavailable', message: 'network follower is read-only', status: 503 }, 'unavailable'],
        [500, 'internal error\n', 'other'],
    ];
    for (const [status, body, reason] of cases) {
        const node = await startFakeNode({
            submit: { status, body: typeof body === 'string' ? body : JSON.stringify(body) },
        });
        try {
            const client = new qsdm.QSDMClient(node.baseURL);
            await assert.rejects(() => client.submitSignedTransfer(envelope), (err) => {
                assert.ok(err instanceof qsdm.TransferError, `${status} -> ${err}`);
                assert.equal(err.status, status);
                assert.equal(err.reason, reason);
                assert.ok(err.serverMessage && !err.serverMessage.includes('"error"'));
                if (reason === 'duplicate') assert.equal(err.transactionId, 'tx-dup-000000000001');
                return true;
            });
        } finally {
            await stopServer(node.srv);
        }
    }
});

test('submitSignedTransfer refuses an unsigned envelope without a request', async () => {
    const node = await startFakeNode();
    try {
        const client = new qsdm.QSDMClient(node.baseURL);
        await assert.rejects(() => client.submitSignedTransfer({ id: 'x', sender: 's' }), /not signed/);
        assert.deepEqual(node.seen, []);
    } finally {
        await stopServer(node.srv);
    }
});

test('prepareTransfer validates locally and keeps caller values', async () => {
    const signer = fakeSigner(1);
    const node = await startFakeNode();
    try {
        const client = new qsdm.QSDMClient(node.baseURL);
        const bad = [
            { sender: signer.address, recipient: 'QSDM_RECIPIENT', amount: 1 },
            { sender: signer.address.toUpperCase(), recipient: PAYEE, amount: 1 },
            { sender: signer.address, recipient: PAYEE, amount: 0 },
            { sender: signer.address, recipient: PAYEE, amount: '1' },
            { sender: signer.address, recipient: PAYEE, amount: 1, fee: -1 },
            { sender: signer.address, recipient: PAYEE, amount: 0.123456789 },
            { sender: signer.address, recipient: PAYEE, amount: 1, nonce: -2 },
        ];
        for (const request of bad) {
            await assert.rejects(() => client.prepareTransfer(request), TypeError);
        }
        assert.deepEqual(node.seen, []);

        const envelope = await client.prepareTransfer({
            sender: signer.address, recipient: PAYEE, amount: 1, id: 'my-transfer-0000000001',
            timestamp: '2026-11-03T00:00:00Z', nonce: 12, parentCells: [],
        });
        assert.deepEqual(envelope, {
            id: 'my-transfer-0000000001', sender: signer.address, recipient: PAYEE, amount: 1, fee: 0,
            geotag: '', parent_cells: [], nonce: 12, timestamp: '2026-11-03T00:00:00Z',
        });
        assert.deepEqual(node.seen, []);
    } finally {
        await stopServer(node.srv);
    }
});

test('prepareTransfer refuses an inconsistent nonce answer', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ sender: PAYEE, nonce: 3, next: 4 }));
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        await assert.rejects(
            () => client.prepareTransfer({ sender: fakeSigner(1).address, recipient: PAYEE, amount: 1 }),
            /inconsistent nonce/
        );
    } finally {
        await stopServer(srv);
    }
});

test('getPoEParents falls back to receipts only when the parents route is missing', async () => {
    const seen = [];
    const { srv, baseURL } = await startServer((req, res) => {
        seen.push(req.url);
        if (req.url.startsWith('/api/v1/receipts')) {
            res.setHeader('Content-Type', 'application/json');
            res.end(JSON.stringify({
                tip: 9,
                receipts: [
                    { tx_id: 'failed-transfer-000000001', status: 0 },
                    { tx_id: 'short', status: 1 },
                    { tx_id: 'solo-heartbeat-9-1791611927090994535', status: 1 },
                    { tx_id: 'solo-heartbeat-9-1791611927090994535', status: 1 },
                    { tx_id: 'solo-reward-9-abcdef0123456789', status: 1 },
                    { tx_id: 'solo-heartbeat-8-1791611917090994535', status: 1 },
                ],
            }));
            return;
        }
        res.statusCode = 404;
        res.end('404 page not found');
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        assert.deepEqual(await client.getPoEParents(), [
            'solo-heartbeat-9-1791611927090994535',
            'solo-reward-9-abcdef0123456789',
        ]);
        assert.deepEqual(seen, ['/api/v1/chain/parents', '/api/v1/receipts?limit=32']);
    } finally {
        await stopServer(srv);
    }

    const busy = await startServer((req, res) => {
        assert.ok(!req.url.startsWith('/api/v1/receipts'), 'a 503 must not fall back to receipts');
        res.statusCode = 503;
        res.end('not enough committed transactions to suggest parents yet');
    });
    try {
        const client = new qsdm.QSDMClient(busy.baseURL);
        await assert.rejects(() => client.getPoEParents(), (err) => err instanceof qsdm.ApiError && err.status === 503);
    } finally {
        await stopServer(busy.srv);
    }

    const malformed = await startServer((req, res) => {
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ parents: ['parent1', 'parent2'] }));
    });
    try {
        const client = new qsdm.QSDMClient(malformed.baseURL);
        await assert.rejects(() => client.getPoEParents(), /no usable Proof-of-Entanglement parents/);
    } finally {
        await stopServer(malformed.srv);
    }
});

test('getChainParents, getReceipt and getReceipts use the public routes', async () => {
    const seen = [];
    const { srv, baseURL } = await startServer((req, res) => {
        seen.push(req.url);
        res.setHeader('Content-Type', 'application/json');
        if (req.url.startsWith('/api/v1/chain/parents')) {
            res.end(JSON.stringify({ tip: 805400, poe_active: true, parents: [PARENT_A, PARENT_B], recent: [] }));
        } else if (req.url === '/api/v1/receipts/missing-tx-00000001') {
            res.statusCode = 404;
            res.end('receipt not found');
        } else if (req.url.startsWith('/api/v1/receipts/')) {
            res.end(JSON.stringify({ tx_id: 'sdk_js_1_0000000000000000', block_height: 900001, status: 1 }));
        } else {
            res.end(JSON.stringify({ tip: 30, from: 0, to: 20, limit: 5, receipts: [] }));
        }
    });
    try {
        const client = new qsdm.QSDMClient(baseURL);
        const parents = await client.getChainParents({ limit: 16 });
        assert.equal(parents.poe_active, true);
        const receipt = await client.getReceipt('sdk_js_1_0000000000000000');
        assert.equal(receipt.status, 1);
        await assert.rejects(() => client.getReceipt('missing-tx-00000001'), (err) => qsdm.isNotFound(err));
        await client.getReceipts({ from: 0, to: 20, limit: 5 });
        await client.getReceipts();
        assert.deepEqual(seen, [
            '/api/v1/chain/parents?limit=16',
            '/api/v1/receipts/sdk_js_1_0000000000000000',
            '/api/v1/receipts/missing-tx-00000001',
            '/api/v1/receipts?from=0&to=20&limit=5',
            '/api/v1/receipts',
        ]);
    } finally {
        await stopServer(srv);
    }
});

// --- QSDM Wallet extension provider ---------------------------------------

function fakeProvider(sender) {
    const calls = [];
    const provider = {
        isQsdmHive: true,
        version: 'qsdm-provider/v1',
        async request({ method, params }) {
            calls.push({ method, params });
            if (method === 'qsdm_requestAccounts') return [sender];
            if (method === 'qsdm_sendTransaction') {
                return { transaction_id: 'hive_wallet_1_0123456789abcdef', status: 'pending', broadcast: 'block-pending' };
            }
            throw new Error(`unexpected method ${method}`);
        },
    };
    return { provider, calls };
}

test('sendTransferWithProvider connects, then asks Hive to sign and send', async () => {
    const sender = fakeSigner(2).address;
    const { provider, calls } = fakeProvider(sender);
    const out = await qsdm.sendTransferWithProvider(provider, { recipient: PAYEE, amount: 1.25 });
    assert.deepEqual(calls, [
        { method: 'qsdm_requestAccounts', params: undefined },
        { method: 'qsdm_sendTransaction', params: { recipient: PAYEE, amount: 1.25 } },
    ]);
    assert.deepEqual(out, {
        transaction_id: 'hive_wallet_1_0123456789abcdef', status: 'pending', broadcast: 'block-pending', sender,
    });

    const second = fakeProvider(sender);
    await qsdm.sendTransferWithProvider(second.provider, { recipient: PAYEE, amount: 2 }, { connect: false });
    assert.deepEqual(second.calls.map((call) => call.method), ['qsdm_sendTransaction']);
});

test('sendTransferWithProvider validates before prompting the user', async () => {
    const { provider, calls } = fakeProvider(fakeSigner(2).address);
    await assert.rejects(() => qsdm.sendTransferWithProvider(provider, { recipient: 'not-an-address', amount: 1 }), TypeError);
    await assert.rejects(() => qsdm.sendTransferWithProvider(provider, { recipient: PAYEE, amount: 0 }), TypeError);
    await assert.rejects(() => qsdm.sendTransferWithProvider(provider, { recipient: PAYEE, amount: 1e-9 }), /8 decimal places/);
    await assert.rejects(() => qsdm.sendTransferWithProvider(null, { recipient: PAYEE, amount: 1 }), TypeError);
    assert.deepEqual(calls, []);
});

test('getWalletProvider and waitForWalletProvider find the injected provider', async () => {
    const { provider } = fakeProvider(PAYEE);
    assert.equal(qsdm.getWalletProvider({ qsdm: provider }), provider);
    assert.equal(qsdm.getWalletProvider({ qsdm: { request() {} } }), null);
    assert.equal(qsdm.getWalletProvider({}), null);

    const scope = new EventTarget();
    setTimeout(() => {
        scope.qsdm = provider;
        scope.dispatchEvent(new Event('qsdm#initialized'));
    }, 10);
    assert.equal(await qsdm.waitForWalletProvider({ scope, timeoutMs: 2000 }), provider);
    assert.equal(await qsdm.waitForWalletProvider({ scope: new EventTarget(), timeoutMs: 20 }), null);
});
