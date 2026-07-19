package main

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	persistenceReserveEnv          = "QSDM_MIN_PERSISTENCE_FREE_BYTES"
	defaultPersistenceReserveBytes = uint64(2 << 30)
	minimumPersistenceReserveBytes = uint64(256 << 20)
)

type diskSpaceProbe func(string) (uint64, error)

func parsePersistenceReserve(raw string) (uint64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultPersistenceReserveBytes, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an unsigned byte count: %w", persistenceReserveEnv, err)
	}
	if value < minimumPersistenceReserveBytes {
		return 0, fmt.Errorf("%s must be at least %d bytes", persistenceReserveEnv, minimumPersistenceReserveBytes)
	}
	return value, nil
}

func checkPersistenceCapacity(path string, minimum uint64, probe diskSpaceProbe) (uint64, error) {
	if probe == nil {
		return 0, fmt.Errorf("persistence disk-space probe is not configured")
	}
	available, err := probe(path)
	if err != nil {
		return 0, fmt.Errorf("inspect persistence free space at %s: %w", path, err)
	}
	if available < minimum {
		return available, fmt.Errorf(
			"persistence disk reserve breached at %s: available_bytes=%d minimum_bytes=%d",
			path, available, minimum)
	}
	return available, nil
}
