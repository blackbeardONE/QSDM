package crypto

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	"github.com/cloudflare/circl/sign/mldsa/mldsa87"
)

// zstdRLEBomb returns one zstd frame with a window of 2^(10+windowExp) bytes
// (windowExp at most 7) and `blocks` RLE blocks of that size. Each block
// regenerates its whole size from a single byte, so four bytes of input become
// up to 128 KiB of output. The frame header declares no content size, so only
// decoding reveals how large the output is.
func zstdRLEBomb(windowExp uint8, blocks int) []byte {
	blockSize := uint32(1) << (10 + windowExp)
	frame := []byte{
		0x28, 0xb5, 0x2f, 0xfd, // magic number
		0x00,           // frame header descriptor: no content size, window descriptor follows, no checksum
		windowExp << 3, // window descriptor: exponent windowExp, mantissa 0
	}
	for i := 0; i < blocks; i++ {
		header := blockSize<<3 | 1<<1 // block type 1: RLE
		if i == blocks-1 {
			header |= 1 // last block
		}
		frame = append(frame, byte(header), byte(header>>8), byte(header>>16), 0x00)
	}
	return frame
}

// A few kilobytes of input that expand to megabytes are refused at once,
// without the decoder allocating the output: a frame whose 128 KiB window
// already exceeds the cap, and one with a small window whose blocks only add
// up past it.
func TestDecompressSignature_RefusesADecompressionBomb(t *testing.T) {
	for _, tc := range []struct {
		name      string
		windowExp uint8
		blocks    int
	}{
		{"128 KiB blocks, 64 MiB in all", 7, 512},
		{"4 KiB blocks, 8 MiB in all", 2, 2048},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bomb := zstdRLEBomb(tc.windowExp, tc.blocks)
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			out, err := DecompressSignature(bomb)
			elapsed := time.Since(start)
			runtime.ReadMemStats(&after)
			if err == nil {
				t.Fatalf("%d bytes of input decompressed to %d bytes without an error", len(bomb), len(out))
			}
			if out != nil {
				t.Errorf("returned %d bytes along with the error", len(out))
			}
			allocated := after.TotalAlloc - before.TotalAlloc
			if allocated > 4<<20 {
				t.Errorf("refusing a %d-byte input allocated %d bytes", len(bomb), allocated)
			}
			t.Logf("refused %d bytes of input in %v, allocating %d bytes: %v", len(bomb), elapsed, allocated, err)
		})
	}
}

// The cap is the ML-DSA-87 signature size: exactly that much decompresses, one
// byte more does not, and neither does a second frame that crosses it.
func TestDecompressSignature_CapIsTheMLDSA87SignatureSize(t *testing.T) {
	compress := func(size int) ([]byte, []byte) {
		t.Helper()
		payload := bytes.Repeat([]byte{0xa5}, size)
		compressed, err := CompressSignature(payload)
		if err != nil {
			t.Fatalf("CompressSignature(%d bytes): %v", size, err)
		}
		return payload, compressed
	}

	payload, compressed := compress(mldsa87.SignatureSize)
	if out, err := DecompressSignature(compressed); err != nil || !bytes.Equal(out, payload) {
		t.Errorf("%d bytes: got %d bytes and error %v, want the payload back", len(payload), len(out), err)
	}

	_, compressed = compress(mldsa87.SignatureSize + 1)
	if out, err := DecompressSignature(compressed); err == nil {
		t.Errorf("decompressed %d bytes, past the ML-DSA-87 signature size, without an error", len(out))
	}

	_, first := compress(mldsa87.SignatureSize - 100)
	_, second := compress(200)
	if out, err := DecompressSignature(append(first, second...)); err == nil {
		t.Errorf("two frames decompressed to %d bytes, past the ML-DSA-87 signature size, without an error", len(out))
	}
}

// A real ML-DSA-87 signature survives the round trip and still verifies.
func TestDecompressSignature_RoundTripsARealSignature(t *testing.T) {
	var seed [mldsa87.SeedSize]byte
	for i := range seed {
		seed[i] = byte(i)
	}
	public, private := mldsa87.NewKeyFromSeed(&seed)
	message := []byte("QSDM compressed signature round trip")
	sig := make([]byte, mldsa87.SignatureSize)
	if err := mldsa87.SignTo(private, message, nil, false, sig); err != nil {
		t.Fatalf("SignTo: %v", err)
	}

	compressed, err := CompressSignature(sig)
	if err != nil {
		t.Fatalf("CompressSignature: %v", err)
	}
	out, err := DecompressSignature(compressed)
	if err != nil {
		t.Fatalf("DecompressSignature: %v", err)
	}
	if !bytes.Equal(out, sig) {
		t.Fatalf("the signature changed in the round trip (%d bytes in, %d out)", len(sig), len(out))
	}
	if !mldsa87.Verify(public, message, nil, out) {
		t.Fatal("the round-tripped signature does not verify")
	}
}
