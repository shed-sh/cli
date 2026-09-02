package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeReleaseHost stands in for the GitHub releases of shed-sh/cli: a
// /latest redirect naming the newest tag, and each release's installer.
// The installer stub behaves like the real one: it writes an executable shed
// into SHED_INSTALL_DIR that answers `shed version` with the release version.
func fakeReleaseHost(t *testing.T, latest string, installerExit int, installedVersion string) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/latest":
			http.Redirect(w, r, server.URL+"/tag/v"+latest, http.StatusFound)
		case strings.HasSuffix(r.URL.Path, "/shed-installer.sh"):
			version := strings.TrimPrefix(strings.Split(r.URL.Path, "/")[2], "v")
			if installedVersion != "" {
				version = installedVersion
			}
			_, _ = fmt.Fprintf(w, `#!/bin/sh
set -eu
[ "${SHED_NO_MODIFY_PATH:-}" = "1" ] || { echo "PATH would be modified" >&2; exit 3; }
[ -n "${SHED_INSTALL_DIR:-}" ] || { echo "no install dir" >&2; exit 3; }
[ %d -eq 0 ] || exit %d
printf '#!/bin/sh\necho %s\n' > "$SHED_INSTALL_DIR/shed"
chmod 0755 "$SHED_INSTALL_DIR/shed"
`, installerExit, installerExit, version)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv(releasesURLEnv, server.URL)
	return server
}

// upgradeApp builds an App whose executable lives in a temp directory, which
// is where the fake installer will drop the new binary.
func upgradeApp(t *testing.T, version string) (*App, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	directory := t.TempDir()
	executable := filepath.Join(directory, "shed")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\necho "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, version)
	app.executablePath = func() (string, error) { return executable, nil }
	return app, &stdout, &stderr, executable
}

func TestUpgradeReplacesTheBinary(t *testing.T) {
	fakeReleaseHost(t, "0.2.0", 0, "")
	app, stdout, stderr, executable := upgradeApp(t, "0.1.0")

	if code := app.Run(context.Background(), []string{"upgrade"}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Upgraded shed 0.1.0 → 0.2.0.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	installed, err := os.ReadFile(executable)
	if err != nil || !strings.Contains(string(installed), "0.2.0") {
		t.Fatalf("installed binary = %q, err = %v", installed, err)
	}
}

func TestUpgradeCheckReportsWithoutInstalling(t *testing.T) {
	fakeReleaseHost(t, "0.2.0", 0, "")
	app, stdout, stderr, executable := upgradeApp(t, "0.1.0")
	before, _ := os.ReadFile(executable)

	if code := app.Run(context.Background(), []string{"upgrade", "--check", "--output", "json"}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var report upgradeReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("stdout = %q: %v", stdout.String(), err)
	}
	if report.Outcome != "update_available" || report.From != "0.1.0" || report.To != "0.2.0" {
		t.Fatalf("report = %+v", report)
	}
	after, _ := os.ReadFile(executable)
	if !bytes.Equal(before, after) {
		t.Fatal("--check replaced the binary")
	}
}

func TestUpgradeReportsCurrentWhenNothingIsNewer(t *testing.T) {
	fakeReleaseHost(t, "0.1.0", 0, "")
	app, stdout, stderr, _ := upgradeApp(t, "0.1.0")

	if code := app.Run(context.Background(), []string{"upgrade"}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already the newest release") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestUpgradeHonorsAPinnedVersion(t *testing.T) {
	fakeReleaseHost(t, "0.3.0", 0, "")
	app, stdout, stderr, _ := upgradeApp(t, "0.3.0")

	// v-prefixed, older than current: an explicit switch, not an error.
	if code := app.Run(context.Background(), []string{"upgrade", "--version", "v0.2.0"}); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Upgraded shed 0.3.0 → 0.2.0.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestUpgradeRefusesASourceBuild(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "dev")

	if code := app.Run(context.Background(), []string{"upgrade"}); code == 0 {
		t.Fatal("Run() upgraded a source build")
	}
	if !strings.Contains(stderr.String(), "source build") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUpgradeRefusesAPackageManagedInstall(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "0.1.0")
	app.executablePath = func() (string, error) {
		return "/opt/homebrew/Cellar/shed/0.1.0/bin/shed", nil
	}

	if code := app.Run(context.Background(), []string{"upgrade"}); code == 0 {
		t.Fatal("Run() upgraded a Homebrew install")
	}
	if !strings.Contains(stderr.String(), "brew upgrade shed-sh/tap/shed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUpgradeExplainsAnUnreachableReleaseHost(t *testing.T) {
	t.Setenv(releasesURLEnv, "http://127.0.0.1:1/releases")
	app, _, stderr, _ := upgradeApp(t, "0.1.0")

	if code := app.Run(context.Background(), []string{"upgrade"}); code == 0 {
		t.Fatal("Run() succeeded against an unreachable host")
	}
	if !strings.Contains(stderr.String(), "could not reach the release host") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUpgradeExplainsAMissingLatestRelease(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No releases published: GitHub redirects /latest to the bare
		// releases listing instead of a /tag/<v> page.
		http.Redirect(w, r, server.URL+"/", http.StatusFound)
	}))
	defer server.Close()
	t.Setenv(releasesURLEnv, server.URL)
	app, _, stderr, _ := upgradeApp(t, "0.1.0")

	if code := app.Run(context.Background(), []string{"upgrade"}); code == 0 {
		t.Fatal("Run() succeeded with no published releases")
	}
	if !strings.Contains(stderr.String(), "did not name a latest version") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUpgradeReportsAFailedInstaller(t *testing.T) {
	fakeReleaseHost(t, "0.2.0", 7, "")
	app, _, stderr, executable := upgradeApp(t, "0.1.0")
	before, _ := os.ReadFile(executable)

	if code := app.Run(context.Background(), []string{"upgrade"}); code == 0 {
		t.Fatal("Run() succeeded although the installer failed")
	}
	if !strings.Contains(stderr.String(), "installer did not finish") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	after, _ := os.ReadFile(executable)
	if !bytes.Equal(before, after) {
		t.Fatal("a failed installer still replaced the binary")
	}
}

func TestUpgradeReportsAWrongInstalledVersion(t *testing.T) {
	// The installer claims success but drops a binary answering as 0.0.1.
	fakeReleaseHost(t, "0.2.0", 0, "0.0.1")
	app, _, stderr, _ := upgradeApp(t, "0.1.0")

	if code := app.Run(context.Background(), []string{"upgrade"}); code == 0 {
		t.Fatal("Run() reported an upgrade the binary does not confirm")
	}
	if !strings.Contains(stderr.String(), "does not answer as 0.2.0") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
