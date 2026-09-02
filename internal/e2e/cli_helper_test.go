package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	code := m.Run()
	if shedPath != "" {
		_ = os.RemoveAll(filepath.Dir(shedPath))
	}
	os.Exit(code)
}

const cliE2EVersion = "e2e"

var (
	shedOnce sync.Once
	shedPath string
	shedErr  error
)

type cliResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func shedBinary(t *testing.T) string {
	t.Helper()
	shedOnce.Do(func() {
		root, err := callerRepositoryRoot()
		if err != nil {
			shedErr = err
			return
		}
		dir, err := os.MkdirTemp("", "shed-e2e-bin-")
		if err != nil {
			shedErr = fmt.Errorf("create binary dir: %w", err)
			return
		}
		bin := filepath.Join(dir, "shed")
		cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+cliE2EVersion, "-o", bin, "./cmd/shed")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		output, err := cmd.CombinedOutput()
		if err != nil {
			_ = os.RemoveAll(dir)
			shedErr = fmt.Errorf("build shed: %w\n%s", err, output)
			return
		}
		shedPath = bin
	})
	if shedErr != nil {
		t.Fatal(shedErr)
	}
	return shedPath
}

func callerRepositoryRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("runtime.Caller failed")
	}
	return filepath.Abs(filepath.Join(filepath.Dir(thisFile), "..", ".."))
}

func isolatedHome(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	root, err := callerRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(root, "testdata", "e2e", name)
	dst := filepath.Join(t.TempDir(), "application")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy fixture %s: %v", name, err)
	}
	return dst
}

func runShed(t *testing.T, home, dir string, timeout time.Duration, args ...string) cliResult {
	t.Helper()
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, shedBinary(t), args...)
	cmd.Dir = dir
	cmd.Env = isolatedEnv(home)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := cliResult{stdout: stdout.String(), stderr: stderr.String(), exitCode: 0}
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("shed %s timed out after %s\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), timeout, result.stdout, result.stderr)
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("shed %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, result.stdout, result.stderr)
		}
		result.exitCode = exitErr.ExitCode()
	}
	return result
}

func requireShedSuccess(t *testing.T, result cliResult, args ...string) {
	t.Helper()
	if result.exitCode != 0 {
		t.Fatalf("shed %s exited %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), result.exitCode, result.stdout, result.stderr)
	}
}

func decodeJSON(t *testing.T, stdout string, dest any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(stdout))
	if err := decoder.Decode(dest); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, stdout)
	}
	if decoder.More() {
		t.Fatalf("stdout carried more than one JSON value:\n%s", stdout)
	}
}

func isolatedEnv(home string) []string {
	drop := map[string]struct{}{
		"HOME":            {},
		"XDG_CONFIG_HOME": {},
		"XDG_DATA_HOME":   {},
		"XDG_STATE_HOME":  {},
		"XDG_CACHE_HOME":  {},
		"SHED_TOKEN":      {},
		"SHED_API_URL":    {},
		"SHED_PORTAL_URL": {},
	}
	env := make([]string, 0, 16)
	for _, kv := range os.Environ() {
		key, _, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		if _, skip := drop[key]; skip {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
	)
}
