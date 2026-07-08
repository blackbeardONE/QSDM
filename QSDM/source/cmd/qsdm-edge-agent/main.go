package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/edgepool"
)

var version = "dev"

type agentFileConfig struct {
	Relay       string   `json:"relay,omitempty"`
	Coordinator string   `json:"coordinator,omitempty"`
	TokenFile   string   `json:"token_file"`
	WorkerID    string   `json:"worker_id"`
	Resources   []string `json:"resources"`
	RAMMiB      uint64   `json:"ram_mib"`
	CPUUnits    uint64   `json:"cpu_units"`
	GPUUnits    uint64   `json:"gpu_units"`
	GPUHelper   string   `json:"gpu_helper,omitempty"`
	PollSeconds int      `json:"poll_seconds"`
	LogFile     string   `json:"log_file,omitempty"`
}

type coordinatorServiceOptions struct {
	TokenFile        string // legacy shared credential
	AgentTokenFile   string
	MotherTokenFile  string
	Listen           string
	ID               string
	AllowLAN         bool
	CPUUnits         uint64
	GPUUnits         uint64
	RAMMiB           uint64
	CPUPercent       int
	GPUPercent       int
	RAMPercent       int
	MaxVerifications int
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "qsdm-edge-agent:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return errors.New("a command is required")
	}
	switch args[0] {
	case "version", "--version", "-version":
		fmt.Printf("qsdm-edge-agent %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	case "token":
		return runToken(args[1:])
	case "relay", "coordinator":
		return runRelay(args[1:])
	case "agent":
		return runAgent(args[1:])
	case "configure-agent":
		return runConfigureAgent(args[1:])
	case "status":
		return runStatus(args[1:])
	case "compute":
		return runCompute(args[1:])
	case "install-service":
		return runInstallService(args[1:])
	case "uninstall-service":
		return uninstallAgentService()
	case "service-status":
		return showAgentServiceStatus()
	case "install-coordinator-service":
		return runInstallCoordinatorService(args[1:])
	case "install-relay-service":
		return runInstallCoordinatorService(args[1:])
	case "uninstall-coordinator-service":
		return uninstallCoordinatorService()
	case "uninstall-relay-service":
		return uninstallCoordinatorService()
	case "coordinator-service-status":
		return showCoordinatorServiceStatus()
	case "relay-service-status":
		return showCoordinatorServiceStatus()
	case "help", "--help", "-h":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runToken(args []string) error {
	flags := flag.NewFlagSet("token", flag.ContinueOnError)
	output := flags.String("out", "edge-pool.token", "token file to create")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if _, err := os.Stat(*output); err == nil {
		return fmt.Errorf("refusing to overwrite existing token file %q", *output)
	}
	if err := os.MkdirAll(filepath.Dir(absoluteOrCurrent(*output)), 0o700); err != nil {
		return err
	}
	token, err := edgepool.GenerateToken()
	if err != nil {
		return err
	}
	if err := edgepool.WriteTokenFile(*output, token); err != nil {
		return err
	}
	fmt.Printf("Created edge-pool token: %s\n", *output)
	return nil
}

func runCompute(args []string) error {
	if len(args) == 0 {
		return errors.New("compute requires submit, status, list, or cancel")
	}
	switch args[0] {
	case "submit":
		flags := flag.NewFlagSet("compute submit", flag.ContinueOnError)
		gateway := flags.String("gateway", "http://127.0.0.1:7742", "Mother Hive Compute Gateway URL")
		tokenFile := flags.String("token-file", defaultComputeGatewayTokenFile(), "Compute Gateway token file")
		requestID := flags.String("request-id", "", "idempotent application request id")
		resource := flags.String("resource", "cpu", "cpu, gpu, or ram")
		units := flags.Uint64("units", 0, "CPU/GPU work units; zero uses the Relay policy")
		ramMiB := flags.Uint64("ram-mib", 0, "RAM MiB; zero uses the Relay policy")
		deadline := flags.Uint64("deadline-seconds", 900, "job deadline from 30 to 3600 seconds")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *requestID == "" {
			randomID := make([]byte, 8)
			if _, err := rand.Read(randomID); err != nil {
				return err
			}
			*requestID = "app-" + strconv.FormatInt(time.Now().UTC().Unix(), 10) + "-" + hex.EncodeToString(randomID)
		}
		request := edgepool.ComputeJobSubmitRequest{
			Version:         edgepool.ComputeProtocolVersion,
			ClientRequestID: *requestID,
			Resource:        edgepool.ResourceKind(strings.ToLower(strings.TrimSpace(*resource))),
			Units:           *units,
			MemoryMiB:       *ramMiB,
			DeadlineSeconds: *deadline,
		}
		return computeGatewayJSON(http.MethodPost, *gateway, "/v1/jobs", *tokenFile, request)
	case "status", "cancel":
		flags := flag.NewFlagSet("compute "+args[0], flag.ContinueOnError)
		gateway := flags.String("gateway", "http://127.0.0.1:7742", "Mother Hive Compute Gateway URL")
		tokenFile := flags.String("token-file", defaultComputeGatewayTokenFile(), "Compute Gateway token file")
		jobID := flags.String("id", "", "compute job id")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if decoded, err := hex.DecodeString(*jobID); err != nil || len(decoded) != 16 {
			return errors.New("--id must be a 16-byte hexadecimal compute job id")
		}
		method := http.MethodGet
		if args[0] == "cancel" {
			method = http.MethodDelete
		}
		return computeGatewayJSON(method, *gateway, "/v1/jobs/"+strings.ToLower(*jobID), *tokenFile, nil)
	case "list":
		flags := flag.NewFlagSet("compute list", flag.ContinueOnError)
		gateway := flags.String("gateway", "http://127.0.0.1:7742", "Mother Hive Compute Gateway URL")
		tokenFile := flags.String("token-file", defaultComputeGatewayTokenFile(), "Compute Gateway token file")
		limit := flags.Int("limit", 20, "number of recent jobs from 1 to 100")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *limit < 1 || *limit > 100 {
			return errors.New("--limit must be between 1 and 100")
		}
		return computeGatewayJSON(http.MethodGet, *gateway, "/v1/jobs?limit="+strconv.Itoa(*limit), *tokenFile, nil)
	default:
		return fmt.Errorf("unknown compute command %q", args[0])
	}
}

func computeGatewayJSON(method, gatewayURL, requestPath, tokenFile string, requestBody any) error {
	tokenRaw, err := os.ReadFile(tokenFile)
	if err != nil {
		return fmt.Errorf("read Compute Gateway token: %w", err)
	}
	token := strings.ToLower(strings.TrimSpace(string(tokenRaw)))
	if decoded, err := hex.DecodeString(token); err != nil || len(decoded) != 32 {
		return errors.New("Compute Gateway token must contain 64 hexadecimal characters")
	}
	base := strings.TrimRight(strings.TrimSpace(gatewayURL), "/")
	if base != "http://127.0.0.1:7742" && !strings.HasPrefix(base, "http://127.0.0.1:") && !strings.HasPrefix(base, "http://localhost:") {
		return errors.New("Compute Gateway must use a loopback HTTP address")
	}
	body := []byte(nil)
	if requestBody != nil {
		body, err = json.Marshal(requestBody)
		if err != nil {
			return err
		}
	}
	request, err := http.NewRequest(method, base+requestPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	responseRaw, err := io.ReadAll(io.LimitReader(response.Body, 256*1024+1))
	if err != nil {
		return err
	}
	if len(responseRaw) > 256*1024 {
		return errors.New("Compute Gateway response exceeded 256 KiB")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Compute Gateway returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseRaw)))
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, responseRaw, "", "  "); err != nil {
		return fmt.Errorf("decode Compute Gateway response: %w", err)
	}
	fmt.Println(formatted.String())
	return nil
}

