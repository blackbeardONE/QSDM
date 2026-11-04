package crypto

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
	"github.com/klauspost/compress/zstd"
)

// CompressSignature compresses an ML-DSA signature using zstd compression.
// This reduces signature size by approximately 50% (4.6 KB → 2.3 KB for ML-DSA-87).
// Returns the compressed signature or an error if compression fails.
func CompressSignature(sig []byte) ([]byte, error) {
	if len(sig) == 0 {
		return nil, nil
	}
	
	var b bytes.Buffer
	// Use best compression level for maximum size reduction
	encoder, err := zstd.NewWriter(&b, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	if err != nil {
		return nil, err
	}
	
	_, err = encoder.Write(sig)
	if err != nil {
		encoder.Close()
		return nil, err
	}
	
	err = encoder.Close()
	if err != nil {
		return nil, err
	}
	
	return b.Bytes(), nil
}

// maxDecompressedSignatureSize caps what DecompressSignature returns: the size
// of an ML-DSA-87 signature (FIPS 204), the largest signature this package
// makes or verifies. zstd regenerates up to 128 KiB from four bytes of input,
// so without a cap a small compressed "signature" expands to gigabytes before
// anything checks its length.
const maxDecompressedSignatureSize = mldsa87.SignatureSize

// DecompressSignature decompresses a zstd-compressed ML-DSA signature.
// Returns the original signature or an error if decompression fails. Input
// that would expand past the ML-DSA-87 signature size is an error, and
// decoding stops there instead of allocating the rest.
func DecompressSignature(compressed []byte) ([]byte, error) {
	if len(compressed) == 0 {
		return nil, nil
	}

	// DecodeAll treats WithDecoderMaxMemory as a limit on the decoded size: it
	// refuses a frame that declares a larger window or content size up front,
	// and otherwise stops after the block that crosses the limit.
	decoder, err := zstd.NewReader(nil,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(maxDecompressedSignatureSize))
	if err != nil {
		return nil, err
	}
	defer decoder.Close()

	sig, err := decoder.DecodeAll(compressed, nil)
	if errors.Is(err, zstd.ErrDecoderSizeExceeded) || errors.Is(err, zstd.ErrWindowSizeExceeded) {
		return nil, fmt.Errorf("crypto: compressed signature would decompress past %d bytes, the ML-DSA-87 signature size: %w",
			maxDecompressedSignatureSize, err)
	}
	if err != nil {
		return nil, err
	}
	return sig, nil
}

