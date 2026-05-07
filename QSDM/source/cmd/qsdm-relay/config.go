package main

// Relay configuration: a slot allowlist file (TOML) + a few
// runtime knobs. All knobs are env-var-overridable so an
// operator can tune behaviour via the systemd unit's
// Environment=… stanzas without editing config files.
//
// On-disk shape (slots.toml):
//
//	# Each [[slot]] block whitelists one tunnel client.
//	[[slot]]
//	slot_id   = "blackbeard-3050"
//	key_hex   = "deadbeef..."           # 64 hex = 32 bytes
//	note      = "blackbeard's home 3050"
//
//	[[slot]]
//	slot_id   = "alice-h100"
//	key_hex   = "..."
//
// The TOML mirrors peer_signers.toml's shape (see
// cmd/qsdm/peer_signers.go) so an operator who already
// allowlisted an attester only has to copy the same
// slot_id+key_hex pair into a second file.

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/blackbeardONE/QSDM/pkg/tunnel"
)

// Config bundles every knob the relay binary cares about.
// Built by loadConfig from flags + environment + defaults.
type Config struct {
	// SlotsPath is the absolute path to the TOML allowlist.
	// Default: "/opt/qsdm/relay_slots.toml".
	SlotsPath string

	// TunnelListenAddr is the [host]:port the tunnel-ingress
	// HTTP server binds. Tunnel clients (qsdm-attester) hit
	// this address with an HTTP/1.1 Upgrade. Default ":7700".
	// In production this is FRONTED by Caddy on the canonical
	// public URL (e.g. https://relay.qsdm.tech).
	TunnelListenAddr string

	// ProxyListenAddr is the [host]:port the public miner
	// HTTP server binds. Public miners hit this address with
	// /<slot>/<…path…>. Default ":7710". Also Caddy-fronted.
	ProxyListenAddr string

	// MetricsListenAddr is the [host]:port for /metrics +
	// /info + /healthz. Default ":7720". Operator-only —
	// NEVER expose to the public internet.
	MetricsListenAddr string

	// LogTunnelEvents, if true (default), emits a structured
	// log line per tunnel register / deregister.
	LogTunnelEvents bool
}

// SlotsFile is the TOML root object. Held as its own type so
// a future format migration (e.g. NDJSON, per-slot files)
// keeps call sites stable.
type SlotsFile struct {
	Slot []SlotEntry `toml:"slot"`
}

// SlotEntry is one allowlist entry.
type SlotEntry struct {
	SlotID string `toml:"slot_id"`
	KeyHex string `toml:"key_hex"`
	Note   string `toml:"note"`
}

// LoadSlotsFile reads and parses path. Returns an empty
// slice (not an error) when path is empty or the file
// doesn't exist — the relay should boot in either case so
// operators can attach an allowlist later via SIGHUP +
// reload.
func LoadSlotsFile(path string) ([]SlotEntry, error) {
	if path == "" {
		return nil, nil
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("relay: stat %s: %w", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("relay: read %s: %w", path, err)
	}
	var f SlotsFile
	if _, err := toml.Decode(string(raw), &f); err != nil {
		return nil, fmt.Errorf("relay: decode %s: %w", path, err)
	}
	return f.Slot, nil
}

// BuildAuthMap converts SlotEntries into the tunnel.AuthMap
// the tunnel.HandleUpgrade Authenticator interface expects.
// Each invalid entry surfaces as a per-entry error in errs;
// the caller decides whether to fail-fast or warn-and-skip.
func BuildAuthMap(entries []SlotEntry) (tunnel.AuthMap, []error) {
	out := make(tunnel.AuthMap, len(entries))
	var errs []error
	for _, e := range entries {
		id := strings.TrimSpace(e.SlotID)
		if id == "" {
			errs = append(errs, fmt.Errorf("slot entry with empty slot_id, note=%q", e.Note))
			continue
		}
		if !tunnel.ValidSlotID(id) {
			errs = append(errs, fmt.Errorf("slot %q: invalid characters (allowed: %s)",
				id, tunnel.AllowedSlotChars))
			continue
		}
		hexStr := strings.TrimSpace(e.KeyHex)
		if hexStr == "" {
			errs = append(errs, fmt.Errorf("slot %q: empty key_hex", id))
			continue
		}
		key, err := hex.DecodeString(hexStr)
		if err != nil {
			errs = append(errs, fmt.Errorf("slot %q: decode key_hex: %w", id, err))
			continue
		}
		if len(key) < 16 {
			errs = append(errs, fmt.Errorf("slot %q: key length %d < 16 bytes", id, len(key)))
			continue
		}
		if _, dup := out[id]; dup {
			errs = append(errs, fmt.Errorf("slot %q: duplicate entry", id))
			continue
		}
		out[id] = tunnel.AuthMapEntry{Key: key, Note: e.Note}
	}
	return out, errs
}

// loadConfig reads env-vars, applies defaults, and returns a
// populated Config. Flag overrides happen later in main.go.
//
// Recognised env vars:
//
//	QSDM_RELAY_SLOTS_PATH        path override
//	QSDM_RELAY_TUNNEL_LISTEN     host:port for tunnel ingress
//	QSDM_RELAY_PROXY_LISTEN      host:port for public miners
//	QSDM_RELAY_METRICS_LISTEN    host:port for /metrics
//	QSDM_RELAY_LOG_TUNNEL_EVENTS "true"/"false" (default true)
func loadConfig() *Config {
	c := &Config{
		SlotsPath:         strings.TrimSpace(os.Getenv("QSDM_RELAY_SLOTS_PATH")),
		TunnelListenAddr:  strings.TrimSpace(os.Getenv("QSDM_RELAY_TUNNEL_LISTEN")),
		ProxyListenAddr:   strings.TrimSpace(os.Getenv("QSDM_RELAY_PROXY_LISTEN")),
		MetricsListenAddr: strings.TrimSpace(os.Getenv("QSDM_RELAY_METRICS_LISTEN")),
		LogTunnelEvents:   true,
	}
	if v := strings.TrimSpace(os.Getenv("QSDM_RELAY_LOG_TUNNEL_EVENTS")); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.LogTunnelEvents = b
		}
	}
	c.applyDefaults()
	return c
}

func (c *Config) applyDefaults() {
	if c.SlotsPath == "" {
		c.SlotsPath = "/opt/qsdm/relay_slots.toml"
	}
	if c.TunnelListenAddr == "" {
		c.TunnelListenAddr = ":7700"
	}
	if c.ProxyListenAddr == "" {
		c.ProxyListenAddr = ":7710"
	}
	if c.MetricsListenAddr == "" {
		c.MetricsListenAddr = ":7720"
	}
}
