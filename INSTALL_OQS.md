# Installation Guide for Open Quantum Safe (OQS) Library on Windows 10

This guide will help you install the Open Quantum Safe (OQS) library and its development headers required for building the QSDM project with Dilithium quantum-safe cryptography.

## Prerequisites

- Git Bash or similar terminal on Windows 10
- CMake (https://cmake.org/download/)
- Visual Studio with C++ development tools installed
- Git (https://git-scm.com/download/win)

## Steps

1. **Clone the OQS-OpenSSL repository**

```bash
git clone https://github.com/open-quantum-safe/liboqs.git
cd liboqs
```

2. **Create a build directory and configure**

```bash
mkdir build && cd build
cmake -G "Visual Studio 16 2019" -A x64 ..
```

Adjust the generator and architecture according to your Visual Studio version.

3. **Build the library**

Open the generated solution file (`liboqs.sln`) in Visual Studio and build the ALL_BUILD project in Release mode.

Alternatively, build from command line:

```bash
cmake --build . --config Release
```

4. **Install the library**

```bash
cmake --install . --config Release --prefix "C:/Program Files/liboqs"
```

5. **Set environment variables**

Add the include and lib paths to your environment variables or configure your Go build to find them:

- Include path: `C:/Program Files/liboqs/include`
- Library path: `C:/Program Files/liboqs/lib`

6. **Verify installation**

Ensure that `oqs/oqs.h` is present in the include directory and the library files are in the lib directory.

## Additional Notes

- You may need to adjust your Go build flags to include the OQS include and lib paths, for example:

```bash
CGO_CFLAGS="-IC:/Program Files/liboqs/include" CGO_LDFLAGS="-LC:/Program Files/liboqs/lib" CGO_ENABLED=1 go run cmd/qsmd/main.go
```

- For more details, refer to the official OQS repository: https://github.com/open-quantum-safe/liboqs

---

This setup will enable the QSDM project to build and link against the real Dilithium quantum-safe cryptography implementation.
