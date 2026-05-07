// Package main implements qsdm-attester, a tiny standalone HTTP
// service that mints v2 challenge nonces signed with this
// operator's HMAC key. Miners (anywhere in the world) GET
// /api/v1/challenge to obtain a (nonce, issued_at, signature)
// triple they commit to in their proof's Attestation. The
// validator's pkg/mining/challenge.HMACSignerVerifier accepts
// any signer_id registered in its peer-signers allowlist, so
// this attester plugs into an existing QSDM network without
// touching consensus code — the validator merely needs to
// register (signer_id, key) in its peer_signers.toml.
//
// Compared with cmd/qsdm (the validator binary), this binary:
//
//   - Holds NO chain state (no accounts, no blocks, no mempool)
//   - Performs NO consensus work
//   - Touches the network ONLY via the four HTTP routes in
//     server.go
//   - Runs comfortably on a home Windows machine alongside
//     qsdmminer-console
//
// Operationally it is roughly equivalent to running a small
// Caddy-style service: load a key, listen on a port, sign
// requests. The threat model is correspondingly small — the
// worst a misbehaving attester can do is serve stale or
// malformed challenges, which causes its own miners to fail
// proof submission. It cannot cause a validator to accept an
// invalid proof.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining/challenge"
	"github.com/blackbeardONE/QSDM/pkg/telemetry"
	"github.com/blackbeardONE/QSDM/pkg/tunnel"
)

func main() {
	exitCode := run()
	os.Exit(exitCode)
}

