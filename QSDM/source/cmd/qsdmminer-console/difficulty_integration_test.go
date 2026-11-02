package main

// HL2 WP-E: the console miner adapts to the difficulty the validator
// advertises in /work. The fixture validator raises the difficulty after
// the first proof; every submitted proof must meet the target of the work
// it was solved for, and must not merely meet the easier target.

import (
	"context"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/v2client"
)

func TestIntegration_RunLoop_AdaptsToAdvertisedDifficulty(t *testing.T) {
	// 2^10 then 2^13 attempts per proof keep the CPU solve well under a
	// second at DAG size 128; production uses 2^difficulty_bits >= 2^16.
	diffs := []int64{1 << 10, 1 << 13}
	var (
		mu       sync.Mutex
		served   int // index into diffs of the work served last
		accepted []int64
		weak     int
	)
	work := buildFixtureWork(t, 1, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/mining/work", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		wk := work
		wk.Difficulty = big.NewInt(diffs[served]).String()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(wk)
	})
	mux.HandleFunc("/api/v1/mining/submit", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		p, err := mining.ParseProof(body)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{RejectReason: "non-canonical", Detail: err.Error()})
			return
		}
		mu.Lock()
		defer mu.Unlock()
		d := diffs[served]
		tgt, _ := mining.TargetFromDifficulty(big.NewInt(d))
		if !mining.MeetsTarget(mining.ProofPoWHash(p.HeaderHash, p.Nonce, p.BatchRoot, p.MixDigest), tgt) {
			weak++
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{RejectReason: "work", Detail: "hash does not meet target"})
			return
		}
		accepted = append(accepted, d)
		if served < len(diffs)-1 {
			served++ // raise the difficulty for the next /work
		}
		_ = json.NewEncoder(w).Encode(api.MiningSubmitResponse{Accepted: true, ProofID: "ok"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := Config{ValidatorURL: srv.URL, RewardAddr: "qsdm1difficulty", BatchCount: 1, PollInterval: "50ms"}
	events := make(chan Event, 256)
	var attempts uint64
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		client := &http.Client{Timeout: 5 * time.Second}
		fetcher, _ := v2client.NewMultiFetcher(client, []string{cfg.ValidatorURL})
		runLoop(ctx, client, fetcher, cfg, &V2Context{}, nil, nil, events, &attempts)
	}()

	want := 4 // one proof at 2^10, then three at 2^13
	n := 0
	for n < want {
		select {
		case ev := <-events:
			if ev.Kind == EvProofAccepted {
				n++
			}
		case <-ctx.Done():
			<-done
			t.Fatalf("timed out after %d accepted proofs", n)
		}
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if weak != 0 {
		t.Fatalf("%d proofs did not meet the advertised target", weak)
	}
	if len(accepted) < want || accepted[0] != diffs[0] {
		t.Fatalf("accepted at difficulties %v", accepted)
	}
	for _, d := range accepted[1:] {
		if d != diffs[1] {
			t.Fatalf("accepted at difficulties %v, want %d after the raise", accepted, diffs[1])
		}
	}
}
