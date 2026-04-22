# PowerShell script to build QSDM with CGO enabled by default
# This is the main build script for development

Write-Host "Building QSDM with CGO enabled (development build)..." -ForegroundColor Cyan
Write-Host "This build includes:" -ForegroundColor Green
Write-Host "  - Quantum-safe cryptography (liboqs)"
Write-Host "  - SQLite storage"
Write-Host "  - CUDA acceleration (if available)"
Write-Host ""

# Set Go environment
$env:GOROOT = "C:\Program Files\Go"
$env:PATH = "C:\Program Files\Go\bin;$env:PATH"
$env:CGO_ENABLED = "1"

# Clear any existing CGO flags to avoid conflicts
Remove-Item Env:\CGO_CFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_LDFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CPPFLAGS -ErrorAction SilentlyContinue
Remove-Item Env:\CGO_CXXFLAGS -ErrorAction SilentlyContinue

# Check if liboqs is available
$liboqsFound = $false
$liboqsPath = $null

# Check common locations
$possiblePaths = @(
    "D:\Projects\QSDM\liboqs_install",
    "D:\Projects\QSDM\liboqs_build",
    "D:\Projects\QSDM\liboqs",
    "C:\liboqs"
)

foreach ($path in $possiblePaths) {
        if (Test-Path "$path\include\oqs\oqs.h") {
        Write-Host "Found liboqs installation at $path\" -ForegroundColor Green
        $env:CGO_CFLAGS = "-I$path\include"
        # Check for DLL import library first (preferred for shared OpenSSL)
        $usingDll = $false
        if (Test-Path "$path\lib\liboqs.dll.a") {
            $env:CGO_LDFLAGS = "-L$path\lib -loqs"
            Write-Host "  Using liboqs DLL (liboqs.dll.a)" -ForegroundColor Gray
            $usingDll = $true
        } elseif (Test-Path "$path\lib\oqs.lib") {
            $env:CGO_LDFLAGS = "-L$path\lib -loqs"
            Write-Host "  Using liboqs DLL (oqs.lib)" -ForegroundColor Gray
            $usingDll = $true
        } elseif (Test-Path "$path\lib\liboqs.a") {
            $env:CGO_LDFLAGS = "-L$path\lib -loqs"
            Write-Host "  Using liboqs static library (liboqs.a)" -ForegroundColor Gray
        } elseif (Test-Path "$path\lib") {
            $env:CGO_LDFLAGS = "-L$path\lib -loqs"
        } else {
            $env:CGO_LDFLAGS = "-loqs"
        }
        $liboqsFound = $true
        $liboqsPath = $path
        $script:usingLiboqsDll = $usingDll
        break
    }
}

if (-not $liboqsFound) {
    Write-Host "WARNING: liboqs not found!" -ForegroundColor Yellow
    Write-Host "Attempting to install liboqs automatically..." -ForegroundColor Cyan
    Write-Host ""
    
    # Try to run the installation script
    if (Test-Path ".\download_and_setup_liboqs.ps1") {
        Write-Host "Running download_and_setup_liboqs.ps1..." -ForegroundColor Cyan
        & ".\download_and_setup_liboqs.ps1"
        
        # Check again after installation
        if (Test-Path "D:\Projects\QSDM\liboqs_install\include\oqs\oqs.h") {
            Write-Host "liboqs installed successfully!" -ForegroundColor Green
            $env:CGO_CFLAGS = "-ID:\Projects\QSDM\liboqs_install\include"
            $env:CGO_LDFLAGS = "-LD:\Projects\QSDM\liboqs_install\lib -loqs"
            $liboqsFound = $true
            $liboqsPath = "D:\Projects\QSDM\liboqs_install"
        }
    }
    
    if (-not $liboqsFound) {
        Write-Host ""
        Write-Host "ERROR: liboqs is required but not found." -ForegroundColor Red
        Write-Host "Please install liboqs manually or run:" -ForegroundColor Yellow
        Write-Host "  .\download_and_setup_liboqs.ps1" -ForegroundColor Cyan
        Write-Host ""
        Write-Host "The build will continue but quantum-safe features will be unavailable." -ForegroundColor Yellow
        Write-Host ""
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
        # Note: When using liboqs.dll, we should NOT link against OpenSSL directly
        # because liboqs.dll handles OpenSSL internally. Linking against -lcrypto
        # can cause symbol conflicts and prevent proper DLL symbol resolution.
        # Only add OpenSSL to linker flags if we're using static liboqs
        if ($script:usingLiboqsDll) {
            Write-Host "  Using liboqs.dll - skipping OpenSSL link (handled by DLL)" -ForegroundColor Gray
            Write-Host "  OpenSSL DLLs still needed at runtime for liboqs.dll" -ForegroundColor Gray
        } else {
            # Only link OpenSSL if using static liboqs library
            if ($env:CGO_LDFLAGS) {
                $env:CGO_LDFLAGS += " -L$path\lib -lcrypto"
            } else {
                $env:CGO_LDFLAGS = "-L$path\lib -lcrypto"
            }
        }
        $opensslFound = $true
        break
    }
}

