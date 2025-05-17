@echo off
REM Clean previous build artifacts
if exist qsdm.exe del qsdm.exe
if exist build rd /s /q build

REM Setup MSVC environment for amd64
call "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\VC\Auxiliary\Build\vcvarsall.bat" x64

REM Set environment variables for CGO
set CGO_ENABLED=1
set CC=cl.exe
set CXX=cl.exe
set CGO_CFLAGS=/W4 /WX
set CGO_LDFLAGS=-Lwasmer-go-patched/target/release -lwasmer_go

REM Build the Go project
go build -o qsdm.exe ./cmd/qsdm

pause
