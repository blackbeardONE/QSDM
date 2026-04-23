// CUDA kernels for QSDM+ mesh3d: parallel SHA-256 hashing and parent-cell validation.
// Compile: nvcc -shared -o mesh3d_kernels.dll sha256_validate.cu (Windows)
//          nvcc -shared -fPIC -o mesh3d_kernels.so sha256_validate.cu (Linux)

#include <stdint.h>
#include <string.h>
#include <cuda_runtime.h>

// SHA-256 constants
__constant__ uint32_t K[64] = {
    0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5,
    0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
    0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3,
    0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
    0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc,
    0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
    0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7,
    0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
    0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13,
    0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
    0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3,
    0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
    0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5,
    0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
    0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208,
    0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2
};

__device__ uint32_t rotr(uint32_t x, uint32_t n) {
    return (x >> n) | (x << (32 - n));
}

__device__ uint32_t ch(uint32_t x, uint32_t y, uint32_t z) {
    return (x & y) ^ (~x & z);
}

__device__ uint32_t maj(uint32_t x, uint32_t y, uint32_t z) {
    return (x & y) ^ (x & z) ^ (y & z);
}

__device__ uint32_t sigma0(uint32_t x) {
    return rotr(x, 2) ^ rotr(x, 13) ^ rotr(x, 22);
}

__device__ uint32_t sigma1(uint32_t x) {
    return rotr(x, 6) ^ rotr(x, 11) ^ rotr(x, 25);
}

__device__ uint32_t gamma0(uint32_t x) {
    return rotr(x, 7) ^ rotr(x, 18) ^ (x >> 3);
}

__device__ uint32_t gamma1(uint32_t x) {
    return rotr(x, 17) ^ rotr(x, 19) ^ (x >> 10);
}

// Single-block SHA-256 for data up to 55 bytes.
// Output: 32 bytes written to `out`.
__device__ void sha256_block(const uint8_t *data, uint32_t len, uint8_t *out) {
    uint32_t h0 = 0x6a09e667, h1 = 0xbb67ae85, h2 = 0x3c6ef372, h3 = 0xa54ff53a;
    uint32_t h4 = 0x510e527f, h5 = 0x9b05688c, h6 = 0x1f83d9ab, h7 = 0x5be0cd19;

    // Prepare message block (padded)
    uint8_t block[64];
    memset(block, 0, 64);
    if (len > 55) len = 55; // single-block limit
    memcpy(block, data, len);
    block[len] = 0x80;
    uint64_t bitlen = (uint64_t)len * 8;
    for (int i = 0; i < 8; i++) {
        block[63 - i] = (uint8_t)(bitlen >> (i * 8));
    }

    // Parse into 16 words
    uint32_t W[64];
    for (int i = 0; i < 16; i++) {
        W[i] = ((uint32_t)block[i*4] << 24) |
               ((uint32_t)block[i*4+1] << 16) |
               ((uint32_t)block[i*4+2] << 8) |
               ((uint32_t)block[i*4+3]);
    }
    for (int i = 16; i < 64; i++) {
        W[i] = gamma1(W[i-2]) + W[i-7] + gamma0(W[i-15]) + W[i-16];
    }

    uint32_t a=h0, b=h1, c=h2, d=h3, e=h4, f=h5, g=h6, h=h7;

    for (int i = 0; i < 64; i++) {
        uint32_t t1 = h + sigma1(e) + ch(e,f,g) + K[i] + W[i];
        uint32_t t2 = sigma0(a) + maj(a,b,c);
        h = g; g = f; f = e; e = d + t1;
        d = c; c = b; b = a; a = t1 + t2;
    }

    h0 += a; h1 += b; h2 += c; h3 += d;
    h4 += e; h5 += f; h6 += g; h7 += h;

    uint32_t hash[8] = {h0, h1, h2, h3, h4, h5, h6, h7};
    for (int i = 0; i < 8; i++) {
        out[i*4]   = (hash[i] >> 24) & 0xff;
        out[i*4+1] = (hash[i] >> 16) & 0xff;
        out[i*4+2] = (hash[i] >> 8)  & 0xff;
        out[i*4+3] = (hash[i])       & 0xff;
    }
}

// Each CUDA thread hashes one parent cell.
// d_data: flat array of all cells concatenated
// d_offsets: byte offset of cell[i] in d_data
// d_lengths: byte length of cell[i]
// d_hashes: output, 32 bytes per cell
// n: number of cells
__global__ void hash_parent_cells(
    const uint8_t *d_data,
    const uint32_t *d_offsets,
    const uint32_t *d_lengths,
    uint8_t *d_hashes,
    int n
) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= n) return;

    const uint8_t *cell = d_data + d_offsets[idx];
    uint32_t cell_len = d_lengths[idx];
    uint8_t *hash_out = d_hashes + idx * 32;

    sha256_block(cell, cell_len, hash_out);
}

