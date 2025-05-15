
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"
    "time"
    "log"

    "github.com/blackbeardONE/QSDM/internal/logging"
    "github.com/blackbeardONE/QSDM/internal/webviewer"
    "github.com/blackbeardONE/QSDM/pkg/networking"
    "github.com/blackbeardONE/QSDM/pkg/storage"
    "github.com/blackbeardONE/QSDM/pkg/consensus"
    "github.com/blackbeardONE/QSDM/config"

    "github.com/blackbeardONE/QSDM/pkg/submesh"
    "github.com/blackbeardONE/QSDM/pkg/wasm"
    "github.com/blackbeardONE/QSDM/pkg/mesh3d"
    "github.com/blackbeardONE/QSDM/pkg/quarantine"
    // "github.com/blackbeardONE/QSDM/pkg/reputation"
)

func main() {
    logging.SetupLogger("qsmd.log")
    logging.Info.Println("Quantum-Secure Dynamic Mesh Ledger (QSDM) node starting up...")

    // Load submesh config
    submeshConfig, err := config.LoadSubmeshConfig("config/micropayments.yml")
    if err != nil {
        logging.Error.Fatalf("Failed to load submesh config: %v", err)
    }
    logging.Info.Printf("Loaded submesh config: %+v\n", submeshConfig)

    // Start web log viewer on port 8080
    webviewer.StartWebLogViewer("qsmd.log", "8080")

    // Initialize dynamic submesh manager
    dynamicManager := submesh.NewDynamicSubmeshManager()

    // Start CLI for dynamic submesh management in a separate goroutine
    go submeshCLI(dynamicManager)

    // Start CLI for governance voting in a separate goroutine
    go governanceCLI()

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    net, err := networking.SetupLibP2P(ctx)
    if err != nil {
        logging.Error.Fatalf("Failed to setup libp2p: %v", err)
    }
    logging.Info.Printf("LibP2P host ID: %s\n", net.Host.ID().String())

    // Initialize storage
    storage, err := storage.NewStorage("transactions.db")
    if err != nil {
        logging.Error.Fatalf("Failed to initialize storage: %v", err)
    }
    defer storage.Close()

    // Initialize consensus
    consensus := consensus.NewProofOfEntanglement()

    // Initialize WASM SDK, load WASM module bytes from file
    var wasmSdk *wasm.WASMSDK
    // Update WASM module path to the newly built non-wasm-bindgen module
    wasmWasmPath := "wasm_module_bg.wasm"
    wasmBytes, err := wasm.LoadWASMFromFile(wasmWasmPath)
    if err != nil {
        logging.Warn.Printf("Failed to load WASM module from %s: %v", wasmWasmPath, err)
        log.Println("WASM SDK disabled due to missing WASM module")
    } else {
        wasmSdk, err = wasm.NewWASMSDK(wasmBytes)
        if err != nil {
            logging.Error.Fatalf("Failed to initialize WASM SDK: %v", err)
        }
        logging.Info.Println("WASM SDK initialized")
    }

    // Set message handler to process incoming transactions
    net.SetMessageHandler(func(msg []byte) {
        // For simplicity, assume msg is the transaction data
        tx := msg
        parentCells := [][]byte{[]byte("parent1"), []byte("parent2")} // TODO: Extract actual parent cells from msg if needed

        // Example: Extract fee and geoTag from transaction (placeholder)
        fee := 0.001
        geoTag := "US"

        // Use dynamic submesh manager to route transaction
        ds, err := dynamicManager.RouteTransaction(fee, geoTag)
        if err != nil {
            logging.Warn.Printf("No dynamic submesh matched for transaction: %v", err)
        } else {
            logging.Info.Printf("Routing transaction to dynamic submesh: %s with priority %d", ds.Name, ds.PriorityLevel)
        }

        // Use WASM SDK to call 'validate' function (if implemented in WASM)
        if wasmSdk != nil {
            _, err = wasmSdk.CallFunction("validate", tx)
            if err != nil {
                logging.Warn.Printf("WASM validation failed: %v", err)
                return
            }
        }

        // Sign the transaction using Dilithium
        signature, err := consensus.Sign(tx)
        if err != nil {
            logging.Error.Printf("Failed to sign transaction: %v", err)
            return
        }
        signatures := [][]byte{signature}

        // Validate transaction
        valid, err := consensus.ValidateTransaction(tx, parentCells, signatures)
        if err != nil || !valid {
            logging.Warn.Println("Received invalid transaction, discarding")
            return
        }

        // Store transaction
        err = storage.StoreTransaction(tx)
        if err != nil {
            logging.Error.Printf("Failed to store transaction: %v", err)
        } else {
            logging.Info.Println("Transaction stored successfully")
        }
    })

    // Periodic broadcast of sample transaction every 10 seconds
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

    // Initialize Phase 3 components
    mesh3dValidator := mesh3d.NewMesh3DValidator()
    quarantineManager := quarantine.NewQuarantineManager(0.5) // 50% invalid tx threshold
    // Removed unused reputationManager declaration

    // Example integration: Validate transaction with 3D mesh validator and quarantine logic
    net.SetMessageHandler(func(msg []byte) {
        tx := &mesh3d.Transaction{
            ID: "tx1",
            ParentCells: []mesh3d.ParentCell{
                {ID: "p1", Data: []byte("parent1")},
                {ID: "p2", Data: []byte("parent2")},
                {ID: "p3", Data: []byte("parent3")},
            },
            Data: msg,
        }

        // Use CPU validation only (removed undefined CUDA validation)
        valid, err := mesh3dValidator.ValidateTransaction(tx)
        if err != nil {
            logging.Error.Printf("3D mesh validation error: %v", err)
            quarantineManager.RecordTransaction("default-submesh", false)
            return
        }
        quarantineManager.RecordTransaction("default-submesh", valid)

        if !valid {
            logging.Warn.Println("Transaction failed 3D mesh validation, discarding")
            return
        }

        // Proceed with existing processing (sign, validate consensus, store, etc.)
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
    })

    // Wait for termination signal
    sigs := make(chan os.Signal, 1)
    signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
    <-sigs

    logging.Info.Println("Shutting down QSDM node...")
    if err := net.Close(); err != nil {
        logging.Error.Printf("Error closing libp2p host: %v", err)
    }
    logging.Info.Println("QSDM node stopped.")
}
