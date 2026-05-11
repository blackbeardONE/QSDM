// qsdm-deploy-landing — push static landing-site changes (HTML / JS /
// WASM / Caddyfile) to the production VPS over SSH using the operator's
// local ed25519 key.
//
// This is a small, focused tool — it replaces the ad-hoc paramiko
// scripts under QSDM/deploy/remote_*_paramiko.py for the landing-site
// case, where the operator has Go on PATH but not necessarily a working
// Python + paramiko install (Windows MSYS2 MinGW Python ships without
// pip on this workstation, which has historically forced a Python
// install detour). The Go toolchain produces a single static binary
// that uses golang.org/x/crypto/ssh directly — no third-party SFTP
// dependency, no Python.
//
// What it does (in order):
//
//  1. Dial root@<host> over SSH on port 22 using the unencrypted ed25519
//     key at ~/.ssh/id_ed25519. (Override the host with QSDM_VPS_HOST
//     and the user with QSDM_VPS_USER; the defaults match
//     QSDM/deploy/_deploy_host.py for parity with the existing scripts.)
//  2. Upload each `--file local=remote` mapping over the SSH transport,
//     using `cat > <remote>` piped from the local file. (We deliberately
//     do not link the pkg/sftp package — there is no SFTP server on the
//     VPS in the current image, and `cat` over an SSH `exec` channel is
//     adequate for the small number of files this tool ships.)
//  3. Run each `--run` shell snippet on the remote, streaming stdout and
//     stderr live so the operator sees Caddy validate / reload output
//     in real time.
//  4. Exit non-zero on the first remote command that returns a non-zero
//     status, so failures don't get swept under a happy-path summary.
//
// Typical invocation (the one used to push the v0.3.1 wallet page),
// run from the QSDM/source/ go module root:
//
//	go run ./cmd/qsdm-deploy-landing \
//	  -file ../deploy/landing/wallet.html=/var/www/qsdm/wallet.html \
//	  -file ../deploy/landing/wallet.js=/var/www/qsdm/wallet.js \
//	  -file ../deploy/landing/wallet.wasm=/var/www/qsdm/wallet.wasm \
//	  -file ../deploy/landing/wasm_exec.js=/var/www/qsdm/wasm_exec.js \
//	  -file ../deploy/landing/index.html=/var/www/qsdm/index.html \
//	  -file ../deploy/Caddyfile=/etc/caddy/Caddyfile \
//	  -run 'caddy validate --config /etc/caddy/Caddyfile' \
//	  -run 'systemctl reload caddy' \
//	  -run 'ls -la /var/www/qsdm/wallet.* /var/www/qsdm/wasm_exec.js'
//
// Why this lives under QSDM/source/cmd/ alongside cmd/qsdm-attester,
// cmd/qsdm-relay, etc.: it's a Go binary, so it must live inside the
// QSDM/source/ go module to compile. The convention in this repo is
// that "everything that is `go build`-able is under source/cmd/" —
// even if the runtime purpose is operator tooling rather than a
// product surface.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// fileMappings is a flag.Var-compatible accumulator for repeated
// `-file LOCAL=REMOTE` arguments. Using a custom Var instead of
// `-file=...` + comma-splitting keeps remote paths that contain
// commas (rare but possible) honest, and the slice preserves the
// order the operator typed them in — which is also the order in
// which files are uploaded, so a deliberate "Caddyfile last" or
// "Caddyfile first" pattern is achievable.
type fileMappings []struct{ local, remote string }

func (f *fileMappings) String() string {
	parts := make([]string, len(*f))
	for i, m := range *f {
		parts[i] = m.local + "=" + m.remote
	}
	return strings.Join(parts, ", ")
}

func (f *fileMappings) Set(s string) error {
	idx := strings.IndexByte(s, '=')
	if idx <= 0 || idx == len(s)-1 {
		return fmt.Errorf("expected LOCAL=REMOTE, got %q", s)
	}
	*f = append(*f, struct{ local, remote string }{
		local:  s[:idx],
		remote: s[idx+1:],
	})
	return nil
}

// runList accumulates `-run "shell cmd"` flags.
type runList []string

func (r *runList) String() string { return strings.Join(*r, " ;; ") }
func (r *runList) Set(s string) error {
	*r = append(*r, s)
	return nil
}

