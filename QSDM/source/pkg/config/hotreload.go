package config

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/monitoring"
)

// ReloadCallback is invoked with the new config when a change is detected.
type ReloadCallback func(newCfg *Config)

// HotReloader watches a config file for changes and reloads it.
type HotReloader struct {
	mu           sync.RWMutex
	filePath     string
	lastHash     string
	lastModTime  time.Time
	current      *Config
	callbacks    []ReloadCallback
	pollInterval time.Duration
	stopCh       chan struct{}
	wg           sync.WaitGroup
	reloadCount  int
	policy       ReloadPolicy

	lastDryRunAt       time.Time
	lastDryRunChanged  bool
	lastDryRunPolicyOK bool
	lastDryRunLoadOK   bool
}

// ReloadPolicy controls which config keys can hot-reload.
type ReloadPolicy struct {
	Allowlist      []string // if non-empty, only these keys may change
	Denylist       []string // keys that can never hot-reload
	RequireRestart []string // keys that can change only on restart
	Strict         bool     // if true, unknown changed keys are blocked when allowlist is set
}

// HotReloadConfig configures the hot reloader.
type HotReloadConfig struct {
	FilePath     string
	PollInterval time.Duration
}

// DefaultHotReloadConfig returns sensible defaults.
func DefaultHotReloadConfig() HotReloadConfig {
	return HotReloadConfig{
		PollInterval: 5 * time.Second,
	}
}

// NewHotReloader creates a reloader for the given config file.
func NewHotReloader(cfg HotReloadConfig, initial *Config) (*HotReloader, error) {
	if cfg.FilePath == "" && initial != nil && initial.ConfigFileUsed != "" {
		cfg.FilePath = initial.ConfigFileUsed
	}
	if cfg.FilePath == "" {
		return nil, fmt.Errorf("no config file path specified")
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 5 * time.Second
	}

	hash, modTime := fileFingerprint(cfg.FilePath)

	return &HotReloader{
		filePath:     cfg.FilePath,
		lastHash:     hash,
		lastModTime:  modTime,
		current:      initial,
		pollInterval: cfg.PollInterval,
		stopCh:       make(chan struct{}),
		policy:       ReloadPolicy{},
	}, nil
}

// SetPolicy updates runtime hot-reload policy.
func (hr *HotReloader) SetPolicy(p ReloadPolicy) {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	hr.policy = p
}

// OnReload registers a callback to invoke when the config changes.
func (hr *HotReloader) OnReload(cb ReloadCallback) {
	hr.mu.Lock()
	defer hr.mu.Unlock()
	hr.callbacks = append(hr.callbacks, cb)
}

// Current returns the currently loaded config.
func (hr *HotReloader) Current() *Config {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	return hr.current
}

// ReloadCount returns how many times the config was reloaded.
func (hr *HotReloader) ReloadCount() int {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	return hr.reloadCount
}

// CheckAndReload checks if the file changed and reloads if needed.
func (hr *HotReloader) CheckAndReload() (changed bool, err error) {
	hash, modTime := fileFingerprint(hr.filePath)

	hr.mu.RLock()
	sameHash := hash == hr.lastHash
	sameTime := modTime.Equal(hr.lastModTime)
	hr.mu.RUnlock()

	if sameHash && sameTime {
		return false, nil
	}

	newCfg := &Config{}
	if loadErr := loadConfigFile(hr.filePath, newCfg); loadErr != nil {
		monitoring.GetMetrics().IncHotReloadApply(false)
		return false, fmt.Errorf("reload failed: %w", loadErr)
	}
	applyDefaults(newCfg)

	hr.mu.RLock()
	current := hr.current
	policy := hr.policy
	hr.mu.RUnlock()
	if policyErr := validateReloadPolicy(current, newCfg, policy); policyErr != nil {
		monitoring.GetMetrics().IncHotReloadApply(false)
		return false, fmt.Errorf("reload blocked by policy: %w", policyErr)
	}

	hr.mu.Lock()
	hr.current = newCfg
	hr.lastHash = hash
	hr.lastModTime = modTime
	hr.reloadCount++
	callbacks := make([]ReloadCallback, len(hr.callbacks))
	copy(callbacks, hr.callbacks)
	hr.mu.Unlock()

	for _, cb := range callbacks {
		cb(newCfg)
	}

	monitoring.GetMetrics().IncHotReloadApply(true)
	return true, nil
}

