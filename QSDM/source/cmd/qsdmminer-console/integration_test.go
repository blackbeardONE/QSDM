package main

// Integration tests for the HTTP pipeline between the console miner
// and a validator. These exercise fetch-work → decode → WorkToMiningCore
// → DAG build → Solve → submit-proof → decode-response end-to-end,
// against an in-process httptest.Server that mimics the validator's
// /api/v1/mining/{work,submit} endpoints.
//
// Why a separate file from main_test.go:
//  - main_test.go covers the pure helpers (Config, formatters, event
//    state machine) in isolation. Those tests never open a socket.
//  - integration_test.go covers the wire path — HTTP transport, JSON
//    encoding, response-shape parsing — which unit tests by design
//    cannot observe. Without this, a regression like "submitProof
//    swallows the HTTP status code" would pass every unit test but
//    break miners silently in production.
//  - The protocol --self-test is in-memory-only. It proves the miner
//    can round-trip against pkg/mining but not against anything that
//    looks like a network peer.
//
// Test budget: solve+verify at difficulty=2 with N=128 finishes in
// well under one second on CI hardware (matches the budget the
// existing --self-test uses). No `testing.Short` gating needed; the
// whole file adds <2s to `go test ./cmd/qsdmminer-console/...`.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
)

// buildFixtureWork produces a validator-shaped api.MiningWork payload
// that the miner will accept, solve, and submit against. Difficulty
// stays at 2 and DAGSize at 128 so the full cycle completes in <1s.
// Returns the wire payload alongside the in-memory WorkSet so tests
// that also need to verify the submitted proof have everything they
// need without re-parsing.
func buildFixtureWork(t *testing.T, epoch, height uint64) api.MiningWork {
	t.Helper()
	ws := syntheticWorkSet(4)
	hdr := [32]byte{0x5E, 0x1F, 0x7E, 0x57}

	cells := make([]api.MiningWorkBatch, len(ws.Batches))
	for i, b := range ws.Batches {
		raw := make([]api.MiningWorkCell, len(b.Cells))
		for j, c := range b.Cells {
			raw[j] = api.MiningWorkCell{
				ID:          hex.EncodeToString(c.ID),
				ContentHash: hex.EncodeToString(c.ContentHash[:]),
			}
		}
		cells[i] = api.MiningWorkBatch{Cells: raw}
	}
	root := ws.Root()
	return api.MiningWork{
		Epoch:             epoch,
		Height:            height,
		HeaderHash:        hex.EncodeToString(hdr[:]),
		Difficulty:        "2",
		DAGSize:           128,
		WorkSetRoot:       hex.EncodeToString(root[:]),
		WorkSet:           cells,
		BatchCountMaximum: 4,
		BlocksPerEpoch:    100,
	}
}

