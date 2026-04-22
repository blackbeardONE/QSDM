# PowerShell script to build Wasmer Go patched native bindings and run WASM integration tests on Windows

# Step 1: Set environment variables
$env:GO111MODULE = "on"
$env:CGO_ENABLED = "1"

# Step 2: Build native Wasmer Rust library
Write-Host "Building native Wasmer Rust library..."
Push-Location wasmer-go-patched
cargo build --release
if ($LASTEXITCODE -ne 0) {
    Write-Error "Failed to build native Wasmer Rust library."
    Exit 1
}
Pop-Location

# Step 3: Build Wasmer Go bindings
Write-Host "Building Wasmer Go bindings..."
Push-Location wasmer-go-patched
go build ./...
if ($LASTEXITCODE -ne 0) {
    Write-Error "Failed to build Wasmer Go bindings."
    Exit 1
}
Pop-Location

# Step 4: Update Go modules
Write-Host "Running go mod tidy..."
go mod tidy

# Step 5: Run WASM integration tests
Write-Host "Running WASM integration tests..."
go test -v tests/wasm_integration_test.go

if ($LASTEXITCODE -ne 0) {
    Write-Error "WASM integration tests failed."
    Exit 1
}

Write-Host "WASM integration tests completed successfully."
