package wasmer

// +build windows

// This file provides a stub implementation to replace open_memstream usage on Windows,
// which is not available, to fix build errors in wasmer-go on Windows.

// #include <stdlib.h>
// #include <stdio.h>
//
// // Stub for open_memstream on Windows.
// FILE* open_memstream(char **bufp, size_t *sizep) {
//     return NULL; // Return NULL to indicate unsupported
// }
import "C"
