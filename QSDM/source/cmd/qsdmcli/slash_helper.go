package main

// slash_helper.go — evidence-bundle assembly for v2 mining
// slash transactions.
//
// The chain-side slashing path (pkg/mining/slashing/{forgedattest,
// doublemining}) consumes opaque EvidenceBlob bytes whose
// per-kind format is JSON wrapping one or two canonical-JSON
// mining.Proof payloads. Operators historically had two ways to
// produce those bytes:
//
//   1. Hand-roll the JSON envelope themselves and hex-encode it.
//      This is a footgun: get the canonical proof bytes wrong
//      (for instance, using json.Marshal(proof) which silently
//      drops the four binary fields tagged json:"-") and the
//      slash either bounces at admit time or, worse, lands as a
//      no-offence rejection that consumes the slasher's tx fee
//      without draining the offender.
//
//   2. Write a Go program that imports
//      pkg/mining/slashing/{forgedattest,doublemining} and calls
//      their EncodeEvidence helpers. Doable but raises the bar
//      for a watcher-bot operator who just wants to ship a slash
//      and move on.
//
// `qsdmcli slash-helper` closes that gap. It owns exactly the
// EncodeEvidence calls the chain consumes, so the bytes it
// produces ARE the bytes consensus accepts. Three subcommands:
//
//   qsdmcli slash-helper forged-attestation [flags]
//       Build a forgedattest.Evidence from one proof file (or
//       stdin) and optional fault-class + memo. Default output
//       is the raw evidence bytes to stdout.
//
//   qsdmcli slash-helper double-mining [flags]
//       Build a doublemining.Evidence from two proof files. The
//       encoder canonicalises (proof_a, proof_b) order so two
//       independent slashers observing the same equivocation pair
//       produce byte-identical evidence — preserving the
//       chain-side per-fingerprint replay protection.
//
//   qsdmcli slash-helper inspect [flags]
//       Decode an existing evidence blob (produced by either of
//       the above, or by some external tool) and pretty-print
//       its contents. Useful for "did my watcher bot really
//       capture what I think it did?" sanity checks before
//       submission.
//
// All three subcommands are deliberately read-only — they write
// to stdout or a chosen file, never to the network. The flow an
// operator follows is:
//
//   1. qsdmcli slash-helper forged-attestation \
//        --proof=offending-proof.json \
//        --fault-class=hmac_mismatch \
//        --memo="caught by watcher #4" \
//        --out=evidence.bin
//   2. qsdmcli slash --sender=$WATCHER --node-id=$OFFENDER \
//        --evidence-kind=forged-attestation \
//        --evidence-file=evidence.bin --amount=1000000000
//
// Or, in one pipe:
//
//   qsdmcli slash-helper forged-attestation --proof=p.json | \
//     qsdmcli slash --sender=$W --node-id=$O \
//       --evidence-kind=forged-attestation --evidence-file=- \
//       --amount=1000000000
//
// The --print-cmd flag on the build subcommands prints the
// matching `qsdmcli slash` invocation to stderr so an operator
// can copy-paste it into a script after editing whatever they
// need to (sender, amount, fee).

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/doublemining"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/forgedattest"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/freshnesscheat"
)

// readProofFile loads a canonical mining.Proof from the named
// path. "-" means stdin so a slasher pipeline can stream a proof
// directly into slash-helper without a temp file.
//
// We accept the wire form the chain produced (mining.Proof
// canonical JSON). Decoding via mining.ParseProof recovers the
// four binary fields tagged json:"-" which a naive json.Unmarshal
// would drop — same trick forgedattest.DecodeEvidence uses.
func readProofFile(path string) (*mining.Proof, error) {
	if path == "" {
		return nil, errors.New("proof path must not be empty")
	}
	var raw []byte
	var err error
	if path == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("proof file %s is empty", path)
	}
	p, err := mining.ParseProof(raw)
	if err != nil {
		return nil, fmt.Errorf("parse proof from %s: %w", path, err)
	}
	return p, nil
}

// writeEvidence emits raw evidence bytes to stdout, the named
// path, or "-" (stdout). 0o600 perms on disk because the
// evidence carries a faulty proof someone in the network is
// about to lose stake over — accidental world-readability isn't
// a security flaw (the same bytes will live on chain) but
// keeping permissions tight matches operator expectations for
// "files I create as part of a slash workflow".
func writeEvidence(out string, blob []byte) error {
	if out == "" || out == "-" {
		_, err := os.Stdout.Write(blob)
		return err
	}
	return os.WriteFile(out, blob, 0o600)
}

