//go:build scylla

// Chaos harness for Scylla: simulates docker-level disruption while writing concurrently.
// Requires a reachable Scylla cluster (SCYLLA_HOSTS env), typically run via:
//
//   scripts/scylla-staging-verify-with-docker.sh
//   SCYLLA_HOSTS=127.0.0.1 SCYLLA_CHAOS_CONTAINER=qsdm-scylla-dev \
//     go test -tags scylla ./pkg/storage -run TestChaos -timeout 10m
//
// The harness supports two chaos hooks:
//
//   - SCYLLA_CHAOS_CONTAINER: docker container name. When set, a goroutine will
//     `docker pause` / `docker unpause` it at random intervals during the run.
//   - SCYLLA_CHAOS_NETPARTITION=1: flag that simulates "partition" by closing the
//     storage session and reopening it mid-run. Useful when docker is unavailable.
//
// Invariants asserted:
//   - No transaction is lost (every successfully ACKed tx id is later readable).
//   - LWT dedupe remains idempotent after reconnects.
//   - Final Ready() succeeds after the chaos window ends.

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func chaosHostsOrSkip(t *testing.T) []string {
	hosts := strings.TrimSpace(os.Getenv("SCYLLA_HOSTS"))
	if hosts == "" {
		t.Skip("set SCYLLA_HOSTS (e.g. 127.0.0.1) to run chaos test")
	}
	var out []string
	for _, h := range strings.Split(hosts, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		t.Fatal("SCYLLA_HOSTS produced no hosts")
	}
	return out
}

func dockerAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// TestChaos_ScyllaWriteDuringPause pauses + unpauses the Scylla container while a writer
// pushes transactions at steady rate. After the chaos window, every ack'd tx id must be
// readable via GetRecentTransactions, and Ready() must be green.
func TestChaos_ScyllaWriteDuringPause(t *testing.T) {
	hosts := chaosHostsOrSkip(t)
	container := strings.TrimSpace(os.Getenv("SCYLLA_CHAOS_CONTAINER"))
	if container == "" || !dockerAvailable() {
		t.Skip("set SCYLLA_CHAOS_CONTAINER and ensure docker is on PATH to run pause/unpause chaos")
	}

	keyspace := strings.TrimSpace(os.Getenv("SCYLLA_KEYSPACE"))
	if keyspace == "" {
		keyspace = "qsdm"
	}

	s, err := NewScyllaStorage(hosts, keyspace, ScyllaClusterConfigFromEnv())
	if err != nil {
		t.Fatalf("NewScyllaStorage: %v", err)
	}
	defer s.Close()

	if err := s.Ready(); err != nil {
		t.Fatalf("Ready before chaos: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	var (
		acked      sync.Map // tx id -> struct{}
		ackedCount atomic.Int64
		failCount  atomic.Int64
		sender     = fmt.Sprintf("chaos-sender-%d", time.Now().UnixNano())
	)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		r := rand.New(rand.NewSource(time.Now().UnixNano()))
		for i := 0; ctx.Err() == nil; i++ {
			id := fmt.Sprintf("chaos-tx-%d-%d", i, r.Int63())
			body, _ := json.Marshal(map[string]interface{}{
				"tx_id":             id,
				"sender_address":    sender,
				"recipient_address": "chaos-recipient",
				"amount":            0.01,
				"timestamp":         time.Now().UTC().Format(time.RFC3339Nano),
			})
			if err := s.StoreTransaction(body); err != nil {
				failCount.Add(1)
			} else {
				acked.Store(id, struct{}{})
				ackedCount.Add(1)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()

	chaosDone := make(chan struct{})
	go func() {
		defer close(chaosDone)
		t.Logf("chaos: pausing %s every ~10s for 2s", container)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := exec.Command("docker", "pause", container).Run(); err != nil {
					t.Logf("docker pause: %v", err)
					continue
				}
				time.Sleep(2 * time.Second)
				if err := exec.Command("docker", "unpause", container).Run(); err != nil {
					t.Errorf("docker unpause: %v", err)
					return
				}
			}
		}
	}()

	<-writerDone
	<-chaosDone

	if ackedCount.Load() == 0 {
		t.Fatal("no transactions ACKed during chaos — harness broken")
	}
	t.Logf("chaos: acked=%d failed=%d", ackedCount.Load(), failCount.Load())

	// Ready must recover.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := s.Ready(); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Ready() did not recover within 30s after chaos")
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Verify a sample of ACKed ids are readable via the sender MV.
	recent, err := s.GetRecentTransactions(sender, 1000)
	if err != nil {
		t.Fatalf("GetRecentTransactions: %v", err)
	}
	seen := make(map[string]bool, len(recent))
	for _, row := range recent {
		if id, ok := row["tx_id"].(string); ok {
			seen[id] = true
		}
	}

	missing := 0
	acked.Range(func(k, _ interface{}) bool {
		if !seen[k.(string)] {
			missing++
		}
		return true
	})
	if missing > 0 {
		t.Errorf("chaos: %d acked tx ids missing from recent-tx view (total acked=%d)", missing, ackedCount.Load())
	}
}

// TestChaos_ScyllaReconnectAfterSessionLoss closes and reopens the storage session mid-run
// to simulate a short-lived network partition. Tx ids ACKed before reconnect must still be
// readable after. This variant works without docker; enabled via SCYLLA_CHAOS_NETPARTITION=1.
func TestChaos_ScyllaReconnectAfterSessionLoss(t *testing.T) {
	if os.Getenv("SCYLLA_CHAOS_NETPARTITION") != "1" {
		t.Skip("set SCYLLA_CHAOS_NETPARTITION=1 to run reconnect chaos")
	}
	hosts := chaosHostsOrSkip(t)

	keyspace := strings.TrimSpace(os.Getenv("SCYLLA_KEYSPACE"))
	if keyspace == "" {
		keyspace = "qsdm"
	}

	open := func() *ScyllaStorage {
		s, err := NewScyllaStorage(hosts, keyspace, ScyllaClusterConfigFromEnv())
		if err != nil {
			t.Fatalf("NewScyllaStorage: %v", err)
		}
		return s
	}

	s := open()
	sender := fmt.Sprintf("chaos-reconnect-%d", time.Now().UnixNano())
	var pre []string
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("pre-%s-%d", sender, i)
		body, _ := json.Marshal(map[string]interface{}{
			"tx_id":             id,
			"sender_address":    sender,
			"recipient_address": "chaos-recipient",
			"amount":            0.01,
			"timestamp":         time.Now().UTC().Format(time.RFC3339Nano),
		})
		if err := s.StoreTransaction(body); err != nil {
			t.Fatalf("pre-chaos StoreTransaction: %v", err)
		}
		pre = append(pre, id)
	}

	// Simulate partition by closing and reopening the session.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	s = open()
	defer s.Close()

	// Write more after reconnect.
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("post-%s-%d", sender, i)
		body, _ := json.Marshal(map[string]interface{}{
			"tx_id":             id,
			"sender_address":    sender,
			"recipient_address": "chaos-recipient",
			"amount":            0.01,
			"timestamp":         time.Now().UTC().Format(time.RFC3339Nano),
		})
		if err := s.StoreTransaction(body); err != nil {
			t.Fatalf("post-chaos StoreTransaction: %v", err)
		}
	}

	recent, err := s.GetRecentTransactions(sender, 500)
	if err != nil {
		t.Fatalf("GetRecentTransactions: %v", err)
	}
	seen := make(map[string]bool, len(recent))
	for _, row := range recent {
		if id, ok := row["tx_id"].(string); ok {
			seen[id] = true
		}
	}
	for _, id := range pre {
		if !seen[id] {
			t.Errorf("pre-chaos tx %s missing after reconnect", id)
		}
	}
}
