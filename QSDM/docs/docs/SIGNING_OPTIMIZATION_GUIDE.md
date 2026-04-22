# Quick Guide: Optimize Signing Speed

## Fastest Option: Switch to ML-DSA-44

**Current:** ML-DSA-87 (0.5 ms signing, 256-bit security)  
**Optimized:** ML-DSA-44 (0.3 ms signing, 128-bit security)

**Change in `pkg/crypto/dilithium.go` (line ~163):**

```go
// Change from:
cname := C.CString("ML-DSA-87")

// To:
cname := C.CString("ML-DSA-44")  // 1.67x faster, 128-bit security (same as Bitcoin)
```

**Result:**
- ✅ **0.5 ms → 0.3 ms** (1.67x faster)
- ✅ **Still quantum-safe** (128-bit security)
- ✅ **Same security level as Bitcoin/Ethereum** (but quantum-safe!)

---

## Balanced Option: Switch to ML-DSA-65

**Change to:**

```go
cname := C.CString("ML-DSA-65")  // 1.25x faster, 192-bit security
```

**Result:**
- ✅ **0.5 ms → 0.4 ms** (1.25x faster)
- ✅ **Higher security than Bitcoin/Ethereum** (192-bit vs 128-bit)
- ✅ **Still quantum-safe**

---

## Comparison

| Algorithm | Signing | Security | vs Bitcoin |
|-----------|---------|----------|------------|
| **ML-DSA-87** | 0.50 ms | 256-bit | 3.6x slower |
| **ML-DSA-65** | 0.40 ms | 192-bit | 2.9x slower |
| **ML-DSA-44** | **0.30 ms** ✅ | 128-bit | 2.1x slower |
| **Bitcoin** | 0.14 ms | 128-bit | Baseline |

**Recommendation:** Use **ML-DSA-44** for maximum speed while maintaining quantum safety.

---

## After Changing Algorithm

1. **Rebuild liboqs** (if needed):
   ```powershell
   .\rebuild_liboqs.ps1
   ```

2. **Rebuild QSDM**:
   ```powershell
   .\build.ps1
   ```

3. **Test**:
   ```powershell
   .\run.ps1
   ```

---

## Additional Optimizations (Optional)

### 1. Pre-computation (10-20% improvement)

Cache NTT tables and polynomial operations. Requires custom implementation.

### 2. Memory Pool (2-5% improvement)

Reuse memory buffers to reduce allocations.

### 3. Batch Signing (10x improvement for batches)

Sign multiple transactions in parallel.

---

## Expected Final Performance

| Optimization | Signing Speed | Improvement |
|--------------|---------------|-------------|
| **ML-DSA-87 (current)** | 0.50 ms | Baseline |
| **ML-DSA-44** | 0.30 ms | 1.67x faster |
| **+ Pre-computation** | 0.27 ms | 1.85x faster |
| **+ Memory optimization** | 0.26 ms | 1.92x faster |

**Best achievable:** ~0.26 ms (1.86x slower than ECDSA, but acceptable)

---

*Last Updated: December 2024*

