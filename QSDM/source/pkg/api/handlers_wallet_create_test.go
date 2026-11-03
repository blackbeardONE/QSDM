package api

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

// keyGenerationSpy stands in for crypto/rand.Reader and counts the reads made
// on behalf of crypto.NewDilithium, the constructor every node-side ML-DSA-87
// key pair comes from (wallet.NewWalletService calls it). It observes the
// pure-Go backend, the one CI builds (CGO_ENABLED=0 or the dilithium_circl
// tag); liboqs draws its randomness in C.
type keyGenerationSpy struct {
	io.Reader
	keyGenerations atomic.Int64
}

func (s *keyGenerationSpy) Read(p []byte) (int, error) {
	stack := make([]byte, 64<<10)
	stack = stack[:runtime.Stack(stack, false)]
	if bytes.Contains(stack, []byte("pkg/crypto.NewDilithium")) {
		s.keyGenerations.Add(1)
	}
	return s.Reader.Read(p)
}

// /api/v1/wallet/create generated a key pair on the node and returned only its
// address; the private key was discarded, so CELL sent there could never be
// spent. The route now answers 410 Gone the way /api/v1/wallet/mint does, to
// GET as well as POST, through the middleware chain the node serves, and it
// generates no key at all.
func TestWalletCreate_410GoneWithoutKeyGeneration(t *testing.T) {
	srv, err := NewServer(&config.Config{JWTHMACSecret: strings.Repeat("s", 32)}, logging.NewSilentLogger(), nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Stop() })
	handler := RequestSizeLimitMiddleware(1 << 20)(srv.SetupTestHandler())

	// Installed after NewServer, whose auth manager generates its own key.
	spy := &keyGenerationSpy{Reader: rand.Reader}
	rand.Reader = spy
	t.Cleanup(func() { rand.Reader = spy.Reader })

	for _, method := range []string{http.MethodPost, http.MethodGet} {
		t.Run(method, func(t *testing.T) {
			before := spy.keyGenerations.Load()
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/wallet/create", nil))
			if generated := spy.keyGenerations.Load() - before; generated != 0 {
				t.Errorf("%s generated a key pair (%d crypto/rand reads by crypto.NewDilithium)", method, generated)
			}
			if rec.Code != http.StatusGone {
				t.Fatalf("%s: status %d, want 410 Gone (body %s)", method, rec.Code, rec.Body.String())
			}

			var resp map[string]interface{}
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode the 410 body: %v (%s)", err, rec.Body.String())
			}
			if status, _ := resp["status"].(string); status != "gone" {
				t.Errorf(`status = %q; want "gone"`, status)
			}
			if reason, _ := resp["reason"].(string); reason == "" {
				t.Error("the 410 body has no reason")
			}
			if _, ok := resp["address"]; ok {
				t.Errorf("the 410 body still hands out an address: %s", rec.Body.String())
			}
			migration, ok := resp["migration"].(map[string]interface{})
			if !ok {
				t.Fatalf("the 410 body has no migration block: %s", rec.Body.String())
			}
			text, _ := json.Marshal(migration)
			for _, want := range []string{"QSDM Hive", "qsdmcli wallet new", "/api/v1/wallet/submit-signed"} {
				if !strings.Contains(string(text), want) {
					t.Errorf("the migration block does not point to %s: %s", want, text)
				}
			}
		})
	}
}
