package legacymining

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	hmacattest "github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
)

// -----------------------------------------------------------------------------
// Fixtures
// -----------------------------------------------------------------------------

var (
	gtStart  = time.Unix(1_790_000_000, 0)
	gtMiner  = strings.Repeat("ab", 32)
	gtOther  = strings.Repeat("cd", 32)
	gtHMACKy = bytes.Repeat([]byte{0x5a}, 32)
)

const (
	gtNode    = "canary-rtx4090-01"
	gtGPUUUID = "GPU-01234567-89ab-cdef-0123-456789abcdef"
	gtGPUName = "NVIDIA GeForce RTX 4090"
)

func gtConfig() Config {
	return Config{
		Version:         ConfigVersion,
		Allowed:         []AllowEntry{{MinerAddr: gtMiner, NodeID: gtNode}},
		MaxProofsPerMin: 60,
		MaxProofsTotal:  3000,
		MaxPending:      600,
		BudgetCell:      1291,
		ExpiresUnix:     gtStart.Unix() + 3600,
	}
}

type gtClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *gtClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *gtClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// gtStore records trip events. Only Event is implemented.
type gtStore struct {
	Store
	mu     sync.Mutex
	events []Event
}

func (s *gtStore) Event(ev Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

func (s *gtStore) snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

type gtEnv struct {
	dir      string
	clock    *gtClock
	store    *gtStore
	enrolled atomic.Bool
	mu       sync.Mutex
	stops    []string
	g        *CanaryGuard
}

func (e *gtEnv) failStop(code int, cause string) {
	e.mu.Lock()
	e.stops = append(e.stops, fmt.Sprintf("%d:%s", code, cause))
	e.mu.Unlock()
}

func (e *gtEnv) failStops() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.stops...)
}

func gtDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), LegacyDirName)
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

// gtNew builds a guard on dir, as one boot of the process.
func gtNew(t *testing.T, dir string, cfg Config) *gtEnv {
	t.Helper()
	e := &gtEnv{dir: dir, clock: &gtClock{t: gtStart}, store: &gtStore{}}
	e.enrolled.Store(true)
	g, err := NewGuard(GuardOptions{
		Dir:        dir,
		Config:     cfg,
		ConfigHash: sha256.Sum256([]byte("cfg")),
		Release:    "hl1-test",
		Store:      e.store,
		FailStop:   e.failStop,
		EnrollmentActive: func(node, owner string) bool {
			return node == gtNode && owner == gtMiner && e.enrolled.Load()
		},
		Now:  e.clock.Now,
		Logf: t.Logf,
	})
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	e.g = g
	return e
}

// tick advances the clock by d in 10 s steps, sealing one local block per
// step, as the driver does.
func (e *gtEnv) tick(d time.Duration) {
	for d > 0 {
		step := 10 * time.Second
		if d < step {
			step = d
		}
		e.clock.Advance(step)
		e.g.ObserveSeal(0, true)
		d -= step
	}
}

// open runs S14-S16 and the quiet period, and requires admission to open.
func (e *gtEnv) open(t *testing.T) {
	t.Helper()
	if err := e.g.PreArm(); err != nil {
		t.Fatalf("PreArm: %v", err)
	}
	e.g.Activate(true)
	e.tick(QuietPeriod)
	if !e.g.AdmissionOpen() {
		t.Fatalf("admission not open after the quiet period: %s", e.g.closedReason(e.clock.Now()))
	}
}

func gtExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func gtReadCause(t *testing.T, dir, marker string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, marker+CauseSuffix))
	if err != nil {
		t.Fatalf("cause file %s: %v", marker, err)
	}
	var mc MarkerCause
	if err := json.Unmarshal(b, &mc); err != nil || mc.AtNS == 0 {
		t.Fatalf("cause file %s = %q: %v", marker, b, err)
	}
	return mc.Cause
}

func gtTemps(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), TempPrefix) {
			out = append(out, e.Name())
		}
	}
	return out
}

// gtProof builds a canonical v2 nvidia-hmac-v1 proof for miner and node, and
// a registry that enrolls node, so the real HMAC verifier accepts it once.
func gtProof(t *testing.T, now time.Time, miner, node, attType string) ([]byte, mining.Proof, *hmacattest.InMemoryRegistry) {
	t.Helper()
	var nonce, root, mix [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	for i := range root {
		root[i], mix[i] = byte(i), byte(0xff-i)
	}
	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Height:     100,
		HeaderHash: [32]byte{0xaa},
		MinerAddr:  miner,
		BatchRoot:  root,
		BatchCount: 1,
		Nonce:      [16]byte{3},
		MixDigest:  mix,
		Attestation: mining.Attestation{
			Type: attType, GPUArch: "ada", Nonce: nonce, IssuedAt: now.Unix(),
		},
	}
	signed, err := hmacattest.Bundle{
		ChallengeBind: hmacattest.HexChallengeBind(miner, root, mix),
		ComputeCap:    "8.9",
		CUDAVersion:   "12.8",
		DriverVer:     "572.16",
		GPUName:       gtGPUName,
		GPUUUID:       gtGPUUUID,
		IssuedAt:      now.Unix(),
		NodeID:        node,
		Nonce:         hex.EncodeToString(nonce[:]),
	}.Sign(gtHMACKy)
	if err != nil {
		t.Fatal(err)
	}
	if p.Attestation.BundleBase64, err = signed.MarshalBase64(); err != nil {
		t.Fatal(err)
	}
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reg := hmacattest.NewInMemoryRegistry()
	if err := reg.Enroll(node, gtGPUUUID, gtHMACKy); err != nil {
		t.Fatal(err)
	}
	return raw, p, reg
}

// gtHMACReplay returns the live HMAC verifier's rejection of a replayed
// nonce, raw and as mining.Verifier flattens it (reject(ReasonAttestation,
// "%v", err)), plus a non-replay nonce mismatch from the same verifier.
func gtHMACReplay(t *testing.T) (raw, flat, mismatch error) {
	t.Helper()
	_, p, reg := gtProof(t, gtStart, gtMiner, gtNode, mining.AttestationTypeHMAC)
	v := hmacattest.NewVerifier(reg)
	v.NonceStore = hmacattest.NewInMemoryNonceStore(2 * mining.FreshnessWindow)
	if err := v.VerifyAttestation(p, gtStart); err != nil {
		t.Fatalf("first verification: %v", err)
	}
	raw = v.VerifyAttestation(p, gtStart)
	if !errors.Is(raw, mining.ErrAttestationNonceMismatch) {
		t.Fatalf("replay error = %v", raw)
	}
	flat = &mining.RejectError{Reason: mining.ReasonAttestation, Detail: fmt.Sprintf("%v", raw)}
	q := p
	q.Attestation.Nonce[0] ^= 1
	mismatch = v.VerifyAttestation(q, gtStart)
	if !errors.Is(mismatch, mining.ErrAttestationNonceMismatch) {
		t.Fatalf("mismatch error = %v", mismatch)
	}
	return raw, flat, mismatch
}

