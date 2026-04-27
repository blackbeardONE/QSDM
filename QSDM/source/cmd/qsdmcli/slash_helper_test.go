package main

// slash_helper_test.go — coverage for the slash-helper
// subcommands. Strategy:
//
//   - Build a known-good v2 proof + bundle pair (the same shape
//     the chain-side forgedattest / doublemining tests use), then
//     drive each subcommand against temp files.
//   - Round-trip the produced evidence through the verifier
//     packages' DecodeEvidence to confirm we emit consensus-
//     compatible bytes.
//   - Hit the negative paths (missing flags, mismatched node-id,
//     non-v2 proofs, identical proofs, etc.) so a future
//     refactor cannot quietly regress an admit-time check.
//
// Tests are deliberately not parallelised: a few of them
// substitute os.Stdin / os.Stdout, which is process-global state.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blackbeardONE/QSDM/pkg/mining"
	"github.com/blackbeardONE/QSDM/pkg/mining/attest/hmac"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/doublemining"
	"github.com/blackbeardONE/QSDM/pkg/mining/slashing/forgedattest"
)

const (
	tNodeID  = "alice-rtx4090-01"
	tGPUUUID = "GPU-01234567-89ab-cdef-0123-456789abcdef"
	tGPUName = "NVIDIA GeForce RTX 4090"
	tAddr    = "qsdm1testminer"
)

var tKey = []byte("slash-helper-test-key------32-bytes!")[:32]

// buildSignedProofForCLI mirrors the pkg/mining/slashing fixtures
// but lives here because cmd packages cannot import test helpers
// from another package's _test.go. We diverge from the chain-
// side helper only by keeping deterministic seeds (tests rely on
// stable canonical bytes for ordering assertions).
//
// height is parameterised so a single test can build two
// distinct-canonical proofs that share or differ on (Epoch,
// Height) without code duplication.
func buildSignedProofForCLI(t *testing.T, epoch, height uint64, batchSeed byte) mining.Proof {
	t.Helper()

	var nonce [32]byte
	for i := range nonce {
		nonce[i] = batchSeed ^ byte(i)
	}
	var batchRoot, mix [32]byte
	for i := range batchRoot {
		batchRoot[i] = byte(i) + batchSeed
		mix[i] = byte(0xFF-i) ^ batchSeed
	}

	p := mining.Proof{
		Version:    mining.ProtocolVersionV2,
		Epoch:      epoch,
		Height:     height,
		HeaderHash: [32]byte{0xBB, batchSeed},
		BatchRoot:  batchRoot,
		BatchCount: 1,
		Nonce:      [16]byte{0x07, batchSeed},
		MixDigest:  mix,
		MinerAddr:  tAddr,
		Attestation: mining.Attestation{
			Type:     mining.AttestationTypeHMAC,
			GPUArch:  "ada",
			Nonce:    nonce,
			IssuedAt: 1_700_000_000,
		},
	}

	b := hmac.Bundle{
		ChallengeBind: hmac.HexChallengeBind(tAddr, batchRoot, mix),
		ComputeCap:    "8.9",
		CUDAVersion:   "12.8",
		DriverVer:     "572.16",
		GPUName:       tGPUName,
		GPUUUID:       tGPUUUID,
		IssuedAt:      1_700_000_000,
		NodeID:        tNodeID,
		Nonce:         hex.EncodeToString(nonce[:]),
	}
	signed, err := b.Sign(tKey)
	if err != nil {
		t.Fatalf("sign bundle: %v", err)
	}
	bundleB64, err := signed.MarshalBase64()
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	p.Attestation.BundleBase64 = bundleB64
	return p
}

// writeProof writes a proof's canonical-JSON form to a temp
// file under t.TempDir(), returning the path. The encoding
// matches what the chain hashed; readProofFile decodes it via
// mining.ParseProof, the same path the chain uses on consume.
func writeProof(t *testing.T, p mining.Proof) string {
	t.Helper()
	raw, err := p.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "proof.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write proof: %v", err)
	}
	return path
}

