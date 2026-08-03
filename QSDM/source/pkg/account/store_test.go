package account

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testDataKey() []byte {
	return []byte("0123456789abcdef0123456789abcdef")
}

func TestStoreLinksAlternateIdentitiesWithoutMergingAccounts(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "accounts.json"), testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.FindOrCreateTelegram("telegram-target", "@target")
	if err != nil {
		t.Fatal(err)
	}
	targetWallet := strings.Repeat("b", 64)
	if err := store.LinkWallet(target.ID, targetWallet); err != nil {
		t.Fatal(err)
	}
	claimedToken, err := store.CreateMagicLink("claimed@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ConsumeMagicLink(claimedToken)
	if err != nil {
		t.Fatal(err)
	}

	linkToken, err := store.CreateIdentityMagicLink(target.ID, "target@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := store.ConsumeMagicLink(linkToken)
	if err != nil {
		t.Fatal(err)
	}
	if linked.ID != target.ID || linked.TelegramSubjectHash == "" || linked.EmailHash == "" || len(linked.Wallets) != 1 || linked.Wallets[0].Address != targetWallet {
		t.Fatalf("alternate email created or replaced an account: %#v", linked)
	}

	conflictTarget, err := store.FindOrCreateTelegram("telegram-conflict-target", "@conflict")
	if err != nil {
		t.Fatal(err)
	}
	conflictToken, err := store.CreateIdentityMagicLink(conflictTarget.ID, "target@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeMagicLink(conflictToken); !errors.Is(err, ErrIdentityInUse) {
		t.Fatalf("email identity conflict was not rejected: %v", err)
	}
	if _, err := store.LinkTelegramIdentity(claimed.ID, "telegram-target", "@target"); !errors.Is(err, ErrIdentityInUse) {
		t.Fatalf("Telegram identity conflict was not rejected: %v", err)
	}
	updated, _, err := store.AccountForSession(mustCreateSession(t, store, claimed.ID))
	if err != nil {
		t.Fatal(err)
	}
	if updated.TelegramSubjectHash != "" {
		t.Fatal("conflicting Telegram identity was attached to the wrong account")
	}
}

func mustCreateSession(t *testing.T, store *Store, accountID string) string {
	t.Helper()
	token, err := store.CreateSession(accountID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestStoreMagicLinkPersistsEncryptedIdentityAndIsOneTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := OpenStore(path, testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.CreateMagicLink("person@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "person@example.com") || strings.Contains(string(raw), token) {
		t.Fatal("account store exposed an email address or raw magic-link token")
	}

	reopened, err := OpenStore(path, testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	account, err := reopened.ConsumeMagicLink(token)
	if err != nil {
		t.Fatal(err)
	}
	masked, _, err := reopened.IdentityView(account)
	if err != nil {
		t.Fatal(err)
	}
	if masked != "p****n@example.com" {
		t.Fatalf("unexpected masked email %q", masked)
	}
	if _, err := reopened.ConsumeMagicLink(token); err == nil {
		t.Fatal("one-time magic link was accepted twice")
	}
}

func TestStoreWalletCannotBeClaimedByTwoAccounts(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "accounts.json"), testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	oneToken, _ := store.CreateMagicLink("one@example.com", time.Minute)
	one, err := store.ConsumeMagicLink(oneToken)
	if err != nil {
		t.Fatal(err)
	}
	twoToken, _ := store.CreateMagicLink("two@example.com", time.Minute)
	two, err := store.ConsumeMagicLink(twoToken)
	if err != nil {
		t.Fatal(err)
	}
	address := strings.Repeat("a", 64)
	if err := store.LinkWallet(one.ID, address); err != nil {
		t.Fatal(err)
	}
	if err := store.LinkWallet(two.ID, address); err == nil {
		t.Fatal("same wallet was linked to a second account")
	}
	unlinked, err := store.UnlinkWallet(one.ID, address)
	if err != nil || !unlinked {
		t.Fatalf("wallet unlink failed: unlinked=%v err=%v", unlinked, err)
	}
	if err := store.LinkWallet(two.ID, address); err != nil {
		t.Fatalf("released wallet could not be linked to another account: %v", err)
	}
	unlinked, err = store.UnlinkWallet(one.ID, address)
	if err != nil || unlinked {
		t.Fatalf("unlinking a wallet from the wrong account was not idempotent: unlinked=%v err=%v", unlinked, err)
	}
}
