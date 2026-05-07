// Package main implements qsdm-relay, the public-facing
// rendezvous server that lets a NAT-bound qsdm-attester
// be reached from the open internet without third-party
// tunnel daemons (Cloudflare Tunnel, frp, ngrok, …).
//
// Architecture: see pkg/tunnel/tunnel.go for the full
// design diagram. In one sentence: an attester opens a
// single outbound TLS+yamux connection to this relay, the
// relay registers it under a slot ID, and miners pull
// challenges via a public URL the relay reverse-proxies
// down the open tunnel.
//
// Operationally the relay runs alongside the validator on
// BLR1 and exposes three independent ports (configurable):
//
//   :7700 — tunnel ingress (Caddy → https://relay.qsdm.tech)
//   :7710 — public miner traffic (Caddy → https://attest.qsdm.tech)
//   :7720 — operator metrics (bound to localhost only)
//
// Boot sequence:
//
//   1. Load slot allowlist from QSDM_RELAY_SLOTS_PATH.
//   2. Build tunnel.AuthMap; fatal on any malformed entry.
//   3. NewServer + Run → blocks until SIGTERM.
//
// The relay holds NO chain state, NO mempool, NO cryptographic
// authority of its own. It is a pure traffic forwarder. The
// only secret it knows is the per-slot HMAC key, used solely
// to authenticate inbound tunnel connections.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		slotsFlag       = flag.String("slots", "", "Path to the TOML slot allowlist (overrides QSDM_RELAY_SLOTS_PATH)")
		tunnelListen    = flag.String("tunnel-listen", "", "[host]:port for tunnel ingress (overrides env)")
		proxyListen     = flag.String("proxy-listen", "", "[host]:port for public miner proxy (overrides env)")
		metricsListen   = flag.String("metrics-listen", "", "[host]:port for /metrics + /info + /healthz (overrides env)")
		quietEvents     = flag.Bool("quiet-events", false, "Suppress per-tunnel-register/deregister log lines")
		versionFlag     = flag.Bool("version", false, "Print build version and exit 0")
		failOnEmptyAuth = flag.Bool("fail-on-empty-allowlist", false, "Exit non-zero if the slot allowlist file is empty or missing")
	)
	flag.Parse()

	if *versionFlag {
		fmt.Println("qsdm-relay", buildVersion)
		return 0
	}

	cfg := loadConfig()
	if *slotsFlag != "" {
		cfg.SlotsPath = *slotsFlag
	}
	if *tunnelListen != "" {
		cfg.TunnelListenAddr = *tunnelListen
	}
	if *proxyListen != "" {
		cfg.ProxyListenAddr = *proxyListen
	}
	if *metricsListen != "" {
		cfg.MetricsListenAddr = *metricsListen
	}
	if *quietEvents {
		cfg.LogTunnelEvents = false
	}

	entries, err := LoadSlotsFile(cfg.SlotsPath)
	if err != nil {
		log.Printf("FATAL: load slots file %s: %v", cfg.SlotsPath, err)
		return 2
	}
	authMap, perEntryErrs := BuildAuthMap(entries)
	for _, e := range perEntryErrs {
		log.Printf("FATAL: slot allowlist: %v", e)
	}
	if len(perEntryErrs) > 0 {
		log.Printf("FATAL: %d slot allowlist entries are invalid; fix %s and restart",
			len(perEntryErrs), cfg.SlotsPath)
		return 2
	}
	if len(authMap) == 0 {
		if *failOnEmptyAuth {
			log.Printf("FATAL: slot allowlist %s is empty (override with --fail-on-empty-allowlist=false)", cfg.SlotsPath)
			return 2
		}
		log.Printf("WARN: slot allowlist %s is empty; relay will accept zero tunnel connections until you add entries", cfg.SlotsPath)
	} else {
		log.Printf("relay: loaded %d slot(s) from %s", len(authMap), cfg.SlotsPath)
		for slot, entry := range authMap {
			log.Printf("relay: slot %q note=%q key_len=%d", slot, entry.Note, len(entry.Key))
		}
	}

	srv, err := NewServer(cfg, authMap, structuredLog)
	if err != nil {
		log.Printf("FATAL: NewServer: %v", err)
		return 2
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := srv.Run(ctx); err != nil {
		log.Printf("FATAL: server: %v", err)
		return 1
	}
	return 0
}

// structuredLog: same key=value formatting used by qsdm-attester.
// Keeps the relay log-library-free; an operator running the
// binary under systemd already has journalctl + grep, which
// works perfectly well on key=value strings.
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
