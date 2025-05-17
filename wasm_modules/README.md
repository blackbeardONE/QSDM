# WASM Modules for QSDM

This directory contains WebAssembly modules implemented in Go using TinyGo for the Quantum-Secure Dynamic Mesh Ledger (QSDM) project.

## Modules

- **wallet**: Implements wallet functionality with methods to get balance and send transactions.
- **validator**: Implements transaction validation logic.

## Build Instructions

Ensure you have TinyGo installed: https://tinygo.org/getting-started/

To build the wallet module:

```bash
cd wasm_modules/wallet
tinygo build -o wallet.wasm -target wasm .
```

To build the validator module:

```bash
cd wasm_modules/validator
tinygo build -o validator.wasm -target wasm .
```

## Integration

The generated `.wasm` files can be loaded by the QSDM WASM SDK for wallet and validator functionality.

## Notes

- The modules use JavaScript syscall interface for interaction.
- These are basic example implementations and should be extended with real logic as needed.