// captureStdout redirects os.Stdout to a pipe for the duration
// of fn, returning whatever fn wrote. The slash-helper writes
// raw evidence bytes to stdout when --out is unset, so tests
// need to capture and decode that stream.
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	origOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan []byte, 1)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- buf
	}()
	fn()
	w.Close()
	os.Stdout = origOut
	return <-done
}

// captureStderr is the same pattern for stderr — used by the
// --print-cmd test which needs to confirm the placeholder
// snippet lands on the right stream so it doesn't corrupt
// piped evidence bytes.
func captureStderr(t *testing.T, fn func()) []byte {
	t.Helper()
	origErr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan []byte, 1)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- buf
	}()
	fn()
	w.Close()
	os.Stderr = origErr
	return <-done
}

// -----------------------------------------------------------------------------
// dispatcher
// -----------------------------------------------------------------------------

func TestSlashHelper_DispatcherRejectsUnknownKind(t *testing.T) {
	cli := &CLI{}
	if err := cli.slashHelper([]string{"weird-kind"}); err == nil {
		t.Fatal("unknown kind accepted")
	} else if !strings.Contains(err.Error(), "unknown slash-helper kind") {
		t.Errorf("error should mention unknown kind: %v", err)
	}
}

func TestSlashHelper_DispatcherRejectsEmpty(t *testing.T) {
	cli := &CLI{}
	if err := cli.slashHelper(nil); err == nil {
		t.Fatal("empty args accepted")
	}
}

// -----------------------------------------------------------------------------
// forged-attestation
// -----------------------------------------------------------------------------

// TestSlashHelperForgedAttestation_HappyPath drives the
// subcommand end to end: it builds a real signed proof, runs
// the helper, and feeds the produced bytes back through
// forgedattest.DecodeEvidence to confirm a chain consumer can
// recover the same Proof + FaultClass + Memo we asked for.
func TestSlashHelperForgedAttestation_HappyPath(t *testing.T) {
	cli := &CLI{}
	p := buildSignedProofForCLI(t, 0, 200, 0x11)
	proofPath := writeProof(t, p)

	dir := t.TempDir()
	outPath := filepath.Join(dir, "evidence.bin")

	stderr := captureStderr(t, func() {
		err := cli.slashHelper([]string{
			"forged-attestation",
			"--proof", proofPath,
			"--fault-class", string(forgedattest.FaultHMACMismatch),
			"--memo", "watcher-bot caught it",
			"--node-id", tNodeID,
			"--out", outPath,
		})
		if err != nil {
			t.Fatalf("slashHelper: %v", err)
		}
	})

	blob, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("evidence file is empty")
	}
	ev, err := forgedattest.DecodeEvidence(blob)
	if err != nil {
		t.Fatalf("decode round-trip: %v", err)
	}
	if ev.FaultClass != forgedattest.FaultHMACMismatch {
		t.Errorf("fault class lost: got %q", ev.FaultClass)
	}
	if ev.Memo != "watcher-bot caught it" {
		t.Errorf("memo lost: got %q", ev.Memo)
	}
	if ev.Proof.Height != 200 || ev.Proof.Version != mining.ProtocolVersionV2 {
		t.Errorf("proof fields lost: %+v", ev.Proof)
	}
	if !bytes.Contains(stderr, []byte("forged-attestation evidence:")) {
		t.Errorf("expected human-readable summary on stderr, got %q", stderr)
	}
}

