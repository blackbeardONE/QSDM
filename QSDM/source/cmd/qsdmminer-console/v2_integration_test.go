package main

// v2_integration_test.go drives the full v2 NVIDIA-locked
// mining loop against an in-process fixture validator. It is
// the v2 sibling of TestIntegration_RunLoop_EndToEnd in
// integration_test.go: same fixture work, same submit pattern,
// but with /api/v1/mining/challenge wired in and the submitted
// proof asserted to carry Version=v2 + a populated
// nvidia-hmac-v1 Attestation.
//
// Why this is the strongest miner-side regression gate:
//
//   - It is the only test that exercises the *complete* flow
//     fetch-work → DAG build → Solve → fetch-challenge → build
//     HMAC bundle → attach → submit. Every link in the chain
//     has a unit test, but only this test fails when the seams
//     between them break (e.g. someone renames a JSON tag on
//     the challenge wire format and unit tests still pass
//     because each side is using its own struct literal).
//
//   - It catches the v1↔v2 mode switch: if the v2-prepare path
//     is silently disabled (e.g. an Enabled-flag regression on
//     V2Context), the assertion that the submitted proof has
//     Version=ProtocolVersionV2 fails and the test is loud
//     about it.
//
// Test budget: same as the v1 integration test — difficulty=2,
// DAGSize=128 → solve+verify in well under 1s on CI hardware.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
)

// fixtureV2Server returns an httptest.Server that serves the
// three endpoints a v2-mode console miner needs against
// realistic wire formats:
//
//   - GET /api/v1/mining/work       → buildFixtureWork()
//   - GET /api/v1/mining/challenge  → real challenge.Issuer
//   - POST /api/v1/mining/submit    → captures body in submitted
//     channel, returns Accepted=true exactly once
//
// The captured submit body lets the test inspect the on-the-
// wire proof rather than relying on internal V2Context state.
func fixtureV2Server(t *testing.T, work api.MiningWork) (*httptest.Server, <-chan []byte) {
	t.Helper()

	sigKey := make([]byte, 32)
	_, _ = rand.Read(sigKey)
	signer, err := challenge.NewHMACSigner("validator-v2", sigKey)
	if err != nil {
		t.Fatalf("NewHMACSigner: %v", err)
	}
	issuer, err := challenge.NewIssuer(signer)
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}

	submitted := make(chan []byte, 4)
	var submits atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/work", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(work)
	})
	mux.HandleFunc("/api/v1/mining/challenge", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		c, err := issuer.Mint()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"nonce":     hex.EncodeToString(c.Nonce[:]),
			"issued_at": c.IssuedAt,
			"signer_id": c.SignerID,
			"signature": hex.EncodeToString(c.Signature),
		})
	})
	mux.HandleFunc("/api/v1/mining/submit", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		select {
		case submitted <- bytes.Clone(body):
		default:
		}
		n := submits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{
				Accepted: true, ProofID: "v2abc",
			})
		} else {
			// Subsequent submits get a clean rejection so the
			// loop doesn't wedge after the test has captured
			// the first body.
			_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{
				Accepted: false, RejectReason: "duplicate_proof",
			})
		}
	})

	return httptest.NewServer(mux), submitted
}

