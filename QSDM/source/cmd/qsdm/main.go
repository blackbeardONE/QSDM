package main

import (
	"bufio"
	"context"
	cryptorand "crypto/rand"
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

	"github.com/blackbeardONE/QSDM/cmd/qsdm/governancecli"
	"github.com/blackbeardONE/QSDM/cmd/qsdm/transaction"
	"github.com/blackbeardONE/QSDM/internal/blockdriver"
	"github.com/blackbeardONE/QSDM/internal/dashboard"
	"github.com/blackbeardONE/QSDM/internal/logging"
	"github.com/blackbeardONE/QSDM/internal/miningsvc"
	"github.com/blackbeardONE/QSDM/internal/v2wiring"
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
	"math/big"
	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/cc"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
	"github.com/blackbeardONE/QSDM/pkg/mining/enrollment"
	"github.com/blackbeardONE/QSDM/pkg/mining/roleguard"
	"github.com/blackbeardONE/QSDM/pkg/monitoring"
	"github.com/blackbeardONE/QSDM/pkg/networking"
	"github.com/blackbeardONE/QSDM/pkg/quarantine"
	"github.com/blackbeardONE/QSDM/pkg/storage"
	"github.com/blackbeardONE/QSDM/pkg/submesh"
	"github.com/blackbeardONE/QSDM/pkg/wallet"
	"github.com/blackbeardONE/QSDM/pkg/wasm"
	"log"
	"runtime/debug"
)

var logger *logging.Logger
var metrics *monitoring.Metrics
var healthChecker *monitoring.HealthChecker

