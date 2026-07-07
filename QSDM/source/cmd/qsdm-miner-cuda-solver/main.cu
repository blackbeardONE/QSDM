#include <cuda_runtime.h>

#include <algorithm>
#include <array>
#include <chrono>
#include <cstdint>
#include <cstring>
#include <iomanip>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

constexpr int kRate = 136;
constexpr int kThreads = 256;
constexpr unsigned long long kMaxBatch = 4ULL * 1024ULL * 1024ULL;

constexpr std::array<uint64_t, 24> kRoundConstants = {
    0x0000000000000001ULL, 0x0000000000008082ULL,
    0x800000000000808aULL, 0x8000000080008000ULL,
    0x000000000000808bULL, 0x0000000080000001ULL,
    0x8000000080008081ULL, 0x8000000000008009ULL,
    0x000000000000008aULL, 0x0000000000000088ULL,
    0x0000000080008009ULL, 0x000000008000000aULL,
    0x000000008000808bULL, 0x800000000000008bULL,
    0x8000000000008089ULL, 0x8000000000008003ULL,
    0x8000000000008002ULL, 0x8000000000000080ULL,
    0x000000000000800aULL, 0x800000008000000aULL,
    0x8000000080008081ULL, 0x8000000000008080ULL,
    0x0000000080000001ULL, 0x8000000080008008ULL};

constexpr std::array<int, 24> kRotation = {
    1,  3,  6,  10, 15, 21, 28, 36, 45, 55, 2,  14,
    27, 41, 56, 8,  25, 43, 62, 18, 39, 61, 20, 44};

constexpr std::array<int, 24> kPiLane = {
    10, 7,  11, 17, 18, 3,  5,  16, 8,  21, 24, 4,
    15, 23, 19, 13, 12, 2,  20, 14, 22, 9,  6,  1};

__device__ __constant__ uint64_t d_round_constants[24] = {
    0x0000000000000001ULL, 0x0000000000008082ULL,
    0x800000000000808aULL, 0x8000000080008000ULL,
    0x000000000000808bULL, 0x0000000080000001ULL,
    0x8000000080008081ULL, 0x8000000000008009ULL,
    0x000000000000008aULL, 0x0000000000000088ULL,
    0x0000000080008009ULL, 0x000000008000000aULL,
    0x000000008000808bULL, 0x800000000000008bULL,
    0x8000000000008089ULL, 0x8000000000008003ULL,
    0x8000000000008002ULL, 0x8000000000000080ULL,
    0x000000000000800aULL, 0x800000008000000aULL,
    0x8000000080008081ULL, 0x8000000000008080ULL,
    0x0000000080000001ULL, 0x8000000080008008ULL};

__device__ __constant__ int d_rotation[24] = {
    1,  3,  6,  10, 15, 21, 28, 36, 45, 55, 2,  14,
    27, 41, 56, 8,  25, 43, 62, 18, 39, 61, 20, 44};

__device__ __constant__ int d_pi_lane[24] = {
    10, 7,  11, 17, 18, 3,  5,  16, 8,  21, 24, 4,
    15, 23, 19, 13, 12, 2,  20, 14, 22, 9,  6,  1};

__host__ __device__ inline uint64_t rotate_left(uint64_t value, int shift) {
  return (value << shift) | (value >> (64 - shift));
}

void keccak_host(uint64_t state[25]) {
  uint64_t columns[5];
  for (int round = 0; round < 24; ++round) {
    for (int i = 0; i < 5; ++i) {
      columns[i] = state[i] ^ state[i + 5] ^ state[i + 10] ^
                   state[i + 15] ^ state[i + 20];
    }
    for (int i = 0; i < 5; ++i) {
      const uint64_t value = columns[(i + 4) % 5] ^
                             rotate_left(columns[(i + 1) % 5], 1);
      for (int lane = i; lane < 25; lane += 5) state[lane] ^= value;
    }
    uint64_t carried = state[1];
    for (int i = 0; i < 24; ++i) {
      const int lane = kPiLane[i];
      const uint64_t previous = state[lane];
      state[lane] = rotate_left(carried, kRotation[i]);
      carried = previous;
    }
    for (int row = 0; row < 25; row += 5) {
      for (int i = 0; i < 5; ++i) columns[i] = state[row + i];
      for (int i = 0; i < 5; ++i) {
        state[row + i] = columns[i] ^
                         ((~columns[(i + 1) % 5]) & columns[(i + 2) % 5]);
      }
    }
    state[0] ^= kRoundConstants[round];
  }
}

