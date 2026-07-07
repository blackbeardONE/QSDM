const fs = require('fs');
const path = require('path');
const assert = require('assert/strict');
const { WASI } = require('wasi');

require('./go.wasm.js');

function readWasm(wasmPath) {
  assert.ok(fs.existsSync(wasmPath), `WASM file not found: ${wasmPath}`);
  return fs.readFileSync(wasmPath);
}

async function runWalletWasi(wasmPath) {
  const wasi = new WASI({
    version: 'preview1',
    args: [],
    env: {},
    preopens: {}
  });

  const { instance } = await WebAssembly.instantiate(readWasm(wasmPath), {
    wasi_snapshot_preview1: wasi.wasiImport
  });

  assert.equal(typeof instance.exports.memory, 'object');
  assert.equal(typeof instance.exports._start, 'function');
  wasi.start(instance);
}

async function runValidatorGo(wasmPath) {
  assert.equal(typeof globalThis.Go, 'function', 'Go WASM runtime was not loaded');

  const go = new globalThis.Go();
  const { instance } = await WebAssembly.instantiate(readWasm(wasmPath), go.importObject);

  assert.equal(typeof instance.exports.run, 'function');
  await go.run(instance);
}

async function main() {
  const wasmRoot = path.resolve(__dirname, '../source/wasm_modules');

  await runWalletWasi(path.join(wasmRoot, 'wallet/wallet.wasm'));
  console.log('wallet.wasm loaded and started');

  await runValidatorGo(path.join(wasmRoot, 'validator/validator.wasm'));
  console.log('validator.wasm loaded and started');

  console.log('Node.js WASM integration tests completed successfully.');
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
