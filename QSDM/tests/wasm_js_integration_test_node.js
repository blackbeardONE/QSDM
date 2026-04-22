const fs = require('fs');
const path = require('path');
const Go = require('./go_wasm_runtime.js').Go;

async function runGoWasmModule(wasmPath) {
  const go = new Go();

  const wasmBuffer = fs.readFileSync(wasmPath);
  const wasmModule = await WebAssembly.compile(wasmBuffer);

  const importObject = go.importObject;

  const instance = await WebAssembly.instantiate(wasmModule, importObject);

  go.run(instance);

  console.log(`WASM module ${path.basename(wasmPath)} loaded and started successfully.`);
}

async function main() {
  try {
    await runGoWasmModule(path.resolve(__dirname, '../wasm_modules/wallet/wallet.wasm'));
    await runGoWasmModule(path.resolve(__dirname, '../wasm_modules/validator/validator.wasm'));
    console.log('Go WASM integration tests completed successfully.');
  } catch (err) {
    console.error('Error running WASM module:', err);
    process.exit(1);
  }
}

main();
