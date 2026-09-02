package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenStorePersistsOwnerOnlyAndIgnoresEnvironmentOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHED_TOKEN", "environment-token")

	store := TokenStore{}
	if err := store.SaveToken("file-token"); err != nil {
		t.Fatalf("SaveToken() error = %v", err)
	}
	token, err := store.LoadToken()
	if err != nil {
		t.Fatalf("LoadToken() error = %v", err)
	}
	if token != "file-token" {
		t.Fatalf("LoadToken() = %q", token)
	}

	path, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("config directory permissions = %o, want 700", dirInfo.Mode().Perm())
	}

	if err := store.ClearToken(); err != nil {
		t.Fatalf("ClearToken() error = %v", err)
	}
	token, err = store.LoadToken()
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		t.Fatalf("token after ClearToken() = %q", token)
	}
}

func TestLoadAppliesOnlyAPIEnvironmentOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHED_API_URL", "https://api.example.com")
	t.Setenv("SHED_TOKEN", "environment-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.APIURL != "https://api.example.com" {
		t.Fatalf("APIURL = %q", cfg.APIURL)
	}
	if cfg.Token != "" {
		t.Fatalf("Load() persisted environment token as %q", cfg.Token)
	}
}
