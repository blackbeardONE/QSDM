# PowerShell script to build QSDM with CGO enabled (full features)
# This requires liboqs and other C dependencies to be installed

Write-Host "Building QSDM with CGO enabled (full features)..." -ForegroundColor Cyan
Write-Host "This build includes:" -ForegroundColor Green
Write-Host "  - WASM module support"
Write-Host "  - Quantum-safe cryptography (liboqs)"
Write-Host "  - CUDA acceleration (if available)"
Write-Host "  - SQLite storage"
Write-Host ""

# Set Go environment
$env:GOROOT = "C:\Program Files\Go"
$env:PATH = "C:\Program Files\Go\bin;$env:PATH"
$env:CGO_ENABLED = "1"

# Clear any existing CGO flags to avoid conflicts (especially wasmer paths)
Remove-Item Env:\CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_LDFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CPPFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CXXFLAGS -ErrorAction SilentlyContinue

# Check if liboqs is available (check both user-provided locations first)
$liboqsFound = $false

# Check C:\liboqs first (check for actual header file)
if (Test-Path "C:\liboqs\include\oqs\oqs.h") {
    Write-Host "Found liboqs installation at C:\liboqs\" -ForegroundColor Green
    $env:CGO_CFLAGS = "-IC:\liboqs\include"
    if (Test-Path "C:\liboqs\lib\oqs.lib") {
        $env:CGO_LDFLAGS = "-LC:\liboqs\lib -loqs"
    } elseif (Test-Path "C:\liboqs\lib\liboqs.a") {
        $env:CGO_LDFLAGS = "-LC:\liboqs\lib -loqs"
    } elseif (Test-Path "C:\liboqs\lib") {
        $env:CGO_LDFLAGS = "-LC:\liboqs\lib -loqs"
    } else {
        $env:CGO_LDFLAGS = "-loqs"
    }
    $liboqsFound = $true
}

# Check D:\Projects\QSDM\liboqs_install (freshly installed location)
if (-not $liboqsFound -and (Test-Path "D:\Projects\QSDM\liboqs_install\include\oqs\oqs.h")) {
    Write-Host "Found liboqs installation at D:\Projects\QSDM\liboqs_install\" -ForegroundColor Green
    $env:CGO_CFLAGS = "-ID:\Projects\QSDM\liboqs_install\include"
    if (Test-Path "D:\Projects\QSDM\liboqs_install\lib\oqs.lib") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs_install\lib -loqs"
    } elseif (Test-Path "D:\Projects\QSDM\liboqs_install\lib\liboqs.a") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs_install\lib -loqs"
    } elseif (Test-Path "D:\Projects\QSDM\liboqs_install\lib") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs_install\lib -loqs"
    } else {
        $env:CGO_LDFLAGS = "-loqs"
    }
    $liboqsFound = $true
}

# Check D:\Projects\QSDM\liboqs_build (user-provided location)
if (-not $liboqsFound -and (Test-Path "D:\Projects\QSDM\liboqs_build\include\oqs\oqs.h")) {
    Write-Host "Found liboqs installation at D:\Projects\QSDM\liboqs_build\" -ForegroundColor Green
    $env:CGO_CFLAGS = "-ID:\Projects\QSDM\liboqs_build\include"
    if (Test-Path "D:\Projects\QSDM\liboqs_build\lib\oqs.lib") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs_build\lib -loqs"
    } elseif (Test-Path "D:\Projects\QSDM\liboqs_build\lib\liboqs.a") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs_build\lib -loqs"
    } elseif (Test-Path "D:\Projects\QSDM\liboqs_build\lib") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs_build\lib -loqs"
    } else {
        $env:CGO_LDFLAGS = "-loqs"
    }
    $liboqsFound = $true
}

