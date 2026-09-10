package chain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// MaxConsensusMembershipFileBytes bounds public membership artifacts read
// from disk. The reader also enforces this limit while reading, so replacing a
// file after its metadata was checked cannot force an unbounded allocation.
const MaxConsensusMembershipFileBytes int64 = 4 << 20

// ConsensusMembershipScheduleFileSchemaVersion identifies the JSON envelope
// used to transport a deterministic sequence of membership snapshots.
const ConsensusMembershipScheduleFileSchemaVersion uint32 = 1

// ConsensusMembershipScheduleFile is a public, operator-reviewed schedule.
// It contains no private key material and does not itself activate consensus
// membership policy in a running node.
type ConsensusMembershipScheduleFile struct {
	SchemaVersion uint32                `json:"schema_version"`
	NetworkID     string                `json:"network_id"`
	Snapshots     []ConsensusMembership `json:"snapshots"`
}

// LoadConsensusMembershipFile reads one strict JSON membership artifact from a
// regular file and validates its deterministic public identity.
func LoadConsensusMembershipFile(path string) (ConsensusMembership, error) {
	raw, err := readConsensusMembershipFile(path)
	if err != nil {
		return ConsensusMembership{}, err
	}
	var membership ConsensusMembership
	if err := decodeConsensusMembershipJSON(path, raw, &membership); err != nil {
		return ConsensusMembership{}, err
	}
	if err := membership.Validate(); err != nil {
		return ConsensusMembership{}, fmt.Errorf("validate membership %q: %w", path, err)
	}
	return membership, nil
}

// LoadConsensusMembershipScheduleFile reads a strict JSON schedule document.
// Its document network ID must exactly match every snapshot, and the returned
// schedule defensively copies all snapshots before it is returned.
func LoadConsensusMembershipScheduleFile(path string) (*ConsensusMembershipSchedule, error) {
	raw, err := readConsensusMembershipFile(path)
	if err != nil {
		return nil, err
	}
	var document ConsensusMembershipScheduleFile
	if err := decodeConsensusMembershipJSON(path, raw, &document); err != nil {
		return nil, err
	}
	if document.SchemaVersion != ConsensusMembershipScheduleFileSchemaVersion {
		return nil, fmt.Errorf("validate membership schedule %q: schema_version %d, want %d", path, document.SchemaVersion, ConsensusMembershipScheduleFileSchemaVersion)
	}
	if strings.TrimSpace(document.NetworkID) == "" || document.NetworkID != strings.TrimSpace(document.NetworkID) {
		return nil, fmt.Errorf("validate membership schedule %q: network_id must be non-empty and trimmed", path)
	}
	schedule, err := NewConsensusMembershipSchedule(document.Snapshots)
	if err != nil {
		return nil, fmt.Errorf("validate membership schedule %q: %w", path, err)
	}
	if schedule.NetworkID() != document.NetworkID {
		return nil, fmt.Errorf("validate membership schedule %q: network_id %q does not match snapshot network %q", path, document.NetworkID, schedule.NetworkID())
	}
	return schedule, nil
}

func readConsensusMembershipFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read membership %q: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("read membership %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("membership %q is not a regular file", path)
	}
	raw, err := io.ReadAll(io.LimitReader(file, MaxConsensusMembershipFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read membership %q: %w", path, err)
	}
	if int64(len(raw)) > MaxConsensusMembershipFileBytes {
		return nil, fmt.Errorf("membership %q exceeds %d bytes", path, MaxConsensusMembershipFileBytes)
	}
	return raw, nil
}

func decodeConsensusMembershipJSON(path string, raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("parse membership %q: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("parse membership %q: multiple JSON values", path)
		}
		return fmt.Errorf("parse membership %q: %w", path, err)
	}
	return nil
}
