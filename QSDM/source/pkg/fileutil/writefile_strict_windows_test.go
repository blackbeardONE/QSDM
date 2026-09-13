//go:build windows

package fileutil

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWriteFileAtomicStrictRetriesAccessDeniedWithoutFallback(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		name := "transient"
		if permanent {
			name = "permanent"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
				t.Fatal(err)
			}
			replace := replaceFileForWrite
			t.Cleanup(func() { replaceFileForWrite = replace })
			attempts := 0
			replaceFileForWrite = func(src, dst string) error {
				attempts++
				raw, err := os.ReadFile(dst)
				if err != nil || string(raw) != "old" {
					t.Fatalf("destination changed before replacement: %q %v", raw, err)
				}
				if permanent || attempts < 3 {
					return windows.ERROR_ACCESS_DENIED
				}
				return replace(src, dst)
			}
			err := WriteFileAtomicStrict(path, []byte("new"), 0o600)
			want := "new"
			if permanent {
				want = "old"
				if !errors.Is(err, windows.ERROR_ACCESS_DENIED) || attempts != 8 {
					t.Fatalf("permanent denial: attempts=%d err=%v", attempts, err)
				}
			} else if err != nil || attempts < 3 {
				t.Fatalf("transient denial: attempts=%d err=%v", attempts, err)
			}
			raw, err := os.ReadFile(path)
			if err != nil || string(raw) != want {
				t.Fatalf("destination=%q want=%q err=%v", raw, want, err)
			}
			if _, cached := directWriteDirectories.Load(filepath.Dir(path)); cached {
				t.Fatal("strict write enabled direct-overwrite fallback")
			}
		})
	}
}
