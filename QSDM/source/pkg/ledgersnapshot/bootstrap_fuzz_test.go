package ledgersnapshot

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// fuzzManifestFiles is an independent statement of the four validator files
// a snapshot holds, in the order Capture writes them.
var fuzzManifestFiles = []string{"qsdm_accounts.json", "qsdm_chain.ndjson", "qsdm_enrollment.json", "qsdm_staking.json"}

// fuzzLowerHex64 is an independent statement of the digest format: 64
// characters, each 0-9 or a-f.
func fuzzLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !('0' <= c && c <= '9' || 'a' <= c && c <= 'f') {
			return false
		}
	}
	return true
}

func fuzzGoodManifest() Manifest {
	m := Manifest{
		SchemaVersion:            1,
		CreatedAt:                "2000-01-02T03:04:05Z",
		Mode:                     "stopped_validator_copy",
		Privacy:                  "aggregate evidence only; source paths and state identifiers are omitted",
		StateLock:                "qsdm-validator.state.lock",
		ValidatorStoppedVerified: true,
	}
	for i, name := range fuzzManifestFiles {
		m.Files = append(m.Files, FileEvidence{Name: name, SizeBytes: int64(100 * (i + 1)), SHA256: strings.Repeat(string("0a5f"[i]), 64)})
	}
	return m
}

// FuzzDecodeManifest feeds the manifest reader any bytes, as a snapshot from
// an untrusted source can carry them. Whatever the input:
//   - it never panics;
//   - a manifest it accepts has schema 1, the stopped-validator flag and the
//     validator state lock, exactly the four validator files in Capture's
//     order (so StageVerified only ever copies those four names), sizes that
//     are not negative and lower-case hex SHA-256 digests;
//   - an accepted manifest reads the same with the lenient decoder
//     (json.Unmarshal), and written back it is accepted again unchanged.
func FuzzDecodeManifest(f *testing.F) {
	good, err := json.MarshalIndent(fuzzGoodManifest(), "", "  ")
	if err != nil {
		f.Fatal(err)
	}
	g := string(good) + "\n"
	if _, err := decodeManifest([]byte(g)); err != nil {
		f.Fatalf("a well-formed manifest is refused: %v", err) // not vacuous
	}
	for _, s := range []string{
		g,
		strings.Replace(g, "{", `{"extra":1,`, 1),
		strings.Replace(g, `"schema_version": 1`, `"schema_version": 2`, 1),
		strings.Replace(g, `"validator_stopped_verified": true`, `"validator_stopped_verified": false`, 1),
		strings.Replace(g, `"qsdm-validator.state.lock"`, `"other.lock"`, 1),
		strings.Replace(g, fuzzManifestFiles[0], "../qsdm_accounts.json", 1),
		strings.Replace(g, `"size_bytes": 100`, `"size_bytes": -1`, 1),
		strings.Replace(g, strings.Repeat("0", 64), strings.Repeat("A", 64), 1),
		g + "{}",
		g + "  \n\t",
		`{"schema_version":1,"validator_stopped_verified":true,"state_lock":"qsdm-validator.state.lock","files":[]}`,
		"", "null", "{}", "[]",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := decodeManifest(data)
		if err != nil {
			return
		}
		if m.SchemaVersion != 1 || !m.ValidatorStoppedVerified || m.StateLock != "qsdm-validator.state.lock" {
			t.Fatalf("accepted a manifest with schema %d, stopped %v, lock %q", m.SchemaVersion, m.ValidatorStoppedVerified, m.StateLock)
		}
		if len(m.Files) != len(fuzzManifestFiles) {
			t.Fatalf("accepted a manifest with %d files", len(m.Files))
		}
		for i, fe := range m.Files {
			if fe.Name != fuzzManifestFiles[i] || fe.SizeBytes < 0 || !fuzzLowerHex64(fe.SHA256) {
				t.Fatalf("accepted manifest entry %d: %+v", i, fe)
			}
		}
		var lenient Manifest
		if err := json.Unmarshal(data, &lenient); err != nil || !reflect.DeepEqual(lenient, m) {
			t.Fatalf("json.Unmarshal reads %+v (%v); the strict reader accepted %+v", lenient, err, m)
		}
		again, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if back, err := decodeManifest(again); err != nil || !reflect.DeepEqual(back, m) {
			t.Fatalf("written back, the manifest reads %+v (%v), want %+v", back, err, m)
		}
	})
}

// FuzzDecodeCheckpoint feeds the advisory checkpoint reader any bytes. The
// file travels with an untrusted snapshot; an import only compares it with
// the pinned checkpoint and never trusts it. Whatever the input it never
// panics, and a checkpoint it accepts round-trips: written back as
// WriteCheckpoint writes it, it reads back unchanged.
func FuzzDecodeCheckpoint(f *testing.F) {
	good, err := json.MarshalIndent(Checkpoint{Height: 7, BlockHash: strings.Repeat("a", 64), StateRoot: strings.Repeat("b", 64),
		ProducerID: strings.Repeat("c", 64), TipSigned: true, Blocks: 8, JournalBytes: 99}, "", "  ")
	if err != nil {
		f.Fatal(err)
	}
	if _, err := decodeCheckpoint(good); err != nil {
		f.Fatalf("a well-formed checkpoint is refused: %v", err) // not vacuous
	}
	for _, s := range []string{
		string(good) + "\n",
		`{"height":1,"extra":true}`,
		`{"height":-1}`,
		`{"height":18446744073709551616}`,
		`{"height":1,"blocks":1e3}`,
		`{"Height":3,"BLOCK_HASH":"x"}`,
		`{"height":1}{"height":2}`,
		"", "null", "{}", "[]",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		cp, err := decodeCheckpoint(data)
		if err != nil {
			return
		}
		out, err := json.MarshalIndent(cp, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if back, err := decodeCheckpoint(append(out, '\n')); err != nil || back != cp {
			t.Fatalf("written back, the checkpoint reads %+v (%v), want %+v", back, err, cp)
		}
	})
}
