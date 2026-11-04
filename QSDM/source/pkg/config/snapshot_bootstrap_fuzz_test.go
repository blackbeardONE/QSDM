package config

import (
	"math/big"
	"os"
	"strings"
	"testing"
)

var fuzzSnapEnvKeys = [5]string{
	"QSDM_SNAPSHOT_BOOTSTRAP", "QSDM_SNAPSHOT_CHECKPOINT_HEIGHT", "QSDM_SNAPSHOT_CHECKPOINT_BLOCK_HASH",
	"QSDM_SNAPSHOT_CHECKPOINT_STATE_ROOT", "QSDM_SNAPSHOT_CHECKPOINT_PRODUCER_ID",
}

// fuzzSnapHex64 is an independent statement of the format of the checkpoint
// hashes: 64 characters, each 0-9 or a-f.
func fuzzSnapHex64(s string) bool {
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

// fuzzSnapHeight parses a height the way the environment variable is
// documented, without strconv: decimal digits only, at most 2^64-1.
func fuzzSnapHeight(s string) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok || !n.IsUint64() {
		return 0, false
	}
	return n.Uint64(), true
}

// FuzzSnapshotBootstrapConfig feeds the [snapshot_bootstrap] keys any file
// values and any environment overrides (mask selects which variables are
// set), as an operator could write them, through toConfig,
// applySnapshotBootstrapEnv and Validate. Whatever the input:
//   - the result is each file value trimmed, replaced by its environment
//     variable when that is set to a non-blank value (trimmed; for enabled,
//     1, true, yes or on in any case mean true, anything else false);
//   - a QSDM_SNAPSHOT_CHECKPOINT_HEIGHT that is not a whole number is refused;
//   - Validate accepts exactly what the import and the boot guard can
//     enforce: off with no checkpoint key set, or on with a height of at
//     least 1, a block hash and a state root of 64 lower-case hex characters,
//     and a producer that is empty or 64 lower-case hex characters.
func FuzzSnapshotBootstrapConfig(f *testing.F) {
	h, r, p := strings.Repeat("1a", 32), strings.Repeat("2b", 32), strings.Repeat("3c", 32)
	if err := (SnapshotBootstrapConfig{Enabled: true, CheckpointHeight: 12, CheckpointBlockHash: h, CheckpointStateRoot: r, CheckpointProducerID: p}).Validate(); err != nil {
		f.Fatalf("a well-formed checkpoint is refused: %v", err) // not vacuous
	}
	f.Add(false, uint64(0), "", "", "", "", "", "", "", "", uint8(0))
	f.Add(true, uint64(12), h, r, p, "", "", "", "", "", uint8(0))
	f.Add(true, uint64(12), " "+h+"\t", r, "", "", "", "", "", "", uint8(0))
	f.Add(false, uint64(12), "", "", "", "", "", "", "", "", uint8(0))
	f.Add(false, uint64(0), "", "", p, "", "", "", "", "", uint8(0))
	f.Add(true, uint64(0), h, r, "", "", "", "", "", "", uint8(0))
	f.Add(true, uint64(3), strings.ToUpper(h), r, "", "", "", "", "", "", uint8(0))
	f.Add(true, uint64(3), h, r, "12D3KooWHT2APYkMStuaiLZvto7ZmrPmUag4TStT7MryGn69feum", "", "", "", "", "", uint8(0))
	f.Add(false, uint64(0), "", "", "", " TRUE ", "12", h, r, p, uint8(31))
	f.Add(true, uint64(5), h, r, "", "0", "", "", "", "", uint8(1))
	f.Add(true, uint64(5), h, r, "", "", "x", "", "", "", uint8(2))
	f.Add(true, uint64(5), h, r, "", "", "-1", "", "", "", uint8(2))
	f.Add(true, uint64(5), h, r, "", "", "18446744073709551616", "", "", "", uint8(2))
	f.Add(true, uint64(5), h, r, "", "", "18446744073709551615", "", "", "", uint8(2))
	f.Add(true, uint64(5), h, r, "", "yes", " 7 ", "", "  ", "", uint8(31))
	f.Fuzz(func(t *testing.T, enabled bool, height uint64, hash, root, producer,
		envEnabled, envHeight, envHash, envRoot, envProducer string, mask uint8) {
		vals := [5]string{envEnabled, envHeight, envHash, envRoot, envProducer}
		var env [5]string
		for i, k := range fuzzSnapEnvKeys {
			v := ""
			if mask&(1<<i) != 0 {
				v = vals[i]
			}
			if strings.ContainsRune(v, 0) {
				return // no environment can hold a NUL byte
			}
			t.Setenv(k, v)
			env[i] = os.Getenv(k) // what the process holds (Windows keeps UTF-16)
		}

		c := SnapshotBootstrapTOML{Enabled: enabled, CheckpointHeight: height, CheckpointBlockHash: hash,
			CheckpointStateRoot: root, CheckpointProducerID: producer}.toConfig()
		applySnapshotBootstrapEnv(&c)
		err := c.Validate()

		want := SnapshotBootstrapConfig{Enabled: enabled, CheckpointHeight: height, CheckpointBlockHash: strings.TrimSpace(hash),
			CheckpointStateRoot: strings.TrimSpace(root), CheckpointProducerID: strings.TrimSpace(producer)}
		if v := strings.TrimSpace(env[0]); v != "" {
			switch strings.ToLower(v) {
			case "1", "true", "yes", "on":
				want.Enabled = true
			default:
				want.Enabled = false
			}
		}
		if v := strings.TrimSpace(env[1]); v != "" {
			n, ok := fuzzSnapHeight(v)
			if !ok {
				if err == nil || !strings.Contains(err.Error(), "QSDM_SNAPSHOT_CHECKPOINT_HEIGHT") {
					t.Fatalf("QSDM_SNAPSHOT_CHECKPOINT_HEIGHT=%q: Validate = %v, want a refusal", v, err)
				}
				return
			}
			want.CheckpointHeight = n
		}
		for i, dst := range []*string{&want.CheckpointBlockHash, &want.CheckpointStateRoot, &want.CheckpointProducerID} {
			if v := strings.TrimSpace(env[2+i]); v != "" {
				*dst = v
			}
		}
		if c != want {
			t.Fatalf("got %+v, want %+v", c, want)
		}

		valid := want.CheckpointHeight == 0 && want.CheckpointBlockHash == "" && want.CheckpointStateRoot == "" && want.CheckpointProducerID == ""
		if want.Enabled {
			valid = want.CheckpointHeight >= 1 && fuzzSnapHex64(want.CheckpointBlockHash) && fuzzSnapHex64(want.CheckpointStateRoot) &&
				(want.CheckpointProducerID == "" || fuzzSnapHex64(want.CheckpointProducerID))
		}
		if (err == nil) != valid {
			t.Fatalf("Validate(%+v) = %v, want valid = %v", c, err, valid)
		}
	})
}
