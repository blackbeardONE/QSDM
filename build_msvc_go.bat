@echo off
REM Setup MSVC environment for amd64
call "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\VC\Auxiliary\Build\vcvarsall.bat" amd64

REM Set environment variables for CGO to use MSVC compiler and flags
set CGO_ENABLED=1
set CC=cl.exe
set CXX=cl.exe
set CGO_CFLAGS=/W4 /Zi /Iwasmer-go-patched\packaged\include /IC:\liboqs\liboqs_install\include -Xclang -fms-compatibility -W0
set CGO_CPPFLAGS=
set CGO_CXXFLAGS=
set CGO_LDFLAGS=/DEBUG /LIBPATH:wasmer-go-patched\target\release /LIBPATH:C:\liboqs\liboqs_install\lib wasmer_go.lib oqs.lib

REM Run Go build
go build -o qsmd.exe -v -x ./cmd/qsmd

pause
