package networking

// hostkey.go: persisting the libp2p host PrivateKey across qsdm.service
// restarts.
//
// Background. By default `libp2p.New(...)` generates a fresh Ed25519
// keypair on every call. The peer.ID is the multihash of that key, so
// without a persistence layer the libp2p node_id changes every time the
// validator restarts. On 2026-05-11 that bit us in production: the
// post-v0.3.2 deploy rolled node_id from `12D3KooWKWPUeH…` to
// `12D3KooWBY9zdQ…`, every pre-restart trust-attestation row aged out of
// the 15-minute freshness window, and the next `trustcheck-external`
// probe failed for ~8 minutes until the BLR1 + OCI sidecars next ticked.
// The blip was self-recovering but it shouldn't happen at all — the
// fix is one short file.
//
// Design.
//   - One config knob, `cfg.NetworkHostKeyPath`. Empty (default) =>
//     ephemeral key. Non-empty => load-or-create.
//   - File format: single line of base64(proto.Marshal(PrivKey)). The
//     proto layout is the one libp2p itself uses for wire-format
//     keys (crypto.MarshalPrivateKey), so an operator could in
//     principle pipe this file straight into any libp2p tool. The
//     base64 wrapper is purely to keep the on-disk file ASCII (so
//     it greps, cats, and copy-pastes cleanly) without adding PEM
//     headers that the proto loader would have to strip.
//   - File mode: 0600. The file is created atomically (write to
//     `<path>.tmp` then rename) so a crash mid-write can't leave a
//     half-written key on disk that the next restart would fail to
//     parse.
//   - We only support Ed25519 today. RSA and Secp256k1 are accepted
//     on the load path (the proto carries the key type), so an
//     operator who already has an RSA libp2p key from another tool
//     can drop it in and we will load it. Newly-generated keys are
//     always Ed25519 because (a) the on-the-wire libp2p peer ID is
//     bounded to 42 chars rather than the longer RSA forms, (b) it
//     is what go-libp2p has defaulted to since v0.18, and (c) ML-DSA
//     post-quantum signing is a separate layer entirely (ML-DSA is
//     the chain-payload signer; libp2p Ed25519 only authenticates
//     point-to-point peer identity inside an already-trusted bootstrap
//     allowlist).

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	libp2pcrypto "github.com/libp2p/go-libp2p/core/crypto"
)

// loadOrCreateHostKey loads the libp2p PrivateKey at `path`, generating
// a fresh Ed25519 keypair (and persisting it atomically) when the file
// is missing. An empty `path` returns (nil, nil) — callers treat this
// as "no persistence, let libp2p generate an ephemeral key".
//
// Returns an actionable error on:
//   - Non-empty path whose parent directory does not exist or is not
//     writable. We do NOT auto-create the parent, because that's a
//     surprising side-effect when an operator typoes the path; the
//     error includes the parent we tried.
//   - File that exists but is unreadable, empty, not valid base64, or
//     not a valid libp2p marshalled private key. The error message
//     names the path and the failure mode so an operator can grep
//     systemd journals and find it.
func loadOrCreateHostKey(path string) (libp2pcrypto.PrivKey, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}

	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("network host_key_path %q is a directory; expected a single-line base64 file", path)
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil, fmt.Errorf("read network host_key_path %q: %w", path, rerr)
		}
		// Strip any trailing whitespace (newline, CRLF, spaces) so a
		// human-edited file still parses.
		b64 := strings.TrimSpace(string(raw))
		if b64 == "" {
			return nil, fmt.Errorf("network host_key_path %q is empty; delete the file to regenerate or restore from backup", path)
		}
		bin, derr := base64.StdEncoding.DecodeString(b64)
		if derr != nil {
			return nil, fmt.Errorf("network host_key_path %q is not valid base64: %w", path, derr)
		}
		k, kerr := libp2pcrypto.UnmarshalPrivateKey(bin)
		if kerr != nil {
			return nil, fmt.Errorf("network host_key_path %q is not a valid libp2p private key: %w", path, kerr)
		}
		return k, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("stat network host_key_path %q: %w", path, err)
	}

	// File does not exist — create a new key. Parent directory must
	// already exist; we never mkdir behind the operator's back.
	parent := filepath.Dir(path)
	if pinfo, perr := os.Stat(parent); perr != nil {
		return nil, fmt.Errorf("network host_key_path parent directory %q does not exist (create it with appropriate permissions before starting the node): %w", parent, perr)
	} else if !pinfo.IsDir() {
		return nil, fmt.Errorf("network host_key_path parent %q is not a directory", parent)
	}

	priv, _, err := libp2pcrypto.GenerateKeyPair(libp2pcrypto.Ed25519, -1)
	if err != nil {
		return nil, fmt.Errorf("generate libp2p Ed25519 keypair: %w", err)
	}
	bin, err := libp2pcrypto.MarshalPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshal newly-generated libp2p private key: %w", err)
	}
	b64 := base64.StdEncoding.EncodeToString(bin)

	// Atomic write: tmp file in the same directory + rename. Single
	// trailing newline so the file is well-formed text.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(b64+"\n"), 0o600); err != nil {
		return nil, fmt.Errorf("write tmp host key %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Best-effort cleanup of the orphan tmp file; ignore
		// secondary errors because the primary one is what the
		// operator needs to see.
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("rename tmp host key %q -> %q: %w", tmp, path, err)
	}
	return priv, nil
}
