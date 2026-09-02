package cli

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/hkdf"

	"shed/internal/api"
	"shed/internal/auth"
	"shed/internal/credentials"
)

type fakeCredentialStore struct {
	credential credentials.Credential
	resolveErr error
	savedToken string
	saveResult credentials.SaveResult
	saveErr    error
	deleted    bool
	deleteErr  error
}

func (f *fakeCredentialStore) Resolve(string) (credentials.Credential, error) {
	return f.credential, f.resolveErr
}

func (f *fakeCredentialStore) Save(_ string, token string) (credentials.SaveResult, error) {
	f.savedToken = token
	return f.saveResult, f.saveErr
}

func (f *fakeCredentialStore) Delete(string, credentials.Source) error {
	f.deleted = true
	return f.deleteErr
}

// syncBuffer is stdout that the fake control plane can also read.
//
// Under protocol 2 the client public key never reaches the API: it travels to
// the approval page in the authorization URL. A fake control plane therefore
// has to learn it the way a browser would -- by reading the link the CLI
// printed -- and that read happens on the server goroutine.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// authorization pulls the session id and public key out of the printed link.
func (b *syncBuffer) authorization(t *testing.T) (sessionID, publicKey string) {
	t.Helper()
	for _, line := range strings.Split(b.String(), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "/cli/login?") {
			continue
		}
		parsed, err := url.Parse(line)
		if err != nil {
			t.Fatalf("parse authorization URL %q: %v", line, err)
		}
		query := parsed.Query()
		return query.Get("session"), query.Get("key")
	}
	t.Fatalf("no authorization URL printed; stdout = %q", b.String())
	return "", ""
}

func TestLoginCompletesManualEncryptedExchange(t *testing.T) {
	var receivedCode string
	var createCalls int
	token := testShedToken(7)
	stdout := &syncBuffer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID, clientPublicKey := stdout.authorization(t)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/cli/auth/sessions":
			// Protocol 2 never creates a session through the API. Counting the
			// call rather than serving it is what makes the regression visible.
			createCalls++
			http.NotFound(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/cli/auth/sessions/"+sessionID+"/exchange":
			var request map[string]string
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				return
			}
			receivedCode = request["verificationCode"]
			envelope := encryptCLIEnvelope(t, clientPublicKey, sessionID, token)
			envelope.User = auth.User{ID: "user_123", Email: "alice@example.com"}
			writeTestJSON(t, w, http.StatusOK, envelope)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("SHED_API_URL", server.URL)
	t.Setenv("SHED_PORTAL_URL", "https://app.shed.dev")
	t.Setenv("SHED_TOKEN", "")

	var stderr bytes.Buffer
	store := &fakeCredentialStore{saveResult: credentials.SaveResult{
		UsedFileFallback: true,
		KeyringError:     errors.New("keyring unavailable"),
	}}
	app := New(stdout, &stderr, "test")
	app.stdin = strings.NewReader("  7k9m-2x4q-wd  \n")
	app.credentials = store
	browserCalled := false
	app.openBrowser = func(string) error {
		browserCalled = true
		return nil
	}

	if code := app.Run(context.Background(), []string{"login", "--no-browser", "--name", "test-workstation"}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if browserCalled {
		t.Fatal("login --no-browser opened a browser")
	}
	if createCalls != 0 {
		t.Fatalf("login called the create-session endpoint %d times", createCalls)
	}
	// The link is built locally, so it carries the token name the flags asked
	// for rather than whatever a server chose to echo.
	if !strings.Contains(stdout.String(), "name=test-workstation") {
		t.Fatalf("authorization URL lost the token name; stdout = %q", stdout.String())
	}
	if receivedCode != "7K9M-2X4Q-WD" {
		t.Fatalf("verification code = %q", receivedCode)
	}
	if store.savedToken != token {
		t.Fatalf("saved token = %q", store.savedToken)
	}
	if !strings.Contains(stdout.String(), "Logged in as alice@example.com.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Fatal("login printed the access token")
	}
	if !strings.Contains(stderr.String(), "owner-only config file") {
		t.Fatalf("stderr = %q, want file fallback warning", stderr.String())
	}
}

func TestLoginContinuesWhenBrowserCannotOpen(t *testing.T) {
	token := testShedToken(8)
	stdout := &syncBuffer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID, clientPublicKey := stdout.authorization(t)
		if r.URL.Path != "/v1/cli/auth/sessions/"+sessionID+"/exchange" {
			http.NotFound(w, r)
			return
		}
		envelope := encryptCLIEnvelope(t, clientPublicKey, sessionID, token)
		envelope.User = auth.User{ID: "user_123"}
		writeTestJSON(t, w, http.StatusOK, envelope)
	}))
	defer server.Close()
	t.Setenv("SHED_API_URL", server.URL)
	t.Setenv("SHED_PORTAL_URL", "https://app.shed.dev")
	t.Setenv("SHED_TOKEN", "")

	var stderr bytes.Buffer
	store := &fakeCredentialStore{}
	app := New(stdout, &stderr, "test")
	app.stdin = strings.NewReader("ABCD-EFGH-IJ\n")
	app.credentials = store
	app.openBrowser = func(string) error { return errors.New("no browser") }

	if code := app.Run(context.Background(), []string{"login"}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "warning: could not open a browser") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if store.savedToken != token {
		t.Fatalf("saved token = %q", store.savedToken)
	}
}

