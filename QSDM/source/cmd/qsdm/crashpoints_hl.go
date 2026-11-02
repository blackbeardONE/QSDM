//go:build hl_crashpoints

package main

// crashpoints_hl.go (HL1 WP9, build tag hl_crashpoints only): crash and fault
// injection for the §7 crash-injection runs. It is never compiled into a
// production build; crashpoints_off.go provides the no-op versions.
//
//	QSDM_HL1_CRASH=<point>[@<n>][,...]   self-SIGKILL at the n-th hit (default 1)
//	QSDM_HL1_FAULT=<point>:<errno>[,...] the point fails with errno (for
//	                                     example ENOSPC, EIO or a number)
//	QSDM_HL1_FSTRACE=1                   log every hl1OSFS SyncFile, SyncDir
//	                                     and WriteFileDurable ("hl1 fstrace:
//	                                     <op> <base name>"), in call order
//
// Points are named in the code that calls hl1Crashpoint, hl1Fault and
// hl1CrashInsideD1, for example persist:H3, persist:after-H4 and
// persist:H7:d1, and the boot step S4r's boot:S4r:append, boot:S4r:fsync
// (faults) and boot:S4r:after-append (crash).

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
)

const hl1CrashpointsEnabled = true

type hl1CrashSpec struct {
	at   uint64
	hits atomic.Uint64
}

var (
	hl1CrashOnce   sync.Once
	hl1CrashPoints map[string]*hl1CrashSpec
	hl1FaultPoints map[string]syscall.Errno
	hl1FSTraceOn   bool
)

var hl1ErrnoNames = map[string]syscall.Errno{
	"ENOSPC": syscall.ENOSPC,
	"EIO":    syscall.EIO,
	"EROFS":  syscall.EROFS,
	"EACCES": syscall.EACCES,
	"EPERM":  syscall.EPERM,
	"ENOENT": syscall.ENOENT,
	"EEXIST": syscall.EEXIST,
	"EDQUOT": syscall.EDQUOT,
}

func hl1LoadCrashpoints() {
	hl1CrashOnce.Do(func() {
		hl1CrashPoints = map[string]*hl1CrashSpec{}
		hl1FaultPoints = map[string]syscall.Errno{}
		hl1FSTraceOn = os.Getenv("QSDM_HL1_FSTRACE") == "1"
		for _, item := range strings.Split(os.Getenv("QSDM_HL1_CRASH"), ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			spec := &hl1CrashSpec{at: 1}
			if name, n, ok := strings.Cut(item, "@"); ok {
				v, err := strconv.ParseUint(n, 10, 64)
				if err != nil || v == 0 {
					log.Printf("hl1 crashpoints: ignoring %q: bad hit count", item)
					continue
				}
				item, spec.at = name, v
			}
			hl1CrashPoints[item] = spec
		}
		for _, item := range strings.Split(os.Getenv("QSDM_HL1_FAULT"), ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			// Point names contain ':' themselves; the errno follows the last one.
			i := strings.LastIndex(item, ":")
			if i <= 0 || i == len(item)-1 {
				log.Printf("hl1 crashpoints: ignoring fault %q: want <point>:<errno>", item)
				continue
			}
			name, errno := item[:i], item[i+1:]
			e, known := hl1ErrnoNames[strings.ToUpper(errno)]
			if !known {
				v, err := strconv.ParseUint(errno, 10, 32)
				if err != nil {
					log.Printf("hl1 crashpoints: ignoring fault %q: unknown errno", item)
					continue
				}
				e = syscall.Errno(v)
			}
			hl1FaultPoints[name] = e
		}
		if len(hl1CrashPoints)+len(hl1FaultPoints) > 0 || hl1FSTraceOn {
			log.Printf("hl1 crashpoints: ACTIVE (test build): crash=%v fault=%v fstrace=%v", keysOf(hl1CrashPoints), hl1FaultPoints, hl1FSTraceOn)
		}
	})
}

// hl1ResetCrashpoints makes the next call re-read the environment (tests).
func hl1ResetCrashpoints() { hl1CrashOnce = sync.Once{} }

func keysOf(m map[string]*hl1CrashSpec) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// hl1Crashpoint SIGKILLs the process when point is armed and this is its
// configured hit.
func hl1Crashpoint(point string) {
	hl1LoadCrashpoints()
	spec := hl1CrashPoints[point]
	if spec == nil || spec.hits.Add(1) != spec.at {
		return
	}
	hl1Kill(point)
}

// hl1Fault returns the injected errno for point, or nil.
func hl1Fault(point string) error {
	hl1LoadCrashpoints()
	if e, ok := hl1FaultPoints[point]; ok {
		return fmt.Errorf("hl1 injected fault at %s: %w", point, e)
	}
	return nil
}

// hl1FSTrace logs a durability operation of hl1OSFS when QSDM_HL1_FSTRACE=1,
// so a process-level test can assert the fsync order from the child's output.
func hl1FSTrace(op, name string) {
	hl1LoadCrashpoints()
	if hl1FSTraceOn {
		log.Printf("hl1 fstrace: %s %s", op, filepath.Base(name))
	}
}

// hl1CrashInsideD1 emulates a SIGKILL inside a D1 write of dir/name (§4.6 C9):
// when point is armed it leaves a partially written, fsynced temp file with
// the D1 name pattern and kills the process before the rename.
func hl1CrashInsideD1(dir, name, point string) {
	hl1LoadCrashpoints()
	spec := hl1CrashPoints[point]
	if spec == nil || spec.hits.Add(1) != spec.at {
		return
	}
	var rnd [8]byte
	_, _ = rand.Read(rnd[:])
	tmp := filepath.Join(dir, fmt.Sprintf("%s%s-%d-%s", legacymining.TempPrefix, name, os.Getpid(), hex.EncodeToString(rnd[:])))
	if f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600); err == nil {
		_, _ = f.Write([]byte(`{"version":1,"hei`))
		_ = f.Sync()
		_ = f.Close()
	}
	hl1Kill(point)
}

func hl1Kill(point string) {
	log.Printf("hl1 crashpoints: SIGKILL at %s", point)
	if p, err := os.FindProcess(os.Getpid()); err == nil {
		_ = p.Kill()
	}
	select {} // the kill is asynchronous; never continue past a crash point
}

// hl1CrashStore adds the submit crash points A1 and A2 and the A3 fault around
// Store.Accept (§4.6).
type hl1CrashStore struct{ legacymining.Store }

func (s hl1CrashStore) Accept(rec legacymining.Record) error {
	hl1Crashpoint("submit:before-accept") // A1: no row, no 200
	if err := hl1Fault("submit:accept"); err != nil {
		return err // A3: a non-UNIQUE error trips FREEZE
	}
	err := s.Store.Accept(rec)
	if err == nil {
		hl1Crashpoint("submit:after-accept") // A2: row committed, no 200
	}
	return err
}

// hl1CrashpointStore wraps the store handed to the mining service.
func hl1CrashpointStore(s legacymining.Store) legacymining.Store { return hl1CrashStore{s} }
