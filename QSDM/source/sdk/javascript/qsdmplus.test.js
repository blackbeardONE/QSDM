// Node.js built-in test for the QSDM+ SDK.
// Run with: node --test qsdmplus.test.js
//
// The tests use a local http.Server as a mock qsdmplus node.

const test = require('node:test');
const assert = require('node:assert/strict');
const http = require('node:http');
const { QSDMPlusClient, ApiError, isNotFound, isUnauthorized } = require('./qsdmplus.js');

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

test('getBalance returns parsed balance', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(req.method, 'GET');
        assert.ok(req.url.startsWith('/api/v1/wallet/balance'));
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ balance: 42.5 }));
    });
    try {
        const c = new QSDMPlusClient(baseURL);
        const v = await c.getBalance('addr-1');
        assert.equal(v, 42.5);
    } finally {
        await stopServer(srv);
    }
});

test('sendTransaction posts JSON body and returns tx id', async () => {
    const { srv, baseURL } = await startServer(async (req, res) => {
        assert.equal(req.method, 'POST');
        let body = '';
        for await (const chunk of req) body += chunk;
        const parsed = JSON.parse(body);
        assert.equal(parsed.from, 'a');
        assert.equal(parsed.to, 'b');
        assert.equal(parsed.amount, 1.5);
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({ transaction_id: 'tx-abc' }));
    });
    try {
        const c = new QSDMPlusClient(baseURL);
        const id = await c.sendTransaction('a', 'b', 1.5);
        assert.equal(id, 'tx-abc');
    } finally {
        await stopServer(srv);
    }
});

test('ApiError on 404 is classified by isNotFound', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        res.statusCode = 404;
        res.end('not here');
    });
    try {
        const c = new QSDMPlusClient(baseURL);
        await assert.rejects(
            () => c.getTransaction('missing'),
            (err) => err instanceof ApiError && isNotFound(err),
        );
    } finally {
        await stopServer(srv);
    }
});

test('401 classified as unauthorized', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        res.statusCode = 401;
        res.end('nope');
    });
    try {
        const c = new QSDMPlusClient(baseURL);
        await assert.rejects(
            () => c.getNodeStatus(),
            (err) => err instanceof ApiError && isUnauthorized(err),
        );
    } finally {
        await stopServer(srv);
    }
});

test('auth headers — bearer and api key', async () => {
    const gotHeaders = [];
    const { srv, baseURL } = await startServer((req, res) => {
        gotHeaders.push({
            authorization: req.headers['authorization'],
            apiKey: req.headers['x-api-key'],
        });
        res.setHeader('Content-Type', 'application/json');
        res.end('{}');
    });
    try {
        const c = new QSDMPlusClient(baseURL);
        c.setToken('jwt-xyz');
        await c.getNodeStatus();
        c.setToken(null); c.setAPIKey('k-123');
        await c.getNodeStatus();

        assert.equal(gotHeaders[0].authorization, 'Bearer jwt-xyz');
        assert.equal(gotHeaders[0].apiKey, undefined);
        assert.equal(gotHeaders[1].apiKey, 'k-123');
    } finally {
        await stopServer(srv);
    }
});

test('getNodeStatus maps known fields and preserves extra', async () => {
    const payload = { node_id: 'n-a', version: '1.0.0', uptime: '1h', chain_tip: 42, peers: 5, custom: 'kept' };
    const { srv, baseURL } = await startServer((req, res) => {
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify(payload));
    });
    try {
        const c = new QSDMPlusClient(baseURL);
        const ns = await c.getNodeStatus();
        assert.equal(ns.nodeId, 'n-a');
        assert.equal(ns.chainTip, 42);
        assert.equal(ns.peers, 5);
        assert.equal(ns.extra.custom, 'kept');
    } finally {
        await stopServer(srv);
    }
});

test('getNetworkTopology returns live cells + links', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(req.url, '/api/v1/network/topology');
        res.setHeader('Content-Type', 'application/json');
        res.end(JSON.stringify({
            title: 'Live network topology',
            live_peer_count: 1,
            cells: [{ id: 'local' }, { id: 'peer-a', role: 'parent' }],
            links: [{ from: 'local', to: 'peer-a', kind: 'dependency' }],
        }));
    });
    try {
        const c = new QSDMPlusClient(baseURL);
        const topo = await c.getNetworkTopology();
        assert.equal(topo.live_peer_count, 1);
        assert.equal(topo.cells.length, 2);
        assert.equal(topo.links[0].kind, 'dependency');
    } finally {
        await stopServer(srv);
    }
});

test('getMetricsPrometheus returns raw text', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        res.setHeader('Content-Type', 'text/plain');
        res.end('qsdmplus_tx_total 42\n');
    });
    try {
        const c = new QSDMPlusClient(baseURL);
        const body = await c.getMetricsPrometheus();
        assert.match(body, /qsdmplus_tx_total/);
    } finally {
        await stopServer(srv);
    }
});

test('baseURL trailing slash is stripped', () => {
    const c = new QSDMPlusClient('http://example.com/');
    assert.equal(c.baseURL, 'http://example.com');
});