# Check D:\Projects\QSDM\liboqs if not found
if (-not $liboqsFound -and (Test-Path "D:\Projects\QSDM\liboqs\include\oqs\oqs.h")) {
    Write-Host "Found liboqs installation at D:\Projects\QSDM\liboqs\" -ForegroundColor Green
    $env:CGO_CFLAGS = "-ID:\Projects\QSDM\liboqs\include"
    if (Test-Path "D:\Projects\QSDM\liboqs\lib\oqs.lib") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs\lib -loqs"
    } elseif (Test-Path "D:\Projects\QSDM\liboqs\lib\liboqs.a") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs\lib -loqs"
    } elseif (Test-Path "D:\Projects\QSDM\liboqs\lib") {
        $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs\lib -loqs"
    } else {
        $env:CGO_LDFLAGS = "-loqs"
    }
    $liboqsFound = $true
}

if (-not $liboqsFound) {
    Write-Host "WARNING: liboqs not found at C:\liboqs\" -ForegroundColor Yellow
    Write-Host "The build may fail. Install liboqs or adjust paths below." -ForegroundColor Yellow
    Write-Host ""
    Write-Host "You can set custom paths:" -ForegroundColor Cyan
    Write-Host '  $env:CGO_CFLAGS = "-I<path-to-liboqs>/include"' -ForegroundColor Gray
    Write-Host '  $env:CGO_LDFLAGS = "-L<path-to-liboqs>/lib -loqs"' -ForegroundColor Gray
    Write-Host ""
    Write-Host "Attempting to search for liboqs in common locations..." -ForegroundColor Cyan
    # Try to find liboqs in other common locations
    $possiblePaths = @(
        "D:\Projects\QSDM\liboqs_install",  # Freshly installed location
        "D:\Projects\QSDM\liboqs_build",  # User-provided location
        "C:\liboqs\liboqs_install",
        "C:\liboqs\liboqs_build",
        "C:\liboqs\build",
        "C:\liboqs",
        "D:\Projects\QSDM\liboqs\liboqs_install",
        "D:\Projects\QSDM\liboqs\liboqs_build",
        "D:\Projects\QSDM\liboqs\build",
        "D:\Projects\QSDM\liboqs",
        "$env:USERPROFILE\liboqs",
        "C:\Program Files\liboqs",
        "C:\liboqs_install",
        "C:\liboqs-go-main"
    )
    foreach ($path in $possiblePaths) {
        if (Test-Path "$path\include\oqs\oqs.h") {
            Write-Host "Found liboqs at: $path" -ForegroundColor Green
            $env:CGO_CFLAGS = "-I$path\include"
            # Check for lib directory
            if (Test-Path "$path\lib") {
                $env:CGO_LDFLAGS = "-L$path\lib -loqs"
                $liboqsFound = $true
            } elseif (Test-Path "$path\build\lib") {
                $env:CGO_LDFLAGS = "-L$path\build\lib -loqs"
                $liboqsFound = $true
            } else {
                Write-Host "WARNING: liboqs library files not found at $path, trying default" -ForegroundColor Yellow
                $env:CGO_LDFLAGS = "-loqs"
                $liboqsFound = $true  # Still mark as found, let linker try to find it
            }
            if ($liboqsFound) {
                break
            }
        }
    }
}

# Check for OpenSSL (required by liboqs)
$opensslFound = $false
$opensslPaths = @(
    "C:\msys64\mingw64",
    "C:\OpenSSL-Win64",
    "C:\OpenSSL",
    "C:\Program Files\OpenSSL",
    "C:\Program Files (x86)\OpenSSL"
)
foreach ($path in $opensslPaths) {
    $cryptoStatic = Test-Path "$path\lib\libcrypto.a"
    $cryptoDll = Test-Path "$path\lib\libcrypto.dll.a"
    if ($cryptoStatic -or $cryptoDll) {
        Write-Host "Found OpenSSL at: $path" -ForegroundColor Green
        if ($env:CGO_LDFLAGS) {
            $env:CGO_LDFLAGS += " -L$path\lib -lcrypto"
        } else {
            $env:CGO_LDFLAGS = "-L$path\lib -lcrypto"
        }
        $opensslFound = $true
        break
    }
}
if (-not $opensslFound) {
    Write-Host "WARNING: OpenSSL not found. liboqs requires OpenSSL's libcrypto." -ForegroundColor Yellow
    Write-Host "  liboqs was built with OpenSSL support and needs -lcrypto at link time." -ForegroundColor Yellow
    Write-Host "  Install OpenSSL or rebuild liboqs without OpenSSL support." -ForegroundColor Yellow
}