// gtFS wraps the OS file system with injected faults. full makes every
// allocation fail: creating a file (noCreate) or writing to one (noWrite).
type gtFS struct {
	durableFS
	noCreate   bool
	noWrite    bool
	syncErr    error
	renameErr  error // every rename
	armedErr   error // only renames of an .armed marker (D2)
	syncDirErr error
	renames    atomic.Int32
}

var errGTNoSpace = errors.New("no space left on device (simulated)")

func (f *gtFS) OpenFile(name string, flag int, perm fs.FileMode) (durableFile, error) {
	if f.noCreate && flag&os.O_CREATE != 0 {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errGTNoSpace}
	}
	file, err := f.durableFS.OpenFile(name, flag, perm)
	if err != nil {
		return nil, err
	}
	return &gtFile{durableFile: file, fs: f}, nil
}

func (f *gtFS) Rename(oldpath, newpath string) error {
	if f.renameErr != nil {
		return f.renameErr
	}
	if f.armedErr != nil && strings.HasSuffix(oldpath, ArmedSuffix) {
		return f.armedErr
	}
	f.renames.Add(1)
	return f.durableFS.Rename(oldpath, newpath)
}

func (f *gtFS) SyncDir(dir string) error {
	if f.syncDirErr != nil {
		return f.syncDirErr
	}
	return f.durableFS.SyncDir(dir)
}

type gtFile struct {
	durableFile
	fs *gtFS
}

func (f *gtFile) Write(p []byte) (int, error) {
	if f.fs.noWrite {
		return 0, errGTNoSpace
	}
	return f.durableFile.Write(p)
}

func (f *gtFile) Sync() error {
	if f.fs.syncErr != nil {
		return f.fs.syncErr
	}
	return f.durableFile.Sync()
}

// -----------------------------------------------------------------------------
// Environment and config (S2, §6.1)
// -----------------------------------------------------------------------------

func TestLoadEnv(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, LegacyDirName, DBFile)
	cfgPath := filepath.Join(root, "mining-canary.json")
	pin := sha256.Sum256([]byte("x"))
	full := map[string]string{
		EnvMode:               "canary",
		EnvDB:                 db,
		EnvCanaryConfig:       cfgPath,
		EnvCanaryConfigSHA256: hex.EncodeToString(pin[:]),
	}
	with := func(over map[string]string) func(string) string {
		return func(k string) string {
			if v, ok := over[k]; ok {
				return v
			}
			return full[k]
		}
	}

	e, err := LoadEnv(func(string) string { return "" })
	if err != nil || e.Mode != ModeOff {
		t.Fatalf("all unset: %+v, %v", e, err)
	}
	e, err = LoadEnv(with(nil))
	if err != nil || e.Mode != ModeCanary || e.DBPath != db || e.ConfigPath != cfgPath || e.ConfigSHA256 != pin ||
		e.LegacyDir() != filepath.Join(root, LegacyDirName) {
		t.Fatalf("full: %+v, %v", e, err)
	}
	for _, k := range []string{EnvMode, EnvDB, EnvCanaryConfig, EnvCanaryConfigSHA256} {
		if _, err := LoadEnv(with(map[string]string{k: ""})); !errors.Is(err, ErrEnvPartial) {
			t.Errorf("%s unset: %v, want ErrEnvPartial", k, err)
		}
		only := func(n string) string {
			if n == k {
				return full[k]
			}
			return ""
		}
		if _, err := LoadEnv(only); !errors.Is(err, ErrEnvPartial) {
			t.Errorf("only %s set: %v, want ErrEnvPartial", k, err)
		}
	}
	bad := []struct {
		over map[string]string
		want error
	}{
		{map[string]string{EnvMode: "off"}, ErrConfig},
		{map[string]string{EnvMode: "Canary"}, ErrConfig},
		{map[string]string{EnvMode: " canary"}, ErrConfig},
		{map[string]string{EnvDB: filepath.Join(LegacyDirName, DBFile)}, ErrConfig},
		{map[string]string{EnvDB: filepath.Join(root, "other", DBFile)}, ErrConfig},
		{map[string]string{EnvDB: filepath.Join(root, LegacyDirName, "x.db")}, ErrConfig},
		{map[string]string{EnvDB: db + string(filepath.Separator)}, ErrConfig},
		{map[string]string{EnvCanaryConfig: "mining-canary.json"}, ErrConfig},
		{map[string]string{EnvCanaryConfigSHA256: "abc"}, ErrConfigHash},
		{map[string]string{EnvCanaryConfigSHA256: strings.Repeat("zz", 32)}, ErrConfigHash},
	}
	for _, b := range bad {
		if _, err := LoadEnv(with(b.over)); !errors.Is(err, b.want) {
			t.Errorf("%v: %v, want %v", b.over, err, b.want)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	good := `{"version":1,"allowed":[{"miner_addr":"` + gtMiner + `","node_id":"` + gtNode + `"}],
 "max_proofs_per_min":60,"max_proofs_total":3000,"max_pending":600,
 "budget_cell":1291,"expires_unix":1790003600}` + "\n"
	path := filepath.Join(t.TempDir(), "mining-canary.json")
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	pin := sha256.Sum256([]byte(good))
	c, err := LoadConfig(path, pin)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if want := gtConfig(); fmt.Sprint(c) != fmt.Sprint(want) {
		t.Fatalf("config = %+v, want %+v", c, want)
	}
	if _, err := LoadConfig(path, sha256.Sum256([]byte("other"))); !errors.Is(err, ErrConfigHash) {
		t.Errorf("pin mismatch: %v", err)
	}
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "missing.json"), pin); !errors.Is(err, ErrConfig) {
		t.Errorf("missing file: %v", err)
	}
	huge := bytes.Repeat([]byte(" "), maxConfigBytes+1)
	hugePath := filepath.Join(t.TempDir(), "huge.json")
	if err := os.WriteFile(hugePath, huge, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(hugePath, sha256.Sum256(huge)); !errors.Is(err, ErrConfig) {
		t.Errorf("oversized file: %v", err)
	}

	edit := func(from, to string) string {
		if !strings.Contains(good, from) {
			t.Fatalf("fixture lacks %q", from)
		}
		return strings.Replace(good, from, to, 1)
	}
	entry := `[{"miner_addr":"` + gtMiner + `","node_id":"` + gtNode + `"}]`
	two := `[{"miner_addr":"` + gtMiner + `","node_id":"` + gtNode + `"},{"miner_addr":"` + gtOther + `","node_id":"n2"}]`
	// Each case names the text its error must contain, so a case cannot pass
	// through an unrelated rule.
	for name, c := range map[string]struct{ doc, want string }{
		"unknown field":      {edit(`"version":1`, `"version":1,"extra":0`), "unknown field"},
		"unknown nested":     {edit(`"node_id"`, `"x":1,"node_id"`), "unknown field"},
		"trailing object":    {good + `{}`, "trailing data"},
		"trailing garbage":   {good + `x`, "trailing data"},
		"not an object":      {`[]`, "cannot unmarshal"},
		"null":               {`null`, "version 0"},
		"version 2":          {edit(`"version":1`, `"version":2`), "version 2"},
		"no allowed":         {edit(entry, `[]`), "0 allowed entries"},
		"null allowed":       {edit(entry, `null`), "0 allowed entries"},
		"two allowed":        {edit(entry, two), "2 allowed entries"},
		"upper-case miner":   {edit(gtMiner, strings.ToUpper(gtMiner)), "miner_addr"},
		"short miner":        {edit(gtMiner, gtMiner[2:]), "miner_addr"},
		"non-hex miner":      {edit(gtMiner, "zz"+gtMiner[2:]), "miner_addr"},
		"empty node":         {edit(`"node_id":"`+gtNode+`"`, `"node_id":""`), "node_id is empty"},
		"zero per-minute":    {edit(`"max_proofs_per_min":60`, `"max_proofs_per_min":0`), "max_proofs_per_min"},
		"negative per-min":   {edit(`"max_proofs_per_min":60`, `"max_proofs_per_min":-1`), "max_proofs_per_min"},
		"zero total":         {edit(`"max_proofs_total":3000`, `"max_proofs_total":0`), "max_proofs_total"},
		"zero pending":       {edit(`"max_pending":600`, `"max_pending":0`), "max_pending 0"},
		"pending over 1024":  {edit(`"max_pending":600`, `"max_pending":1025`), "max_pending 1025"},
		"zero budget":        {edit(`"budget_cell":1291`, `"budget_cell":0`), "budget_cell"},
		"zero expiry":        {edit(`"expires_unix":1790003600`, `"expires_unix":0`), "expires_unix"},
		"string number":      {edit(`"max_pending":600`, `"max_pending":"600"`), "cannot unmarshal"},
		"fractional pending": {edit(`"max_pending":600`, `"max_pending":600.5`), "cannot unmarshal"},
	} {
		_, err := ParseConfig([]byte(c.doc), sha256.Sum256([]byte(c.doc)))
		if !errors.Is(err, ErrConfig) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want ErrConfig containing %q", name, err, c.want)
		}
	}
	edge := edit(`"max_pending":600`, `"max_pending":1024`)
	if c, err := ParseConfig([]byte(edge), sha256.Sum256([]byte(edge))); err != nil || c.MaxPending != MaxPendingLimit {
		t.Errorf("max_pending 1024: %+v, %v", c, err)
	}
	// An expired config loads: expiry is a trigger, not a load failure.
	old := edit(`"expires_unix":1790003600`, `"expires_unix":1`)
	if _, err := ParseConfig([]byte(old), sha256.Sum256([]byte(old))); err != nil {
		t.Errorf("expired config must load: %v", err)
	}
}

