package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
	"github.com/blackbeardONE/QSDM/pkg/chain"
	"github.com/blackbeardONE/QSDM/pkg/producerpolicy"
)

// hl1ExitCode is the panic value of the stubbed hl1Exit.
type hl1ExitCode int

// hl1CatchExit runs f with hl1Exit stubbed and returns the exit code, or -1
// if f returned without exiting.
func hl1CatchExit(t *testing.T, f func()) (code int) {
	t.Helper()
	orig := hl1Exit
	hl1Exit = func(c int) { panic(hl1ExitCode(c)) }
	defer func() {
		hl1Exit = orig
		if r := recover(); r != nil {
			c, ok := r.(hl1ExitCode)
			if !ok {
				panic(r)
			}
			code = int(c)
		}
	}()
	f()
	return -1
}

func TestHL1FatalRestoreExits78(t *testing.T) {
	if code := hl1CatchExit(t, func() { fatalRestore("boom %d", 1) }); code != legacymining.ExitFatalRestore {
		t.Fatalf("exit = %d, want 78", code)
	}
}

// The four boot steps that were exit 1 before HL1 and exit 78 now (dep:855,
// dep:863-864, dep:2334, dep:2343).
func TestHL1FatalConversionsExit78(t *testing.T) {
	dir := t.TempDir()

	t.Run("state lock busy", func(t *testing.T) {
		path := filepath.Join(dir, legacymining.StateLockFile)
		held, err := chain.AcquireStateLock(path)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if code := hl1CatchExit(t, func() { hl1AcquireStateLock(path) }); code != 78 {
			t.Fatalf("exit = %d, want 78", code)
		}
	})
	t.Run("state lock free", func(t *testing.T) {
		var lock *chain.StateLock
		if code := hl1CatchExit(t, func() { lock = hl1AcquireStateLock(filepath.Join(dir, "free.lock")) }); code != -1 {
			t.Fatalf("exit = %d with a free lock", code)
		}
		if lock == nil {
			t.Fatal("no lock returned")
		}
		_ = lock.Close()
	})
	t.Run("transition prefix under the lock", func(t *testing.T) {
		p := &producerpolicy.Transition{
			Version: 1, CheckpointHeight: 10, EffectiveHeight: 11, HistoricalSignatureHeight: 1,
			CheckpointHash:         strings.Repeat("a", 64),
			HistoricalProducer:     strings.Repeat("b", 64),
			ReplacementProducer:    strings.Repeat("c", 64),
			HistoricalPrefixSHA256: strings.Repeat("d", 64),
			HistoricalPrefixBytes:  4,
		}
		journal := filepath.Join(dir, "qsdm_chain.ndjson")
		hl1WriteFile(t, journal, "abc\n")
		if code := hl1CatchExit(t, func() { hl1LockedTransitionPreflight(p, journal) }); code != 78 {
			t.Fatalf("mismatched prefix: exit = %d, want 78", code)
		}
		sum := sha256.Sum256([]byte("abc\n"))
		p.HistoricalPrefixSHA256 = hex.EncodeToString(sum[:])
		if code := hl1CatchExit(t, func() { hl1LockedTransitionPreflight(p, journal) }); code != -1 {
			t.Fatalf("matching prefix: exit = %d", code)
		}
		if code := hl1CatchExit(t, func() { hl1LockedTransitionPreflight(nil, journal) }); code != -1 {
			t.Fatalf("no transition: exit = %d", code)
		}
	})
	t.Run("OpenChainJournal", func(t *testing.T) {
		blocker := filepath.Join(dir, "not-a-dir")
		hl1WriteFile(t, blocker, "x")
		if code := hl1CatchExit(t, func() { hl1OpenChainJournal(filepath.Join(blocker, "qsdm_chain.ndjson"), nil) }); code != 78 {
			t.Fatalf("exit = %d, want 78", code)
		}
		var j *chain.ChainJournal
		if code := hl1CatchExit(t, func() { j = hl1OpenChainJournal(filepath.Join(dir, "ok.ndjson"), nil) }); code != -1 || j == nil {
			t.Fatalf("valid journal: exit = %d", code)
		}
		_ = j.Close()
	})
	t.Run("persistence reserve", func(t *testing.T) {
		for _, raw := range []string{"garbage", "-1", "1024"} {
			if code := hl1CatchExit(t, func() { hl1PersistenceReserve(raw) }); code != 78 {
				t.Fatalf("%q: exit = %d, want 78", raw, code)
			}
		}
		var v uint64
		if code := hl1CatchExit(t, func() { v = hl1PersistenceReserve("") }); code != -1 || v != defaultPersistenceReserveBytes {
			t.Fatalf("default reserve: exit=%d v=%d", code, v)
		}
	})
}

