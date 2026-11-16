"""Sabotage for internal/pulse: each case removes one check; the package tests
must FAIL. Files are restored byte for byte. Run from src/: python -I internal/pulse/testdata/sabotage.py"""
import hashlib
import os
import subprocess
import sys
from pathlib import Path

PKG = Path(__file__).resolve().parent.parent
CASES = [
    ("inactive rule verifies", "rule.go", "\tif !r.Active(height) {\n\t\treturn nil, ErrInactive", "\tif false {\n\t\treturn nil, ErrInactive"),
    ("unset height means active", "rule.go", "r.Params.ActivationHeight > 0 && height", "height"),
    ("signature not checked", "rule.go", "\tif !Verify(pk, BatchDomain, body, sig) {", "\tif false {"),
    ("pool not bound to its key", "rule.go", "if err != nil || AddressOf(pk) != b.PoolID {", "if err != nil {"),
    ("height not bound", "rule.go", "\tif b.Height != height {", "\tif false {"),
    ("fee ceiling ignored", "rule.go", "\tif b.FeeBPS > maxFee {", "\tif false {"),
    ("sequence replay", "rule.go", "\tif b.Seq <= st.LastSeq(b.PoolID) {", "\tif false {"),
    ("non-member proof pooled", "rule.go", "\t\tif !members[c.Owner] {", "\t\tif false {"),
    ("listed proof missing", "rule.go", "\tif found != len(b.ProofIDs) {", "\tif false {"),
    ("unenrolled paid", "rule.go", "\t\tif enrolled(w.Owner) {", "\t\tif true {"),
    ("pooled proof replay", "rule.go", "\t\tif st.ProofPooled(id) {", "\t\tif false {"),
    ("fee cap ignored", "split.go", "\tif c := in.CapLeft(in.FeeTo); p > c {", "\tif c := in.CapLeft(in.FeeTo); false && p > c {"),
]


def main() -> int:
    env = dict(os.environ, GOFLAGS="-p=2")
    src = PKG.parent.parent
    caught = 0
    for name, rel, old, new in CASES:
        p = PKG / rel
        orig = p.read_bytes()
        h = hashlib.sha256(orig).hexdigest()
        text = orig.decode()
        assert text.count(old) == 1, (name, text.count(old))
        try:
            p.write_bytes(text.replace(old, new).encode())
            r = subprocess.run(["go", "test", "./internal/pulse/", "-count=1"], cwd=src, env=env, capture_output=True, text=True)
            ok = r.returncode != 0
        finally:
            p.write_bytes(orig)
        assert hashlib.sha256(p.read_bytes()).hexdigest() == h
        caught += ok
        print(("CAUGHT " if ok else "MISSED ") + name)
    print(f"sabotage: {caught} of {len(CASES)} caught")
    return 0 if caught == len(CASES) else 1


if __name__ == "__main__":
    sys.exit(main())