// printSlashCmd writes a copy-pasteable `qsdmcli slash` snippet
// to stderr. We deliberately print to stderr (not stdout) so
// the raw evidence bytes piped through stdout are not corrupted
// by the human-readable hint.
//
// The snippet is guidance, not a recommendation: a real watcher
// bot fills in --sender / --nonce / --fee / --amount from its
// own policy, and the printed values are placeholder hints.
func printSlashCmd(kind slashing.EvidenceKind, nodeID, evidencePath string, defaultAmount uint64) {
	if evidencePath == "" || evidencePath == "-" {
		evidencePath = "<evidence-file>"
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "Next: submit the slash. Replace placeholders below.")
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "  qsdmcli slash \\\n")
	fmt.Fprintf(os.Stderr, "    --sender=<YOUR_ADDRESS> \\\n")
	fmt.Fprintf(os.Stderr, "    --node-id=%s \\\n", quoteIfEmpty(nodeID))
	fmt.Fprintf(os.Stderr, "    --evidence-kind=%s \\\n", kind)
	fmt.Fprintf(os.Stderr, "    --evidence-file=%s \\\n", evidencePath)
	fmt.Fprintf(os.Stderr, "    --amount=%d \\\n", defaultAmount)
	fmt.Fprintf(os.Stderr, "    --memo=\"reason: <free text>\"\n")
	fmt.Fprintln(os.Stderr)
}

func quoteIfEmpty(s string) string {
	if s == "" {
		return "<NODE_ID>"
	}
	return s
}

// -----------------------------------------------------------------------------
// slash-helper top-level dispatcher
// -----------------------------------------------------------------------------

// slashHelper dispatches to the per-kind subcommand. Routes by
// the first positional arg so the user-facing surface is
// `qsdmcli slash-helper <kind> [flags]`, mirroring how
// `qsdmcli enrollments` etc. consume their first non-flag arg.
//
// We resolve the subcommand BEFORE constructing a flag.FlagSet
// because the flag sets per kind differ — forged-attestation
// has --fault-class which makes no sense for double-mining, and
// double-mining has --proof-a / --proof-b which would just
// confuse a forged-attestation user.
func (c *CLI) slashHelper(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: qsdmcli slash-helper <kind> [flags]\n  kind ∈ {forged-attestation, double-mining, freshness-cheat, inspect}")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "forged-attestation":
		return c.slashHelperForgedAttestation(rest)
	case "double-mining":
		return c.slashHelperDoubleMining(rest)
	case "freshness-cheat":
		return c.slashHelperFreshnessCheat(rest)
	case "inspect":
		return c.slashHelperInspect(rest)
	default:
		return fmt.Errorf("unknown slash-helper kind %q (want forged-attestation | double-mining | freshness-cheat | inspect)", sub)
	}
}

// -----------------------------------------------------------------------------
// slash-helper forged-attestation
// -----------------------------------------------------------------------------

