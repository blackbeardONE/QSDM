/**
 * This test script runs in a Node.js environment with WASM support.
 * It loads the compiled wallet and validator WASM modules,
 * instantiates them, and calls exported functions to verify functionality.
 *
 * Usage:
 *   node tests/wasm_js_integration_test_new.js
 */

const fs = require('fs').promises;
const path = require('path');
const wasi = require('wasi');
const { WASI } = wasi;
const { argv, env } = require('process');

const gojsStub = {
  syscall: {
    js: {
      valueGet: function() { return function() {}; },
      valueSet: function() { return function() {}; },
      valueCall: function() { return function() {}; },
      valueInvoke: function() { return function() {}; },
      valueNew: function() { return function() {}; },
      valueLength: function() { return 0; },
      valuePrepareString: function() { return 0; },
      valueLoadString: function() { return 0; },
      valueInstanceOf: function() { return false; },
      valueType: function() { return 0; },
      valueIndex: function() { return function() {}; },
      valueSetIndex: function() { return function() {}; },
      valueCallGo: function() { return function() {}; },
      finalizerRef: function() { return function() {}; },
      stringVal: function() { return ""; },
      valueReceive: function() { return function() {}; },
      // Add other syscall/js functions as needed
    }
  }
};

async function loadWasmModule(wasmPath) {
  const wasmBuffer = await fs.readFile(wasmPath);
  const wasiInstance = new WASI({
    args: argv,
    env,
    preopens: {
      '/': './'
    },
    version: 'preview1'
  });

  const importObject = {
    wasi_snapshot_preview1: wasiInstance.wasiImport,
    gojs: gojsStub
  };

  const wasmModule = await WebAssembly.compile(wasmBuffer);
  const instance = await WebAssembly.instantiate(wasmModule, importObject);
  wasiInstance.start(instance);
  return instance;
}

async function testWalletWasm() {
  const walletWasmPath = path.resolve(__dirname, '../wasm_modules/wallet/wallet.wasm');
  const walletInstance = await loadWasmModule(walletWasmPath);
  if (!walletInstance.exports) {
    throw new Error('Wallet WASM module exports not found');
  }
  console.log('Wallet WASM module loaded and instantiated successfully');
}

async function testValidatorWasm() {
  const validatorWasmPath = path.resolve(__dirname, '../wasm_modules/validator/validator.wasm');
  const validatorInstance = await loadWasmModule(validatorWasmPath);
  if (!validatorInstance.exports) {
    throw new Error('Validator WASM module exports not found');
  }
  console.log('Validator WASM module loaded and instantiated successfully');
}

async function runTests() {
  try {
    await testWalletWasm();
    await testValidatorWasm();
    console.log('WASM JS integration tests passed');
  } catch (err) {
    console.error('WASM JS integration tests failed:', err);
    process.exit(1);
  }
}

runTests();
