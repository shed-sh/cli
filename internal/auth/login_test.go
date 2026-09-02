package auth

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeLoginClient struct {
	envelope TokenEnvelope
	code     string
	err      error
}

func (f *fakeLoginClient) ExchangeLoginSession(_ context.Context, _ string, code string) (TokenEnvelope, error) {
	f.code = code
	return f.envelope, f.err
}

// The whole point of the ceremony is that this reaches no network, so the test
// passes a client that would fail loudly if it were called.
func TestBeginLoginBuildsTheAuthorizationURLWithoutTheNetwork(t *testing.T) {
	client := &fakeLoginClient{err: errors.New("the network must not be touched")}
	attempt, err := BeginLogin(client, "my-workstation", "https://app.shed.dev", Redirect{})
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	if decoded, err := rawBase64.DecodeString(attempt.Session.ID); err != nil || len(decoded) != 32 {
		t.Fatalf("session ID %q is not 32 bytes of base64url (err = %v)", attempt.Session.ID, err)
	}

	parsed, err := url.Parse(attempt.Session.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if parsed.Host != "app.shed.dev" || parsed.Path != "/cli/login" {
		t.Fatalf("authorization URL = %q", attempt.Session.AuthorizationURL)
	}
	query := parsed.Query()
	if query.Get("v") != ProtocolVersion {
		t.Fatalf("protocol version = %q, want %q", query.Get("v"), ProtocolVersion)
	}
	if query.Get("session") != attempt.Session.ID {
		t.Fatalf("session parameter = %q", query.Get("session"))
	}
	if query.Get("name") != "my-workstation" {
		t.Fatalf("name parameter = %q", query.Get("name"))
	}
	if decoded, err := rawBase64.DecodeString(query.Get("key")); err != nil || len(decoded) != 32 {
		t.Fatalf("key parameter is invalid: length=%d err=%v", len(decoded), err)
	}
	// Without a listener there is nothing to redirect to, and a redirect the
	// CLI is not serving would strand the browser.
	if query.Has("redirect_uri") || query.Has("state") {
		t.Fatalf("URL carried a redirect with no listener: %q", parsed.RawQuery)
	}
}

func TestBeginLoginCarriesTheLoopbackCallback(t *testing.T) {
	redirect := Redirect{URI: "http://127.0.0.1:52100/callback", State: "state-value"}
	attempt, err := BeginLogin(&fakeLoginClient{}, "workstation", "http://localhost:3000/", redirect)
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	query, err := url.Parse(attempt.Session.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if got := query.Query().Get("redirect_uri"); got != redirect.URI {
		t.Fatalf("redirect_uri = %q, want %q", got, redirect.URI)
	}
	if got := query.Query().Get("state"); got != redirect.State {
		t.Fatalf("state = %q, want %q", got, redirect.State)
	}
	// A trailing slash on the configured portal must not double up.
	if !strings.HasPrefix(attempt.Session.AuthorizationURL, "http://localhost:3000/cli/login?") {
		t.Fatalf("authorization URL = %q", attempt.Session.AuthorizationURL)
	}
}

// Two logins must never collide on a session id, because the id is what the
// exchange is addressed to.
func TestBeginLoginGeneratesAFreshSessionEachTime(t *testing.T) {
	first, err := BeginLogin(&fakeLoginClient{}, "workstation", "https://app.shed.dev", Redirect{})
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	second, err := BeginLogin(&fakeLoginClient{}, "workstation", "https://app.shed.dev", Redirect{})
	if err != nil {
		t.Fatalf("BeginLogin() error = %v", err)
	}
	if first.Session.ID == second.Session.ID {
		t.Fatal("two logins reused a session ID")
	}
}

// A portal address the CLI cannot build a link from is a configuration fault,
// and it has to be caught here rather than handed to a browser.
func TestBeginLoginRejectsAnUnusablePortalURL(t *testing.T) {
	tests := map[string]string{
		"empty":         "",
		"relative":      "/cli/login",
		"no host":       "https://",
		"wrong scheme":  "ftp://app.shed.dev",
		"not a url":     "://nope",
		"scheme only":   "https:",
		"missing colon": "app.shed.dev",
	}
	for name, portalURL := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := BeginLogin(&fakeLoginClient{}, "workstation", portalURL, Redirect{}); err == nil {
				t.Fatalf("BeginLogin() accepted portal URL %q", portalURL)
			}
		})
	}
}

