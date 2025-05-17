package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/internal/webviewer"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/networking"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	// Removed separate reputation import since reputation.go is part of quarantine package
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wasm"
)

func setupLogging() {
	logging.SetupLogger("qsdm.log")
	logging.Info.Println("Quantum-Secure Dynamic Mesh Ledger (QSDM) node starting up...")
}

func startWebViewer() {
	webviewer.StartWebLogViewer("qsdm.log", "8080")
}

func setupNetwork(ctx context.Context) (*networking.Network, error) {
	net, err := networking.SetupLibP2P(ctx)
	if err != nil {
		return nil, err
	}
	logging.Info.Printf("LibP2P host ID: %s\n", net.Host.ID().String())
	return net, nil
}

func setupStorage() (storage.Storage, error) {
	// Load environment variables from .env file if present
	_ = godotenv.Load()

	useScylla := os.Getenv("USE_SCYLLA")
	if useScylla == "true" {
		hosts := []string{"127.0.0.1"} // Replace with actual ScyllaDB hosts
		keyspace := "qsdm"
		scyllaStorage, err := storage.NewScyllaStorage(hosts, keyspace)
		if err != nil {
			return nil, err
		}
		logging.Info.Println("Using ScyllaDB storage")
		return scyllaStorage, nil
	}

	sqliteStorage, err := storage.NewStorage("transactions.db")
	if err != nil {
		return nil, err
	}
	logging.Info.Println("Using SQLite storage")
	return sqliteStorage, nil
}

func setupConsensus() *consensus.ProofOfEntanglement {
	return consensus.NewProofOfEntanglement()
}

func setupWASM() (*wasm.WASMSDK, error) {
	// Load wallet WASM module
	walletWasmPath := "wasm_modules/wallet/wallet.wasm"
	walletBytes, err := wasm.LoadWASMFromFile(walletWasmPath)
	if err != nil {
		logging.Warn.Printf("Failed to load wallet WASM module from %s: %v", walletWasmPath, err)
		log.Println("WASM wallet module disabled due to missing WASM file")
		return nil, nil
	}
	walletSdk, err := wasm.NewWASMSDK(walletBytes)
	if err != nil {
		logging.Error.Printf("Failed to create WASM SDK for wallet: %v", err)
		return nil, err
	}
	logging.Info.Println("WASM wallet SDK initialized")

	// Load validator WASM module
	validatorWasmPath := "wasm_modules/validator/validator.wasm"
	validatorBytes, err := wasm.LoadWASMFromFile(validatorWasmPath)
	if err != nil {
		logging.Warn.Printf("Failed to load validator WASM module from %s: %v", validatorWasmPath, err)
		log.Println("WASM validator module disabled due to missing WASM file")
		return walletSdk, nil // Return wallet SDK even if validator missing
	}
	validatorSdk, err := wasm.NewWASMSDK(validatorBytes)
	if err != nil {
		logging.Error.Printf("Failed to create WASM SDK for validator: %v", err)
		return walletSdk, nil
	}
	logging.Info.Println("WASM validator SDK initialized")

	// For simplicity, return wallet SDK; extend struct to hold both if needed
	return walletSdk, nil
}

func handleTransaction(msg []byte, dynamicManager *submesh.DynamicSubmeshManager, wasmSdk *wasm.WASMSDK, consensus *consensus.ProofOfEntanglement, storage *storage.Storage) {
	tx := msg
	parentCells := [][]byte{[]byte("parent1"), []byte("parent2")} // TODO: Extract actual parent cells from msg if needed

	fee := 0.001
	geoTag := "US"

	ds, err := dynamicManager.RouteTransaction(fee, geoTag)
	if err != nil {
		logging.Warn.Printf("No dynamic submesh matched for transaction: %v", err)
	} else {
		logging.Info.Printf("Routing transaction to dynamic submesh: %s with priority %d", ds.Name, ds.PriorityLevel)
	}

	if wasmSdk != nil {
		_, err = wasmSdk.CallFunction("validateTransaction", tx)
		if err != nil {
			logging.Warn.Printf("WASM validation failed: %v", err)
			return
		}
	}

	signature, err := consensus.Sign(tx)
	if err != nil {
		logging.Error.Printf("Failed to sign transaction: %v", err)
		return
	}
	signatures := [][]byte{signature}

	valid, err := consensus.ValidateTransaction(tx, parentCells, signatures)
	if err != nil || !valid {
		logging.Warn.Println("Received invalid transaction, discarding")
		return
	}

	err = storage.StoreTransaction(tx)
	if err != nil {
		logging.Error.Printf("Failed to store transaction: %v", err)
	} else {
		logging.Info.Println("Transaction stored successfully")
	}
}