func TestNewGuardRefusals(t *testing.T) {
	dir := gtDir(t)
	opts := func() GuardOptions {
		return GuardOptions{
			Dir: dir, Config: gtConfig(), FailStop: func(int, string) {},
			EnrollmentActive: func(string, string) bool { return true },
		}
	}
	o := opts()
	o.Config.MaxPending = 0
	if _, err := NewGuard(o); !errors.Is(err, ErrConfig) {
		t.Errorf("invalid config: %v", err)
	}
	o = opts()
	o.FailStop = nil
	if _, err := NewGuard(o); err == nil {
		t.Error("nil FailStop accepted")
	}
	o = opts()
	o.EnrollmentActive = nil
	if _, err := NewGuard(o); err == nil {
		t.Error("nil EnrollmentActive accepted")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, d := range map[string]string{
		"relative": LegacyDirName,
		"missing":  filepath.Join(t.TempDir(), "absent"),
		"file":     file,
	} {
		o = opts()
		o.Dir = d
		if _, err := NewGuard(o); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s dir: %v, want ErrUnsafePath", name, err)
		}
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err == nil {
		o = opts()
		o.Dir = link
		if _, err := NewGuard(o); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("symlink dir: %v, want ErrUnsafePath", err)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		wide := filepath.Join(t.TempDir(), LegacyDirName)
		if err := os.Mkdir(wide, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(wide, 0o750); err != nil {
			t.Fatal(err)
		}
		o = opts()
		o.Dir = wide
		if _, err := NewGuard(o); !errors.Is(err, ErrUnsafePath) {
			t.Errorf("mode 0750 dir: %v, want ErrUnsafePath", err)
		}
	}
	if _, err := NewGuard(opts()); err != nil {
		t.Fatalf("valid options: %v", err)
	}
}

// -----------------------------------------------------------------------------
// D1/D2 (§3.2)
// -----------------------------------------------------------------------------

func TestD1WriteFileDurable(t *testing.T) {
	dir := gtDir(t)
	// A stale temp from a SIGKILLed writer never blocks a later D1.
	stale := filepath.Join(dir, fmt.Sprintf("%sW-%d-0000000000000000", TempPrefix, os.Getpid()))
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"one", "two"} {
		if err := WriteFileDurable(dir, "W", []byte(body)); err != nil {
			t.Fatalf("D1: %v", err)
		}
		if b, err := os.ReadFile(filepath.Join(dir, "W")); err != nil || string(b) != body {
			t.Fatalf("W = %q, %v", b, err)
		}
	}
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(filepath.Join(dir, "W")); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("mode: %v %v", fi.Mode(), err)
		}
	}
	if got := gtTemps(t, dir); len(got) != 1 || filepath.Join(dir, got[0]) != stale {
		t.Errorf("temps after D1 = %v, want only the stale one", got)
	}
	for _, name := range []string{"", ".", "..", "a/b", filepath.Join("a", "b")} {
		if err := WriteFileDurable(dir, name, nil); err == nil {
			t.Errorf("name %q accepted", name)
		}
	}

	// Failures before the rename leave no temp and no target.
	for name, f := range map[string]*gtFS{
		"no space to create": {noCreate: true},
		"no space to write":  {noWrite: true},
		"fsync fails":        {syncErr: errors.New("EIO")},
		"rename fails":       {renameErr: errors.New("EIO")},
	} {
		f.durableFS = osDurableFS{}
		if err := d1Write(f, dir, "X", []byte("x")); err == nil {
			t.Errorf("%s: D1 succeeded", name)
		}
		if gtExists(t, filepath.Join(dir, "X")) || len(gtTemps(t, dir)) != 1 {
			t.Errorf("%s: left X or a temp: %v", name, gtTemps(t, dir))
		}
	}
	f := &gtFS{durableFS: osDurableFS{}, syncDirErr: errors.New("EIO")}
	if err := d1Write(f, dir, "Y", []byte("y")); err == nil {
		t.Error("D1 with failing fsync(dir) succeeded")
	}

	n, err := RemoveStaleTemps(dir)
	if err != nil || n != 1 || gtExists(t, stale) || !gtExists(t, filepath.Join(dir, "W")) {
		t.Errorf("RemoveStaleTemps = %d, %v", n, err)
	}
	if err := os.Mkdir(filepath.Join(dir, TempPrefix+"dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if n, err := RemoveStaleTemps(dir); err != nil || n != 0 {
		t.Errorf("RemoveStaleTemps with a temp-named directory = %d, %v", n, err)
	}
	if n, err := RemoveStaleTemps(filepath.Join(dir, "absent")); err != nil || n != 0 {
		t.Errorf("RemoveStaleTemps on a missing dir = %d, %v", n, err)
	}
}

