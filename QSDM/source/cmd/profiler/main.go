package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/storage"
)

type StorageInterface interface {
	StoreTransaction(data []byte) error
	GetBalance(address string) (float64, error)
	UpdateBalance(address string, amount float64) error
	SetBalance(address string, balance float64) error
	GetTransaction(txID string) (map[string]interface{}, error)
	Close() error
}

func main() {
	var (
		cpuProfile  = flag.String("cpuprofile", "", "write cpu profile to file")
		memProfile  = flag.String("memprofile", "", "write memory profile to file")
		storageType = flag.String("storage", "sqlite", "storage type (sqlite or scylla)")
		operation   = flag.String("op", "all", "operation to profile (store, balance, get, all)")
		iterations  = flag.Int("iterations", 1000, "number of iterations")
	)
	flag.Parse()

	if *cpuProfile != "" {
		f, err := os.Create(*cpuProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	fmt.Printf("=== %s Performance Profiler ===\n\n", branding.Name)
	fmt.Printf("Storage: %s\n", *storageType)
	fmt.Printf("Operation: %s\n", *operation)
	fmt.Printf("Iterations: %d\n\n", *iterations)

	// Initialize storage
	var s StorageInterface

	switch *storageType {
	case "sqlite":
		st, err := storage.NewStorage("profiler.db")
		if err != nil {
			log.Fatalf("Failed to initialize SQLite: %v", err)
		}
		s = st
		defer s.Close()
		defer os.Remove("profiler.db")

	case "scylla":
		st, err := storage.NewScyllaStorage([]string{"127.0.0.1"}, "profiler", storage.ScyllaClusterConfigFromEnv())
		if err != nil {
			log.Fatalf("Failed to initialize ScyllaDB: %v", err)
		}
		s = st
		defer s.Close()

	default:
		log.Fatalf("Invalid storage type: %s", *storageType)
	}

	// Run profiling
	start := time.Now()

	switch *operation {
	case "store":
		profileStoreTransaction(s, *iterations)
	case "balance":
		profileBalanceOperations(s, *iterations)
	case "get":
		profileGetTransaction(s, *iterations)
	case "all":
		profileStoreTransaction(s, *iterations)
		profileBalanceOperations(s, *iterations)
		profileGetTransaction(s, *iterations)
	default:
		log.Fatalf("Invalid operation: %s (use store, balance, get, or all)", *operation)
	}

	duration := time.Since(start)
	fmt.Printf("\nTotal Duration: %v\n", duration)

	// Memory profile
	if *memProfile != "" {
		f, err := os.Create(*memProfile)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		runtime.GC()
		pprof.WriteHeapProfile(f)
		fmt.Printf("Memory profile written to %s\n", *memProfile)
	}

	fmt.Println("\n=== Profiling Complete ===")
	fmt.Println("Use 'go tool pprof' to analyze profiles:")
	if *cpuProfile != "" {
		fmt.Printf("  go tool pprof %s\n", *cpuProfile)
	}
	if *memProfile != "" {
		fmt.Printf("  go tool pprof %s\n", *memProfile)
	}
}

func profileStoreTransaction(s StorageInterface, count int) {
	fmt.Printf("Profiling StoreTransaction (%d iterations)...\n", count)
	start := time.Now()

	for i := 0; i < count; i++ {
		txData := []byte(fmt.Sprintf(`{"id":"tx%d","sender":"addr1","recipient":"addr2","amount":%d}`, i, i))
		if err := s.StoreTransaction(txData); err != nil {
			log.Printf("Error: %v", err)
		}
	}

	duration := time.Since(start)
	fmt.Printf("  Duration: %v\n", duration)
	fmt.Printf("  Throughput: %.2f tx/s\n", float64(count)/duration.Seconds())
}

func profileBalanceOperations(s StorageInterface, count int) {
	fmt.Printf("Profiling BalanceOperations (%d iterations)...\n", count)

	// Set initial balance
	if err := s.SetBalance("profiler_address", 1000.0); err != nil {
		log.Printf("Error setting balance: %v", err)
		return
	}

	start := time.Now()
	for i := 0; i < count; i++ {
		if err := s.UpdateBalance("profiler_address", 1.0); err != nil {
			log.Printf("Error: %v", err)
		}
	}
	duration := time.Since(start)

	fmt.Printf("  Duration: %v\n", duration)
	fmt.Printf("  Throughput: %.2f ops/s\n", float64(count)/duration.Seconds())
}

func profileGetTransaction(s StorageInterface, count int) {
	fmt.Printf("Profiling GetTransaction (%d iterations)...\n", count)

	// Store some transactions first
	for i := 0; i < count; i++ {
		txData := []byte(fmt.Sprintf(`{"id":"get_tx%d","sender":"addr1","recipient":"addr2","amount":%d}`, i, i))
		if err := s.StoreTransaction(txData); err != nil {
			log.Printf("Error storing: %v", err)
		}
	}

	start := time.Now()
	for i := 0; i < count; i++ {
		txID := fmt.Sprintf("get_tx%d", i)
		_, err := s.GetTransaction(txID)
		if err != nil {
			// Expected for some storage backends
		}
	}
	duration := time.Since(start)

	fmt.Printf("  Duration: %v\n", duration)
	fmt.Printf("  Throughput: %.2f tx/s\n", float64(count)/duration.Seconds())
}
