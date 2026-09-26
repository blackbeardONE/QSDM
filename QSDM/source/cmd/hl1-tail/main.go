// Command hl1-tail is the HL1 recovery tool (design rev 4 §4.7, Appendix A):
//
//	hl1-tail trim-fragment --file journal|receipts
//	hl1-tail trim --above <J-1>
//	hl1-tail watermark show|seed|retire
//
// Run it only after R-STOP, as qsdm-tech, with core.env sourced and the
// absolute release path:
//
//	runuser -u qsdm-tech -- sh -c 'umask 077; set -a; . /etc/qsdm-tech/core.env; set +a; cd /var/lib/qsdm-tech/core; exec /opt/qsdm-tech/releases/<release>/hl1-tail ...'
//
// The state directory is resolved exactly as core does, Dir(cfg.SQLitePath).
// Every mutating subcommand holds qsdm-validator.state.lock for its whole run
// and exits 3 without changing anything when the lock is busy. The logic is
// in internal/hl1tail.
package main

import (
	"os"
	"path/filepath"
	"time"

	"github.com/blackbeardONE/QSDM/internal/hl1tail"
	"github.com/blackbeardONE/QSDM/pkg/config"
)

func main() {
	os.Exit(hl1tail.Run(os.Args[1:], hl1tail.Env{
		StateDir: stateDir,
		Getenv:   os.Getenv,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
		Now:      time.Now,
	}))
}

// stateDir is Dir(cfg.SQLitePath) of the core configuration (CONFIG_FILE and
// the environment from core.env), as in cmd/qsdm main (dep:844).
func stateDir() (string, error) {
	cfg, err := config.LoadConfig()
	if err != nil {
		return "", err
	}
	return filepath.Dir(cfg.SQLitePath), nil
}
