// Node.js built-in test for the QSDM SDK.
// Run with: node --test qsdm.test.js

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

test('QSDMClient exported', () => {
    assert.equal(typeof qsdm.QSDMClient, 'function');
});

test('QSDMClient performs a wallet balance request', async () => {
    const { srv, baseURL } = await startServer((req, res) => {
        assert.equal(req.method, 'GET');
        assert.ok(req.url.startsWith('/api/v1/wallet/balance'));
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

test('ApiError, isNotFound, isUnauthorized are re-exported', () => {
    assert.equal(typeof qsdm.ApiError, 'function');
    assert.equal(typeof qsdm.isNotFound, 'function');
    assert.equal(typeof qsdm.isUnauthorized, 'function');

    const err = new qsdm.ApiError(404, 'http://x/y', 'not found');
    assert.ok(qsdm.isNotFound(err));
    assert.ok(!qsdm.isUnauthorized(err));
});