// TestIntegration_FetchWork_RoundTrip asserts fetchWork correctly
// decodes a validator-shaped JSON response. A regression here —
// e.g. a renamed JSON tag on MiningWork — would silently make every
// miner unable to pick up new work.
func TestIntegration_FetchWork_RoundTrip(t *testing.T) {
	want := buildFixtureWork(t, 1, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/work", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "GET only", http.StatusMethodNotAllowed)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			// The miner MUST send this header per pkg/api contract;
			// asserting it catches a regression where the Accept
			// header is dropped on refactor.
			http.Error(w, "Accept header missing", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := fetchWork(ctx, &http.Client{Timeout: 2 * time.Second}, srv.URL)
	if err != nil {
		t.Fatalf("fetchWork: %v", err)
	}
	if got.Epoch != want.Epoch || got.Height != want.Height {
		t.Errorf("epoch/height mismatch: got %d/%d want %d/%d",
			got.Epoch, got.Height, want.Epoch, want.Height)
	}
	if got.DAGSize != want.DAGSize {
		t.Errorf("DAGSize: got %d want %d", got.DAGSize, want.DAGSize)
	}
	if got.Difficulty != want.Difficulty {
		t.Errorf("Difficulty: got %q want %q", got.Difficulty, want.Difficulty)
	}
	if got.BatchCountMaximum != want.BatchCountMaximum {
		t.Errorf("BatchCountMaximum: got %d want %d", got.BatchCountMaximum, want.BatchCountMaximum)
	}
	if got.HeaderHash != want.HeaderHash {
		t.Errorf("HeaderHash: got %q want %q", got.HeaderHash, want.HeaderHash)
	}
}

// TestIntegration_FetchWork_HTTPErrorSurfacesStatus verifies that a
// non-200 response surfaces the status code to the caller. Without
// this, a validator returning 503 would look to the miner like a
// transient JSON decode failure and might generate misleading
// "decode work" error events.
func TestIntegration_FetchWork_HTTPErrorSurfacesStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/work", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"no work yet"}`, http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := fetchWork(ctx, &http.Client{Timeout: 2 * time.Second}, srv.URL)
	if err == nil {
		t.Fatal("expected error from 503, got nil")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should include status 503, got: %v", err)
	}
}

// TestIntegration_SubmitProof_AcceptedParsesProofID proves the happy
// path of submitProof: a 200 with Accepted=true deserialises into a
// MiningSubmitResponse whose ProofID the caller can use. A
// regression here would make every accepted proof invisible to the
// console renderer's "accepted" counter.
func TestIntegration_SubmitProof_AcceptedParsesProofID(t *testing.T) {
	var received atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/submit", func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			http.Error(w, "wrong content-type: "+ct, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{
			Accepted: true,
			ProofID:  "deadbeef",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := submitProof(ctx, &http.Client{Timeout: 2 * time.Second}, srv.URL, []byte(`{"proof":"dummy"}`))
	if err != nil {
		t.Fatalf("submitProof: %v", err)
	}
	if !resp.Accepted {
		t.Error("expected Accepted=true")
	}
	if resp.ProofID != "deadbeef" {
		t.Errorf("ProofID: got %q want deadbeef", resp.ProofID)
	}
	if n := received.Load(); n != 1 {
		t.Errorf("server received %d requests, expected 1", n)
	}
}

// TestIntegration_SubmitProof_BadRequestStillDecodesRejection verifies
// that a 400 with a shaped MiningSubmitResponse body (Accepted=false,
// RejectReason set) is NOT treated as a transport error by the miner.
// The validator legitimately uses 400 to return a rejection reason;
// fetching the detail means miners can log "why" a proof was rejected
// instead of retrying a malformed request forever.
func TestIntegration_SubmitProof_BadRequestStillDecodesRejection(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/submit", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{
			Accepted:     false,
			RejectReason: "target_not_met",
			Detail:       "proof digest above target",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := submitProof(ctx, &http.Client{Timeout: 2 * time.Second}, srv.URL, []byte(`{}`))
	if err != nil {
		t.Fatalf("400 with a MiningSubmitResponse body must not error: %v", err)
	}
	if resp.Accepted {
		t.Error("expected Accepted=false on a rejection response")
	}
	if resp.RejectReason != "target_not_met" {
		t.Errorf("RejectReason: got %q want target_not_met", resp.RejectReason)
	}
	if !strings.Contains(resp.Detail, "above target") {
		t.Errorf("Detail should carry server explanation: %q", resp.Detail)
	}
}

// TestIntegration_RunLoop_EndToEnd drives the full mining loop against
// an in-process fixture validator. It waits for the first EvProofAccepted
// event (which requires fetchWork → DAG build → Solve → submitProof all
// to succeed), then cancels the loop. This is the strongest regression
// gate we have: if any link in that chain breaks, the event never fires
// and the test times out.
func TestIntegration_RunLoop_EndToEnd(t *testing.T) {
	work := buildFixtureWork(t, 1, 1)

	var submits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/work", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(work)
	})
	mux.HandleFunc("/api/v1/mining/submit", func(w http.ResponseWriter, r *http.Request) {
		// Accept exactly the first submit, reject subsequent ones
		// with a well-formed rejection. Without this second branch
		// a fast solver might race against the ctx cancel and
		// submit twice; we want the test to never wedge on a 2nd
		// request that is never answered.
		n := submits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if n == 1 {
			_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{
				Accepted: true, ProofID: "abc123",
			})
		} else {
			_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{
				Accepted: false, RejectReason: "duplicate_proof",
			})
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := Config{
		ValidatorURL: srv.URL,
		RewardAddr:   "qsdm1integration",
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
		runLoop(ctx, &http.Client{Timeout: 5 * time.Second}, cfg, events, &attempts)
	}()

	deadline := time.After(30 * time.Second)
	sawConnected := false
	sawEpoch := false
	sawDAG := false
	for {
		select {
		case ev := <-events:
			switch ev.Kind {
			case EvConnected:
				sawConnected = true
			case EvEpochChanged:
				sawEpoch = true
				if ev.Epoch != work.Epoch {
					t.Errorf("EvEpochChanged epoch: got %d want %d", ev.Epoch, work.Epoch)
				}
				if ev.DAGSize != work.DAGSize {
					t.Errorf("EvEpochChanged DAGSize: got %d want %d", ev.DAGSize, work.DAGSize)
				}
			case EvDAGReady:
				sawDAG = true
			case EvProofAccepted:
				cancel()
				<-done

				if ev.ProofID != "abc123" {
					t.Errorf("ProofID: got %q want abc123", ev.ProofID)
				}
				if ev.Height != work.Height {
					t.Errorf("Event height: got %d want %d", ev.Height, work.Height)
				}
				if atomic.LoadUint64(&attempts) == 0 {
					t.Error("expected attempts counter > 0 by the time a proof was accepted")
				}
				if !sawConnected {
					t.Error("expected EvConnected before EvProofAccepted")
				}
				if !sawEpoch {
					t.Error("expected EvEpochChanged before EvProofAccepted")
				}
				if !sawDAG {
					t.Error("expected EvDAGReady before EvProofAccepted")
				}
				if n := submits.Load(); n == 0 {
					t.Errorf("expected at least one submit, got %d", n)
				}
				return
			case EvError:
				// Transient errors are legal (e.g. Solve ctx race
				// near cancel). Log and keep waiting — the overall
				// deadline will catch a stuck loop.
				t.Logf("transient loop error (continuing): %s", ev.Message)
			}
		case <-deadline:
			cancel()
			<-done
			t.Fatal("timed out waiting for EvProofAccepted; loop may be stuck")
		}
	}
}
