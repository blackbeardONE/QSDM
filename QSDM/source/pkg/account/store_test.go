package account

import (
	"encoding/json"
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

func TestStoreRejectsWrongDataKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := OpenStore(path, testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindOrCreateTelegram("telegram-subject", "@operator"); err != nil {
		t.Fatal(err)
	}
	wrongKey := []byte("fedcba9876543210fedcba9876543210")
	if _, err := OpenStore(path, wrongKey); err == nil {
		t.Fatal("encrypted account store opened with the wrong data key")
	}
}

func TestStoreUpgradesLegacyDocumentAfterKeyValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := OpenStore(path, testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.FindOrCreateTelegram("legacy-subject", "@legacy")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy storeDocument
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Version = legacyStoreVersion
	legacy.KeyCheck = ""
	raw, err = json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenStore(path, []byte("fedcba9876543210fedcba9876543210")); err == nil {
		t.Fatal("legacy account store opened with the wrong data key")
	}
	reopened, err := OpenStore(path, testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.CreateSession(account.ID, time.Minute); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var upgraded storeDocument
	if err := json.Unmarshal(raw, &upgraded); err != nil {
		t.Fatal(err)
	}
	if upgraded.Version != storeVersion || upgraded.KeyCheck == "" {
		t.Fatalf("legacy account store was not upgraded: version=%d keyCheck=%t", upgraded.Version, upgraded.KeyCheck != "")
	}
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

func TestStoreKeepsOnlyTheLatestMagicLinkForEachFlow(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "accounts.json"), testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	oldLogin, err := store.CreateMagicLink("person@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	newLogin, err := store.CreateMagicLink("person@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeMagicLink(oldLogin); err == nil {
		t.Fatal("superseded sign-in link remained valid")
	}
	if _, err := store.ConsumeMagicLink(newLogin); err != nil {
		t.Fatalf("latest sign-in link was rejected: %v", err)
	}

	account, err := store.FindOrCreateTelegram("telegram-subject", "@operator")
	if err != nil {
		t.Fatal(err)
	}
	oldIdentity, err := store.CreateIdentityMagicLink(account.ID, "old@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	newIdentity, err := store.CreateIdentityMagicLink(account.ID, "new@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeMagicLink(oldIdentity); err == nil {
		t.Fatal("superseded identity-link email remained valid")
	}
	linked, err := store.ConsumeMagicLink(newIdentity)
	if err != nil {
		t.Fatalf("latest identity-link email was rejected: %v", err)
	}
	masked, _, err := store.IdentityView(linked)
	if err != nil {
		t.Fatal(err)
	}
	if masked != "n*w@example.com" {
		t.Fatalf("latest email identity was not linked: %q", masked)
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

func TestStoreListsAndRevokesOtherSessionsOnly(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "accounts.json"), testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	account := mustCreateEmailAccount(t, store, "person@example.com")
	otherAccount := mustCreateEmailAccount(t, store, "other@example.com")
	current := mustCreateSession(t, store, account.ID)
	other := mustCreateSession(t, store, account.ID)
	unrelated := mustCreateSession(t, store, otherAccount.ID)

	views, err := store.SessionsForAccount(account.ID, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || !views[0].Current || views[1].Current {
		t.Fatalf("session listing exposed an invalid current-session view: %#v", views)
	}
	revoked, err := store.RevokeOtherSessions(account.ID, current)
	if err != nil || revoked != 1 {
		t.Fatalf("other session revocation failed: revoked=%d err=%v", revoked, err)
	}
	if _, _, err := store.AccountForSession(current); err != nil {
		t.Fatalf("current session was revoked: %v", err)
	}
	if _, _, err := store.AccountForSession(other); err == nil {
		t.Fatal("other browser session remained active")
	}
	if _, _, err := store.AccountForSession(unrelated); err != nil {
		t.Fatalf("unrelated account session was revoked: %v", err)
	}
}

func TestStoreCapsActiveSessionsPerAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.json")
	store, err := OpenStore(path, testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	account := mustCreateEmailAccount(t, store, "person@example.com")
	otherAccount := mustCreateEmailAccount(t, store, "other@example.com")
	unrelated := mustCreateSession(t, store, otherAccount.ID)
	tokens := make([]string, 0, maxActiveSessionsPerAccount+1)
	for i := 0; i < maxActiveSessionsPerAccount+1; i++ {
		tokens = append(tokens, mustCreateSession(t, store, account.ID))
		time.Sleep(time.Millisecond)
	}

	views, err := store.SessionsForAccount(account.ID, tokens[len(tokens)-1])
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != maxActiveSessionsPerAccount {
		t.Fatalf("active session count = %d, want %d", len(views), maxActiveSessionsPerAccount)
	}
	if _, _, err := store.AccountForSession(tokens[0]); err == nil {
		t.Fatal("oldest session remained active after the per-account cap")
	}
	for _, token := range tokens[1:] {
		if _, _, err := store.AccountForSession(token); err != nil {
			t.Fatalf("newer session was unexpectedly pruned: %v", err)
		}
	}
	if _, _, err := store.AccountForSession(unrelated); err != nil {
		t.Fatalf("another account's session was pruned: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var legacyOverflow storeDocument
	if err := json.Unmarshal(raw, &legacyOverflow); err != nil {
		t.Fatal(err)
	}
	overflowHash := keyedHash(testDataKey(), "session", "legacy-overflow")
	now := time.Now().UTC()
	legacyOverflow.Sessions = append(legacyOverflow.Sessions, sessionRecord{
		TokenHash: overflowHash,
		AccountID: account.ID,
		CreatedAt: now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
	})
	raw, err = json.MarshalIndent(legacyOverflow, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenStore(path, testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	reopened.mu.RLock()
	_, overflowActive := reopened.sessions[overflowHash]
	activeCount := 0
	for _, record := range reopened.sessions {
		if record.AccountID == account.ID {
			activeCount++
		}
	}
	reopened.mu.RUnlock()
	if overflowActive || activeCount != maxActiveSessionsPerAccount {
		t.Fatalf("startup did not prune legacy session overflow: active=%d overflow=%t", activeCount, overflowActive)
	}
}

func TestStoreDeleteAccountRemovesSessionsAndPendingLinks(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "accounts.json"), testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	account := mustCreateEmailAccount(t, store, "delete@example.com")
	session := mustCreateSession(t, store, account.ID)
	pendingLogin, err := store.CreateMagicLink("delete@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.LinkWallet(account.ID, strings.Repeat("d", 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(account.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.AccountForSession(session); err == nil {
		t.Fatal("deleted account session remained active")
	}
	if _, err := store.ConsumeMagicLink(pendingLogin); err == nil {
		t.Fatal("pending same-email login survived account deletion")
	}

	telegramAccount, err := store.FindOrCreateTelegram("delete-telegram", "@delete")
	if err != nil {
		t.Fatal(err)
	}
	pendingIdentity, err := store.CreateIdentityMagicLink(telegramAccount.ID, "pending@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(telegramAccount.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConsumeMagicLink(pendingIdentity); err == nil {
		t.Fatal("account-bound identity link survived account deletion")
	}
}

func TestStoreDestructiveChangesRollbackWhenPersistenceFails(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "accounts.json"), testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	account := mustCreateEmailAccount(t, store, "rollback@example.com")
	current := mustCreateSession(t, store, account.ID)
	other := mustCreateSession(t, store, account.ID)
	originalPath := store.path
	store.path = filepath.Join(t.TempDir(), "missing", "accounts.json")
	if _, err := store.RevokeOtherSessions(account.ID, current); err == nil {
		t.Fatal("session revocation unexpectedly persisted to an invalid path")
	}
	if _, _, err := store.AccountForSession(other); err != nil {
		t.Fatalf("failed session revocation was not rolled back: %v", err)
	}
	if err := store.DeleteAccount(account.ID); err == nil {
		t.Fatal("account deletion unexpectedly persisted to an invalid path")
	}
	if _, _, err := store.AccountForSession(current); err != nil {
		t.Fatalf("failed account deletion was not rolled back: %v", err)
	}
	store.path = originalPath
}

func TestStoreCredentialPruningRollsBackWhenPersistenceFails(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "accounts.json"), testDataKey())
	if err != nil {
		t.Fatal(err)
	}
	account := mustCreateEmailAccount(t, store, "rollback@example.com")
	tokens := make([]string, 0, maxActiveSessionsPerAccount)
	for i := 0; i < maxActiveSessionsPerAccount; i++ {
		tokens = append(tokens, mustCreateSession(t, store, account.ID))
		time.Sleep(time.Millisecond)
	}
	oldLink, err := store.CreateMagicLink("pending@example.com", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	originalPath := store.path
	store.path = filepath.Join(t.TempDir(), "missing", "accounts.json")
	if _, err := store.CreateSession(account.ID, time.Minute); err == nil {
		t.Fatal("session cap unexpectedly persisted to an invalid path")
	}
	if _, err := store.CreateMagicLink("pending@example.com", time.Minute); err == nil {
		t.Fatal("magic-link replacement unexpectedly persisted to an invalid path")
	}
	store.path = originalPath

	for _, token := range tokens {
		if _, _, err := store.AccountForSession(token); err != nil {
			t.Fatalf("failed session pruning was not rolled back: %v", err)
		}
	}
	if _, err := store.ConsumeMagicLink(oldLink); err != nil {
		t.Fatalf("failed magic-link replacement was not rolled back: %v", err)
	}
}

func TestStoreRejectsDuplicateAndOrphanedRecords(t *testing.T) {
	tests := []struct {
		name   string
		want   string
		mutate func(*storeDocument, map[string]int)
	}{
		{
			name: "duplicate account ID",
			want: "duplicate account ID",
			mutate: func(doc *storeDocument, indexes map[string]int) {
				doc.Accounts = append(doc.Accounts, doc.Accounts[indexes["first"]])
			},
		},
		{
			name: "duplicate email identity",
			want: "duplicate email identity",
			mutate: func(doc *storeDocument, indexes map[string]int) {
				first := doc.Accounts[indexes["first"]]
				doc.Accounts[indexes["second"]].EmailHash = first.EmailHash
				doc.Accounts[indexes["second"]].EmailEncrypted = first.EmailEncrypted
			},
		},
		{
			name: "duplicate Telegram identity",
			want: "duplicate Telegram identity",
			mutate: func(doc *storeDocument, indexes map[string]int) {
				telegram := doc.Accounts[indexes["telegram"]]
				doc.Accounts[indexes["second"]].TelegramSubjectHash = telegram.TelegramSubjectHash
				doc.Accounts[indexes["second"]].TelegramNameEncrypted = telegram.TelegramNameEncrypted
			},
		},
		{
			name: "wallet linked twice",
			want: "wallet linked to multiple accounts",
			mutate: func(doc *storeDocument, indexes map[string]int) {
				doc.Accounts[indexes["second"]].Wallets = append(
					doc.Accounts[indexes["second"]].Wallets,
					doc.Accounts[indexes["first"]].Wallets[0],
				)
			},
		},
		{
			name: "orphaned session",
			want: "orphaned session",
			mutate: func(doc *storeDocument, _ map[string]int) {
				doc.Sessions[0].AccountID = "acct_missing"
			},
		},
		{
			name: "orphaned identity link",
			want: "orphaned identity link",
			mutate: func(doc *storeDocument, _ map[string]int) {
				doc.MagicLinks[0].AccountID = "acct_missing"
			},
		},
		{
			name: "duplicate session",
			want: "duplicate session",
			mutate: func(doc *storeDocument, _ map[string]int) {
				doc.Sessions = append(doc.Sessions, doc.Sessions[0])
			},
		},
		{
			name: "duplicate magic link",
			want: "duplicate magic link",
			mutate: func(doc *storeDocument, _ map[string]int) {
				doc.MagicLinks = append(doc.MagicLinks, doc.MagicLinks[0])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "accounts.json")
			store, err := OpenStore(path, testDataKey())
			if err != nil {
				t.Fatal(err)
			}
			first := mustCreateEmailAccount(t, store, "first@example.com")
			second := mustCreateEmailAccount(t, store, "second@example.com")
			telegram, err := store.FindOrCreateTelegram("pending-telegram", "@pending")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.LinkWallet(first.ID, strings.Repeat("a", 64)); err != nil {
				t.Fatal(err)
			}
			mustCreateSession(t, store, first.ID)
			if _, err := store.CreateIdentityMagicLink(telegram.ID, "pending@example.com", time.Minute); err != nil {
				t.Fatal(err)
			}

			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc storeDocument
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			indexes := make(map[string]int)
			for i, account := range doc.Accounts {
				switch account.ID {
				case first.ID:
					indexes["first"] = i
				case second.ID:
					indexes["second"] = i
				case telegram.ID:
					indexes["telegram"] = i
				}
			}
			tt.mutate(&doc, indexes)
			raw, err = json.MarshalIndent(doc, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenStore(path, testDataKey()); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("OpenStore() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func mustCreateEmailAccount(t *testing.T, store *Store, email string) *Account {
	t.Helper()
	token, err := store.CreateMagicLink(email, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	account, err := store.ConsumeMagicLink(token)
	if err != nil {
		t.Fatal(err)
	}
	return account
}
