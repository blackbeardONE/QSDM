package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"sync"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/blackbeardONE/QSDM/cmd/qsdmplus/governancecli"
	"github.com/blackbeardONE/QSDM/cmd/qsdmplus/transaction"
	"github.com/blackbeardONE/QSDM/internal/dashboard"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/internal/webviewer"
	"github.com/blackbeardONE/QSDM/internal/alerting"
	"github.com/blackbeardONE/QSDM/pkg/api"
	"github.com/blackbeardONE/QSDM/pkg/branding"
	"github.com/blackbeardONE/QSDM/pkg/bridge"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/config"
	"github.com/blackbeardONE/QSDM/pkg/consensus"
	"github.com/blackbeardONE/QSDM/pkg/contracts"
	"github.com/blackbeardONE/QSDM/pkg/envcompat"
	"github.com/blackbeardONE/QSDM/pkg/governance"
	"github.com/blackbeardONE/QSDM/pkg/mempool"
	"github.com/blackbeardONE/QSDM/pkg/mesh3d"
	"github.com/blackbeardONE/QSDM/pkg/mining/roleguard"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/networking"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
	"github.com/blackbeardONE/QSDM/pkg/wasm"
	"log"
)

var logger *logging.Logger
var metrics *monitoring.Metrics
var healthChecker *monitoring.HealthChecker

func envPublishMeshCompanion() bool {
	return envcompat.Truthy("QSDM_PUBLISH_MESH_COMPANION", "QSDMPLUS_PUBLISH_MESH_COMPANION")
}

type Storage interface {
	StoreTransaction(tx []byte) error
	Close() error
	GetBalance(address string) (float64, error)
	Ready() error
}

type scyllaStorageAdapter struct {
	*storage.ScyllaStorage
}

func (a *scyllaStorageAdapter) GetBalance(address string) (float64, error) {
	return a.ScyllaStorage.GetBalance(address)
}

func (a *scyllaStorageAdapter) Close() error {
	a.ScyllaStorage.Close()
	return nil
}

func submeshCLI(dynamicManager *submesh.DynamicSubmeshManager, profilePath string) {
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Submesh CLI started. Type 'help' for commands.")
	for {
		fmt.Print("> ")
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Error reading input:", err)
			continue
		}
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		args := strings.Split(input, " ")
		cmd := strings.ToLower(args[0])

		switch cmd {
		case "help":
			fmt.Println("Available commands:")
			fmt.Println("  list                             - List all submeshes")
			fmt.Println("  add <name> <priority> [fee] [geotags] - Add a new submesh with name, priority, optional fee threshold and geotags (comma-separated)")
			fmt.Println("  remove <name>                    - Remove a submesh by name")
			fmt.Println("  update <name> <priority> [fee] [geotags] - Update priority, optional fee threshold and geotags of a submesh")
			fmt.Println("  route <fee> <geotag>             - Show which submesh would route a P2P tx with given fee and geotag")
			fmt.Println("  save                             - Write current submeshes to the configured profile YAML (if path set)")
			fmt.Println("  exit                            - Exit the CLI")
		case "list":
			submeshes := dynamicManager.ListSubmeshes()
			if len(submeshes) == 0 {
				fmt.Println("No submeshes found.")
			} else {
				fmt.Println("Submeshes:")
				for _, sm := range submeshes {
					fmt.Printf("  Name: %s, Priority: %d, FeeThreshold: %.2f, GeoTags: %v\n", sm.Name, sm.PriorityLevel, sm.FeeThreshold, sm.GeoTags)
				}
			}
		case "add":
			if len(args) < 3 {
				fmt.Println("Usage: add <name> <priority> [fee] [geotags]")
				continue
			}
			name := args[1]
			priority, err := strconv.Atoi(args[2])
			if err != nil {
				fmt.Println("Invalid priority:", args[2])
				continue
			}
			feeThreshold := 0.0
			if len(args) >= 4 {
				feeThreshold, err = strconv.ParseFloat(args[3], 64)
				if err != nil {
					fmt.Println("Invalid fee threshold:", args[3])
					continue
				}
			}
			geoTags := []string{}
			if len(args) >= 5 {
				geoTags = strings.Split(args[4], ",")
			}
			ds := &submesh.DynamicSubmesh{
				Name:          name,
				PriorityLevel: priority,
				FeeThreshold:  feeThreshold,
				GeoTags:       geoTags,
			}
			dynamicManager.AddOrUpdateSubmesh(ds)
			fmt.Println("Submesh added or updated:", name)
		case "remove":
			if len(args) < 2 {
				fmt.Println("Usage: remove <name>")
				continue
			}
			name := args[1]
			err := dynamicManager.RemoveSubmesh(name)
			if err != nil {
				fmt.Println("Failed to remove submesh:", err)
			} else {
				fmt.Println("Submesh removed:", name)
			}
		case "route":
			if len(args) < 3 {
				fmt.Println("Usage: route <fee> <geotag>")
				continue
			}
			fee, err := strconv.ParseFloat(args[1], 64)
			if err != nil {
				fmt.Println("Invalid fee:", args[1])
				continue
			}
			tag := args[2]
			ds, err := dynamicManager.RouteTransaction(fee, tag)
			if err != nil {
				fmt.Println("route:", err)
				continue
			}
			if ds == nil {
				fmt.Println("route: no matching submesh (nil)")
			} else {
				fmt.Printf("route: name=%s priority=%d fee_threshold=%.2f geotags=%v\n",
					ds.Name, ds.PriorityLevel, ds.FeeThreshold, ds.GeoTags)
			}
		case "update":
			if len(args) < 3 {
				fmt.Println("Usage: update <name> <priority> [fee] [geotags]")
				continue
			}
			name := args[1]
			priority, err := strconv.Atoi(args[2])
			if err != nil {
				fmt.Println("Invalid priority:", args[2])
				continue
			}
			feeThreshold := 0.0
			if len(args) >= 4 {
				feeThreshold, err = strconv.ParseFloat(args[3], 64)
				if err != nil {
					fmt.Println("Invalid fee threshold:", args[3])
					continue
				}
			}
			geoTags := []string{}
			if len(args) >= 5 {
				geoTags = strings.Split(args[4], ",")
			}
			ds := &submesh.DynamicSubmesh{
				Name:          name,
				PriorityLevel: priority,
				FeeThreshold:  feeThreshold,
				GeoTags:       geoTags,
			}
			dynamicManager.AddOrUpdateSubmesh(ds)
			fmt.Println("Submesh updated:", name)
		case "save":
			if strings.TrimSpace(profilePath) == "" {
				fmt.Println("save: no submesh profile path configured (set submesh profile in main config)")
				continue
			}
			if err := submesh.SaveProfilesToPath(dynamicManager, profilePath); err != nil {
				fmt.Println("save failed:", err)
			} else {
				fmt.Println("saved submesh profile to", profilePath)
			}
		case "exit":
			fmt.Println("Exiting Submesh CLI.")
			return
		default:
			fmt.Println("Unknown command. Type 'help' for commands.")
		}
	}
}

// SetupNetwork wires a libp2p host bound to the configured TCP port so ufw rules
// and peer dial strings stay stable across restarts. Pass port=0 for ephemeral.
func SetupNetwork(ctx context.Context, logger *logging.Logger, port int) (*networking.Network, error) {
	return networking.SetupLibP2PWithPort(ctx, logger, port)
}