func TestLoginRejectsInvalidAndMalformedResponses(t *testing.T) {
	tests := []struct {
		name           string
		malformed      bool
		exchangeStatus int
		exchangeError  string
	}{
		{
			// A 200 that is not an envelope. There is no create call left to
			// return nonsense, so this is where a broken control plane shows up.
			name:      "malformed envelope",
			malformed: true,
		},
		{
			name:           "invalid code",
			exchangeStatus: http.StatusBadRequest,
			exchangeError:  "invalid_verification_code",
		},
		{
			name:           "consumed session",
			exchangeStatus: http.StatusConflict,
			exchangeError:  "authorization_consumed",
		},
		{
			name:           "expired session",
			exchangeStatus: http.StatusGone,
			exchangeError:  "authorization_expired",
		},
		{
			name:           "rate limited",
			exchangeStatus: http.StatusTooManyRequests,
			exchangeError:  "too_many_attempts",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/exchange") {
					http.NotFound(w, r)
					return
				}
				if test.malformed {
					writeTestJSON(t, w, http.StatusOK, map[string]string{"encryptedToken": "not-an-envelope"})
					return
				}
				writeTestJSON(t, w, test.exchangeStatus, map[string]string{
					"error":   test.exchangeError,
					"message": "Login could not be completed",
				})
			}))
			defer server.Close()
			t.Setenv("SHED_API_URL", server.URL)
			t.Setenv("SHED_PORTAL_URL", "https://app.shed.dev")
			t.Setenv("SHED_TOKEN", "")

			stdout := &syncBuffer{}
			var stderr bytes.Buffer
			store := &fakeCredentialStore{}
			app := New(stdout, &stderr, "test")
			app.stdin = strings.NewReader("ABCD\n")
			app.credentials = store
			app.openBrowser = func(string) error { return nil }

			if code := app.Run(context.Background(), []string{"login", "--no-browser"}); code == 0 {
				t.Fatalf("Run() succeeded, stdout = %q", stdout.String())
			}
			if store.savedToken != "" {
				t.Fatalf("saved token = %q", store.savedToken)
			}
		})
	}
}