func defaultComputeGatewayTokenFile() string {
	if configured := strings.TrimSpace(os.Getenv("QSDM_COMPUTE_GATEWAY_TOKEN_FILE")); configured != "" {
		return configured
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "compute-gateway.token"
	}
	return filepath.Join(configDir, "QSDM-Hive", "namespace", "qsdm-mother-hive", "compute-gateway.token")
}

func runRelay(args []string) error {
	flags := flag.NewFlagSet("relay", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:7740", "relay listen address")
	tokenFile := flags.String("token-file", "", "legacy shared HMAC token file")
	agentTokenFile := flags.String("agent-token-file", "", "HMAC token shared only with agents")
	motherTokenFile := flags.String("mother-token-file", "", "HMAC token shared only with the QSDM Hive Mother role")
	stateDir := flags.String("state-dir", defaultStateDir(), "persistent relay state directory")
	id := flags.String("id", "", "stable relay identifier")
	allowLAN := flags.Bool("allow-lan", false, "allow a non-loopback LAN listener")
	cpuUnits := flags.Uint64("cpu-units", 250_000, "CPU iterations issued per job")
	gpuUnits := flags.Uint64("gpu-units", 5_000_000, "GPU mix operations issued per job")
	ramMiB := flags.Uint64("ram-mib", 64, "RAM MiB issued per job")
	cpuPercent := flags.Int("cpu-percent", 50, "percentage of the CPU per-job ceiling relayed to QSDM Hive")
	gpuPercent := flags.Int("gpu-percent", 40, "percentage of the GPU per-job ceiling relayed to QSDM Hive")
	ramPercent := flags.Int("ram-percent", 25, "percentage of the RAM per-job ceiling relayed to QSDM Hive")
	maxVerifications := flags.Int("max-verifications", 2, "maximum simultaneous result verifications")
	if err := flags.Parse(args); err != nil {
		return err
	}
	agentPath, motherPath, err := resolveRelayTokenPaths(*tokenFile, *agentTokenFile, *motherTokenFile)
	if err != nil {
		return err
	}
	if !isLoopbackListen(*listen) && !*allowLAN {
		return errors.New("a non-loopback relay requires explicit --allow-lan; restrict the OS firewall to the private laboratory network")
	}
	agentToken, err := edgepool.LoadTokenFile(agentPath)
	if err != nil {
		return err
	}
	motherToken, err := edgepool.LoadTokenFile(motherPath)
	if err != nil {
		return err
	}
	relay, err := edgepool.NewRelay(edgepool.RelayConfig{
		ID:               *id,
		ListenAddress:    *listen,
		AgentToken:       agentToken,
		MotherToken:      motherToken,
		StateDir:         *stateDir,
		CPUUnits:         *cpuUnits,
		GPUUnits:         *gpuUnits,
		RAMMiB:           *ramMiB,
		CPUPercent:       *cpuPercent,
		GPUPercent:       *gpuPercent,
		RAMPercent:       *ramPercent,
		MaxVerifications: *maxVerifications,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("QSDM edge relay listening on %s (CPU %d%%, GPU %d%%, RAM %d%%)\n", *listen, *cpuPercent, *gpuPercent, *ramPercent)
	fmt.Println("Only authenticated built-in CPU, RAM, and GPU work is accepted; remote shell execution is not supported.")
	return relay.Serve(ctx)
}

func runAgent(args []string) error {
	flags := flag.NewFlagSet("agent", flag.ContinueOnError)
	configPath := flags.String("config", "", "optional JSON agent configuration")
	relay := flags.String("relay", "", "relay URL")
	coordinator := flags.String("coordinator", "", "legacy alias for --relay")
	tokenFile := flags.String("token-file", "", "agent-to-relay HMAC token file")
	workerID := flags.String("worker-id", "", "stable worker identifier")
	resources := flags.String("resources", "", "comma-separated cpu,ram,gpu resources")
	ramMiB := flags.Uint64("ram-mib", 0, "maximum RAM MiB contributed by this agent")
	cpuUnits := flags.Uint64("cpu-units", 0, "maximum CPU iterations per job")
	gpuUnits := flags.Uint64("gpu-units", 0, "maximum GPU operations per job")
	gpuHelper := flags.String("gpu-helper", "", "trusted qsdm-edge-gpu-helper path")
	pollSeconds := flags.Int("poll-seconds", 0, "seconds between completed jobs")
	logFile := flags.String("log-file", "", "agent log file")
	silent := flags.Bool("silent", false, "write only to the log file")
	background := flags.Bool("background", false, "detach without a visible terminal window")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *background && os.Getenv("QSDM_EDGE_AGENT_BACKGROUND") != "1" {
		pid, err := launchBackground(append([]string{"agent"}, args...))
		if err != nil {
			return err
		}
		if !*silent {
			fmt.Printf("QSDM edge agent started in background with PID %d\n", pid)
		}
		return nil
	}

	fileConfig := agentFileConfig{}
	if *configPath != "" {
		if err := readStrictJSON(*configPath, &fileConfig); err != nil {
			return err
		}
	}
	requestedRelay := firstNonEmpty(*relay, *coordinator)
	applyAgentOverrides(&fileConfig, requestedRelay, *tokenFile, *workerID, *resources, *ramMiB, *cpuUnits, *gpuUnits, *gpuHelper, *pollSeconds, *logFile)
	relayURL := agentRelayURL(fileConfig)
	if relayURL == "" || fileConfig.TokenFile == "" {
		return errors.New("relay and token_file are required in flags or config")
	}
	if fileConfig.WorkerID == "" {
		fileConfig.WorkerID = defaultWorkerID()
	}
	token, err := edgepool.LoadTokenFile(fileConfig.TokenFile)
	if err != nil {
		return err
	}
	resourceKinds, err := parseResources(fileConfig.Resources)
	if err != nil {
		return err
	}
	if fileConfig.LogFile == "" {
		fileConfig.LogFile = edgepool.DefaultAgentLogPath()
	}
	logger, closeLog, err := newAgentLogger(fileConfig.LogFile, *silent)
	if err != nil {
		return err
	}
	defer closeLog()
	agent, err := edgepool.NewAgent(edgepool.AgentConfig{
		RelayURL:      relayURL,
		Token:         token,
		WorkerID:      fileConfig.WorkerID,
		AgentVersion:  version,
		Resources:     resourceKinds,
		RAMMiB:        fileConfig.RAMMiB,
		CPUUnits:      fileConfig.CPUUnits,
		GPUUnits:      fileConfig.GPUUnits,
		GPUHelperPath: fileConfig.GPUHelper,
		PollInterval:  time.Duration(fileConfig.PollSeconds) * time.Second,
		Logger:        logger,
	})
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Printf("starting qsdm-edge-agent version=%s worker=%s", version, fileConfig.WorkerID)
	return agent.Run(ctx)
}

func runConfigureAgent(args []string) error {
	flags := flag.NewFlagSet("configure-agent", flag.ContinueOnError)
	output := flags.String("out", "qsdm-edge-agent.json", "configuration file to create")
	relay := flags.String("relay", "", "relay URL")
	coordinator := flags.String("coordinator", "", "legacy alias for --relay")
	tokenFile := flags.String("token-file", "", "agent-to-relay HMAC token file")
	workerID := flags.String("worker-id", defaultWorkerID(), "stable worker identifier")
	resources := flags.String("resources", "cpu,ram", "comma-separated cpu,ram,gpu resources")
	ramMiB := flags.Uint64("ram-mib", 256, "maximum RAM MiB to contribute")
	cpuUnits := flags.Uint64("cpu-units", 500_000, "maximum CPU iterations per job")
	gpuUnits := flags.Uint64("gpu-units", 10_000_000, "maximum GPU operations per job")
	gpuHelper := flags.String("gpu-helper", "", "trusted qsdm-edge-gpu-helper path")
	pollSeconds := flags.Int("poll-seconds", 5, "seconds between completed jobs")
	logFile := flags.String("log-file", edgepool.DefaultAgentLogPath(), "silent agent log file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	relayURL := firstNonEmpty(*relay, *coordinator)
	if relayURL == "" || *tokenFile == "" {
		return errors.New("--relay and --token-file are required")
	}
	resourceKinds, err := parseResources(strings.Split(*resources, ","))
	if err != nil {
		return err
	}
	resourceNames := make([]string, len(resourceKinds))
	for index, resource := range resourceKinds {
		resourceNames[index] = string(resource)
	}
	config := agentFileConfig{
		Relay:       relayURL,
		TokenFile:   *tokenFile,
		WorkerID:    *workerID,
		Resources:   resourceNames,
		RAMMiB:      *ramMiB,
		CPUUnits:    *cpuUnits,
		GPUUnits:    *gpuUnits,
		GPUHelper:   *gpuHelper,
		PollSeconds: *pollSeconds,
		LogFile:     *logFile,
	}
	if err := writeExclusiveJSON(*output, config); err != nil {
		return err
	}
	fmt.Printf("Created agent config: %s\n", *output)
	fmt.Printf("Run silently: qsdm-edge-agent agent --config %q --silent\n", *output)
	return nil
}

func runStatus(args []string) error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	relay := flags.String("relay", "", "relay URL")
	coordinator := flags.String("coordinator", "", "legacy alias for --relay")
	tokenFile := flags.String("token-file", "", "legacy shared HMAC token file")
	motherTokenFile := flags.String("mother-token-file", "", "QSDM Hive Mother-role HMAC token file")
	workerID := flags.String("worker-id", defaultWorkerID()+"-status", "request identity")
	if err := flags.Parse(args); err != nil {
		return err
	}
	relayURL := firstNonEmpty(*relay, *coordinator, "http://127.0.0.1:7740")
	motherPath := firstNonEmpty(*motherTokenFile, *tokenFile)
	if motherPath == "" {
		return errors.New("--mother-token-file is required")
	}
	token, err := edgepool.LoadTokenFile(motherPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := edgepool.QueryStatus(ctx, relayURL, *workerID, token)
	if err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(status, "", "  ")
	fmt.Println(string(raw))
	return nil
}

func runInstallService(args []string) error {
	flags := flag.NewFlagSet("install-service", flag.ContinueOnError)
	configPath := flags.String("config", "", "agent JSON configuration to install")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*configPath) == "" {
		return errors.New("--config is required")
	}
	message, err := installAgentService(*configPath)
	if err != nil {
		return err
	}
	fmt.Println(message)
	return nil
}

func runInstallCoordinatorService(args []string) error {
	flags := flag.NewFlagSet("install-relay-service", flag.ContinueOnError)
	tokenFile := flags.String("token-file", "", "legacy shared HMAC token file")
	agentTokenFile := flags.String("agent-token-file", "", "HMAC token shared only with agents")
	motherTokenFile := flags.String("mother-token-file", "", "HMAC token shared only with the QSDM Hive Mother role")
	listen := flags.String("listen", "127.0.0.1:7740", "relay listen address")
	id := flags.String("id", "", "stable relay identifier")
	allowLAN := flags.Bool("allow-lan", false, "allow a non-loopback LAN listener")
	cpuUnits := flags.Uint64("cpu-units", 250_000, "CPU iterations issued per job")
	gpuUnits := flags.Uint64("gpu-units", 5_000_000, "GPU mix operations issued per job")
	ramMiB := flags.Uint64("ram-mib", 64, "RAM MiB issued per job")
	cpuPercent := flags.Int("cpu-percent", 50, "percentage of CPU capacity relayed to QSDM Hive")
	gpuPercent := flags.Int("gpu-percent", 40, "percentage of GPU capacity relayed to QSDM Hive")
	ramPercent := flags.Int("ram-percent", 25, "percentage of RAM capacity relayed to QSDM Hive")
	maxVerifications := flags.Int("max-verifications", 2, "maximum simultaneous result verifications")
	if err := flags.Parse(args); err != nil {
		return err
	}
	agentPath, motherPath, err := resolveRelayTokenPaths(*tokenFile, *agentTokenFile, *motherTokenFile)
	if err != nil {
		return err
	}
	if !isLoopbackListen(*listen) && !*allowLAN {
		return errors.New("a non-loopback relay requires explicit --allow-lan; restrict the OS firewall to the private laboratory network")
	}
	message, err := installCoordinatorService(coordinatorServiceOptions{
		TokenFile:        *tokenFile,
		AgentTokenFile:   agentPath,
		MotherTokenFile:  motherPath,
		Listen:           *listen,
		ID:               *id,
		AllowLAN:         *allowLAN,
		CPUUnits:         *cpuUnits,
		GPUUnits:         *gpuUnits,
		RAMMiB:           *ramMiB,
		CPUPercent:       *cpuPercent,
		GPUPercent:       *gpuPercent,
		RAMPercent:       *ramPercent,
		MaxVerifications: *maxVerifications,
	})
	if err != nil {
		return err
	}
	fmt.Println(message)
	return nil
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "QSDM Agent and Relay utilities for the QSDM Hive Mother role")
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  token             Generate a 256-bit pool token file")
	fmt.Fprintln(w, "  relay             Aggregate agents and enforce QSDM Hive resource limits")
	fmt.Fprintln(w, "  coordinator       Legacy alias for relay")
	fmt.Fprintln(w, "  configure-agent   Create a worker configuration file")
	fmt.Fprintln(w, "  agent             Run an outbound-only CPU/RAM/GPU worker")
	fmt.Fprintln(w, "  status            Show authenticated pool status")
	fmt.Fprintln(w, "  compute           Submit and inspect local Mother Hive application jobs")
	fmt.Fprintln(w, "  install-service   Install and start the persistent Linux user service (Linux only)")
	fmt.Fprintln(w, "  uninstall-service Stop and remove the Linux user service (Linux only)")
	fmt.Fprintln(w, "  service-status    Show Linux user-service status and recent logs (Linux only)")
	fmt.Fprintln(w, "  install-relay-service         Install the Linux relay user service (Linux only)")
	fmt.Fprintln(w, "  uninstall-relay-service       Remove the Linux relay service (Linux only)")
	fmt.Fprintln(w, "  relay-service-status          Show Linux relay service status (Linux only)")
	fmt.Fprintln(w, "  *-coordinator-service         Legacy aliases for relay service commands")
	fmt.Fprintln(w, "  version           Show binary version")
}

func applyAgentOverrides(config *agentFileConfig, relay, tokenFile, workerID, resources string, ramMiB, cpuUnits, gpuUnits uint64, gpuHelper string, pollSeconds int, logFile string) {
	if relay != "" {
		config.Relay = relay
	}
	if tokenFile != "" {
		config.TokenFile = tokenFile
	}
	if workerID != "" {
		config.WorkerID = workerID
	}
	if resources != "" {
		config.Resources = strings.Split(resources, ",")
	}
	if ramMiB > 0 {
		config.RAMMiB = ramMiB
	}
	if cpuUnits > 0 {
		config.CPUUnits = cpuUnits
	}
	if gpuUnits > 0 {
		config.GPUUnits = gpuUnits
	}
	if gpuHelper != "" {
		config.GPUHelper = gpuHelper
	}
	if pollSeconds > 0 {
		config.PollSeconds = pollSeconds
	}
	if logFile != "" {
		config.LogFile = logFile
	}
}

func agentRelayURL(config agentFileConfig) string {
	return firstNonEmpty(config.Relay, config.Coordinator)
}

func resolveRelayTokenPaths(legacy, agent, mother string) (string, string, error) {
	agentPath := firstNonEmpty(agent, legacy)
	motherPath := firstNonEmpty(mother, legacy)
	if motherPath == "" {
		motherPath = agentPath
	}
	if agentPath == "" || motherPath == "" {
		return "", "", errors.New("--agent-token-file and --mother-token-file are required (or use legacy --token-file for both)")
	}
	return agentPath, motherPath, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func parseResources(raw []string) ([]edgepool.ResourceKind, error) {
	if len(raw) == 0 {
		raw = []string{"cpu", "ram"}
	}
	seen := map[edgepool.ResourceKind]bool{}
	resources := make([]edgepool.ResourceKind, 0, len(raw))
	for _, value := range raw {
		resource := edgepool.ResourceKind(strings.ToLower(strings.TrimSpace(value)))
		if !resource.Valid() {
			return nil, fmt.Errorf("unsupported resource %q", value)
		}
		if !seen[resource] {
			seen[resource] = true
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func readStrictJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("configuration must contain exactly one JSON object")
		}
		return fmt.Errorf("invalid trailing configuration data: %w", err)
	}
	return nil
}

func writeExclusiveJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(absoluteOrCurrent(path)), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func newAgentLogger(path string, silent bool) (*log.Logger, func(), error) {
	if err := os.MkdirAll(filepath.Dir(absoluteOrCurrent(path)), 0o700); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	output := io.Writer(file)
	if !silent {
		output = io.MultiWriter(os.Stdout, file)
	}
	return log.New(output, "", log.LstdFlags|log.LUTC), func() { _ = file.Close() }, nil
}

func defaultStateDir() string {
	if configDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(configDir, "QSDM", "edge-pool")
	}
	return ".qsdm-edge-pool"
}

func defaultWorkerID() string {
	hostname, _ := os.Hostname()
	hostname = strings.ToLower(hostname)
	var builder strings.Builder
	for _, char := range hostname {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('-')
		}
	}
	value := strings.Trim(builder.String(), "-._")
	if value == "" {
		value = "qsdm-worker-" + strconv.Itoa(os.Getpid())
	}
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

func isLoopbackListen(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func absoluteOrCurrent(path string) string {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		cwd, _ := os.Getwd()
		return cwd
	}
	return path
}