func HandleTransaction(logger *logging.Logger, msg []byte, dynamicManager *submesh.DynamicSubmeshManager, wasmSdk *wasm.WASMSDK, consensus *consensus.ProofOfEntanglement, storage Storage, nvidiaP2PGate *monitoring.NvidiaLockP2PGate) {
	transaction.HandleTransaction(logger, msg, dynamicManager, wasmSdk, consensus, storage, nvidiaP2PGate)
}

func HandlePhase3Transaction(logger *logging.Logger, msg []byte, mesh3dValidator *mesh3d.Mesh3DValidator, quarantineManager *quarantine.QuarantineManager, reputationManager *quarantine.ReputationManager, consensus *consensus.ProofOfEntanglement, storage Storage, nvidiaP2PGate *monitoring.NvidiaLockP2PGate) {
	transaction.HandlePhase3Transaction(logger, msg, mesh3dValidator, quarantineManager, reputationManager, consensus, storage, nvidiaP2PGate)
}

func main() {
	// Global panic handler to catch any crashes during initialization
	// This catches panics that occur before we can set up proper error handling
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "\n\nFATAL ERROR: Application panic during initialization\n")
			fmt.Fprintf(os.Stderr, "Error: %v\n", r)
			fmt.Fprintf(os.Stderr, "\nThis may be caused by:\n")
			fmt.Fprintf(os.Stderr, "  - Missing OpenSSL DLLs (libcrypto-3-x64.dll, libssl-3-x64.dll)\n")
			fmt.Fprintf(os.Stderr, "    Even if liboqs is statically linked, it depends on OpenSSL!\n")
			fmt.Fprintf(os.Stderr, "  - Missing CUDA DLLs (cudart64_*.dll)\n")
			fmt.Fprintf(os.Stderr, "  - Missing liboqs DLL (if dynamically linked)\n")
			fmt.Fprintf(os.Stderr, "  - CGO initialization failure\n")
			fmt.Fprintf(os.Stderr, "  - Stack overflow\n")
			fmt.Fprintf(os.Stderr, "\nSolutions:\n")
			fmt.Fprintf(os.Stderr, "  1. Ensure OpenSSL DLLs are in PATH or executable directory\n")
			fmt.Fprintf(os.Stderr, "  2. Run: .\run.ps1 (sets up PATH correctly)\n")
			fmt.Fprintf(os.Stderr, "  3. Check Event Viewer: Windows Logs > Application\n")
			os.Stderr.Sync()
			os.Exit(1)
		}
	}()

	// Early console output to verify the application starts
	// Use os.Stdout directly and flush to ensure output appears immediately
	fmt.Fprintln(os.Stdout, branding.LogPrefix+"Starting application...")
	os.Stdout.Sync()
	fmt.Fprintln(os.Stdout, branding.LogPrefix+"Loading configuration...")
	os.Stdout.Sync()

	// Load configuration
	cfg, err := config.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: Failed to load configuration: %v\n", err)
		os.Stderr.Sync()
		log.Fatalf("Failed to load configuration: %v", err)
	}

	fmt.Fprintf(os.Stdout, "%sConfiguration loaded successfully\n", branding.LogPrefix)
	os.Stdout.Sync()
	fmt.Fprintf(os.Stdout, "%sLog file: %s\n", branding.LogPrefix, cfg.LogFile)
	os.Stdout.Sync()

	// Major Update Phase 2.3 startup guard: refuse to start if the
	// (node_role, mining_enabled) pair is inconsistent with either the
	// configuration rules or the compile-time build profile. The guard must
	// run BEFORE any listeners (HTTP, P2P, dashboard) open so misconfigured
	// nodes do not advertise themselves on the network.
	if err := roleguard.MustMatchRole(cfg.NodeRole, cfg.MiningEnabled); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: startup role guard rejected configuration: %v\n", err)
		os.Stderr.Sync()
		log.Fatalf("startup role guard: %v", err)
	}
	fmt.Fprintf(os.Stdout, "%sNode role: %s (build profile: %s, mining_enabled=%v)\n",
		branding.LogPrefix, cfg.NodeRole.String(), roleguard.BuildProfile, cfg.MiningEnabled)
	os.Stdout.Sync()

	logger = logging.NewLoggerWithLevel(cfg.LogFile, true, cfg.LogLevel)
	logger.Info(branding.FullTitle()+" node starting up...", "config", "loaded", "log_level", cfg.LogLevel)
	fmt.Fprintln(os.Stdout, branding.LogPrefix+"Logger initialized")
	os.Stdout.Sync()

	// One AuthManager for API + dashboard: each NewAuthManager() generates a new ML-DSA keypair, so separate instances cannot verify each other's JWTs.
	var sharedAuth *api.AuthManager
	if sam, err := api.NewAuthManager(); err != nil {
		logger.Warn("Failed to initialize shared auth manager", "error", err)
	} else {
		sam.SetJWTHMACFallbackSecret(cfg.JWTHMACSecret)
		sharedAuth = sam
		logger.Info("Shared JWT auth manager initialized for API and dashboard")
	}

	// Configure alerting webhook (env QSDM_ALERT_WEBHOOK or config)
	if cfg.AlertWebhookURL != "" {
		alerting.SetWebhookURL(cfg.AlertWebhookURL)
		logger.Info("Alerting webhook configured", "url", cfg.AlertWebhookURL)
	}

	// Initialize monitoring
	metrics = monitoring.GetMetrics()
	healthChecker = monitoring.NewHealthChecker(metrics)

	// Register components for health monitoring
	healthChecker.RegisterComponent("network")
	healthChecker.RegisterComponent("storage")
	healthChecker.RegisterComponent("consensus")
	healthChecker.RegisterComponent("governance")
	healthChecker.RegisterComponent("wallet")
	healthChecker.RegisterComponent("dashboard")

	// Start periodic health checks
	go func() {
		ticker := time.NewTicker(cfg.HealthCheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				healthChecker.CheckHealth()
			}
		}
	}()

	// Create dashboard instance (will be started in goroutine)
	nonceTTL := int64(cfg.NvidiaLockIngestNonceTTL.Seconds())
	if nonceTTL <= 0 && cfg.NvidiaLockRequireIngestNonce {
		nonceTTL = int64((10 * time.Minute).Seconds())
	}
	dash := dashboard.NewDashboard(metrics, healthChecker, fmt.Sprintf("%d", cfg.DashboardPort), cfg.NGCIngestSecret != "", dashboard.DashboardNvidiaLock{
		Enabled:               cfg.NvidiaLockEnabled,
		MaxProofAge:           cfg.NvidiaLockMaxProofAge,
		ExpectedNodeID:        cfg.NvidiaLockExpectedNodeID,
		ProofHMACSecret:       cfg.NvidiaLockProofHMACSecret,
		RequireIngestNonce:    cfg.NvidiaLockRequireIngestNonce,
		IngestNonceTTLSeconds: nonceTTL,
		GateP2P:               cfg.NvidiaLockGateP2P,
	}, cfg.JWTHMACSecret, cfg.DashboardMetricsScrapeSecret, cfg.DashboardStrictAuth, fmt.Sprintf("http://127.0.0.1:%d", cfg.APIPort), sharedAuth)

	if err := webviewer.StartWebLogViewer(cfg.LogFile, fmt.Sprintf("%d", cfg.LogViewerPort)); err != nil {
		logger.Warn("Web log viewer disabled",
			"port", cfg.LogViewerPort,
			"reason", err.Error(),
			"fix", "set WEBVIEWER_USERNAME and WEBVIEWER_PASSWORD env vars (or QSDM_WEBVIEWER_ALLOW_DEFAULT_CREDS=1 for local dev only)",
		)
	}

	dynamicManager := submesh.NewDynamicSubmeshManager()
	if rp := cfg.ResolvedSubmeshConfigPath(); rp != "" {
		loaded, err := submesh.ApplyProfilesFromFile(dynamicManager, rp)
		if err != nil {
			log.Fatalf("Failed to load submesh profile %q: %v", rp, err)
		}
		logger.Info("Loaded submesh profiles", "path", rp, "count", len(loaded))
	}
	// Check DISABLE_CLI environment variable first (for Docker/containerized environments)
	disableCLI := os.Getenv("DISABLE_CLI") == "true" || os.Getenv("DISABLE_CLI") == "1"
	// /dev/null is a character device but not a TTY; use a real terminal check (see golang.org/x/term).
	stdinInteractive := term.IsTerminal(int(os.Stdin.Fd()))

	// Only start submesh CLI when attached to a real TTY and CLI is not disabled
	if !disableCLI {
		if stdinInteractive {
			go submeshCLI(dynamicManager, cfg.ResolvedSubmeshConfigPath())
		} else {
			logger.Info("Submesh CLI disabled (non-interactive mode)")
		}
	} else {
		logger.Info("Submesh CLI disabled (DISABLE_CLI env set)")
	}

	governanceManager := governance.NewSnapshotVoting(cfg.ProposalFile)
	healthChecker.UpdateComponentHealth("governance", monitoring.HealthStatusHealthy, "Governance system initialized")
	if !disableCLI {
		if stdinInteractive {
			go governancecli.GovernanceCLI(governanceManager)
		} else {
			logger.Info("Governance CLI disabled (non-interactive mode)")
		}
	} else {
		logger.Info("Governance CLI disabled (DISABLE_CLI env set)")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	net, err := SetupNetwork(ctx, logger, cfg.NetworkPort)
	if err != nil {
		logger.Error("Failed to setup libp2p", "error", err)
		metrics.RecordError("Network setup failed: " + err.Error())
		healthChecker.UpdateComponentHealth("network", monitoring.HealthStatusUnhealthy, err.Error())
		log.Fatalf("Failed to setup libp2p: %v", err)
	}
	healthChecker.UpdateComponentHealth("network", monitoring.HealthStatusHealthy, "Network initialized")

	// Start DHT-based bootstrap discovery for WAN peer finding
	if len(cfg.BootstrapPeers) > 0 || true { // always start DHT; IPFS defaults used if no custom peers
		bsCfg := networking.BootstrapConfig{
			BootstrapPeers: cfg.BootstrapPeers,
		}
		bsDisc, bsErr := networking.NewBootstrapDiscovery(ctx, net.Host, bsCfg, logger)
		if bsErr != nil {
			logger.Warn("DHT bootstrap discovery failed to start", "error", bsErr)
		} else {
			logger.Info("DHT bootstrap discovery started",
				"bootstrap_peers", len(cfg.BootstrapPeers),
			)
			defer bsDisc.Close()
		}
	}

	// Initialize storage backend
	fmt.Fprintln(os.Stdout, branding.LogPrefix+"Initializing storage...")
	os.Stdout.Sync()
	var storageBackend Storage
	if cfg.UseScylla() {
		scyllaExtra := storage.ScyllaClusterConfigFromAuthTLS(
			cfg.ScyllaUsername, cfg.ScyllaPassword,
			cfg.ScyllaTLSCaPath, cfg.ScyllaTLSCertPath, cfg.ScyllaTLSKeyPath,
			cfg.ScyllaTLSInsecureSkipVerify,
		)
		scyllaStorage, err := storage.NewScyllaStorage(cfg.ScyllaHosts, cfg.ScyllaKeyspace, scyllaExtra)
		if err != nil {
			logger.Error("Failed to initialize ScyllaDB storage", "error", err)
			logger.Warn("Falling back to SQLite storage")
			sqliteStorage, err := storage.NewStorage(cfg.SQLitePath)
			if err != nil {
				logger.Error("Failed to initialize SQLite storage", "error", err)
				logger.Warn("Falling back to file storage (SQLite requires CGO)")
				fileStorage, fileErr := storage.NewFileStorage("storage")
				if fileErr != nil {
					log.Fatalf("Failed to initialize any storage backend: SQLite=%v, File=%v", err, fileErr)
				}
				logger.Info("Using file storage (SQLite not available without CGO)")
				storageBackend = fileStorage
				healthChecker.UpdateComponentHealth("storage", monitoring.HealthStatusHealthy, "File-based storage initialized")
			} else {
				logger.Info("Using SQLite storage")
				storageBackend = sqliteStorage
				healthChecker.UpdateComponentHealth("storage", monitoring.HealthStatusHealthy, "SQLite storage initialized")
			}
		} else {
			logger.Info("Using ScyllaDB storage")
			storageBackend = &scyllaStorageAdapter{scyllaStorage}
			healthChecker.UpdateComponentHealth("storage", monitoring.HealthStatusHealthy, "ScyllaDB storage initialized")
		}
	} else {
		sqliteStorage, err := storage.NewStorage(cfg.SQLitePath)
		if err != nil {
			logger.Warn("Failed to initialize SQLite storage", "error", err)
			logger.Info("SQLite requires CGO. Falling back to file storage for non-CGO builds")
			fileStorage, fileErr := storage.NewFileStorage("storage")
			if fileErr != nil {
				log.Fatalf("Failed to initialize storage: SQLite=%v, File=%v", err, fileErr)
			}
			logger.Info("Using file storage (SQLite not available without CGO)")
			storageBackend = fileStorage
			healthChecker.UpdateComponentHealth("storage", monitoring.HealthStatusHealthy, "File-based storage initialized")
		} else {
			logger.Info("Using SQLite storage")
			storageBackend = sqliteStorage
			healthChecker.UpdateComponentHealth("storage", monitoring.HealthStatusHealthy, "SQLite storage initialized")
		}
	}
	fmt.Fprintln(os.Stdout, branding.LogPrefix+"Storage initialized")
	os.Stdout.Sync()
	defer storageBackend.Close()

	// Initialize consensus with error handling
	fmt.Fprintln(os.Stdout, branding.LogPrefix+"Initializing consensus (quantum-safe)...")
	os.Stdout.Sync()

	var poe *consensus.ProofOfEntanglement
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("Panic during consensus initialization", "error", r)
				fmt.Fprintf(os.Stderr, "ERROR: Consensus initialization panic: %v\n", r)
				fmt.Fprintf(os.Stderr, "This may indicate:\n")
				fmt.Fprintf(os.Stderr, "  - Missing liboqs.dll (check executable directory or PATH)\n")
				fmt.Fprintf(os.Stderr, "  - Missing OpenSSL DLLs (libcrypto, libssl)\n")
				fmt.Fprintf(os.Stderr, "  - CGO initialization failure\n")
				os.Stderr.Sync()
			}
		}()
		fmt.Fprintln(os.Stdout, branding.LogPrefix+"Creating Proof-of-Entanglement instance...")
		os.Stdout.Sync()
		poe = consensus.NewProofOfEntanglement()
		if poe == nil {
			fmt.Fprintln(os.Stdout, branding.LogPrefix+"Proof-of-Entanglement returned nil (CGO/liboqs may not be available)")
			os.Stdout.Sync()
		} else {
			fmt.Fprintln(os.Stdout, branding.LogPrefix+"Proof-of-Entanglement created successfully")
			os.Stdout.Sync()
		}
	}()

	if poe == nil {
		logger.Warn("Consensus not available",
			"reason", "Quantum-safe cryptography (liboqs) initialization failed",
			"impact", "Transactions accepted without signature verification",
			"note", "Check if liboqs DLLs are available and OpenSSL is in PATH")
		healthChecker.UpdateComponentHealth("consensus", monitoring.HealthStatusDegraded,
			"liboqs initialization failed - Quantum-safe signature verification unavailable. Node accepts transactions without signature verification. This is expected if liboqs/OpenSSL DLLs are not properly configured.")
		fmt.Fprintf(os.Stderr, "WARNING: Consensus degraded - CGO and liboqs required for quantum-safe consensus\n")
		fmt.Fprintf(os.Stderr, "Check that liboqs.dll is in PATH or executable directory\n")
		os.Stderr.Sync()
	} else {
		logger.Info("Consensus initialized successfully", "type", "Proof-of-Entanglement")
		healthChecker.UpdateComponentHealth("consensus", monitoring.HealthStatusHealthy, "Proof-of-Entanglement initialized with quantum-safe cryptography")
		fmt.Fprintln(os.Stdout, branding.LogPrefix+"Consensus initialized (quantum-safe)")
		os.Stdout.Sync()
	}
	consensus := poe

	// Load wallet WASM module (optional, requires CGO and wasmtime DLLs)
	// Disabled when wasmtime DLLs are not available to avoid crashes
	walletWasmPath := "wasm_modules/wallet/wallet.wasm"
	walletBytes, err := wasm.LoadWASMFromFile(walletWasmPath)
	var walletSdk *wasm.WASMSDK
	if err != nil {
		logger.Warn("Failed to load wallet WASM module", "path", walletWasmPath, "error", err)
		log.Println("WASM wallet module disabled due to missing WASM file")
	} else {
		// Try to create WASM SDK - this will fail gracefully if wasmtime DLLs are missing
		walletSdk, err = wasm.NewWASMSDK(walletBytes)
		if err != nil {
			logger.Warn("Failed to create WASM SDK for wallet", "error", err)
			log.Printf("WASM wallet SDK disabled: %v (this is normal if wasmtime DLLs are missing)", err)
			walletSdk = nil
		} else {
			logger.Info("WASM wallet SDK initialized")
		}
	}

	// Load validator WASM module (optional, requires CGO)
	validatorWasmPath := "wasm_modules/validator/validator.wasm"
	_, err = wasm.LoadWASMFromFile(validatorWasmPath)
	if err != nil {
		logger.Warn("Failed to load validator WASM module", "path", validatorWasmPath, "error", err)
		log.Println("WASM validator module disabled due to missing WASM file")
	} else {
		logger.Info("WASM validator module loaded")
	}

	// Initialize contracts engine (uses WASM SDK for execution)
	contractEngine := contracts.NewContractEngine(walletSdk)
	if walletSdk != nil {
		logger.Info("Contract engine initialized with wasmer WASM execution")
	} else {
		logger.Warn("Wasmer SDK not available; trying wazero (pure-Go) runtime")
	}

	// Try wazero as a pure-Go WASM runtime (no CGO or DLLs needed)
	if walletSdk == nil {
		if len(walletBytes) > 0 {
			wrt, wrtErr := wasm.NewWazeroRuntime(walletBytes)
			if wrtErr != nil {
				logger.Warn("Wazero runtime failed to load wallet WASM", "error", wrtErr)
			} else {
				contractEngine.SetWazeroRuntime(wrt)
				logger.Info("Contract engine using wazero (pure-Go) WASM runtime")
			}
		} else {
			wrt, _ := wasm.NewWazeroRuntime(nil)
			if wrt != nil {
				contractEngine.SetWazeroRuntime(wrt)
				logger.Info("Wazero runtime ready (no WASM module loaded yet — contracts will use simulation)")
			}
		}
	}
	_ = contractEngine

	// Initialize cross-chain bridge (atomic swap + lock/unlock protocol)
	bridgeProto, bridgeErr := bridge.NewBridgeProtocol()
	if bridgeErr != nil {
		logger.Warn("Bridge protocol not available", "error", bridgeErr)
	} else {
		logger.Info("Cross-chain bridge protocol initialized")
	}
	_ = bridgeProto

	atomicSwap, swapErr := bridge.NewAtomicSwapProtocol()
	if swapErr != nil {
		logger.Warn("Atomic swap protocol not available", "error", swapErr)
	} else {
		logger.Info("Atomic swap protocol initialized")
	}
	// Derive state directory from SQLite path
	stateDir := filepath.Dir(cfg.SQLitePath)
	bridgeStatePath := filepath.Join(stateDir, "qsdmplus_bridge_state.json")
	tokenRegistryPath := filepath.Join(stateDir, "qsdmplus_tokens.json")
	stakingPath := filepath.Join(stateDir, "qsdmplus_staking.json")
	// User store persistence: fall back to <stateDir>/qsdmplus_users.json
	// when nothing was set explicitly (config file or env). This matches
	// the sibling staking/bridge JSON files and keeps all ledger-local
	// state under /opt/qsdmplus on the default systemd layout.
	if strings.TrimSpace(cfg.UserStorePath) == "" {
		cfg.UserStorePath = filepath.Join(stateDir, "qsdmplus_users.json")
	}

	tracePath := filepath.Join(stateDir, "contract_traces.ndjson")
	contractEngine.Tracer().ConfigureRetention(tracePath, 7*24*time.Hour)
	contractEngine.Tracer().StartTraceCompactionLoop(ctx, 1*time.Hour, 16<<20)

	// Restore bridge/swap state from previous run
	if bridgeProto != nil || atomicSwap != nil {
		lc, sc, loadErr := bridge.LoadState(bridgeStatePath, bridgeProto, atomicSwap)
		if loadErr != nil {
			logger.Warn("Failed to load bridge state", "error", loadErr)
		} else if lc > 0 || sc > 0 {
			logger.Info("Restored bridge state from disk", "locks", lc, "swaps", sc)
		}
	}

	// Start bridge auto-saver (flushes every 30 s and on shutdown)
	var bridgeAutoSaver *bridge.AutoSaver
	if bridgeProto != nil || atomicSwap != nil {
		bridgeAutoSaver = bridge.NewAutoSaver(bridgeStatePath, bridgeProto, atomicSwap, 30*time.Second)
		logger.Info("Bridge state auto-saver started", "path", bridgeStatePath)
	}
	_ = bridgeAutoSaver

	// Bridge P2P relay — propagate lock/swap events across the network
	var bridgeRelay *bridge.P2PRelay
	if bridgeProto != nil || atomicSwap != nil {
		var relayErr error
		bridgeRelay, relayErr = bridge.NewP2PRelay(net, bridgeProto, atomicSwap, net.Host.ID().String())
		if relayErr != nil {
			logger.Warn("Bridge P2P relay not available", "error", relayErr)
		} else {
			logger.Info("Bridge P2P relay started on topic " + bridge.BridgeTopicName)
		}
	}
	_ = bridgeRelay

	nodeValidatorSet := chain.NewValidatorSet(chain.DefaultValidatorSetConfig())
	minValStake := chain.DefaultValidatorSetConfig().MinStake
	if err := nodeValidatorSet.Register("bootstrap", minValStake); err != nil {
		logger.Warn("Bootstrap validator registration", "error", err)
	}
	nodeEvidenceManager := chain.NewEvidenceManager(nodeValidatorSet)

	// Declare phase 3 components before use
	// Initialize Mesh3D validator (may fail if CUDA/liboqs DLLs missing, but won't crash)
	fmt.Fprintln(os.Stdout, branding.LogPrefix+"Initializing 3D mesh validator...")
	os.Stdout.Sync()

	var mesh3dValidator *mesh3d.Mesh3DValidator
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("Panic during Mesh3D validator initialization", "error", r)
				fmt.Fprintf(os.Stderr, "ERROR: Mesh3D validator initialization panic: %v\n", r)
				fmt.Fprintf(os.Stderr, "This may indicate missing CUDA or liboqs DLLs\n")
				os.Stderr.Sync()
			}
		}()
		mesh3dValidator = mesh3d.NewMesh3DValidator()
		if mesh3dValidator != nil {
			fmt.Fprintln(os.Stdout, branding.LogPrefix+"3D mesh validator initialized")
			os.Stdout.Sync()
		} else {
			fmt.Fprintln(os.Stdout, branding.LogPrefix+"3D mesh validator initialization returned nil")
			os.Stdout.Sync()
		}
	}()

	if mesh3dValidator == nil {
		logger.Warn("Mesh3D validator not available",
			"reason", "Initialization failed (CUDA/liboqs may be unavailable)",
			"impact", "3D mesh validation will be limited")
		// Try to create a minimal validator - NewMesh3DValidator should never return nil
		// but if it does, we'll handle it gracefully
		fmt.Fprintln(os.Stdout, branding.LogPrefix+"Warning - Mesh3D validator is nil, attempting to create minimal validator")
		os.Stdout.Sync()
		// Try one more time with explicit error handling
		mesh3dValidator = mesh3d.NewMesh3DValidator()
		if mesh3dValidator == nil {
			logger.Error("Mesh3D validator creation failed completely",
				"note", "Phase 3 validation may not work properly")
			fmt.Fprintf(os.Stderr, "ERROR: Cannot create Mesh3D validator - Phase 3 features disabled\n")
			os.Stderr.Sync()
		}
	}
	quarantineManager := quarantine.NewQuarantineManager(0.5) // 0.5 threshold for quarantine
	reputationManager := quarantine.NewReputationManager(10, 5)
	monitor := quarantine.NewMonitor(quarantineManager, logger, 30*time.Second)
	monitor.Start()

	// Initialize wallet service for creating transactions
	walletService, err := wallet.NewWalletService()
	if err != nil {
		logger.Warn("Failed to initialize wallet service", "error", err)
		logger.Info("Node will operate in receive-only mode")
		healthChecker.UpdateComponentHealth("wallet", monitoring.HealthStatusDegraded,
			"Wallet service unavailable: "+err.Error()+". Node operating in receive-only mode. This is expected if liboqs/quantum-safe crypto is not available.")
	} else {
		logger.Info("Wallet service initialized", "address", walletService.GetAddress(), "balance", walletService.GetBalance())
		healthChecker.UpdateComponentHealth("wallet", monitoring.HealthStatusHealthy, "Wallet service initialized")
		// Initialize wallet balance in storage if needed
		// Check if storage implements balance methods (SQLite storage does)
		if walletService != nil {
			if balanceStorage, ok := storageBackend.(interface {
				GetBalance(address string) (float64, error)
				SetBalance(address string, balance float64) error
			}); ok {
				currentBalance, _ := balanceStorage.GetBalance(walletService.GetAddress())
				if currentBalance == 0 {
					// Set initial balance for new wallet
					initialBalance := float64(walletService.GetBalance())
					if err := balanceStorage.SetBalance(walletService.GetAddress(), initialBalance); err != nil {
						logger.Warn("Failed to set initial balance", "error", err)
					} else {
						logger.Info("Initial wallet balance set in storage", "balance", initialBalance)
					}
				}
			}
		}
	}

	if walletService != nil {
		if err := nodeValidatorSet.Register(walletService.GetAddress(), minValStake); err != nil {
			logger.Warn("Wallet validator registration", "error", err)
		}
	}

	nodeTxRep := networking.NewReputationTracker(networking.DefaultReputationConfig())
	nodeEvidenceRep := networking.NewReputationTracker(networking.ReputationConfigForEvidence())

	var evidenceRelay *networking.EvidenceP2PRelay
	evIng := networking.NewEvidenceGossipIngress(nodeEvidenceManager, nodeEvidenceRep, networking.DefaultEvidenceGossipConfig())
	if er, evErr := networking.NewEvidenceP2PRelay(net, evIng, net.Host.ID().String()); evErr != nil {
		logger.Warn("Evidence P2P relay not started", "error", evErr)
	} else {
		evidenceRelay = er
		logger.Info("Evidence P2P relay started", "topic", networking.EvidenceTopicName)
	}

	liveBFT := chain.NewBFTConsensus(nodeValidatorSet, chain.DefaultConsensusConfig())
	bftExec := chain.NewBFTExecutor(liveBFT)
	bftIngress := networking.NewBFTGossipIngress(networking.DefaultBFTGossipConfig(), bftExec)
	bftIngress.SetReputationTracker(nodeTxRep)
	bftExec.SetEvidenceManager(nodeEvidenceManager)
	var bftRelay *networking.BFTP2PRelay
	if br, bftErr := networking.NewBFTP2PRelay(net, bftIngress, net.Host.ID().String()); bftErr != nil {
		logger.Warn("BFT gossip relay not started", "error", bftErr)
	} else {
		bftRelay = br
		bftExec.SetPublisher(bftRelay.PublishRaw)
		logger.Info("BFT gossip relay started", "topic", networking.BFTTopicName)
	}
	polFollower := chain.NewPolFollower(nodeValidatorSet, chain.DefaultConsensusConfig().QuorumFraction)
	polIngress := networking.NewPolGossipIngress(networking.DefaultPolGossipConfig(), polFollower)
	var polRelay *networking.PolP2PRelay
	if pr, polErr := networking.NewPolP2PRelay(net, polIngress, net.Host.ID().String()); polErr != nil {
		logger.Warn("POL gossip relay not started", "error", polErr)
	} else {
		polRelay = pr
		logger.Info("POL gossip relay started", "topic", networking.PolTopicName)
	}

	adminAccounts := chain.NewAccountStore()
	adminPool := mempool.New(mempool.DefaultConfig())
	adminFinality := chain.NewFinalityGadget(chain.DefaultFinalityConfig())
	polFollower.SetAnchorFinality(true)
	adminFinality.SetPolFollower(polFollower)
	adminReceipts := chain.NewReceiptStore()
	prodCfg := chain.DefaultProducerConfig()
	prodCfg.ProducerID = net.Host.ID().String()
	stakingLedger, stakeErr := chain.LoadOrNewStakingLedger(stakingPath)
	if stakeErr != nil {
		logger.Warn("Failed to load staking ledger; using new ledger", "error", stakeErr, "path", stakingPath)
		stakingLedger = chain.NewStakingLedger()
	}
	stakingLedger.SetPersistPath(stakingPath)
	nodeEvidenceManager.SetStakingLedger(stakingLedger)
	polTipSnap := struct {
		mu        sync.RWMutex
		height    uint64
		stateRoot string
		ok        bool
	}{}
	adminProducer := chain.NewBlockProducer(adminPool, adminAccounts, prodCfg)
	adminProducer.SetAppendReceiptStore(adminReceipts)
	adminProducer.SetPolFollower(polFollower)
	adminProducer.SetBFTSealGate(liveBFT)
	adminProducer.SetPreSealBFTRound(func(blk *chain.Block) error {
		return chain.RunSyntheticBFTRoundWithExecutor(bftExec, nodeValidatorSet, blk)
	})
	adminPool.SetAdmissionChecker(func(_ *mempool.Tx) error {
		polTipSnap.mu.RLock()
		h, sr, ok := polTipSnap.height, polTipSnap.stateRoot, polTipSnap.ok
		polTipSnap.mu.RUnlock()
		if !ok {
			return nil
		}
		if polFollower != nil && polFollower.AnchorFinalityEnabled() {
			if !polFollower.CanExtendFromTip(h, sr) {
				return chain.ErrPolExtensionBlocked
			}
		}
		if liveBFT != nil && !liveBFT.IsCommitted(h) {
			return chain.ErrBFTExtensionBlocked
		}
		return nil
	})
	adminProducer.OnSealed = func() {
		if blk, ok := adminProducer.LatestBlock(); ok {
			polTipSnap.mu.Lock()
			polTipSnap.height = blk.Height
			polTipSnap.stateRoot = blk.StateRoot
			polTipSnap.ok = true
			polTipSnap.mu.Unlock()
			stakingLedger.ProcessCommittedHeight(adminAccounts, blk.Height, blk.StateRoot)
			networking.PublishPolAfterBlockSeal(logger, polRelay, polFollower, bftExec, liveBFT, nodeValidatorSet, blk)
			bftExec.PrunePendingHeight(blk.Height)
			adminFinality.TrackBlockWithMeta(blk.Height, blk.Hash, blk.StateRoot)
			adminFinality.UpdateTip(blk.Height)
		}
		chain.SyncValidatorStakesFromCommittedTip(nodeValidatorSet, adminAccounts, adminProducer, stakingLedger)
	}
	bftExec.SetOnCommitted(func(height uint64, round uint32, blockHash string) {
		defer bftExec.ClearLastInboundBFTGossipPeer()
		logger.Info("BFT committed height", "height", height, "round", round, "block_hash", blockHash)
		if blk, ok := bftExec.PendingBlock(height, blockHash); ok {
			err := adminProducer.TryAppendExternalBlock(blk)
			bftExec.NoteFollowerAppend(err)
			if err != nil {
				var ace *chain.ExternalAppendConflictError
				if errors.As(err, &ace) {
					relayPeer, _ := bftExec.PendingProposeSource(height, blockHash)
					if relayPeer == "" {
						relayPeer = bftExec.LastInboundBFTGossipPeer()
					}
					details := fmt.Sprintf(
						"TryAppendExternalBlock conflict after BFT commit height=%d round=%d vote_value=%q",
						ace.Height, round, blockHash,
					)
					if relayPeer != "" {
						details += fmt.Sprintf(" pending_propose_relay_peer=%q", relayPeer)
					}
					nodeEvidenceManager.SubmitEvidenceBestEffort(chain.ConsensusEvidence{
						Type:        chain.EvidenceForkWitness,
						Height:      ace.Height,
						Round:       round,
						BlockHashes: []string{ace.ExistingHash, ace.NewHash},
						Details:     details,
						Timestamp:   time.Now(),
					})
					if relayPeer != "" {
						nodeTxRep.RecordEvent(relayPeer, networking.EventInvalidBlock, 0)
					}
					bftExec.ClearPendingProposeSource(height, blockHash)
				}
				logger.Debug("BFT follower append skipped", "height", height, "error", err)
			} else {
				bftExec.PrunePendingHeight(height)
			}
		}
		if height > 128 {
			bftExec.PrunePendingBelow(height - 64)
		}
	})
	if walletService != nil {
		adminAccounts.Credit(walletService.GetAddress(), float64(walletService.GetBalance()))
	}
	chain.SyncValidatorStakesFromCommittedTip(nodeValidatorSet, adminAccounts, adminProducer, stakingLedger)

	txGossipIng := networking.NewTxGossipIngress(
		chain.NewGossipValidator(chain.NewSigVerifier(), chain.NewTxValidator(adminAccounts), chain.DefaultGossipValidationConfig()),
		adminPool,
		nodeTxRep,
	)
	txGossipRelay := networking.NewTxGossipRelay(net.Broadcast, networking.DefaultTxGossipRelayConfig())
	txGossipIng.SetTxGossipRelay(txGossipRelay)
	net.SetTxGossipIngress(txGossipIng)
	monitoring.SetScrapeProcessIdentity(net.Host.ID().String())
	auditSecret := cfg.JWTHMACSecret
	if auditSecret == "" {
		auditSecret = "qsdmplus-admin-audit-default"
	}
	var adminHot *config.HotReloader
	if cfg.ConfigFileUsed != "" {
		if hr, hrErr := config.NewHotReloader(config.HotReloadConfig{FilePath: cfg.ConfigFileUsed, PollInterval: 30 * time.Second}, cfg); hrErr != nil {
			logger.Warn("Admin hot reloader not attached", "error", hrErr)
		} else {
			adminHot = hr
		}
	}

	// Dashboard topology + WS metrics share the same libp2p network and node subsystems as the API admin view.
	if dash != nil {
		dash.SetNetwork(net)
		logger.Info("Network topology monitoring enabled in dashboard")
		pe := monitoring.GlobalScrapePrometheusExporter()
		pe.RegisterCollector("node_chain", monitoring.ChainCollector(
			adminProducer.ChainHeight,
			func() int { return len(nodeValidatorSet.ActiveValidators()) },
		))
		pe.RegisterCollector("node_mempool", monitoring.MempoolCollector(
			func() int { return adminPool.Size() },
			func() map[string]interface{} { return adminPool.Stats() },
		))
		pe.RegisterCollector("node_bft_gossip", func() []monitoring.Metric {
			s := bftIngress.Stats()
			return []monitoring.Metric{
				{Name: "qsdm_bft_gossip_ingress_ok_total", Help: "BFT gossip messages accepted (dedupe passed, apply ok or no executor)", Type: monitoring.MetricCounter, Value: float64(s.IngressOK)},
				{Name: "qsdm_bft_gossip_dedupe_drops_total", Help: "BFT gossip duplicate payloads dropped", Type: monitoring.MetricCounter, Value: float64(s.DedupeDropped)},
				{Name: "qsdm_bft_gossip_rate_limited_total", Help: "BFT gossip messages rejected by per-peer rate limit", Type: monitoring.MetricCounter, Value: float64(s.RateLimited)},
				{Name: "qsdm_bft_gossip_rejected_wire_total", Help: "BFT gossip wire rejects (decode / empty / unknown kind)", Type: monitoring.MetricCounter, Value: float64(s.RejectedWire)},
				{Name: "qsdm_bft_gossip_apply_errors_total", Help: "BFT gossip executor apply errors after validation", Type: monitoring.MetricCounter, Value: float64(s.ApplyErrors)},
			}
		})
		pe.RegisterCollector("node_bft_follower", func() []monitoring.Metric {
			ok, sk, cx := bftExec.FollowerAppendStats()
			return []monitoring.Metric{
				{Name: "qsdm_bft_follower_append_ok_total", Help: "Successful TryAppendExternalBlock calls after BFT commit", Type: monitoring.MetricCounter, Value: float64(ok)},
				{Name: "qsdm_bft_follower_append_skip_total", Help: "Failed TryAppendExternalBlock calls excluding hash conflicts", Type: monitoring.MetricCounter, Value: float64(sk)},
				{Name: "qsdm_bft_follower_append_conflict_total", Help: "TryAppendExternalBlock hash conflicts at same height", Type: monitoring.MetricCounter, Value: float64(cx)},
			}
		})
		dash.SetRealtimeMetricsSource(dashboard.MetricsSource{
			Prometheus: pe,
			Accounts:   adminAccounts,
			Validators: nodeValidatorSet,
			Finality:   adminFinality,
			Mempool:    adminPool,
			Receipts:   adminReceipts,
			Peers:      nodeTxRep,
			Producer:   adminProducer,
		})
		go func() {
			logger.Info("Starting dashboard server", "port", cfg.DashboardPort)
			if err := dash.Start(); err != nil {
				logger.Error("Dashboard server failed", "error", err)
				log.Printf("CRITICAL: Dashboard server error: %v", err)
				log.Printf("Dashboard will not be available. Check if port %d is in use.", cfg.DashboardPort)
			}
		}()
		time.Sleep(2 * time.Second)
		client := &http.Client{Timeout: 2 * time.Second}
		resp, derr := client.Get(fmt.Sprintf("http://localhost:%d/api/health", cfg.DashboardPort))
		if derr == nil {
			resp.Body.Close()
			logger.Info("Monitoring dashboard verified and running", "url", fmt.Sprintf("http://localhost:%d", cfg.DashboardPort))
			healthChecker.UpdateComponentHealth("dashboard", monitoring.HealthStatusHealthy, "Dashboard running")
		} else {
			logger.Warn("Dashboard may not be running",
				"url", fmt.Sprintf("http://localhost:%d", cfg.DashboardPort),
				"error", derr,
				"hint", "Check if port is available or if another service is using it")
			healthChecker.UpdateComponentHealth("dashboard", monitoring.HealthStatusUnhealthy, "Dashboard not responding: "+derr.Error())
			log.Printf("WARNING: Dashboard verification failed. Error: %v", derr)
			log.Printf("You can still try accessing http://localhost:%d manually", cfg.DashboardPort)
		}
	}

	// -------------------------------------------------------------------
	// Trust / attestation transparency wiring (Major Update §8.5).
	//
	// The trust surface is a *transparency signal*, not a consensus rule
	// (see docs/docs/NVIDIA_LOCK_CONSENSUS_SCOPE.md). The node therefore:
	//
	//   1. Opts in by default (TrustEndpointsDisabled defaults to false).
	//   2. Publishes the size of its known active-validator set as the
	//      "total public" denominator.
	//   3. Publishes its own NGC proof (from the monitoring ring buffer)
	//      as the sole numerator, because this build does not yet gossip
	//      cross-peer NGC attestations. That yields an honest 0/N or 1/N
	//      ratio — exactly the anti-over-claim posture the widget wants.
	//   4. Refreshes the cached summary every cfg.TrustRefreshInterval
	//      (default 10 s), so HTTP reads are O(1) memory reads.
	//
	// If TrustEndpointsDisabled is true, the singleton is intentionally
	// left nil *and* the disabled flag is set, so the handlers return
	// HTTP 404 per §8.5.3 ("a node that does not serve the trust surface
	// must not answer the endpoint").
	// -------------------------------------------------------------------
	if cfg.TrustEndpointsDisabled {
		api.SetTrustAggregator(nil, true)
		logger.Info("Trust transparency endpoints disabled by config",
			"knob", "[trust] disabled=true / QSDM_TRUST_DISABLED=1",
			"surface", "/api/v1/trust/attestations/* will return 404")
	} else {
		localNodeID := net.Host.ID().String()
		trustPeerProvider := api.NewValidatorSetPeerProvider(
			api.ValidatorEnumeratorFunc(func() []string {
				vs := nodeValidatorSet.ActiveValidators()
				out := make([]string, 0, len(vs))
				for _, v := range vs {
					out = append(out, v.Address)
				}
				return out
			}),
		)
		trustLocalSource := &api.MonitoringLocalSource{
			NodeID:     localNodeID,
			RegionHint: cfg.TrustRegionHint,
		}
		trustAgg := api.NewTrustAggregator(api.TrustConfig{
			PeerProvider: trustPeerProvider,
			LocalSource:  trustLocalSource,
			FreshWithin:  cfg.TrustFreshWithin,
		})
		// Seed the cache synchronously so the first HTTP scrape after
		// boot does not see an empty summary. The aggregator's warm-up
		// window is separate and still governs the 200 / 503 split.
		trustAgg.Refresh()
		api.SetTrustAggregator(trustAgg, false)
		// Expose the aggregator's cached numbers on /metrics so
		// Alertmanager can page on attested-count drops without
		// having to poll the JSON endpoint. Registered
		// unconditionally whenever the trust surface is enabled —
		// the collector is nil-safe and O(1), so there is no reason
		// to gate it behind the dashboard UI.
		monitoring.GlobalScrapePrometheusExporter().RegisterCollector(
			"trust_aggregator",
			api.TrustMetricsCollector(trustAgg),
		)
		logger.Info("Trust transparency endpoints wired",
			"node_id", localNodeID,
			"region_hint", cfg.TrustRegionHint,
			"fresh_within", cfg.TrustFreshWithin.String(),
			"refresh_interval", cfg.TrustRefreshInterval.String())
		go func() {
			t := time.NewTicker(cfg.TrustRefreshInterval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					trustAgg.Refresh()
				}
			}
		}()
	}

	// Start secure HTTP API server (optional, requires CGO for quantum-safe crypto)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("Panic during API server initialization", "error", r)
				log.Printf("API server initialization panic: %v (this is normal if CGO/liboqs is not available)", r)
			}
		}()

		apiServer, err := api.NewServer(cfg, logger, walletService, storageBackend, dynamicManager, sharedAuth)
		if err != nil {
			logger.Warn("API server not available",
				"error", err,
				"reason", "CGO and liboqs required for quantum-safe authentication",
				"note", "Node will continue without API server")
			log.Printf("API server will not be available: %v", err)
			log.Printf("To enable API server: build with CGO_ENABLED=1 and install liboqs")
		} else {
			apiServer.SetContractEngine(contractEngine)
			if bridgeProto != nil {
				apiServer.SetBridgeProtocol(bridgeProto)
			}
			if atomicSwap != nil {
				apiServer.SetAtomicSwapProtocol(atomicSwap)
			}
			if bridgeRelay != nil {
				apiServer.SetBridgeRelay(bridgeRelay, net.Host.ID().String())
			}
			apiServer.SetTxGossipBroadcast(func(b []byte) error {
				if txGossipRelay != nil {
					return txGossipRelay.MaybePublishOpaque(b)
				}
				return net.Broadcast(b)
			})
			apiServer.SetTokenRegistryPath(tokenRegistryPath)
			apiServer.SetAdminAPI(&api.AdminAPI{
				Accounts:     adminAccounts,
				Validators:   nodeValidatorSet,
				Finality:     adminFinality,
				Mempool:      adminPool,
				Receipts:     adminReceipts,
				Peers:        nodeTxRep,
				Tracer:       contractEngine.Tracer(),
				Producer:     adminProducer,
				BFTExecutor:  bftExec,
				PolFollower:  polFollower,
				Audit:        api.NewAdminAuditTrail(auditSecret),
				HotReloader:  adminHot,
			})
			logger.Info("Starting secure HTTP API server", "port", cfg.APIPort)
			if err := apiServer.Start(); err != nil {
				logger.Error("API server failed", "error", err)
				log.Printf("API server error: %v", err)
			}
		}
	}()

	var nvidiaP2PGate *monitoring.NvidiaLockP2PGate
	if cfg.NvidiaLockEnabled && cfg.NvidiaLockGateP2P {
		maxAge := cfg.NvidiaLockMaxProofAge
		if maxAge <= 0 {
			maxAge = 15 * time.Minute
		}
		nvidiaP2PGate = &monitoring.NvidiaLockP2PGate{
			Enabled:         true,
			MaxProofAge:     maxAge,
			ExpectedNodeID:  cfg.NvidiaLockExpectedNodeID,
			ProofHMACSecret: cfg.NvidiaLockProofHMACSecret,
		}
		logger.Info("NVIDIA-lock P2P gate enabled: libp2p transactions require a qualifying ingested NGC proof (non-consuming check)")
	}

	// Inbound pubsub: dispatch JSON wallet txs vs mesh3d wire (`qsdm_mesh3d_v1`) without double-processing the same payload.
	net.SetMessageHandler(func(msg []byte) {
		metrics.IncrementNetworkMessagesRecv()
		metrics.IncrementTransactionsProcessed()
		transaction.DispatchInboundP2P(transaction.DispatchDeps{
			Logger:            logger,
			Msg:               msg,
			DynamicManager:    dynamicManager,
			WasmSdk:           walletSdk,
			Consensus:         consensus,
			Storage:           transaction.AdaptStorage(storageBackend),
			NvidiaGate:        nvidiaP2PGate,
			Mesh3dValidator:   mesh3dValidator,
			QuarantineManager: quarantineManager,
			ReputationManager: reputationManager,
		})
	})

	// Transaction generation goroutine (only if wallet service is available)
	if walletService != nil {
		go func() {
			// Wait a bit for network to stabilize
			time.Sleep(5 * time.Second)

			txCounter := 0
			seq := 0
			for {
				seq++
				// Get recent transactions for parent cells
				var parentCells []string
				if txStorage, ok := storageBackend.(interface {
					GetRecentTransactions(address string, limit int) ([]map[string]interface{}, error)
				}); ok {
					recentTxs, err := txStorage.GetRecentTransactions(walletService.GetAddress(), 2)
					if err == nil && len(recentTxs) >= 2 {
						// Use recent transaction IDs as parent cells
						for _, tx := range recentTxs {
							if txID, ok := tx["id"].(string); ok && txID != "" {
								parentCells = append(parentCells, txID)
							}
						}
					}
				}

				// If we don't have enough parent cells, use deterministic synthetic IDs (API-valid length)
				if len(parentCells) < 2 {
					a, b := wallet.StableParentCellIDs(seq, walletService.GetAddress())
					parentCells = []string{a, b}
				}

				// Create a real transaction
				// For demo: send to a test recipient (in production, this would come from user input)
				recipient := "test_recipient_address"
				amount := 10
				fee := 0.1
				geotag := "US"

				txBytes, err := walletService.CreateTransaction(recipient, amount, fee, geotag, parentCells)
				if err != nil {
					logger.Error("Failed to create transaction", "error", err)
					time.Sleep(30 * time.Second)
					continue
				}

				// Broadcast via gossip relay (dedupe + rate limit) then libp2p publish
				err = txGossipRelay.MaybePublishOpaque(txBytes)
				if err != nil {
					logger.Error("Failed to broadcast transaction", "error", err)
					metrics.RecordError("Broadcast failed: " + err.Error())
				} else {
					txCounter++
					metrics.IncrementNetworkMessagesSent()
					logger.Info("Transaction created and broadcasted",
						"tx_number", txCounter,
						"sender", walletService.GetAddress(),
						"recipient", recipient,
						"amount", amount,
						"balance", walletService.GetBalance())

					if envPublishMeshCompanion() && len(parentCells) >= 2 {
						sm := "default-submesh"
						if ds, e1 := dynamicManager.MatchP2POrReject(fee, geotag, txBytes); e1 == nil && ds != nil {
							sm = ds.Name
						} else if ds, e2 := dynamicManager.RouteTransaction(fee, geotag); e2 == nil && ds != nil {
							sm = ds.Name
						}
						wire, werr := mesh3d.BuildMeshCompanionFromWalletJSON(txBytes, parentCells[:2], sm)
						if werr != nil {
							logger.Warn("mesh companion build failed", "error", werr)
						} else if cerr := txGossipRelay.MaybePublishOpaque(wire); cerr != nil {
							logger.Warn("mesh companion gossip failed", "error", cerr)
						} else {
							monitoring.RecordMeshCompanionPublish()
							metrics.IncrementNetworkMessagesSent()
						}
					}
				}

				// Generate transactions at configured interval
				time.Sleep(cfg.TransactionInterval)
			}
		}()
	} else {
		logger.Info("Wallet service not available - node operating in receive-only mode")
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	// Graceful shutdown
	go func() {
		<-sigs
		logger.Info("Shutdown signal received, initiating graceful shutdown...")

		// Update health status
		healthChecker.UpdateComponentHealth("network", monitoring.HealthStatusDegraded, "Shutting down")
		healthChecker.UpdateComponentHealth("storage", monitoring.HealthStatusDegraded, "Shutting down")

		// Flush bridge state to disk before closing
		if bridgeAutoSaver != nil {
			bridgeAutoSaver.Stop()
			logger.Info("Bridge state saved to disk")
		}

		if err := chain.SaveStakingLedger(stakingLedger, stakingPath); err != nil {
			logger.Warn("Staking ledger flush on shutdown failed", "error", err, "path", stakingPath)
		} else {
			logger.Info("Staking ledger saved to disk", "path", stakingPath)
		}

		if evidenceRelay != nil {
			evidenceRelay.Close()
		}
		if polRelay != nil {
			polRelay.Close()
		}

		// Close network
		if err := net.Close(); err != nil {
			logger.Error("Error closing libp2p host", "error", err)
		}

		// Close storage
		if err := storageBackend.Close(); err != nil {
			logger.Error("Error closing storage", "error", err)
		}

		// Log final metrics
		stats := metrics.GetStats()
		logger.Info("Final metrics", "stats", stats)

		logger.Info(branding.Name + " node stopped gracefully.")
		os.Exit(0)
	}()

	// Keep main goroutine alive
	logger.Info(branding.Name + " node running. Press Ctrl+C to shutdown.")

	// Use os.Stdout and flush to ensure output appears immediately
	fmt.Fprintln(os.Stdout, "="+strings.Repeat("=", 60)+"=")
	os.Stdout.Sync()
	fmt.Fprintln(os.Stdout, branding.Name+" node is RUNNING")
	os.Stdout.Sync()
	fmt.Fprintln(os.Stdout, "="+strings.Repeat("=", 60)+"=")
	os.Stdout.Sync()
	fmt.Fprintf(os.Stdout, "Dashboard:     http://localhost:%d\n", cfg.DashboardPort)
	os.Stdout.Sync()
	fmt.Fprintf(os.Stdout, "Log Viewer:    http://localhost:%d\n", cfg.LogViewerPort)
	os.Stdout.Sync()
	if cfg.EnableTLS {
		fmt.Fprintf(os.Stdout, "API Server:    https://localhost:%d (TLS 1.3)\n", cfg.APIPort)
	} else {
		fmt.Fprintf(os.Stdout, "API Server:    http://localhost:%d (INSECURE - dev only)\n", cfg.APIPort)
	}
	os.Stdout.Sync()
	fmt.Fprintf(os.Stdout, "Log File:      %s\n", cfg.LogFile)
	os.Stdout.Sync()
	fmt.Fprintln(os.Stdout, "="+strings.Repeat("=", 60)+"=")
	os.Stdout.Sync()
	fmt.Fprintln(os.Stdout, "Press Ctrl+C to shutdown gracefully")
	os.Stdout.Sync()
	fmt.Fprintln(os.Stdout, "="+strings.Repeat("=", 60)+"=")
	os.Stdout.Sync()

	select {} // Block forever until shutdown signal
}
