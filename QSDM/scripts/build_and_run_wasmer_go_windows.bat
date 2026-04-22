@echo off
REM Build the native Wasmer Go bindings on Windows and run the WASM integration test

REM Step 1: Set environment variables for Go and CGO
set GO111MODULE=on
set CGO_ENABLED=1

REM Step 2: Build the Wasmer Go patched library
cd wasmer-go-patched
go build -v ./...

if errorlevel 1 (
  echo Failed to build wasmer-go-patched native bindings.
  exit /b 1
)

cd ..

REM Step 3: Run Go mod tidy to update dependencies
go mod tidy

REM Step 4: Run the WASM integration test
go test -v tests/wasm_integration_test.go

pause
