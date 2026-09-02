package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

type LoginAttempt struct {
	Session    LoginSession
	encryption *Encryption
	client     Client
}

// BeginLogin prepares a login without touching the network.
//
// Everything the approval page needs is generated here and carried in the
// authorization URL: the session id, the key the token will be sealed to, and
// the loopback callback if one is waiting. That is the point of the ceremony --
// an unreachable control plane can no longer stop a browser from opening, and
// the failure, when there is one, lands after someone has actually approved
// something rather than before they saw anything at all.
//
// The private half of the key never leaves this process, so nothing that passes
// through the browser can open the token that comes back.
func BeginLogin(client Client, tokenName, portalURL string, redirect Redirect) (*LoginAttempt, error) {
	encryption, err := NewEncryption()
	if err != nil {
		return nil, err
	}
	sessionID, err := newSessionID()
	if err != nil {
		return nil, err
	}
	authorizationURL, err := AuthorizationURL(portalURL, sessionID, tokenName, encryption.PublicKey(), redirect)
	if err != nil {
		return nil, err
	}
	return &LoginAttempt{
		Session: LoginSession{
			ID:               sessionID,
			AuthorizationURL: authorizationURL,
			ExpiresAt:        time.Now().Add(SessionTTL),
		},
		encryption: encryption,
		client:     client,
	}, nil
}

// AuthorizationURL builds the link the browser is sent to.
//
// The parameters are the request: whoever opens this decides nothing, and the
// control plane validates every one of them again before it writes a session.
// They are visible in the address bar by design -- none of them is a secret.
// The code that authorizes the exchange is issued at approval time and travels
// back through the callback, never through this URL.
func AuthorizationURL(portalURL, sessionID, tokenName, publicKey string, redirect Redirect) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(portalURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("portal URL %q is not an absolute URL", portalURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("portal URL scheme must be http or https, got %q", parsed.Scheme)
	}

	authorization := *parsed
	authorization.Path = strings.TrimRight(parsed.Path, "/") + "/cli/login"
	query := authorization.Query()
	query.Set("v", ProtocolVersion)
	query.Set("session", sessionID)
	query.Set("key", publicKey)
	query.Set("name", tokenName)
	if redirect.URI != "" {
		query.Set("redirect_uri", redirect.URI)
		query.Set("state", redirect.State)
	}
	authorization.RawQuery = query.Encode()
	return authorization.String(), nil
}

// normalizeCode prepares a code for the exchange, and the two kinds of code
// have to be treated differently.
//
// A typed verification code is upper case in its canonical form and people type
// it however they like, so it is folded rather than rejecting someone who used
// lower case. A code delivered over the loopback callback is 32 bytes of
// base64url, where case carries meaning -- folding that one would corrupt a
// code nobody mistyped, because nobody typed it at all.
//
// The shape decides, and the two cannot be confused: only the delivered form is
// 43 characters of base64url.
func normalizeCode(value string) string {
	value = strings.TrimSpace(value)
	if decoded, err := rawBase64.DecodeString(value); err == nil && len(decoded) == 32 {
		return value
	}
	return strings.ToUpper(value)
}

// newSessionID matches the shape the control plane pins: 32 random bytes as
// unpadded base64url. The CLI names the session because it has to be able to
// exchange against it later, and under --no-browser nothing comes back through
// a callback to tell it what the name was.
func newSessionID() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate login session ID: %w", err)
	}
	return rawBase64.EncodeToString(value), nil
}

// clockSkewGrace keeps a local clock that runs slightly fast from discarding a
// session the server still accepts. The check below is only a fast path to skip a
// pointless round trip; the server remains the authority on expiry.
const clockSkewGrace = 30 * time.Second

func (a *LoginAttempt) Complete(ctx context.Context, verificationCode string) (LoginResult, error) {
	if !a.Session.ExpiresAt.Add(clockSkewGrace).After(time.Now()) {
		return LoginResult{}, errors.New("login expired; run shed login again")
	}
	verificationCode = normalizeCode(verificationCode)
	if verificationCode == "" {
		return LoginResult{}, errors.New("verification code is required")
	}
	envelope, err := a.client.ExchangeLoginSession(ctx, a.Session.ID, verificationCode)
	if err != nil {
		return LoginResult{}, err
	}
	token, err := a.encryption.Decrypt(a.Session.ID, envelope)
	if err != nil {
		return LoginResult{}, err
	}
	if envelope.User.ID == "" {
		return LoginResult{}, errors.New("shed API returned an empty user identity")
	}
	return LoginResult{Token: token, User: envelope.User}, nil
}

var unsafeTokenName = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// MaxTokenNameLength bounds the label stored alongside the token. It matches the
// shape DefaultTokenName produces, so a hand-written --name cannot describe a
// token in ways a generated one could not.
const MaxTokenNameLength = 64

// ValidateTokenName checks a user-supplied token label. DefaultTokenName sanitizes
// the hostname it derives; a name passed with --name gets the same rules rather
// than being forwarded to the server unchecked.
func ValidateTokenName(name string) error {
	if name == "" {
		return errors.New("token name is empty")
	}
	if len(name) > MaxTokenNameLength {
		return fmt.Errorf("token name is %d characters, limit is %d", len(name), MaxTokenNameLength)
	}
	if unsafeTokenName.MatchString(name) {
		return errors.New("token name may contain only letters, digits, dots, hyphens, and underscores")
	}
	return nil
}

func DefaultTokenName() string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	hostname = strings.Trim(unsafeTokenName.ReplaceAllString(hostname, "-"), "-")
	if hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("cli_%s_%d", hostname, time.Now().Unix())
}
