
package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/blackbeardONE/QSDM/config"
    "github.com/blackbeardONE/QSDM/internal/logging"
    "github.com/blackbeardONE/QSDM/internal/webviewer"
    "github.com/blackbeardONE/QSDM/pkg/consensus"
    "github.com/blackbeardONE/QSDM/pkg/mesh3d"
    "github.com/blackbeardONE/QSDM/pkg/networking"
    "github.com/blackbeardONE/QSDM/pkg/quarantine"
    "github.com/blackbeardONE/QSDM/pkg/storage"
    "github.com/blackbeardONE/QSDM/pkg/submesh"
    "github.com/blackbeardONE/QSDM/pkg/wasm"
)

package main

import (
    "context"
    "log"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/blackbeardONE/QSDM/config"
    "github.com/blackbeardONE/QSDM/internal/logging"
    "github.com/blackbeardONE/QSDM/internal/webviewer"
    "github.com/blackbeardONE/QSDM/pkg/consensus"
    "github.com/blackbeardONE/QSDM/pkg/mesh3d"
    "github.com/blackbeardONE/QSDM/pkg/networking"
    "github.com/blackbeardONE/QSDM/pkg/quarantine"
    "github.com/blackbeardONE/QSDM/pkg/storage"
    "github.com/blackbeardONE/QSDM/pkg/submesh"
    "github.com/blackbeardONE/QSDM/pkg/wasm"
)

func setupLogging() {
    logging.SetupLogger("qsmd.log")
    logging.Info.Println("Quantum-Secure Dynamic Mesh Ledger (QSDM) node starting up...")
}

func loadConfig() (*submesh.SubmeshConfig, error) {
    submeshConfig, err := config.LoadSubmeshConfig("config/micropayments.yml")
    if err != nil {
        return nil, err
    }
    logging.Info.Printf("Loaded submesh config: %+v\n", submeshConfig)
    return submeshConfig, nil
}

func startWebViewer() {
    webviewer.StartWebLogViewer("qsmd.log", "8080")
}

func setupNetwork(ctx context.Context) (*networking.Network, error) {
    net, err := networking.SetupLibP2P(ctx)
    if err != nil {
        return nil, err
    }
    logging.Info.Printf("LibP2P host ID: %s\n", net.Host.ID().String())
    return net, nil
}

func setupStorage() (*storage.Storage, error) {
    storage, err := storage.NewStorage("transactions.db")
    if err != nil {
        return nil, err
    }
    return storage, nil
}

func setupConsensus() *consensus.ProofOfEntanglement {
    return consensus.NewProofOfEntanglement()
}

func setupWASM() (*wasm.WASMSDK, error) {
    wasmWasmPath := "wasm_module_bg.wasm"
    wasmBytes, err := wasm.LoadWASMFromFile(wasmWasmPath)
    if err != nil {
        logging.Warn.Printf("Failed to load WASM module from %s: %v", wasmWasmPath, err)
        log.Println("WASM SDK disabled due to missing WASM module")
        return nil, nil
    }
    wasmSdk, err := wasm.NewWASMSDK(wasmBytes)
    if err != nil {
        return nil, err
    }
    logging.Info.Println("WASM SDK initialized")
    return wasmSdk, nil
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
        _, err = wasmSdk.CallFunction("validate", tx)
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

func handlePhase3Transaction(msg []byte, mesh3dValidator *mesh3d.Mesh3DValidator, quarantineManager *quarantine.QuarantineManager, consensus *consensus.ProofOfEntanglement, storage *storage.Storage) {
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
        return
    }
    quarantineManager.RecordTransaction("default-submesh", valid)

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

    submeshConfig, err := loadConfig()
    if err != nil {
        logging.Error.Fatalf("Failed to load submesh config: %v", err)
    }

    startWebViewer()

    dynamicManager := submesh.NewDynamicSubmeshManager()

    go submeshCLI(dynamicManager)
    go governanceCLI()

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

    net.SetMessageHandler(func(msg []byte) {
        handlePhase3Transaction(msg, mesh3dValidator, quarantineManager, consensus, storage)
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
