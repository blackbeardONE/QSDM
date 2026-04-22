# WASM Module Interfaces for QSDM

This document describes the interfaces exposed by the WASM modules in the Quantum-Secure Dynamic Mesh Ledger (QSDM) project, focusing on the wallet and validator modules.

## Wallet WASM Module

### JavaScript Callback Functions

- `getBalance() : Promise<number>`
  - Returns the current balance as a fixed number (1000 for testing).
  - Usage:
    ```javascript
    const balance = await getBalance();
    ```

- `sendTransaction(recipient: string, amount: number) : Promise<boolean>`
  - Simulates sending a transaction to the specified recipient with the given amount.
  - Returns `true` on success.
  - Usage:
    ```javascript
    const success = await sendTransaction("recipient_address", 100);
    ```

### WASM Exports

- May include additional exported functions such as `add` for testing purposes.

## Validator WASM Module

### JavaScript Callback Functions

- `validateTransaction(txData: string) : Promise<boolean>`
  - Validates the transaction data string.
  - Returns `true` if the transaction data is valid (e.g., "valid" or "test transaction data"), otherwise `false`.
  - Usage:
    ```javascript
    const isValid = await validateTransaction("valid");
    ```

### WASM Exports

- May include additional exported functions for validation logic.

## Integration Notes

- The JS callback functions are exposed globally on the `window` object using Go's `syscall/js` package.
- These functions are intended to be called from JavaScript environments that support WASM and WASI.
- The test suite includes both browser-based and Node.js-based tests to verify these interfaces.

## Next Steps

- Extend interfaces with additional wallet and validator functionality as development progresses.
- Maintain synchronization between Go source code and interface documentation.
- Provide TypeScript type definitions for improved developer experience.

---

Developed by Blackbeard | Ten Titanics | GitHub: blackbeardONE  
© 2023-2024 Quantum-Secure Dynamic Mesh Ledger (QSDM) Project
