// Command genesis-ceremony is a pure-Go DRY-RUN of the QSDM mainnet
// genesis ceremony. It models an N-of-N commit-reveal randomness
// beacon whose output becomes the `genesis_seed` field of the first
// block, plus a per-participant signed attestation bundle that each
// ceremony participant publishes so any third party can independently
// reconstruct and verify the final genesis artefact.
//
// This binary is EXPLICITLY NOT the production ceremony driver. It
// exists so:
//
//  1. Validator operators can walk through the expected data flow
//     end-to-end before the real ceremony.
//  2. External monitors can be built and tested against a realistic
//     JSON artefact shape before mainnet.
//  3. `tok-01` counsel review has a concrete artefact to review (what
//     gets published, what is kept private, what is verifiable).
//
// Cryptographic shortcuts taken in this dry-run (called out so they
// cannot accidentally ship to production):
//
//   - Participant signing keys are ed25519. The production ceremony
//     uses ML-DSA-87 (NIST FIPS 204) via liboqs, matching every other
//     validator signing surface. We do not import liboqs here because
//     this binary must build on any developer laptop without CGO.
//   - The commit-reveal is single-round; production adds a timeout
//     and a timelock-encrypted reveal phase (VDF or delay-encryption)
//     so a late-breaking participant cannot cancel the ceremony.
//   - Participant identities are supplied via flags / a JSON config,
//     not derived from on-chain validator registration as the real
//     ceremony will.
//
// Every JSON artefact this binary emits has `"dry_run": true` at top
// level so downstream tooling cannot confuse it with a real bundle.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

// -----------------------------------------------------------------------
// Wire shapes
// -----------------------------------------------------------------------

// Params captures the CELL_TOKENOMICS.md constants the ceremony is
// pinning. We include them in the bundle so an auditor can verify the
// ceremony was run against the *intended* tokenomics — not a tampered
// local copy.
type Params struct {
	TotalSupplyCell       uint64 `json:"total_supply_cell"`
	TreasuryAllocationCell uint64 `json:"treasury_allocation_cell"`
	MiningEmissionCell    uint64 `json:"mining_emission_cell"`
	CoinDecimals          uint8  `json:"coin_decimals"`
	SmallestUnitName      string `json:"smallest_unit_name"`
	TargetBlockTimeSecs   uint32 `json:"target_block_time_secs"`
	HalvingEveryYears     uint8  `json:"halving_every_years"`
}

// DefaultParams encodes the values ratified per Phase 0 recommendation
// in Major Update.md §11.1 and pinned in CELL_TOKENOMICS.md /
// REBRAND_NOTES.md §4. Any change requires the "mining-audit-rerun-
// required" label per AUDIT_PACKET_MINING.md §5.
func DefaultParams() Params {
	return Params{
		TotalSupplyCell:        100_000_000,
		TreasuryAllocationCell: 10_000_000,
		MiningEmissionCell:     90_000_000,
		CoinDecimals:           8,
		SmallestUnitName:       "dust",
		TargetBlockTimeSecs:    10,
		HalvingEveryYears:      4,
	}
}

// Participant is one contributor to the ceremony. In production the
// pubkey is an ML-DSA-87 validator key registered on-chain; here we
// use ed25519 for a dry-run that needs zero CGO.
type Participant struct {
	ID        string            `json:"id"`
	PubKeyHex string            `json:"pubkey_hex"`
	Commit    string            `json:"commit_hex"`
	Reveal    string            `json:"reveal_hex,omitempty"`
	Signature string            `json:"signature_hex"`
	Metadata  map[string]string `json:"metadata,omitempty"`

	// not serialised — present only for the in-memory simulation
	privateKey ed25519.PrivateKey `json:"-"`
	revealByte []byte             `json:"-"`
}

