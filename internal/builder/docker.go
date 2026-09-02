package builder

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"shed/internal/definition"
	"shed/internal/diag"
	"shed/internal/source"
)

type Docker struct {
	Command string
	Stdout  io.Writer
	Stderr  io.Writer
}

func (docker Docker) Build(ctx context.Context, archive source.Archive) (Image, error) {
	command := docker.Command
	if command == "" {
		command = os.Getenv("SHED_DOCKER_BIN")
	}
	if command == "" {
		command = "docker"
	}
	if _, err := exec.LookPath(command); err != nil {
		return Image{}, &diag.Error{
			Code:    "runtime_unavailable",
			Summary: "Shed builds locally with Docker, and Docker was not found.",
			Facts:   []diag.Fact{{Label: "Looked for", Value: command + " on PATH"}},
			Hints: []string{
				"Install or start Docker, then run the command again",
				"Or package the project without building it: shed deploy --mock",
			},
			Cause: err,
		}
	}
	contextRoot, err := os.MkdirTemp("", "shed-build-context-")
	if err != nil {
		return Image{}, fmt.Errorf("create Docker build context: %w", err)
	}
	defer func() { _ = os.RemoveAll(contextRoot) }()
	manifest, err := source.Extract(archive, contextRoot)
	if err != nil {
		return Image{}, fmt.Errorf("extract build input: %w", err)
	}
	imageTag := "shed-local:" + digestPrefix(archive.Content.Digest)
	if err := os.WriteFile(filepath.Join(contextRoot, "Dockerfile"), []byte(dockerfileFor(manifest)), 0o600); err != nil {
		return Image{}, fmt.Errorf("write Dockerfile: %w", err)
	}
	stdout, stderr := docker.Stdout, docker.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if err := run(ctx, command, []string{"build", "--tag", imageTag, contextRoot}, stdout, stderr); err != nil {
		return Image{}, fmt.Errorf("docker build: %w", err)
	}
	inspect := exec.CommandContext(ctx, command, "image", "inspect", "--format", "{{.Id}}", imageTag)
	data, err := inspect.Output()
	if err != nil {
		return Image{}, fmt.Errorf("inspect Docker image: %w", err)
	}
	digest := strings.TrimSpace(string(data))
	if digest == "" {
		return Image{}, fmt.Errorf("inspect Docker image: empty image identity")
	}
	return Image{ID: imageTag, Digest: digest}, nil
}

func dockerfileFor(manifest definition.Manifest) string {
	workingDirectory := manifest.Run.WorkingDirectory
	if workingDirectory == "" {
		workingDirectory = "/app"
	}
	lines := []string{"FROM " + manifest.Build.Image, "WORKDIR " + workingDirectory, "COPY . ."}
	for _, command := range manifest.Build.Commands {
		lines = append(lines, "RUN "+jsonArray(command))
	}
	keys := make([]string, 0, len(manifest.Run.Environment))
	for key := range manifest.Run.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		lines = append(lines, "ENV "+key+"="+strconv.Quote(manifest.Run.Environment[key]))
	}
	lines = append(lines, "EXPOSE "+strconv.Itoa(manifest.Run.Port))
	if manifest.Run.User != "" {
		lines = append(lines, "USER "+manifest.Run.User)
	}
	lines = append(lines, "CMD "+jsonArray(manifest.Run.Command))
	return strings.Join(lines, "\n") + "\n"
}

func run(ctx context.Context, command string, args []string, stdout, stderr io.Writer) error {
	process := exec.CommandContext(ctx, command, args...)
	process.Stdout = stdout
	process.Stderr = stderr
	return process.Run()
}

func digestPrefix(value string) string {
	value = strings.TrimPrefix(value, "sha256:")
	if len(value) >= 16 {
		return value[:16]
	}
	if value == "" {
		return "unknown"
	}
	return value
}

func jsonArray(values []string) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.Quote(value)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
