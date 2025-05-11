package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/blackbeardONE/QSDM/internal/logging"
    "github.com/blackbeardONE/QSDM/internal/webviewer"
    "github.com/blackbeardONE/QSDM/pkg/networking"
    "github.com/blackbeardONE/QSDM/pkg/storage"
    "github.com/blackbeardONE/QSDM/pkg/consensus"
    "github.com/blackbeardONE/QSDM/config"
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

    // Set message handler to process incoming transactions
    net.SetMessageHandler(func(msg []byte) {
        // For simplicity, assume msg is the transaction data
        tx := msg
        parentCells := [][]byte{[]byte("parent1"), []byte("parent2")} // TODO: Extract actual parent cells from msg if needed

        // Here, we could add submesh rule checks, e.g., fees or geo tags from submeshConfig
        // For now, just log the submesh name
        logging.Info.Printf("Processing transaction under submesh: %s", submeshConfig.Name)

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
