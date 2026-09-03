package e2e_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"shed/internal/builder"
	localruntime "shed/internal/runtime"
	"shed/internal/source"
	"shed/internal/state"
	"shed/internal/workflow"
)

func TestPackagedArchiveDockerEndToEnd(t *testing.T) {
	requireDockerE2E(t)
	root := filepath.Join(t.TempDir(), "application")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(repositoryRoot(t), "testdata", "e2e", "docker-node")
	if err := os.CopyFS(root, os.DirFS(fixture)); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	generated, err := workflow.GenerateDefinition(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := source.Prepare(root, filepath.Join(t.TempDir(), "application.tar.gz"), generated.Source, generated.Manifest.Content.Include...)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("remove original source: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	image, err := (builder.Docker{}).Build(ctx, archive)
	if err != nil {
		t.Fatalf("build archive after source removal: %v", err)
	}
	runner := &localruntime.Docker{}
	instance, err := runner.Start(ctx, "inst_archive_e2e", image.ID, image.Digest, 0, generated.Manifest.Run.Port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = runner.Remove(cleanupCtx, instance)
		_ = exec.CommandContext(cleanupCtx, "docker", "image", "rm", "--force", image.ID).Run()
	})
	if _, err := runner.WaitReady(ctx, instance, 45*time.Second); err != nil {
		t.Fatal(err)
	}
	assertHTTPBody(t, instance.URL, "shed-e2e-v1\n")
}

func TestSourceToRunningDockerEndToEnd(t *testing.T) {
	requireDockerE2E(t)

	root := filepath.Join(t.TempDir(), "application")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(repositoryRoot(t), "testdata", "e2e", "docker-node")
	if err := os.CopyFS(root, os.DirFS(fixture)); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}

	store := state.NewStoreAt(filepath.Join(t.TempDir(), "instances.json"))
	runner := &localruntime.Docker{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var images []string
	var current *localruntime.Instance
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if current != nil {
			_ = runner.Remove(cleanupCtx, *current)
		}
		for _, image := range images {
			_ = exec.CommandContext(cleanupCtx, "docker", "image", "rm", "--force", image).Run()
		}
	})

	first := deployFixture(t, ctx, root, &store, runner)
	current = first.Instance
	images = append(images, first.Instance.ImageID)
	assertHTTPBody(t, first.Instance.URL, "shed-e2e-v1\n")

	second := deployFixture(t, ctx, root, &store, runner)
	if second.Instance.Container != first.Instance.Container || second.Instance.URL != first.Instance.URL {
		t.Fatalf("unchanged rerun created a new runtime: first=%+v second=%+v", first.Instance, second.Instance)
	}
	if second.NextOperation != "reuse_instance" {
		t.Fatalf("unchanged rerun next operation = %q", second.NextOperation)
	}

	serverFile := filepath.Join(root, "server.js")
	data, err := os.ReadFile(serverFile)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), "shed-e2e-v1", "shed-e2e-v2", 1)
	if err := os.WriteFile(serverFile, []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}

	third := deployFixture(t, ctx, root, &store, runner)
	current = third.Instance
	images = append(images, third.Instance.ImageID)
	if third.Instance.ID != first.Instance.ID || third.Instance.URL != first.Instance.URL || third.Instance.HostPort != first.Instance.HostPort {
		t.Fatalf("update did not preserve application identity and URL: first=%+v third=%+v", first.Instance, third.Instance)
	}
	if third.Instance.Container == first.Instance.Container {
		t.Fatalf("changed source reused old container %q", third.Instance.Container)
	}
	if third.Source.ContentDigest == first.Source.ContentDigest {
		t.Fatal("changed source did not change the content digest")
	}
	assertHTTPBody(t, third.Instance.URL, "shed-e2e-v2\n")
}

func requireDockerE2E(t *testing.T) {
	t.Helper()
	if os.Getenv("SHED_E2E_DOCKER") != "1" {
		t.Skip("set SHED_E2E_DOCKER=1 to run the Docker end-to-end test")
	}
	if output, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Fatalf("Docker is required when SHED_E2E_DOCKER=1: %v: %s", err, strings.TrimSpace(string(output)))
	}
}

func deployFixture(t *testing.T, ctx context.Context, root string, store *state.Store, runner *localruntime.Docker) workflow.Result {
	t.Helper()
	result, err := workflow.Run(ctx, workflow.Options{
		Root:         root,
		State:        store,
		Runtime:      runner,
		ReadyTimeout: 45 * time.Second,
	})
	if err != nil {
		t.Fatalf("deploy fixture: %v", err)
	}
	if result.Outcome != "ready" || result.Instance == nil || result.Runtime == nil {
		t.Fatalf("deployment result = %+v", result)
	}
	if result.Runtime.StatusCode != http.StatusOK {
		t.Fatalf("readiness status = %d", result.Runtime.StatusCode)
	}
	return result
}

func assertHTTPBody(t *testing.T, url, expected string) {
	t.Helper()
	response, err := http.Get(url + "/")
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(data) != expected {
		t.Fatalf("GET %s = HTTP %d %q, want HTTP 200 %q", url, response.StatusCode, data, expected)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(fmt.Errorf("resolve repository root: %w", err))
	}
	return root
}
