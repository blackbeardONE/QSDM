# Reproducible build for the QSDM mesh3d CUDA kernels.
#
# Usage:
#   make           # builds libmesh3d_kernels.so (Linux) or .dll (Windows MSYS2)
#   make clean
#   make sm_70     # build for a specific compute capability only
#   make all_sm    # build a fat binary covering sm_50 .. sm_90
#
# The `cuda-build` GitHub Actions workflow invokes `make all_sm` and
# uploads the produced library as a release artefact. The release-time
# binary therefore runs on every NVIDIA GPU with compute capability
# >= 5.0 without requiring per-target nvcc on the operator host.
#
# Override toolchain via env or `make NVCC=/opt/cuda-12/bin/nvcc`.

NVCC      ?= nvcc
SRC       := sha256_validate.cu
OBJ_DIR   ?= build
BUILD_DIR ?= $(OBJ_DIR)

# Default compute capability list — every still-supported NVIDIA GPU
# from the Maxwell GTX 9xx (sm_50) to the Hopper H100 (sm_90).
GENCODES ?= \
    -gencode arch=compute_50,code=sm_50 \
    -gencode arch=compute_60,code=sm_60 \
    -gencode arch=compute_70,code=sm_70 \
    -gencode arch=compute_75,code=sm_75 \
    -gencode arch=compute_80,code=sm_80 \
    -gencode arch=compute_86,code=sm_86 \
    -gencode arch=compute_89,code=sm_89 \
    -gencode arch=compute_90,code=sm_90 \
    -gencode arch=compute_90,code=compute_90

# Compiler flags
#   -O3                    optimisation level
#   --use_fast_math        reciprocal/sqrt approximations OK for SHA-256
#                          (loops dominate; no FP transcendentals)
#   -Xcompiler -fPIC       relocatable host code, required for shared object
#   -lineinfo              minimal source mapping in the cubin so cuda-gdb /
#                          Nsight Compute can attribute hot-spots without
#                          shipping full debug symbols
#   -Werror cross-execution-space-call  catches host/device function
#                          mismatches at compile time (otherwise nvcc
#                          warns + emits silently broken code)
NVCC_FLAGS ?= \
    -O3 \
    --use_fast_math \
    -Xcompiler -fPIC \
    -lineinfo \
    -Werror cross-execution-space-call

UNAME_S := $(shell uname -s 2>/dev/null || echo Windows_NT)
ifeq ($(UNAME_S),Linux)
    LIB_EXT := so
    LIB_NAME := libmesh3d_kernels.$(LIB_EXT)
    SHARED_FLAG := -shared
else ifeq ($(UNAME_S),Darwin)
    LIB_EXT := dylib
    LIB_NAME := libmesh3d_kernels.$(LIB_EXT)
    SHARED_FLAG := -shared
else
    # Windows (MSYS2 / cygwin make).
    LIB_EXT := dll
    LIB_NAME := mesh3d_kernels.$(LIB_EXT)
    SHARED_FLAG := -shared
endif

OUT := $(BUILD_DIR)/$(LIB_NAME)

.PHONY: all clean all_sm
.DEFAULT_GOAL := all

all: $(OUT)

$(OUT): $(SRC) | $(BUILD_DIR)
	$(NVCC) $(NVCC_FLAGS) $(GENCODES) $(SHARED_FLAG) -o $@ $(SRC)

$(BUILD_DIR):
	@mkdir -p $@

# Build a fat binary covering every supported compute capability.
all_sm: $(OUT)

# Per-target: e.g. `make sm_80`
sm_%: $(SRC) | $(BUILD_DIR)
	$(NVCC) $(NVCC_FLAGS) -gencode arch=compute_$*,code=sm_$* $(SHARED_FLAG) -o $(BUILD_DIR)/$(LIB_NAME) $(SRC)

clean:
	@rm -rf $(BUILD_DIR)