// Bundle is the full ceremony artefact. Published once by every
// participant, and by the foundation as an anchoring publication.
// Third-party verifiers (including cmd/trustcheck analogues to be
// built post-launch) read this bundle and the genesis block, and
// confirm hash(bundle) matches genesis_bundle_hash in block 0.
type Bundle struct {
	DryRun         bool          `json:"dry_run"`
	SchemaVersion  uint32        `json:"schema_version"`
	CeremonyID     string        `json:"ceremony_id"`
	StartedAt      string        `json:"started_at"`
	FinishedAt     string        `json:"finished_at"`
	Network        string        `json:"network"`
	Params         Params        `json:"params"`
	Participants   []Participant `json:"participants"`
	CommitRoot     string        `json:"commit_root_hex"`
	RevealConcat   string        `json:"reveal_concat_hex"`
	GenesisSeed    string        `json:"genesis_seed_hex"`
	GenesisHash    string        `json:"genesis_hash_hex"`
	TreasuryAddr   string        `json:"treasury_address"`
	BundleHash     string        `json:"bundle_hash_hex"`
	ProducedBy     string        `json:"produced_by"`
	Notes          []string      `json:"notes,omitempty"`
}

const bundleSchemaVersion uint32 = 1

// -----------------------------------------------------------------------
// main
// -----------------------------------------------------------------------

func main() {
	var (
		mode         = flag.String("mode", "run", "One of: run, verify, schema.")
		participants = flag.Int("participants", 5, "Number of simulated ceremony participants (run mode only).")
		network      = flag.String("network", "qsdm-dryrun-local", "Network identifier embedded in the bundle.")
		treasury     = flag.String("treasury-addr", "qsdm-treasury-DRYRUN-0000000000000000000000", "Treasury address the bundle commits to.")
		outPath      = flag.String("out", "", "File to write the bundle JSON to (default: stdout).")
		inPath       = flag.String("in", "", "Bundle JSON to read (verify / schema mode).")
	)
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintln(out, "genesis-ceremony — QSDM mainnet genesis ceremony DRY-RUN.")
		fmt.Fprintln(out)
		fmt.Fprintln(out, "This binary does not produce real genesis allocations. See the")
		fmt.Fprintln(out, "package comment at cmd/genesis-ceremony/main.go for the list of")
		fmt.Fprintln(out, "cryptographic shortcuts taken.")
		fmt.Fprintln(out)
		fmt.Fprintf(out, "Usage: %s [flags]\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	switch *mode {
	case "run":
		if *participants < 2 {
			fmt.Fprintln(os.Stderr, "genesis-ceremony: --participants must be >= 2")
			os.Exit(2)
		}
		b, err := RunCeremony(*participants, *network, *treasury, DefaultParams())
		if err != nil {
			fmt.Fprintf(os.Stderr, "genesis-ceremony: run failed: %v\n", err)
			os.Exit(1)
		}
		if err := writeBundle(b, *outPath); err != nil {
			fmt.Fprintf(os.Stderr, "genesis-ceremony: write failed: %v\n", err)
			os.Exit(1)
		}
	case "verify":
		b, err := readBundle(*inPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "genesis-ceremony: read failed: %v\n", err)
			os.Exit(1)
		}
		if err := VerifyBundle(b); err != nil {
			fmt.Fprintf(os.Stderr, "genesis-ceremony: verify FAILED: %v\n", err)
			os.Exit(2)
		}
		fmt.Printf("genesis-ceremony: verify OK (ceremony_id=%s, %d participants, seed=%s…)\n",
			b.CeremonyID, len(b.Participants), b.GenesisSeed[:16])
	case "schema":
		// Emit a JSON-schema-ish description for tooling to lean on.
		skel := Bundle{DryRun: true, SchemaVersion: bundleSchemaVersion, Params: DefaultParams()}
		_ = json.NewEncoder(os.Stdout).Encode(skel)
	default:
		fmt.Fprintf(os.Stderr, "genesis-ceremony: unknown --mode %q\n", *mode)
		os.Exit(2)
	}
}

