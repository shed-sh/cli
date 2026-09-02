package e2e_test

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestCLISourceToRunningDockerEndToEnd(t *testing.T) {
	requireDockerE2E(t)
	home := isolatedHome(t)
	root := copyFixture(t, "docker-node")

	init := runShed(t, home, root, 0, "init", "--output", "json")
	requireShedSuccess(t, init, "init", "--output", "json")

	deploy := runShed(t, home, root, 5*time.Minute, "deploy", ".", "--output", "json")
	requireShedSuccess(t, deploy, "deploy", ".")
	var result struct {
		Outcome  string `json:"outcome"`
		Instance *struct {
			ID      string `json:"instanceId"`
			URL     string `json:"url"`
			ImageID string `json:"imageId"`
		} `json:"instance"`
	}
	decodeJSON(t, deploy.stdout, &result)
	if result.Instance != nil {
		instance := *result.Instance
		t.Cleanup(func() {
			_ = runShed(t, home, root, 30*time.Second, "destroy", instance.ID)
			if instance.ImageID == "" {
				return
			}
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = exec.CommandContext(cleanupCtx, "docker", "image", "rm", "--force", instance.ImageID).Run()
		})
	}
	if result.Outcome != "ready" || result.Instance == nil || result.Instance.URL == "" {
		t.Fatalf("deploy = %#v\nstderr:\n%s", result, deploy.stderr)
	}
	assertHTTPBody(t, result.Instance.URL, "shed-e2e-v1\n")

	stopped := runShed(t, home, root, 30*time.Second, "stop", result.Instance.ID)
	requireShedSuccess(t, stopped, "stop", result.Instance.ID)
}