if (-not $opensslFound) {
    Write-Host "WARNING: OpenSSL not found. liboqs requires OpenSSL's libcrypto." -ForegroundColor Yellow
    Write-Host "  Install OpenSSL or the build may fail." -ForegroundColor Yellow
}

# Check for CUDA (optional)
$cudaInclude = "C:\CUDA\include"
$cudaFound = $false
if (Test-Path $cudaInclude) {
    Write-Host "Found CUDA installation" -ForegroundColor Green
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
Get-Process | Where-Object {$_.ProcessName -like "*go*"} | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

# Build with CGO enabled
# Use -ldflags to increase stack size (fixes stack overflow during CGO init)
# Increased to 32MB to handle OpenSSL DLL initialization
Write-Host "Compiling with CGO..." -ForegroundColor Cyan
go build -mod=mod -ldflags "-extldflags=-Wl,--stack,33554432" -o qsdmplus.exe ./cmd/qsdmplus

# After build, copy required DLLs to executable directory if needed
Write-Host ""
Write-Host "Setting up runtime DLLs..." -ForegroundColor Cyan

if ($liboqsFound) {
    # Check liboqs build configuration
    $liboqsBuildDir = "D:\Projects\QSDM\liboqs\build"
    $cmakeCache = "$liboqsBuildDir\CMakeCache.txt"
    if (Test-Path $cmakeCache) {
        $cacheContent = Get-Content $cmakeCache -ErrorAction SilentlyContinue
        $opensslShared = $cacheContent | Select-String "OQS_USE_OPENSSL_SHARED" | Select-Object -First 1
        # Check if OpenSSL shared linking is enabled (format: OQS_USE_OPENSSL_SHARED:UNINITIALIZED=ON or OQS_USE_OPENSSL_SHARED:BOOL=ON)
        # Note: If OQS_USE_OPENSSL_SHARED=OFF, OpenSSL is statically linked into the DLL, which is actually preferred on Windows
        if ($opensslShared -and $opensslShared -match "=OFF") {
            Write-Host "  ℹ️  liboqs built with OpenSSL statically linked (embedded in DLL)" -ForegroundColor Cyan
            Write-Host "     This is the recommended configuration for Windows" -ForegroundColor Gray
            Write-Host ""
        } elseif ($opensslShared -and $opensslShared -notmatch "=ON") {
            Write-Host "  ⚠️  WARNING: liboqs was NOT built with OpenSSL shared linking!" -ForegroundColor Yellow
            Write-Host "     This may cause OQS_SIG_new to return nil at runtime" -ForegroundColor Yellow
            Write-Host "     Recommended: Run .\rebuild_liboqs.ps1 before building" -ForegroundColor Cyan
            Write-Host ""
        }
    }
    
    # Search for liboqs.dll in the installation directory (recursive search)
    $liboqsDll = $null
    $searchPaths = @(
        "$liboqsPath\bin",
        "$liboqsPath\lib",
        "$liboqsPath"
    )
    
    Write-Host "  Searching for liboqs.dll..." -ForegroundColor Cyan
    foreach ($searchPath in $searchPaths) {
        if (Test-Path $searchPath) {
            $foundDll = Get-ChildItem -Path $searchPath -Filter "liboqs.dll" -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($foundDll) {
                $liboqsDll = $foundDll.FullName
                Write-Host "    Found: $liboqsDll" -ForegroundColor Gray
                break
            }
        }
    }
    
    # Also check if liboqs was built as static library
    $staticLib = $null
    foreach ($searchPath in $searchPaths) {
        if (Test-Path $searchPath) {
            $foundLib = Get-ChildItem -Path $searchPath -Filter "liboqs.a" -Recurse -ErrorAction SilentlyContinue | Select-Object -First 1
            if ($foundLib) {
                $staticLib = $foundLib.FullName
                break
            }
        }
    }
    
    if ($liboqsDll -and -not (Test-Path ".\liboqs.dll")) {
        Write-Host "  Copying liboqs.dll..." -ForegroundColor Cyan
        try {
            Copy-Item $liboqsDll -Destination ".\liboqs.dll" -Force -ErrorAction Stop
            Write-Host "    ✅ liboqs.dll copied" -ForegroundColor Green
        } catch {
            Write-Host "    ⚠️  Failed to copy liboqs.dll: $_" -ForegroundColor Yellow
            Write-Host "    Note: You may need to add liboqs directory to PATH" -ForegroundColor Yellow
        }
    } elseif (Test-Path ".\liboqs.dll") {
        Write-Host "  ✅ liboqs.dll already exists" -ForegroundColor Green
    } elseif ($staticLib) {
        Write-Host "  ℹ️  liboqs built as static library (liboqs.a found)" -ForegroundColor Cyan
        Write-Host "    No DLL needed - statically linked" -ForegroundColor Gray
        Write-Host "    Note: Still requires OpenSSL DLLs at runtime" -ForegroundColor Gray
        Write-Host "    ⚠️  Static linking with OpenSSL shared may cause initialization issues" -ForegroundColor Yellow
        Write-Host "    Consider rebuilding liboqs as DLL: .\rebuild_liboqs.ps1" -ForegroundColor Cyan
    } else {
        Write-Host "  ⚠️  liboqs.dll not found" -ForegroundColor Yellow
        Write-Host "    This may cause runtime errors if liboqs is dynamically linked" -ForegroundColor Yellow
        Write-Host "    Check build output or try: Get-ChildItem -Path '$liboqsPath' -Recurse -Filter '*.dll'" -ForegroundColor Gray
        Write-Host "    If using static library, this is expected" -ForegroundColor Gray
    }
}

# Copy OpenSSL DLLs if needed
if ($opensslFound) {
    $opensslBin = "C:\msys64\mingw64\bin"
    $cryptoDll = "$opensslBin\libcrypto-3-x64.dll"
    $sslDll = "$opensslBin\libssl-3-x64.dll"
    
    if (Test-Path $cryptoDll) {
        if (-not (Test-Path ".\libcrypto-3-x64.dll")) {
            Write-Host "  Copying OpenSSL DLLs..." -ForegroundColor Cyan
            try {
                Copy-Item $cryptoDll -Destination "." -Force -ErrorAction Stop
                if (Test-Path $sslDll) {
                    Copy-Item $sslDll -Destination "." -Force -ErrorAction Stop
                }
                Write-Host "    ✅ OpenSSL DLLs copied" -ForegroundColor Green
            } catch {
                Write-Host "    ⚠️  Failed to copy OpenSSL DLLs: $_" -ForegroundColor Yellow
                Write-Host "    Note: Ensure C:\msys64\mingw64\bin is in PATH" -ForegroundColor Yellow
            }
        } else {
            Write-Host "  ✅ OpenSSL DLLs already exist" -ForegroundColor Green
        }
    }
}

# Copy CUDA DLLs if needed (cudart64_*.dll)
if ($cudaFound) {
    $cudaBin = "C:\CUDA\bin"
    if (Test-Path $cudaBin) {
        $cudaDll = Get-ChildItem -Path $cudaBin -Filter "cudart64_*.dll" -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($cudaDll) {
            $cudaDllName = $cudaDll.Name
            if (-not (Test-Path ".\$cudaDllName")) {
                Write-Host "  Copying CUDA runtime DLL..." -ForegroundColor Cyan
                try {
                    Copy-Item $cudaDll.FullName -Destination ".\$cudaDllName" -Force -ErrorAction Stop
                    Write-Host "    ✅ CUDA DLL copied: $cudaDllName" -ForegroundColor Green
                } catch {
                    Write-Host "    ⚠️  Failed to copy CUDA DLL: $_" -ForegroundColor Yellow
                    Write-Host "    Note: Ensure C:\CUDA\bin is in PATH" -ForegroundColor Yellow
                }
            } else {
                Write-Host "  ✅ CUDA DLL already exists: $cudaDllName" -ForegroundColor Green
            }
        } else {
            Write-Host "  ⚠️  CUDA DLL not found in $cudaBin" -ForegroundColor Yellow
        }
    }
}

if ($LASTEXITCODE -eq 0) {
    Write-Host ""
    Write-Host "Build successful! Executable: qsdmplus.exe" -ForegroundColor Green
    Write-Host ""
    Write-Host "Features enabled:" -ForegroundColor Green
    if ($liboqsFound) {
        Write-Host "  ✅ Quantum-safe consensus (Proof-of-Entanglement)"
        Write-Host "  ✅ API server (quantum-safe authentication)"
    } else {
        Write-Host "  ⚠️  Quantum-safe consensus (liboqs not found - will be degraded)"
        Write-Host "  ⚠️  API server (may not start without liboqs)"
    }
    Write-Host "  ✅ SQLite storage"
    Write-Host "  ✅ Full cryptographic verification"
    if ($cudaFound) {
        Write-Host "  ✅ CUDA acceleration (3D mesh)"
    } else {
        Write-Host "  ⚠️  3D mesh (CPU-based, CUDA not available)"
    }
    Write-Host ""
    Write-Host "You can now run: .\qsdmplus.exe" -ForegroundColor Cyan
} else {
    Write-Host ""
    Write-Host "Build failed." -ForegroundColor Red
    Write-Host ""
    if (-not $liboqsFound) {
        Write-Host "If the error is related to liboqs:" -ForegroundColor Yellow
        Write-Host "  1. Run: .\download_and_setup_liboqs.ps1" -ForegroundColor Cyan
        Write-Host "  2. Or install liboqs manually" -ForegroundColor Cyan
        Write-Host ""
    }
    Write-Host "If you see 'Access is denied':" -ForegroundColor Yellow
    Write-Host "  1. Add C:\Program Files\Go to antivirus exclusions" -ForegroundColor Cyan
    Write-Host "  2. Run PowerShell as Administrator" -ForegroundColor Cyan
    Write-Host "  3. Close any running Go processes" -ForegroundColor Cyan
    Write-Host ""
    exit $LASTEXITCODE
}
