package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// APIURLEnv overrides the control-plane address for one process. It is named
// here so that the CLI, the API client and their diagnostics all point at the
// same knob instead of each spelling it out.
const APIURLEnv = "SHED_API_URL"

// DefaultAPIURL is the hosted control plane. Someone who installed a release
// should never have to configure this, so it is the shipped address rather
// than a development one.
const DefaultAPIURL = "https://bench.shed.codes"

// PortalURLEnv overrides the portal address, which is where shed login sends
// the browser.
const PortalURLEnv = "SHED_PORTAL_URL"

// DefaultPortalURL is the hosted portal, and pairs with DefaultAPIURL.
//
// The portal address is configuration rather than something the control plane
// hands back. Asking the API where to send the browser would mean no browser
// could open until the API answered, which is the round trip the login
// ceremony exists to remove.
//
// Working against a local stack means setting both, which is what the
// repository's own development instructions do.
const DefaultPortalURL = "https://console.shed.codes"

type Config struct {
	APIURL    string `json:"apiUrl,omitempty"`
	PortalURL string `json:"portalUrl,omitempty"`
	Token     string `json:"token,omitempty"`
}

type TokenStore struct{}

func Load() (Config, error) {
	cfg, err := loadFile()
	if err != nil {
		return cfg, err
	}

	if value := os.Getenv(APIURLEnv); value != "" {
		cfg.APIURL = value
	}
	if cfg.APIURL == "" {
		cfg.APIURL = DefaultAPIURL
	}

	if value := os.Getenv(PortalURLEnv); value != "" {
		cfg.PortalURL = value
	}
	if cfg.PortalURL == "" {
		cfg.PortalURL = DefaultPortalURL
	}

	return cfg, nil
}

func (TokenStore) LoadToken() (string, error) {
	cfg, err := loadFile()
	if err != nil {
		return "", err
	}
	return cfg.Token, nil
}

func (TokenStore) SaveToken(token string) error {
	return updateToken(token)
}

func (TokenStore) ClearToken() error {
	return updateToken("")
}

func updateToken(token string) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create Shed configuration directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("protect Shed configuration directory: %w", err)
	}

	cfg, err := loadFile()
	if err != nil {
		return err
	}
	cfg.Token = token
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Shed configuration: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("save Shed configuration: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect Shed configuration: %w", err)
	}
	return nil
}

func loadFile() (Config, error) {
	cfg := Config{}
	path, err := configPath()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read Shed configuration: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse Shed configuration: %w", err)
	}
	return cfg, nil
}

// Path is where the CLI reads and writes its configuration. It is exported so
// that a diagnostic can name the file a setting came from instead of leaving
// someone to guess which of the three sources decided it.
func Path() (string, error) {
	return configPath()
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user configuration directory: %w", err)
	}
	return filepath.Join(dir, "shed", "config.json"), nil
}
