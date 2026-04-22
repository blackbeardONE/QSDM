# QSDM Wallet vs QSDM WASM Wallet

**Last Updated:** December 2024

---

## Overview

QSDM has **two wallet implementations** for different use cases:

1. **QSDM Wallet** (`pkg/wallet/`) - Go-native wallet service
2. **QSDM WASM Wallet** (`wasm_modules/wallet/`) - WebAssembly wallet module

Both are **for QSDM transactions** (not separate cryptocurrencies).

---

## 1. QSDM Wallet (`pkg/wallet/`)

### What It Is
- **Go-native wallet service** that runs directly in the QSDM node
- Uses **CGO** to call liboqs directly
- Part of the QSDM node application

### Location
```
pkg/wallet/
├── wallet.go          # Main wallet service
└── wallet_stub.go     # Stub for non-CGO builds
```

### Characteristics

| Feature | Details |
|---------|---------|
| **Language** | Go (native) |
| **Cryptography** | Direct CGO calls to liboqs |
| **Runtime** | QSDM node process |
| **Dependencies** | Requires CGO, liboqs, OpenSSL |
| **Performance** | Fastest (direct native calls) |
| **Use Case** | QSDM node wallet operations |

### Code Example

```go
// Create wallet service
walletService, err := wallet.NewWalletService()
if err != nil {
    return err
}

// Create transaction
txBytes, err := walletService.CreateTransaction(
    recipient,    // Recipient address
    amount,       // Amount to send
    fee,          // Transaction fee
    geotag,       // Geographic tag
    parentCells,  // Parent cell IDs
)
```

### Features
- ✅ Direct liboqs integration (via CGO)
- ✅ Optimized signing (`SignOptimized()`)
- ✅ Compressed signatures (`SignDataCompressed()`)
- ✅ Full Go type safety
- ✅ Best performance

### When to Use
- **QSDM node operations** - Creating transactions from the node
- **Server-side wallet** - Backend wallet service
- **High performance** - When speed is critical

---

## 2. QSDM WASM Wallet (`wasm_modules/wallet/`)

### What It Is
- **WebAssembly wallet module** that can run in WASM runtimes
- Compiled to `.wasm` format
- Can run in browsers, Node.js, or other WASM environments

### Location
```
wasm_modules/wallet/
├── wallet.go           # JS/WASM interface
├── wallet_wasm.go      # TinyGo WASM build
├── walletcore/         # Core wallet logic
│   └── walletcore.go
├── walletcrypto/       # Crypto operations
│   └── crypto.go
└── wallet.wasm         # Compiled WASM binary
```

### Characteristics

| Feature | Details |
|---------|---------|
| **Language** | Go (compiled to WASM) |
| **Cryptography** | Uses liboqs-go bindings (if available) |
| **Runtime** | WASM runtime (wasmtime, browser, etc.) |
| **Dependencies** | WASM runtime, liboqs-go (optional) |
| **Performance** | Good (WASM overhead) |
| **Use Case** | Browser wallets, portable wallets |

### Code Example

```javascript
// Load WASM module
const wasmModule = await WebAssembly.instantiateStreaming(
    fetch('wallet.wasm')
);

// Call wallet functions
const balance = wasmModule.instance.exports.GetBalance();
const address = wasmModule.instance.exports.GetAddress();
```

### Features
- ✅ Portable (runs anywhere WASM is supported)
- ✅ Browser-compatible
- ✅ Sandboxed execution
- ✅ Can be embedded in web apps
- ⚠️ Requires WASM runtime

### When to Use
- **Browser wallets** - Web-based wallet applications
- **Portable wallets** - Cross-platform wallet apps
- **Embedded wallets** - Wallet functionality in other apps
- **WASM environments** - When you need WASM compatibility

---

## Key Differences

| Aspect | QSDM Wallet | QSDM WASM Wallet |
|--------|-------------|------------------|
| **Implementation** | Go native (CGO) | Go compiled to WASM |
| **Runtime** | QSDM node process | WASM runtime |
| **Performance** | Fastest | Good (WASM overhead) |
| **Dependencies** | CGO, liboqs, OpenSSL | WASM runtime |
| **Portability** | Limited (needs CGO) | High (WASM everywhere) |
| **Use Case** | Node operations | Browser/portable apps |
| **Integration** | Direct Go code | WASM module loading |

---

## How They Work Together

### In QSDM Node

```go
// 1. Native wallet for node operations
walletService, _ := wallet.NewWalletService()

// 2. WASM wallet for external use
walletWasmPath := "wasm_modules/wallet/wallet.wasm"
walletBytes, _ := wasm.LoadWASMFromFile(walletWasmPath)
walletSdk, _ := wasm.NewWASMSDK(walletBytes)
```

**Both can coexist:**
- **Native wallet** - For node's own transactions
- **WASM wallet** - For loading external wallet modules

---

## Architecture

### QSDM Wallet (Native)
```
QSDM Node Process
    │
    ├── pkg/wallet/ (Go)
    │   └── Uses pkg/crypto/dilithium.go
    │       └── CGO → liboqs.dll
    │           └── ML-DSA-87
```

### QSDM WASM Wallet
```
WASM Runtime (Browser/Node.js/etc.)
    │
    ├── wallet.wasm (Compiled Go)
    │   └── Uses walletcrypto/
    │       └── liboqs-go (if available)
    │           └── ML-DSA-87
```

---

## Use Cases

### Use QSDM Wallet (Native) When:
- ✅ Running QSDM node
- ✅ Server-side wallet operations
- ✅ Maximum performance needed
- ✅ Direct integration with QSDM node

### Use QSDM WASM Wallet When:
- ✅ Building browser wallet
- ✅ Cross-platform wallet app
- ✅ Embedding wallet in web app
- ✅ Portable wallet functionality

---

## Both Are For QSDM

**Important:** Both wallets are **for QSDM transactions**, not separate cryptocurrencies:

- **QSDM Wallet** - Creates QSDM transactions
- **QSDM WASM Wallet** - Creates QSDM transactions
- **Same network** - Both use QSDM mesh ledger
- **Same cryptography** - Both use ML-DSA-87
- **Same transactions** - Both create QSDM transactions

---

## Summary

| Wallet Type | Location | Purpose | Runtime |
|-------------|----------|---------|---------|
| **QSDM Wallet** | `pkg/wallet/` | Node wallet service | QSDM node |
| **QSDM WASM Wallet** | `wasm_modules/wallet/` | Portable wallet | WASM runtime |

**Both:**
- ✅ Create QSDM transactions
- ✅ Use ML-DSA-87 quantum-safe cryptography
- ✅ Sign transactions for QSDM mesh ledger
- ✅ Part of the QSDM project

**Difference:**
- **Native wallet** - Fast, direct, node-integrated
- **WASM wallet** - Portable, browser-compatible, embeddable

---

*Both wallets serve the same purpose (QSDM transactions) but target different environments.*