// TestIntegration_RunLoop_v2_EndToEnd asserts that a runLoop
// configured with an enabled V2Context fetches a challenge,
// attaches a v2 attestation, and submits a Version=v2 proof
// the validator accepts. Failure here means a real testnet
// operator's v2 mining setup is broken.
func TestIntegration_RunLoop_v2_EndToEnd(t *testing.T) {
	work := buildFixtureWork(t, 1, 1)
	srv, submitted := fixtureV2Server(t, work)
	defer srv.Close()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "hmac.key")
	if _, err := GenerateHMACKeyFile(keyPath); err != nil {
		t.Fatalf("GenerateHMACKeyFile: %v", err)
	}
	v2ctx, err := LoadV2Context(V2Config{
		Protocol:    "v2",
		NodeID:      "console-v2-test",
		GPUUUID:     "GPU-deadbeef-1234-5678-9abc-def012345678",
		GPUName:     "NVIDIA GeForce RTX 4090",
		GPUArch:     "ada",
		HMACKeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("LoadV2Context: %v", err)
	}
	if !v2ctx.IsEnabled() {
		t.Fatal("v2ctx must be enabled for this test")
	}

	cfg := Config{
		ValidatorURL: srv.URL,
		RewardAddr:   "qsdm1v2integration",
		BatchCount:   1,
		PollInterval: "50ms",
	}
	events := make(chan Event, 128)
	var attempts uint64
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runLoop(ctx, &http.Client{Timeout: 5 * time.Second}, cfg, v2ctx, events, &attempts)
	}()

	deadline := time.After(30 * time.Second)
	sawV2 := false
	for {
		select {
		case ev := <-events:
			switch ev.Kind {
			case EvV2ChallengeOK:
				sawV2 = true
				if ev.IssuedAt == 0 {
					t.Error("EvV2ChallengeOK should carry the validator-issued IssuedAt")
				}
			case EvProofAccepted:
				cancel()
				<-done

				if !sawV2 {
					t.Error("EvV2ChallengeOK must precede EvProofAccepted in v2 mode")
				}

				// Inspect the submitted proof off the wire to
				// prove v2 framing landed end-to-end. Reading
				// from V2Context wouldn't catch a regression
				// where the attestation is built but stripped
				// before submit.
				select {
				case raw := <-submitted:
					// The wire encoding is canonical JSON
					// (uint64 fields are JSON strings); decode
					// via the spec-defined ParseProof rather
					// than json.Unmarshal to avoid accidentally
					// asserting a different schema than the
					// validator uses.
					pp, err := mining.ParseProof(raw)
					if err != nil {
						t.Fatalf("decode submitted proof: %v", err)
					}
					p := *pp
					if p.Version != mining.ProtocolVersionV2 {
						t.Errorf("submitted proof.Version: got %d want %d (v2)",
							p.Version, mining.ProtocolVersionV2)
					}
					if p.Attestation.Type == "" {
						t.Error("submitted proof must carry a non-empty Attestation.Type")
					}
					if p.Attestation.BundleBase64 == "" {
						t.Error("submitted proof must carry a non-empty Attestation.BundleBase64")
					}
					if p.Attestation.GPUArch != "ada" {
						t.Errorf("submitted Attestation.GPUArch: got %q want ada",
							p.Attestation.GPUArch)
					}
					var zero [32]byte
					if p.Attestation.Nonce == zero {
						t.Error("submitted Attestation.Nonce must be set to challenge nonce")
					}
				case <-time.After(2 * time.Second):
					t.Fatal("no submit body captured within 2s of EvProofAccepted")
				}
				return
			case EvError:
				// As in the v1 sibling test, transient errors
				// are legal — Solve can race ctx.Done near the
				// cancel. The deadline catches a stuck loop.
				t.Logf("transient loop error (continuing): %s", ev.Message)
			}
		case <-deadline:
			cancel()
			<-done
			t.Fatal("timed out waiting for v2 EvProofAccepted; loop may be stuck")
		}
	}
}

// TestIntegration_RunLoop_v2_ChallengeOutageStaysAtV1Empty
// proves that when the validator's challenge endpoint is
// 503-ing, the v2-mode miner refuses to submit anything (i.e.
// does NOT fall back to v1) and surfaces the error as an
// EvError. A silent v1 fallback would be the worst kind of
// bug — proofs would post that the forked validator throws
// out, wasting solve cycles + leaving the operator with no
// log signal that anything is wrong.
func TestIntegration_RunLoop_v2_ChallengeOutageStaysAtV1Empty(t *testing.T) {
	work := buildFixtureWork(t, 1, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/work", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(work)
	})
	mux.HandleFunc("/api/v1/mining/challenge", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"no issuer"}`, http.StatusServiceUnavailable)
	})
	var submits atomic.Int32
	mux.HandleFunc("/api/v1/mining/submit", func(w http.ResponseWriter, r *http.Request) {
		submits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{
			Accepted: false, RejectReason: "should_not_be_called",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "hmac.key")
	if _, err := GenerateHMACKeyFile(keyPath); err != nil {
		t.Fatalf("GenerateHMACKeyFile: %v", err)
	}
	v2ctx, err := LoadV2Context(V2Config{
		Protocol:    "v2",
		NodeID:      "outage-test",
		GPUUUID:     "GPU-feedface-0000-0000-0000-000000000000",
		HMACKeyPath: keyPath,
	})
	if err != nil {
		t.Fatalf("LoadV2Context: %v", err)
	}

	cfg := Config{
		ValidatorURL: srv.URL,
		RewardAddr:   "qsdm1outage",
		BatchCount:   1,
		PollInterval: "50ms",
	}
	events := make(chan Event, 128)
	var attempts uint64
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		runLoop(ctx, &http.Client{Timeout: 2 * time.Second}, cfg, v2ctx, events, &attempts)
	}()

	gotV2PrepareErr := false
	deadline := time.After(8 * time.Second)
collect:
	for {
		select {
		case ev := <-events:
			if ev.Kind == EvError && containsAny(ev.Message, "v2 prepare", "fetch challenge") {
				gotV2PrepareErr = true
				cancel()
				break collect
			}
			if ev.Kind == EvProofAccepted {
				t.Fatal("v2 mode must NOT submit when challenge endpoint is 503")
			}
		case <-deadline:
			cancel()
			break collect
		}
	}
	<-done

	if !gotV2PrepareErr {
		t.Error("expected at least one EvError mentioning v2 prepare / fetch challenge")
	}
	if n := submits.Load(); n != 0 {
		t.Errorf("v2 mode must not submit on challenge outage; got %d submits", n)
	}
}

// containsAny reports whether s contains any of substrs.
// Local helper to avoid importing strings just for two calls.
func containsAny(s string, substrs ...string) bool {
	for _, ss := range substrs {
		if ss != "" && len(s) >= len(ss) {
			for i := 0; i+len(ss) <= len(s); i++ {
				if s[i:i+len(ss)] == ss {
					return true
				}
			}
		}
	}
	return false
}