# Check for CUDA (optional) - can cause warnings but is safe to ignore
$cudaInclude = "C:\CUDA\include"
$cudaFound = $false
if (Test-Path $cudaInclude) {
    Write-Host "Found CUDA installation" -ForegroundColor Green
    Write-Host "Note: CUDA may show warnings (__cdecl redefined). These are safe to ignore." -ForegroundColor Gray
    if ($env:CGO_CFLAGS) {
        $env:CGO_CFLAGS += " -IC:\CUDA\include"
    } else {
        $env:CGO_CFLAGS = "-IC:\CUDA\include"
    }
    if ($env:CGO_LDFLAGS) {
        $env:CGO_LDFLAGS += " -LC:\CUDA\lib\x64 -lcudart"
    } else {
        $env:CGO_LDFLAGS = "-LC:\CUDA\lib\x64 -lcudart"
    }
    $cudaFound = $true
} else {
    Write-Host "CUDA not found (optional, 3D mesh acceleration will be unavailable)" -ForegroundColor Yellow
}

Write-Host ""
Write-Host "CGO Environment:" -ForegroundColor Cyan
Write-Host "  CGO_ENABLED: $env:CGO_ENABLED"
Write-Host "  CGO_CFLAGS: $env:CGO_CFLAGS"
Write-Host "  CGO_LDFLAGS: $env:CGO_LDFLAGS"
Write-Host ""

# Verify Go version
Write-Host "Go version:" -ForegroundColor Cyan
go version
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Go is not working properly" -ForegroundColor Red
    exit 1
}

Write-Host ""
Write-Host "Building..." -ForegroundColor Cyan

# Try to kill any Go processes that might be locking the compiler
Write-Host "Checking for running Go processes..." -ForegroundColor Gray
$goProcesses = Get-Process | Where-Object {$_.ProcessName -like "*go*" -or $_.Path -like "*Go*"}
if ($goProcesses) {
    Write-Host "Found running Go processes, attempting to close them..." -ForegroundColor Yellow
    $goProcesses | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
}

# Check if liboqs is configured before building
if (-not $liboqsFound -or -not $env:CGO_CFLAGS) {
    Write-Host ""
    Write-Host "ERROR: liboqs not found. Cannot build with CGO." -ForegroundColor Red
    Write-Host ""
    Write-Host "Please install liboqs:" -ForegroundColor Yellow
    Write-Host "  1. Clone: git clone https://github.com/open-quantum-safe/liboqs.git" -ForegroundColor Gray
    Write-Host "  2. Build: cd liboqs && mkdir build && cd build" -ForegroundColor Gray
    Write-Host "     cmake -DCMAKE_INSTALL_PREFIX=C:/liboqs .." -ForegroundColor Gray
    Write-Host "     cmake --build . --config Release" -ForegroundColor Gray
    Write-Host "     cmake --install . --config Release" -ForegroundColor Gray
    Write-Host ""
    Write-Host "Or use the non-CGO build: .\build_no_cgo.ps1" -ForegroundColor Cyan
    exit 1
}

# Attempt to build with retry logic for access denied errors
$buildAttempts = 0
$maxAttempts = 3
$buildSuccess = $false
$buildOutput = ""

