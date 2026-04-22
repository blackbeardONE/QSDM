# Performance Analysis: ML-DSA-87 vs Traditional Blockchain Cryptography

## Executive Summary

**ML-DSA-87 performance is acceptable for blockchain applications:**
- ✅ **Signing:** ~0.5 ms (2,000 TPS capacity)
- ✅ **Verification:** ~0.19 ms (5,300 TPS capacity) - **faster than ECDSA!**
- ⚠️ **Bandwidth:** ~4.6 KB per signature (larger than ECDSA)

**Real-world impact:** ML-DSA-87 can handle **10-100x more transactions** than most blockchains actually process.

---

## Detailed Performance Benchmarks

### 1. Modern Desktop/Server Hardware (Intel x86/x64)

#### 1.1. Operation Latency

| Operation | ML-DSA-87 | ECDSA secp256k1 | Ed25519 | Performance vs ECDSA |
|-----------|-----------|-----------------|---------|---------------------|
| **Key Generation** | 0.176 ms | 0.077 ms | 0.050 ms | 2.3x slower |
| **Signing** | 0.495 ms | 0.135 ms | 0.200 ms | 3.6x slower |
| **Verification** | 0.185 ms | 0.326 ms | 0.200 ms | **0.6x faster** ✅ |

**Key Insight:** ML-DSA-87 **verification is faster** than ECDSA!

#### 1.2. Throughput (Operations per Second)

| Operation | ML-DSA-87 | ECDSA secp256k1 | Ed25519 | Real-World Capacity |
|-----------|-----------|-----------------|---------|---------------------|
| **Key Generation** | 5,682 ops/sec | 12,987 ops/sec | 20,000 ops/sec | Sufficient for wallet creation |
| **Signing** | 2,020 ops/sec | 7,407 ops/sec | 5,000 ops/sec | **2,020 TPS signing** |
| **Verification** | 5,405 ops/sec | 3,067 ops/sec | 5,000 ops/sec | **5,405 TPS verification** ✅ |

**Blockchain Capacity:**
- **Signing:** Can handle **2,000+ transactions/second** (signing)
- **Verification:** Can handle **5,400+ transactions/second** (verification)
- **Most blockchains:** Process <100 TPS, so ML-DSA-87 is **20-50x over-provisioned**

### 2. Embedded/IoT Hardware (ARM Cortex M4 @ 168 MHz)

| Operation | ML-DSA-87 | ECDSA secp256k1 | Performance Impact |
|-----------|-----------|-----------------|-------------------|
| **Key Generation** | 58.167 ms | 0.077 ms | 725x slower (still <100ms) |
| **Signing** | 125.5 ms | 0.135 ms | 900x slower (still <200ms) |
| **Verification** | 62.687 ms | 0.326 ms | 191x slower (still <100ms) |

**IoT Assessment:**
- ✅ All operations complete in **<200ms** (acceptable for IoT)
- ⚠️ Significantly slower than ECDSA, but still practical
- ✅ Suitable for low-frequency IoT transactions

### 3. Comparison with Major Blockchains

#### 3.1. Actual Transaction Throughput

| Blockchain | Max TPS | Limiting Factor | ML-DSA-87 Capacity |
|------------|---------|----------------|-------------------|
| **Bitcoin** | ~7 TPS | Block size/time | ✅ **285x headroom** |
| **Ethereum** | ~15-30 TPS | Gas limits | ✅ **67-135x headroom** |
| **Solana** | ~3,000 TPS | Network bandwidth | ⚠️ **0.7x capacity** (close) |
| **QSDM** | **2,000+ TPS** | ML-DSA-87 signing | ✅ **Sufficient** |

**Analysis:**
- ML-DSA-87 can handle **Bitcoin's throughput 285x over**
- ML-DSA-87 can handle **Ethereum's throughput 67-135x over**
- ML-DSA-87 is **slightly below Solana's peak** (but Solana rarely hits 3,000 TPS)
- **QSDM's 2,000 TPS capacity is competitive** with high-performance blockchains

#### 3.2. Transaction Processing Time

**Typical Blockchain Transaction Flow:**
1. User signs transaction: **0.5 ms** (ML-DSA-87)
2. Network propagates: **Variable** (network-dependent)
3. Validator verifies: **0.19 ms** (ML-DSA-87) - **faster than ECDSA!**
4. Block inclusion: **Variable** (consensus-dependent)

**Total Crypto Overhead:** ~0.7 ms per transaction
- **ECDSA equivalent:** ~0.5 ms per transaction
- **Difference:** +0.2 ms (negligible for blockchain use)

### 4. Bandwidth and Storage Impact

#### 4.1. Signature Size Comparison

| Algorithm | Signature Size | Ratio vs ECDSA | Storage Impact |
|-----------|----------------|----------------|----------------|
| **ML-DSA-87** | 4,627 bytes | **72x larger** | ~4.6 KB per transaction |
| **ECDSA secp256k1** | 64-72 bytes | 1x (baseline) | ~70 bytes per transaction |
| **Ed25519** | 64 bytes | 1x | ~64 bytes per transaction |

#### 4.2. Network Bandwidth Impact

**Transaction Transmission:**
- **ML-DSA-87:** ~4.6 KB per signature
- **ECDSA:** ~70 bytes per signature
- **Overhead:** +4.5 KB per transaction

**Real-World Impact:**
- **1,000 transactions:** +4.5 MB (ML-DSA-87) vs +70 KB (ECDSA)
- **10,000 transactions:** +45 MB (ML-DSA-87) vs +700 KB (ECDSA)
- **100,000 transactions:** +450 MB (ML-DSA-87) vs +7 MB (ECDSA)