// TestSlashHelperForgedAttestation_StdoutWrite covers the
// default --out=- path: bytes flow to stdout, ready to pipe
// into `qsdmcli slash --evidence-file=-`.
func TestSlashHelperForgedAttestation_StdoutWrite(t *testing.T) {
	cli := &CLI{}
	p := buildSignedProofForCLI(t, 0, 50, 0x22)
	proofPath := writeProof(t, p)

	stdout := captureStdout(t, func() {
		// Helper writes a stderr summary which we ignore here;
		// the test cares about stdout staying pure.
		_ = captureStderr(t, func() {
			if err := cli.slashHelper([]string{
				"forged-attestation",
				"--proof", proofPath,
			}); err != nil {
				t.Fatalf("slashHelper: %v", err)
			}
		})
	})
	if len(stdout) == 0 {
		t.Fatal("stdout empty; helper should default to stdout")
	}
	if _, err := forgedattest.DecodeEvidence(stdout); err != nil {
		t.Errorf("stdout bytes failed Decode round-trip: %v", err)
	}
}

// TestSlashHelperForgedAttestation_PrintCmd asserts the helper
// honours --print-cmd by emitting a `qsdmcli slash …` snippet to
// stderr (NEVER stdout, which carries the evidence bytes).
func TestSlashHelperForgedAttestation_PrintCmd(t *testing.T) {
	cli := &CLI{}
	p := buildSignedProofForCLI(t, 0, 75, 0x33)
	proofPath := writeProof(t, p)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "evidence.bin")

	stderr := captureStderr(t, func() {
		if err := cli.slashHelper([]string{
			"forged-attestation",
			"--proof", proofPath,
			"--node-id", tNodeID,
			"--out", outPath,
			"--print-cmd",
		}); err != nil {
			t.Fatalf("slashHelper: %v", err)
		}
	})
	wants := []string{
		"qsdmcli slash",
		"--evidence-kind=forged-attestation",
		"--node-id=" + tNodeID,
		"--evidence-file=" + outPath,
	}
	for _, w := range wants {
		if !bytes.Contains(stderr, []byte(w)) {
			t.Errorf("expected stderr to contain %q; got: %s", w, stderr)
		}
	}
}

// TestSlashHelperForgedAttestation_RejectsMissingProof guards
// the constructor's validation. Missing --proof is an early
// programmer error that should produce a clear message rather
// than panic in os.ReadFile.
func TestSlashHelperForgedAttestation_RejectsMissingProof(t *testing.T) {
	cli := &CLI{}
	if err := cli.slashHelper([]string{"forged-attestation"}); err == nil {
		t.Fatal("missing --proof accepted")
	}
}

// TestSlashHelperForgedAttestation_RejectsV1Proof confirms the
// pre-flight Version check fires before encoding. Forged-
// attestation is a v2-only offence; encoding evidence around a
// v1 proof would be wasted bytes the chain rejects anyway.
func TestSlashHelperForgedAttestation_RejectsV1Proof(t *testing.T) {
	cli := &CLI{}

	v1 := mining.Proof{
		Version:    1,
		Epoch:      0,
		Height:     1,
		HeaderHash: [32]byte{0x01},
		BatchCount: 1,
		MinerAddr:  tAddr,
	}
	raw, err := v1.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "v1.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write proof: %v", err)
	}

	err = cli.slashHelper([]string{"forged-attestation", "--proof", path})
	if err == nil || !strings.Contains(err.Error(), "pre-v2") {
		t.Errorf("v1 proof should be rejected with 'pre-v2' message; got %v", err)
	}
}