__device__ void keccak_device(uint64_t state[25]) {
  uint64_t columns[5];
  for (int round = 0; round < 24; ++round) {
    for (int i = 0; i < 5; ++i) {
      columns[i] = state[i] ^ state[i + 5] ^ state[i + 10] ^
                   state[i + 15] ^ state[i + 20];
    }
    for (int i = 0; i < 5; ++i) {
      const uint64_t value = columns[(i + 4) % 5] ^
                             rotate_left(columns[(i + 1) % 5], 1);
      for (int lane = i; lane < 25; lane += 5) state[lane] ^= value;
    }
    uint64_t carried = state[1];
    for (int i = 0; i < 24; ++i) {
      const int lane = d_pi_lane[i];
      const uint64_t previous = state[lane];
      state[lane] = rotate_left(carried, d_rotation[i]);
      carried = previous;
    }
    for (int row = 0; row < 25; row += 5) {
      for (int i = 0; i < 5; ++i) columns[i] = state[row + i];
      for (int i = 0; i < 5; ++i) {
        state[row + i] = columns[i] ^
                         ((~columns[(i + 1) % 5]) & columns[(i + 2) % 5]);
      }
    }
    state[0] ^= d_round_constants[round];
  }
}

uint64_t load_le64_host(const uint8_t* input) {
  uint64_t value = 0;
  for (int i = 0; i < 8; ++i) value |= static_cast<uint64_t>(input[i]) << (8 * i);
  return value;
}

void store_le64_host(uint8_t* output, uint64_t value) {
  for (int i = 0; i < 8; ++i) output[i] = static_cast<uint8_t>(value >> (8 * i));
}

__device__ uint64_t load_le64_device(const uint8_t* input) {
  uint64_t value = 0;
  for (int i = 0; i < 8; ++i) value |= static_cast<uint64_t>(input[i]) << (8 * i);
  return value;
}

__device__ void store_le64_device(uint8_t* output, uint64_t value) {
  for (int i = 0; i < 8; ++i) output[i] = static_cast<uint8_t>(value >> (8 * i));
}

void sha3_256_host(const uint8_t* input, size_t length, uint8_t output[32]) {
  if (length >= kRate) throw std::runtime_error("SHA3 input exceeds one rate block");
  uint8_t block[kRate] = {};
  std::memcpy(block, input, length);
  block[length] ^= 0x06;
  block[kRate - 1] ^= 0x80;
  uint64_t state[25] = {};
  for (int i = 0; i < kRate / 8; ++i) state[i] ^= load_le64_host(block + i * 8);
  keccak_host(state);
  for (int i = 0; i < 4; ++i) store_le64_host(output + i * 8, state[i]);
}

__device__ void sha3_256_device(const uint8_t* input, int length,
                                uint8_t output[32]) {
  uint8_t block[kRate] = {};
  for (int i = 0; i < length; ++i) block[i] = input[i];
  block[length] ^= 0x06;
  block[kRate - 1] ^= 0x80;
  uint64_t state[25] = {};
  for (int i = 0; i < kRate / 8; ++i) state[i] ^= load_le64_device(block + i * 8);
  keccak_device(state);
  for (int i = 0; i < 4; ++i) store_le64_device(output + i * 8, state[i]);
}

struct SolveInput {
  uint8_t header[32];
  uint8_t batch_root[32];
  uint8_t target[32];
  uint8_t base_nonce[16];
  unsigned long long attempts;
};

struct SolveOutput {
  int found;
  unsigned long long index;
  uint8_t nonce[16];
  uint8_t mix[32];
  uint8_t hash[32];
};

__device__ void add_nonce(uint8_t nonce[16], unsigned long long increment) {
  unsigned long long carry = increment;
  for (int i = 0; i < 16 && carry != 0; ++i) {
    const unsigned int sum = static_cast<unsigned int>(nonce[i]) +
                             static_cast<unsigned int>(carry & 0xffULL);
    nonce[i] = static_cast<uint8_t>(sum & 0xffU);
    carry = (carry >> 8) + (sum >> 8);
  }
}

__device__ bool below_target(const uint8_t hash[32], const uint8_t target[32]) {
  for (int i = 0; i < 32; ++i) {
    if (hash[i] < target[i]) return true;
    if (hash[i] > target[i]) return false;
  }
  return false;
}

