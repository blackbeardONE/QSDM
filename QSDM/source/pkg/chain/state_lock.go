package chain

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// StateLock holds an operating-system lock for one validator state directory.
// The lock is released automatically by the OS if the process crashes.
type StateLock struct {
	file *os.File
	path string
}

// AcquireStateLock prevents two validator processes from mutating the same
// chain journal and account snapshot concurrently.
func AcquireStateLock(path string) (*StateLock, error) {
	if path == "" {
		return nil, errors.New("chain.AcquireStateLock: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("chain.AcquireStateLock: create parent: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("chain.AcquireStateLock: open %s: %w", path, err)
	}
	if err := lockStateFile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("chain.AcquireStateLock: state directory is already in use (%s): %w", path, err)
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.Seek(0, 0)
		_, _ = f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
		_ = f.Sync()
	}
	return &StateLock{file: f, path: path}, nil
}

// Close releases the state-directory lock.
func (l *StateLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := unlockStateFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if unlockErr != nil {
		return fmt.Errorf("chain.StateLock.Close: unlock %s: %w", l.path, unlockErr)
	}
	return closeErr
}