// -----------------------------------------------------------------------
// ceremony
// -----------------------------------------------------------------------

// RunCeremony simulates N participants, each of whom:
//  1. Generates an ed25519 keypair.
//  2. Draws a 32-byte reveal and commits to sha3-256(reveal).
//  3. Reveals their value after all commits are in.
//  4. Signs the complete post-reveal bundle with their private key.
//
// The genesis seed is sha3-256 over the concatenation of reveals in
// participant-ID order (not submission order — that is what makes the
// output bit-stable across re-runs with the same participant set).
// The genesis hash commits the seed, the params, the treasury address,
// and the network identifier.
func RunCeremony(n int, network, treasuryAddr string, params Params) (*Bundle, error) {
	if n < 2 {
		return nil, fmt.Errorf("ceremony requires at least 2 participants, got %d", n)
	}
	if params.TotalSupplyCell != params.TreasuryAllocationCell+params.MiningEmissionCell {
		return nil, fmt.Errorf("params invariant: total %d != treasury %d + mining %d",
			params.TotalSupplyCell, params.TreasuryAllocationCell, params.MiningEmissionCell)
	}

	startedAt := time.Now().UTC()
	parts := make([]Participant, n)
	for i := 0; i < n; i++ {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("generate participant %d key: %w", i, err)
		}
		reveal := make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, reveal); err != nil {
			return nil, fmt.Errorf("generate participant %d reveal: %w", i, err)
		}
		h := sha3.Sum256(reveal)
		parts[i] = Participant{
			ID:         fmt.Sprintf("participant-%02d", i+1),
			PubKeyHex:  hex.EncodeToString(pub),
			Commit:     hex.EncodeToString(h[:]),
			Reveal:     hex.EncodeToString(reveal),
			privateKey: priv,
			revealByte: reveal,
		}
	}

	// Sort by ID so the commit root and reveal concatenation are
	// deterministic across independent reproducers even though the
	// map iteration order at runtime is not stable.
	sort.Slice(parts, func(i, j int) bool { return parts[i].ID < parts[j].ID })

	commitRoot := hashBundle(func(h io.Writer) {
		for _, p := range parts {
			fmt.Fprintf(h, "%s:%s\n", p.ID, p.Commit)
		}
	})

	revealConcat := make([]byte, 0, len(parts)*32)
	for _, p := range parts {
		revealConcat = append(revealConcat, p.revealByte...)
	}
	seed := sha3.Sum256(revealConcat)

	ceremonyID := hashBundle(func(h io.Writer) {
		fmt.Fprintf(h, "QSDM-genesis-dryrun/%s/%s/%d\n", network, treasuryAddr, startedAt.UnixNano())
	})

	b := &Bundle{
		DryRun:        true,
		SchemaVersion: bundleSchemaVersion,
		CeremonyID:    ceremonyID,
		StartedAt:     startedAt.Format(time.RFC3339Nano),
		Network:       network,
		Params:        params,
		Participants:  parts,
		CommitRoot:    commitRoot,
		RevealConcat:  hex.EncodeToString(revealConcat),
		GenesisSeed:   hex.EncodeToString(seed[:]),
		TreasuryAddr:  treasuryAddr,
		ProducedBy:    "cmd/genesis-ceremony (dry-run)",
		Notes: []string{
			"DRY-RUN: ed25519 stands in for ML-DSA-87 production keys.",
			"DRY-RUN: single-round commit-reveal — production adds timelock.",
			"DRY-RUN: participant set is synthetic, not the on-chain validator set.",
			"See docs/docs/AUDIT_PACKET_MINING.md §6 for reproducible-build recipe.",
		},
	}

	// The genesis hash commits to every stable field except per-
	// participant signatures and the FinishedAt timestamp (which is
	// written after signatures are collected). This lets each
	// participant compute the hash independently and sign it.
	b.GenesisHash = hashBundle(func(h io.Writer) {
		fmt.Fprintf(h, "ceremony_id=%s\n", b.CeremonyID)
		fmt.Fprintf(h, "network=%s\n", b.Network)
		fmt.Fprintf(h, "treasury_addr=%s\n", b.TreasuryAddr)
		fmt.Fprintf(h, "commit_root=%s\n", b.CommitRoot)
		fmt.Fprintf(h, "genesis_seed=%s\n", b.GenesisSeed)
		pj, _ := json.Marshal(b.Params)
		fmt.Fprintf(h, "params=%s\n", pj)
	})

	// Sign the genesis hash with each participant's ed25519 key and
	// stash the signature back into the slice.
	msg, err := hex.DecodeString(b.GenesisHash)
	if err != nil {
		return nil, fmt.Errorf("decode genesis hash: %w", err)
	}
	for i := range b.Participants {
		sig := ed25519.Sign(b.Participants[i].privateKey, msg)
		b.Participants[i].Signature = hex.EncodeToString(sig)
	}

	b.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	b.BundleHash = hashBundle(func(h io.Writer) {
		fmt.Fprintf(h, "genesis_hash=%s\n", b.GenesisHash)
		for _, p := range b.Participants {
			fmt.Fprintf(h, "sig:%s=%s\n", p.ID, p.Signature)
		}
		fmt.Fprintf(h, "finished_at=%s\n", b.FinishedAt)
	})

	// Scrub the in-memory private keys before returning — they must
	// never leave the generating process. In the real ceremony each
	// participant runs this binary on air-gapped hardware and the
	// private key is shredded immediately; the dry-run emulates that
	// by overwriting.
	for i := range b.Participants {
		b.Participants[i].privateKey = nil
		b.Participants[i].revealByte = nil
	}

	return b, nil
}