func TestArmAndTripMarker(t *testing.T) {
	dir := gtDir(t)
	armed := filepath.Join(dir, "X"+ArmedSuffix)
	tripped := filepath.Join(dir, "X"+TrippedSuffix)
	if ok, err := ArmMarker(dir, "X", []byte("A\n")); !ok || err != nil {
		t.Fatalf("ArmMarker = %v, %v", ok, err)
	}
	if err := TripMarker(dir, "X", []byte("fallback\n")); err != nil {
		t.Fatalf("TripMarker: %v", err)
	}
	if gtExists(t, armed) {
		t.Error("armed marker survived the rename")
	}
	if b, _ := os.ReadFile(tripped); string(b) != "A\n" {
		t.Errorf("tripped content = %q, want the armed content", b)
	}
	// Already tripped: no re-arm and no change.
	if ok, err := ArmMarker(dir, "X", []byte("B\n")); ok || err != nil || gtExists(t, armed) {
		t.Errorf("ArmMarker over a tripped marker = %v, %v", ok, err)
	}
	if err := TripMarker(dir, "X", []byte("C\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(tripped); string(b) != "A\n" {
		t.Errorf("second trip changed the marker to %q", b)
	}
	// Never armed: D1 fallback.
	if err := TripMarker(dir, "Y", []byte("fallback\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Y"+TrippedSuffix)); string(b) != "fallback\n" {
		t.Errorf("fallback content = %q", b)
	}
}

// -----------------------------------------------------------------------------
// Latches, pre-armed markers and marker I/O (§6.5, §3.2)
// -----------------------------------------------------------------------------

func TestLatchesSurviveRestart(t *testing.T) {
	dir := gtDir(t)
	e := gtNew(t, dir, gtConfig())
	e.open(t)
	for _, f := range []string{TrippedArmedFile, AdmissionStoppedArmedFile} {
		if !gtExists(t, filepath.Join(dir, f)) {
			t.Fatalf("PreArm did not create %s", f)
		}
	}

	e.g.StopAdmission(CauseExpired)
	e.g.StopAdmission("second cause is ignored")
	if s := e.g.State(); s != StateAdmissionStopped || !s.PayoutsEnabled() || e.g.AdmissionOpen() {
		t.Fatalf("after StopAdmission: %v open=%v", s, e.g.AdmissionOpen())
	}
	e.g.Freeze(CauseStall)
	e.g.Freeze("second cause is ignored")
	if s := e.g.State(); s != StateFrozen || s.PayoutsEnabled() {
		t.Fatalf("after Freeze: %v", s)
	}
	for _, m := range []string{MarkerTripped, MarkerAdmissionStopped} {
		if gtExists(t, filepath.Join(dir, m+ArmedSuffix)) || !gtExists(t, filepath.Join(dir, m+TrippedSuffix)) {
			t.Errorf("%s: armed marker not renamed to the latch", m)
		}
		var am ArmedMarker
		b, _ := os.ReadFile(filepath.Join(dir, m+TrippedSuffix))
		if err := json.Unmarshal(b, &am); err != nil || am.Release != "hl1-test" || am.PID != os.Getpid() || am.BootNS != gtStart.UnixNano() {
			t.Errorf("%s content = %q, %v", m, b, err)
		}
	}
	if c := gtReadCause(t, dir, MarkerTripped); c != CauseStall {
		t.Errorf("TRIPPED cause = %q", c)
	}
	if c := gtReadCause(t, dir, MarkerAdmissionStopped); c != CauseExpired {
		t.Errorf("ADMISSION_STOPPED cause = %q", c)
	}
	evs := e.store.snapshot()
	if len(evs) != 2 || evs[0].Kind != "admission-stop" || evs[0].Detail != CauseExpired || evs[1].Kind != "freeze" || evs[1].Detail != CauseStall {
		t.Errorf("events = %+v", evs)
	}
	if len(e.failStops()) != 0 {
		t.Errorf("fail-stops: %v", e.failStops())
	}

	// Restart: both latches hold, PreArm re-arms neither, admission stays shut.
	e2 := gtNew(t, dir, gtConfig())
	if s := e2.g.State(); s != StateFrozen {
		t.Fatalf("after restart: %v", s)
	}
	if err := e2.g.PreArm(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{TrippedArmedFile, AdmissionStoppedArmedFile} {
		if gtExists(t, filepath.Join(dir, f)) {
			t.Errorf("PreArm re-armed %s over its latch", f)
		}
	}
	e2.g.Activate(true)
	e2.tick(2 * QuietPeriod)
	if e2.g.AdmissionOpen() {
		t.Fatal("admission opened with latches present")
	}
	if err := e2.g.Admit(); RejectKindOf(err) != KindAdmissionClosed || !strings.Contains(err.Error(), "FROZEN") {
		t.Errorf("Admit = %v", err)
	}

	// Clearing TRIPPED.json and restarting leaves ADMISSION_STOPPED.
	if err := os.Remove(filepath.Join(dir, TrippedFile)); err != nil {
		t.Fatal(err)
	}
	e3 := gtNew(t, dir, gtConfig())
	if s := e3.g.State(); s != StateAdmissionStopped {
		t.Fatalf("after clearing TRIPPED: %v", s)
	}
	if err := e3.g.PreArm(); err != nil || !gtExists(t, filepath.Join(dir, TrippedArmedFile)) || gtExists(t, filepath.Join(dir, AdmissionStoppedArmedFile)) {
		t.Fatalf("PreArm after clearing TRIPPED: %v", err)
	}

	// Clearing both reopens after the quiet period (config not yet expired).
	if err := os.Remove(filepath.Join(dir, AdmissionStoppedFile)); err != nil {
		t.Fatal(err)
	}
	e4 := gtNew(t, dir, gtConfig())
	e4.open(t)
}

func TestKillLatch(t *testing.T) {
	dir := gtDir(t)
	e := gtNew(t, dir, gtConfig())
	e.open(t)
	kill := filepath.Join(dir, KillFile)
	if err := os.WriteFile(kill, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if s := e.g.State(); s != StateKilled || s.PayoutsEnabled() {
		t.Fatalf("with KILL: %v", s)
	}
	err := e.g.Admit()
	if RejectKindOf(err) != KindAdmissionClosed || !errors.Is(err, ErrUnavailable) || !strings.Contains(err.Error(), "KILLED") {
		t.Fatalf("Admit with KILL = %v", err)
	}
	// KILLED outranks FROZEN.
	e.g.Freeze(CauseInvariant)
	if s := e.g.State(); s != StateKilled {
		t.Errorf("KILL+FROZEN: %v", s)
	}
	// Sticky until restart.
	if err := os.Remove(kill); err != nil {
		t.Fatal(err)
	}
	if s := e.g.State(); s != StateKilled || e.g.AdmissionOpen() {
		t.Errorf("KILL removed at runtime: %v", s)
	}
	// Survives restart while the file exists.
	if err := os.WriteFile(kill, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, TrippedFile)); err != nil {
		t.Fatal(err)
	}
	e2 := gtNew(t, dir, gtConfig())
	e2.g.Activate(true)
	e2.tick(2 * QuietPeriod)
	if s := e2.g.State(); s != StateKilled || e2.g.AdmissionOpen() {
		t.Errorf("KILL after restart: %v", s)
	}
	if err := os.Remove(kill); err != nil {
		t.Fatal(err)
	}
	e3 := gtNew(t, dir, gtConfig())
	e3.open(t)
}

// §7: pre-armed rename on a full file system. The rename allocates nothing, so
// both latches trip durably although no new file can be created.
func TestPreArmedTripOnFullFilesystem(t *testing.T) {
	for _, mode := range []string{"no-create", "no-write"} {
		t.Run(mode, func(t *testing.T) {
			dir := gtDir(t)
			e := gtNew(t, dir, gtConfig())
			e.open(t)
			full := &gtFS{durableFS: osDurableFS{}, noCreate: mode == "no-create", noWrite: mode == "no-write"}
			e.g.fs = full
			e.g.Freeze(CauseMarkPaidIO)
			e.g.StopAdmission(CauseBudget)
			if got := e.failStops(); len(got) != 0 {
				t.Fatalf("fail-stop on a full file system with pre-armed markers: %v", got)
			}
			if full.renames.Load() != 2 {
				t.Errorf("renames = %d, want 2", full.renames.Load())
			}
			for _, m := range []string{MarkerTripped, MarkerAdmissionStopped} {
				if !gtExists(t, filepath.Join(dir, m+TrippedSuffix)) || gtExists(t, filepath.Join(dir, m+ArmedSuffix)) {
					t.Errorf("%s not tripped by rename", m)
				}
				if gtExists(t, filepath.Join(dir, m+CauseSuffix)) {
					t.Errorf("%s cause file written on a full file system", m)
				}
			}
			if len(gtTemps(t, dir)) != 0 {
				t.Errorf("temps left: %v", gtTemps(t, dir))
			}
			if s := e.g.State(); s != StateFrozen {
				t.Errorf("state %v", s)
			}
			e2 := gtNew(t, dir, gtConfig())
			if !e2.g.frozen.Load() || !e2.g.stopped.Load() {
				t.Error("latches lost across restart")
			}
		})
	}
}

// §3.2 D2 and §6.6: a failed marker rename with a failed D1 fallback fails
// stop with ExitFailStop and CauseMarkerIO; the in-memory latch holds anyway.
func TestMarkerIOFailStop(t *testing.T) {
	type tc struct {
		name     string
		prearm   bool
		fs       *gtFS
		failStop bool
	}
	cases := []tc{
		{"rename and D1 fail", true, &gtFS{renameErr: errors.New("EIO"), noCreate: true}, true},
		{"not armed, full", false, &gtFS{noCreate: true}, true},
		{"fsync(dir) fails", true, &gtFS{syncDirErr: errors.New("EIO")}, true},
		{"marker rename fails, D1 works", true, &gtFS{armedErr: errors.New("EIO")}, false},
		{"not armed, D1 works", false, &gtFS{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := gtDir(t)
			e := gtNew(t, dir, gtConfig())
			if c.prearm {
				if err := e.g.PreArm(); err != nil {
					t.Fatal(err)
				}
			}
			c.fs.durableFS = osDurableFS{}
			e.g.fs = c.fs
			e.g.Freeze(CauseTxFamily)
			e.g.StopAdmission(CauseDuplicates)
			want := []string(nil)
			if c.failStop {
				want = []string{"86:marker-io", "86:marker-io"}
			}
			if got := e.failStops(); fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("fail-stops = %v, want %v", got, want)
			}
			if s := e.g.State(); s != StateFrozen || !e.g.stopped.Load() {
				t.Errorf("in-memory latches: %v stopped=%v", s, e.g.stopped.Load())
			}
			if !c.failStop {
				for _, m := range []string{MarkerTripped, MarkerAdmissionStopped} {
					if !gtExists(t, filepath.Join(dir, m+TrippedSuffix)) {
						t.Errorf("%s missing after the D1 fallback", m)
					}
				}
			}
		})
	}
	// PreArm itself on a full file system reports the error (the caller
	// then freezes with CausePreArm, which fails stop as above).
	dir := gtDir(t)
	e := gtNew(t, dir, gtConfig())
	e.g.fs = &gtFS{durableFS: osDurableFS{}, noCreate: true}
	if err := e.g.PreArm(); err == nil || !errors.Is(err, errGTNoSpace) {
		t.Errorf("PreArm on a full file system = %v", err)
	}
}

// -----------------------------------------------------------------------------
// Admission: S16, allowlist, caps and the 503/400 mapping (§4.1, §6.2)
// -----------------------------------------------------------------------------

func TestAdmissionConditions(t *testing.T) {
	dir := gtDir(t)
	e := gtNew(t, dir, gtConfig())
	if err := e.g.PreArm(); err != nil {
		t.Fatal(err)
	}
	reason := func() string { return e.g.closedReason(e.clock.Now()) }
	if e.g.AdmissionOpen() || reason() != "not-activated" {
		t.Fatalf("before Activate: %q", reason())
	}
	// Seals before Activate do not count.
	for i := 0; i < 20; i++ {
		e.g.ObserveSeal(uint64(i), true)
	}
	e.g.Activate(true)
	e.g.Activate(false) // ignored
	if !e.g.clean.Load() {
		t.Fatal("second Activate took effect")
	}
	e.tick(QuietPeriod - time.Second)
	if e.g.AdmissionOpen() || reason() != "quiet-period" {
		t.Fatalf("at 119 s: %q", reason())
	}
	e.clock.Advance(time.Second)
	if !e.g.AdmissionOpen() {
		t.Fatalf("at 120 s with 12 seals: %q", reason())
	}

	// The seal count gates independently of time; non-local seals do not
	// count. Six local seals 20 s apart reach 120 s, then 130 s.
	e3 := gtNew(t, gtDir(t), gtConfig())
	e3.g.Activate(true)
	for i := 0; i < QuietSeals-1; i++ {
		e3.clock.Advance(20 * time.Second)
		e3.g.ObserveSeal(uint64(i), true)
		e3.g.ObserveSeal(uint64(i), false)
		e3.g.ObserveSeal(uint64(i), false)
	}
	e3.clock.Advance(10 * time.Second)
	if e3.g.AdmissionOpen() || e3.g.closedReason(e3.clock.Now()) != "quiet-period" {
		t.Fatalf("6 local seals: %q", e3.g.closedReason(e3.clock.Now()))
	}
	e3.g.ObserveSeal(7, true)
	if !e3.g.AdmissionOpen() {
		t.Fatalf("7 local seals: %q", e3.g.closedReason(e3.clock.Now()))
	}

	// Enrollment must be active and owned by the allowlisted miner.
	e.enrolled.Store(false)
	if e.g.AdmissionOpen() || reason() != "enrollment-inactive" {
		t.Fatalf("inactive enrollment: %q", reason())
	}
	e.enrolled.Store(true)
	if !e.g.AdmissionOpen() {
		t.Fatal("enrollment restored")
	}

	// Reconciliation not clean: never opens in this process.
	e4 := gtNew(t, gtDir(t), gtConfig())
	e4.g.Activate(false)
	e4.tick(10 * QuietPeriod)
	if e4.g.AdmissionOpen() || e4.g.closedReason(e4.clock.Now()) != "reconcile-not-clean" {
		t.Fatalf("unclean reconcile: %q", e4.g.closedReason(e4.clock.Now()))
	}

	// Expiry latches ADMISSION_STOPPED; payouts continue.
	e.tick(time.Duration(gtConfig().ExpiresUnix-e.clock.Now().Unix()) * time.Second)
	if s := e.g.State(); s != StateAdmissionStopped || !s.PayoutsEnabled() || e.g.AdmissionOpen() {
		t.Fatalf("at expiry: %v", s)
	}
	if c := gtReadCause(t, dir, MarkerAdmissionStopped); !strings.HasPrefix(c, CauseExpired) {
		t.Errorf("expiry cause = %q", c)
	}
}

func TestRejectionMapping(t *testing.T) {
	e := gtNew(t, gtDir(t), gtConfig())
	is503 := func(name string, err error, kind RejectKind) {
		t.Helper()
		var re *mining.RejectError
		if RejectKindOf(err) != kind || kind.HTTPStatus() != http.StatusServiceUnavailable ||
			!errors.Is(err, ErrUnavailable) || errors.As(err, &re) {
			t.Errorf("%s: %v (kind %v), want 503 %v", name, err, RejectKindOf(err), kind)
		}
	}
	is400 := func(name string, err error, kind RejectKind, reason mining.RejectReason) {
		t.Helper()
		var re *mining.RejectError
		if RejectKindOf(err) != kind || kind.HTTPStatus() != http.StatusBadRequest ||
			errors.Is(err, ErrUnavailable) || !errors.As(err, &re) || re.Reason != reason {
			t.Errorf("%s: %v (kind %v), want 400 %v/%s", name, err, RejectKindOf(err), kind, reason)
		}
	}

	// Step 3: admission closed is 503, before and after activation.
	is503("admit before Activate", e.g.Admit(), KindAdmissionClosed)
	e.open(t)
	if err := e.g.Admit(); err != nil {
		t.Fatalf("Admit while open: %v", err)
	}

	// Step 4: Precheck rejections are 400.
	now := e.clock.Now()
	good, p, _ := gtProof(t, now, gtMiner, gtNode, mining.AttestationTypeHMAC)
	c, err := e.g.Precheck(good)
	if err != nil || c.Proof == nil || c.Proof.MinerAddr != gtMiner || c.NodeID != gtNode || c.AttNonce != p.Attestation.Nonce {
		t.Fatalf("Precheck(good) = %+v, %v", c, err)
	}
	is400("empty object", func() error { _, err := e.g.Precheck([]byte(`{}`)); return err }(), KindMalformed, mining.ReasonNonCanonical)
	is400("not JSON", func() error { _, err := e.g.Precheck([]byte(`nope`)); return err }(), KindMalformed, mining.ReasonNonCanonical)
	cc, _, _ := gtProof(t, now, gtMiner, gtNode, mining.AttestationTypeCC)
	is400("cc attestation", func() error { _, err := e.g.Precheck(cc); return err }(), KindAttestationType, mining.ReasonAttestation)
	badBundle := p
	badBundle.Attestation.BundleBase64 = "!!!"
	raw, _ := badBundle.CanonicalJSON()
	is400("bad bundle", func() error { _, err := e.g.Precheck(raw); return err }(), KindMalformed, mining.ReasonNonCanonical)
	wrongMiner, _, _ := gtProof(t, now, gtOther, gtNode, mining.AttestationTypeHMAC)
	is400("wrong miner", func() error { _, err := e.g.Precheck(wrongMiner); return err }(), KindMinerNotAllowed, mining.ReasonBadAddr)
	wrongNode, _, _ := gtProof(t, now, gtMiner, "other-node", mining.AttestationTypeHMAC)
	is400("wrong node", func() error { _, err := e.g.Precheck(wrongNode); return err }(), KindNodeNotAllowed, mining.ReasonAttestation)

	// Step 5: the per-minute cap is 503.
	for i := 0; i < gtConfig().MaxProofsPerMin; i++ {
		if err := e.g.TakeRate(); err != nil {
			t.Fatalf("TakeRate %d: %v", i, err)
		}
	}
	is503("rate cap", e.g.TakeRate(), KindRateLimited)
	e.tick(time.Minute)
	if err := e.g.TakeRate(); err != nil {
		t.Errorf("TakeRate in the next minute: %v", err)
	}

	// Step 8: the Store's UNIQUE rejections are 400 and the guard does not
	// turn them into FREEZE.
	is400("UNIQUE proof_id", fmt.Errorf("accept: %w", &Rejection{Kind: KindDuplicate}), KindDuplicate, mining.ReasonDuplicate)
	is400("UNIQUE nonce", fmt.Errorf("accept: %w", &Rejection{Kind: KindNonceConflict}), KindNonceConflict, mining.ReasonAttestation)
	e.g.ObserveRejection(&Rejection{Kind: KindDuplicate})
	if s := e.g.State(); s != StateOpen {
		t.Errorf("state after a UNIQUE hit: %v", s)
	}
	// After a trip, submissions are 503 again.
	e.g.Freeze(CauseAcceptIO)
	is503("admit while FROZEN", e.g.Admit(), KindAdmissionClosed)
	if len(e.failStops()) != 0 {
		t.Errorf("fail-stops: %v", e.failStops())
	}
}

// -----------------------------------------------------------------------------
// Automatic triggers (§6.6)
// -----------------------------------------------------------------------------

// TestTriggerTable drives every §6.6 trigger the guard owns from an open
// guard, and the negative control next to each threshold.
func TestTriggerTable(t *testing.T) {
	rawReplay, flatReplay, nonceMismatch := gtHMACReplay(t)
	cfg := gtConfig()
	rc := 3.56490987
	precheck := func(e *gtEnv, miner, node string) {
		raw, _, _ := gtProof(t, e.clock.Now(), miner, node, mining.AttestationTypeHMAC)
		if _, err := e.g.Precheck(raw); err == nil {
			t.Fatalf("Precheck(%s, %s) passed", miner[:4], node)
		}
	}
	admitN := func(e *gtEnv, n int) {
		for i := 0; i < n; i++ {
			_ = e.g.Admit()
		}
	}
	dupErrs := []error{
		&Rejection{Kind: KindDuplicate, Detail: "proof_id"},
		fmt.Errorf("store: %w", &Rejection{Kind: KindNonceConflict}),
		&mining.RejectError{Reason: mining.ReasonDuplicate, Detail: "proof 01 already seen"},
		flatReplay,
		rawReplay,
	}
	ignored := []error{
		nil,
		errors.New("disk I/O error"),
		&Rejection{Kind: KindUnavailable},
		&Rejection{Kind: KindMalformed},
		&mining.RejectError{Reason: mining.ReasonStaleHeight},
		&mining.RejectError{Reason: mining.ReasonAttestation, Detail: "hmac: hmac mismatch"},
		&mining.RejectError{Reason: mining.ReasonAttestation, Detail: nonceMismatch.Error()},
		nonceMismatch,
	}
	observe := func(e *gtEnv, n int) {
		for i := 0; i < n; i++ {
			for _, err := range ignored {
				e.g.ObserveRejection(err)
			}
			e.g.ObserveRejection(dupErrs[i%len(dupErrs)])
		}
	}
	totals := func(proofs uint64, emitted float64) Totals {
		return Totals{ConfigSHA256: sha256.Sum256([]byte("cfg")), Proofs: proofs, Emitted: emitted}
	}
	budgetStop := float64(cfg.BudgetCell) - BudgetStopMarginCells*rc

	type tc struct {
		name  string
		drive func(e *gtEnv)
		want  State
		cause string // cause-file prefix; "" when the state stays OPEN
	}
	cases := []tc{
		// ADMISSION_STOP: >= 5 non-allowlisted submissions in 10 min.
		{"4 non-allowlisted", func(e *gtEnv) {
			for i := 0; i < NotAllowlistedLimit-1; i++ {
				precheck(e, gtOther, gtNode)
			}
		}, StateOpen, ""},
		{"5 non-allowlisted (miner and node)", func(e *gtEnv) {
			for i := 0; i < NotAllowlistedLimit; i++ {
				if i%2 == 0 {
					precheck(e, gtOther, gtNode)
				} else {
					precheck(e, gtMiner, "other-node")
				}
			}
		}, StateAdmissionStopped, CauseNotAllowlisted},
		{"5 non-allowlisted over more than 10 min", func(e *gtEnv) {
			for i := 0; i < NotAllowlistedLimit; i++ {
				precheck(e, gtOther, gtNode)
				e.tick(TriggerWindow / (NotAllowlistedLimit - 1))
			}
		}, StateOpen, ""},
		{"malformed and wrong type are not non-allowlisted", func(e *gtEnv) {
			for i := 0; i < 2*NotAllowlistedLimit; i++ {
				_, _ = e.g.Precheck([]byte(`{}`))
				cc, _, _ := gtProof(t, e.clock.Now(), gtMiner, gtNode, mining.AttestationTypeCC)
				_, _ = e.g.Precheck(cc)
			}
		}, StateOpen, ""},
		// ADMISSION_STOP: > 10 duplicates or nonce conflicts in 10 min,
		// counting DB UNIQUE hits, verifier duplicates and HMAC replays.
		{"10 duplicates", func(e *gtEnv) { observe(e, DuplicateLimit) }, StateOpen, ""},
		{"11 duplicates (mixed)", func(e *gtEnv) { observe(e, DuplicateLimit+1) }, StateAdmissionStopped, CauseDuplicates},
		{"11 DB UNIQUE hits only", func(e *gtEnv) {
			for i := 0; i <= DuplicateLimit; i++ {
				k := KindDuplicate
				if i%2 == 1 {
					k = KindNonceConflict
				}
				e.g.ObserveRejection(fmt.Errorf("accept: %w", &Rejection{Kind: k}))
			}
		}, StateAdmissionStopped, CauseDuplicates},
		{"11 duplicates over more than 10 min", func(e *gtEnv) {
			for i := 0; i <= DuplicateLimit; i++ {
				e.g.ObserveRejection(&Rejection{Kind: KindDuplicate})
				e.tick(TriggerWindow / DuplicateLimit)
			}
		}, StateOpen, ""},
		// ADMISSION_STOP: > 2 x max_proofs_per_min in each of 3 consecutive minutes.
		{"burst in 3 consecutive minutes", func(e *gtEnv) {
			for m := 0; m < RateBurstMinutes; m++ {
				admitN(e, RateBurstFactor*cfg.MaxProofsPerMin+1)
				e.tick(time.Minute)
			}
		}, StateAdmissionStopped, CauseRateBurst},
		{"exactly 2x in 3 consecutive minutes", func(e *gtEnv) {
			for m := 0; m < RateBurstMinutes; m++ {
				admitN(e, RateBurstFactor*cfg.MaxProofsPerMin)
				e.tick(time.Minute)
			}
		}, StateOpen, ""},
		{"burst in 2 minutes, a gap, then 1", func(e *gtEnv) {
			for _, n := range []int{121, 121, 0, 121} {
				admitN(e, n)
				e.tick(time.Minute)
			}
		}, StateOpen, ""},
		{"burst resumes after a gap", func(e *gtEnv) {
			for _, n := range []int{121, 121, 0, 121, 121, 121} {
				admitN(e, n)
				e.tick(time.Minute)
			}
		}, StateAdmissionStopped, CauseRateBurst},
		// Graceful ADMISSION_STOP.
		{"proofs below T", func(e *gtEnv) { e.g.ObserveTotals(totals(cfg.MaxProofsTotal-1, 0), rc) }, StateOpen, ""},
		{"proofs_H >= T", func(e *gtEnv) { e.g.ObserveTotals(totals(cfg.MaxProofsTotal, 0), rc) }, StateAdmissionStopped, CauseProofsTotal},
		{"emitted just below B-2rc", func(e *gtEnv) { e.g.ObserveTotals(totals(1, math.Nextafter(budgetStop, 0)), rc) }, StateOpen, ""},
		{"emitted_H >= B-2rc", func(e *gtEnv) { e.g.ObserveTotals(totals(1, budgetStop), rc) }, StateAdmissionStopped, CauseBudget},
		{"emitted NaN", func(e *gtEnv) { e.g.ObserveTotals(totals(1, math.NaN()), rc) }, StateAdmissionStopped, CauseBudget},
		{"emitted negative", func(e *gtEnv) { e.g.ObserveTotals(totals(1, -1), rc) }, StateAdmissionStopped, CauseBudget},
		{"rewardCell zero", func(e *gtEnv) { e.g.ObserveTotals(totals(1, 0), 0) }, StateAdmissionStopped, CauseBudget},
		{"totals of another config", func(e *gtEnv) {
			e.g.ObserveTotals(Totals{ConfigSHA256: sha256.Sum256([]byte("other"))}, rc)
		}, StateAdmissionStopped, CauseBudget},
		{"expiry", func(e *gtEnv) {
			e.tick(time.Duration(cfg.ExpiresUnix-e.clock.Now().Unix()) * time.Second)
			e.g.Admit()
		}, StateAdmissionStopped, CauseExpired},
		// FREEZE: no local durable seal for NoSealTimeout (stall of the producer).
		{"59 s without a seal", func(e *gtEnv) { e.clock.Advance(NoSealTimeout - time.Second) }, StateOpen, ""},
		{"60 s without a seal", func(e *gtEnv) { e.clock.Advance(NoSealTimeout) }, StateFrozen, CauseNoSeal},
		{"non-local seals do not reset the clock", func(e *gtEnv) {
			for i := 0; i < 6; i++ {
				e.clock.Advance(10 * time.Second)
				e.g.ObserveSeal(uint64(i), false)
			}
		}, StateFrozen, CauseNoSeal},
		{"no-seal seen by Admit", func(e *gtEnv) {
			e.clock.Advance(NoSealTimeout)
			if RejectKindOf(e.g.Admit()) != KindAdmissionClosed {
				t.Error("Admit after the no-seal trip")
			}
		}, StateFrozen, CauseNoSeal},
	}
	// FREEZE: every cause the Ledger, Store, driver and reconciliation trip.
	for _, c := range []string{
		CauseDBOpen, CauseDBMissing, CauseReconcile, CausePreArm, CauseAcceptIO, CauseEnqueue,
		CausePreSeal, CauseInvariant + ":I5", CauseTxFamily, CauseMarkPaidIO, CauseNonLocalBlock,
		CauseLedgerUninit, CauseUnknownSafeError, CauseStall,
	} {
		c := c
		cases = append(cases, tc{"freeze " + c, func(e *gtEnv) { e.g.Freeze(c) }, StateFrozen, c})
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := gtDir(t)
			e := gtNew(t, dir, cfg)
			e.open(t)
			c.drive(e)
			if s := e.g.State(); s != c.want {
				t.Fatalf("state %v, want %v", s, c.want)
			}
			if got := e.g.AdmissionOpen(); got != (c.want == StateOpen) {
				t.Errorf("AdmissionOpen = %v", got)
			}
			if len(e.failStops()) != 0 {
				t.Errorf("fail-stops: %v", e.failStops())
			}
			marker := map[State]string{StateFrozen: MarkerTripped, StateAdmissionStopped: MarkerAdmissionStopped}[c.want]
			for _, m := range []string{MarkerTripped, MarkerAdmissionStopped} {
				if tripped := gtExists(t, filepath.Join(dir, m+TrippedSuffix)); tripped != (m == marker) {
					t.Errorf("%s tripped = %v", m, tripped)
				}
			}
			if marker == "" {
				if evs := e.store.snapshot(); len(evs) != 0 {
					t.Errorf("events = %+v", evs)
				}
				return
			}
			if got := gtReadCause(t, dir, marker); !strings.HasPrefix(got, c.cause) {
				t.Errorf("cause %q, want prefix %q", got, c.cause)
			}
			if evs := e.store.snapshot(); len(evs) != 1 || !strings.HasPrefix(evs[0].Detail, c.cause) {
				t.Errorf("events = %+v", evs)
			}
		})
	}
}

