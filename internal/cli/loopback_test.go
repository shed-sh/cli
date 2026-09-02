package cli

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func startTestLoopback(t *testing.T) *loopbackReceiver {
	t.Helper()
	receiver, err := startLoopback()
	if err != nil {
		t.Fatalf("startLoopback() error = %v", err)
	}
	t.Cleanup(receiver.Close)
	return receiver
}

func callbackWith(t *testing.T, receiver *loopbackReceiver, query url.Values) int {
	t.Helper()
	response, err := http.Get(receiver.RedirectURI() + "?" + query.Encode())
	if err != nil {
		t.Fatalf("callback request failed: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode
}

func TestLoopbackDeliversCode(t *testing.T) {
	receiver := startTestLoopback(t)

	status := callbackWith(t, receiver, url.Values{
		"state": {receiver.State()},
		"code":  {"7Q2M4X"},
	})
	if status != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", status)
	}

	select {
	case code := <-receiver.Codes():
		if code != "7Q2M4X" {
			t.Fatalf("delivered code = %q", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("callback did not deliver a code")
	}
}

// The state is the only thing separating the real callback from one any local
// process could fire at the port, so a wrong one must deliver nothing at all.
func TestLoopbackRejectsWrongState(t *testing.T) {
	receiver := startTestLoopback(t)

	status := callbackWith(t, receiver, url.Values{
		"state": {"not-the-state"},
		"code":  {"7Q2M4X"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("wrong state status = %d, want 400", status)
	}

	select {
	case code := <-receiver.Codes():
		t.Fatalf("forged callback delivered %q", code)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestLoopbackRejectsMissingCode(t *testing.T) {
	receiver := startTestLoopback(t)

	if status := callbackWith(t, receiver, url.Values{"state": {receiver.State()}}); status != http.StatusBadRequest {
		t.Fatalf("missing code status = %d, want 400", status)
	}
}

// A second callback must not block the handler once the first has been taken.
func TestLoopbackToleratesDuplicateCallback(t *testing.T) {
	receiver := startTestLoopback(t)
	query := url.Values{"state": {receiver.State()}, "code": {"7Q2M4X"}}

	for attempt := range 3 {
		if status := callbackWith(t, receiver, query); status != http.StatusOK {
			t.Fatalf("callback %d status = %d, want 200", attempt, status)
		}
	}
}

func TestLoopbackBindsOnlyLoopback(t *testing.T) {
	receiver := startTestLoopback(t)

	parsed, err := url.Parse(receiver.RedirectURI())
	if err != nil {
		t.Fatalf("redirect URI is not a URL: %v", err)
	}
	if parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Path != loopbackPath {
		t.Fatalf("redirect URI = %q, want http on 127.0.0.1%s", receiver.RedirectURI(), loopbackPath)
	}
	if parsed.Port() == "" || parsed.Port() == "0" {
		t.Fatalf("redirect URI has no bound port: %q", receiver.RedirectURI())
	}
}

// Bench requires 32 bytes of unpadded base64url, the same shape as a session id.
func TestLoopbackStateShape(t *testing.T) {
	receiver := startTestLoopback(t)
	if len(receiver.State()) != 43 {
		t.Fatalf("state length = %d, want 43", len(receiver.State()))
	}
}

func TestLoopbackStateIsUniquePerListener(t *testing.T) {
	first := startTestLoopback(t)
	second := startTestLoopback(t)
	if first.State() == second.State() {
		t.Fatal("two listeners produced the same state")
	}
}
