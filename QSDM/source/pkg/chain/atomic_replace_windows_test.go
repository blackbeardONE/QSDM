//go:build windows

package chain

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestAccountStoreSaveFallsBackWhenDestinationDeniesDeleteSharing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store := NewAccountStore()
	store.Credit("alice", 1)
	if err := store.Save(path); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	defer windows.CloseHandle(handle)

	store.Credit("bob", 2)
	for i := 0; i < 5; i++ {
		store.Credit("bob", 1)
		if err := store.Save(path); err != nil {
			t.Fatalf("fallback Save %d: %v", i, err)
		}
	}

	restored := NewAccountStore()
	if _, err := restored.Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if bob, ok := restored.Get("bob"); !ok || bob.Balance != 7 {
		t.Fatalf("fallback snapshot missing bob: %+v, ok=%v", bob, ok)
	}
	if randomTemps, err := filepath.Glob(path + ".tmp-*"); err != nil || len(randomTemps) != 0 {
		t.Fatalf("random snapshot temps accumulated: %v, err=%v", randomTemps, err)
	}
	if pending, err := filepath.Glob(path + ".pending"); err != nil || len(pending) > 1 {
		t.Fatalf("expected at most one reusable pending snapshot: %v, err=%v", pending, err)
	}
}
