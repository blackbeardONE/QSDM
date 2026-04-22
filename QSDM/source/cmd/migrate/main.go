package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/storage"
)

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "-stats-only" {
		if len(os.Args) < 3 {
			fmt.Println("Usage: migrate -stats-only <source_db>")
			fmt.Println("Counts transaction and balance rows by walking SQLite (same iterators as migrate; no Scylla).")
			os.Exit(1)
		}
		if err := runStatsOnly(os.Args[2]); err != nil {
			log.Fatal(err)
		}
		return
	}

	if len(os.Args) < 3 {
		fmt.Println("Usage: migrate <source_db> <scylla_hosts> [scylla_keyspace]")
		fmt.Println("       migrate -stats-only <source_db>")
		fmt.Println("")
		fmt.Println("Arguments:")
		fmt.Println("  source_db        Path to SQLite database file (requires CGO build for export)")
		fmt.Println("  scylla_hosts     Comma-separated list of ScyllaDB hosts (e.g., 127.0.0.1)")
		fmt.Println("  scylla_keyspace  ScyllaDB keyspace name (default: qsdm)")
		fmt.Println("")
		fmt.Println("Exports all transactions from SQLite (decrypt), inserts via StoreTransactionMigrate")
		fmt.Println("(no balance deltas), then copies balances from SQLite with SetBalance.")
		fmt.Println("")
		fmt.Println("Example:")
		fmt.Println("  migrate qsdmplus.db 127.0.0.1 qsdm")
		fmt.Println("  migrate qsdmplus.db 127.0.0.1,127.0.0.2 qsdm   # multiple hosts")
		fmt.Println("  migrate -stats-only qsdmplus.db               # row counts + timing, no Scylla")
		fmt.Println("")
		fmt.Println("Optional CQL auth/TLS: set SCYLLA_USERNAME, SCYLLA_PASSWORD, SCYLLA_TLS_* (see SCYLLA_MIGRATION.md §10).")
		os.Exit(1)
	}

	sourceDB := os.Args[1]
	scyllaHosts := parseScyllaHosts(os.Args[2])

	keyspace := "qsdm"
	if len(os.Args) > 3 {
		keyspace = os.Args[3]
	}

	runStart := time.Now()
	fmt.Printf("=== %s SQLite to ScyllaDB Migration Tool ===\n\n", branding.Name)
	fmt.Printf("Source: %s\n", sourceDB)
	fmt.Printf("Destination: ScyllaDB (%v, keyspace: %s)\n\n", scyllaHosts, keyspace)

	fmt.Println("1. Opening SQLite database...")
	t0 := time.Now()
	sqliteStorage, err := storage.NewStorage(sourceDB)
	if err != nil {
		log.Fatalf("Failed to open SQLite database: %v", err)
	}
	defer sqliteStorage.Close()
	fmt.Printf("   (elapsed %s)\n", time.Since(t0).Round(time.Millisecond))

	fmt.Println("2. Connecting to ScyllaDB...")
	t1 := time.Now()
	scyllaStorage, err := storage.NewScyllaStorage(scyllaHosts, keyspace, storage.ScyllaClusterConfigFromEnv())
	if err != nil {
		log.Fatalf("Failed to connect to ScyllaDB: %v", err)
	}
	defer scyllaStorage.Close()
	fmt.Printf("   (elapsed %s)\n", time.Since(t1).Round(time.Millisecond))

	fmt.Println("3. Migrating transactions (history only, no balance deltas)...")
	t2 := time.Now()
	if err := migrateTransactions(sqliteStorage, scyllaStorage); err != nil {
		log.Fatalf("Failed to migrate transactions: %v", err)
	}
	fmt.Printf("   (phase elapsed %s)\n", time.Since(t2).Round(time.Millisecond))

	fmt.Println("4. Migrating balances...")
	t3 := time.Now()
	if err := migrateBalances(sqliteStorage, scyllaStorage); err != nil {
		log.Fatalf("Failed to migrate balances: %v", err)
	}
	fmt.Printf("   (phase elapsed %s)\n", time.Since(t3).Round(time.Millisecond))

	fmt.Println("\n=== Migration Complete ===")
	fmt.Println("Transactions and balances copied. Re-run is partially idempotent (Scylla LWT on tx_id).")
	fmt.Printf("Total wall time: %s\n", time.Since(runStart).Round(time.Millisecond))
}

// runStatsOnly walks SQLite like migrate but only counts rows (no Scylla). Useful for sizing
// large databases before a maintenance window.
func runStatsOnly(sourceDB string) error {
	runStart := time.Now()
	fmt.Printf("=== %s SQLite migration stats (no Scylla) ===\n\n", branding.Name)
	fmt.Printf("Source: %s\n\n", sourceDB)

	fmt.Println("1. Opening SQLite database...")
	t0 := time.Now()
	sqliteStorage, err := storage.NewStorage(sourceDB)
	if err != nil {
		return fmt.Errorf("open sqlite: %w", err)
	}
	defer sqliteStorage.Close()
	fmt.Printf("   (elapsed %s)\n", time.Since(t0).Round(time.Millisecond))

	fmt.Println("2. Counting stored transactions (decrypt walk)...")
	t1 := time.Now()
	txCount, err := countStoredTransactions(sqliteStorage)
	if err != nil {
		return fmt.Errorf("count transactions: %w", err)
	}
	fmt.Printf("   Transactions: %d (phase elapsed %s)\n", txCount, time.Since(t1).Round(time.Millisecond))

	fmt.Println("3. Counting balance rows...")
	t2 := time.Now()
	balCount, err := countBalanceRows(sqliteStorage)
	if err != nil {
		return fmt.Errorf("count balances: %w", err)
	}
	fmt.Printf("   Balances: %d (phase elapsed %s)\n", balCount, time.Since(t2).Round(time.Millisecond))

	fmt.Println("\n=== Stats complete ===")
	fmt.Printf("Total wall time: %s\n", time.Since(runStart).Round(time.Millisecond))
	return nil
}

func countStoredTransactions(source *storage.Storage) (int, error) {
	n := 0
	err := source.ForEachStoredTransaction(func(_ []byte) error {
		n++
		if n%10000 == 0 {
			fmt.Printf("   ... %d transactions\n", n)
		}
		return nil
	})
	return n, err
}

func countBalanceRows(source *storage.Storage) (int, error) {
	n := 0
	err := source.ForEachBalance(func(_ string, _ float64) error {
		n++
		return nil
	})
	return n, err
}

func migrateTransactions(source *storage.Storage, dest *storage.ScyllaStorage) error {
	n := 0
	err := source.ForEachStoredTransaction(func(raw []byte) error {
		if err := dest.StoreTransactionMigrate(raw); err != nil {
			return fmt.Errorf("after %d ok rows: %w", n, err)
		}
		n++
		if n%500 == 0 {
			fmt.Printf("   ... migrated %d transactions\n", n)
		}
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("   Migrated %d transactions\n", n)
	return nil
}

func migrateBalances(source *storage.Storage, dest *storage.ScyllaStorage) error {
	n := 0
	err := source.ForEachBalance(func(address string, balance float64) error {
		if err := dest.SetBalance(address, balance); err != nil {
			return fmt.Errorf("balance %s: %w", address, err)
		}
		n++
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Printf("   Migrated %d balance rows\n", n)
	return nil
}

func parseScyllaHosts(arg string) []string {
	var out []string
	for _, h := range strings.Split(arg, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		return []string{"127.0.0.1"}
	}
	return out
}