**Assessment:**
- ⚠️ **Significant bandwidth increase** for high-volume networks
- ✅ **Acceptable** for most blockchain use cases (<10,000 TPS)
- ⚠️ **May impact** very high-throughput networks (>5,000 TPS)

#### 4.3. Storage Impact

**Blockchain Storage (per 1 million transactions):**
- **ML-DSA-87:** ~4.6 GB (signatures only)
- **ECDSA:** ~70 MB (signatures only)
- **Difference:** +4.5 GB per million transactions

**Long-term Storage (10 years, 1 TPS average):**
- **ML-DSA-87:** ~1.45 TB (signatures)
- **ECDSA:** ~22 GB (signatures)
- **Difference:** +1.43 TB over 10 years

**Assessment:**
- ⚠️ **Significant storage increase** for long-term archival
- ✅ **Acceptable** with modern storage costs (~$20/TB)
- ✅ **Compression** can reduce storage by 50-70%

### 5. Performance Optimization Strategies

#### 5.1. Batch Verification
- **ML-DSA-87:** Supports batch verification (multiple signatures at once)
- **Benefit:** Can verify 10-100 signatures in parallel
- **Throughput:** Potentially **10,000+ TPS** with batching

#### 5.2. Hardware Acceleration
- **CPU:** Modern CPUs handle ML-DSA-87 efficiently
- **GPU:** Can accelerate verification (CUDA/OpenCL)
- **FPGA/ASIC:** Custom hardware can achieve **10-100x speedup**

#### 5.3. Algorithm Selection
- **ML-DSA-44:** 128-bit security, **2x faster** than ML-DSA-87
- **ML-DSA-65:** 192-bit security, **1.5x faster** than ML-DSA-87
- **ML-DSA-87:** 256-bit security, **maximum security**

**Trade-off:** Lower security = better performance

### 6. Real-World Performance Scenarios

#### Scenario 1: Low-Volume Network (<100 TPS)
- **ML-DSA-87 Performance:** ✅ **Excellent** (20x headroom)
- **Bandwidth Impact:** ✅ **Negligible** (<500 KB/sec)
- **Storage Impact:** ✅ **Acceptable** (<40 GB/year)
- **Verdict:** **Perfect fit**

#### Scenario 2: Medium-Volume Network (100-1,000 TPS)
- **ML-DSA-87 Performance:** ✅ **Good** (2-20x headroom)
- **Bandwidth Impact:** ⚠️ **Moderate** (5-50 MB/sec)
- **Storage Impact:** ⚠️ **Significant** (400 GB/year)
- **Verdict:** **Acceptable with optimization**

#### Scenario 3: High-Volume Network (1,000-5,000 TPS)
- **ML-DSA-87 Performance:** ⚠️ **At capacity** (signing limited)
- **Bandwidth Impact:** ⚠️ **High** (50-250 MB/sec)
- **Storage Impact:** ⚠️ **Very high** (2-10 TB/year)
- **Verdict:** **May need optimization or ML-DSA-65**

#### Scenario 4: Ultra-High-Volume Network (>5,000 TPS)
- **ML-DSA-87 Performance:** ❌ **Insufficient** (exceeds capacity)
- **Bandwidth Impact:** ❌ **Very high** (>250 MB/sec)
- **Storage Impact:** ❌ **Extremely high** (>10 TB/year)
- **Verdict:** **Requires ML-DSA-65 or hardware acceleration**

### 7. Performance vs Security Trade-off

| Algorithm | Security | Signing Speed | Verification Speed | Use Case |
|-----------|----------|--------------|-------------------|----------|
| **ML-DSA-44** | 128-bit | **Fastest** (0.3 ms) | **Fastest** (0.1 ms) | General purpose |
| **ML-DSA-65** | 192-bit | Fast (0.4 ms) | Fast (0.15 ms) | Financial/critical |
| **ML-DSA-87** | 256-bit | Moderate (0.5 ms) | Moderate (0.19 ms) | Maximum security |

**QSDM Choice:** ML-DSA-87 (256-bit) - **Maximum security** with acceptable performance.

### 8. Conclusion

**ML-DSA-87 Performance Summary:**
- ✅ **Signing:** 0.5 ms (2,000 TPS) - **sufficient for most blockchains**
- ✅ **Verification:** 0.19 ms (5,400 TPS) - **faster than ECDSA!**
- ⚠️ **Bandwidth:** 4.6 KB per signature - **larger but acceptable**
- ⚠️ **Storage:** ~1.45 TB per 10 years - **manageable with compression**

**Real-World Assessment:**
- **Performance:** ✅ **More than adequate** for blockchain use
- **Bandwidth:** ⚠️ **Acceptable** for most networks (<5,000 TPS)
- **Storage:** ⚠️ **Manageable** with modern storage and compression
- **Security:** ✅ **Maximum quantum resistance** (256-bit)

**Verdict:** ML-DSA-87 provides **excellent security** with **acceptable performance** for blockchain applications. The performance is sufficient for 99% of blockchain use cases, with only ultra-high-throughput networks (>5,000 TPS) potentially needing optimization.

---

## References

- WolfSSL Post-Quantum Benchmarks (2024)
- NIST Post-Quantum Cryptography Performance Analysis
- liboqs Performance Benchmarks
- Blockchain Throughput Analysis (Bitcoin, Ethereum, Solana)

---

*Last Updated: December 2024*

