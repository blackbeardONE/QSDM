// +build ignore

package main

/*
#include <oqs/oqs.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#include <windows.h>

static int test_openssl_available() {
    HMODULE hCrypto = LoadLibraryA(".\\libcrypto-3-x64.dll");
    if (!hCrypto) {
        hCrypto = LoadLibraryA("C:\\msys64\\mingw64\\bin\\libcrypto-3-x64.dll");
    }
    if (!hCrypto) {
        return 0;
    }
    
    typedef const char* (*OpenSSL_version_func)(int);
    OpenSSL_version_func openssl_version = (OpenSSL_version_func)GetProcAddress(hCrypto, "OpenSSL_version");
    if (openssl_version) {
        const char* version = openssl_version(0);
        if (version && strlen(version) > 0) {
            return 1;
        }
    }
    return 0;
}
#endif
*/
import "C"
import (
	"fmt"
	"unsafe"
)

func main() {
	fmt.Println("Testing liboqs initialization...")
	fmt.Println("")
	
	// Test OpenSSL availability
	fmt.Println("1. Testing OpenSSL DLL availability...")
	opensslOk := C.test_openssl_available()
	if opensslOk == 1 {
		fmt.Println("   ✅ OpenSSL DLL is available and functional")
	} else {
		fmt.Println("   ❌ OpenSSL DLL is NOT available or not functional")
		fmt.Println("      This will cause OQS_SIG_new to fail")
		return
	}
	
	fmt.Println("")
	fmt.Println("2. Testing OQS_SIG_new...")
	cname := C.CString("Dilithium2")
	defer C.free(unsafe.Pointer(cname))
	
	sig := C.OQS_SIG_new(cname)
	if sig == nil {
		fmt.Println("   ❌ OQS_SIG_new returned nil")
		fmt.Println("")
		fmt.Println("   This indicates liboqs cannot initialize even though OpenSSL is available.")
		fmt.Println("   Possible causes:")
		fmt.Println("     - liboqs was built against a different OpenSSL version")
		fmt.Println("     - liboqs static library doesn't match the OpenSSL DLL")
		fmt.Println("     - Symbol resolution issue between static liboqs and dynamic OpenSSL")
		fmt.Println("")
		fmt.Println("   Solution: Rebuild liboqs as a DLL instead of static library")
	} else {
		fmt.Println("   ✅ OQS_SIG_new succeeded!")
		fmt.Println("   ✅ liboqs is working correctly")
		C.OQS_SIG_free(sig)
	}
}

