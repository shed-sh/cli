package credentials

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	keyring "github.com/zalando/go-keyring"
)

var ErrNotFound = errors.New("no Shed credentials found")

type Source string

const (
	SourceEnvironment Source = "environment"
	SourceKeyring     Source = "keyring"
	SourceFile        Source = "file"
)

type Credential struct {
	Token  string
	Source Source
}

type SaveResult struct {
	UsedFileFallback bool
	KeyringError     error
}

type Keyring interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

type LegacyStore interface {
	LoadToken() (string, error)
	SaveToken(string) error
	ClearToken() error
}

type Store struct {
	keyring Keyring
	legacy  LegacyStore
	getenv  func(string) string
}

func New(legacy LegacyStore) *Store {
	return &Store{
		keyring: systemKeyring{},
		legacy:  legacy,
		getenv:  os.Getenv,
	}
}

func (s *Store) Resolve(apiURL string) (Credential, error) {
	if token := strings.TrimSpace(s.getenv("SHED_TOKEN")); token != "" {
		return Credential{Token: token, Source: SourceEnvironment}, nil
	}

	account := accountName(apiURL)
	token, keyringErr := s.keyring.Get("shed", account)
	if keyringErr == nil && token != "" {
		return Credential{Token: token, Source: SourceKeyring}, nil
	}

	legacyToken, legacyErr := s.legacy.LoadToken()
	if legacyErr != nil {
		return Credential{}, legacyErr
	}
	if legacyToken != "" {
		return Credential{Token: legacyToken, Source: SourceFile}, nil
	}
	return Credential{}, ErrNotFound
}

func (s *Store) Save(apiURL, token string) (SaveResult, error) {
	account := accountName(apiURL)
	if err := s.keyring.Set("shed", account, token); err == nil {
		if err := s.legacy.ClearToken(); err != nil {
			rollbackErr := s.keyring.Delete("shed", account)
			return SaveResult{}, errors.Join(
				fmt.Errorf("clear plaintext credential after keyring save: %w", err),
				wrapIfError("roll back keyring credential", rollbackErr),
			)
		}
		return SaveResult{}, nil
	} else {
		if fileErr := s.legacy.SaveToken(token); fileErr != nil {
			return SaveResult{}, errors.Join(
				fmt.Errorf("save credential to system keyring: %w", err),
				fmt.Errorf("save credential fallback: %w", fileErr),
			)
		}
		return SaveResult{UsedFileFallback: true, KeyringError: err}, nil
	}
}

func wrapIfError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

func (s *Store) Delete(apiURL string, source Source) error {
	account := accountName(apiURL)
	keyringErr := s.keyring.Delete("shed", account)
	if errors.Is(keyringErr, keyring.ErrNotFound) || source == SourceFile {
		keyringErr = nil
	}
	fileErr := s.legacy.ClearToken()
	if keyringErr != nil || fileErr != nil {
		return errors.Join(keyringErr, fileErr)
	}
	return nil
}

func accountName(apiURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(apiURL), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return trimmed
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String()
}

type systemKeyring struct{}

func (systemKeyring) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (systemKeyring) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (systemKeyring) Delete(service, account string) error {
	return keyring.Delete(service, account)
}