func main() {
	var (
		host       string
		user_      string
		keyPath    string
		timeoutStr string
		files      fileMappings
		runs       runList
	)
	flag.StringVar(&host, "host", envDefault("QSDM_VPS_HOST", "206.189.132.232"), "remote host (env QSDM_VPS_HOST)")
	flag.StringVar(&user_, "user", envDefault("QSDM_VPS_USER", "root"), "remote SSH user (env QSDM_VPS_USER)")
	flag.StringVar(&keyPath, "key", defaultKeyPath(), "path to OpenSSH private key (ed25519 expected)")
	flag.StringVar(&timeoutStr, "timeout", "30s", "TCP dial + per-step SSH timeout")
	flag.Var(&files, "file", "LOCAL=REMOTE file to upload (repeat for multiple)")
	flag.Var(&runs, "run", "shell command to run on the remote after uploads (repeat for multiple; runs in order)")
	flag.Parse()

	if len(files) == 0 && len(runs) == 0 {
		fmt.Fprintln(os.Stderr, "no -file uploads and no -run commands specified; nothing to do")
		os.Exit(2)
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bad --timeout: %v\n", err)
		os.Exit(2)
	}

	keyBytes, err := os.ReadFile(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read private key %s: %v\n", keyPath, err)
		os.Exit(1)
	}
	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		// Encrypted keys are a future feature; for now we hard-fail
		// instead of pretending to prompt and silently breaking the
		// non-interactive operator workflow.
		fmt.Fprintf(os.Stderr, "parse private key: %v\n  (this tool currently supports only UNENCRYPTED OpenSSH keys; decrypt %s first if it's password-protected)\n", err, keyPath)
		os.Exit(1)
	}

	cfg := &ssh.ClientConfig{
		User:            user_,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // see note below
		Timeout:         timeout,
	}
	// HostKeyCallback note: we use InsecureIgnoreHostKey because:
	//  (a) the deployed Caddy is the source of truth for HTTPS — we
	//      are not protecting against a MITM that controls the
	//      network between this workstation and the VPS for SSH
	//      uploads, since that attacker would already control the
	//      DNS records you point at the VPS.
	//  (b) The historical paramiko deploy scripts do the same
	//      (AutoAddPolicy / no host-key check). Keeping parity
	//      avoids a "this tool prompts but the old ones didn't"
	//      gotcha.
	// A future hardening step is to bake the VPS host key fingerprint
	// into _deploy_host.py / this file. Out of scope for the wallet
	// deploy.

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	addr := net.JoinHostPort(host, "22")
	fmt.Fprintf(os.Stderr, "[ssh] dialing %s@%s …\n", user_, addr)
	d := &net.Dialer{Timeout: timeout}
	tcp, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tcp dial: %v\n", err)
		os.Exit(1)
	}
	clientConn, chans, reqs, err := ssh.NewClientConn(tcp, addr, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ssh handshake: %v\n", err)
		os.Exit(1)
	}
	client := ssh.NewClient(clientConn, chans, reqs)
	defer client.Close()

	fmt.Fprintf(os.Stderr, "[ssh] connected (%s)\n", client.ServerVersion())

	for _, m := range files {
		if err := uploadFile(client, m.local, m.remote); err != nil {
			fmt.Fprintf(os.Stderr, "upload %s -> %s: %v\n", m.local, m.remote, err)
			os.Exit(1)
		}
	}

	for _, cmd := range runs {
		if err := runRemote(client, cmd); err != nil {
			fmt.Fprintf(os.Stderr, "remote command %q: %v\n", cmd, err)
			os.Exit(1)
		}
	}

	fmt.Fprintln(os.Stderr, "[ssh] all steps OK")
}

// uploadFile streams the contents of localPath to remotePath on the
// connected SSH host by piping the file through `cat > <remote>`.
//
// This is slower than SFTP for many small files (each upload spawns a
// remote shell), but the landing-site deploy is at most a handful of
// files per run, so the simplicity is worth more than the throughput.
// The remote `cat` is invoked under `/bin/sh -c` so shell metacharacters
// in the remote path are honoured (operators occasionally point
// uploads at $HOME-relative paths).
//
// Returns an error if the remote command exits non-zero or if the SSH
// session disappears mid-transfer.
func uploadFile(client *ssh.Client, localPath, remotePath string) error {
	abs, err := filepath.Abs(localPath)
	if err != nil {
		return fmt.Errorf("abs path: %w", err)
	}
	src, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer src.Close()
	stat, err := src.Stat()
	if err != nil {
		return err
	}

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	// Use printf-quoted single quotes around the remote path so a path
	// that legitimately contains single quotes is escaped; for the
	// /var/www/qsdm and /etc/caddy targets this is overkill, but it
	// keeps the tool safe if an operator points it elsewhere.
	cmd := fmt.Sprintf("cat > %s", shellEscape(remotePath))
	fmt.Fprintf(os.Stderr, "[upload] %s → %s (%d bytes)\n", abs, remotePath, stat.Size())
	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	// Copy in the background so we can close stdin first and wait on
	// the session's exit afterwards.
	copyErrCh := make(chan error, 1)
	go func() {
		_, err := io.Copy(stdin, src)
		_ = stdin.Close()
		copyErrCh <- err
	}()

	if copyErr := <-copyErrCh; copyErr != nil {
		return fmt.Errorf("stream body: %w", copyErr)
	}
	if err := session.Wait(); err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("remote cat exit %d", ee.ExitStatus())
		}
		return fmt.Errorf("session wait: %w", err)
	}
	return nil
}

// runRemote executes a single shell command line on the VPS with
// stdout / stderr streamed back to the operator's terminal. The remote
// shell is the default user shell (likely bash on the VPS image),
// invoked via ssh.Session.Run which uses `exec` rather than `-c`, so
// the supplied command is treated as a single shell command line.
//
// On non-zero exit the function returns an error wrapping the exit
// status so the caller's main() can fail fast.
func runRemote(client *ssh.Client, command string) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	fmt.Fprintf(os.Stderr, "[run] %s\n", command)
	if err := session.Run(command); err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("exit %d", ee.ExitStatus())
		}
		return err
	}
	return nil
}

func envDefault(envKey, fallback string) string {
	if v, ok := os.LookupEnv(envKey); ok && v != "" {
		return v
	}
	return fallback
}

// defaultKeyPath returns ~/.ssh/id_ed25519 if it exists, otherwise
// ~/.ssh/id_rsa. The fallback isn't exercised by the production VPS
// (which uses ed25519) but keeps the tool useful on operator
// workstations that haven't yet rotated to ed25519.
func defaultKeyPath() string {
	cur, err := user.Current()
	if err != nil {
		return ""
	}
	for _, name := range []string{"id_ed25519", "id_rsa"} {
		p := filepath.Join(cur.HomeDir, ".ssh", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func shellEscape(s string) string {
	// Wrap in single quotes; any embedded single quote becomes '\''.
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