// -----------------------------------------------------------------------------
// FAILSTOP arm and trip
// -----------------------------------------------------------------------------

func TestHL1FailStopArmAndTrip(t *testing.T) {
	dir := t.TempDir()
	stopper := &hl1FailStopper{}
	now := time.Unix(1700000000, 42)
	if err := hl1ArmFailStop(stopper, dir, nil, "hardened-legacy-test", now); err != nil {
		t.Fatal(err)
	}
	armed, err := os.ReadFile(filepath.Join(dir, legacymining.FailStopArmedFile))
	if err != nil {
		t.Fatal(err)
	}
	var m legacymining.ArmedMarker
	if err := json.Unmarshal(armed, &m); err != nil || m.Release != "hardened-legacy-test" || m.BootNS != now.UnixNano() || m.PID != os.Getpid() {
		t.Fatalf("FAILSTOP.armed = %s (%v)", armed, err)
	}
	if err := hl1RefuseFailStop(dir); err != nil {
		t.Fatalf("armed only must not refuse boot: %v", err)
	}

	var codes []int
	stopper.exit = func(c int) { codes = append(codes, c) }
	stopper.now = func() time.Time { return time.Unix(1700000001, 0) }
	stopper.logf = func(string, ...any) {}
	stopper.Stop(legacymining.ExitFailStop, "persist:H3:disk full")
	stopper.Stop(legacymining.ExitFailStop, "second call")

	if len(codes) != 1 || codes[0] != 86 {
		t.Fatalf("exit codes = %v, want exactly [86]", codes)
	}
	if _, err := os.Lstat(filepath.Join(dir, legacymining.FailStopArmedFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("FAILSTOP.armed still present after the trip (err=%v)", err)
	}
	tripped, err := os.ReadFile(filepath.Join(dir, legacymining.FailStopFile))
	if err != nil || string(tripped) != string(armed) {
		t.Fatalf("FAILSTOP.json = %q (%v), want the renamed armed marker %q", tripped, err, armed)
	}
	var cause legacymining.MarkerCause
	raw, _ := os.ReadFile(filepath.Join(dir, legacymining.FailStopCauseFile))
	if err := json.Unmarshal(raw, &cause); err != nil || cause.Cause != "persist:H3:disk full" {
		t.Fatalf("FAILSTOP.cause.json = %s (%v)", raw, err)
	}
	// S1: the next boot refuses with exit 78.
	if err := hl1RefuseFailStop(dir); err == nil {
		t.Fatal("S1 accepted a tripped FAILSTOP.json")
	}
	if code := hl1CatchExit(t, func() { hl1BootS1S3(dir, "x") }); code != 78 {
		t.Fatalf("boot with FAILSTOP.json: exit = %d, want 78", code)
	}
}

func TestHL1FailStopFallsBackToD1WhenNotArmed(t *testing.T) {
	dir := t.TempDir()
	stopper := &hl1FailStopper{logf: func(string, ...any) {}}
	stopper.arm(dir, []byte("{\"release\":\"r\"}\n"))
	var code int
	stopper.exit = func(c int) { code = c }
	stopper.Stop(86, "produce:post-apply:signer")
	if code != 86 {
		t.Fatalf("exit = %d", code)
	}
	if got, err := os.ReadFile(filepath.Join(dir, legacymining.FailStopFile)); err != nil || string(got) != "{\"release\":\"r\"}\n" {
		t.Fatalf("FAILSTOP.json = %q (%v)", got, err)
	}
}

func TestHL1FailStopUnarmedStillExits(t *testing.T) {
	stopper := &hl1FailStopper{logf: func(string, ...any) {}}
	var code int
	stopper.exit = func(c int) { code = c }
	stopper.Stop(86, "marker-io")
	if code != 86 {
		t.Fatalf("exit = %d", code)
	}
}

// -----------------------------------------------------------------------------
// S3: stale temp cleanup (under the lock) and arming
// -----------------------------------------------------------------------------

func TestHL1ArmRemovesStaleTempsInBothDirs(t *testing.T) {
	state := t.TempDir()
	legacy := filepath.Join(state, legacymining.LegacyDirName)
	if err := os.Mkdir(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := []string{
		filepath.Join(state, legacymining.TempPrefix+legacymining.WatermarkFile+"-123-00112233aabbccdd"),
		filepath.Join(state, legacymining.TempPrefix+legacymining.FailStopArmedFile+"-9-ffffffffffffffff"),
		filepath.Join(legacy, legacymining.TempPrefix+legacymining.TrippedFile+"-1-0000000000000000"),
	}
	keep := []string{
		filepath.Join(state, "qsdm_accounts.json"),
		filepath.Join(state, "hl1-tmp-lookalike"),
		filepath.Join(legacy, legacymining.DBFile),
	}
	for _, p := range append(append([]string{}, stale...), keep...) {
		hl1WriteFile(t, p, "x")
	}
	if err := hl1ArmFailStop(&hl1FailStopper{}, state, hl1LegacyDirs(state, legacymining.Env{}), "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, p := range stale {
		if _, err := os.Lstat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("stale temp survived: %s", p)
		}
	}
	for _, p := range keep {
		if _, err := os.Lstat(p); err != nil {
			t.Fatalf("non-temp file removed: %s", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(state, legacymining.FailStopArmedFile)); err != nil {
		t.Fatalf("FAILSTOP.armed not created: %v", err)
	}
}

func TestHL1ArmKeepsTrippedFailStop(t *testing.T) {
	// The replay mode arms only if FAILSTOP.json is absent, and never removes it.
	dir := t.TempDir()
	hl1WriteFile(t, filepath.Join(dir, legacymining.FailStopFile), "tripped")
	if err := hl1ArmFailStop(&hl1FailStopper{}, dir, nil, "r", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, legacymining.FailStopArmedFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("armed a new FAILSTOP next to a tripped one")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, legacymining.FailStopFile)); string(got) != "tripped" {
		t.Fatalf("FAILSTOP.json changed: %q", got)
	}
}

func TestHL1ArmFailureExits78(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does-not-exist")
	if err := hl1ArmFailStop(&hl1FailStopper{}, missing, nil, "r", time.Now()); err == nil {
		t.Fatal("arming in a missing state directory succeeded")
	}
}

// -----------------------------------------------------------------------------
// S2: environment, config and sync URLs
// -----------------------------------------------------------------------------

func hl1CanaryConfigFile(t *testing.T, dir string) (string, string) {
	t.Helper()
	cfg := legacymining.Config{
		Version:         1,
		Allowed:         []legacymining.AllowEntry{{MinerAddr: strings.Repeat("ab", 32), NodeID: "node-1"}},
		MaxProofsPerMin: 60, MaxProofsTotal: 3000, MaxPending: 600, BudgetCell: 1291, ExpiresUnix: 4102444800,
	}
	data, _ := json.Marshal(cfg)
	p := filepath.Join(dir, "mining-canary.json")
	hl1WriteFile(t, p, string(data))
	sum := sha256.Sum256(data)
	return p, hex.EncodeToString(sum[:])
}

func TestHL1LoadBootConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath, pin := hl1CanaryConfigFile(t, dir)
	db := filepath.Join(dir, legacymining.LegacyDirName, legacymining.DBFile)
	env := func(kv map[string]string) func(string) string {
		return func(k string) string { return kv[k] }
	}
	full := map[string]string{
		legacymining.EnvMode:               "canary",
		legacymining.EnvDB:                 db,
		legacymining.EnvCanaryConfig:       cfgPath,
		legacymining.EnvCanaryConfigSHA256: pin,
	}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range full {
			m[a] = b
		}
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
		return m
	}

	if b, err := hl1LoadBootConfig(env(nil), []string{"https://x"}); err != nil || b.Canary() {
		t.Fatalf("Stage A: %+v %v", b, err)
	}
	if b, err := hl1LoadBootConfig(env(full), nil); err != nil || !b.Canary() || b.Config.MaxPending != 600 {
		t.Fatalf("valid canary: %+v %v", b, err)
	}
	for name, tc := range map[string]struct {
		env  map[string]string
		urls []string
		want error
	}{
		"partial env":    {with(legacymining.EnvCanaryConfigSHA256, ""), nil, legacymining.ErrEnvPartial},
		"bad mode":       {with(legacymining.EnvMode, "open"), nil, legacymining.ErrConfig},
		"hash mismatch":  {with(legacymining.EnvCanaryConfigSHA256, strings.Repeat("0", 64)), nil, legacymining.ErrConfigHash},
		"missing config": {with(legacymining.EnvCanaryConfig, filepath.Join(dir, "nope.json")), nil, legacymining.ErrConfig},
		"sync URLs":      {full, []string{"https://api.qsdm.tech/api/v1"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := hl1LoadBootConfig(env(tc.env), tc.urls)
			if err == nil {
				t.Fatal("accepted")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	// The same refusals exit 78 through S1-S3.
	t.Setenv(legacymining.EnvMode, "canary")
	if code := hl1CatchExit(t, func() { hl1BootS1S3(t.TempDir(), "r") }); code != 78 {
		t.Fatalf("partial env through hl1BootS1S3: exit = %d, want 78", code)
	}
	for k, v := range full {
		t.Setenv(k, v)
	}
	t.Setenv("QSDM_CHAIN_SYNC_URLS", "https://api.qsdm.tech/api/v1")
	if code := hl1CatchExit(t, func() { hl1BootS1S3(t.TempDir(), "r") }); code != 78 {
		t.Fatalf("canary with sync URLs: exit = %d, want 78", code)
	}
}

func TestHL1SyncURLsNotStartedInProducerRole(t *testing.T) {
	urls := []string{"https://api.qsdm.tech/api/v1"}
	if hl1ShouldStartChainSync(true, urls) {
		t.Fatal("producer role starts HTTP chain sync")
	}
	if !hl1ShouldStartChainSync(false, urls) {
		t.Fatal("follower role does not start HTTP chain sync")
	}
	if hl1ShouldStartChainSync(false, nil) {
		t.Fatal("chain sync started without URLs")
	}
	if err := hl1CheckSyncURLs(legacymining.ModeOff, urls); err != nil {
		t.Fatalf("Stage A refuses sync URLs: %v", err)
	}
	if err := hl1CheckSyncURLs(legacymining.ModeCanary, urls); err == nil {
		t.Fatal("canary accepts sync URLs")
	}
}

func TestHL1ExternalAppendDisabledInProducerRole(t *testing.T) {
	calls := 0
	appendFn := func(*chain.Block) error { calls++; return nil }
	if err := hl1ExternalAppend(true, appendFn)(&chain.Block{}); !errors.Is(err, errHL1ExternalAppendDisabled) || calls != 0 {
		t.Fatalf("producer role: err=%v calls=%d", err, calls)
	}
	if err := hl1ExternalAppend(false, appendFn)(&chain.Block{}); err != nil || calls != 1 {
		t.Fatalf("follower role: err=%v calls=%d", err, calls)
	}
}