// run is the testable body of main. Returns the desired exit
// code. Splitting main / run lets a future test exercise the
// startup path without calling os.Exit (which would terminate
// the test binary).
func run() int {
	listenFlag := flag.String("listen", "", "[host]:port to bind (overrides QSDM_ATTESTER_LISTEN; default :7733)")
	keyFlag := flag.String("key", "", "Path to 32-byte HMAC signer key (overrides QSDM_ATTESTER_KEY_PATH; default ~/.qsdm/attester.key)")
	signerFlag := flag.String("signer-id", "", "Override the auto-derived signer_id (advanced; must start with 'attester-')")
	noteFlag := flag.String("note", "", "Free-form tag emitted on /info (defaults to hostname)")
	logEveryFlag := flag.Uint64("log-every", 0, "If >0, log a sample line every Nth issuance")
	relayFlag := flag.String("relay", "", "Optional reverse-tunnel relay URL (e.g. https://relay.qsdm.tech). Leave empty to run without a tunnel.")
	slotFlag := flag.String("slot", "", "Slot ID this attester occupies on the relay (required when --relay is set)")
	relayKeyFlag := flag.String("relay-key", "", "Hex-encoded 32-byte HMAC key shared with the relay's allowlist. Empty = re-use the signer key.")
	telemetryDisabled := flag.Bool("telemetry-disabled", false, "Disable the Reference Telemetry Oracle (skip nvidia-smi polling, /api/v1/telemetry/reference returns 404)")
	telemetryEvery := flag.Duration("telemetry-every", 60*time.Second, "Interval between collector ticks (e.g. 30s, 5m). Default 60s.")
	telemetryFile := flag.String("telemetry-file", "", "Path to the persisted telemetry profile (default ~/.qsdm/telemetry.json). Use '-' to disable persistence.")
	telemetryNote := flag.String("telemetry-note", "", "Operator-facing tag included in the published profile (defaults to attester --note)")
	telemetryNvidiaPath := flag.String("telemetry-nvidia-smi", "", "Override the nvidia-smi binary path (default: 'nvidia-smi' from PATH)")
	versionFlag := flag.Bool("version", false, "Print the build version and exit 0")
	flag.Parse()

	if *versionFlag {
		fmt.Println("qsdm-attester", buildVersion)
		return 0
	}

	cfg, err := loadConfig()
	if err != nil {
		log.Printf("FATAL: load config: %v", err)
		return 2
	}
	// Flag values override env (env was already read by
	// loadConfig). Empty flag = keep whatever loadConfig
	// produced (env / default).
	if *listenFlag != "" {
		cfg.ListenAddr = *listenFlag
	}
	if *keyFlag != "" {
		cfg.KeyPath = *keyFlag
	}
	if *signerFlag != "" {
		cfg.SignerIDOverride = *signerFlag
	}
	if *noteFlag != "" {
		cfg.Note = *noteFlag
	}
	if *logEveryFlag != 0 {
		cfg.LogIssuanceEvery = *logEveryFlag
	}

	signerID, key, fresh, err := loadOrCreateSignerKey(cfg.KeyPath, cfg.SignerIDOverride)
	if err != nil {
		log.Printf("FATAL: load signer key: %v", err)
		return 2
	}
	signer, err := challenge.NewHMACSigner(signerID, key)
	if err != nil {
		log.Printf("FATAL: build HMAC signer: %v", err)
		return 2
	}
	issuer, err := challenge.NewIssuer(signer)
	if err != nil {
		log.Printf("FATAL: build challenge issuer: %v", err)
		return 2
	}

	keyFP := keyFingerprint(key)
	srv, err := NewServer(cfg, issuer, signer, keyFP)
	if err != nil {
		log.Printf("FATAL: build server: %v", err)
		return 2
	}
	srv.SetIssuanceLogger(func(snap LogIssuance) {
		log.Printf("attester: issued nonce signer_id=%s issued_at=%d total=%d remote=%s",
			snap.SignerID, snap.IssuedAt, snap.Total, snap.RemoteIP)
	})

	if fresh {
		log.Printf("attester: generated fresh signer key path=%s signer_id=%s key_fingerprint=%s",
			cfg.KeyPath, signerID, keyFP)
		log.Printf("attester: COPY THIS LINE INTO peer_signers.toml ON THE VALIDATOR ↓")
		log.Printf("attester: signer_id=%q key_hex=%q note=%q",
			signerID, hexKey(key), cfg.Note)
	} else {
		log.Printf("attester: loaded existing signer key path=%s signer_id=%s key_fingerprint=%s",
			cfg.KeyPath, signerID, keyFP)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Optional Reference Telemetry Oracle: collects
	// nvidia-smi snapshots into a long-running profile and
	// publishes it (signed with the attester's HMAC key)
	// at /api/v1/telemetry/reference. Disabled if
	// --telemetry-disabled is set OR if no GPU collector
	// is available on the host. Errors here are
	// non-fatal: an attester without a working GPU should
	// still mint challenges, just without the bonus
	// reference-catalog data.
	var telemetryWG sync.WaitGroup
	if !*telemetryDisabled {
		telProvider, telReg, telCollector, telPath, telErr := buildTelemetry(
			signerID, key, cfg, *telemetryNote, *telemetryFile, *telemetryNvidiaPath)
		if telErr != nil {
			structuredLog("attester: telemetry oracle disabled (build error)",
				"err", telErr.Error())
		} else {
			srv.SetTelemetry(telProvider)
			telemetryWG.Add(1)
			go func() {
				defer telemetryWG.Done()
				structuredLog("attester: telemetry collector starting",
					"every", telemetryEvery.String(),
					"file", telPath,
					"collector", telCollector.Kind())
				runTelemetryCollector(ctx, telReg, telCollector, telPath, *telemetryEvery, telProvider, structuredLog)
			}()
		}
	} else {
		structuredLog("attester: telemetry oracle disabled by --telemetry-disabled")
	}

	// Optional reverse-tunnel client: when --relay is set
	// the attester runs the same Routes() handler on TWO
	// listeners — the local TCP listener AND a yamux
	// session multiplexed inside an outbound TLS+HTTP/1.1
	// connection to the relay. Public miners reach this
	// attester at <relay>/<slot>/api/v1/mining/challenge.
	//
	// Spawned BEFORE srv.Run so a misconfigured tunnel
	// (bad URL, bad key) surfaces before we accept any
	// local traffic. The tunnel goroutine self-heals via
	// Client.Run's reconnect loop; ctx cancellation is the
	// only termination signal it honours.
	var tunnelWG sync.WaitGroup
	if relay := *relayFlag; relay != "" {
		slot := *slotFlag
		if slot == "" {
			log.Printf("FATAL: --relay set but --slot is empty (cannot register without a slot ID)")
			return 2
		}
		if !tunnel.ValidSlotID(slot) {
			log.Printf("FATAL: --slot %q invalid (allowed: %s)", slot, tunnel.AllowedSlotChars)
			return 2
		}
		tunnelKey, keyErr := resolveRelayKey(*relayKeyFlag, key)
		if keyErr != nil {
			log.Printf("FATAL: --relay-key: %v", keyErr)
			return 2
		}
		client := &tunnel.Client{
			RelayURL: relay,
			SlotID:   slot,
			SignerID: signerID,
			Key:      tunnelKey,
			Handler:  srv.Routes(),
			Logf:     structuredLog,
		}
		tunnelWG.Add(1)
		go func() {
			defer tunnelWG.Done()
			structuredLog("attester: tunnel client starting",
				"relay", relay, "slot", slot, "signer_id", signerID)
			if err := client.Run(ctx); err != nil {
				structuredLog("attester: tunnel client exited with error",
					"err", err.Error())
				return
			}
			structuredLog("attester: tunnel client exited cleanly")
		}()
	}

	srvErr := srv.Run(ctx, structuredLog)
	// Wait for any running tunnel goroutine to finish
	// draining before we exit, so clean shutdown is
	// observable end-to-end. The telemetry collector
	// goroutine drains under the same ctx — wait for both
	// before reporting the exit code.
	tunnelWG.Wait()
	telemetryWG.Wait()
	if srvErr != nil {
		log.Printf("FATAL: server: %v", srvErr)
		return 1
	}
	return 0
}

// buildTelemetry assembles a TelemetryProvider, the
// underlying registry, and a collector. Returns five values
// + a fatal error (the caller decides whether it's truly
// fatal or just "no telemetry today"). Wraps the relatively
// noisy construction so main.go's primary flow stays
// readable.
//
// Persistence path resolution:
//
//   "-"          → persistence disabled (in-memory only)
//   ""           → ~/.qsdm/telemetry.json (default)
//   "<path>"     → the literal path
//
// Collector resolution: today always *NVIDIASMICollector.
// Future non-NVIDIA collectors plug in here.
func buildTelemetry(
	signerID string,
	signerKey []byte,
	cfg *Config,
	noteOverride string,
	pathFlag string,
	nvidiaSMIPath string,
) (*TelemetryProvider, *telemetry.Registry, telemetry.Collector, string, error) {
	hostNote := noteOverride
	if hostNote == "" {
		hostNote = cfg.Note
	}

	persistPath, err := resolveTelemetryPath(pathFlag)
	if err != nil {
		return nil, nil, nil, "", err
	}

	collector := &telemetry.NVIDIASMICollector{Path: nvidiaSMIPath}

	reg, err := telemetry.NewRegistry(signerID, hostNote, collector.Kind())
	if err != nil {
		return nil, nil, nil, "", err
	}
	if persistPath != "" {
		if loaded, loadErr := reg.LoadFromFile(persistPath); loadErr != nil {
			return nil, nil, nil, persistPath, fmt.Errorf("telemetry: load %s: %w", persistPath, loadErr)
		} else if loaded {
			structuredLog("attester: telemetry hydrated from disk",
				"path", persistPath)
		}
	}

	provider := &TelemetryProvider{
		Registry:    reg,
		Key:         append([]byte(nil), signerKey...),
		PersistPath: persistPath,
	}
	return provider, reg, collector, persistPath, nil
}

// resolveTelemetryPath turns the operator's --telemetry-file
// flag into the absolute path to use. "-" disables
// persistence (returned as empty string). Empty (default)
// resolves to ~/.qsdm/telemetry.json.
func resolveTelemetryPath(flagValue string) (string, error) {
	if flagValue == "-" {
		return "", nil
	}
	if flagValue != "" {
		return flagValue, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir for default telemetry path: %w", err)
	}
	return filepath.Join(home, ".qsdm", "telemetry.json"), nil
}

// resolveRelayKey returns the HMAC key the tunnel client
// should present to the relay. If hexFlag is non-empty it
// is decoded; otherwise the attester's own signer key is
// reused (the recommended posture — one key per attester
// keeps configuration small).
func resolveRelayKey(hexFlag string, signerKey []byte) ([]byte, error) {
	if hexFlag == "" {
		out := make([]byte, len(signerKey))
		copy(out, signerKey)
		return out, nil
	}
	h := hexFlag
	const hexdigits = "0123456789abcdefABCDEF"
	for i := 0; i < len(h); i++ {
		found := false
		for j := 0; j < len(hexdigits); j++ {
			if h[i] == hexdigits[j] {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("relay key: non-hex byte at position %d", i)
		}
	}
	if len(h)%2 != 0 {
		return nil, fmt.Errorf("relay key: odd length %d", len(h))
	}
	out := make([]byte, len(h)/2)
	for i := 0; i < len(out); i++ {
		hi := hexValue(h[i*2])
		lo := hexValue(h[i*2+1])
		out[i] = hi<<4 | lo
	}
	if len(out) < 16 {
		return nil, fmt.Errorf("relay key: %d bytes < 16-byte minimum", len(out))
	}
	return out, nil
}

func hexValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}

// structuredLog is the standard-lib "log" formatted as
// key=value pairs. Keeps the binary log-library-free; this
// is sufficient for an operator-run service that doesn't need
// to ingest into a structured log pipeline.
func structuredLog(msg string, kv ...any) {
	if len(kv) == 0 {
		log.Print(msg)
		return
	}
	out := msg
	for i := 0; i+1 < len(kv); i += 2 {
		out += fmt.Sprintf(" %v=%v", kv[i], kv[i+1])
	}
	log.Print(out)
}

// hexKey is a one-shot encoder used to print the freshly-minted
// key to stdout on first boot so the operator can copy it into
// the validator's peer-signers file. It is the ONLY place in
// the codebase that prints the raw key bytes; all other paths
// use keyFingerprint.
//
// Defined here (instead of in config.go) to keep the security-
// sensitive surface area in main.go where it is obvious to a
// reviewer that printing happens exactly once per "fresh"
// boot.
func hexKey(key []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(key)*2)
	for i, b := range key {
		out[i*2] = hexdigits[b>>4]
		out[i*2+1] = hexdigits[b&0x0f]
	}
	return string(out)
}
