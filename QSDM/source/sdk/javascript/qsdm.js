/**
 * QSDM JavaScript SDK — preferred entry point (rebrand of qsdmplus.js).
 *
 * The QSDM platform was historically shipped under the transitional name "QSDM+".
 * This module re-exports the same client as qsdmplus.js with QSDMClient as the
 * preferred symbol name; QSDMPlusClient remains available as a legacy alias.
 *
 * Works in browsers and Node.js (18+ which ships fetch globally, or any Node + a
 * fetch polyfill). The wire protocol is identical:
 *
 *   const { QSDMClient } = require('qsdm');
 *   const c = new QSDMClient('http://node:8080');
 *   c.setToken(jwt);
 *   const balance = await c.getBalance('addr');
 *
 * Native coin: Cell (CELL), 8 decimals, smallest unit "dust".
 */

const legacy = require('./qsdmplus.js');

// Re-export under the preferred name.
const QSDMClient = legacy.QSDMPlusClient || legacy;
const { ApiError, isNotFound, isUnauthorized, QSDMPlusClient } = legacy;

if (typeof module !== 'undefined' && module.exports) {
    module.exports = QSDMClient;
    module.exports.QSDMClient = QSDMClient;
    module.exports.ApiError = ApiError;
    module.exports.isNotFound = isNotFound;
    module.exports.isUnauthorized = isUnauthorized;
    // Back-compat aliases — retained for the deprecation window.
    module.exports.QSDMPlusClient = QSDMPlusClient;
}
