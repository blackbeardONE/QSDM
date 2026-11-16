"""Sabotage of the hand-merged join points on the public port. Each case edits one file (exact text, asserted),
runs the named package tests, expects FAILURE, and restores the file byte for byte."""
import subprocess
import sys
from pathlib import Path

SRC = Path(sys.argv[1])
CASES = [
    ("PULSE folds before QSDM-20", "pkg/chain/enrollment_aware_applier.go",
     "\t// QSDM-20 tokens join the root once the first token exists, so every\n",
     "\tif ps := a.PulseStateStore(); ps != nil && ps.Count() > 0 {\n\t\th := sha256.New()\n\t\th.Write([]byte(\"state:\"))\n"
     "\t\th.Write([]byte(root))\n\t\th.Write([]byte(\"\\npulse:\"))\n\t\th.Write([]byte(ps.StateRoot()))\n"
     "\t\troot = hex.EncodeToString(h.Sum(nil))\n\t}\n\t// QSDM-20 tokens join the root once the first token exists, so every\n",
     "./pkg/chain/", "TestPulseFoldsAfterQSDM20"),
    ("empty PULSE store folds too", "pkg/chain/enrollment_aware_applier.go",
     "\tif ps := a.PulseStateStore(); ps != nil && ps.Count() > 0 {\n",
     "\tif ps := a.PulseStateStore(); ps != nil {\n", "./pkg/chain/", "TestPulseEmptyStoreKeepsThePublicRoots"),
    ("replay clone drops PULSE", "pkg/chain/enrollment_aware_applier.go",
     "\t\tclone.pulse = livePulse.ChainReplayClone().(*PulseStateStore)\n", "\t\t_ = livePulse\n",
     "./pkg/chain/", "Pulse"),
    ("I7 drops the PULSE family", "internal/legacymining/ledger.go",
     "\t\tcase tx.ContractID == chain.TaskContractID:\n\t\tcase tx.ContractID == chain.PulseContractID:\n",
     "\t\tcase tx.ContractID == chain.TaskContractID:\n", "./internal/legacymining/", "Pulse"),
    ("v2wiring: no PULSE admission layer", "internal/v2wiring/v2wiring.go",
     "\t\t\t\t\t\tchain.PulseAdmissionChecker(\n\t\t\t\t\t\t\tchain.RecoveryCapsuleAdmissionChecker(\n"
     "\t\t\t\t\t\t\t\tchain.QSDM20AdmissionChecker(aware.NextBlockHeight, cfg.BaseAdmit))))))))\n",
     "\t\t\t\t\t\tchain.RecoveryCapsuleAdmissionChecker(\n"
     "\t\t\t\t\t\t\tchain.QSDM20AdmissionChecker(aware.NextBlockHeight, cfg.BaseAdmit)))))))\n",
     "./internal/v2wiring/ ./cmd/qsdm/ ./pkg/api/", "Pulse"),
    ("v2wiring: PULSE store not installed", "internal/v2wiring/v2wiring.go",
     "\tpulseState := chain.NewPulseStateStore()\n\taware.SetPulseStateStore(pulseState)\n",
     "\tpulseState := chain.NewPulseStateStore()\n",
     "./internal/v2wiring/ ./cmd/qsdm/ ./internal/blockdriver/", "Pulse"),
    ("boot restore skips PULSE", "cmd/qsdm/main.go",
     "\taware.SetQSDM20StateStore(qsdm20)\n\taware.SetPulseStateStore(pulseState)\n\treturn persistedStateRestore{\n",
     "\taware.SetQSDM20StateStore(qsdm20)\n\treturn persistedStateRestore{\n", "./cmd/qsdm/", "Pulse|Persisted|Restore"),
    ("snapshot scan skips PULSE replay", "cmd/qsdm/main.go",
     "\t\tif _, err := replayPulseStateFromBlocks(pulseState, []*chain.Block{blk}); err != nil {\n"
     "\t\t\treturn -1, fmt.Errorf(\"replay PULSE state at height %d: %w\", blk.Height, err)\n\t\t}\n",
     "", "./cmd/qsdm/", "Pulse|Persisted|Snapshot"),
]
caught = 0
results = []
for name, rel, old, new, pkgs, run in CASES:
    p = SRC / rel
    orig = p.read_bytes()
    crlf = b"\r\n" in orig
    text = orig.decode("utf-8").replace("\r\n", "\n")
    assert text.count(old) == 1, (name, text.count(old))
    mutated = text.replace(old, new)
    if crlf:
        mutated = mutated.replace("\n", "\r\n")
    p.write_bytes(mutated.encode("utf-8"))
    try:
        r = subprocess.run(["go", "test", "-short", "-count=1", "-run", run] + pkgs.split(), cwd=SRC,
                           capture_output=True, text=True, env={**__import__("os").environ, "CGO_ENABLED": "0", "GOFLAGS": "-p=2"})
        failed = r.returncode != 0 and "FAIL" in r.stdout + r.stderr and "no test files" not in r.stdout
        if not failed and "ok " not in r.stdout:
            print("  (no test ran?)", r.stdout[-300:], r.stderr[-300:])
    finally:
        p.write_bytes(orig)
    assert p.read_bytes() == orig
    caught += failed
    results.append(("CAUGHT" if failed else "MISSED") + ": " + name)
    print(results[-1], flush=True)
print("sabotage: %d of %d caught" % (caught, len(CASES)))