__global__ void solve_kernel(const uint8_t* dag, uint32_t dag_entries,
                             const SolveInput* input, SolveOutput* output) {
  const unsigned long long index =
      static_cast<unsigned long long>(blockIdx.x) * blockDim.x + threadIdx.x;
  if (index >= input->attempts) return;

  uint8_t nonce[16];
  for (int i = 0; i < 16; ++i) nonce[i] = input->base_nonce[i];
  add_nonce(nonce, index);

  uint8_t seed_input[48];
  for (int i = 0; i < 32; ++i) seed_input[i] = input->header[i];
  for (int i = 0; i < 16; ++i) seed_input[32 + i] = nonce[i];
  uint8_t mix[32];
  sha3_256_device(seed_input, 48, mix);

  uint8_t walk_input[64];
  for (int step = 0; step < 64; ++step) {
    const uint32_t dag_index =
        ((static_cast<uint32_t>(mix[0]) << 24) |
         (static_cast<uint32_t>(mix[1]) << 16) |
         (static_cast<uint32_t>(mix[2]) << 8) |
         static_cast<uint32_t>(mix[3])) % dag_entries;
    for (int i = 0; i < 32; ++i) {
      walk_input[i] = mix[i];
      walk_input[32 + i] = dag[static_cast<unsigned long long>(dag_index) * 32 + i];
    }
    sha3_256_device(walk_input, 64, mix);
  }

  uint8_t final_input[112];
  for (int i = 0; i < 32; ++i) final_input[i] = input->header[i];
  for (int i = 0; i < 16; ++i) final_input[32 + i] = nonce[i];
  for (int i = 0; i < 32; ++i) final_input[48 + i] = input->batch_root[i];
  for (int i = 0; i < 32; ++i) final_input[80 + i] = mix[i];
  uint8_t hash[32];
  sha3_256_device(final_input, 112, hash);

  if (below_target(hash, input->target) && atomicCAS(&output->found, 0, 1) == 0) {
    output->index = index;
    for (int i = 0; i < 16; ++i) output->nonce[i] = nonce[i];
    for (int i = 0; i < 32; ++i) {
      output->mix[i] = mix[i];
      output->hash[i] = hash[i];
    }
  }
}

void check_cuda(cudaError_t status, const char* operation) {
  if (status != cudaSuccess) {
    throw std::runtime_error(std::string(operation) + ": " + cudaGetErrorString(status));
  }
}

uint8_t hex_nibble(char value) {
  if (value >= '0' && value <= '9') return static_cast<uint8_t>(value - '0');
  if (value >= 'a' && value <= 'f') return static_cast<uint8_t>(value - 'a' + 10);
  if (value >= 'A' && value <= 'F') return static_cast<uint8_t>(value - 'A' + 10);
  throw std::runtime_error("non-hexadecimal character");
}

template <size_t N>
std::array<uint8_t, N> parse_hex(const std::string& text) {
  if (text.size() != N * 2) throw std::runtime_error("invalid hexadecimal width");
  std::array<uint8_t, N> output{};
  for (size_t i = 0; i < N; ++i) {
    output[i] = static_cast<uint8_t>((hex_nibble(text[i * 2]) << 4) |
                                     hex_nibble(text[i * 2 + 1]));
  }
  return output;
}

std::string to_hex(const uint8_t* data, size_t length) {
  std::ostringstream output;
  output << std::hex << std::setfill('0');
  for (size_t i = 0; i < length; ++i) output << std::setw(2) << static_cast<int>(data[i]);
  return output.str();
}

std::string text_to_hex(const std::string& text) {
  return to_hex(reinterpret_cast<const uint8_t*>(text.data()), text.size());
}

std::vector<uint8_t> build_dag(uint64_t epoch, const std::array<uint8_t, 32>& root,
                               uint32_t entries) {
  if (entries < 2) throw std::runtime_error("DAG must contain at least two entries");
  const std::string domain = "QSDM/mesh3d-pow/v1";
  std::array<uint8_t, 58> seed_input{};
  std::memcpy(seed_input.data(), domain.data(), domain.size());
  for (int i = 0; i < 8; ++i) seed_input[domain.size() + i] = static_cast<uint8_t>(epoch >> (8 * i));
  std::memcpy(seed_input.data() + domain.size() + 8, root.data(), root.size());

  std::vector<uint8_t> dag(static_cast<size_t>(entries) * 32);
  sha3_256_host(seed_input.data(), domain.size() + 8 + root.size(), dag.data());
  std::array<uint8_t, 36> next{};
  for (uint32_t i = 1; i < entries; ++i) {
    std::memcpy(next.data(), dag.data() + static_cast<size_t>(i - 1) * 32, 32);
    next[32] = static_cast<uint8_t>(i);
    next[33] = static_cast<uint8_t>(i >> 8);
    next[34] = static_cast<uint8_t>(i >> 16);
    next[35] = static_cast<uint8_t>(i >> 24);
    sha3_256_host(next.data(), next.size(), dag.data() + static_cast<size_t>(i) * 32);
  }
  return dag;
}