// TestSlashHelperForgedAttestation_RejectsNodeIDMismatch
// confirms the cross-check between bundle.NodeID and --node-id
// fires before encoding. Mismatched node-id is a guaranteed
// chain-side rejection (forgedattest.go binds them); failing
// fast saves the operator a fee.
func TestSlashHelperForgedAttestation_RejectsNodeIDMismatch(t *testing.T) {
	cli := &CLI{}
	p := buildSignedProofForCLI(t, 0, 100, 0x44)
	proofPath := writeProof(t, p)

	err := cli.slashHelper([]string{
		"forged-attestation",
		"--proof", proofPath,
		"--node-id", "wrong-node-id",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("node-id mismatch should be rejected; got %v", err)
	}
}

// TestSlashHelperForgedAttestation_RejectsUnknownFaultClass
// surfaces the encoder's allowlist. Stuffing arbitrary fault
// metadata into the consensus pre-image could enable
// fingerprint replay; the encoder declines, and we pass that
// up cleanly to the user.
func TestSlashHelperForgedAttestation_RejectsUnknownFaultClass(t *testing.T) {
	cli := &CLI{}
	p := buildSignedProofForCLI(t, 0, 100, 0x55)
	proofPath := writeProof(t, p)

	err := cli.slashHelper([]string{
		"forged-attestation",
		"--proof", proofPath,
		"--fault-class", "made-up-class",
	})
	if err == nil || !strings.Contains(err.Error(), "fault_class") {
		t.Errorf("unknown fault-class should be rejected; got %v", err)
	}
}

// -----------------------------------------------------------------------------
// double-mining
// -----------------------------------------------------------------------------

// TestSlashHelperDoubleMining_HappyPath drives the subcommand
// against two distinct-canonical proofs at the same (Epoch,
// Height). The encoder canonicalises ordering, so we feed in
// either order and confirm the produced bytes decode.
func TestSlashHelperDoubleMining_HappyPath(t *testing.T) {
	cli := &CLI{}
	pa := buildSignedProofForCLI(t, 5, 100, 0x10)
	pb := buildSignedProofForCLI(t, 5, 100, 0x20) // distinct canonical
	pathA := writeProof(t, pa)
	pathB := writeProof(t, pb)

	dir := t.TempDir()
	outPath := filepath.Join(dir, "evidence.bin")

	_ = captureStderr(t, func() {
		err := cli.slashHelper([]string{
			"double-mining",
			"--proof-a", pathA,
			"--proof-b", pathB,
			"--node-id", tNodeID,
			"--memo", "fan-out caught at validator-3",
			"--out", outPath,
		})
		if err != nil {
			t.Fatalf("slashHelper: %v", err)
		}
	})

	blob, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	ev, err := doublemining.DecodeEvidence(blob)
	if err != nil {
		t.Fatalf("decode round-trip: %v", err)
	}
	if ev.Memo != "fan-out caught at validator-3" {
		t.Errorf("memo lost: got %q", ev.Memo)
	}
	if ev.ProofA.Height != 100 || ev.ProofB.Height != 100 {
		t.Errorf("heights wrong: a=%d b=%d", ev.ProofA.Height, ev.ProofB.Height)
	}
}

// TestSlashHelperDoubleMining_StableEncoding documents the
// "two slashers see the same equivocation, both produce the
// same bytes" guarantee. We feed the same pair in opposite
// orders and assert the encoded output is byte-identical.
func TestSlashHelperDoubleMining_StableEncoding(t *testing.T) {
	cli := &CLI{}
	pa := buildSignedProofForCLI(t, 5, 100, 0x10)
	pb := buildSignedProofForCLI(t, 5, 100, 0x20)
	pathA := writeProof(t, pa)
	pathB := writeProof(t, pb)
	dir := t.TempDir()
	outAB := filepath.Join(dir, "ab.bin")
	outBA := filepath.Join(dir, "ba.bin")

	for _, run := range []struct {
		out  string
		args []string
	}{
		{outAB, []string{"double-mining", "--proof-a", pathA, "--proof-b", pathB, "--out", outAB}},
		{outBA, []string{"double-mining", "--proof-a", pathB, "--proof-b", pathA, "--out", outBA}},
	} {
		_ = captureStderr(t, func() {
			if err := cli.slashHelper(run.args); err != nil {
				t.Fatalf("slashHelper: %v", err)
			}
		})
	}

	ab, _ := os.ReadFile(outAB)
	ba, _ := os.ReadFile(outBA)
	if !bytes.Equal(ab, ba) {
		t.Errorf("AB / BA outputs differ: encoder is not order-stable")
	}
}

// TestSlashHelperDoubleMining_RejectsHeightMismatch fires the
// pre-flight (Epoch, Height) check. A mismatch isn't
// equivocation; submitting it would waste a slasher's fee.
func TestSlashHelperDoubleMining_RejectsHeightMismatch(t *testing.T) {
	cli := &CLI{}
	pa := buildSignedProofForCLI(t, 5, 100, 0x10)
	pb := buildSignedProofForCLI(t, 5, 101, 0x20) // height differs
	pathA := writeProof(t, pa)
	pathB := writeProof(t, pb)

	err := cli.slashHelper([]string{
		"double-mining",
		"--proof-a", pathA,
		"--proof-b", pathB,
	})
	if err == nil || !strings.Contains(err.Error(), "Height") {
		t.Errorf("height mismatch should be rejected; got %v", err)
	}
}

// TestSlashHelperDoubleMining_RejectsIdenticalProofs surfaces
// the encoder's "ProofA == ProofB" check. A confused slasher
// who submits the same proof twice gets a clear error rather
// than a silent zero-byte difference.
func TestSlashHelperDoubleMining_RejectsIdenticalProofs(t *testing.T) {
	cli := &CLI{}
	pa := buildSignedProofForCLI(t, 5, 100, 0x10)
	pathA := writeProof(t, pa)
	// Same canonical bytes, two different files. Encoder
	// compares canonical bytes, not file paths.
	pathB := writeProof(t, pa)

	err := cli.slashHelper([]string{
		"double-mining",
		"--proof-a", pathA,
		"--proof-b", pathB,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "equivocation") {
		t.Errorf("identical proofs should be rejected with equivocation message; got %v", err)
	}
}

// TestSlashHelperDoubleMining_RejectsBothStdin enforces the
// "only one of --proof-a / --proof-b may be '-'" rule. Two
// stdin inputs are nonsensical and would block forever.
func TestSlashHelperDoubleMining_RejectsBothStdin(t *testing.T) {
	cli := &CLI{}
	err := cli.slashHelper([]string{
		"double-mining",
		"--proof-a", "-",
		"--proof-b", "-",
	})
	if err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Errorf("both-stdin should be rejected; got %v", err)
	}
}

// TestSlashHelperDoubleMining_RejectsMissingFlags catches the
// constructor's both-required check.
func TestSlashHelperDoubleMining_RejectsMissingFlags(t *testing.T) {
	cli := &CLI{}
	if err := cli.slashHelper([]string{"double-mining"}); err == nil {
		t.Fatal("missing --proof-a/--proof-b accepted")
	}
}

// -----------------------------------------------------------------------------
// inspect
// -----------------------------------------------------------------------------

// TestSlashHelperInspect_ForgedAttestation drives the inspect
// subcommand against a real forged-attestation evidence blob
// and confirms the operator-facing JSON view recovers the
// fault_class + memo + proof summary.
func TestSlashHelperInspect_ForgedAttestation(t *testing.T) {
	cli := &CLI{}
	p := buildSignedProofForCLI(t, 0, 200, 0x66)
	blob, err := forgedattest.EncodeEvidence(forgedattest.Evidence{
		Proof:      p,
		FaultClass: forgedattest.FaultHMACMismatch,
		Memo:       "inspect target",
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	dir := t.TempDir()
	evidencePath := filepath.Join(dir, "ev.bin")
	if err := os.WriteFile(evidencePath, blob, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	stdout := captureStdout(t, func() {
		err := cli.slashHelper([]string{
			"inspect",
			"--kind", string(slashing.EvidenceKindForgedAttestation),
			"--evidence-file", evidencePath,
		})
		if err != nil {
			t.Fatalf("slashHelper inspect: %v", err)
		}
	})

	var view map[string]interface{}
	if err := json.Unmarshal(stdout, &view); err != nil {
		t.Fatalf("inspect output not JSON: %v body=%s", err, stdout)
	}
	if view["kind"] != string(slashing.EvidenceKindForgedAttestation) {
		t.Errorf("kind: got %v", view["kind"])
	}
	if view["fault_class"] != string(forgedattest.FaultHMACMismatch) {
		t.Errorf("fault_class: got %v", view["fault_class"])
	}
	if view["memo"] != "inspect target" {
		t.Errorf("memo: got %v", view["memo"])
	}
	if _, ok := view["proof_a"]; !ok {
		t.Errorf("inspect should populate proof_a; got: %v", view)
	}
}

// TestSlashHelperInspect_DoubleMining covers the equivocation
// path of the inspector — both proof_a and proof_b should be
// populated, no fault_class.
func TestSlashHelperInspect_DoubleMining(t *testing.T) {
	cli := &CLI{}
	pa := buildSignedProofForCLI(t, 5, 100, 0x77)
	pb := buildSignedProofForCLI(t, 5, 100, 0x88)
	blob, err := doublemining.EncodeEvidence(doublemining.Evidence{
		ProofA: pa,
		ProofB: pb,
		Memo:   "fan-out",
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	stdout := captureStdout(t, func() {
		err := cli.slashHelper([]string{
			"inspect",
			"--kind", string(slashing.EvidenceKindDoubleMining),
			"--evidence-hex", hex.EncodeToString(blob),
		})
		if err != nil {
			t.Fatalf("slashHelper inspect: %v", err)
		}
	})

	var view map[string]interface{}
	if err := json.Unmarshal(stdout, &view); err != nil {
		t.Fatalf("inspect output not JSON: %v body=%s", err, stdout)
	}
	if _, ok := view["proof_a"]; !ok {
		t.Errorf("missing proof_a: %v", view)
	}
	if _, ok := view["proof_b"]; !ok {
		t.Errorf("missing proof_b: %v", view)
	}
	if view["memo"] != "fan-out" {
		t.Errorf("memo: got %v", view["memo"])
	}
}

// TestSlashHelperInspect_RejectsMissingFlags pins the
// constructor's --kind requirement and the
// "one of --evidence-file/--evidence-hex" requirement.
func TestSlashHelperInspect_RejectsMissingFlags(t *testing.T) {
	cli := &CLI{}
	tests := [][]string{
		{"inspect"},
		{"inspect", "--kind", "forged-attestation"}, // missing evidence
	}
	for _, args := range tests {
		if err := cli.slashHelper(args); err == nil {
			t.Errorf("missing flags accepted: %v", args)
		}
	}
}

// TestSlashHelperInspect_RejectsBadKind ensures the inspect
// subcommand only handles kinds it understands. Adding a new
// EvidenceKind in the future is intentionally a code change
// here, not silent passthrough.
func TestSlashHelperInspect_RejectsBadKind(t *testing.T) {
	cli := &CLI{}
	dir := t.TempDir()
	evPath := filepath.Join(dir, "x.bin")
	_ = os.WriteFile(evPath, []byte("garbage"), 0o600)

	err := cli.slashHelper([]string{
		"inspect",
		"--kind", "freshness-cheat", // not yet supported
		"--evidence-file", evPath,
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected unsupported-kind error; got %v", err)
	}
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

// TestReadProofFile_RejectsEmpty is a regression guard: an
// operator who pipes an empty file or empty stdin should get a
// clear "empty" error, not a downstream "json: unexpected end
// of input" mystery.
func TestReadProofFile_RejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty")
	_ = os.WriteFile(path, []byte("   \n"), 0o600)
	if _, err := readProofFile(path); err == nil ||
		!strings.Contains(err.Error(), "empty") {
		t.Errorf("expected empty-file error; got %v", err)
	}
}

// TestReadProofFile_RejectsMissing covers the other constructor
// branch: empty path is a programmer error, not a stdin alias.
func TestReadProofFile_RejectsMissing(t *testing.T) {
	if _, err := readProofFile(""); err == nil {
		t.Error("empty path accepted")
	}
}
