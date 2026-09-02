package credentials

import (
	"errors"
	"testing"

	keyring "github.com/zalando/go-keyring"
)

type fakeKeyring struct {
	token     string
	getErr    error
	setErr    error
	deleteErr error
	account   string
}

func (f *fakeKeyring) Get(_, account string) (string, error) {
	f.account = account
	return f.token, f.getErr
}

func (f *fakeKeyring) Set(_, account, token string) error {
	f.account = account
	if f.setErr == nil {
		f.token = token
	}
	return f.setErr
}

func (f *fakeKeyring) Delete(_, account string) error {
	f.account = account
	if f.deleteErr == nil {
		f.token = ""
	}
	return f.deleteErr
}

type fakeLegacyStore struct {
	token    string
	loadErr  error
	saveErr  error
	clearErr error
}

func (f *fakeLegacyStore) LoadToken() (string, error) {
	return f.token, f.loadErr
}

func (f *fakeLegacyStore) SaveToken(token string) error {
	if f.saveErr == nil {
		f.token = token
	}
	return f.saveErr
}

func (f *fakeLegacyStore) ClearToken() error {
	if f.clearErr == nil {
		f.token = ""
	}
	return f.clearErr
}

func newTestStore(keyringBackend Keyring, legacy LegacyStore, env string) *Store {
	return &Store{
		keyring: keyringBackend,
		legacy:  legacy,
		getenv: func(string) string {
			return env
		},
	}
}

func TestResolveCredentialPrecedence(t *testing.T) {
	tests := []struct {
		name       string
		env        string
		keyring    *fakeKeyring
		legacy     *fakeLegacyStore
		wantToken  string
		wantSource Source
	}{
		{
			name:       "environment",
			env:        "env-token",
			keyring:    &fakeKeyring{token: "keyring-token"},
			legacy:     &fakeLegacyStore{token: "file-token"},
			wantToken:  "env-token",
			wantSource: SourceEnvironment,
		},
		{
			name:       "keyring",
			keyring:    &fakeKeyring{token: "keyring-token"},
			legacy:     &fakeLegacyStore{token: "file-token"},
			wantToken:  "keyring-token",
			wantSource: SourceKeyring,
		},
		{
			name:       "legacy file",
			keyring:    &fakeKeyring{getErr: errors.New("unavailable")},
			legacy:     &fakeLegacyStore{token: "file-token"},
			wantToken:  "file-token",
			wantSource: SourceFile,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := newTestStore(test.keyring, test.legacy, test.env)
			credential, err := store.Resolve("HTTPS://API.EXAMPLE.COM/")
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if credential.Token != test.wantToken || credential.Source != test.wantSource {
				t.Fatalf("Resolve() = %#v", credential)
			}
		})
	}
}

func TestResolveReturnsNotFound(t *testing.T) {
	store := newTestStore(
		&fakeKeyring{getErr: keyring.ErrNotFound},
		&fakeLegacyStore{},
		"",
	)
	if _, err := store.Resolve("https://api.example.com"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrNotFound", err)
	}
}

func TestSaveUsesKeyringAndClearsLegacyToken(t *testing.T) {
	keyringBackend := &fakeKeyring{}
	legacy := &fakeLegacyStore{token: "old-token"}
	store := newTestStore(keyringBackend, legacy, "")

	result, err := store.Save("HTTPS://API.EXAMPLE.COM/", "new-token")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if result.UsedFileFallback {
		t.Fatal("Save() unexpectedly used file fallback")
	}
	if keyringBackend.token != "new-token" || legacy.token != "" {
		t.Fatalf("keyring token = %q, legacy token = %q", keyringBackend.token, legacy.token)
	}
	if keyringBackend.account != "https://api.example.com" {
		t.Fatalf("keyring account = %q", keyringBackend.account)
	}
}

func TestSaveFallsBackToLegacyFile(t *testing.T) {
	keyringErr := errors.New("keyring unavailable")
	legacy := &fakeLegacyStore{}
	store := newTestStore(&fakeKeyring{setErr: keyringErr}, legacy, "")

	result, err := store.Save("https://api.example.com", "new-token")
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if !result.UsedFileFallback || !errors.Is(result.KeyringError, keyringErr) {
		t.Fatalf("Save() result = %#v", result)
	}
	if legacy.token != "new-token" {
		t.Fatalf("legacy token = %q", legacy.token)
	}
}

func TestSaveRollsBackKeyringWhenLegacyClearFails(t *testing.T) {
	keyringBackend := &fakeKeyring{}
	legacy := &fakeLegacyStore{
		token:    "old-token",
		clearErr: errors.New("read-only config"),
	}
	store := newTestStore(keyringBackend, legacy, "")

	if _, err := store.Save("https://api.example.com", "new-token"); err == nil {
		t.Fatal("Save() succeeded")
	}
	if keyringBackend.token != "" {
		t.Fatalf("keyring token after rollback = %q", keyringBackend.token)
	}
	if legacy.token != "old-token" {
		t.Fatalf("legacy token = %q", legacy.token)
	}
}

func TestDeleteClearsBothStores(t *testing.T) {
	keyringBackend := &fakeKeyring{token: "token"}
	legacy := &fakeLegacyStore{token: "token"}
	store := newTestStore(keyringBackend, legacy, "")
	if err := store.Delete("https://api.example.com", SourceKeyring); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if keyringBackend.token != "" || legacy.token != "" {
		t.Fatalf("tokens remain: keyring=%q legacy=%q", keyringBackend.token, legacy.token)
	}
}

func TestDeleteFileCredentialToleratesUnavailableKeyring(t *testing.T) {
	legacy := &fakeLegacyStore{token: "token"}
	store := newTestStore(
		&fakeKeyring{deleteErr: errors.New("keyring unavailable")},
		legacy,
		"",
	)
	if err := store.Delete("https://api.example.com", SourceFile); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if legacy.token != "" {
		t.Fatalf("legacy token = %q", legacy.token)
	}
}