// The no-seal clock starts at Activate, and FROZEN/KILLED pause payouts (and
// so the Ledger's stall counter, which counts only while PayoutsEnabled).
func TestNoSealClockAndStallPause(t *testing.T) {
	e := gtNew(t, gtDir(t), gtConfig())
	if err := e.g.PreArm(); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(10 * NoSealTimeout)
	if s := e.g.State(); s != StateOpen {
		t.Fatalf("before Activate: %v", s)
	}
	e.g.Activate(true)
	e.tick(10 * NoSealTimeout)
	if s := e.g.State(); s != StateOpen || !s.PayoutsEnabled() {
		t.Fatalf("sealing every 10 s: %v", s)
	}
	e.g.Freeze(CauseStall)
	if s := e.g.State(); s.PayoutsEnabled() {
		t.Fatalf("FROZEN pays: %v", s)
	}
	e.clock.Advance(10 * NoSealTimeout)
	if s := e.g.State(); s != StateFrozen {
		t.Fatalf("state %v", s)
	}
	if c := gtReadCause(t, e.dir, MarkerTripped); c != CauseStall {
		t.Errorf("first cause replaced: %q", c)
	}
}

func TestHMACReplayTextPinned(t *testing.T) {
	raw, flat, mismatch := gtHMACReplay(t)
	if !strings.HasPrefix(raw.Error(), hmacNonceReplay) {
		t.Fatalf("HMAC replay error %q no longer starts with %q", raw, hmacNonceReplay)
	}
	if !countsAsDuplicate(raw) || !countsAsDuplicate(flat) || countsAsDuplicate(mismatch) {
		t.Fatalf("countsAsDuplicate raw=%v flat=%v mismatch=%v", countsAsDuplicate(raw), countsAsDuplicate(flat), countsAsDuplicate(mismatch))
	}
}

