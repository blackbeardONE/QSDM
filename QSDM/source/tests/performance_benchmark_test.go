package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/governance"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
)

// BenchmarkTransactionCreation measures transaction creation performance
func BenchmarkTransactionCreation(b *testing.B) {
	walletService, err := wallet.NewWalletService()
	if err != nil {
		b.Skipf("Wallet service not available: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := walletService.CreateTransaction(
			fmt.Sprintf("recipient%d", i),
			100,
			0.01,
			"US",
			[]string{"parent1", "parent2"},
		)
		if err != nil {
			b.Fatalf("Failed to create transaction: %v", err)
		}
	}
}

// BenchmarkConsensusValidation measures consensus validation performance
func BenchmarkConsensusValidation(b *testing.B) {
	poe := consensus.NewProofOfEntanglement()
	if poe == nil {
		b.Skip("Consensus not available (CGO disabled)")
	}

	logger := logging.NewLogger("bench.log", false)

	txData := []byte("test transaction data")
	parentCells := [][]byte{[]byte("parent1"), []byte("parent2")}
	signature, _ := poe.Sign(txData)
	signatures := [][]byte{signature}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = poe.ValidateTransaction(txData, parentCells, signatures, logger)
	}
}

// Benchmark3DMeshValidationCPU measures 3D mesh validation (CPU)
func Benchmark3DMeshValidationCPU(b *testing.B) {
	validator := mesh3d.NewMesh3DValidator()

	tx := &mesh3d.Transaction{
		ID: "bench_tx",
		ParentCells: []mesh3d.ParentCell{
			{ID: "p1", Data: make([]byte, 64)},
			{ID: "p2", Data: make([]byte, 64)},
			{ID: "p3", Data: make([]byte, 64)},
		},
		Data: make([]byte, 256),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = validator.ValidateTransaction(tx)
	}
}

// BenchmarkStorageOperations measures storage performance
func BenchmarkStorageOperations(b *testing.B) {
	store, err := storage.NewFileStorage("bench_data")
	if err != nil {
		b.Skipf("Storage not available: %v", err)
	}
	defer func() {
		store.Close()
		os.RemoveAll("bench_data")
	}()

	txData := []byte(`{"id":"bench_tx","amount":100}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = store.StoreTransaction(txData)
	}
}

// BenchmarkMempoolBulkAdd measures sequential inserts into the default mempool (fee-heap path).
func BenchmarkMempoolBulkAdd(b *testing.B) {
	m := mempool.New(mempool.DefaultConfig())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id := fmt.Sprintf("bench-mem-%d", i)
		_ = m.Add(&mempool.Tx{
			ID: id, Sender: "a", Recipient: "b", Amount: 1, Fee: float64(i%100) * 0.001,
		})
	}
}

// BenchmarkSubmeshRouting measures submesh routing performance
func BenchmarkSubmeshRouting(b *testing.B) {
	dsManager := submesh.NewDynamicSubmeshManager()

	// Setup submeshes
	for i := 0; i < 10; i++ {
		ds := &submesh.DynamicSubmesh{
			Name:          fmt.Sprintf("submesh%d", i),
			FeeThreshold:  float64(i) * 0.01,
			PriorityLevel: i,
			GeoTags:       []string{"US", "EU"},
		}
		dsManager.AddOrUpdateSubmesh(ds)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = dsManager.RouteTransaction(0.05, "US")
	}
}

// BenchmarkGovernanceVoting measures governance voting performance
func BenchmarkGovernanceVoting(b *testing.B) {
	sv := governance.NewSnapshotVoting("bench_proposals.json")
	proposalID := "bench_prop"
	duration := 1 * time.Hour
	quorum := 10

	_ = sv.AddProposal(proposalID, "Benchmark proposal", duration, quorum)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sv.Vote(proposalID, fmt.Sprintf("voter%d", i), 1, true)
	}
}

// BenchmarkJSONMarshaling measures JSON performance
func BenchmarkJSONMarshaling(b *testing.B) {
	tx := map[string]interface{}{
		"id":          "tx123",
		"sender":      "sender123",
		"recipient":   "recipient123",
		"amount":      100.0,
		"fee":         0.01,
		"geotag":      "US",
		"parentCells": []string{"p1", "p2"},
		"timestamp":   time.Now().Format(time.RFC3339),
		"signature":   "sig123",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = json.Marshal(tx)
	}
}

// BenchmarkTransactionThroughput measures end-to-end throughput
func BenchmarkTransactionThroughput(b *testing.B) {
	store, err := storage.NewFileStorage("bench_throughput_data")
	if err != nil {
		b.Skipf("Storage not available: %v", err)
	}
	defer func() {
		store.Close()
		os.RemoveAll("bench_throughput_data")
	}()

	walletService, err := wallet.NewWalletService()
	if err != nil {
		b.Skipf("Wallet service not available: %v", err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			txData, err := walletService.CreateTransaction(
				"recipient",
				100,
				0.01,
				"US",
				[]string{"parent1", "parent2"},
			)
			if err == nil {
				_ = store.StoreTransaction(txData)
			}
		}
	})
}

// CompareCUDAvsCPU compares CUDA vs CPU performance
func CompareCUDAvsCPU(b *testing.B) {
	validator := mesh3d.NewMesh3DValidator()

	tx := &mesh3d.Transaction{
		ID: "compare_tx",
		ParentCells: []mesh3d.ParentCell{
			{ID: "p1", Data: make([]byte, 64)},
			{ID: "p2", Data: make([]byte, 64)},
			{ID: "p3", Data: make([]byte, 64)},
		},
		Data: make([]byte, 256),
	}

	// CPU benchmark
	b.Run("CPU", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = validator.ValidateTransaction(tx)
		}
	})

	// Note: CUDA benchmark would require actual CUDA device
	// This is a placeholder for when CUDA is available
	b.Log("CUDA comparison: CUDA acceleration available when GPU is present")
}
