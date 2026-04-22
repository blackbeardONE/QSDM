# Find and copy wasmtime DLLs from Go module cache

Write-Host "=== Finding wasmtime DLLs ===" -ForegroundColor Cyan
Write-Host ""

# Get Go module cache path
$gopath = go env GOPATH
if (-not $gopath) {
    $gopath = "$env:USERPROFILE\go"
}

Write-Host "GOPATH: $gopath" -ForegroundColor Gray
Write-Host ""

# Check common locations
$searchPaths = @(
    "$gopath\pkg\mod\github.com\bytecodealliance\wasmtime-go@v1.0.0\build",
    "$env:USERPROFILE\.go\pkg\mod\github.com\bytecodealliance\wasmtime-go@v1.0.0\build",
    "C:\go\pkg\mod\github.com\bytecodealliance\wasmtime-go@v1.0.0\build"
)

$found = $false
foreach ($path in $searchPaths) {
    if (Test-Path $path) {
        Write-Host "Checking: $path" -ForegroundColor Yellow
        $dlls = Get-ChildItem -Path $path -Recurse -Filter "*.dll" -ErrorAction SilentlyContinue
        if ($dlls) {
            Write-Host "  Found DLLs:" -ForegroundColor Green
            foreach ($dll in $dlls) {
                Write-Host "    $($dll.Name) -> $($dll.FullName)" -ForegroundColor Gray
                Copy-Item $dll.FullName -Destination "." -Force
                Write-Host "      ✓ Copied to project directory" -ForegroundColor Green
                $found = $true
            }
        }
    }
}

if (-not $found) {
    Write-Host "✗ No wasmtime DLLs found in Go module cache" -ForegroundColor Red
    Write-Host ""
    Write-Host "Solution: The application will crash because wasmtime DLLs are missing." -ForegroundColor Yellow
    Write-Host "You can either:" -ForegroundColor Yellow
    Write-Host "  1. Download wasmtime DLLs manually" -ForegroundColor Gray
    Write-Host "  2. Build without CGO (build_no_cgo.ps1)" -ForegroundColor Gray
    Write-Host "  3. Use Dependency Walker to identify the exact missing DLL" -ForegroundColor Gray
} else {
    Write-Host ""
    Write-Host "✓ All wasmtime DLLs copied successfully!" -ForegroundColor Green
    Write-Host "Now try running: .\run_qsdmplus.ps1" -ForegroundColor Cyan
}

