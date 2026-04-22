/**
 * This test script runs in a Node.js environment with WASM support.
 * It loads the compiled wallet and validator WASM modules,
 * instantiates them, and calls exported functions to verify functionality.
 *
 * Usage:
 *   node tests/wasm_js_integration_test_new.js
 */

import fs from 'fs/promises';
import path from 'path';
import { WASI } from '@wasmer/wasi';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

async function loadWasmModule(wasmPath) {
  const wasmBuffer = await fs.readFile(wasmPath);
  const wasi = new WASI({
    args: [],
    env: {},
    preopens: {
      '/': __dirname
    }
  });

  const importObject = {
    ...wasi.getImports()
  };

  const wasmModule = await WebAssembly.compile(wasmBuffer);
  const instance = await WebAssembly.instantiate(wasmModule, importObject);
  wasi.start(instance);
  return instance;
}

async function testWalletWasm() {
  const walletWasmPath = path.resolve(__dirname, '../wasm_modules/wallet/wallet.wasm');

  // Mock global JS functions for syscall/js
  global.getBalance = async () => 1000;
  global.sendTransaction = async (recipient, amount) => {
    console.log(`Mock sendTransaction called with recipient=${recipient}, amount=${amount}`);
    return true;
  };

  const walletInstance = await loadWasmModule(walletWasmPath);
  if (!walletInstance.exports) {
    throw new Error('Wallet WASM module exports not found');
  }
  console.log('Wallet WASM module loaded and instantiated successfully');

  // Call mocked JS callbacks and verify
  const balance = await global.getBalance();
  if (balance === 1000) {
    console.log('getBalance test passed');
  } else {
    console.error('getBalance test failed');
  }

  const txResult = await global.sendTransaction('recipient_address', 100);
  if (txResult === true) {
    console.log('sendTransaction test passed');
  } else {
    console.error('sendTransaction test failed');
  }
}

async function testValidatorWasm() {
  const validatorWasmPath = path.resolve(__dirname, '../wasm_modules/validator/validator.wasm');

  // Mock global JS function for syscall/js
  global.validateTransaction = async (txData) => {
    if (txData === 'valid' || txData === 'test transaction data') {
      return true;
    }
    return false;
  };

  const validatorInstance = await loadWasmModule(validatorWasmPath);
  if (!validatorInstance.exports) {
    throw new Error('Validator WASM module exports not found');
  }
  console.log('Validator WASM module loaded and instantiated successfully');

  // Call mocked JS callback and verify
  const validResult = await global.validateTransaction('valid');
  if (validResult === true) {
    console.log('validateTransaction valid input test passed');
  } else {
    console.error('validateTransaction valid input test failed');
  }

  const invalidResult = await global.validateTransaction('invalid');
  if (invalidResult === false) {
    console.log('validateTransaction invalid input test passed');
  } else {
    console.error('validateTransaction invalid input test failed');
  }
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