func TestLoginWithTokenFlagSkipsBrowserFlow(t *testing.T) {
	// --token must go straight to /v1/cli/me for verification and save the
	// credential; no session-create/exchange calls are allowed to reach the
	// server, and no browser must be opened.
	token := testShedToken(11)
	var sessionCalls, meCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/cli/me":
			meCalls++
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
			}
			writeTestJSON(t, w, http.StatusOK, auth.User{ID: "user_11", Email: "carol@example.com"})
		case strings.Contains(r.URL.Path, "/auth/sessions"):
			sessionCalls++
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("SHED_API_URL", server.URL)
	t.Setenv("SHED_TOKEN", "")

	var stdout, stderr bytes.Buffer
	store := &fakeCredentialStore{}
	app := New(&stdout, &stderr, "test")
	app.credentials = store
	browserCalled := false
	app.openBrowser = func(string) error { browserCalled = true; return nil }

	if code := app.Run(context.Background(), []string{"login", "--token", token}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if browserCalled {
		t.Fatal("--token flow opened a browser")
	}
	if sessionCalls != 0 {
		t.Fatalf("--token flow made %d session calls, want 0", sessionCalls)
	}
	if meCalls != 1 {
		t.Fatalf("--token flow made %d /me calls, want 1", meCalls)
	}
	if store.savedToken != token {
		t.Fatalf("saved token = %q, want %q", store.savedToken, token)
	}
	if !strings.Contains(stdout.String(), "Logged in as carol@example.com (via flag)") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestLoginRefusesInteractivePromptOnNonTTY(t *testing.T) {
	// A real *os.File that is not a terminal (empty pipe) with no --token /
	// SHED_TOKEN / piped content must fail with login_requires_terminal
	// rather than hanging on a hidden prompt read.
	t.Setenv("SHED_API_URL", "http://127.0.0.1:1")
	t.Setenv("SHED_TOKEN", "")

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	app.stdin = reader
	app.credentials = &fakeCredentialStore{}

	if code := app.Run(context.Background(), []string{"login", "--no-browser"}); code == 0 {
		t.Fatalf("login succeeded on non-TTY stdin, stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "login_requires_terminal") &&
		!strings.Contains(stderr.String(), "interactive") {
		t.Fatalf("stderr = %q, want login_requires_terminal diagnostic", stderr.String())
	}
}

func TestLoginAcceptsPipedTokenFromStdin(t *testing.T) {
	// echo <token> | shed login must accept the token from a real pipe and
	// verify + save it exactly like --token does.
	token := testShedToken(13)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/cli/me" && r.Header.Get("Authorization") == "Bearer "+token {
			writeTestJSON(t, w, http.StatusOK, auth.User{ID: "user_13"})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	t.Setenv("SHED_API_URL", server.URL)
	t.Setenv("SHED_TOKEN", "")

	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(token + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()

	var stdout, stderr bytes.Buffer
	store := &fakeCredentialStore{}
	app := New(&stdout, &stderr, "test")
	app.stdin = reader
	app.credentials = store

	if code := app.Run(context.Background(), []string{"login", "--no-browser"}); code != 0 {
		t.Fatalf("piped-stdin login code = %d, stderr = %q", code, stderr.String())
	}
	if store.savedToken != token {
		t.Fatalf("saved token = %q, want %q", store.savedToken, token)
	}
	if !strings.Contains(stdout.String(), "via stdin") {
		t.Fatalf("stdout = %q, want 'via stdin' source annotation", stdout.String())
	}
}

func TestLoginHonorsCanceledContext(t *testing.T) {
	t.Setenv("SHED_API_URL", "http://127.0.0.1:1")
	t.Setenv("SHED_PORTAL_URL", "https://app.shed.dev")
	t.Setenv("SHED_TOKEN", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	app.credentials = &fakeCredentialStore{}

	if code := app.Run(ctx, []string{"login", "--no-browser"}); code == 0 {
		t.Fatal("Run() succeeded with canceled context")
	}
	if !strings.Contains(stderr.String(), "Login cancelled.") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestLoginCancelsWhileWaitingForVerificationCode(t *testing.T) {
	// Ctrl+C during the "enter the verification code" prompt must exit the
	// login flow cleanly instead of hanging on the blocking stdin read.
	server := &loginServer{}
	server.start(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stdinReader, stdinWriter := io.Pipe()
	defer func() { _ = stdinWriter.Close() }()

	stdout := server.stdout
	var stderr bytes.Buffer
	app := New(stdout, &stderr, "test")
	app.stdin = stdinReader
	app.credentials = &fakeCredentialStore{}
	app.openBrowser = func(string) error { return nil }

	done := make(chan int, 1)
	go func() { done <- app.Run(ctx, []string{"login", "--no-browser"}) }()

	deadline := time.After(2 * time.Second)
	for !strings.Contains(stdout.String(), "verification code") {
		select {
		case <-deadline:
			t.Fatalf("verification prompt never appeared, stdout = %q", stdout.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()

	select {
	case code := <-done:
		if code == 0 {
			t.Fatalf("login succeeded after Ctrl+C, stderr = %q", stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("login did not exit after Ctrl+C")
	}
	if !strings.Contains(stderr.String(), "Login cancelled.") {
		t.Fatalf("stderr = %q, want 'Login cancelled.'", stderr.String())
	}
}

func TestWhoamiAndLogout(t *testing.T) {
	var revoked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer stored-token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/cli/me":
			writeTestJSON(t, w, http.StatusOK, auth.User{ID: "user_123", Email: "alice@example.com"})
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/cli/auth/tokens/current":
			revoked = true
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("SHED_API_URL", server.URL)
	t.Setenv("SHED_TOKEN", "")

	store := &fakeCredentialStore{credential: credentials.Credential{
		Token:  "stored-token",
		Source: credentials.SourceKeyring,
	}}
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	app.credentials = store

	if code := app.Run(context.Background(), []string{"whoami"}); code != 0 {
		t.Fatalf("whoami code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "alice@example.com") || !strings.Contains(stdout.String(), "user_123") {
		t.Fatalf("whoami stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := app.Run(context.Background(), []string{"logout"}); code != 0 {
		t.Fatalf("logout code = %d, stderr = %q", code, stderr.String())
	}
	if !revoked || !store.deleted {
		t.Fatalf("revoked = %t, deleted = %t", revoked, store.deleted)
	}
}

func TestLogoutLocalDoesNotCallBackend(t *testing.T) {
	t.Setenv("SHED_API_URL", "http://127.0.0.1:1")
	t.Setenv("SHED_TOKEN", "")
	store := &fakeCredentialStore{credential: credentials.Credential{
		Token:  "stored-token",
		Source: credentials.SourceFile,
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	app.credentials = store

	if code := app.Run(context.Background(), []string{"logout", "--local"}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if !store.deleted {
		t.Fatal("logout --local did not delete credentials")
	}
}

// loginServer is a scripted Shed API for the login flow. exchangeErrors is
// consumed one entry per exchange attempt: an empty entry issues a real encrypted
// envelope, a non-empty one fails with that error code.
type loginServer struct {
	exchangeErrors []string
	revokeStatus   int
	token          string
	// stdout is where the authorization URL is printed, and the only place the
	// client public key exists on this side of the ceremony.
	stdout *syncBuffer

	exchanges int
	revoked   bool
}

func (s *loginServer) start(t *testing.T) *httptest.Server {
	t.Helper()
	if s.token == "" {
		s.token = testShedToken(9)
	}
	if s.revokeStatus == 0 {
		s.revokeStatus = http.StatusNoContent
	}
	if s.stdout == nil {
		s.stdout = &syncBuffer{}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID, clientPublicKey := s.stdout.authorization(t)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/cli/auth/sessions/"+sessionID+"/exchange":
			index := s.exchanges
			s.exchanges++
			if index < len(s.exchangeErrors) && s.exchangeErrors[index] != "" {
				status := http.StatusBadRequest
				if s.exchangeErrors[index] != api.ErrCodeInvalidVerificationCode {
					status = http.StatusGone
				}
				writeTestJSON(t, w, status, map[string]string{
					"error":   s.exchangeErrors[index],
					"message": "Login could not be completed",
				})
				return
			}
			envelope := encryptCLIEnvelope(t, clientPublicKey, sessionID, s.token)
			envelope.User = auth.User{ID: "user_123", Email: "alice@example.com"}
			writeTestJSON(t, w, http.StatusOK, envelope)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/cli/auth/tokens/current":
			s.revoked = true
			w.WriteHeader(s.revokeStatus)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("SHED_API_URL", server.URL)
	t.Setenv("SHED_PORTAL_URL", "https://app.shed.dev")
	t.Setenv("SHED_TOKEN", "")
	return server
}

// TestLoginRefusesMismatchedEndpoints pins the guard against a split-brain
// ceremony: a custom API with the default portal would send the browser to
// hosted Shed while this process polls somewhere else, so the login must be
// refused before any browser opens.
func TestLoginRefusesMismatchedEndpoints(t *testing.T) {
	t.Setenv("SHED_API_URL", "http://127.0.0.1:1")
	t.Setenv("SHED_PORTAL_URL", "")
	t.Setenv("SHED_TOKEN", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	app.stdin = strings.NewReader("")
	app.credentials = &fakeCredentialStore{}
	browserOpened := false
	app.openBrowser = func(string) error { browserOpened = true; return nil }

	if code := app.Run(context.Background(), []string{"login"}); code == 0 {
		t.Fatal("Run() succeeded with a custom API URL and the default portal")
	}
	if browserOpened {
		t.Fatal("a browser opened for a login that could never complete")
	}
	for _, want := range []string{"cannot mix a custom address", "SHED_API_URL", "built-in default"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, missing %q", stderr.String(), want)
		}
	}
}

func TestLoginRevokesTokenWhenCredentialSaveFails(t *testing.T) {
	server := &loginServer{}
	server.start(t)

	stdout := server.stdout
	var stderr bytes.Buffer
	store := &fakeCredentialStore{saveErr: errors.New("keyring locked")}
	app := New(stdout, &stderr, "test")
	app.stdin = strings.NewReader("ABCD-EFGH-IJ\n")
	app.credentials = store
	app.openBrowser = func(string) error { return nil }

	if code := app.Run(context.Background(), []string{"login", "--no-browser"}); code == 0 {
		t.Fatal("login succeeded despite a credential save failure")
	}
	if !server.revoked {
		t.Fatal("login left an unstored token alive on the server")
	}
	if !strings.Contains(stderr.String(), "save login credentials") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if strings.Contains(stdout.String(), "Logged in") {
		t.Fatalf("login reported success, stdout = %q", stdout.String())
	}
}

func TestLoginReportsRevokeFailureAfterSaveFailure(t *testing.T) {
	server := &loginServer{revokeStatus: http.StatusInternalServerError}
	server.start(t)

	stdout := server.stdout
	var stderr bytes.Buffer
	store := &fakeCredentialStore{saveErr: errors.New("keyring locked")}
	app := New(stdout, &stderr, "test")
	app.stdin = strings.NewReader("ABCD-EFGH-IJ\n")
	app.credentials = store
	app.openBrowser = func(string) error { return nil }

	if code := app.Run(context.Background(), []string{"login", "--no-browser"}); code == 0 {
		t.Fatal("login succeeded despite save and revoke both failing")
	}
	// Both failures must survive; the user has to know a live token was issued and
	// neither stored nor revoked.
	if !strings.Contains(stderr.String(), "save login credentials") {
		t.Fatalf("stderr lost the save failure = %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "revoke unstored token") {
		t.Fatalf("stderr lost the revoke failure = %q", stderr.String())
	}
}

func TestLoginRetriesRejectedVerificationCode(t *testing.T) {
	server := &loginServer{exchangeErrors: []string{api.ErrCodeInvalidVerificationCode}}
	server.start(t)

	stdout := server.stdout
	var stderr bytes.Buffer
	store := &fakeCredentialStore{}
	app := New(stdout, &stderr, "test")
	app.stdin = strings.NewReader("WRNG-CODE-11\nABCD-EFGH-IJ\n")
	app.credentials = store
	app.openBrowser = func(string) error { return nil }

	if code := app.Run(context.Background(), []string{"login", "--no-browser"}); code != 0 {
		t.Fatalf("login code = %d, stderr = %q", code, stderr.String())
	}
	if server.exchanges != 2 {
		t.Fatalf("exchange attempts = %d, want 2", server.exchanges)
	}
	if store.savedToken != server.token {
		t.Fatalf("saved token = %q", store.savedToken)
	}
	if !strings.Contains(stderr.String(), "not accepted") {
		t.Fatalf("stderr did not explain the retry = %q", stderr.String())
	}
}

func TestLoginStopsRetryingAfterMaxAttempts(t *testing.T) {
	server := &loginServer{exchangeErrors: []string{
		api.ErrCodeInvalidVerificationCode,
		api.ErrCodeInvalidVerificationCode,
		api.ErrCodeInvalidVerificationCode,
		api.ErrCodeInvalidVerificationCode,
	}}
	server.start(t)

	stdout := server.stdout
	var stderr bytes.Buffer
	store := &fakeCredentialStore{}
	app := New(stdout, &stderr, "test")
	app.stdin = strings.NewReader("AAAA\nBBBB\nCCCC\nDDDD\n")
	app.credentials = store
	app.openBrowser = func(string) error { return nil }

	if code := app.Run(context.Background(), []string{"login", "--no-browser"}); code == 0 {
		t.Fatal("login succeeded with a permanently invalid code")
	}
	if server.exchanges != maxVerificationAttempts {
		t.Fatalf("exchange attempts = %d, want %d", server.exchanges, maxVerificationAttempts)
	}
	if store.savedToken != "" {
		t.Fatalf("saved token = %q", store.savedToken)
	}
}

func TestLoginDoesNotRetryTerminalExchangeError(t *testing.T) {
	// An expired authorization cannot be fixed by retyping, so the CLI must not
	// burn the user's remaining attempts on it.
	server := &loginServer{exchangeErrors: []string{
		api.ErrCodeAuthorizationExpired,
		api.ErrCodeAuthorizationExpired,
	}}
	server.start(t)

	stdout := server.stdout
	var stderr bytes.Buffer
	app := New(stdout, &stderr, "test")
	app.stdin = strings.NewReader("AAAA\nBBBB\n")
	app.credentials = &fakeCredentialStore{}
	app.openBrowser = func(string) error { return nil }

	if code := app.Run(context.Background(), []string{"login", "--no-browser"}); code == 0 {
		t.Fatal("login succeeded with an expired authorization")
	}
	if server.exchanges != 1 {
		t.Fatalf("exchange attempts = %d, want 1", server.exchanges)
	}
}

func TestLoginRejectsUnsafeTokenName(t *testing.T) {
	server := &loginServer{}
	server.start(t)

	stdout := server.stdout
	var stderr bytes.Buffer
	app := New(stdout, &stderr, "test")
	app.stdin = strings.NewReader("ABCD-EFGH-IJ\n")
	app.credentials = &fakeCredentialStore{}
	app.openBrowser = func(string) error { return nil }

	if code := app.Run(context.Background(), []string{"login", "--name", "bad name;rm"}); code == 0 {
		t.Fatal("login accepted an unsafe --name")
	}
	if server.exchanges != 0 {
		t.Fatal("login contacted the server with an unsafe --name")
	}
}

func TestLoginReportsOpenedBrowser(t *testing.T) {
	server := &loginServer{}
	server.start(t)

	stdout := server.stdout
	var stderr bytes.Buffer
	app := New(stdout, &stderr, "test")
	app.stdin = strings.NewReader("ABCD-EFGH-IJ\n")
	app.credentials = &fakeCredentialStore{}
	// The URL travels to the browser, not the terminal; the fake browser
	// records it so the test server can read the session from it.
	app.openBrowser = func(link string) error {
		_, _ = fmt.Fprintln(stdout, link)
		return nil
	}

	if code := app.Run(context.Background(), []string{"login"}); code != 0 {
		t.Fatalf("login code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Opened your browser to sign in.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "Open this link to sign in:") {
		t.Fatal("the authorization URL was printed even though the browser opened")
	}
}

func TestHelpFlagExitsZeroWithoutErrorLine(t *testing.T) {
	for _, args := range [][]string{{"login", "-h"}, {"logout", "--help"}, {"deploy", "-h"}} {
		var stdout, stderr bytes.Buffer
		app := New(&stdout, &stderr, "test")
		app.credentials = &fakeCredentialStore{}

		if code := app.Run(context.Background(), args); code != 0 {
			t.Fatalf("%v code = %d, want 0", args, code)
		}
		if strings.Contains(stderr.String(), "help requested") {
			t.Fatalf("%v stderr leaked the flag sentinel = %q", args, stderr.String())
		}
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}

func encryptCLIEnvelope(t *testing.T, clientPublicKey, sessionID, token string) auth.TokenEnvelope {
	t.Helper()
	encoding := base64.RawURLEncoding
	clientBytes, err := encoding.DecodeString(clientPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ecdh.X25519().NewPublicKey(clientBytes)
	if err != nil {
		t.Fatal(err)
	}
	serverPrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := serverPrivate.ECDH(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	salt := bytes.Repeat([]byte{3}, 32)
	key := make([]byte, 32)
	info := []byte("shed-cli-login-v1:" + sessionID)
	if _, err := io.ReadFull(hkdf.New(sha256.New, secret, salt, info), key); err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{5}, aead.NonceSize())
	ciphertext := aead.Seal(nil, nonce, []byte(token), info)
	return auth.TokenEnvelope{
		EncryptedToken:  encoding.EncodeToString(ciphertext),
		ServerPublicKey: encoding.EncodeToString(serverPrivate.PublicKey().Bytes()),
		Nonce:           encoding.EncodeToString(nonce),
		KDFSalt:         encoding.EncodeToString(salt),
		ProtocolVersion: auth.EnvelopeVersion,
	}
}

func testShedToken(fill byte) string {
	return "shed_pat_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

// The loopback branch of the login had no end-to-end coverage: every other
// login test passes --no-browser, so the callback path, the state check and the
// handoff into the exchange were only ever exercised in pieces.
//
// This drives the whole thing the way a browser does. The CLI opens the link,
// the "browser" reads the parameters out of it and fires the callback at the
// listener the CLI is holding, and nobody types anything.
func TestLoginCompletesThroughTheLoopbackCallback(t *testing.T) {
	token := testShedToken(11)
	stdout := &syncBuffer{}
	var exchangedCode string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID, clientPublicKey := stdout.authorization(t)
		if r.URL.Path != "/v1/cli/auth/sessions/"+sessionID+"/exchange" {
			http.NotFound(w, r)
			return
		}
		var request map[string]string
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		exchangedCode = request["verificationCode"]
		envelope := encryptCLIEnvelope(t, clientPublicKey, sessionID, token)
		envelope.User = auth.User{ID: "user_123", Email: "alice@example.com"}
		writeTestJSON(t, w, http.StatusOK, envelope)
	}))
	defer server.Close()
	t.Setenv("SHED_API_URL", server.URL)
	t.Setenv("SHED_PORTAL_URL", "https://app.shed.dev")
	t.Setenv("SHED_TOKEN", "")

	// A code nobody has to read, which is the point of loopback delivery.
	const deliveredCode = "Zm9vYmFyYmF6cXV4MDEyMzQ1Njc4OWFiY2RlZmdoaWo"

	var stderr bytes.Buffer
	store := &fakeCredentialStore{}
	app := New(stdout, &stderr, "test")
	// Nothing is typed. An empty reader also proves the keyboard watcher does
	// not decide the outcome when the callback wins the race.
	app.stdin = strings.NewReader("")
	app.credentials = store
	app.openBrowser = func(link string) error {
		// The URL travels to the browser, not the terminal; record it so the
		// test server can read the session from it.
		_, _ = fmt.Fprintln(stdout, link)
		parsed, err := url.Parse(link)
		if err != nil {
			return err
		}
		query := parsed.Query()
		callback, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			return err
		}
		if callback.Hostname() != "127.0.0.1" || callback.Path != "/callback" {
			t.Errorf("redirect_uri = %q, want a loopback callback", query.Get("redirect_uri"))
		}
		delivered := url.Values{"code": {deliveredCode}, "state": {query.Get("state")}}
		callback.RawQuery = delivered.Encode()
		response, err := http.Get(callback.String())
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusOK {
			t.Errorf("callback status = %d", response.StatusCode)
		}
		return nil
	}

	if code := app.Run(context.Background(), []string{"login"}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if exchangedCode != deliveredCode {
		t.Fatalf("exchanged %q, want the code the browser delivered", exchangedCode)
	}
	if store.savedToken != token {
		t.Fatalf("saved token = %q", store.savedToken)
	}
	if strings.Contains(stdout.String(), deliveredCode) {
		t.Fatal("the delivered code was printed; loopback delivery should be silent")
	}
}

// A callback carrying someone else's state is not our browser answering. It
// must not complete the login, and the CLI must stay on the keyboard fallback.
func TestLoginIgnoresACallbackWithTheWrongState(t *testing.T) {
	token := testShedToken(12)
	stdout := &syncBuffer{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sessionID, clientPublicKey := stdout.authorization(t)
		if r.URL.Path != "/v1/cli/auth/sessions/"+sessionID+"/exchange" {
			http.NotFound(w, r)
			return
		}
		envelope := encryptCLIEnvelope(t, clientPublicKey, sessionID, token)
		envelope.User = auth.User{ID: "user_123"}
		writeTestJSON(t, w, http.StatusOK, envelope)
	}))
	defer server.Close()
	t.Setenv("SHED_API_URL", server.URL)
	t.Setenv("SHED_PORTAL_URL", "https://app.shed.dev")
	t.Setenv("SHED_TOKEN", "")

	var stderr bytes.Buffer
	store := &fakeCredentialStore{}
	app := New(stdout, &stderr, "test")
	// The forged callback is rejected, so the login can only finish because
	// this code was typed.
	app.stdin = strings.NewReader("ABCD-EFGH-IJ\n")
	app.credentials = store
	app.openBrowser = func(link string) error {
		// The URL travels to the browser, not the terminal; record it so the
		// test server can read the session from it.
		_, _ = fmt.Fprintln(stdout, link)
		parsed, err := url.Parse(link)
		if err != nil {
			return err
		}
		callback, err := url.Parse(parsed.Query().Get("redirect_uri"))
		if err != nil {
			return err
		}
		callback.RawQuery = url.Values{
			"code":  {"Zm9yZ2VkY29kZTAxMjM0NTY3ODlhYmNkZWZnaGlqa2w"},
			"state": {"not-the-state-the-cli-generated-for-this-login"},
		}.Encode()
		response, err := http.Get(callback.String())
		if err != nil {
			return err
		}
		defer func() { _ = response.Body.Close() }()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("forged callback status = %d, want %d", response.StatusCode, http.StatusBadRequest)
		}
		return nil
	}

	if code := app.Run(context.Background(), []string{"login"}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if store.savedToken != token {
		t.Fatalf("saved token = %q", store.savedToken)
	}
}
