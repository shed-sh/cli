package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shed/internal/config"
	"shed/internal/diag"
)

// closedAddress is an address nothing listens on, which is the shape of the
// refused dial a machine with no control plane running actually hits.
func closedAddress(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	address := server.URL
	server.Close()
	return address
}

// An unreachable control plane is the first thing a new machine hits, because
// the default address expects a local one. The failure has to explain itself
// there or the CLI reads as broken rather than unconfigured.
func TestUnreachableAPIReportsIntentAndRemedy(t *testing.T) {
	t.Setenv(config.APIURLEnv, "")
	address := closedAddress(t)

	_, err := New(address, "").ExchangeLoginSession(context.Background(), "session", "code")
	if err == nil {
		t.Fatal("ExchangeLoginSession() against a closed listener returned no error")
	}
	diagnostic, ok := diag.As(err)
	if !ok {
		t.Fatalf("error is not a diagnostic: %v", err)
	}
	if diagnostic.Code != "api_unreachable" {
		t.Errorf("Code = %q, want api_unreachable", diagnostic.Code)
	}
	if !strings.Contains(diagnostic.Summary, address) {
		t.Errorf("Summary %q does not name the address it tried", diagnostic.Summary)
	}

	facts := map[string]string{}
	for _, fact := range diagnostic.Facts {
		facts[fact.Label] = fact.Value
	}
	if want := "POST " + address + "/v1/cli/auth/sessions/session/exchange"; facts["Tried"] != want {
		t.Errorf("Tried = %q, want %q", facts["Tried"], want)
	}
	// The Cause fact carries the transport failure without the method-and-URL
	// prefix the Tried fact already spells out.
	if strings.Contains(facts["Cause"], address) {
		t.Errorf("Cause = %q, want the url.Error envelope stripped", facts["Cause"])
	}
	if !strings.Contains(strings.Join(diagnostic.Hints, "\n"), config.APIURLEnv) {
		t.Errorf("hints %q never name %s", diagnostic.Hints, config.APIURLEnv)
	}
}

// Each source gets credited for the address, because the remedy differs:
// a default wants a local control plane, an override wants checking, and a
// file wants editing.
func TestAPIURLIsCreditedToWhateverSetIt(t *testing.T) {
	path, err := config.Path()
	if err != nil {
		t.Fatalf("config.Path() error = %v", err)
	}
	tests := []struct {
		name    string
		env     string
		baseURL string
		want    string
	}{
		{name: "default", baseURL: config.DefaultAPIURL, want: "built-in default"},
		{name: "environment", env: "https://api.example.com", baseURL: "https://api.example.com", want: config.APIURLEnv},
		{name: "file", baseURL: "http://127.0.0.1:8080", want: path},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(config.APIURLEnv, test.env)
			if got := describeAPIURL(test.baseURL); !strings.Contains(got, test.want) {
				t.Errorf("describeAPIURL(%q) = %q, want it to name %q", test.baseURL, got, test.want)
			}
		})
	}
}

// Calling a configured address a default would send someone looking for a
// setting they never made.
func TestConfiguredAddressIsNotCalledADefault(t *testing.T) {
	t.Setenv(config.APIURLEnv, "https://api.example.com")
	for _, hint := range New("https://api.example.com", "").reachabilityHints() {
		if strings.Contains(hint, "built-in default, which expects") {
			t.Errorf("hint %q calls a configured address a default", hint)
		}
	}
}

// Someone who installed a release never chose an address, so the failure must
// not point them at the override that exists for developing Shed. This is the
// end-user path: the only thing they can act on is their connection.
func TestUnconfiguredFailureNeverNamesTheOverride(t *testing.T) {
	t.Setenv(config.APIURLEnv, "")

	hints := strings.Join(New(config.DefaultAPIURL, "").reachabilityHints(), "\n")
	if strings.Contains(hints, config.APIURLEnv) {
		t.Errorf("hints offer %s to someone who never configured one: %q", config.APIURLEnv, hints)
	}
	if strings.Contains(hints, "control plane running on this machine") {
		t.Errorf("hints tell a released CLI to run a control plane locally: %q", hints)
	}
}

// Whoever set an address can be asked about it, so naming the setting is
// useful there and only there.
func TestConfiguredFailureStillNamesTheSetting(t *testing.T) {
	t.Setenv(config.APIURLEnv, "https://api.example.com")

	hints := strings.Join(New("https://api.example.com", "").reachabilityHints(), "\n")
	if !strings.Contains(hints, config.APIURLEnv) {
		t.Errorf("hints never name the setting that caused this: %q", hints)
	}
}

// Ctrl-C during a request is the caller's decision. Reporting it as an
// unreachable API would send someone debugging a network that is fine.
func TestCancelledRequestIsNotReportedAsUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := New(server.URL, "").ExchangeLoginSession(ctx, "session", "code")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if _, ok := diag.As(err); ok {
		t.Errorf("cancellation was dressed up as a diagnostic: %v", err)
	}
}