func setupPhase3Components() (*mesh3d.Mesh3DValidator, *quarantine.QuarantineManager) {
	mesh3dValidator := mesh3d.NewMesh3DValidator()
	quarantineManager := quarantine.NewQuarantineManager(0.5) // 50% invalid tx threshold
	return mesh3dValidator, quarantineManager
}

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/internal/webviewer"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/networking"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wasm"
	"github.com/blackbeardONE/QSDM/pkg/quarantine/reputation"
)

func handlePhase3Transaction(msg []byte, mesh3dValidator *mesh3d.Mesh3DValidator, quarantineManager *quarantine.QuarantineManager, reputationManager *quarantine.ReputationManager, consensus *consensus.ProofOfEntanglement, storage *storage.Storage) {
	tx := &mesh3d.Transaction{
		ID: "tx1",
		ParentCells: []mesh3d.ParentCell{
			{ID: "p1", Data: []byte("parent1")},
			{ID: "p2", Data: []byte("parent2")},
			{ID: "p3", Data: []byte("parent3")},
		},
		Data: msg,
	}

	valid, err := mesh3dValidator.ValidateTransaction(tx)
	if err != nil {
		logging.Error.Printf("3D mesh validation error: %v", err)
		quarantineManager.RecordTransaction("default-submesh", false)
		reputationManager.Penalize("default-node")
		return
	}
	quarantineManager.RecordTransaction("default-submesh", valid)
	if valid {
		reputationManager.Reward("default-node")
	} else {
		reputationManager.Penalize("default-node")
	}

	if !valid {
		logging.Warn.Println("Transaction failed 3D mesh validation, discarding")
		return
	}

	signature, err := consensus.Sign(tx.Data)
	if err != nil {
		logging.Error.Printf("Failed to sign transaction: %v", err)
		return
	}
	signatures := [][]byte{signature}

	validConsensus, err := consensus.ValidateTransaction(tx.Data, [][]byte{[]byte("parent1"), []byte("parent2")}, signatures)
	if err != nil || !validConsensus {
		logging.Warn.Println("Received invalid transaction by consensus, discarding")
		return
	}

	err = storage.StoreTransaction(tx.Data)
	if err != nil {
		logging.Error.Printf("Failed to store transaction: %v", err)
	} else {
		logging.Info.Println("Transaction stored successfully")
	}
}

func main() {
	setupLogging()

	startWebViewer()

	dynamicManager := submesh.NewDynamicSubmeshManager()
	go submeshCLI(dynamicManager)

	governanceManager := governance.NewSnapshotVoting()
	go governanceCLI(governanceManager)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	net, err := setupNetwork(ctx)
	if err != nil {
		logging.Error.Fatalf("Failed to setup libp2p: %v", err)
	}

	storage, err := setupStorage()
	if err != nil {
		logging.Error.Fatalf("Failed to initialize storage: %v", err)
	}
	defer storage.Close()

	consensus := setupConsensus()

	wasmSdk, err := setupWASM()
	if err != nil {
		logging.Error.Fatalf("Failed to initialize WASM SDK: %v", err)
	}

	net.SetMessageHandler(func(msg []byte) {
		handleTransaction(msg, dynamicManager, wasmSdk, consensus, storage)
	})

	go func() {
		for {
			tx := []byte("sample transaction data")
			err := net.Broadcast(tx)
			if err != nil {
				logging.Error.Printf("Failed to broadcast transaction: %v", err)
			} else {
				logging.Info.Println("Transaction broadcasted successfully")
			}
			time.Sleep(10 * time.Second)
		}
	}()

	mesh3dValidator, quarantineManager := setupPhase3Components()
	reputationManager := quarantine.NewReputationManager(10, 5)
	monitor := quarantine.NewMonitor(quarantineManager, 30*time.Second)
	monitor.Start()

	net.SetMessageHandler(func(msg []byte) {
		handlePhase3Transaction(msg, mesh3dValidator, quarantineManager, reputationManager, consensus, storage)
	})

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	<-sigs

	logging.Info.Println("Shutting down QSDM node...")
	if err := net.Close(); err != nil {
		logging.Error.Printf("Error closing libp2p host: %v", err)
	}
	logging.Info.Println("QSDM node stopped.")
}