// slashHelperForgedAttestation builds a forgedattest.Evidence
// from a single proof file plus optional fault-class + memo,
// and emits the encoded bytes.
//
// Sanity checks performed BEFORE encoding (so the operator gets
// a clear error rather than a chain-side rejection):
//
//   - Proof.Version must be ≥ ProtocolVersionV2 (forged-
//     attestation is a v2-only offence — slashing a v1 proof
//     would be nonsensical and the chain-side decoder would
//     reject it for missing the Attestation block).
//   - Proof.Attestation.BundleBase64 must parse as an
//     hmac.Bundle. The chain-side decoder runs the same check;
//     catching it here saves one network round trip.
//   - If --node-id is set, bundle.NodeID must equal it. The
//     forgedattest verifier requires this binding (see
//     forgedattest.go:231); failing the check locally avoids a
//     guaranteed slashing.ErrEvidenceVerification rejection on
//     chain.
//
// We deliberately do NOT re-run the HMAC verifier locally. The
// slasher does not have the offender's HMAC key (the whole point
// of an HMAC system is that only the owner does), so we can't
// confirm "yes, this proof actually fails verification" client-
// side. The chain has the registry; let it adjudicate.
func (c *CLI) slashHelperForgedAttestation(args []string) error {
	fs := flag.NewFlagSet("slash-helper forged-attestation", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		proofPath  = fs.String("proof", "", "path to the offending mining.Proof canonical JSON ('-' for stdin) (required)")
		faultClass = fs.String("fault-class", "", "optional fault class hint: hmac_mismatch | gpu_uuid_mismatch | challenge_bind_mismatch | deny_listed_gpu | node_not_enrolled | node_revoked | bundle_malformed | nonce_mismatch")
		memo       = fs.String("memo", "", "optional human-readable memo (≤256 bytes)")
		out        = fs.String("out", "-", "output path for the encoded evidence ('-' for stdout)")
		nodeID     = fs.String("node-id", "", "if set, verify the proof's bundle.node_id matches before encoding")
		printCmd   = fs.Bool("print-cmd", false, "after writing the evidence, print a placeholder `qsdmcli slash` invocation to stderr")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *proofPath == "" {
		fs.Usage()
		return fmt.Errorf("--proof is required")
	}

	proof, err := readProofFile(*proofPath)
	if err != nil {
		return err
	}

	if proof.Version < mining.ProtocolVersionV2 {
		return fmt.Errorf(
			"proof.version=%d is pre-v2; forged-attestation is a v2-only offence",
			proof.Version)
	}
	if proof.Attestation.Type == "" {
		return errors.New("proof has no attestation block; cannot be a forged-attestation case")
	}
	if proof.Attestation.BundleBase64 == "" {
		return errors.New("proof.attestation.bundle is empty; cannot be a forged-attestation case")
	}
	bundle, err := hmac.ParseBundle(proof.Attestation.BundleBase64)
	if err != nil {
		// A malformed bundle IS itself a forged-attestation
		// case (FaultBundleMalformed). We surface a notice but
		// do NOT bail — the chain accepts a malformed-bundle
		// payload and slashes accordingly. Callers who want
		// strict pre-flight can check separately.
		fmt.Fprintf(os.Stderr,
			"warn: bundle parse failed (%v); evidence will still encode "+
				"and the chain will treat this as bundle_malformed\n", err)
	} else if *nodeID != "" && bundle.NodeID != *nodeID {
		return fmt.Errorf(
			"proof.bundle.node_id=%q does not match --node-id=%q; chain-side verifier would reject",
			bundle.NodeID, *nodeID)
	}

	ev := forgedattest.Evidence{
		Proof:      *proof,
		FaultClass: forgedattest.FaultClass(*faultClass),
		Memo:       *memo,
	}
	blob, err := forgedattest.EncodeEvidence(ev)
	if err != nil {
		return fmt.Errorf("encode evidence: %w", err)
	}
	// Round-trip sanity: decode our own output with the same
	// codec the chain uses. If this fails the encoder is buggy
	// and the rest of the pipeline would silently produce
	// admit-time rejections.
	if _, err := forgedattest.DecodeEvidence(blob); err != nil {
		return fmt.Errorf("encoder produced bytes that fail Decode round-trip: %w", err)
	}

	if err := writeEvidence(*out, blob); err != nil {
		return fmt.Errorf("write evidence: %w", err)
	}

	// Stderr summary so an interactive user sees what landed
	// on disk without parsing the binary blob.
	fmt.Fprintf(os.Stderr,
		"forged-attestation evidence: %d bytes, fault_class=%q memo=%dB\n",
		len(blob), *faultClass, len(*memo))

	if *printCmd {
		printSlashCmd(slashing.EvidenceKindForgedAttestation,
			*nodeID, *out, forgedattest.DefaultMaxSlashDust)
	}
	return nil
}

// -----------------------------------------------------------------------------
// slash-helper double-mining
// -----------------------------------------------------------------------------

// slashHelperDoubleMining builds a doublemining.Evidence from
// two proof files and emits the encoded bytes.
//
// Sanity checks performed BEFORE encoding mirror the chain-side
// admit + verify path so an operator gets a clear error rather
// than a far-flung consensus rejection:
//
//   - Both proofs must be v2.
//   - Both proofs must share (Epoch, Height).
//   - Both proofs' bundles must bind to the same NodeID (and to
//     --node-id when the operator passes it).
//   - Canonical bytes must DIFFER (otherwise it's the same proof
//     submitted twice — not equivocation).
//
// Order canonicalisation is delegated to
// doublemining.EncodeEvidence (lex-min of canonical bytes goes
// first); we do not pre-sort here because the encoder owns the
// invariant.
func (c *CLI) slashHelperDoubleMining(args []string) error {
	fs := flag.NewFlagSet("slash-helper double-mining", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		proofA   = fs.String("proof-a", "", "first equivocating proof file ('-' for stdin) (required)")
		proofB   = fs.String("proof-b", "", "second equivocating proof file (required, '-' permitted but not for both)")
		memo     = fs.String("memo", "", "optional human-readable memo (≤256 bytes)")
		out      = fs.String("out", "-", "output path for the encoded evidence ('-' for stdout)")
		nodeID   = fs.String("node-id", "", "if set, verify both proofs' bundle.node_id match before encoding")
		printCmd = fs.Bool("print-cmd", false, "after writing the evidence, print a placeholder `qsdmcli slash` invocation to stderr")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *proofA == "" || *proofB == "" {
		fs.Usage()
		return fmt.Errorf("--proof-a and --proof-b are required")
	}
	if *proofA == "-" && *proofB == "-" {
		return errors.New("only one of --proof-a / --proof-b may be '-' (stdin)")
	}

	pa, err := readProofFile(*proofA)
	if err != nil {
		return fmt.Errorf("proof-a: %w", err)
	}
	pb, err := readProofFile(*proofB)
	if err != nil {
		return fmt.Errorf("proof-b: %w", err)
	}

	if pa.Version < mining.ProtocolVersionV2 || pb.Version < mining.ProtocolVersionV2 {
		return fmt.Errorf(
			"both proofs must be v2 (got a.version=%d b.version=%d); double-mining is v2-only",
			pa.Version, pb.Version)
	}
	if pa.Height != pb.Height {
		return fmt.Errorf(
			"proofs must share Height for an equivocation case (got a=%d b=%d)",
			pa.Height, pb.Height)
	}
	if pa.Epoch != pb.Epoch {
		return fmt.Errorf(
			"proofs must share Epoch for an equivocation case (got a=%d b=%d)",
			pa.Epoch, pb.Epoch)
	}

	bundleA, errA := hmac.ParseBundle(pa.Attestation.BundleBase64)
	bundleB, errB := hmac.ParseBundle(pb.Attestation.BundleBase64)
	if errA != nil {
		return fmt.Errorf("proof-a bundle parse: %w", errA)
	}
	if errB != nil {
		return fmt.Errorf("proof-b bundle parse: %w", errB)
	}
	if bundleA.NodeID != bundleB.NodeID {
		return fmt.Errorf(
			"both proofs must bind to the same node_id (got a=%q b=%q); not equivocation by a single operator",
			bundleA.NodeID, bundleB.NodeID)
	}
	if *nodeID != "" && bundleA.NodeID != *nodeID {
		return fmt.Errorf(
			"proofs' bundle.node_id=%q does not match --node-id=%q; chain-side verifier would reject",
			bundleA.NodeID, *nodeID)
	}

	ev := doublemining.Evidence{
		ProofA: *pa,
		ProofB: *pb,
		Memo:   *memo,
	}
	blob, err := doublemining.EncodeEvidence(ev)
	if err != nil {
		// Most likely failure mode: ProofA == ProofB. The
		// encoder owns this invariant; we surface its message
		// verbatim because it's already actionable.
		return fmt.Errorf("encode evidence: %w", err)
	}
	if _, err := doublemining.DecodeEvidence(blob); err != nil {
		return fmt.Errorf("encoder produced bytes that fail Decode round-trip: %w", err)
	}

	if err := writeEvidence(*out, blob); err != nil {
		return fmt.Errorf("write evidence: %w", err)
	}

	resolvedNode := bundleA.NodeID
	if *nodeID != "" {
		resolvedNode = *nodeID
	}
	fmt.Fprintf(os.Stderr,
		"double-mining evidence: %d bytes, node_id=%q epoch=%d height=%d memo=%dB\n",
		len(blob), resolvedNode, pa.Epoch, pa.Height, len(*memo))

	if *printCmd {
		printSlashCmd(slashing.EvidenceKindDoubleMining,
			resolvedNode, *out, doublemining.DefaultMaxSlashDust)
	}
	return nil
}

// -----------------------------------------------------------------------------
// slash-helper freshness-cheat
// -----------------------------------------------------------------------------

// slashHelperFreshnessCheat builds a freshnesscheat.Evidence
// from one offending proof file plus a chain-anchored
// (height, block_time) pair the slasher claims sealed the
// inclusion of that proof. Emits the encoded bytes.
//
// Why the slasher must supply the anchor pair: the freshness-
// cheat offence is "this proof was on-chain at H, and at H the
// chain's wall-clock anchor was T, and (T − bundle.IssuedAt)
// exceeds the freshness window". The proof itself does not
// carry T (block time is a chain-level property, not a proof
// field), so the slasher provides it. On a v2-real-time mainnet
// the slasher reads (H, T) from `qsdmcli block-info` (future
// work); on testnets they pick a (H, T) consistent with their
// observation and trust their `freshnesscheat.TrustingTestWitness`
// to accept it.
//
// Sanity checks performed BEFORE encoding:
//
//   - Proof.Version must be ≥ ProtocolVersionV2 (freshness-cheat
//     is a v2-only offence).
//   - Proof.Attestation.BundleBase64 must parse as an
//     hmac.Bundle (a malformed bundle is forged-attestation,
//     not freshness-cheat).
//   - The supplied --anchor-block-time must sit strictly after
//     bundle.IssuedAt and within MaxAnchorAgeSeconds. Mirrors
//     the chain-side rules so an operator gets a clear error
//     locally rather than a far-flung consensus rejection.
//   - The resulting staleness (anchor_time − issued_at) must
//     exceed FreshnessWindow + DefaultGraceWindow. Submitting
//     borderline-stale evidence wastes the slasher's tx fee
//     because the chain rejects with ErrNotStaleEnough; we
//     refuse to encode it locally.
//   - If --node-id is set, bundle.NodeID must equal it. The
//     verifier requires this binding; failing the check locally
//     avoids a guaranteed slashing.ErrEvidenceVerification.
func (c *CLI) slashHelperFreshnessCheat(args []string) error {
	fs := flag.NewFlagSet("slash-helper freshness-cheat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		proofPath       = fs.String("proof", "", "path to the offending mining.Proof canonical JSON ('-' for stdin) (required)")
		anchorHeight    = fs.Uint64("anchor-height", 0, "chain block height that included the offending proof (required)")
		anchorBlockTime = fs.Int64("anchor-block-time", 0, "wall-clock seal time of the anchor block (unix seconds) (required)")
		memo            = fs.String("memo", "", "optional human-readable memo (≤256 bytes)")
		out             = fs.String("out", "-", "output path for the encoded evidence ('-' for stdout)")
		nodeID          = fs.String("node-id", "", "if set, verify the proof's bundle.node_id matches before encoding")
		printCmd        = fs.Bool("print-cmd", false, "after writing the evidence, print a placeholder `qsdmcli slash` invocation to stderr")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *proofPath == "" {
		fs.Usage()
		return fmt.Errorf("--proof is required")
	}
	if *anchorHeight == 0 {
		fs.Usage()
		return fmt.Errorf("--anchor-height is required (and must be non-zero; height 0 is genesis and cannot include a proof)")
	}
	if *anchorBlockTime <= 0 {
		fs.Usage()
		return fmt.Errorf("--anchor-block-time is required (positive unix seconds)")
	}

	proof, err := readProofFile(*proofPath)
	if err != nil {
		return err
	}

	if proof.Version < mining.ProtocolVersionV2 {
		return fmt.Errorf(
			"proof.version=%d is pre-v2; freshness-cheat is a v2-only offence",
			proof.Version)
	}
	if proof.Attestation.Type == "" {
		return errors.New("proof has no attestation block; cannot be a freshness-cheat case")
	}
	if proof.Attestation.BundleBase64 == "" {
		return errors.New("proof.attestation.bundle is empty; cannot be a freshness-cheat case")
	}
	bundle, err := hmac.ParseBundle(proof.Attestation.BundleBase64)
	if err != nil {
		// A malformed bundle would route to forged-attestation,
		// not freshness-cheat. We reject loudly so the slasher
		// switches sub-commands.
		return fmt.Errorf(
			"bundle parse failed (%w); a malformed bundle is forged-attestation, "+
				"not freshness-cheat — use `qsdmcli slash-helper forged-attestation` instead",
			err)
	}
	if *nodeID != "" && bundle.NodeID != *nodeID {
		return fmt.Errorf(
			"proof.bundle.node_id=%q does not match --node-id=%q; chain-side verifier would reject",
			bundle.NodeID, *nodeID)
	}

	// Local mirror of the verifier's anchor-sanity checks. We
	// fail loudly here so the slasher doesn't burn a tx fee on
	// guaranteed-rejection evidence.
	if *anchorBlockTime <= bundle.IssuedAt {
		return fmt.Errorf(
			"--anchor-block-time=%d must be strictly greater than bundle.issued_at=%d "+
				"(anchor cannot precede the proof being slashed)",
			*anchorBlockTime, bundle.IssuedAt)
	}
	if *anchorBlockTime-bundle.IssuedAt > freshnesscheat.MaxAnchorAgeSeconds {
		return fmt.Errorf(
			"anchor age %ds exceeds 1-year sanity bound; check --anchor-block-time / proof.bundle.issued_at",
			*anchorBlockTime-bundle.IssuedAt)
	}
	staleness := *anchorBlockTime - bundle.IssuedAt
	threshold := int64(mining.FreshnessWindow.Seconds()) + int64(freshnesscheat.DefaultGraceWindow.Seconds())
	if staleness <= threshold {
		return fmt.Errorf(
			"staleness %ds does not exceed FreshnessWindow+Grace=%ds; "+
				"proof was within the freshness window at anchor time and is NOT a slashable freshness-cheat",
			staleness, threshold)
	}

	ev := freshnesscheat.Evidence{
		Proof:           *proof,
		AnchorHeight:    *anchorHeight,
		AnchorBlockTime: *anchorBlockTime,
		Memo:            *memo,
	}
	blob, err := freshnesscheat.EncodeEvidence(ev)
	if err != nil {
		return fmt.Errorf("encode evidence: %w", err)
	}
	if _, err := freshnesscheat.DecodeEvidence(blob); err != nil {
		return fmt.Errorf("encoder produced bytes that fail Decode round-trip: %w", err)
	}

	if err := writeEvidence(*out, blob); err != nil {
		return fmt.Errorf("write evidence: %w", err)
	}

	resolvedNode := bundle.NodeID
	if *nodeID != "" {
		resolvedNode = *nodeID
	}
	fmt.Fprintf(os.Stderr,
		"freshness-cheat evidence: %d bytes, node_id=%q anchor_height=%d staleness=%ds memo=%dB\n",
		len(blob), resolvedNode, *anchorHeight, staleness, len(*memo))
	fmt.Fprintf(os.Stderr,
		"note: chain-side acceptance still requires a configured BlockInclusionWitness "+
			"(production today rejects all freshness-cheat slashes pending BFT finality; see MINING_PROTOCOL_V2.md §12.3)\n")

	if *printCmd {
		printSlashCmd(slashing.EvidenceKindFreshnessCheat,
			resolvedNode, *out, freshnesscheat.DefaultMaxSlashDust)
	}
	return nil
}

// -----------------------------------------------------------------------------
// slash-helper inspect
// -----------------------------------------------------------------------------

// inspectView is the human-readable wire shape printed by
// `slash-helper inspect`. Distinct from the per-kind Evidence
// structs because:
//
//   - Mining proof JSON is verbose; we surface the operator-
//     useful subset (height, epoch, miner_addr, version, type).
//   - Hex-encoded fields (HMAC, BundleBase64) are summarised by
//     length rather than dumped raw, to keep stdout under one
//     screenful for the common case.
//
// Operators who want the full proof JSON should pipe through
// jq or use the dedicated mining-proof inspector (future work).
type inspectView struct {
	Kind slashing.EvidenceKind  `json:"kind"`
	Size int                    `json:"size_bytes"`
	A    map[string]interface{} `json:"proof_a,omitempty"`
	B    map[string]interface{} `json:"proof_b,omitempty"`
	Memo string                 `json:"memo,omitempty"`
	// Only populated for forged-attestation:
	FaultClass forgedattest.FaultClass `json:"fault_class,omitempty"`
	// Only populated for freshness-cheat:
	AnchorHeight    uint64 `json:"anchor_height,omitempty"`
	AnchorBlockTime int64  `json:"anchor_block_time,omitempty"`
	StalenessSecs   int64  `json:"staleness_secs,omitempty"`
}

// proofSummary maps a mining.Proof to a small dict of operator-
// useful fields. We DO NOT dump the full canonical JSON; an
// operator who wants byte-level access can pipe the original
// proof file through jq / cat.
func proofSummary(p mining.Proof) map[string]interface{} {
	bundleLen := 0
	attestType := ""
	if p.Attestation.Type != "" {
		attestType = string(p.Attestation.Type)
		bundleLen = len(p.Attestation.BundleBase64)
	}
	return map[string]interface{}{
		"version":       p.Version,
		"epoch":         p.Epoch,
		"height":        p.Height,
		"miner_addr":    p.MinerAddr,
		"batch_count":   p.BatchCount,
		"attest_type":   attestType,
		"attest_bundle": fmt.Sprintf("%d bytes (b64)", bundleLen),
	}
}

// slashHelperInspect decodes an existing evidence blob and
// pretty-prints its contents. The kind is inferred from a
// required --kind flag — we deliberately do NOT auto-detect by
// JSON shape because both forgedattest and doublemining wire
// envelopes are JSON objects with similar field names; an
// auto-detect would add ambiguity for zero benefit (every caller
// already knows what kind they're inspecting because they chose
// it when they built the slash payload).
func (c *CLI) slashHelperInspect(args []string) error {
	fs := flag.NewFlagSet("slash-helper inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		kind         = fs.String("kind", "", "evidence kind: forged-attestation | double-mining | freshness-cheat (required)")
		evidenceFile = fs.String("evidence-file", "", "path to evidence blob ('-' for stdin)")
		evidenceHex  = fs.String("evidence-hex", "", "hex-encoded evidence blob")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *kind == "" {
		fs.Usage()
		return fmt.Errorf("--kind is required")
	}

	var blob []byte
	var err error
	switch {
	case *evidenceFile != "":
		if *evidenceFile == "-" {
			blob, err = io.ReadAll(os.Stdin)
		} else {
			blob, err = os.ReadFile(*evidenceFile)
		}
	case *evidenceHex != "":
		blob, err = hex.DecodeString(strings.TrimSpace(*evidenceHex))
	default:
		return fmt.Errorf("provide one of --evidence-file or --evidence-hex")
	}
	if err != nil {
		return fmt.Errorf("read evidence: %w", err)
	}

	view := inspectView{
		Kind: slashing.EvidenceKind(*kind),
		Size: len(blob),
	}
	switch slashing.EvidenceKind(*kind) {
	case slashing.EvidenceKindForgedAttestation:
		ev, err := forgedattest.DecodeEvidence(blob)
		if err != nil {
			return fmt.Errorf("decode forged-attestation evidence: %w", err)
		}
		view.A = proofSummary(ev.Proof)
		view.FaultClass = ev.FaultClass
		view.Memo = ev.Memo
	case slashing.EvidenceKindDoubleMining:
		ev, err := doublemining.DecodeEvidence(blob)
		if err != nil {
			return fmt.Errorf("decode double-mining evidence: %w", err)
		}
		view.A = proofSummary(ev.ProofA)
		view.B = proofSummary(ev.ProofB)
		view.Memo = ev.Memo
	case slashing.EvidenceKindFreshnessCheat:
		ev, err := freshnesscheat.DecodeEvidence(blob)
		if err != nil {
			return fmt.Errorf("decode freshness-cheat evidence: %w", err)
		}
		view.A = proofSummary(ev.Proof)
		view.AnchorHeight = ev.AnchorHeight
		view.AnchorBlockTime = ev.AnchorBlockTime
		// Staleness is the operator-meaningful number — surface
		// it explicitly so an inspector can confirm at a glance
		// "yes, this is well past the freshness window".
		view.StalenessSecs = ev.AnchorBlockTime - ev.Proof.Attestation.IssuedAt
		view.Memo = ev.Memo
	default:
		return fmt.Errorf(
			"unsupported --kind %q (slash-helper inspect supports forged-attestation, double-mining, freshness-cheat)",
			*kind)
	}

	out, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal inspect view: %w", err)
	}
	fmt.Println(string(out))
	return nil
}
