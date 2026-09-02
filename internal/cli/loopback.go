package cli

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"time"
)

// loopbackPath must match what bench validates and what the approval page
// redirects to. Changing it here alone breaks the handoff silently.
const loopbackPath = "/callback"

// loopbackShutdownGrace gives the browser long enough to receive the "you can
// close this tab" body before the process exits underneath it.
const loopbackShutdownGrace = 2 * time.Second

// loopbackReceiver is the CLI half of loopback delivery: a listener held on
// 127.0.0.1 that the approval page redirects the browser to, so the
// verification code travels machine-locally instead of through the person.
//
// The token itself is unaffected. It is still minted by bench, still encrypted
// to this process's public key, and still only decryptable here — the callback
// only carries the code that authorizes the exchange.
type loopbackReceiver struct {
	server   *http.Server
	codes    chan string
	state    string
	redirect string
}

// startLoopback binds an ephemeral port and serves the callback.
//
// The port is bound before anything is advertised to bench, so the URL we ask
// the browser to visit is always one this process already holds. Reserving a
// port we had not bound would leave a window for another local process to take
// it and receive the code instead.
func startLoopback() (*loopbackReceiver, error) {
	state, err := randomState()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("bind loopback listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	receiver := &loopbackReceiver{
		codes:    make(chan string, 1),
		state:    state,
		redirect: fmt.Sprintf("http://127.0.0.1:%d%s", port, loopbackPath),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(loopbackPath, receiver.handle)
	receiver.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = receiver.server.Serve(listener) }()

	return receiver, nil
}

func (r *loopbackReceiver) handle(w http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()

	// Constant time, because this is the only thing separating the real
	// callback from one any local process could fire at us.
	if subtle.ConstantTimeCompare([]byte(query.Get("state")), []byte(r.state)) != 1 {
		http.Error(w, "unrecognized login callback", http.StatusBadRequest)
		return
	}
	code := query.Get("code")
	if code == "" {
		http.Error(w, "login callback carried no verification code", http.StatusBadRequest)
		return
	}

	// Buffered and non-blocking: a duplicate callback must not wedge the
	// handler once the first one has already been taken.
	select {
	case r.codes <- code:
	default:
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(callbackPage))
}

// RedirectURI is what bench is told to send the browser to.
func (r *loopbackReceiver) RedirectURI() string { return r.redirect }

// State is the value the callback must echo back.
func (r *loopbackReceiver) State() string { return r.state }

// Codes yields the verification code once the browser hands it over.
func (r *loopbackReceiver) Codes() <-chan string { return r.codes }

// Close stops serving. The grace period is for the response body already in
// flight, not for any work of our own.
func (r *loopbackReceiver) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), loopbackShutdownGrace)
	defer cancel()
	_ = r.server.Shutdown(ctx)
}

// randomState matches the session id: 32 random bytes as unpadded base64url,
// which is the shape bench validates.
func randomState() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate login state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// Served to the browser after a successful handoff. Self-contained because it
// is fetched from a local port with no assets behind it.
const callbackPage = `<!doctype html>
<meta charset="utf-8">
<title>Signed in to Shed</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>
  :root { color-scheme: light dark; }
  body {
    margin: 0; min-height: 100vh; display: grid; place-items: center;
    font: 16px/1.5 ui-sans-serif, system-ui, sans-serif;
    background: #f0f4f4; color: #171b1c;
  }
  @media (prefers-color-scheme: dark) {
    body { background: #16181c; color: #eef2f4; }
  }
  main { text-align: center; padding: 32px; }
  h1 { margin: 0 0 8px; font-size: 20px; font-weight: 600; letter-spacing: -0.02em; }
  p { margin: 0; opacity: 0.7; font-size: 14px; }
</style>
<main>
  <h1>Signed in to Shed</h1>
  <p>You can close this tab and go back to your terminal.</p>
</main>
`
