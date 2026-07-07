package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/tunnel"
)

var buildVersion = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	var (
		relay           = flag.String("relay", envString("QSDM_HOME_GATEWAY_RELAY", ""), "Relay origin URL, e.g. https://api.qsdm.tech")
		slot            = flag.String("slot", envString("QSDM_HOME_GATEWAY_SLOT", ""), "Relay slot ID for this home validator")
		keyHex          = flag.String("key-hex", envString("QSDM_HOME_GATEWAY_KEY_HEX", ""), "Hex HMAC key shared with the relay slot allowlist")
		backend         = flag.String("backend", envString("QSDM_HOME_GATEWAY_BACKEND", "http://127.0.0.1:8080"), "Local validator API backend")
		signerID        = flag.String("signer-id", envString("QSDM_HOME_GATEWAY_SIGNER_ID", defaultSignerID()), "Gateway signer/log identity")
		allowEnrollment = flag.Bool("allow-enrollment", envBool("QSDM_HOME_GATEWAY_ALLOW_ENROLLMENT", false), "Expose mining enrollment endpoints in addition to the default mining/status allowlist")
		allowHive       = flag.Bool("allow-hive", envBool("QSDM_HOME_GATEWAY_ALLOW_HIVE", false), "Expose the consumer-safe QSDM Hive API allowlist")
		printKey        = flag.Bool("generate-key", false, "Print a fresh 32-byte relay slot key and exit")
		version         = flag.Bool("version", false, "Print version and exit")
	)
	flag.Parse()

	if *version {
		fmt.Println("qsdm-home-gateway", buildVersion)
		return 0
	}
	if *printKey {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			log.Printf("generate key: %v", err)
			return 2
		}
		fmt.Println(hex.EncodeToString(key))
		return 0
	}

	if strings.TrimSpace(*relay) == "" {
		log.Print("FATAL: --relay or QSDM_HOME_GATEWAY_RELAY is required")
		return 2
	}
	if strings.TrimSpace(*slot) == "" {
		log.Print("FATAL: --slot or QSDM_HOME_GATEWAY_SLOT is required")
		return 2
	}
	if !tunnel.ValidSlotID(*slot) {
		log.Printf("FATAL: invalid slot %q (allowed chars: %s)", *slot, tunnel.AllowedSlotChars)
		return 2
	}
	key, err := hex.DecodeString(strings.TrimSpace(*keyHex))
	if err != nil || len(key) < 16 {
		log.Printf("FATAL: --key-hex must decode to at least 16 bytes (use --generate-key)")
		return 2
	}
	backendURL, err := url.Parse(strings.TrimSpace(*backend))
	if err != nil || backendURL.Scheme == "" || backendURL.Host == "" {
		log.Printf("FATAL: invalid --backend %q", *backend)
		return 2
	}
	if backendURL.Hostname() != "127.0.0.1" && backendURL.Hostname() != "localhost" {
		log.Printf("FATAL: backend must be localhost/127.0.0.1, got %q", backendURL.Host)
		return 2
	}

	handler := newGatewayHandler(backendURL, *allowEnrollment, *allowHive)
	handlerWithTimeouts := http.TimeoutHandler(handler, 35*time.Second, "gateway timeout")

	client := tunnel.Client{
		RelayURL: strings.TrimRight(*relay, "/"),
		SlotID:   strings.TrimSpace(*slot),
		SignerID: strings.TrimSpace(*signerID),
		Key:      key,
		Handler:  handlerWithTimeouts,
		Logf:     structuredLog,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("home-gateway: starting relay=%s slot=%s backend=%s allow_enrollment=%t allow_hive=%t",
		client.RelayURL, client.SlotID, backendURL.String(), *allowEnrollment, *allowHive)
	if err := client.Run(ctx); err != nil {
		log.Printf("FATAL: gateway stopped: %v", err)
		return 1
	}
	return 0
}

func envString(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envBool(name string, fallback bool) bool {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		}
	}
	return fallback
}

func defaultSignerID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "qsdm-home-gateway"
	}
	return "qsdm-home-gateway-" + strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, host)
}

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
