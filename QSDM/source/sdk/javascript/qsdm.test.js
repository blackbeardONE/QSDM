// Node.js built-in test for the QSDM SDK.
// Run with: node --test qsdm.test.js
//
// Covers every public method on QSDMClient plus the package-level helpers
// (`ApiError`, `isNotFound`, `isUnauthorized`) and configuration knobs
// (`setToken`, `setAPIKey`, baseURL trim, per-request timeout).

const test = require('node:test');
const assert = require('node:assert/strict');
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
    assert.equal(typeof qsdm.isNotFound, 'function');
    assert.equal(typeof qsdm.isUnauthorized, 'function');
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

test('sendTransaction POSTs JSON and returns the transaction_id', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(req.method, 'POST');
        assert.equal(req.url, '/api/v1/wallet/send');
        assert.equal(req.headers['content-type'], 'application/json');
        let body = '';
        req.on('data', (chunk) => { body += chunk; });
        req.on('end', () => {
            assert.deepEqual(JSON.parse(body), { from: 'a', to: 'b', amount: 5 });
            res.setHeader('Content-Type', 'application/json');
            res.end(JSON.stringify({ transaction_id: 'tx-42' }));
        });
    });
    try {
        const c = new qsdm.QSDMClient(baseURL);
        const id = await c.sendTransaction('a', 'b', 5);
        assert.equal(id, 'tx-42');
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