// DryRunReload loads the config file from disk and reports whether its fingerprint
// differs from the last successfully applied reload, which top-level keys would
// change, and whether ReloadPolicy would block the update. It does not mutate
// current config, bump reload counters, or invoke callbacks.
func (hr *HotReloader) DryRunReload() (fileChanged bool, changedKeys []string, policyErr error, loadErr error) {
	hash, modTime := fileFingerprint(hr.filePath)

	hr.mu.RLock()
	sameHash := hash == hr.lastHash
	sameTime := modTime.Equal(hr.lastModTime)
	current := hr.current
	policy := hr.policy
	hr.mu.RUnlock()

	if sameHash && sameTime {
		return false, nil, nil, nil
	}

	newCfg := &Config{}
	if loadErr = loadConfigFile(hr.filePath, newCfg); loadErr != nil {
		hr.mu.Lock()
		hr.lastDryRunAt = time.Now().UTC()
		hr.lastDryRunChanged = true
		hr.lastDryRunPolicyOK = false
		hr.lastDryRunLoadOK = false
		hr.mu.Unlock()
		return true, nil, nil, loadErr
	}
	applyDefaults(newCfg)
	changedKeys = changedTopLevelKeys(current, newCfg)
	policyErr = validateReloadPolicy(current, newCfg, policy)

	hr.mu.Lock()
	hr.lastDryRunAt = time.Now().UTC()
	hr.lastDryRunChanged = true
	hr.lastDryRunPolicyOK = policyErr == nil
	hr.lastDryRunLoadOK = true
	hr.mu.Unlock()

	return true, changedKeys, policyErr, nil
}

// LastDryRunInfo returns the last DryRunReload outcome snapshot (UTC timestamps).
func (hr *HotReloader) LastDryRunInfo() map[string]interface{} {
	hr.mu.RLock()
	defer hr.mu.RUnlock()
	return map[string]interface{}{
		"last_dry_run_at":        hr.lastDryRunAt,
		"last_file_changed":      hr.lastDryRunChanged,
		"last_policy_ok":         hr.lastDryRunPolicyOK,
		"last_load_ok":           hr.lastDryRunLoadOK,
		"reload_count_applied":   hr.reloadCount,
		"config_file":            hr.filePath,
	}
}

// Start begins the background file-watching loop.
func (hr *HotReloader) Start() {
	hr.wg.Add(1)
	go func() {
		defer hr.wg.Done()
		ticker := time.NewTicker(hr.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hr.stopCh:
				return
			case <-ticker.C:
				changed, err := hr.CheckAndReload()
				if err != nil {
					log.Printf("[hot-reload] error: %v", err)
				}
				if changed {
					log.Printf("[hot-reload] config reloaded from %s", hr.filePath)
				}
			}
		}
	}()
}

// Stop halts the background watcher.
func (hr *HotReloader) Stop() {
	close(hr.stopCh)
	hr.wg.Wait()
}

func fileFingerprint(path string) (hash string, modTime time.Time) {
	info, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}
	}
	modTime = info.ModTime()

	f, err := os.Open(path)
	if err != nil {
		return "", modTime
	}
	defer f.Close()

	h := sha256.New()
	io.Copy(h, f)
	hash = fmt.Sprintf("%x", h.Sum(nil))
	return
}

func validateReloadPolicy(oldCfg, newCfg *Config, p ReloadPolicy) error {
	if oldCfg == nil {
		return nil
	}
	changed := changedTopLevelKeys(oldCfg, newCfg)
	if len(changed) == 0 {
		return nil
	}

	for _, key := range changed {
		if containsKey(p.Denylist, key) {
			return fmt.Errorf("key %q is denylisted for hot reload", key)
		}
		if containsKey(p.RequireRestart, key) {
			return fmt.Errorf("key %q requires restart", key)
		}
	}
	if len(p.Allowlist) > 0 && p.Strict {
		for _, key := range changed {
			if !containsKey(p.Allowlist, key) {
				return fmt.Errorf("key %q not in allowlist", key)
			}
		}
	}
	return nil
}

func changedTopLevelKeys(oldCfg, newCfg *Config) []string {
	oldMap := structToMap(oldCfg)
	newMap := structToMap(newCfg)
	keys := make([]string, 0, len(newMap))
	for k := range newMap {
		keys = append(keys, k)
	}
	var changed []string
	for _, k := range keys {
		if !reflect.DeepEqual(oldMap[k], newMap[k]) {
			changed = append(changed, k)
		}
	}
	return changed
}

func structToMap(cfg *Config) map[string]interface{} {
	b, _ := json.Marshal(cfg)
	out := map[string]interface{}{}
	_ = json.Unmarshal(b, &out)
	return out
}

func containsKey(list []string, key string) bool {
	for _, k := range list {
		if k == key {
			return true
		}
	}
	return false
}
