@echo off
REM Set environment variables for CGO to use GCC compiler and flags
set CGO_ENABLED=1
set CC=gcc
set CXX=g++
set CGO_CFLAGS=-Iwasmer-go-patched/packaged/include -IC:/liboqs/liboqs_install/include -Wall -Wno-unused-function -Wno-macro-redefined
set CGO_LDFLAGS=-Lwasmer-go-patched/target/release -LC:/liboqs/liboqs_install/lib -lwasmer_go -loqs

echo CGO_ENABLED=%CGO_ENABLED%
echo CC=%CC%
echo CXX=%CXX%
echo CGO_CFLAGS=%CGO_CFLAGS%
echo CGO_LDFLAGS=%CGO_LDFLAGS%

REM Run Go build
go build -o qsmd.exe -v -x ./cmd/qsmd

pause