func envPublishMeshCompanion() bool {
	return envcompat.Truthy("QSDM_PUBLISH_MESH_COMPANION", "QSDM_PUBLISH_MESH_COMPANION")
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
// hostKeyPath, when non-empty, persists the libp2p host PrivateKey across
// restarts so peer.ID is stable too — see pkg/networking/hostkey.go for the
// on-disk format. An empty hostKeyPath preserves the legacy ephemeral-identity
// behaviour (acceptable for tests and dev; on production it causes the
// post-restart trust-attestation blip documented in RELEASE_NOTES_v0.3.0.md
// "Session 87").
func SetupNetwork(ctx context.Context, logger *logging.Logger, port int, hostKeyPath string) (*networking.Network, error) {
	return networking.SetupLibP2PWithPortAndKey(ctx, logger, port, hostKeyPath)
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

	net, err := SetupNetwork(ctx, logger, cfg.NetworkPort, cfg.NetworkHostKeyPath)
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

	// Optional WASM contract modules. The non-CGO !cgo build defaults to the
	// pure-Go wazero runtime via pkg/wasm; the .wasm files themselves are
	// not shipped in the repo and the loader falls back gracefully when
	// they are absent (production state on most validators).
	//
	// Logging policy: file-absent is the configured-off case (INFO);
	// file-present-but-malformed and SDK-instantiation errors are real
	// problems (WARN).
	walletWasmPath := "wasm_modules/wallet/wallet.wasm"
	walletBytes, err := wasm.LoadWASMFromFile(walletWasmPath)
	var walletSdk *wasm.WASMSDK
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Info("WASM wallet module disabled (file absent)", "path", walletWasmPath)
		} else {
			logger.Warn("Failed to load wallet WASM module", "path", walletWasmPath, "error", err)
		}
	} else {
		walletSdk, err = wasm.NewWASMSDK(walletBytes)
		if err != nil {
			logger.Warn("Failed to instantiate WASM SDK for wallet", "error", err)
			walletSdk = nil
		} else {
			logger.Info("WASM wallet SDK initialized")
		}
	}

	validatorWasmPath := "wasm_modules/validator/validator.wasm"
	_, err = wasm.LoadWASMFromFile(validatorWasmPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Info("WASM validator module disabled (file absent)", "path", validatorWasmPath)
		} else {
			logger.Warn("Failed to load validator WASM module", "path", validatorWasmPath, "error", err)
		}
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
	bridgeStatePath := filepath.Join(stateDir, "qsdm_bridge_state.json")
	tokenRegistryPath := filepath.Join(stateDir, "qsdm_tokens.json")
	stakingPath := filepath.Join(stateDir, "qsdm_staking.json")
	challengeKeyPath := filepath.Join(stateDir, "qsdm_challenge_hmac.key")

	// v2 NVIDIA-locked mining activation (MINING_PROTOCOL_V2.md
	// §0 / §10). Setting this env activates FORK_V2_HEIGHT=0 so
	// the verifier engages the v2 attestation gate from genesis:
	// every accepted proof must carry a valid nvidia-cc-v1 or
	// nvidia-hmac-v1 attestation. CPU / non-NVIDIA proofs are
	// rejected at the verifier without entering the mempool.
	//
	// Default OFF preserves v1 testnet behaviour (CPU proofs
	// accepted economically only) so existing testnets keep
	// running unchanged. The env-gated activation is the
	// minimum-blast-radius switch: a misconfigured restart
	// without QSDM_V2_ACTIVE=1 quietly reverts to v1, so
	// operators must intentionally opt in.
	v2Active := envcompat.Truthy("QSDM_V2_ACTIVE", "QSDM_V2_ACTIVE")
	if v2Active {
		mining.SetForkV2Height(0)
		logger.Info("v2 NVIDIA-locked mining: ACTIVE from genesis",
			"fork_v2_height", 0,
			"env_var", "QSDM_V2_ACTIVE",
			"effect", "non-NVIDIA / unattested proofs rejected at /api/v1/mining/submit",
			"see", "MINING_PROTOCOL_V2.md §0 / §10")
	} else {
		logger.Info("v2 NVIDIA-locked mining: NOT active (v1 testnet protocol)",
			"env_var", "QSDM_V2_ACTIVE",
			"to_activate", "set QSDM_V2_ACTIVE=1 (chain reset required per §10.3)")
	}
	// User store persistence: fall back to <stateDir>/qsdm_users.json
	// when nothing was set explicitly (config file or env). This matches
	// the sibling staking/bridge JSON files and keeps all ledger-local
	// state under /opt/qsdm on the default systemd layout.
	if strings.TrimSpace(cfg.UserStorePath) == "" {
		cfg.UserStorePath = filepath.Join(stateDir, "qsdm_users.json")
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

	// Wire both reputation trackers into the monitoring
	// scrape so qsdm_reputation_*{tracker="tx|evidence"}
	// gauges reflect live state. Also start the per-tracker
	// decay goroutine — without this, scores never decay
	// back toward zero, which means historic peer
	// behaviour permanently dominates current state.
	monitoring.RegisterReputationProvider("tx", nodeTxRep)
	monitoring.RegisterReputationProvider("evidence", nodeEvidenceRep)
	nodeTxRep.Start()
	nodeEvidenceRep.Start()
	defer nodeTxRep.Stop()
	defer nodeEvidenceRep.Stop()

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
	// v2 mining wiring (Phase 2c): see internal/v2wiring/v2wiring.go.
	// Wire() constructs the on-chain enrollment state,
	// EnrollmentApplier, EnrollmentAwareApplier, SlashApplier,
	// monitoring gauge provider, mempool admission gate, and the
	// HTTP submitter handle in one centralised, test-covered
	// place. We pass BaseAdmit=nil here because the POL/BFT
	// extension predicate is registered separately below — the
	// pool admission gate set inside Wire is replaced once we
	// install the composed BaseAdmit + enrollment.AdmissionChecker
	// pair, ensuring both predicates run for non-enrollment txs.
	govStorePath := filepath.Join(stateDir, "qsdm_governance.json")
	v2Wired, v2WireErr := v2wiring.Wire(v2wiring.Config{
		Accounts:       adminAccounts,
		Pool:           adminPool,
		BaseAdmit:      nil, // re-installed below alongside POL/BFT predicate.
		SlashRewardBPS: chain.SlashRewardCap,
		LogSweepError: func(h uint64, err error) {
			logger.Warn("v2 mining: enrollment sweep failed",
				"height", h, "error", err)
		},
		// GovParamStorePath persists the chain-parameter
		// store (TCs / ForkV2TCHeight / activation params)
		// across restarts via atomic-rename snapshots in the
		// post-seal hook. Default value path lives next to
		// the other state files; the directory is the same
		// stateDir we already use for qsdm_chain.ndjson +
		// qsdm_accounts.json + qsdm_enrollment.json so the
		// operator's reset / backup ceremony covers all four
		// in one step.
		GovParamStorePath: govStorePath,
		LogSnapshotError: func(h uint64, err error) {
			logger.Warn("gov param store: snapshot failed",
				"height", h, "path", govStorePath, "error", err)
		},
		// Slash receipts NDJSON persistence. Per-publish
		// append + boot-time replay so the
		// /api/v1/mining/slash/{tx_id} GET endpoint serves
		// continuous history across restarts. Schema
		// matches api.SlashReceiptView so an operator's
		// `tail -f` shows the same fields the HTTP endpoint
		// returns. The default path lives next to the other
		// state files in stateDir.
		SlashReceiptsPath: filepath.Join(stateDir, "qsdm_slash_receipts.ndjson"),
		LogSlashReceiptsError: func(err error) {
			logger.Warn("slash receipts: persistence error",
				"path", filepath.Join(stateDir, "qsdm_slash_receipts.ndjson"),
				"error_str", err.Error())
		},
	})
	if v2WireErr != nil {
		log.Fatalf("v2 mining wiring failed: %v", v2WireErr)
	}

	adminProducer := chain.NewBlockProducer(adminPool, v2Wired.StateApplier, prodCfg)
	v2Wired.AttachToProducer(adminProducer)
	adminProducer.SetAppendReceiptStore(adminReceipts)
	adminProducer.SetPolFollower(polFollower)
	// Solo-validator mode (QSDM_SOLO_VALIDATOR_MODE=1):
	// without peers there is no BFT quorum to commit blocks
	// or POL gossip to publish round certificates, so the
	// production gates would refuse to extend past genesis.
	// Skip SetBFTSealGate and SetPreSealBFTRound in solo
	// mode; internal/blockdriver drives ProduceBlock directly
	// against an unguarded producer. When a peer joins, flip
	// the env var off and these gates are restored on the
	// next process restart — there is no consensus-relevant
	// state to migrate because the solo chain is treated as
	// a clean testnet origin, not a fork to merge.
	soloValidatorMode := envcompat.Truthy("QSDM_SOLO_VALIDATOR_MODE", "QSDM_SOLO_VALIDATOR_MODE")
	if !soloValidatorMode {
		adminProducer.SetBFTSealGate(liveBFT)
		adminProducer.SetPreSealBFTRound(func(blk *chain.Block) error {
			return chain.RunSyntheticBFTRoundWithExecutor(bftExec, nodeValidatorSet, blk)
		})
	} else {
		logger.Info("Solo validator mode: BFT seal gate and pre-seal synthetic round disabled",
			"env_var", "QSDM_SOLO_VALIDATOR_MODE",
			"reason", "no peer validators to drive BFT quorum; internal/blockdriver will own block production")
	}
	// Compose the admission gate. v2wiring.Wire() already
	// installed enrollment.AdmissionChecker(nil); we replace it
	// with the same checker wrapped around the POL/BFT base
	// predicate. Order matters: enrollment txs go through the
	// stateless enroll/unenroll validators FIRST (cheap,
	// attributable), and non-enrollment txs fall through to
	// the POL/BFT predicate (consensus-attributable). Without
	// this composition either path would silently bypass one
	// of the two gates.
	baseAdmit := func(_ *mempool.Tx) error {
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
	}
	if soloValidatorMode {
		// Solo: ignore the BFT/POL extension predicate
		// because liveBFT.IsCommitted always returns false
		// without peer votes, which would otherwise reject
		// every solo-mode reward tx.
		v2wiring.ReinstallAdmissionGate(adminPool, nil)
		logger.Info("Solo validator mode: admission gate now ignores BFT/POL predicates",
			"reason", "no peers to vote → liveBFT.IsCommitted permanently false")
	} else {
		v2wiring.ReinstallAdmissionGate(adminPool, baseAdmit)
	}
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

	// v1 mining service (Phase 2c-v wiring): install the
	// concrete api.MiningService that backs /api/v1/mining/work
	// and /api/v1/mining/submit. Without this, both endpoints
	// return 503 mining_unavailable — the pre-2026-05-06 BLR1
	// posture confirmed by the curl probe in commit history.
	//
	// Bring-up parameters (deliberately conservative):
	//   - WorkSet: a deterministic 3-batch / 3-cell-per-batch
	//     synthetic. Mining cells are ID-sorted so any miner
	//     re-canonicalises to byte-identical content.
	//   - DAGSize: 1024 entries (32 KiB resident). Enough to
	//     exercise the DAG-walk path; small enough that a
	//     fresh validator boots in ms. Production mainnet
	//     re-targets to mining.ProductionDAGSize.
	//   - Difficulty: 2 (matches qsdmminer --self-test).
	//     Hits a target in <2 attempts on commodity CPU,
	//     keeping the bring-up loop interactive. Real
	//     difficulty retargets land with the
	//     DifficultyAdjuster wiring (Phase 4.6).
	//
	// All three values are static for now. They can be
	// promoted to operator-tunable config keys once we have a
	// second validator that needs to agree on them. Until
	// then, hardcoding keeps consensus byte-identical.
	// soloDriver is constructed AFTER specCheck so the
	// optional Tier-3 RewardPenalty can be wired in at New
	// time (the Driver field is read-only after construction
	// to keep the buildTxs hot path branch-free). The
	// declaration stays here so the rest of the boot code
	// can reference it; the construction itself is below.
	var soloDriver *blockdriver.Driver

	// v2 attestation dispatcher. Wired UNCONDITIONALLY (whether
	// or not QSDM_V2_ACTIVE is set) because:
	//
	//   - With v2 NOT active, ForkV2Height defaults to MaxUint64
	//     and the verifier never invokes the dispatcher. Wiring
	//     it costs ~one nonce-store map and a per-validator
	//     HMAC-key file; nothing consensus-touching.
	//
	//   - With v2 ACTIVE, the dispatcher is the consensus rule
	//     that rejects CPU / non-NVIDIA proofs. A miswired
	//     dispatcher (nil) silently falls through to
	//     FailClosedVerifier, which rejects everything — safe,
	//     but the operator gets no diagnostics. Wiring eagerly
	//     means a startup-time failure (e.g. unreadable HMAC
	//     key file) surfaces clearly here rather than silently
	//     during the first /api/v1/mining/submit request.
	//
	// Collaborators:
	//   Registry        — enrollment.NewStateBackedRegistry over
	//                     the live on-chain enrollment state, so
	//                     the HMAC verifier resolves operator
	//                     entries against the same state the
	//                     EnrollmentApplier mutates.
	//   ChallengeVerifier — challenge.NewHMACSignerVerifier
	//                       seeded with this validator's local
	//                       signer key. Multi-validator setups
	//                       Register() each peer's key here at
	//                       genesis; for the solo deploy the
	//                       single-key registration is enough.
	//   NonceStore      — per-(node_id, nonce) replay cache,
	//                     in-memory with 2*FreshnessWindow
	//                     retention. Production multi-validator
	//                     deployments will swap this for a
	//                     persistent store; the interface stays
	//                     unchanged.
	//   CCConfig        — populated from cc.LoadVerifierConfig
	//                     when QSDM_CC_ROOTS_DIR is set; nil
	//                     otherwise (cc.NewStubVerifier handles
	//                     nvidia-cc-v1 proofs as ErrNotYetAvailable
	//                     so the bring-up posture does not pretend
	//                     to verify CC bundles without a trust
	//                     anchor).
	//
	// The validator's HMAC key is a per-process secret persisted
	// to <stateDir>/qsdm_challenge_hmac.key. Auto-generated on
	// first boot via crypto/rand; the key never leaves the file
	// and never appears in logs (only the signer_id, derived
	// from the key's first 8 bytes via hex, is logged).
	chSignerID, chSignerKey, chKeyErr := loadOrCreateChallengeKey(challengeKeyPath)
	if chKeyErr != nil {
		log.Fatalf("v2 challenge key init: %v", chKeyErr)
	}
	chSigner, chSignerErr := challenge.NewHMACSigner(chSignerID, chSignerKey)
	if chSignerErr != nil {
		log.Fatalf("v2 challenge signer: %v", chSignerErr)
	}
	chSignerVerifier := challenge.NewHMACSignerVerifier()
	if regErr := chSignerVerifier.Register(chSignerID, chSignerKey); regErr != nil {
		log.Fatalf("v2 challenge signer-verifier registration: %v", regErr)
	}
	// Peer-signer allowlist. Lets the validator accept v2
	// challenges minted by remote cmd/qsdm-attester instances
	// (e.g. an operator's home machine running on a 3050).
	// The file path is opt-in via QSDM_PEER_SIGNERS_FILE; an
	// unset/missing file is a no-op so existing deployments
	// keep their pre-Phase-2c-attester posture.
	if peerSignersPath := strings.TrimSpace(os.Getenv("QSDM_PEER_SIGNERS_FILE")); peerSignersPath != "" {
		peers, peersErr := LoadPeerSignersFile(peerSignersPath)
		if peersErr != nil {
			log.Fatalf("v2 peer-signers load: %v", peersErr)
		}
		registered, regErrs := RegisterPeerSigners(chSignerVerifier, peers)
		for _, e := range regErrs {
			logger.Warn("v2 peer-signer rejected",
				"signer_id", e.PeerSigner.SignerID,
				"note", e.PeerSigner.Note,
				"reason", e.Err.Error())
		}
		if len(regErrs) > 0 {
			log.Fatalf("v2 peer-signers: %d entries rejected; fix peer_signers.toml and restart", len(regErrs))
		}
		logger.Info("v2 peer-signers loaded",
			"path", peerSignersPath,
			"registered", registered)
	}
	chIssuer, chIssuerErr := challenge.NewIssuer(chSigner)
	if chIssuerErr != nil {
		log.Fatalf("v2 challenge issuer: %v", chIssuerErr)
	}
	api.SetChallengeIssuer(chIssuer)
	logger.Info("v2 challenge issuer wired",
		"signer_id", chSigner.SignerID(),
		"key_path", challengeKeyPath,
		"endpoint", "/api/v1/mining/challenge")

	hmacNonceStore := hmac.NewInMemoryNonceStore(2 * mining.FreshnessWindow)
	attestProdCfg := attest.ProductionConfig{
		Registry:          enrollment.NewStateBackedRegistry(v2Wired.EnrollmentState),
		ChallengeVerifier: chSignerVerifier,
		NonceStore:        hmacNonceStore,
		// DenyList nil → hmac.EmptyDenyList (genesis posture).
		// FreshnessWindow / AllowedFutureSkew zero → spec defaults.
	}

	// Optional: Tier-2 telemetry advisory checker. Wired
	// only when the operator opts in via
	// QSDM_SPEC_CHECK_ENABLED — keeps the bit-for-bit
	// behaviour of pre-Tier-2 deployments unchanged. See
	// cmd/qsdm/spec_check.go for the wiring rationale.
	specCheck, specCheckErr := buildSpecCheckWiring(context.Background(), logger.Info)
	if specCheckErr != nil {
		log.Fatalf("spec-check wiring: %v", specCheckErr)
	}
	if specCheck != nil {
		attestProdCfg.HMACOnAccept = specCheck.Adapter.OnHMACAccept
		total, signers, skus := specCheck.Catalog.Counters()
		logger.Info("spec-check: Tier-2 advisory checker active",
			"catalog_entries", total,
			"catalog_signers", signers,
			"catalog_skus", skus,
			"peer_urls", strings.Join(specCheck.PeerURLs, ","),
			"refresh_every", specCheck.RefreshEvery.String(),
			"ring_cap", specCheck.RingCap)
		go runSpecCheckPoller(context.Background(), specCheck, logger.Info)
		api.SetSpecAnomaliesProbe(specAnomaliesProbe(specCheck))
		monitoring.SetSpecCheckProbe(specCheckMonitoringProbe(specCheck))
		logger.Info("/api/v1/mining/spec-anomalies probe wired")
		logger.Info("qsdm_spec_check_* Prometheus collector wired")
		// Tier-3 wiring — only when the operator turned on
		// QSDM_SPEC_PENALTY_ENABLED, in which case
		// specCheck.Penalty is non-nil and the probes have
		// real data to publish. Pre-Tier-3 deployments
		// short-circuit the SetSpec*Probe calls (probe
		// returns nil) and the endpoints serve 503.
		if specCheck.Penalty != nil {
			api.SetSpecPenaltyProbe(specPenaltyProbe(specCheck))
			monitoring.SetSpecPenaltyProbe(specPenaltyMonitoringProbe(specCheck))
			logger.Info("/api/v1/mining/penalty probe wired")
			logger.Info("qsdm_spec_penalty_* Prometheus collector wired")
		}
	} else {
		logger.Info("spec-check: Tier-2 advisory checker disabled (set QSDM_SPEC_CHECK_ENABLED=1 to enable)")
	}
	if ccRootsDir := strings.TrimSpace(os.Getenv("QSDM_CC_ROOTS_DIR")); ccRootsDir != "" {
		// CC trust anchor is operator-supplied: if the dir is
		// configured but unreadable, refuse to boot rather than
		// silently fall back to the stub. A v2-active validator
		// that pretends to verify nvidia-cc-v1 but actually
		// rejects every CC proof is exactly the silent-failure
		// posture the activation gate exists to prevent.
		ccCfg, ccErr := cc.LoadVerifierConfig(cc.VerifierConfigOptions{
			RootPaths:  []string{ccRootsDir},
			NonceStore: hmacNonceStore,
		})
		if ccErr != nil {
			log.Fatalf("v2 cc verifier config: %v", ccErr)
		}
		if ccCfg != nil {
			attestProdCfg.CCConfig = ccCfg
			logger.Info("v2 nvidia-cc-v1 verifier wired",
				"roots_dir", ccRootsDir,
				"pinned_root_count", len(ccCfg.PinnedRoots))
		}
	} else {
		logger.Info("v2 nvidia-cc-v1 verifier: stub (datacenter Hopper/Blackwell path disabled)",
			"env_var", "QSDM_CC_ROOTS_DIR",
			"effect", "consumer NVIDIA GPUs only, via nvidia-hmac-v1; CC proofs reject as ErrNotYetAvailable")
	}
	v2Dispatcher, v2DispErr := attest.NewProductionDispatcher(attestProdCfg)
	if v2DispErr != nil {
		log.Fatalf("v2 attestation dispatcher wiring failed: %v", v2DispErr)
	}
	logger.Info("v2 attestation dispatcher wired",
		"hmac_path_active", true,
		"cc_path_active", attestProdCfg.CCConfig != nil,
		"fork_v2_active", v2Active,
		"effect_when_active", "post-fork proofs require nvidia-cc-v1 or nvidia-hmac-v1 attestation")

	if soloValidatorMode {
		// In solo mode the blockdriver is the miningsvc
		// reward sink. Tier-3 reward downgrade is wired here
		// (the deferred construction is the whole reason
		// the soloDriver var was declared earlier instead of
		// allocated inline). Pre-Tier-3 deployments leave
		// RewardPenalty nil → noopRewardPenalty inside the
		// driver, byte-identical to before.
		blockdriverCfg := blockdriver.Config{
			Producer: adminProducer,
			Pool:     adminPool,
			Accounts: adminAccounts,
			Logger:   logger,
		}
		if specCheck != nil && specCheck.Penalty != nil {
			// Cast: telemetrycheck.PerMinerStats already
			// satisfies blockdriver.RewardPenalty by virtue
			// of MultiplierFor(string) float64. Go's
			// structural interface check makes this a no-
			// op assignment at runtime.
			blockdriverCfg.RewardPenalty = specCheck.Penalty
			logger.Info("solo blockdriver: Tier-3 reward downgrade wired",
				"window_size", specCheck.Penalty.Config().WindowSize,
				"threshold_pct", specCheck.Penalty.Config().MismatchThresholdPct,
				"multiplier", specCheck.Penalty.Config().PenaltyMultiplier)
		}
		drv, drvErr := blockdriver.New(blockdriverCfg)
		if drvErr != nil {
			log.Fatalf("solo blockdriver wiring failed: %v", drvErr)
		}
		soloDriver = drv
		// Publish the driver to the Tier-3 monitoring probe
		// so /metrics surfaces blockdriver-side withheld-dust
		// counters. Safe to call even when Tier-3 is off —
		// the probe is nil-checked at scrape time.
		SetSoloDriverForMonitoring(soloDriver)
	}

	miningSvcCfg := miningsvc.Config{
		Producer:       adminProducer,
		WorkSet:        bringUpWorkSet(),
		DAGSize:        1024,
		Difficulty:     big.NewInt(2),
		BlocksPerEpoch: mining.DefaultBlocksPerMiningEpoch,
		Attestation:    v2Dispatcher,
	}
	if soloDriver != nil {
		miningSvcCfg.RewardSink = soloDriver
	}
	miningSvc, miningErr := miningsvc.New(miningSvcCfg)
	if miningErr != nil {
		// Mining wiring failure is operator-actionable; refuse
		// to boot in a half-wired state where /work and /submit
		// would return 503 silently.
		log.Fatalf("mining service wiring failed: %v", miningErr)
	}
	api.SetMiningService(miningSvc)
	if soloValidatorMode {
		// Wire the read-only AccountStore probe so operators
		// can curl /api/v1/mining/account?address=<addr> and
		// see mining rewards land. Only enabled in solo mode
		// because outside solo mode the wallet endpoints
		// already cover this with auth.
		api.SetMiningAccountProbe(accountProbeFromStore(adminAccounts))
		logger.Info("Solo validator mode: /api/v1/mining/account balance probe wired")
	}
	// Wire the emission probe regardless of solo mode — it
	// reads pure schedule state, no AccountStore peek, so
	// it's always safe to expose. SDK clients render
	// tokenomics widgets from this endpoint.
	api.SetMiningEmissionProbe(emissionProbeFromProducer(adminProducer))
	logger.Info("/api/v1/mining/emission probe wired (chain.DefaultEmissionSchedule)")
	api.SetMiningBlocksProbe(blocksProbeFromProducer(adminProducer))
	logger.Info("/api/v1/mining/blocks probe wired (BlockProducer header projection)")
	api.SetMiningReceiptProbe(receiptProbeFromStore(adminReceipts))
	logger.Info("/api/v1/receipts/{tx_id} probe wired (ReceiptStore lookup)")
	api.SetMiningReceiptsListProbe(receiptsListProbeFromStore(adminReceipts, adminProducer))
	logger.Info("/api/v1/receipts probe wired (ReceiptStore height-range list)")
	logger.Info("Mining service installed",
		"dag_size", uint32(1024),
		"difficulty", "2",
		"blocks_per_epoch", mining.DefaultBlocksPerMiningEpoch,
		"reward_sink_wired", soloDriver != nil,
		"endpoints", "/api/v1/mining/work, /api/v1/mining/submit")

	// Chain + accounts persistence (Phase 2c-vii follow-up).
	// Without this, every restart wipes both the BlockProducer
	// chain (in-memory `[]*Block`) and the AccountStore (in-
	// memory map), so the genesis-seal block below fires on
	// every boot and re-credits the funder + re-seeds the
	// prefund address. The NDJSON chain log is appended per
	// seal, the accounts JSON is overwritten per seal; on the
	// next boot we hydrate both before the genesis-seal block
	// runs so HasTip()=true and the seal is skipped.
	//
	// State files live under stateDir (the dir holding
	// qsdm.db). On a fresh host both paths are absent and the
	// genesis seal runs as before. On a returning host the
	// files exist and the genesis seal is skipped — exactly
	// the contract the seal-skip branch was already written
	// against, so no other code-paths change.
	//
	// Order matters during hydrate: load CHAIN first (it
	// determines HasTip), then ACCOUNTS. If the chain shows
	// blocks but the accounts file is missing, we fail-fast
	// rather than boot a half-restored state where balances
	// are zero but tip is non-zero — that combination would
	// cause the next reward tx (funder.nonce>0 expected) to
	// fail the nonce check and stall the chain.
	chainStatePath := filepath.Join(stateDir, "qsdm_chain.ndjson")
	accountsStatePath := filepath.Join(stateDir, "qsdm_accounts.json")
	enrollmentStatePath := filepath.Join(stateDir, "qsdm_enrollment.json")
	// Receipts are NDJSON-append-only (qsdm_receipts.ndjson). The
	// legacy whole-store JSON path (qsdm_receipts.json) is kept
	// only for one-shot migration of pre-existing deployments —
	// see the boot block below. New installs go straight to
	// NDJSON; the legacy path is never written.
	receiptsNDJSONPath := filepath.Join(stateDir, "qsdm_receipts.ndjson")
	receiptsLegacyJSONPath := filepath.Join(stateDir, "qsdm_receipts.json")
	if persistedBlocks, restoreErr := chain.LoadChainNDJSON(chainStatePath); restoreErr != nil {
		log.Fatalf("chain restore: read %s: %v", chainStatePath, restoreErr)
	} else if len(persistedBlocks) > 0 {
		if err := adminProducer.RestoreChain(persistedBlocks); err != nil {
			log.Fatalf("chain restore: producer hydrate from %s (%d blocks): %v",
				chainStatePath, len(persistedBlocks), err)
		}
		loadedAccounts, loadErr := adminAccounts.Load(accountsStatePath)
		if loadErr != nil {
			log.Fatalf("chain restore: accounts file %s missing or unreadable while chain has %d blocks (%v) — refusing to boot a half-restored state. Either restore the matching accounts file or wipe %s to reset the chain.",
				accountsStatePath, len(persistedBlocks), loadErr, chainStatePath)
		}
		// Enrollment state is hydrated next so the registry is
		// populated before the v2 attestation gate ever sees a
		// /api/v1/mining/submit. Missing file is OK (operator
		// nuked /opt/qsdm/qsdm_enrollment.json by hand to clear
		// enrollments without resetting the chain) — the
		// validator boots with an empty registry and any v2
		// proof rejects until the operator re-enrolls.
		loadedEnrollments, enrollErr := v2Wired.EnrollmentState.Load(enrollmentStatePath)
		if enrollErr != nil {
			log.Fatalf("chain restore: enrollment file %s unreadable (%v) — refusing to boot. Either restore the file or remove it (the chain will continue without enrollments and v2 proofs will reject).",
				enrollmentStatePath, enrollErr)
		}
		// Receipts: NDJSON-first. If qsdm_receipts.ndjson
		// exists we use it. Otherwise, if a legacy
		// qsdm_receipts.json is present, we one-shot
		// migrate by Load-ing the legacy JSON into the
		// in-memory store and re-flushing every receipt as
		// NDJSON via AppendBlockNDJSON; the legacy file is
		// then renamed to qsdm_receipts.json.legacy so the
		// operator has a backup for one boot, after which
		// the validator will only ever touch the .ndjson
		// path. A missing receipts file at all is fine —
		// fresh chain or operator hand-cleared it. A
		// corrupt receipts file is a hard error so the
		// operator notices instead of silently losing tx
		// history.
		loadedReceipts := 0
		_, ndjsonStatErr := os.Stat(receiptsNDJSONPath)
		_, legacyStatErr := os.Stat(receiptsLegacyJSONPath)
		switch {
		case ndjsonStatErr == nil:
			n, recErr := adminReceipts.LoadNDJSON(receiptsNDJSONPath)
			if recErr != nil {
				log.Fatalf("chain restore: receipts NDJSON %s unreadable (%v) — trim the offending trailing line or delete the file to continue without receipt history.",
					receiptsNDJSONPath, recErr)
			}
			loadedReceipts = n
		case legacyStatErr == nil:
			n, recErr := adminReceipts.Load(receiptsLegacyJSONPath)
			if recErr != nil {
				log.Fatalf("chain restore: legacy receipts JSON %s unreadable (%v) — delete the file to continue without receipt history.",
					receiptsLegacyJSONPath, recErr)
			}
			loadedReceipts = n
			// Re-flush as NDJSON so subsequent boots
			// take the cheap path. We walk the legacy
			// store's per-block index and append each
			// block's slice; this preserves height
			// ordering on disk, which is the same
			// order LoadNDJSON observes.
			migrated := 0
			for h := uint64(0); h <= adminProducer.TipHeight(); h++ {
				if got := adminReceipts.GetByBlock(h); len(got) == 0 {
					continue
				}
				w, werr := adminReceipts.AppendBlockNDJSON(receiptsNDJSONPath, h)
				if werr != nil {
					log.Fatalf("receipts migration: append height=%d failed: %v — leave qsdm_receipts.json in place and re-run", h, werr)
				}
				migrated += w
			}
			backupPath := receiptsLegacyJSONPath + ".legacy"
			if err := os.Rename(receiptsLegacyJSONPath, backupPath); err != nil {
				logger.Warn("receipts migration: rename legacy file failed (NDJSON is now the source of truth, but the JSON file remains)",
					"src", receiptsLegacyJSONPath, "dst", backupPath, "error_str", err.Error())
			}
			logger.Info("Receipts migrated: legacy JSON → NDJSON append-only",
				"loaded_legacy", n,
				"written_ndjson", migrated,
				"legacy_renamed_to", backupPath,
				"ndjson_path", receiptsNDJSONPath)
		default:
			// Neither file present — fresh chain or hand-cleared. No-op.
		}
		logger.Info("Chain + accounts + enrollments + receipts restored from disk",
			"blocks", len(persistedBlocks),
			"tip_height", adminProducer.TipHeight(),
			"accounts_loaded", loadedAccounts,
			"enrollments_loaded", loadedEnrollments,
			"receipts_loaded", loadedReceipts,
			"chain_path", chainStatePath,
			"accounts_path", accountsStatePath,
			"enrollment_path", enrollmentStatePath,
			"receipts_path", receiptsNDJSONPath,
			"genesis_seal_will_skip", true)
	} else {
		logger.Info("No persisted chain found; genesis seal will run on a fresh chain",
			"chain_path", chainStatePath)
	}

	// Compose persistence with whatever OnSealedBlock the
	// v2wiring layer installed. v2wiring's hook runs the
	// enrollment-sweep + gov-promote logic against
	// AccountStore — those mutations MUST land before we
	// snapshot, otherwise the on-disk accounts trail the
	// in-memory state by one block's worth of stake
	// matures + activated gov params. So the order is:
	//
	//  1. v2wiring hook (sweep + promote)
	//  2. AppendBlockToFile (chain log gains the block)
	//  3. AccountStore.Save (accounts catch up to chain)
	//
	// If a crash interrupts between (2) and (3): on
	// restart, chain has block N, accounts trail at N-1.
	// This is recoverable but not yet auto-recovered (the
	// fail-fast above is the safe default — operator wipes
	// or hand-replays). That's acceptable for testnet; a
	// future commit can add chain-replay-on-mismatch.
	priorSealedBlockHook := adminProducer.OnSealedBlock
	adminProducer.OnSealedBlock = func(blk *chain.Block) {
		if priorSealedBlockHook != nil {
			priorSealedBlockHook(blk)
		}
		if blk == nil {
			return
		}
		if err := chain.AppendBlockToFile(chainStatePath, blk); err != nil {
			logger.Warn("chain persistence: append failed",
				"height", blk.Height,
				"path", chainStatePath,
				"error_str", err.Error())
			return
		}
		if err := adminAccounts.Save(accountsStatePath); err != nil {
			logger.Warn("accounts persistence: save failed",
				"path", accountsStatePath,
				"error_str", err.Error())
		}
		// Enrollment state must persist alongside accounts —
		// the v2 attestation gate (hmac.Verify) consults the
		// registry every /api/v1/mining/submit, and a registry
		// that lags one block behind would let a slashed
		// NodeID briefly continue to mine after restart, or
		// (more commonly) reject every legitimate operator
		// because the on-disk snapshot was empty when the
		// validator restarted.
		if v2Wired.EnrollmentState != nil {
			if err := v2Wired.EnrollmentState.Save(enrollmentStatePath); err != nil {
				logger.Warn("enrollment persistence: save failed",
					"path", enrollmentStatePath,
					"error_str", err.Error())
			}
		}
		// Receipts: NDJSON append-only. Per-seal cost is
		// O(receipts in this block) regardless of total
		// receipt-store size, so this stays sub-millisecond
		// even after the chain has accumulated millions of
		// receipts. The legacy O(N_total) save was migrated
		// out at boot.
		if adminReceipts != nil {
			if _, err := adminReceipts.AppendBlockNDJSON(receiptsNDJSONPath, blk.Height); err != nil {
				logger.Warn("receipts persistence: append failed",
					"height", blk.Height,
					"path", receiptsNDJSONPath,
					"error_str", err.Error())
			}
		}
	}

	// Single-validator genesis seal. /api/v1/mining/work
	// requires the producer to have at least one sealed
	// block — without it, every miner request returns 503
	// "mining: work unavailable" because chainAdapter has no
	// HeaderHashAt(0) to anchor the proof. On a fresh BLR1-
	// only network there are no peer validators to drive
	// BFT-committed external blocks via TryAppendExternalBlock,
	// so we must seal genesis ourselves. The producer's POL
	// and BFT gates short-circuit when len(bp.chain) == 0
	// (see pkg/chain/block.go:229-241), making this the only
	// height at which a solo validator can advance the tip
	// without violating consensus invariants.
	//
	// The preSealBFTRound hook installed above (line ~855)
	// trips a type guard that requires the applier to be a
	// *AccountStore — our live applier is the
	// EnrollmentAwareApplier wrapper, so we must temporarily
	// clear preSealBFTRound, seal, then restore. The
	// synthetic BFT round is unnecessary at genesis (the BFT
	// and POL gates aren't active yet anyway) so skipping it
	// is safe.
	if !adminProducer.HasTip() {
		// In solo mode, fund and use the blockdriver's
		// canonical funder address so the genesis tx and the
		// driver's subsequent reward txs share a single nonce
		// stream. Outside solo mode (peer cluster), genesis
		// would arrive via TryAppendExternalBlock from a
		// peer's BFT-committed proposal, so this whole branch
		// is dead code; we still run it for resilience —
		// worst case the chain gets a 1-CELL transfer at
		// height 0 that any peer's chain would replay safely.
		genesisFunder := blockdriver.FunderAddress

		// Genesis prefund: when QSDM_GENESIS_PREFUND_ADDR is
		// set (canonical for v2 testnet activation per spec
		// §10.3 — the fresh chain commits at least one
		// pre-funded operator wallet so an HMAC enrollment
		// can land before any block reward has emitted), the
		// genesis tx becomes a transfer of
		// QSDM_GENESIS_PREFUND_AMOUNT_CELL from the funder to
		// that address. The default branch (env unset) keeps
		// the legacy 1-CELL transfer to qsdm-genesis-anchor —
		// useful for the no-prefund testnet where operators
		// arrive after some block rewards have minted CELL
		// the natural way.
		genesisDest := "qsdm-genesis-anchor"
		genesisAmount := 1.0
		genesisPrefundAddr := strings.TrimSpace(os.Getenv("QSDM_GENESIS_PREFUND_ADDR"))
		if genesisPrefundAddr != "" {
			rawAmount := strings.TrimSpace(os.Getenv("QSDM_GENESIS_PREFUND_AMOUNT_CELL"))
			parsedAmount, parseErr := strconv.ParseFloat(rawAmount, 64)
			switch {
			case parseErr != nil || parsedAmount <= 0:
				logger.Warn("QSDM_GENESIS_PREFUND_ADDR set but AMOUNT_CELL missing/invalid; ignoring prefund",
					"addr", genesisPrefundAddr,
					"raw_amount", rawAmount,
					"parse_error_str", func() string {
						if parseErr != nil {
							return parseErr.Error()
						}
						return ""
					}())
			default:
				genesisDest = genesisPrefundAddr
				genesisAmount = parsedAmount
				logger.Info("Genesis prefund configured",
					"addr", genesisDest,
					"amount_cell", genesisAmount,
					"env_addr", "QSDM_GENESIS_PREFUND_ADDR",
					"env_amount", "QSDM_GENESIS_PREFUND_AMOUNT_CELL")
			}
		}

		// Save & clear preSealBFTRound only when it's set
		// (non-solo). The solo-mode skip earlier means it is
		// already nil and Restore would be a no-op; we still
		// re-set it on the off chance the caller flips solo
		// mode mid-boot via another goroutine.
		var preSealRestore func(blk *chain.Block) error
		if !soloValidatorMode {
			preSealRestore = func(blk *chain.Block) error {
				return chain.RunSyntheticBFTRoundWithExecutor(bftExec, nodeValidatorSet, blk)
			}
			adminProducer.SetPreSealBFTRound(nil)
		}
		// Seed the funder. In solo mode the blockdriver was
		// already constructed and credited the funder from
		// blockdriver.DefaultFunderBalance — Credit here adds
		// 1 more CELL on top, harmless. Outside solo mode
		// this is a fresh credit. When prefund is configured
		// we top up to genesisAmount + 1 to guarantee the
		// transfer (and any nonce-1 follow-on) succeeds even
		// if the blockdriver auto-credit didn't run for some
		// reason.
		adminAccounts.Credit(genesisFunder, genesisAmount+1.0)
		seedTx := &mempool.Tx{
			ID:        fmt.Sprintf("genesis-seed-%d", time.Now().UnixNano()),
			Sender:    genesisFunder,
			Recipient: genesisDest,
			Amount:    genesisAmount,
			Fee:       0,
			Nonce:     0,
			AddedAt:   time.Now(),
		}
		if err := adminPool.Add(seedTx); err != nil {
			logger.Warn("Genesis seal: mempool admission failed; chain will stay at tip=0 and /api/v1/mining/work will return 503 until a peer-driven external block lands",
				"error_str", err.Error())
		} else {
			blk, prodErr := adminProducer.ProduceBlock()
			if prodErr != nil {
				logger.Warn("Genesis seal: ProduceBlock failed; chain stays at tip=0",
					"error_str", prodErr.Error(),
					"validator_set_active", len(nodeValidatorSet.ActiveValidators()),
					"mempool_size", adminPool.Size())
			} else if blk != nil {
				logger.Info("Genesis block sealed by solo validator; chain tip advanced",
					"height", blk.Height,
					"hash", blk.Hash,
					"tx_count", len(blk.Transactions),
					"prefund_addr", genesisDest,
					"prefund_amount_cell", genesisAmount)
			} else {
				logger.Warn("Genesis seal: ProduceBlock returned nil with no error")
			}
		}
		if preSealRestore != nil {
			adminProducer.SetPreSealBFTRound(preSealRestore)
		}
	}

	// Start the solo-mode block driver after genesis has
	// settled. If the genesis seal failed (e.g. mempool
	// admission rejected the seed for an unexpected reason),
	// the driver will still try every Period — its own
	// heartbeat tx funds the chain forward.
	if soloDriver != nil {
		// Re-sync the driver's funder nonce. The genesis seal
		// above used the same funder account at nonce=0, so
		// the AccountStore now has funder.Nonce=1 but the
		// driver's in-memory counter is still 0. Without this
		// call the very first tick would issue a reward tx at
		// nonce=0 → ApplyTx rejects → "all transactions failed
		// state application" → tip never advances past 0.
		soloDriver.SyncFunderNonce()
		// Use a fresh background context so the driver
		// outlives any request-scoped contexts. Stop is
		// triggered by the existing graceful-shutdown handler
		// (SIGINT/SIGTERM); see the deferred soloDriver.Stop()
		// registered just below.
		soloDriver.Start(context.Background())
		// Best-effort hook into the existing shutdown path —
		// the validator's main shutdown closure runs on Ctrl-C
		// and SIGTERM and should drain in-flight ticks before
		// closing the network.
		defer soloDriver.Stop()
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
		auditSecret = "qsdm-admin-audit-default"
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
		// Quarantine transparency surface. Mirrors the trust_aggregator
		// collector's contract: nil-safe, O(1) per scrape, closes over
		// live state so /metrics always reflects the latest Stats()
		// without a parallel update path. Paired with the
		// qsdm-quarantine alert group in
		// deploy/prometheus/alerts_qsdm.example.yml.
		pe.RegisterCollector("quarantine_manager", quarantine.MetricsCollector(quarantineManager))
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
				stack := debug.Stack()
				logger.Error("Panic during API server initialization",
					"error_str", fmt.Sprintf("%v", r),
					"stack", string(stack))
				log.Printf("API server initialization panic: %v (this is normal if CGO/liboqs is not available)\nstack:\n%s", r, stack)
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
			// Wire the live chain tip into /api/v1/status so the
			// status endpoint reflects what the producer
			// actually has. Without this hook the response
			// hardcodes chain_tip=0, which made the
			// blockdriver advance look invisible.
			apiServer.SetChainTipSource(func() uint64 {
				if adminProducer == nil {
					return 0
				}
				return adminProducer.TipHeight()
			})
			apiServer.SetPeerCountSource(func() int {
				if net == nil || net.Host == nil {
					return 0
				}
				return len(net.Host.Network().Peers())
			})
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

// bringUpWorkSet returns the deterministic v1 mining workset
// the validator serves to every miner during the bring-up
// posture. Two requirements:
//
//  1. Stable across runs and platforms (every miner sees the
//     same WorkSet.Root() so DAG entries match between
//     miner and validator).
//  2. Passes mining.WorkSet.Validate() — which means each
//     batch must have 3..5 cells, and each cell ID must be
//     non-empty and globally unique within the workset.
//
// accountProbeFromStore adapts a *chain.AccountStore to the
// api.MiningAccountProbe interface. Only used in solo mode;
// see api.SetMiningAccountProbe.
type accountStoreProbe struct {
	store *chain.AccountStore
}

func (p accountStoreProbe) BalanceOf(address string) (float64, uint64, bool) {
	if p.store == nil {
		return 0, 0, false
	}
	acc, ok := p.store.Get(address)
	if !ok || acc == nil {
		return 0, 0, false
	}
	return acc.Balance, acc.Nonce, true
}

func accountProbeFromStore(store *chain.AccountStore) api.MiningAccountProbe {
	return accountStoreProbe{store: store}
}

// emissionStoreProbe adapts a *chain.BlockProducer + the
// canonical chain.DefaultEmissionSchedule to
// api.MiningEmissionProbe. Lives in cmd/qsdm because the
// schedule choice is operator policy, not consensus — peer
// nodes pick their own at boot from the same defaults.
type emissionStoreProbe struct {
	producer *chain.BlockProducer
	schedule chain.EmissionSchedule
}

func (p emissionStoreProbe) Snapshot() api.MiningEmissionSnapshot {
	var tip uint64
	if p.producer != nil && p.producer.HasTip() {
		tip = p.producer.TipHeight()
	}
	rewardDust := p.schedule.BlockRewardDust(tip + 1)
	return api.MiningEmissionSnapshot{
		ChainTip:               tip,
		MiningCapDust:          p.schedule.MiningCapDust,
		BlocksPerEpoch:         p.schedule.BlocksPerEpoch,
		TargetBlockTimeSeconds: p.schedule.TargetBlockTimeSeconds,
		CurrentEpoch:           p.schedule.EpochForHeight(tip),
		BlockRewardDust:        rewardDust,
		BlockRewardCell:        p.schedule.BlockRewardCell(tip + 1),
		EmittedDust:            p.schedule.CumulativeEmittedDust(tip),
		EmittedCell:            formatDustAsCellLocal(p.schedule.CumulativeEmittedDust(tip)),
		RemainingDust:          p.schedule.RemainingSupplyDust(tip),
		NextHalvingHeight:      p.schedule.NextHalvingHeight(tip),
		NextHalvingETASeconds:  p.schedule.NextHalvingETA(tip),
	}
}

func emissionProbeFromProducer(p *chain.BlockProducer) api.MiningEmissionProbe {
	return emissionStoreProbe{
		producer: p,
		schedule: chain.DefaultEmissionSchedule(),
	}
}

// blocksProbeFromProducer adapts a *chain.BlockProducer to
// api.MiningBlocksProbe so the public chain dashboard can list
// the last N block headers.
//
// The probe is intentionally O(N) over bp.AllBlocks() per call
// because (a) AllBlocks already takes the bp.mu lock and returns
// a snapshot copy so we don't hold the producer lock while
// projecting, and (b) for the current testnet the chain is
// small enough that a full walk to filter by [from, to] runs in
// microseconds. When the chain grows past a few hundred thousand
// blocks, replace this with a height-indexed accessor in
// pkg/chain/block.go (BlockProducer.BlockAtHeight) and switch
// HeadersInRange to a slice operation.
type blocksStoreProbe struct {
	producer *chain.BlockProducer
}

func (p blocksStoreProbe) Tip() uint64 {
	if p.producer == nil || !p.producer.HasTip() {
		return 0
	}
	return p.producer.TipHeight()
}

func (p blocksStoreProbe) HeadersInRange(from, to uint64) []api.MiningBlockHeader {
	if p.producer == nil {
		return nil
	}
	all := p.producer.AllBlocks()
	out := make([]api.MiningBlockHeader, 0, 32)
	for _, b := range all {
		if b == nil {
			continue
		}
		if b.Height < from || b.Height > to {
			continue
		}
		hdr := b.Header()
		out = append(out, api.MiningBlockHeader{
			Height:     hdr.Height,
			Hash:       hdr.Hash,
			PrevHash:   hdr.PrevHash,
			StateRoot:  hdr.StateRoot,
			TxRoot:     hdr.TxRoot,
			TxCount:    hdr.TxCount,
			Timestamp:  hdr.Timestamp.UTC().Format(time.RFC3339),
			ProducerID: b.ProducerID,
		})
	}
	return out
}

func blocksProbeFromProducer(p *chain.BlockProducer) api.MiningBlocksProbe {
	return blocksStoreProbe{producer: p}
}

// receiptStoreProbe adapts a *chain.ReceiptStore to
// api.MiningReceiptProbe so the public /api/v1/receipts/{tx_id}
// endpoint can serve per-tx outcomes from the live store. The
// projection trims fields the wire view doesn't expose (none
// today; future-proofing for an internal-only field).
type receiptStoreProbe struct {
	store *chain.ReceiptStore
}

func (p receiptStoreProbe) GetReceipt(txID string) (api.TxReceiptView, bool) {
	if p.store == nil {
		return api.TxReceiptView{}, false
	}
	r, ok := p.store.Get(txID)
	if !ok || r == nil {
		return api.TxReceiptView{}, false
	}
	logs := make([]api.TxReceiptLogView, 0, len(r.Logs))
	for _, l := range r.Logs {
		logs = append(logs, api.TxReceiptLogView{
			Topic: l.Topic,
			Data:  l.Data,
			Index: l.Index,
		})
	}
	return api.TxReceiptView{
		TxID:         r.TxID,
		BlockHeight:  r.BlockHeight,
		BlockHash:    r.BlockHash,
		Status:       uint8(r.Status),
		GasUsed:      r.GasUsed,
		Fee:          r.Fee,
		Logs:         logs,
		Error:        r.Error,
		Timestamp:    r.Timestamp.UTC().Format(time.RFC3339Nano),
		ContractID:   r.ContractID,
		IndexInBlock: r.IndexInBlock,
	}, true
}

func receiptProbeFromStore(rs *chain.ReceiptStore) api.MiningReceiptProbe {
	return receiptStoreProbe{store: rs}
}

// receiptsListProbe adapts (*chain.ReceiptStore, *chain.BlockProducer)
// to api.MiningReceiptsListProbe. The store provides the
// height-range walk; the producer provides the live tip the
// handler uses as a default `to` when the caller omits it.
//
// Both collaborators are read concurrently — RLock on the
// store, atomic read on the producer's tip — so this probe
// has no internal lock of its own.
type receiptsListProbe struct {
	store    *chain.ReceiptStore
	producer *chain.BlockProducer
}

func (p receiptsListProbe) Tip() uint64 {
	if p.producer == nil {
		return 0
	}
	return p.producer.TipHeight()
}

func (p receiptsListProbe) ListByHeightRange(from, to uint64, limit int) []api.TxReceiptView {
	if p.store == nil {
		return nil
	}
	recs := p.store.ListByHeightRange(from, to, limit)
	if len(recs) == 0 {
		return nil
	}
	out := make([]api.TxReceiptView, 0, len(recs))
	for _, r := range recs {
		if r == nil {
			continue
		}
		logs := make([]api.TxReceiptLogView, 0, len(r.Logs))
		for _, l := range r.Logs {
			logs = append(logs, api.TxReceiptLogView{
				Topic: l.Topic,
				Data:  l.Data,
				Index: l.Index,
			})
		}
		out = append(out, api.TxReceiptView{
			TxID:         r.TxID,
			BlockHeight:  r.BlockHeight,
			BlockHash:    r.BlockHash,
			Status:       uint8(r.Status),
			GasUsed:      r.GasUsed,
			Fee:          r.Fee,
			Logs:         logs,
			Error:        r.Error,
			Timestamp:    r.Timestamp.UTC().Format(time.RFC3339Nano),
			ContractID:   r.ContractID,
			IndexInBlock: r.IndexInBlock,
		})
	}
	return out
}

func receiptsListProbeFromStore(rs *chain.ReceiptStore, p *chain.BlockProducer) api.MiningReceiptsListProbe {
	return receiptsListProbe{store: rs, producer: p}
}

// formatDustAsCellLocal mirrors the helper in
// pkg/api/handlers_status.go (dust → "X.YYYYYYYY" CELL).
// Duplicated here to avoid an import cycle since the helper
// there is unexported.
func formatDustAsCellLocal(dust uint64) string {
	whole := dust / chain.DustPerCell
	frac := dust % chain.DustPerCell
	return fmt.Sprintf("%d.%0*d", whole, chain.CellDecimals, frac)
}

// challengeKeyLen is the on-disk size of the validator's
// challenge-issuer HMAC key. 32 bytes matches HMAC-SHA256's
// natural key size and exceeds the 16-byte minimum the
// challenge package enforces (see pkg/mining/challenge/hmac_signer.go).
const challengeKeyLen = 32

// loadOrCreateChallengeKey reads the per-validator challenge
// HMAC key from path. On first boot (file does not exist) it
// generates a fresh 32-byte key from crypto/rand, writes it
// with 0o600 perms (owner read/write only) and returns it.
//
// Returned values:
//
//	signerID — opaque-but-stable identifier for this key,
//	           rendered as the lowercase hex of the first 8
//	           key bytes prefixed with "validator-". This is
//	           the value miners see in
//	           Challenge.SignerID and the value the
//	           HMACSignerVerifier registers under. Knowing the
//	           signer_id leaks NOTHING about the secret —
//	           HMAC-SHA256 with a 32-byte key is preimage-
//	           resistant — so deriving it from the key is
//	           safe and saves a separate id-management step.
//	key      — 32 raw bytes. Never logged.
//	err      — read / write / generate failure. Boot fails;
//	           the caller log.Fatalfs.
//
// The "auto-generate on first boot" posture is deliberately
// permissive for a solo validator. Multi-validator setups
// will replace this with an explicit genesis-time key
// distribution step (each operator hand-edits their key
// file and shares the corresponding public hex SignerID
// over a side channel so peers can Register one another).
func loadOrCreateChallengeKey(path string) (signerID string, key []byte, err error) {
	if path == "" {
		return "", nil, errors.New("empty challenge key path")
	}
	if existing, readErr := os.ReadFile(path); readErr == nil {
		if len(existing) != challengeKeyLen {
			return "", nil, fmt.Errorf(
				"challenge key at %s has length %d, expected %d (delete the file to regenerate)",
				path, len(existing), challengeKeyLen)
		}
		return deriveChallengeSignerID(existing), existing, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return "", nil, fmt.Errorf("read challenge key %s: %w", path, readErr)
	}
	fresh := make([]byte, challengeKeyLen)
	if _, randErr := cryptorand.Read(fresh); randErr != nil {
		return "", nil, fmt.Errorf("generate challenge key: %w", randErr)
	}
	if writeErr := os.WriteFile(path, fresh, 0o600); writeErr != nil {
		return "", nil, fmt.Errorf("persist challenge key %s: %w", path, writeErr)
	}
	return deriveChallengeSignerID(fresh), fresh, nil
}

// deriveChallengeSignerID returns the deterministic
// SignerID-from-key derivation the challenge issuer uses.
// Format: "validator-" || lowercase-hex(key[:8]). 8 bytes is
// 64 bits of entropy — beyond practical collision range for
// the validator population we'll ever ship. Stable across
// restarts because the key file is stable.
func deriveChallengeSignerID(key []byte) string {
	if len(key) < 8 {
		return "validator-shortkey"
	}
	return fmt.Sprintf("validator-%x", key[:8])
}

// The cells below are 16-byte synthetic IDs derived from a
// fixed lexicographic prefix; the content hashes are the
// SHA-256-style 32-byte arrays with deterministic byte
// patterns. Nothing here is consensus-critical beyond
// "miner and validator must agree" — once a per-epoch
// derivation lands (Phase 4.6), this helper is deleted and
// the WorkSet comes from chain state.
func bringUpWorkSet() mining.WorkSet {
	mkCell := func(prefix byte, idx int) mining.ParentCellRef {
		id := []byte{
			0xC0, 0xFF, 0xEE, 0x01,
			prefix, byte(idx),
			0xDE, 0xAD, 0xBE, 0xEF,
			0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		}
		var ch [32]byte
		ch[0] = prefix
		ch[1] = byte(idx)
		ch[2] = 0xA5
		return mining.ParentCellRef{ID: id, ContentHash: ch}
	}
	ws := mining.WorkSet{Batches: []mining.Batch{
		{Cells: []mining.ParentCellRef{mkCell('a', 0), mkCell('a', 1), mkCell('a', 2)}},
		{Cells: []mining.ParentCellRef{mkCell('b', 0), mkCell('b', 1), mkCell('b', 2)}},
		{Cells: []mining.ParentCellRef{mkCell('c', 0), mkCell('c', 1), mkCell('c', 2)}},
	}}
	ws.Canonicalize()
	return ws
}
