const fs = require('fs');
const path = require('path');
const { WASI } = require('wasi');
const goWasmRuntime = require('./go_wasm_runtime.js');

async function runWasmModule(wasmPath) {
  console.log(`Running WASM module: ${wasmPath}`);

  if (!fs.existsSync(wasmPath)) {
    console.error(`WASM file not found: ${wasmPath}`);
    return;
  }

  const wasmBuffer = fs.readFileSync(wasmPath);

  const go = new goWasmRuntime.Go();
  const wasi = new WASI({
    args: [],
    env: process.env,
    preopens: {
      '/': process.cwd()
    }
  });

  const importObject = {
    ...go.importObject,
    ...wasi.getImportObject()
  };

  try {
    const { instance } = await WebAssembly.instantiate(wasmBuffer, importObject);
    wasi.start(instance);
    await go.run(instance);
    console.log(`WASM module ${wasmPath} executed successfully.`);
  } catch (err) {
    console.error(`Error running WASM module ${wasmPath}:`, err);
  }
}

async function main() {
  const walletWasm = path.resolve(__dirname, '../wasm_modules/wallet/wallet.wasm');
  const validatorWasm = path.resolve(__dirname, '../wasm_modules/validator/validator.wasm');

  await runWasmModule(walletWasm);
  await runWasmModule(validatorWasm);

  console.log('Node.js Go WASM integration tests completed.');
}

main();