// Each thread validates one cell:
// - Must be >= 32 bytes
// - SHA-256 hash must not be all zeros
// d_results: output, 1 = valid, 0 = invalid
__global__ void validate_parent_cells(
    const uint8_t *d_data,
    const uint32_t *d_offsets,
    const uint32_t *d_lengths,
    int *d_results,
    int n
) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= n) return;

    uint32_t cell_len = d_lengths[idx];
    if (cell_len < 32) {
        d_results[idx] = 0;
        return;
    }

    uint8_t hash[32];
    sha256_block(d_data + d_offsets[idx], cell_len, hash);

    int all_zero = 1;
    for (int i = 0; i < 32; i++) {
        if (hash[i] != 0) { all_zero = 0; break; }
    }

    d_results[idx] = all_zero ? 0 : 1;
}

// --- Host API (called from Go via CGO) ---

// MESH3D_API makes the host-side entry points visible in the produced
// shared library. On ELF (.so) symbols are public by default; on PE
// (.dll) MSVC and ld-link ONLY export what is explicitly marked
// __declspec(dllexport), which is why we used to ship a Windows DLL
// with a single NvOptimusEnablementCuda export and nothing else —
// CGO then failed to resolve mesh3d_hash_cells / mesh3d_validate_cells
// at link time. Keep this macro next to every extern "C" entry point.
#ifdef _WIN32
#define MESH3D_API __declspec(dllexport)
#else
#define MESH3D_API __attribute__((visibility("default")))
#endif

extern "C" {

// mesh3d_hash_cells hashes `n` cells on GPU.
// Returns 0 on success, non-zero on CUDA error.
MESH3D_API int mesh3d_hash_cells(
    const uint8_t *h_data,    // flat cell data
    const uint32_t *h_offsets,
    const uint32_t *h_lengths,
    uint8_t *h_hashes,        // output: n*32 bytes
    int n,
    uint32_t total_bytes
) {
    uint8_t *d_data = NULL;
    uint32_t *d_offsets = NULL, *d_lengths = NULL;
    uint8_t *d_hashes = NULL;

    cudaMalloc(&d_data, total_bytes);
    cudaMalloc(&d_offsets, n * sizeof(uint32_t));
    cudaMalloc(&d_lengths, n * sizeof(uint32_t));
    cudaMalloc(&d_hashes, n * 32);

    cudaMemcpy(d_data, h_data, total_bytes, cudaMemcpyHostToDevice);
    cudaMemcpy(d_offsets, h_offsets, n * sizeof(uint32_t), cudaMemcpyHostToDevice);
    cudaMemcpy(d_lengths, h_lengths, n * sizeof(uint32_t), cudaMemcpyHostToDevice);

    int threads = 256;
    int blocks = (n + threads - 1) / threads;
    hash_parent_cells<<<blocks, threads>>>(d_data, d_offsets, d_lengths, d_hashes, n);

    cudaError_t err = cudaDeviceSynchronize();
    if (err != cudaSuccess) {
        cudaFree(d_data); cudaFree(d_offsets); cudaFree(d_lengths); cudaFree(d_hashes);
        return (int)err;
    }

    cudaMemcpy(h_hashes, d_hashes, n * 32, cudaMemcpyDeviceToHost);

    cudaFree(d_data); cudaFree(d_offsets); cudaFree(d_lengths); cudaFree(d_hashes);
    return 0;
}

// mesh3d_validate_cells validates `n` cells on GPU.
// h_results: output array of `n` ints (1=valid, 0=invalid).
// Returns 0 on success.
MESH3D_API int mesh3d_validate_cells(
    const uint8_t *h_data,
    const uint32_t *h_offsets,
    const uint32_t *h_lengths,
    int *h_results,
    int n,
    uint32_t total_bytes
) {
    uint8_t *d_data = NULL;
    uint32_t *d_offsets = NULL, *d_lengths = NULL;
    int *d_results = NULL;

    cudaMalloc(&d_data, total_bytes);
    cudaMalloc(&d_offsets, n * sizeof(uint32_t));
    cudaMalloc(&d_lengths, n * sizeof(uint32_t));
    cudaMalloc(&d_results, n * sizeof(int));

    cudaMemcpy(d_data, h_data, total_bytes, cudaMemcpyHostToDevice);
    cudaMemcpy(d_offsets, h_offsets, n * sizeof(uint32_t), cudaMemcpyHostToDevice);
    cudaMemcpy(d_lengths, h_lengths, n * sizeof(uint32_t), cudaMemcpyHostToDevice);

    int threads = 256;
    int blocks = (n + threads - 1) / threads;
    validate_parent_cells<<<blocks, threads>>>(d_data, d_offsets, d_lengths, d_results, n);

    cudaError_t err = cudaDeviceSynchronize();
    if (err != cudaSuccess) {
        cudaFree(d_data); cudaFree(d_offsets); cudaFree(d_lengths); cudaFree(d_results);
        return (int)err;
    }

    cudaMemcpy(h_results, d_results, n * sizeof(int), cudaMemcpyDeviceToHost);

    cudaFree(d_data); cudaFree(d_offsets); cudaFree(d_lengths); cudaFree(d_results);
    return 0;
}

} // extern "C"
