package hl1tail

// watermark.go: the served watermark W (design rev 4 §3.1). Reading and
// validation match cmd/qsdm (hl1ReadWatermark, S5 rule 2) exactly; the
// cmd/qsdm tests cross-check both parsers.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/blackbeardONE/QSDM/internal/legacymining"
)

// maxWatermarkBytes bounds the watermark file (cmd/qsdm hl1MaxWatermarkBytes).
const maxWatermarkBytes = 4096

// ErrWatermarkMissing reports that the state directory has no W.
var ErrWatermarkMissing = errors.New("watermark missing")

// ReadWatermark reads and validates <dir>/hl1-served-watermark.json. An absent
// file returns ErrWatermarkMissing.
func ReadWatermark(dir string) (legacymining.Watermark, error) {
	p := filepath.Join(dir, legacymining.WatermarkFile)
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return legacymining.Watermark{}, ErrWatermarkMissing
	}
	if err != nil {
		return legacymining.Watermark{}, fmt.Errorf("watermark unreadable: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return legacymining.Watermark{}, fmt.Errorf("watermark %s is not a regular file", p)
	}
	if fi.Size() > maxWatermarkBytes {
		return legacymining.Watermark{}, fmt.Errorf("watermark %s is %d bytes", p, fi.Size())
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return legacymining.Watermark{}, fmt.Errorf("watermark unreadable: %w", err)
	}
	return ParseWatermark(data)
}

// ParseWatermark decodes W strictly (unknown fields and trailing data are
// rejected) and validates the version, the hash and the source.
func ParseWatermark(data []byte) (legacymining.Watermark, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var w legacymining.Watermark
	if err := dec.Decode(&w); err != nil {
		return legacymining.Watermark{}, fmt.Errorf("watermark invalid: %w", err)
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return legacymining.Watermark{}, errors.New("watermark invalid: trailing data")
	}
	if w.Version != legacymining.WatermarkVersion {
		return legacymining.Watermark{}, fmt.Errorf("watermark invalid: version %d", w.Version)
	}
	if !isLowerHex(w.Hash, 64) {
		return legacymining.Watermark{}, fmt.Errorf("watermark invalid: hash %q", w.Hash)
	}
	switch w.Source {
	case legacymining.WatermarkSourceSeal, legacymining.WatermarkSourceBoot,
		legacymining.WatermarkSourceReplay, legacymining.WatermarkSourceSeed:
	default:
		return legacymining.Watermark{}, fmt.Errorf("watermark invalid: source %q", w.Source)
	}
	return w, nil
}

// writeWatermark D1-writes W into dir, byte-compatible with cmd/qsdm
// hl1WriteWatermark: json.Marshal(w) + "\n".
func writeWatermark(dir string, w legacymining.Watermark) error {
	data, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("encode watermark: %w", err)
	}
	if err := legacymining.WriteFileDurable(dir, legacymining.WatermarkFile, append(data, '\n')); err != nil {
		return fmt.Errorf("write watermark %d: %w", w.Height, err)
	}
	return nil
}

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