class SolverServer {
 public:
  SolverServer() {
    int count = 0;
    check_cuda(cudaGetDeviceCount(&count), "cudaGetDeviceCount");
    if (count < 1) throw std::runtime_error("no CUDA device is available");
    check_cuda(cudaSetDevice(0), "cudaSetDevice");
    check_cuda(cudaGetDeviceProperties(&properties_, 0), "cudaGetDeviceProperties");
    if (properties_.major < 7 || (properties_.major == 7 && properties_.minor < 5)) {
      throw std::runtime_error("GPU requires CUDA compute capability 7.5 or newer");
    }
  }

  ~SolverServer() {
    if (device_dag_) cudaFree(device_dag_);
  }

  void initialize(uint64_t epoch, const std::array<uint8_t, 32>& root,
                  uint32_t entries) {
    const auto started = std::chrono::steady_clock::now();
    auto dag = build_dag(epoch, root, entries);
    if (device_dag_) {
      check_cuda(cudaFree(device_dag_), "cudaFree DAG");
      device_dag_ = nullptr;
    }
    check_cuda(cudaMalloc(&device_dag_, dag.size()), "cudaMalloc DAG");
    check_cuda(cudaMemcpy(device_dag_, dag.data(), dag.size(), cudaMemcpyHostToDevice),
               "cudaMemcpy DAG");
    entries_ = entries;
    epoch_ = epoch;
    const auto elapsed = std::chrono::duration_cast<std::chrono::milliseconds>(
        std::chrono::steady_clock::now() - started).count();
    std::cout << "OK INIT " << text_to_hex(properties_.name) << " "
              << properties_.major << "." << properties_.minor << " "
              << entries_ << " " << elapsed << std::endl;
  }

  SolveOutput solve(const SolveInput& input, long long* duration_ms) {
    if (!device_dag_ || entries_ < 2) throw std::runtime_error("DAG is not initialized");
    if (input.attempts == 0 || input.attempts > kMaxBatch) {
      throw std::runtime_error("attempt batch is outside the supported range");
    }
    SolveInput* device_input = nullptr;
    SolveOutput* device_output = nullptr;
    SolveOutput output{};
    check_cuda(cudaMalloc(&device_input, sizeof(SolveInput)), "cudaMalloc solve input");
    check_cuda(cudaMalloc(&device_output, sizeof(SolveOutput)), "cudaMalloc solve output");
    try {
      check_cuda(cudaMemcpy(device_input, &input, sizeof(input), cudaMemcpyHostToDevice),
                 "cudaMemcpy solve input");
      check_cuda(cudaMemset(device_output, 0, sizeof(SolveOutput)), "cudaMemset solve output");
      const int blocks = static_cast<int>((input.attempts + kThreads - 1) / kThreads);
      const auto started = std::chrono::steady_clock::now();
      solve_kernel<<<blocks, kThreads>>>(device_dag_, entries_, device_input, device_output);
      check_cuda(cudaGetLastError(), "solve_kernel launch");
      check_cuda(cudaDeviceSynchronize(), "solve_kernel synchronize");
      *duration_ms = std::max<long long>(1, std::chrono::duration_cast<std::chrono::milliseconds>(
          std::chrono::steady_clock::now() - started).count());
      check_cuda(cudaMemcpy(&output, device_output, sizeof(output), cudaMemcpyDeviceToHost),
                 "cudaMemcpy solve output");
    } catch (...) {
      cudaFree(device_input);
      cudaFree(device_output);
      throw;
    }
    cudaFree(device_input);
    cudaFree(device_output);
    return output;
  }

  const cudaDeviceProp& properties() const { return properties_; }

 private:
  cudaDeviceProp properties_{};
  uint8_t* device_dag_ = nullptr;
  uint32_t entries_ = 0;
  uint64_t epoch_ = 0;
};

