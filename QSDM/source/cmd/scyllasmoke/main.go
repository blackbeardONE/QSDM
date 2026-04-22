// Scyllasmoke connects to Scylla, runs initSchema via NewScyllaStorage, and exercises read paths.
// Use after `scripts/scylla-docker-dev.ps1` / a running cluster.
//
// Environment:
//   SCYLLA_HOSTS   comma-separated native transport addresses (default: 127.0.0.1)
//   SCYLLA_KEYSPACE keyspace name (default: qsdm)
//   SCYLLA_USERNAME, SCYLLA_PASSWORD, SCYLLA_TLS_CA_PATH, SCYLLA_TLS_CERT_PATH, SCYLLA_TLS_KEY_PATH,
//   SCYLLA_TLS_INSECURE_SKIP_VERIFY — optional CQL auth / TLS (see storage.ScyllaClusterConfigFromEnv)
package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/storage"
)

func main() {
	log.SetFlags(0)
	hosts := parseHosts(getenv("SCYLLA_HOSTS", "127.0.0.1"))
	keyspace := getenv("SCYLLA_KEYSPACE", "qsdm")

	fmt.Printf("scyllasmoke: hosts=%v keyspace=%s\n", hosts, keyspace)

	s, err := storage.NewScyllaStorage(hosts, keyspace, storage.ScyllaClusterConfigFromEnv())
	if err != nil {
		log.Fatalf("NewScyllaStorage: %v", err)
	}
	defer s.Close()

	if err := s.Ready(); err != nil {
		log.Fatalf("Ready: %v", err)
	}
	fmt.Println("scyllasmoke: Ready OK")

	txs, err := s.GetRecentTransactions("__scyllasmoke_no_history__", 5)
	if err != nil {
		log.Fatalf("GetRecentTransactions: %v", err)
	}
	if txs == nil {
		txs = []map[string]interface{}{}
	}
	fmt.Printf("scyllasmoke: GetRecentTransactions (empty wallet) count=%d OK\n", len(txs))

	_, err = s.GetTransaction("__scyllasmoke_missing_tx__")
	if err == nil {
		log.Fatal("GetTransaction: expected error for missing tx")
	}
	fmt.Printf("scyllasmoke: GetTransaction missing id -> error (expected): %v\n", err)

	fmt.Println("scyllasmoke: all checks passed")
}

func getenv(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func parseHosts(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{"127.0.0.1"}
	}
	return out
}
