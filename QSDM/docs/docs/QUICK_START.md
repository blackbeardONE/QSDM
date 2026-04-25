# QSDM Quick Start Guide

**Last Updated:** December 2024  
**Version:** QSDM with ML-DSA-87 Optimizations

---

## Overview

**QSDM** (Quantum-Secure Dynamic Mesh Ledger) is a quantum-resistant blockchain using ML-DSA-87 (NIST FIPS 204) for 256-bit quantum-safe security. This guide will help you get started quickly.

---

## Prerequisites

### Required
- **Windows 10+** (Linux/macOS in development)
- **Go 1.20+**
- **Git**
- **PowerShell 5.1+**

### Optional (for full features)
- **OpenSSL 3.x** (for liboqs)
- **liboqs** (automatically built by `build.ps1`)

---

## Quick Start (5 minutes)

### 1. Clone and Build

```powershell
# Clone the repository
git clone https://github.com/blackbeardONE/QSDM.git
cd QSDM

# Build with all optimizations
.\scripts\build.ps1

# Run the node
.\scripts\run.ps1
```

The node will:
- Initialize quantum-safe cryptography (ML-DSA-87)
- Start libp2p networking
- Create SQLite database
- Launch monitoring dashboard (http://localhost:8081)
- Start log viewer (http://localhost:8080)

---

## What's New: Performance Optimizations

### Signing Performance
- **Regular Signing**: 0.50 ms
- **Optimized Signing**: 0.45-0.475 ms (5-10% faster) ✅
- **Batch Signing**: 0.025-0.10 ms per signature (10-100x faster) ✅

### Verification Performance
- **QSDM Verification**: 0.19 ms ✅
- **ECDSA (Bitcoin/Ethereum)**: 0.33 ms
- **QSDM is 1.76x faster!** ✅

### Signature Compression
- **Original Size**: 4,627 bytes
- **Compressed Size**: 2,314 bytes
- **50% reduction** ✅

### Storage Compression
- **Compression Ratio**: 60-70%
- **10-year storage**: 435-580 GB (vs 1.45 TB uncompressed)
- **70% reduction** ✅

---

## Key Features

### 1. Quantum-Safe Cryptography
- **Algorithm**: ML-DSA-87 (NIST FIPS 204)
- **Security Level**: 256-bit quantum-safe
- **Performance**: Optimized with memory pooling

### 2. Optimized Storage
- **Database**: SQLite with WAL mode
- **Compression**: zstd (best compression level)
- **Encryption**: AES-GCM

### 3. Monitoring
- **Dashboard**: http://localhost:8081
- **Log Viewer**: http://localhost:8080
- **Metrics**: Storage operations, transaction throughput, error tracking

### 4. Performance Features
- **Memory Pooling**: Reduces allocations
- **Batch Operations**: Parallel signing
- **Compression**: Reduces storage and bandwidth

---

## Configuration

### Environment Variables

```powershell
# Network
$env:NETWORK_PORT = "4001"
$env:BOOTSTRAP_PEERS = "peer1,peer2"

# Storage
$env:STORAGE_TYPE = "sqlite"
$env:SQLITE_PATH = "qsdm.db"

# Monitoring
$env:DASHBOARD_PORT = "8081"
$env:LOG_VIEWER_PORT = "8080"
$env:LOG_FILE = "qsdm.log"
$env:LOG_LEVEL = "INFO"

# API
$env:API_PORT = "8443"
$env:ENABLE_TLS = "true"
```

### Default Ports
- **Network**: 4001
- **Dashboard**: 8081
- **Log Viewer**: 8080
- **API**: 8443

---

## Usage Examples

### Check Node Status

```powershell
# View logs
Get-Content qsdm.log -Tail 50

# Check dashboard
Start-Process "http://localhost:8081"
```

### Monitor Performance

Access the dashboard at http://localhost:8081 to view:
- Transaction throughput
- Storage operation metrics
- Error rates
- System health

---

## Troubleshooting

### Common Issues

#### 1. "Dilithium not initialized"
**Solution:**
- Ensure OpenSSL DLLs are in PATH
- Run `.\run.ps1` (sets up PATH correctly)
- Check `liboqs.dll` is available

#### 2. "Storage operation failed"
**Solution:**
- Check disk space
- Verify database file permissions
- Check SQLite is installed

#### 3. "Port already in use"
**Solution:**
- Change port in environment variables
- Stop conflicting services
- Check firewall settings

### Getting Help

- **Documentation**: See `docs/` directory
- **Performance Report**: `docs/PERFORMANCE_BENCHMARK_REPORT.md`
- **Next Steps**: `docs/NEXT_STEPS.md`

---

## Performance Tips

1. **Use Optimized Signing**: Automatically enabled
2. **Batch Operations**: Use `SignBatchOptimized()` for multiple transactions
3. **Compression**: Enabled by default (50% signature reduction)
4. **Storage**: WAL mode enabled for better concurrency

---

## What's Next?

See [docs/NEXT_STEPS.md](NEXT_STEPS.md) for:
- Production readiness checklist
- Feature development roadmap
- Testing and validation

---

## Performance Comparison

| Metric | QSDM (Optimized) | Bitcoin/Ethereum |
|--------|----------------|------------------|
| **Signing** | 0.45-0.475 ms | 0.14 ms |
| **Verification** | **0.19 ms** ✅ | 0.33 ms |
| **Signature Size** | 2.3 KB (compressed) | 70 bytes |
| **Security** | **256-bit quantum-safe** ✅ | 128-bit classical |

**QSDM Advantages:**
- ✅ Quantum-safe (future-proof)
- ✅ Faster verification than ECDSA
- ✅ Optimized storage and signatures

---

*Happy coding! 🚀*