void run_self_test(SolverServer& server) {
  std::array<uint8_t, 32> root{};
  root[0] = 0x42;
  server.initialize(7, root, 128);
  SolveInput input{};
  input.header[0] = 0x5e;
  input.batch_root[0] = 0xa1;
  std::memset(input.target, 0xff, sizeof(input.target));
  input.base_nonce[0] = 0x11;
  input.attempts = 1;
  long long elapsed = 0;
  const SolveOutput output = server.solve(input, &elapsed);
  if (!output.found || std::memcmp(output.nonce, input.base_nonce, 16) != 0) {
    throw std::runtime_error("CUDA solver self-test did not return the expected nonce");
  }

  const auto dag = build_dag(7, root, 128);
  uint8_t seed_input[48];
  std::memcpy(seed_input, input.header, 32);
  std::memcpy(seed_input + 32, input.base_nonce, 16);
  uint8_t mix[32];
  sha3_256_host(seed_input, sizeof(seed_input), mix);
  uint8_t walk_input[64];
  for (int step = 0; step < 64; ++step) {
    const uint32_t index = ((static_cast<uint32_t>(mix[0]) << 24) |
                            (static_cast<uint32_t>(mix[1]) << 16) |
                            (static_cast<uint32_t>(mix[2]) << 8) |
                            static_cast<uint32_t>(mix[3])) % 128;
    std::memcpy(walk_input, mix, 32);
    std::memcpy(walk_input + 32, dag.data() + static_cast<size_t>(index) * 32, 32);
    sha3_256_host(walk_input, sizeof(walk_input), mix);
  }
  if (std::memcmp(mix, output.mix, 32) != 0) {
    throw std::runtime_error("CUDA SHA3/DAG result differs from the host reference");
  }
}

void serve() {
  SolverServer server;
  run_self_test(server);
  std::cout << "READY " << text_to_hex(server.properties().name) << " "
            << server.properties().major << "." << server.properties().minor << std::endl;

  std::string line;
  while (std::getline(std::cin, line)) {
    try {
      std::istringstream command(line);
      std::string operation;
      command >> operation;
      if (operation == "INIT") {
        uint64_t epoch = 0;
        uint32_t entries = 0;
        std::string root_hex;
        if (!(command >> epoch >> root_hex >> entries)) throw std::runtime_error("malformed INIT command");
        server.initialize(epoch, parse_hex<32>(root_hex), entries);
      } else if (operation == "SOLVE") {
        std::string header_hex, batch_hex, target_hex, nonce_hex;
        unsigned long long attempts = 0;
        if (!(command >> header_hex >> batch_hex >> target_hex >> nonce_hex >> attempts)) {
          throw std::runtime_error("malformed SOLVE command");
        }
        SolveInput input{};
        const auto header = parse_hex<32>(header_hex);
        const auto batch = parse_hex<32>(batch_hex);
        const auto target = parse_hex<32>(target_hex);
        const auto nonce = parse_hex<16>(nonce_hex);
        std::memcpy(input.header, header.data(), 32);
        std::memcpy(input.batch_root, batch.data(), 32);
        std::memcpy(input.target, target.data(), 32);
        std::memcpy(input.base_nonce, nonce.data(), 16);
        input.attempts = attempts;
        long long elapsed = 0;
        const SolveOutput output = server.solve(input, &elapsed);
        if (output.found) {
          std::cout << "OK SOLVE 1 " << to_hex(output.nonce, 16) << " "
                    << to_hex(output.mix, 32) << " " << to_hex(output.hash, 32)
                    << " " << attempts << " " << elapsed << std::endl;
        } else {
          std::cout << "OK SOLVE 0 - - - " << attempts << " " << elapsed << std::endl;
        }
      } else if (operation == "PING") {
        std::cout << "OK PONG" << std::endl;
      } else if (operation == "QUIT") {
        std::cout << "OK QUIT" << std::endl;
        return;
      } else {
        throw std::runtime_error("unknown command");
      }
    } catch (const std::exception& error) {
      std::cout << "ERR " << error.what() << std::endl;
    }
  }
}

}  // namespace

int main(int argc, char** argv) {
  try {
    if (argc == 2 && std::string(argv[1]) == "--server") {
      serve();
      return 0;
    }
    if (argc == 2 && std::string(argv[1]) == "--self-test") {
      SolverServer server;
      run_self_test(server);
      std::cout << "CUDA solver self-test OK on " << server.properties().name << std::endl;
      return 0;
    }
    std::cerr << "usage: qsdm-miner-cuda-solver --server | --self-test\n";
    return 2;
  } catch (const std::exception& error) {
    std::cerr << "qsdm-miner-cuda-solver: " << error.what() << "\n";
    return 1;
  }
}
