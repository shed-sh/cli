package workflow

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"shed/internal/definition"
	"shed/internal/diag"
	"shed/internal/shedfile"
)

func TestInitializeWritesDefinitionOnce(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, root, "package.json", `{"scripts":{"start":"node index.js"}}`)
	writeWorkflowFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeWorkflowFile(t, root, "index.js", "console.log('ready')\n")
	generated, created, filename, err := Initialize(root, nil, "yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !created || generated.Manifest.Build.Image == "" || filename != definition.ManifestFileName {
		t.Fatalf("generated=%+v created=%v filename=%q", generated, created, filename)
	}
	if _, err := os.Stat(filepath.Join(root, definition.ManifestFileName)); err != nil {
		t.Fatal(err)
	}
	_, created, _, err = Initialize(root, nil, "yaml")
	if err != nil || created {
		t.Fatalf("second init created=%v err=%v", created, err)
	}
}

func TestExistingDefinitionIsAuthoritativeWithoutRailpackDetection(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, root, "main.py", "print('hello')\n")
	manifest := definition.Manifest{
		APIVersion: definition.ManifestAPIVersion,
		Kind:       definition.ManifestKind,
		Content:    definition.ManifestContent{Include: []string{"main.py"}},
		Build:      definition.ManifestBuild{Image: "python:3.13", Commands: [][]string{{"python", "-m", "compileall", "."}}},
		Run:        definition.ManifestRun{Command: []string{"python", "main.py"}, Port: 8000},
	}
	data, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	writeWorkflowFile(t, root, definition.ManifestFileName, string(data))
	result, err := Run(context.Background(), Options{Root: root, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Application.Build.Image != "python:3.13" || result.Source.FileCount != 2 {
		t.Fatalf("result=%+v", result)
	}
}

func TestRunProducesMockUploadReceiptWithoutNetwork(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, root, "package.json", `{
  "scripts": {"start": "node src/server.js"},
  "dependencies": {"express": "5.1.0"},
  "packageManager": "pnpm@10.0.0",
  "engines": {"node": "24.x"}
}`)
	writeWorkflowFile(t, root, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	writeWorkflowFile(t, root, "src/server.js", "console.log('ready')\n")
	archivePath := filepath.Join(t.TempDir(), "application.tar.gz")
	result, err := Run(context.Background(), Options{
		Root:      root,
		Archive:   archivePath,
		RequestID: "request-123",
		Mock:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "uploaded_mock" || result.RequestID != "request-123" {
		t.Fatalf("result = %#v", result)
	}
	if result.Source.ID == "" || result.Deployment == nil || result.Deployment.State != "awaiting_builder" {
		t.Fatalf("mock receipt = %#v", result)
	}
	if result.Source.ArchivePath != archivePath {
		t.Fatalf("archive path = %q", result.Source.ArchivePath)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("retained archive: %v", err)
	}
}

func TestRunDryRunSkipsDeployment(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, root, "package.json", `{"scripts":{"start":"node index.js"},"dependencies":{"express":"5.1.0"}}`)
	writeWorkflowFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeWorkflowFile(t, root, "index.js", "console.log('ready')\n")
	result, err := Run(context.Background(), Options{Root: root, DryRun: true, Mock: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != "prepared" || result.Deployment != nil || result.NextOperation != "mock_upload" {
		t.Fatalf("result = %#v", result)
	}
}

// Real Railpack detection over a real Go layout: the conventional cmd/<name>
// binary whose built artifact the project also gitignores at the root.
func TestGoProjectIsDetectedPackagedAndLowered(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, root, "go.mod", "module example.com/service\n\ngo 1.24\n")
	writeWorkflowFile(t, root, "cmd/service/main.go", "package main\n\nfunc main() {}\n")
	writeWorkflowFile(t, root, "internal/api/handler.go", "package api\n")
	writeWorkflowFile(t, root, ".gitignore", "/service\n")
	writeWorkflowFile(t, root, "service", "stale binary\n")

	generated, err := GenerateDefinition(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	manifest := generated.Manifest
	if manifest.Build.Image != "golang:1.24" {
		t.Fatalf("image = %q", manifest.Build.Image)
	}
	if len(manifest.Run.Command) == 0 || manifest.Run.Command[0] != "./out" {
		t.Fatalf("run command = %#v", manifest.Run.Command)
	}
	build := manifest.Build.Commands[len(manifest.Build.Commands)-1]
	if len(build) < 2 || build[0] != "go" || build[1] != "build" {
		t.Fatalf("build command = %#v", manifest.Build.Commands)
	}
	// The source of the ignored root binary must survive into the bundle.
	if !slices.Contains(manifest.Content.Include, "cmd") {
		t.Fatalf("include = %#v", manifest.Content.Include)
	}
	if slices.Contains(manifest.Content.Include, "service") {
		t.Fatalf("ignored binary was packaged: %#v", manifest.Content.Include)
	}

	prepared, err := Prepare(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	var packaged []string
	for _, entry := range prepared.Archive.Content.Files {
		packaged = append(packaged, entry.Path)
	}
	if !slices.Contains(packaged, "cmd/service/main.go") {
		t.Fatalf("archive = %v", packaged)
	}
}

// A SHED program is evaluated at prepare time and only its evaluation ships:
// the archive carries the resulting SHED.yaml and never the program itself.
func TestShedProgramResolvesAndPackagesItsEvaluation(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, root, "go.mod", "module example.com/service\n\ngo 1.24\n")
	writeWorkflowFile(t, root, "cmd/service/main.go", "package main\n\nfunc main() {}\n")
	writeWorkflowFile(t, root, "SHED", `b = build(
    srcs = ["cmd", "go.mod"],
    image = "golang:1.24",
    commands = [["go", "build", "-o", "out", "./cmd/service"]],
)

http_app(
    name = "service",
    build = b,
    cmd = ["./out"],
    port = 8080,
)
`)
	prepared, err := Prepare(Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = prepared.Close() })
	if prepared.Manifest.Build.Image != "golang:1.24" || prepared.Manifest.Metadata.Name != "service" {
		t.Fatalf("manifest = %+v", prepared.Manifest)
	}
	var packaged []string
	for _, entry := range prepared.Archive.Content.Files {
		packaged = append(packaged, entry.Path)
	}
	if !slices.Contains(packaged, definition.ManifestFileName) || !slices.Contains(packaged, "cmd/service/main.go") {
		t.Fatalf("archive = %v", packaged)
	}
	if slices.Contains(packaged, "SHED") {
		t.Fatalf("the program leaked into the archive: %v", packaged)
	}
	reparsed, err := definition.ParseManifest(prepared.ManifestYAML)
	if err != nil || reparsed.Run.Port != 8080 {
		t.Fatalf("embedded YAML: %+v err=%v", reparsed, err)
	}
}

func TestTwoDefinitionsAreAConflict(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, root, "go.mod", "module example.com/service\n\ngo 1.24\n")
	writeWorkflowFile(t, root, "SHED", "b = 1\n")
	writeWorkflowFile(t, root, definition.ManifestFileName, "apiVersion: shed.run/v1alpha1\n")
	_, err := ResolveDefinition(root, nil)
	diagnostic, ok := diag.As(err)
	if !ok || diagnostic.Code != "definition_conflict" {
		t.Fatalf("err = %v", err)
	}
	_, _, _, err = Initialize(root, nil, "yaml")
	if diagnostic, ok = diag.As(err); !ok || diagnostic.Code != "definition_conflict" {
		t.Fatalf("init err = %v", err)
	}
}

// Deploy-style resolution stops at the first diagnostic but says how many
// there are and where to see the rest.
func TestBrokenProgramPointsAtCheck(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, root, "go.mod", "module example.com/service\n\ngo 1.24\n")
	writeWorkflowFile(t, root, "SHED", `b = build(
    srcs = ["go.mo"],
    image = "golang:1.24",
)
c = build(
    srcs = ["go.mod"],
    image = "golang:1.24",
)
`)
	_, err := ResolveDefinition(root, nil)
	diagnostic, ok := diag.As(err)
	if !ok || diagnostic.Code != "unknown_src" {
		t.Fatalf("err = %v", err)
	}
	hints := strings.Join(diagnostic.Hints, "\n")
	if !strings.Contains(hints, "shed check") {
		t.Fatalf("hints = %q", hints)
	}
}

func TestInitializeWritesShedFormat(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFile(t, root, "go.mod", "module example.com/service\n\ngo 1.24\n")
	writeWorkflowFile(t, root, "cmd/service/main.go", "package main\n\nfunc main() {}\n")
	generated, created, filename, err := Initialize(root, nil, "shed")
	if err != nil {
		t.Fatal(err)
	}
	if !created || filename != shedfile.FileName {
		t.Fatalf("created=%v filename=%q", created, filename)
	}
	if _, err := os.Stat(filepath.Join(root, definition.ManifestFileName)); !os.IsNotExist(err) {
		t.Fatalf("SHED.yaml should not exist: %v", err)
	}
	// A second init validates the program instead of regenerating, and its
	// evaluation reproduces what init reported.
	validated, created, filename, err := Initialize(root, nil, "shed")
	if err != nil || created || filename != shedfile.FileName {
		t.Fatalf("revalidate: created=%v filename=%q err=%v", created, filename, err)
	}
	if !slices.Equal(validated.Manifest.Content.Include, generated.Manifest.Content.Include) ||
		validated.Manifest.Build.Image != generated.Manifest.Build.Image ||
		validated.Manifest.Run.Port != generated.Manifest.Run.Port {
		t.Fatalf("evaluation drifted: %+v vs %+v", validated.Manifest, generated.Manifest)
	}
}

// recordingProgress captures every phase callback the workflow makes so tests
// can assert on the phase sequence without depending on a real terminal.
type recordingProgress struct {
	events []string
}

func (r *recordingProgress) Start(phase string) {
	r.events = append(r.events, "start:"+phase)
}
func (r *recordingProgress) Detail(text string) {
	r.events = append(r.events, "detail:"+text)
}
func (r *recordingProgress) StageDone(phase, detail string) {
	if detail == "" {
		r.events = append(r.events, "done:"+phase)
		return
	}
	r.events = append(r.events, "done:"+phase+":"+detail)
}
func (r *recordingProgress) Fail(err error) {
	if err != nil {
		r.events = append(r.events, "fail:"+err.Error())
		return
	}
	r.events = append(r.events, "fail:")
}

func TestRunEmitsPackagePhaseThroughProgressObserver(t *testing.T) {
	// Even in mock mode (no build/start/ready), Run must emit the "package"
	// phase so the CLI's local Progress panel gets a start+done for it.
	root := t.TempDir()
	writeWorkflowFile(t, root, "package.json", `{"scripts":{"start":"node index.js"},"dependencies":{"express":"5.1.0"}}`)
	writeWorkflowFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeWorkflowFile(t, root, "index.js", "console.log('ready')\n")

	report := &recordingProgress{}
	_, err := Run(context.Background(), Options{Root: root, DryRun: true, Mock: true, Progress: report})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.events) == 0 || report.events[0] != "start:package" {
		t.Fatalf("first event = %q, want start:package. all = %v", report.events, report.events)
	}
	var sawDone bool
	for _, event := range report.events {
		if strings.HasPrefix(event, "done:package") {
			sawDone = true
		}
	}
	if !sawDone {
		t.Fatalf("no done:package in events: %v", report.events)
	}
}

func TestRunReportsPackageFailureThroughProgressObserver(t *testing.T) {
	// A directory Railpack cannot classify must produce a fail: event so a
	// live Progress panel marks the "package" stage red instead of leaving
	// the spinner running forever.
	root := t.TempDir()
	writeWorkflowFile(t, root, "README.md", "no application here\n")

	report := &recordingProgress{}
	_, err := Run(context.Background(), Options{Root: root, DryRun: true, Mock: true, Progress: report})
	if err == nil {
		t.Fatal("Run succeeded on an unclassifiable project")
	}
	sawFail := false
	for _, event := range report.events {
		if strings.HasPrefix(event, "fail:") {
			sawFail = true
		}
	}
	if !sawFail {
		t.Fatalf("no fail: event: %v", report.events)
	}
}

func writeWorkflowFile(t *testing.T, root, relative, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
