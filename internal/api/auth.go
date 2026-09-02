package api

import (
	"context"
	"net/http"
	"net/url"

	"shed/internal/auth"
	"shed/internal/diag"
)

// ErrNotLoggedIn is returned whenever an authenticated call is attempted without a
// resolved credential. Every layer reports the same sentence so the remedy is
// phrased identically wherever the user hits it.
var ErrNotLoggedIn error = &diag.Error{
	Code:    "not_logged_in",
	Summary: "You are not signed in to Shed.",
	Hints: []string{
		"Sign in: shed login",
		"Or use an existing token: export SHED_TOKEN=<token>",
	},
}

// Error codes the login exchange endpoint returns in the "error" field.
const (
	// ErrCodeInvalidVerificationCode means the user mistyped; the session is still
	// usable, so the CLI may prompt again.
	ErrCodeInvalidVerificationCode = "invalid_verification_code"
	// ErrCodeAuthorizationConsumed means the session already produced a token.
	ErrCodeAuthorizationConsumed = "authorization_consumed"
	// ErrCodeAuthorizationExpired means the session outlived its window.
	ErrCodeAuthorizationExpired = "authorization_expired"
	// ErrCodeTooManyAttempts means the server is rate limiting further attempts.
	ErrCodeTooManyAttempts = "too_many_attempts"
)

func (c *Client) ExchangeLoginSession(ctx context.Context, sessionID, verificationCode string) (auth.TokenEnvelope, error) {
	var envelope auth.TokenEnvelope
	path := "/v1/cli/auth/sessions/" + url.PathEscape(sessionID) + "/exchange"
	err := c.requestJSON(ctx, http.MethodPost, path, map[string]string{
		"verificationCode": verificationCode,
	}, &envelope, false)
	return envelope, err
}

func (c *Client) CurrentUser(ctx context.Context) (auth.User, error) {
	var user auth.User
	err := c.requestJSON(ctx, http.MethodGet, "/v1/cli/me", nil, &user, true)
	return user, err
}

func (c *Client) RevokeCurrentToken(ctx context.Context) error {
	return c.requestJSON(ctx, http.MethodDelete, "/v1/cli/auth/tokens/current", nil, nil, true)
}