func TestCompleteRejectsExpiredAndEmptyCode(t *testing.T) {
	client := &fakeLoginClient{}
	attempt := &LoginAttempt{
		Session: LoginSession{ID: "session", ExpiresAt: time.Now().Add(-clockSkewGrace - time.Minute)},
		client:  client,
	}
	_, err := attempt.Complete(context.Background(), "ABCD")
	if err == nil {
		t.Fatal("Complete() accepted expired session")
	}
	// Assert the local expiry check is what rejected this, not a downstream
	// decode failure that would happen for any zero-valued envelope.
	if !strings.Contains(err.Error(), "login expired") {
		t.Fatalf("Complete() error = %v, want login expired", err)
	}
	if client.code != "" {
		t.Fatalf("expired session still called the server with code %q", client.code)
	}

	attempt.Session.ExpiresAt = time.Now().Add(time.Minute)
	if _, err := attempt.Complete(context.Background(), " "); err == nil {
		t.Fatal("Complete() accepted empty code")
	}
}

func TestCompleteToleratesClockSkewWithinGrace(t *testing.T) {
	client := &fakeLoginClient{}
	attempt := &LoginAttempt{
		// A local clock running a few seconds fast must not discard a session the
		// server still considers valid.
		Session:    LoginSession{ID: "session", ExpiresAt: time.Now().Add(-clockSkewGrace / 2)},
		encryption: &Encryption{},
		client:     client,
	}
	if _, err := attempt.Complete(context.Background(), "ABCD"); err == nil {
		t.Fatal("Complete() succeeded with an empty envelope")
	}
	if client.code != "ABCD" {
		t.Fatalf("session within skew grace was rejected locally; server code = %q", client.code)
	}
}

func TestValidateTokenName(t *testing.T) {
	valid := []string{"cli_host_1700000000", "my-workstation", "laptop.local", "A1"}
	for _, name := range valid {
		if err := ValidateTokenName(name); err != nil {
			t.Fatalf("ValidateTokenName(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"",
		"has space",
		"newline\nname",
		"semi;colon",
		"slash/name",
		strings.Repeat("a", MaxTokenNameLength+1),
	}
	for _, name := range invalid {
		if err := ValidateTokenName(name); err == nil {
			t.Fatalf("ValidateTokenName(%q) accepted an unsafe name", name)
		}
	}
}

func TestDefaultTokenNamePassesValidation(t *testing.T) {
	if err := ValidateTokenName(DefaultTokenName()); err != nil {
		t.Fatalf("DefaultTokenName() produced an invalid name: %v", err)
	}
}

func TestCompletePropagatesExchangeError(t *testing.T) {
	want := errors.New("exchange failed")
	attempt := &LoginAttempt{
		Session:    LoginSession{ID: "session", ExpiresAt: time.Now().Add(time.Minute)},
		encryption: &Encryption{},
		client:     &fakeLoginClient{err: want},
	}
	_, err := attempt.Complete(context.Background(), " abcd ")
	if !errors.Is(err, want) {
		t.Fatalf("Complete() error = %v, want %v", err, want)
	}
	if attempt.client.(*fakeLoginClient).code != "ABCD" {
		t.Fatalf("verification code = %q", attempt.client.(*fakeLoginClient).code)
	}
}

// A code delivered over the callback is base64url, where case is meaningful.
// Folding it the way a typed code is folded corrupts a code nobody mistyped,
// and the exchange then fails for a login that was approved correctly.
func TestCompletePreservesTheCaseOfADeliveredCode(t *testing.T) {
	const delivered = "Zm9vYmFyYmF6cXV4MDEyMzQ1Njc4OWFiY2RlZmdoaWo"

	client := &fakeLoginClient{err: errors.New("stop after the exchange")}
	attempt := &LoginAttempt{
		Session: LoginSession{ID: "session", ExpiresAt: time.Now().Add(time.Minute)},
		client:  client,
	}
	if _, err := attempt.Complete(context.Background(), delivered); err == nil {
		t.Fatal("Complete() succeeded against a failing client")
	}
	if client.code != delivered {
		t.Fatalf("sent %q, want %q unchanged", client.code, delivered)
	}
}

// Typed codes keep their old forgiveness: the canonical form is upper case, and
// someone typing what they read in lower case is not a mistake worth failing.
func TestCompleteFoldsATypedCode(t *testing.T) {
	client := &fakeLoginClient{err: errors.New("stop after the exchange")}
	attempt := &LoginAttempt{
		Session: LoginSession{ID: "session", ExpiresAt: time.Now().Add(time.Minute)},
		client:  client,
	}
	if _, err := attempt.Complete(context.Background(), "  7k9m-2x4q-wd  "); err == nil {
		t.Fatal("Complete() succeeded against a failing client")
	}
	if client.code != "7K9M-2X4Q-WD" {
		t.Fatalf("sent %q, want the folded code", client.code)
	}
}
