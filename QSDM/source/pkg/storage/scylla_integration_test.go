//go:build scylla

package storage

import (
	"os"
	"strings"
	"testing"
)

// TestIntegrationScyllaRecentTxPath requires a reachable Scylla cluster (e.g. docker per scripts/scylla-docker-dev.ps1).
// Set SCYLLA_HOSTS (comma-separated, optional) and SCYLLA_KEYSPACE (optional, default qsdm).
// Example: SCYLLA_HOSTS=127.0.0.1 go test -tags scylla ./pkg/storage/... -run TestIntegrationScyllaRecentTxPath
func TestIntegrationScyllaRecentTxPath(t *testing.T) {
	host := strings.TrimSpace(os.Getenv("SCYLLA_HOSTS"))
	if host == "" {
		t.Skip("set SCYLLA_HOSTS to run (e.g. 127.0.0.1)")
	}
	var hosts []string
	for _, h := range strings.Split(host, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	if len(hosts) == 0 {
		t.Fatal("SCYLLA_HOSTS produced no hosts")
	}
	ks := strings.TrimSpace(os.Getenv("SCYLLA_KEYSPACE"))
	if ks == "" {
		ks = "qsdm"
	}
	s, err := NewScyllaStorage(hosts, ks, ScyllaClusterConfigFromEnv())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Ready(); err != nil {
		t.Fatal(err)
	}
	txs, err := s.GetRecentTransactions("__integration_empty_wallet__", 10)
	if err != nil {
		t.Fatal(err)
	}
	if txs == nil {
		t.Fatal("expected non-nil slice")
	}
	if len(txs) != 0 {
		t.Fatalf("expected 0 txs for synthetic address, got %d", len(txs))
	}
}
