# Signing Speed Optimization Strategy

## Current Performance

| Algorithm | Signing Speed | Target | Gap |
|-----------|---------------|--------|-----|
| **QSDM (ML-DSA-87)** | 0.50 ms | 0.14 ms (Bitcoin) | 3.6x slower |
| Bitcoin (ECDSA) | 0.14 ms | - | Baseline |
| Ethereum (ECDSA) | 0.14 ms | - | Baseline |

**Goal:** Match or beat 0.14 ms signing speed.

---

## Optimization Strategies

### 1. Algorithm Selection (Quick Win)

**Option A: ML-DSA-44 (128-bit security)**
- **Signing:** 0.3 ms (1.67x faster than ML-DSA-87)
- **Security:** 128-bit (same as Bitcoin/Ethereum)
- **Result:** 2.1x faster than ML-DSA-87, still 2.1x slower than ECDSA

**Option B: ML-DSA-65 (192-bit security)**
- **Signing:** 0.4 ms (1.25x faster than ML-DSA-87)
- **Security:** 192-bit (higher than Bitcoin/Ethereum)
- **Result:** 1.5x faster than ML-DSA-87, still 2.9x slower than ECDSA

**Recommendation:** Use ML-DSA-44 for maximum speed, or ML-DSA-65 for balanced security/speed.

### 2. Pre-computation and Caching

**Strategy:** Pre-compute and cache intermediate values that don't change between signatures.

**Implementation:**
- Cache NTT (Number Theoretic Transform) tables
- Pre-compute polynomial operations
- Reuse computation for multiple signatures

**Expected Improvement:** 10-20% faster (0.5 ms → 0.4-0.45 ms)

### 3. Parallel Batch Signing

**Strategy:** Sign multiple transactions in parallel using goroutines.

**Implementation:**
```go
// Sign 10 transactions in parallel
// Each takes 0.5 ms, but total time is ~0.05 ms per signature
```

**Expected Improvement:** 10x faster per signature in batches (0.5 ms → 0.05 ms)

**Note:** Only helps for batch operations, not single signatures.

### 4. CGO Call Optimization

**Strategy:** Reduce CGO call overhead by batching operations.

**Current:** Each Sign() call = 1 CGO call
**Optimized:** Batch multiple operations in single CGO call

**Expected Improvement:** 5-10% faster (0.5 ms → 0.45-0.475 ms)

### 5. Memory Pool Optimization

**Strategy:** Reuse memory buffers to reduce allocations.

**Expected Improvement:** 2-5% faster (0.5 ms → 0.475-0.49 ms)

### 6. Hybrid Approach: Fast Path for Common Cases

**Strategy:** Use faster algorithm (ML-DSA-44) for high-frequency transactions, ML-DSA-87 for critical transactions.

**Implementation:**
- Low-value transactions: ML-DSA-44 (0.3 ms)
- High-value transactions: ML-DSA-87 (0.5 ms)

**Expected Improvement:** Average 0.3-0.4 ms for most transactions

---

## Recommended Approach

### Phase 1: Algorithm Selection (Immediate - 5 minutes)

**Switch to ML-DSA-44 for maximum speed:**
- **Result:** 0.5 ms → 0.3 ms (1.67x faster)
- **Still:** 2.1x slower than ECDSA, but acceptable

**Or use ML-DSA-65 for balanced approach:**
- **Result:** 0.5 ms → 0.4 ms (1.25x faster)
- **Still:** 2.9x slower than ECDSA, but higher security

### Phase 2: Pre-computation (1-2 hours)

**Cache intermediate values:**
- Pre-compute NTT tables
- Cache polynomial operations
- **Result:** Additional 10-20% improvement

### Phase 3: Memory Optimization (30 minutes)

**Optimize memory allocations:**
- Use memory pools
- Reduce allocations
- **Result:** Additional 2-5% improvement

---

## Expected Final Results

### With ML-DSA-44 + Optimizations

| Optimization | Signing Speed | Improvement |
|--------------|---------------|-------------|
| **Baseline (ML-DSA-87)** | 0.50 ms | - |
| **ML-DSA-44** | 0.30 ms | 1.67x faster |
| **+ Pre-computation** | 0.27 ms | 1.85x faster |
| **+ Memory optimization** | 0.26 ms | 1.92x faster |

**Final:** 0.26 ms (still 1.86x slower than ECDSA, but very close!)

### With ML-DSA-65 + Optimizations

| Optimization | Signing Speed | Improvement |
|--------------|---------------|-------------|
| **Baseline (ML-DSA-87)** | 0.50 ms | - |
| **ML-DSA-65** | 0.40 ms | 1.25x faster |
| **+ Pre-computation** | 0.36 ms | 1.39x faster |
| **+ Memory optimization** | 0.35 ms | 1.43x faster |

**Final:** 0.35 ms (2.5x slower than ECDSA, but higher security)

---

## Realistic Assessment

**To match ECDSA (0.14 ms):**
- ⚠️ **Very difficult** - ML-DSA is fundamentally more complex
- ⚠️ **May require:** Custom assembly, hardware acceleration, or different algorithm

**Best achievable:**
- **ML-DSA-44:** ~0.26-0.30 ms (2-2.1x slower than ECDSA)
- **ML-DSA-65:** ~0.35-0.40 ms (2.5-2.9x slower than ECDSA)
- **ML-DSA-87:** ~0.45-0.50 ms (3.2-3.6x slower than ECDSA)

**Verdict:** Can get **close** to ECDSA speed, but likely won't match it exactly. However, **0.26-0.30 ms is still excellent** for blockchain use.

---

## Alternative: Hybrid Algorithm Approach

**Use different algorithms for different use cases:**

| Use Case | Algorithm | Signing Speed | Security |
|----------|-----------|---------------|----------|
| **High-frequency transactions** | ML-DSA-44 | 0.30 ms | 128-bit |
| **Critical transactions** | ML-DSA-87 | 0.50 ms | 256-bit |
| **Balanced** | ML-DSA-65 | 0.40 ms | 192-bit |

**Average:** ~0.35 ms (if 50/50 split)

---

## Conclusion

**Best strategy:**
1. ✅ **Switch to ML-DSA-44** for maximum speed (0.3 ms)
2. ✅ **Add pre-computation** for 10-20% improvement (0.27 ms)
3. ✅ **Optimize memory** for 2-5% improvement (0.26 ms)

**Final result:** ~0.26 ms signing (1.86x slower than ECDSA, but acceptable)

**Trade-off:** Lower security (128-bit vs 256-bit), but still quantum-safe and faster than current.

---

*Last Updated: December 2024*

