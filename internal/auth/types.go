package auth

import (
	"context"
	"time"
)

// ProtocolVersion is the login ceremony this CLI speaks. Under version 2 the
// CLI builds the authorization URL from its own configuration and reaches no
// network before opening the browser; the session is created when the approval
// page approves it.
const ProtocolVersion = "2"

// EnvelopeVersion is the sealed-token format this CLI can open. It is
// deliberately not the ceremony version: the encryption did not change when the
// ceremony did, so a server serving either ceremony seals tokens the same way.
const EnvelopeVersion = "1"

// SessionTTL is how long the CLI keeps waiting for a login to complete.
//
// It is not a mirror of the server's expiry. Under version 2 the server starts
// its own clock when the session is approved, which is some unknown time after
// the browser opened, so a local window sized to the server's would give up on
// a session still perfectly alive. This is only a fast path to stop waiting
// forever; the server stays the authority on whether a session is expired.
const SessionTTL = 15 * time.Minute

// Redirect asks the server to hand the verification code to a loopback listener
// this process is already holding, instead of printing it for someone to copy.
//
// The zero value means no redirect, which is the path --no-browser, SSH
// sessions and CI take: the code is read off the approval screen and typed in.
type Redirect struct {
	URI   string
	State string
}

type LoginSession struct {
	ID               string    `json:"sessionId"`
	AuthorizationURL string    `json:"authorizationUrl"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

type TokenEnvelope struct {
	EncryptedToken  string `json:"encryptedToken"`
	ServerPublicKey string `json:"serverPublicKey"`
	Nonce           string `json:"nonce"`
	KDFSalt         string `json:"kdfSalt"`
	ProtocolVersion string `json:"protocolVersion"`
	User            User   `json:"user"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	// Tenant is the workspace this token belongs to. A personal access token
	// is minted for one workspace and stays with it, so which workspace it
	// speaks for is not derivable from the email -- someone in two workspaces
	// has two tokens that look identical from here.
	Tenant Tenant `json:"tenant"`
}

// Tenant is the workspace a token acts in, as bench reports it on /v1/cli/me.
type Tenant struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

type LoginResult struct {
	Token string
	User  User
}

// Client is the part of the ceremony that still needs the control plane: the
// exchange. Creating the session is the approval page's job now, so the CLI has
// no reason to call the API before someone has approved anything.
type Client interface {
	ExchangeLoginSession(context.Context, string, string) (TokenEnvelope, error)
}