// VerifyBundle re-runs every invariant the bundle commits to. Any
// third-party verifier (foundation, auditor, journalist) can call this
// against a published bundle to confirm consistency.
func VerifyBundle(b *Bundle) error {
	if b == nil {
		return errors.New("nil bundle")
	}
	if b.SchemaVersion != bundleSchemaVersion {
		return fmt.Errorf("unsupported schema version %d (expected %d)", b.SchemaVersion, bundleSchemaVersion)
	}
	if !b.DryRun {
		// We refuse to "verify" anything not marked as a dry-run so
		// nobody accidentally uses this tool to bless a mainnet
		// bundle. The production verifier is a different binary.
		return errors.New("bundle missing dry_run=true; this tool refuses to verify non-dry-run bundles")
	}
	if len(b.Participants) < 2 {
		return fmt.Errorf("bundle has %d participants, ceremony requires >= 2", len(b.Participants))
	}
	if b.Params.TotalSupplyCell != b.Params.TreasuryAllocationCell+b.Params.MiningEmissionCell {
		return errors.New("tokenomics invariant: total != treasury + mining")
	}

	// Participants sorted.
	for i := 1; i < len(b.Participants); i++ {
		if strings.Compare(b.Participants[i-1].ID, b.Participants[i].ID) >= 0 {
			return fmt.Errorf("participants must be in ascending ID order; %q precedes %q",
				b.Participants[i-1].ID, b.Participants[i].ID)
		}
	}

	// Recompute commit root.
	wantCommitRoot := hashBundle(func(h io.Writer) {
		for _, p := range b.Participants {
			fmt.Fprintf(h, "%s:%s\n", p.ID, p.Commit)
		}
	})
	if wantCommitRoot != b.CommitRoot {
		return fmt.Errorf("commit root mismatch: have %s, recomputed %s", b.CommitRoot, wantCommitRoot)
	}

	// Recompute reveal concat and check each reveal opens its commit.
	revealConcat := make([]byte, 0, len(b.Participants)*32)
	for _, p := range b.Participants {
		revealBytes, err := hex.DecodeString(p.Reveal)
		if err != nil {
			return fmt.Errorf("participant %q: decode reveal: %w", p.ID, err)
		}
		if len(revealBytes) != 32 {
			return fmt.Errorf("participant %q: reveal must be 32 bytes, got %d", p.ID, len(revealBytes))
		}
		h := sha3.Sum256(revealBytes)
		if hex.EncodeToString(h[:]) != p.Commit {
			return fmt.Errorf("participant %q: reveal does not open the commit", p.ID)
		}
		revealConcat = append(revealConcat, revealBytes...)
	}
	if hex.EncodeToString(revealConcat) != b.RevealConcat {
		return errors.New("reveal concatenation does not match stored value")
	}
	seed := sha3.Sum256(revealConcat)
	if hex.EncodeToString(seed[:]) != b.GenesisSeed {
		return errors.New("genesis seed mismatch")
	}

	// Recompute genesis hash.
	wantGenesis := hashBundle(func(h io.Writer) {
		fmt.Fprintf(h, "ceremony_id=%s\n", b.CeremonyID)
		fmt.Fprintf(h, "network=%s\n", b.Network)
		fmt.Fprintf(h, "treasury_addr=%s\n", b.TreasuryAddr)
		fmt.Fprintf(h, "commit_root=%s\n", b.CommitRoot)
		fmt.Fprintf(h, "genesis_seed=%s\n", b.GenesisSeed)
		pj, _ := json.Marshal(b.Params)
		fmt.Fprintf(h, "params=%s\n", pj)
	})
	if wantGenesis != b.GenesisHash {
		return fmt.Errorf("genesis hash mismatch: have %s, recomputed %s", b.GenesisHash, wantGenesis)
	}

	// Verify every signature.
	msg, err := hex.DecodeString(b.GenesisHash)
	if err != nil {
		return fmt.Errorf("decode genesis hash: %w", err)
	}
	for _, p := range b.Participants {
		pub, err := hex.DecodeString(p.PubKeyHex)
		if err != nil {
			return fmt.Errorf("participant %q: decode pubkey: %w", p.ID, err)
		}
		if len(pub) != ed25519.PublicKeySize {
			return fmt.Errorf("participant %q: pubkey must be %d bytes, got %d", p.ID, ed25519.PublicKeySize, len(pub))
		}
		sig, err := hex.DecodeString(p.Signature)
		if err != nil {
			return fmt.Errorf("participant %q: decode sig: %w", p.ID, err)
		}
		if !ed25519.Verify(ed25519.PublicKey(pub), msg, sig) {
			return fmt.Errorf("participant %q: signature verify failed", p.ID)
		}
	}

	// Recompute bundle hash.
	wantBundle := hashBundle(func(h io.Writer) {
		fmt.Fprintf(h, "genesis_hash=%s\n", b.GenesisHash)
		for _, p := range b.Participants {
			fmt.Fprintf(h, "sig:%s=%s\n", p.ID, p.Signature)
		}
		fmt.Fprintf(h, "finished_at=%s\n", b.FinishedAt)
	})
	if wantBundle != b.BundleHash {
		return fmt.Errorf("bundle hash mismatch: have %s, recomputed %s", b.BundleHash, wantBundle)
	}

	return nil
}

// -----------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------

func hashBundle(writeFn func(io.Writer)) string {
	h := sha3.New256()
	writeFn(h)
	return hex.EncodeToString(h.Sum(nil))
}

func writeBundle(b *Bundle, path string) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if path == "" {
		_, err := os.Stdout.Write(append(data, '\n'))
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func readBundle(path string) (*Bundle, error) {
	var r io.Reader = os.Stdin
	if path != "" {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		r = f
	}
	var b Bundle
	if err := json.NewDecoder(r).Decode(&b); err != nil {
		return nil, err
	}
	return &b, nil
}