while ($buildAttempts -lt $maxAttempts -and -not $buildSuccess) {
    $buildAttempts++
    if ($buildAttempts -gt 1) {
        Write-Host "Build attempt $buildAttempts of $maxAttempts..." -ForegroundColor Yellow
        # Additional cleanup between retries
        Get-Process | Where-Object {$_.ProcessName -like "*go*"} | Stop-Process -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 2
    }
    
    # Capture both stdout and stderr, suppress PowerShell exceptions during retries
    try {
        $buildOutput = go build -o qsdmplus.exe ./cmd/qsdmplus 2>&1 | Out-String
        $buildExitCode = $LASTEXITCODE
    } catch {
        $buildOutput = $_.Exception.Message
        $buildExitCode = 1
    }
    
    if ($buildExitCode -eq 0 -and (Test-Path "qsdmplus.exe")) {
        $buildSuccess = $true
    } elseif ($buildOutput -match "Access is denied" -or $buildOutput -match "fork/exec.*Access is denied" -or $_.Exception.Message -match "Access is denied") {
        Write-Host ""
        Write-Host "ERROR: Access denied when trying to use Go compiler" -ForegroundColor Red
        Write-Host ""
        Write-Host "This is usually caused by:" -ForegroundColor Yellow
        Write-Host "  1. Antivirus blocking C:\Program Files\Go" -ForegroundColor Cyan
        Write-Host "  2. File permissions issue" -ForegroundColor Cyan
        Write-Host "  3. Another process locking the compiler" -ForegroundColor Cyan
        Write-Host ""
        
        if ($buildAttempts -lt $maxAttempts) {
            Write-Host "Attempting to resolve and retry (attempt $buildAttempts/$maxAttempts)..." -ForegroundColor Yellow
            # More aggressive cleanup
            Get-Process | Where-Object {$_.ProcessName -like "*go*" -or $_.Path -like "*Go*"} | Stop-Process -Force -ErrorAction SilentlyContinue
            Start-Sleep -Seconds 3
            # Suppress the error message for retries
            $ErrorActionPreference = 'SilentlyContinue'
            continue
        } else {
            Write-Host "Solutions:" -ForegroundColor Yellow
            Write-Host "  1. Add C:\Program Files\Go to antivirus exclusions (Windows Defender)" -ForegroundColor Cyan
            Write-Host "  2. Run PowerShell as Administrator" -ForegroundColor Cyan
            Write-Host "  3. Try building without CUDA: .\build_with_cgo_no_cuda.ps1" -ForegroundColor Cyan
            Write-Host "  4. See fix_access_denied.md for detailed instructions" -ForegroundColor Cyan
            Write-Host ""
            exit 1
        }
    } else {
        # Other build error, show it and exit
        Write-Host ""
        Write-Host "Build failed with error:" -ForegroundColor Red
        if ($buildOutput) {
            Write-Host $buildOutput -ForegroundColor Red
        }
        Write-Host ""
        Write-Host "Common issues:" -ForegroundColor Yellow
        Write-Host "  1. liboqs not installed or not in PATH" -ForegroundColor Cyan
        Write-Host "  2. C compiler (gcc/MSVC) not available" -ForegroundColor Cyan
        Write-Host "  3. Missing CGO_CFLAGS or CGO_LDFLAGS" -ForegroundColor Cyan
        Write-Host "  4. CUDA warnings (try build_with_cgo_no_cuda.ps1)" -ForegroundColor Cyan
        Write-Host ""
        Write-Host "See INSTALL_OQS.md for installation instructions" -ForegroundColor Yellow
        exit $buildExitCode
    }
}

if ($buildSuccess) {
    Write-Host ""
    Write-Host "✓ Build successful! Executable: qsdmplus.exe" -ForegroundColor Green
    
    # Verify the executable exists and show its size
    if (Test-Path "qsdmplus.exe") {
        $exeInfo = Get-Item "qsdmplus.exe"
        Write-Host "  File size: $([math]::Round($exeInfo.Length / 1MB, 2)) MB" -ForegroundColor Gray
        Write-Host "  Created: $($exeInfo.CreationTime)" -ForegroundColor Gray
    }
    
    Write-Host ""
    Write-Host "All features enabled:" -ForegroundColor Green
    Write-Host "  ✓ Quantum-safe consensus (Proof-of-Entanglement)" -ForegroundColor Green
    Write-Host "  ✓ WASM modules (wallet/validator)" -ForegroundColor Green
    Write-Host "  ✓ SQLite storage" -ForegroundColor Green
    Write-Host "  ✓ Full cryptographic verification" -ForegroundColor Green
    if ($cudaFound) {
        Write-Host "  ✓ CUDA acceleration (3D mesh)" -ForegroundColor Green
    }
    Write-Host ""
    Write-Host "You can now run: .\qsdmplus.exe" -ForegroundColor Cyan
}

