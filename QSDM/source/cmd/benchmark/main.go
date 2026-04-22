package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/storage"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: benchmark <storage_type>")
		fmt.Println("  storage_type: sqlite or scylla")
		fmt.Println("")
		fmt.Println("Example:")
		fmt.Println("  benchmark sqlite")
		fmt.Println("  benchmark scylla")
		os.Exit(1)
	}

	storageType := os.Args[1]

	fmt.Printf("=== %s Storage Performance Benchmark ===\n\n", branding.Name)
	fmt.Printf("Storage Type: %s\n", storageType)
	fmt.Printf("Starting benchmark...\n\n")

	var s StorageInterface
	var err error

	switch storageType {
	case "sqlite":
		s, err = storage.NewStorage("benchmark.db")
		if err != nil {
			log.Fatalf("Failed to initialize SQLite: %v", err)
		}
		defer s.Close()
		defer os.Remove("benchmark.db") // Cleanup

	case "scylla":
		hosts := []string{"127.0.0.1"}
		if len(os.Args) > 2 {
			hosts = []string{os.Args[2]}
		}
		s, err = storage.NewScyllaStorage(hosts, "benchmark", storage.ScyllaClusterConfigFromEnv())
		if err != nil {
			log.Fatalf("Failed to initialize ScyllaDB: %v", err)
		}
		defer s.Close()

	default:
		log.Fatalf("Invalid storage type: %s (must be 'sqlite' or 'scylla')", storageType)
	}

	// Run benchmarks
	fmt.Println("1. Transaction Storage Benchmark")
	benchmarkStoreTransaction(s, 1000)

	fmt.Println("\n2. Balance Operations Benchmark")
	benchmarkBalanceOperations(s, 1000)

	fmt.Println("\n3. Transaction Retrieval Benchmark")
	benchmarkGetTransaction(s, 100)

	fmt.Println("\n=== Benchmark Complete ===")
}

type StorageInterface interface {
	StoreTransaction(data []byte) error
	GetBalance(address string) (float64, error)
	UpdateBalance(address string, amount float64) error
	SetBalance(address string, balance float64) error
	GetTransaction(txID string) (map[string]interface{}, error)
	Close() error
}

func benchmarkStoreTransaction(s StorageInterface, count int) {
	fmt.Printf("  Storing %d transactions...\n", count)

	start := time.Now()
	for i := 0; i < count; i++ {
		txData := []byte(fmt.Sprintf(`{"id":"tx%d","sender":"addr1","recipient":"addr2","amount":%d}`, i, i))
		if err := s.StoreTransaction(txData); err != nil {
			log.Printf("Error storing transaction %d: %v", i, err)
		}
	}
	duration := time.Since(start)

	throughput := float64(count) / duration.Seconds()
	fmt.Printf("  Duration: %v\n", duration)
	fmt.Printf("  Throughput: %.2f tx/s\n", throughput)
	fmt.Printf("  Avg Latency: %v\n", duration/time.Duration(count))
}

func benchmarkBalanceOperations(s StorageInterface, count int) {
	fmt.Printf("  Performing %d balance operations...\n", count)

	// Set initial balance
	if err := s.SetBalance("test_address", 1000.0); err != nil {
		log.Printf("Error setting balance: %v", err)
		return
	}

	start := time.Now()
	for i := 0; i < count; i++ {
		if err := s.UpdateBalance("test_address", 1.0); err != nil {
			log.Printf("Error updating balance: %v", err)
		}
	}
	duration := time.Since(start)

	// Verify final balance
	balance, err := s.GetBalance("test_address")
	if err != nil {
		log.Printf("Error getting balance: %v", err)
	} else {
		fmt.Printf("  Final balance: %.2f (expected: %.2f)\n", balance, 1000.0+float64(count))
	}

	throughput := float64(count) / duration.Seconds()
	fmt.Printf("  Duration: %v\n", duration)
	fmt.Printf("  Throughput: %.2f ops/s\n", throughput)
	fmt.Printf("  Avg Latency: %v\n", duration/time.Duration(count))
}

func benchmarkGetTransaction(s StorageInterface, count int) {
	fmt.Printf("  Retrieving %d transactions...\n", count)

	// First, store some transactions
	for i := 0; i < count; i++ {
		txData := []byte(fmt.Sprintf(`{"id":"get_tx%d","sender":"addr1","recipient":"addr2","amount":%d}`, i, i))
		if err := s.StoreTransaction(txData); err != nil {
			log.Printf("Error storing transaction: %v", err)
		}
	}

	start := time.Now()
	for i := 0; i < count; i++ {
		txID := fmt.Sprintf("get_tx%d", i)
		_, err := s.GetTransaction(txID)
		if err != nil {
			log.Printf("Error getting transaction %s: %v", txID, err)
		}
	}
	duration := time.Since(start)

	throughput := float64(count) / duration.Seconds()
	fmt.Printf("  Duration: %v\n", duration)
	fmt.Printf("  Throughput: %.2f tx/s\n", throughput)
	fmt.Printf("  Avg Latency: %v\n", duration/time.Duration(count))
}
