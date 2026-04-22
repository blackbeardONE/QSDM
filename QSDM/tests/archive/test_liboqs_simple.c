// Simple test to verify liboqs can initialize
// Compile with: gcc -o test_liboqs_simple.exe test_liboqs_simple.c -L. -L../liboqs_install/lib -loqs -I../liboqs_install/include

#include <oqs/oqs.h>
#include <stdio.h>
#include <stdlib.h>

#ifdef _WIN32
#include <windows.h>
#endif

int main() {
    printf("Testing liboqs initialization...\n\n");
    
#ifdef _WIN32
    // Preload OpenSSL DLLs
    printf("1. Loading OpenSSL DLLs...\n");
    HMODULE hCrypto = LoadLibraryA(".\\libcrypto-3-x64.dll");
    HMODULE hSsl = LoadLibraryA(".\\libssl-3-x64.dll");
    
    if (hCrypto && hSsl) {
        printf("   ✓ OpenSSL DLLs loaded\n");
        
        // Initialize OpenSSL
        typedef int (*OPENSSL_init_crypto_func)(unsigned long, void*);
        OPENSSL_init_crypto_func openssl_init_crypto = (OPENSSL_init_crypto_func)GetProcAddress(hCrypto, "OPENSSL_init_crypto");
        if (openssl_init_crypto) {
            openssl_init_crypto(0x00000002UL | 0x00000004UL | 0x00000008UL | 0x00000040UL, NULL);
            printf("   ✓ OpenSSL initialized\n");
        }
    } else {
        printf("   ✗ Failed to load OpenSSL DLLs\n");
        return 1;
    }
#endif
    
    printf("\n2. Testing OQS_SIG_new...\n");
    // NOTE: Dilithium was removed in liboqs 0.15.0+, replaced by ML-DSA (FIPS 204)
    // Using ML-DSA-87 (256-bit security, equivalent to Dilithium5)
    // Highest security level - recommended for maximum security requirements
    OQS_SIG *sig = OQS_SIG_new("ML-DSA-87");
    
    if (sig == NULL) {
        printf("   ✗ OQS_SIG_new returned NULL\n");
        printf("\n   This indicates liboqs cannot initialize.\n");
        printf("   Possible causes:\n");
        printf("     - OpenSSL symbols not resolved\n");
        printf("     - liboqs internal error\n");
        printf("     - Algorithm not available\n");
        return 1;
    }
    
    printf("   ✓ OQS_SIG_new succeeded!\n");
    printf("   Public key length: %zu\n", sig->length_public_key);
    printf("   Secret key length: %zu\n", sig->length_secret_key);
    printf("   Signature length: %zu\n", sig->length_signature);
    
    OQS_SIG_free(sig);
    
    printf("\n✓ liboqs is working correctly!\n");
    return 0;
}