// -----------------------------------------------------------------------------
// Concurrency (run with -race)
// -----------------------------------------------------------------------------

func TestGuardConcurrency(t *testing.T) {
	dir := gtDir(t)
	e := gtNew(t, dir, gtConfig())
	e.open(t)
	const n = 200
	var wg sync.WaitGroup
	var taken atomic.Int32
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if e.g.TakeRate() == nil {
				taken.Add(1)
			}
			_ = e.g.Admit()
			_ = e.g.AdmissionOpen()
			e.g.ObserveRejection(&Rejection{Kind: KindDuplicate})
			_ = e.g.State()
			if i%10 == 0 {
				e.g.Freeze(fmt.Sprintf("%s:%d", CauseInvariant, i))
			}
			_ = e.g.Config()
		}(i)
	}
	close(start)
	wg.Wait()
	if int(taken.Load()) != gtConfig().MaxProofsPerMin {
		t.Errorf("TakeRate granted %d, cap %d", taken.Load(), gtConfig().MaxProofsPerMin)
	}
	if e.g.State() != StateFrozen || !e.g.stopped.Load() {
		t.Fatalf("state %v stopped=%v", e.g.State(), e.g.stopped.Load())
	}
	evs := e.store.snapshot()
	kinds := map[string]int{}
	for _, ev := range evs {
		kinds[ev.Kind]++
	}
	if kinds["freeze"] != 1 || kinds["admission-stop"] != 1 || len(evs) != 2 {
		t.Errorf("events = %+v", evs)
	}
	e.g.mu.Lock()
	first := e.g.frozenCause
	e.g.mu.Unlock()
	if c := gtReadCause(t, dir, MarkerTripped); c != first || !strings.HasPrefix(c, CauseInvariant) {
		t.Errorf("cause file %q, first cause %q", c, first)
	}
	if len(gtTemps(t, dir)) != 0 || len(e.failStops()) != 0 {
		t.Errorf("temps %v fail-stops %v", gtTemps(t, dir), e.failStops())
	}
}
