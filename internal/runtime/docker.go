package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"shed/internal/diag"
)

type Instance struct {
	ID        string `json:"instanceId"`
	Container string `json:"containerId"`
	HostPort  int    `json:"hostPort"`
	URL       string `json:"url"`
	ImageID   string `json:"imageId"`
	Digest    string `json:"imageDigest"`
}

type Docker struct {
	Command string
	Stdout  io.Writer
	Stderr  io.Writer
}

type ReadinessResult struct {
	StatusCode int           `json:"statusCode"`
	URL        string        `json:"url"`
	Elapsed    time.Duration `json:"-"`
}

func (docker Docker) Start(ctx context.Context, instanceID, imageID, imageDigest string, hostPort, containerPort int) (Instance, error) {
	command := docker.command()
	if _, err := exec.LookPath(command); err != nil {
		return Instance{}, &diag.Error{
			Code:    "runtime_unavailable",
			Summary: "Shed runs projects locally with Docker, and Docker was not found.",
			Facts:   []diag.Fact{{Label: "Looked for", Value: command + " on PATH"}},
			Hints: []string{
				"Install or start Docker, then run the command again",
				"Or package the project without running it: shed deploy --mock",
			},
			Cause: err,
		}
	}
	if hostPort == 0 {
		var err error
		hostPort, err = freePort()
		if err != nil {
			return Instance{}, err
		}
	}
	if containerPort < 1 || containerPort > 65535 {
		return Instance{}, fmt.Errorf("invalid container port %d", containerPort)
	}
	args := []string{"run", "--detach", "--label", "run.shed.instance=" + instanceID, "--publish", fmt.Sprintf("127.0.0.1:%d:%d/tcp", hostPort, containerPort), imageID}
	output, err := docker.output(ctx, args...)
	if err != nil {
		return Instance{}, fmt.Errorf("start runtime: %w", err)
	}
	container := strings.TrimSpace(output)
	if container == "" {
		return Instance{}, errors.New("start runtime: Docker returned no container ID")
	}
	return Instance{ID: instanceID, Container: container, HostPort: hostPort, URL: fmt.Sprintf("http://127.0.0.1:%d", hostPort), ImageID: imageID, Digest: imageDigest}, nil
}

func (docker Docker) WaitReady(ctx context.Context, instance Instance, timeout time.Duration) (ReadinessResult, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	var lastErr error
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, instance.URL+"/", nil)
		if err != nil {
			return ReadinessResult{}, err
		}
		response, err := client.Do(request)
		if err == nil {
			if time.Now().After(deadline) {
				_ = response.Body.Close()
				return ReadinessResult{}, errors.New("readiness_timeout: response arrived after deadline")
			}
			status := response.StatusCode
			_ = response.Body.Close()
			if status >= 500 {
				return ReadinessResult{StatusCode: status, URL: instance.URL}, fmt.Errorf("http_5xx: runtime returned HTTP %d", status)
			}
			return ReadinessResult{StatusCode: status, URL: instance.URL}, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return ReadinessResult{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return ReadinessResult{}, fmt.Errorf("readiness_timeout: %w", lastErr)
}

func (docker Docker) Logs(ctx context.Context, instance Instance, output io.Writer) error {
	return docker.run(ctx, []string{"logs", instance.Container}, output, docker.Stderr)
}

func (docker Docker) Stop(ctx context.Context, instance Instance) error {
	return docker.run(ctx, []string{"stop", "--time", "10", instance.Container}, docker.Stdout, docker.Stderr)
}

func (docker Docker) Remove(ctx context.Context, instance Instance) error {
	return docker.run(ctx, []string{"rm", "--force", instance.Container}, docker.Stdout, docker.Stderr)
}

func (docker Docker) command() string {
	if docker.Command != "" {
		return docker.Command
	}
	if value := os.Getenv("SHED_DOCKER_BIN"); value != "" {
		return value
	}
	return "docker"
}

func (docker Docker) output(ctx context.Context, args ...string) (string, error) {
	process := exec.CommandContext(ctx, docker.command(), args...)
	data, err := process.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(data))
		if message != "" {
			return "", fmt.Errorf("%w: %s", err, message)
		}
	}
	return string(data), err
}

func (docker Docker) run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	process := exec.CommandContext(ctx, docker.command(), args...)
	process.Stdout = stdout
	process.Stderr = stderr
	return process.Run()
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate runtime port: %w", err)
	}
	defer func() { _ = listener.Close() }()
	address, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("allocate runtime port: unexpected address")
	}
	return address.Port, nil
}
