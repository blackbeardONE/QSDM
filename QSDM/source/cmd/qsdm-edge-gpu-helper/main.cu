#include <cuda_runtime.h>

#include <algorithm>
#include <chrono>
#include <cstdint>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <iomanip>
#include <iostream>
#include <sstream>
#include <stdexcept>
#include <string>

namespace {

__device__ __forceinline__ unsigned long long splitmix64(unsigned long long value) {
  value += 0x9e3779b97f4a7c15ULL;
  value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9ULL;
  value = (value ^ (value >> 27)) * 0x94d049bb133111ebULL;
  return value ^ (value >> 31);
}

__global__ void edge_mix_kernel(unsigned long long seed,
                                unsigned long long units,
                                unsigned long long* xor_value,
                                unsigned long long* sum_value) {
  const unsigned long long index =
      static_cast<unsigned long long>(blockIdx.x) * blockDim.x + threadIdx.x;
  const unsigned long long stride =
      static_cast<unsigned long long>(blockDim.x) * gridDim.x;
  unsigned long long local_xor = 0;
  unsigned long long local_sum = 0;
  for (unsigned long long i = index; i < units; i += stride) {
    const unsigned long long value = splitmix64(seed + i);
    local_xor ^= value;
    local_sum += value;
  }
  atomicXor(xor_value, local_xor);
  atomicAdd(sum_value, local_sum);
}

void check_cuda(cudaError_t status, const char* operation) {
  if (status != cudaSuccess) {
    std::ostringstream message;
    message << operation << ": " << cudaGetErrorString(status);
    throw std::runtime_error(message.str());
  }
}

unsigned char hex_nibble(char value) {
  if (value >= '0' && value <= '9') return static_cast<unsigned char>(value - '0');
  if (value >= 'a' && value <= 'f') return static_cast<unsigned char>(value - 'a' + 10);
  if (value >= 'A' && value <= 'F') return static_cast<unsigned char>(value - 'A' + 10);
  throw std::runtime_error("seed contains a non-hexadecimal character");
}

unsigned long long seed64_from_hex(const std::string& seed) {
  if (seed.size() != 64) {
    throw std::runtime_error("seed must contain exactly 64 hexadecimal characters");
  }
  unsigned long long value = 0;
  for (int index = 0; index < 8; ++index) {
    const unsigned char byte = static_cast<unsigned char>(
        (hex_nibble(seed[index * 2]) << 4) | hex_nibble(seed[index * 2 + 1]));
    value |= static_cast<unsigned long long>(byte) << (index * 8);
  }
  return value;
}

std::string json_escape(const char* raw) {
  std::ostringstream escaped;
  for (const unsigned char value : std::string(raw ? raw : "")) {
    switch (value) {
      case '\\': escaped << "\\\\"; break;
      case '"': escaped << "\\\""; break;
      case '\n': escaped << "\\n"; break;
      case '\r': escaped << "\\r"; break;
      case '\t': escaped << "\\t"; break;
      default:
        if (value < 0x20) {
          escaped << "\\u" << std::hex << std::setw(4) << std::setfill('0')
                  << static_cast<int>(value) << std::dec;
        } else {
          escaped << static_cast<char>(value);
        }
    }
  }
  return escaped.str();
}

std::string device_uuid(int device) {
  char bus_id[32] = {};
  if (cudaDeviceGetPCIBusId(bus_id, sizeof(bus_id), device) != cudaSuccess) {
    return "";
  }
  return bus_id;
}

void print_usage() {
  std::cerr << "qsdm-edge-gpu-helper --seed <64-hex> --units <count> --json\n";
}

}  // namespace

int main(int argc, char** argv) {
  try {
    std::string seed;
    unsigned long long units = 0;
    bool json = false;
    for (int index = 1; index < argc; ++index) {
      const std::string arg = argv[index];
      if (arg == "--seed" && index + 1 < argc) {
        seed = argv[++index];
      } else if (arg == "--units" && index + 1 < argc) {
        units = std::stoull(argv[++index]);
      } else if (arg == "--json") {
        json = true;
      } else if (arg == "--help" || arg == "-h") {
        print_usage();
        return 0;
      } else {
        throw std::runtime_error("unknown or incomplete argument: " + arg);
      }
    }
    if (!json || seed.empty() || units == 0 || units > 100000000ULL) {
      print_usage();
      return 2;
    }

    int device = 0;
    int device_count = 0;
    check_cuda(cudaGetDeviceCount(&device_count), "cudaGetDeviceCount");
    if (device_count < 1) {
      throw std::runtime_error("no CUDA device is available");
    }
    check_cuda(cudaSetDevice(device), "cudaSetDevice");
    cudaDeviceProp properties{};
    check_cuda(cudaGetDeviceProperties(&properties, device), "cudaGetDeviceProperties");
    if (properties.major < 7 || (properties.major == 7 && properties.minor < 5)) {
      throw std::runtime_error("GPU requires CUDA compute capability 7.5 or newer");
    }

    unsigned long long* device_xor = nullptr;
    unsigned long long* device_sum = nullptr;
    check_cuda(cudaMalloc(&device_xor, sizeof(unsigned long long)), "cudaMalloc xor");
    check_cuda(cudaMalloc(&device_sum, sizeof(unsigned long long)), "cudaMalloc sum");
    check_cuda(cudaMemset(device_xor, 0, sizeof(unsigned long long)), "cudaMemset xor");
    check_cuda(cudaMemset(device_sum, 0, sizeof(unsigned long long)), "cudaMemset sum");

    const int threads = 256;
    const unsigned long long needed_blocks = (units + threads - 1) / threads;
    const int blocks = static_cast<int>(std::min<unsigned long long>(needed_blocks, 4096));
    const auto started = std::chrono::steady_clock::now();
    edge_mix_kernel<<<blocks, threads>>>(
        seed64_from_hex(seed), units, device_xor, device_sum);
    check_cuda(cudaGetLastError(), "edge_mix_kernel launch");
    check_cuda(cudaDeviceSynchronize(), "edge_mix_kernel synchronize");

    unsigned long long xor_value = 0;
    unsigned long long sum_value = 0;
    check_cuda(cudaMemcpy(&xor_value, device_xor, sizeof(xor_value), cudaMemcpyDeviceToHost),
               "cudaMemcpy xor");
    check_cuda(cudaMemcpy(&sum_value, device_sum, sizeof(sum_value), cudaMemcpyDeviceToHost),
               "cudaMemcpy sum");
    cudaFree(device_xor);
    cudaFree(device_sum);
    const auto duration_ms = std::max<long long>(
        1, std::chrono::duration_cast<std::chrono::milliseconds>(
               std::chrono::steady_clock::now() - started)
               .count());

    std::ostringstream xor_hex;
    xor_hex << std::hex << std::setw(16) << std::setfill('0') << xor_value;
    std::ostringstream sum_hex;
    sum_hex << std::hex << std::setw(16) << std::setfill('0') << sum_value;
    std::cout << "{\"xor_value\":\"" << xor_hex.str()
              << "\",\"sum_value\":\"" << sum_hex.str()
              << "\",\"gpu_name\":\"" << json_escape(properties.name)
              << "\",\"gpu_uuid\":\"" << json_escape(device_uuid(device).c_str())
              << "\",\"duration_ms\":" << duration_ms
              << ",\"units\":" << units << "}\n";
    return 0;
  } catch (const std::exception& error) {
    std::cerr << "qsdm-edge-gpu-helper: " << error.what() << "\n";
    return 1;
  }
}
