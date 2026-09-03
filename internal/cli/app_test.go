package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"shed/internal/clispec"
	"shed/internal/definition"
	"shed/internal/execution"
	"shed/internal/workflow"
)

type cliRemoteBackend struct {
	request execution.Request
	status  execution.Deployment
}

func (backend *cliRemoteBackend) Submit(_ context.Context, request execution.Request) (execution.Deployment, error) {
	backend.request = request
	return backend.status, nil
}

func (backend *cliRemoteBackend) Status(context.Context, string) (execution.Deployment, error) {
	return backend.status, nil
}

func (backend *cliRemoteBackend) Stream(_ context.Context, _ string, cursor string, _ execution.Stage, _ bool, _ func(execution.Record) error) (string, error) {
	return cursor, nil
}

func (backend *cliRemoteBackend) Cancel(context.Context, string) (execution.Deployment, error) {
	backend.status.State = execution.StateCancelling
	return backend.status, nil
}

// TestDeployAllowsOptionsAfterDirectory pins the arg reordering that lets
// `shed deploy . --dry-run` work, and with it the value-option table that
// clispec derives from the flag kinds. A value flag missing from that table
// would leave its value stranded as a second positional argument, which is why
// this asserts through Bind rather than over a list of strings.
func TestDeployAllowsOptionsAfterDirectory(t *testing.T) {
	deploy, ok := clispec.Lookup("deploy")
	if !ok {
		t.Fatal("deploy is missing from the command catalog")
	}
	binding, err := clispec.Bind(deploy, []string{
		".", "--dry-run", "--request-id", "request-1", "--archive=bundle.tar.gz", "--output", "json",
	}, io.Discard)
	if err != nil {
		t.Fatalf("Bind() error = %v", err)
	}

	if got := binding.Args(); !reflect.DeepEqual(got, []string{"."}) {
		t.Fatalf("Args() = %v, want [.]", got)
	}
	if !binding.Bool("dry-run") {
		t.Error("--dry-run was not set")
	}
	for name, want := range map[string]string{
		"request-id": "request-1",
		"archive":    "bundle.tar.gz",
		"output":     "json",
	} {
		if got := binding.String(name); got != want {
			t.Errorf("--%s = %q, want %q", name, got, want)
		}
	}
}

// TestEverySpecCommandHasAHandler and its converse make the catalog and the
// dispatch table structurally inseparable: a command can no longer be
// documented without being runnable, or runnable without being documented.
func TestEverySpecCommandHasAHandler(t *testing.T) {
	for _, command := range clispec.Commands() {
		if _, ok := handlers[command.Name]; !ok {
			t.Errorf("command %q is in the catalog but has no handler", command.Name)
		}
	}
	for name := range handlers {
		if _, ok := clispec.Lookup(name); !ok {
			t.Errorf("handler %q has no catalog entry, so help never mentions it", name)
		}
	}
}

// TestDeployHelpListsEveryDeployFlag is the regression that motivated moving the
// catalog into clispec: help used to hardcode nine of deploy's eleven flags.
func TestDeployHelpListsEveryDeployFlag(t *testing.T) {
	deploy, ok := clispec.Lookup("deploy")
	if !ok {
		t.Fatal("deploy is missing from the command catalog")
	}
	var stdout bytes.Buffer
	New(&stdout, io.Discard, "test").printHelp()

	for _, declared := range deploy.Flags {
		if !strings.Contains(stdout.String(), "--"+declared.Name) {
			t.Errorf("shed help never mentions --%s", declared.Name)
		}
	}
}

// TestDefinitionFileNameMatchesManifest keeps clispec a leaf package without
// letting its copy of the manifest filename drift from the real one.
func TestDefinitionFileNameMatchesManifest(t *testing.T) {
	if clispec.DefinitionFileName != definition.ManifestFileName {
		t.Fatalf("clispec.DefinitionFileName = %q, want %q", clispec.DefinitionFileName, definition.ManifestFileName)
	}
}

// TestLogStageValuesMatchExecution pins the spec's --stage enum to the states
// execution actually accepts, now that Bind validates the value and the handler
// no longer re-checks it.
func TestLogStageValuesMatchExecution(t *testing.T) {
	logs, ok := clispec.Lookup("logs")
	if !ok {
		t.Fatal("logs is missing from the command catalog")
	}
	stage, ok := logs.Flag("stage")
	if !ok {
		t.Fatal("logs has no --stage flag")
	}
	for _, value := range stage.Values {
		if !execution.Stage(value).Valid() {
			t.Errorf("--stage advertises %q, which execution rejects", value)
		}
	}
}

func TestDirectoryShorthandProducesJSONMockReceipt(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "package.json", `{"scripts":{"start":"node index.js"},"dependencies":{"express":"5.1.0"}}`)
	writeCLIFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeCLIFile(t, root, "index.js", "console.log('ready')\n")
	archivePath := filepath.Join(t.TempDir(), "application.tar.gz")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	exitCode := app.Run(context.Background(), []string{
		root,
		"--output", "json",
		"--archive", archivePath,
		"--request-id", "request-cli",
		"--mock",
	})
	if exitCode != 0 {
		t.Fatalf("exit = %d, stderr = %s", exitCode, stderr.String())
	}
	var result workflow.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if result.Outcome != "uploaded_mock" || result.RequestID != "request-cli" || result.Deployment == nil {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("archive: %v", err)
	}
}

func TestInitWritesAndThenValidatesDefinition(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "package.json", `{"scripts":{"start":"node index.js"}}`)
	writeCLIFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeCLIFile(t, root, "index.js", "console.log('ready')\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	if exit := app.Run(context.Background(), []string{"init", root}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(root, "SHED.yaml")); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if exit := app.Run(context.Background(), []string{"init", root}); exit != 0 || !bytes.Contains(stdout.Bytes(), []byte("already here and valid")) {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
}

// TestRemoteDeployPackagesThenReturnsResumableReceipt pins the default: a plain
// `shed <directory>` goes to the cloud. No --remote is needed, and no Docker is
// consulted.
func TestRemoteDeployPackagesThenReturnsResumableReceipt(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "package.json", `{"scripts":{"start":"node index.js"}}`)
	writeCLIFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeCLIFile(t, root, "index.js", "console.log('ready')\n")
	backend := &cliRemoteBackend{status: execution.Deployment{ID: "dep_remote", State: execution.StateAccepted}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	app.remote = backend
	exit := app.Run(context.Background(), []string{root, "--detach", "--project", "agent-app", "--request-id", "agent-request", "--output", "json"})
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	var result execution.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode receipt: %v\n%s", err, stdout.String())
	}
	if result.Outcome != "pending" || result.Deployment.ID != "dep_remote" || result.NextOperation != "status" {
		t.Fatalf("result = %#v", result)
	}
	if backend.request.ProjectName != "agent-app" || backend.request.RequestID != "agent-request" {
		t.Fatalf("request = %#v", backend.request)
	}
	if backend.request.Archive.Content.FileCount == 0 || backend.request.Archive.Digest == "" {
		t.Fatalf("archive = %#v", backend.request.Archive)
	}
}

// TestRemoteFlagStillMeansTheCloud keeps `--remote` working for scripts written
// when the cloud was opt-in. It changes nothing; it must not start rejecting.
func TestRemoteFlagStillMeansTheCloud(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "package.json", `{"scripts":{"start":"node index.js"}}`)
	writeCLIFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeCLIFile(t, root, "index.js", "console.log('ready')\n")
	backend := &cliRemoteBackend{status: execution.Deployment{ID: "dep_remote", State: execution.StateAccepted}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	app.remote = backend
	if exit := app.Run(context.Background(), []string{"deploy", root, "--remote", "--detach", "--output", "json"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if backend.request.Archive.Digest == "" {
		t.Fatalf("--remote never reached the backend: %#v", backend.request)
	}
}

// TestLocalDeployIsTheDockerPath pins that Docker is only ever consulted behind
// --local, and that the failure when it is missing points back at the default.
func TestLocalDeployIsTheDockerPath(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "package.json", `{"scripts":{"start":"node index.js"}}`)
	writeCLIFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeCLIFile(t, root, "index.js", "console.log('ready')\n")
	t.Setenv("SHED_DOCKER_BIN", "shed-test-no-docker-here")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	app.remote = &cliRemoteBackend{status: execution.Deployment{ID: "dep_remote", State: execution.StateAccepted}}
	if exit := app.Run(context.Background(), []string{"deploy", root, "--local", "--output", "json"}); exit != 1 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	var result struct {
		Outcome string            `json:"outcome"`
		Failure execution.Failure `json:"failure"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode failure: %v\n%s", err, stdout.String())
	}
	if result.Outcome != "failed" || result.Failure.Code != "runtime_unavailable" {
		t.Fatalf("result = %#v", result)
	}
	if !strings.Contains(result.Failure.Message, "shed deploy") {
		t.Fatalf("failure never points at the cloud default: %q", result.Failure.Message)
	}
}

func TestRemoteStatusAcceptsFlagsAfterDeploymentID(t *testing.T) {
	backend := &cliRemoteBackend{status: execution.Deployment{ID: "dep_remote", State: execution.StateBuilding}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	app.remote = backend
	if exit := app.Run(context.Background(), []string{"status", "dep_remote", "--output", "json"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	var result execution.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Deployment.ID != "dep_remote" {
		t.Fatalf("result=%#v err=%v output=%s", result, err, stdout.String())
	}
}

func TestMachineOutputReturnsStructuredFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	exit := app.Run(context.Background(), []string{"deploy", "--remote", "--mock", "--output", "json"})
	if exit != 1 {
		t.Fatalf("exit = %d", exit)
	}
	var result struct {
		Outcome string            `json:"outcome"`
		Failure execution.Failure `json:"failure"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode failure: %v\n%s", err, stdout.String())
	}
	if result.Outcome != "failed" || result.Failure.Code == "" || stderr.Len() != 0 {
		t.Fatalf("result=%#v stderr=%s", result, stderr.String())
	}
}

func TestBareInvocationExplainsItselfWithoutInspecting(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "package.json", `{"scripts":{"start":"node index.js"}}`)
	writeCLIFile(t, root, "package-lock.json", `{"lockfileVersion":3}`)
	writeCLIFile(t, root, "index.js", "console.log('ready')\n")
	t.Chdir(root)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), nil); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "shed deploy") || !strings.Contains(stdout.String(), "Nothing was inspected") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	// A deployable project sits in the working directory. Nothing may be
	// packaged, built, or written until the user names a command.
	if _, err := os.Stat(filepath.Join(root, "SHED.yaml")); !os.IsNotExist(err) {
		t.Fatalf("bare invocation touched the project: %v", err)
	}
}

func TestUnknownCommandSuggestsTheNearestCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"statuss", "dep_remote"}); exit != 1 {
		t.Fatalf("exit=%d stdout=%s", exit, stdout.String())
	}
	if !strings.Contains(stderr.String(), `"statuss" is not a Shed command`) || !strings.Contains(stderr.String(), "Did you mean: shed status") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestMissingDirectoryIsReportedAsAMissingDirectory(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{filepath.Join(t.TempDir(), "absent")}); exit != 1 {
		t.Fatalf("exit=%d stdout=%s", exit, stdout.String())
	}
	if !strings.Contains(stderr.String(), "no directory named") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUnsupportedProjectFailsWithGuidanceAndAStableCode(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "main.py", "print('hello')\n")
	writeCLIFile(t, root, "requirements.txt", "flask==3.0.0\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"deploy", root, "--dry-run"}); exit != 1 {
		t.Fatalf("exit=%d stdout=%s", exit, stdout.String())
	}
	if !strings.Contains(stderr.String(), "Python") || !strings.Contains(stderr.String(), "Next steps:") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := app.Run(context.Background(), []string{"deploy", root, "--dry-run", "--output", "json"}); exit != 1 {
		t.Fatalf("exit=%d stdout=%s", exit, stdout.String())
	}
	var result struct {
		Failure execution.Failure `json:"failure"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode failure: %v\n%s", err, stdout.String())
	}
	if result.Failure.Code != "unsupported_project" || !strings.Contains(result.Failure.Message, "python") {
		t.Fatalf("failure = %#v", result.Failure)
	}
}

// `shed help` promises agents that --output json puts exactly one object on
// stdout and nothing else. That promise is the contract, so keep it tested:
// a diagnostic is the most likely thing to leak prose into the stream.
// init has to explain the definition it wrote, not just announce a file, and it
// has to explain it identically to a person and to an agent.
func TestInitReportsWhatItDetectedAndWhatWillRun(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "go.mod", "module example.com/service\n\ngo 1.24\n")
	writeCLIFile(t, root, "cmd/service/main.go", "package main\n\nfunc main() {}\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"init", root}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	report := stdout.String()
	for _, want := range []string{
		`Go (Railpack provider "golang")`, // what it detected
		"golang:1.24",                     // the toolchain it chose
		"cmd",                             // the sources it will package
		"go build",                        // how it builds
		"./out",                           // what it runs
		"8080",                            // where it serves
		"Next steps:",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report does not mention %q:\n%s", want, report)
		}
	}

	stdout.Reset()
	if exit := app.Run(context.Background(), []string{"init", root, "--output", "json"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	var machine definitionReport
	if err := json.Unmarshal(stdout.Bytes(), &machine); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	// The file already exists by now, so this run validated rather than wrote,
	// and validation must not claim a detection that never happened.
	if machine.Outcome != "unchanged" || machine.Provider != "" {
		t.Fatalf("report = %#v", machine)
	}
	if !strings.Contains(machine.Detected, "authoritative") {
		t.Fatalf("detected = %q", machine.Detected)
	}
	if machine.Toolchain != "golang:1.24" || machine.Runs.Port != 8080 || machine.NextOperation != "deploy" {
		t.Fatalf("report = %#v", machine)
	}
	if !reflect.DeepEqual(machine.Runs.Command, []string{"./out"}) || len(machine.Builds) == 0 {
		t.Fatalf("report = %#v", machine)
	}
	if machine.Description == "" || machine.Application.Build.Image != machine.Toolchain {
		t.Fatalf("report = %#v", machine)
	}
}

func TestJSONOutputKeepsStdoutParseableWhenACommandFails(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "main.py", "print('hello')\n")
	writeCLIFile(t, root, "requirements.txt", "flask==3.0.0\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"deploy", root, "--dry-run", "--output", "json"}); exit != 1 {
		t.Fatalf("exit=%d stdout=%s", exit, stdout.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("stdout did not parse as JSON: %v\n%s", err, stdout.String())
	}
	if decoder.More() {
		t.Fatalf("stdout carried more than one object: %s", stdout.String())
	}
	if result["type"] != "result" || result["outcome"] != "failed" {
		t.Fatalf("result = %#v", result)
	}
	if stderr.Len() != 0 {
		t.Fatalf("machine output leaked prose to stderr: %q", stderr.String())
	}
}

// The human counterpart: guidance belongs on stderr, leaving stdout clean for
// whatever the caller pipes it into.
func TestHumanFailureWritesGuidanceToStderrOnly(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "main.py", "print('hello')\n")
	writeCLIFile(t, root, "requirements.txt", "flask==3.0.0\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"deploy", root, "--dry-run"}); exit != 1 {
		t.Fatalf("exit=%d", exit)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Next steps:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func writeCLIFile(t *testing.T, root, relative, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const cliShedProgram = `b = build(
    srcs = ["go.mod", "main.go"],
    image = "golang:1.24",
    commands = [["go", "build", "-o", "out", "."]],
)

http_app(
    name = "checked",
    build = b,
    cmd = ["./out"],
    port = 8080,
)
`

func TestCheckValidProgramReportsTheApplication(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeCLIFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	writeCLIFile(t, root, "SHED", cliShedProgram)
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"check", root, "--output", "json"}); exit != 0 {
		t.Fatalf("exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout did not parse: %v\n%s", err, stdout.String())
	}
	if result["outcome"] != "valid" || result["nextOperation"] != "deploy" {
		t.Fatalf("result = %#v", result)
	}
	application, ok := result["application"].(map[string]any)
	if !ok {
		t.Fatalf("no application in %#v", result)
	}
	run := application["run"].(map[string]any)
	if run["port"] != float64(8080) {
		t.Fatalf("run = %#v", run)
	}
	if diagnostics := result["diagnostics"].([]any); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

// check reports every diagnostic in one pass while keeping the stdout
// contract: exactly one JSON object, nothing else.
func TestCheckReportsEveryDiagnosticInOneJSONObject(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeCLIFile(t, root, "SHED", `b = build(
    srcs = ["go.mo"],
    image = "golang:1.24",
)
http_app(
    name = "hello",
    build = b,
    cmd = "./out",
    port = 8080,
)
`)
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"check", root, "--output", "json"}); exit != 1 {
		t.Fatalf("exit=%d stdout=%s", exit, stdout.String())
	}
	decoder := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("stdout did not parse: %v\n%s", err, stdout.String())
	}
	if decoder.More() {
		t.Fatalf("stdout carried more than one object: %s", stdout.String())
	}
	if result["outcome"] != "invalid" {
		t.Fatalf("result = %#v", result)
	}
	diagnostics := result["diagnostics"].([]any)
	if len(diagnostics) != 2 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	first := diagnostics[0].(map[string]any)
	if first["code"] != "unknown_src" || !strings.Contains(first["message"].(string), "SHED:") {
		t.Fatalf("first = %#v", first)
	}
	if stderr.Len() != 0 {
		t.Fatalf("machine output leaked prose to stderr: %q", stderr.String())
	}
}

func TestCheckHumanModeRendersEveryDiagnosticToStderr(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "go.mod", "module example.com/app\n\ngo 1.24\n")
	writeCLIFile(t, root, "SHED", `b = build(
    srcs = ["go.mo"],
    image = "golang:1.24",
)
http_app(
    name = "hello",
    build = b,
    cmd = "./out",
    port = 8080,
)
`)
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"check", root}); exit != 1 {
		t.Fatalf("exit=%d", exit)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
	for _, want := range []string{"go.mo", "argv list", "2 problems"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q: %s", want, stderr.String())
		}
	}
}

func TestCheckWithoutADefinitionPointsAtInit(t *testing.T) {
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"check", root, "--output", "json"}); exit != 1 {
		t.Fatalf("exit=%d", exit)
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("stdout did not parse: %v\n%s", err, stdout.String())
	}
	failure := result["failure"].(map[string]any)
	if failure["code"] != "missing_definition" {
		t.Fatalf("failure = %#v", failure)
	}
}

func TestSchemaPrintsTheAPIInBothFormats(t *testing.T) {
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")
	if exit := app.Run(context.Background(), []string{"schema"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	for _, want := range []string{"build(*, srcs, image, commands = [])", "glob(patterns, *, exclude = [])", "Not yet supported"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stub missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	if exit := app.Run(context.Background(), []string{"schema", "--output", "json"}); exit != 0 {
		t.Fatalf("exit=%d", exit)
	}
	var schema map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &schema); err != nil {
		t.Fatalf("schema did not parse: %v", err)
	}
	if builtins := schema["builtins"].([]any); len(builtins) != 3 {
		t.Fatalf("builtins = %#v", builtins)
	}
}

// init --format shed writes a program whose evaluation is exactly what init
// reported, so the loop init -> check -> deploy needs no YAML at all.
func TestInitShedFormatRoundTrips(t *testing.T) {
	root := t.TempDir()
	writeCLIFile(t, root, "go.mod", "module example.com/service\n\ngo 1.24\n")
	writeCLIFile(t, root, "cmd/service/main.go", "package main\n\nfunc main() {}\n")
	var stdout, stderr bytes.Buffer
	app := New(&stdout, &stderr, "test")

	if exit := app.Run(context.Background(), []string{"init", root, "--format", "shed", "--output", "json"}); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	var initResult map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &initResult); err != nil {
		t.Fatalf("init output did not parse: %v\n%s", err, stdout.String())
	}
	if initResult["outcome"] != "created" || !strings.HasSuffix(initResult["path"].(string), "SHED") {
		t.Fatalf("init = %#v", initResult)
	}
	if _, err := os.Stat(filepath.Join(root, "SHED.yaml")); !os.IsNotExist(err) {
		t.Fatalf("SHED.yaml should not exist: %v", err)
	}

	stdout.Reset()
	if exit := app.Run(context.Background(), []string{"check", root, "--output", "json"}); exit != 0 {
		t.Fatalf("check exit=%d stderr=%s stdout=%s", exit, stderr.String(), stdout.String())
	}
	var checkResult map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &checkResult); err != nil {
		t.Fatalf("check output did not parse: %v", err)
	}
	if checkResult["outcome"] != "valid" {
		t.Fatalf("check = %#v", checkResult)
	}
	if !reflect.DeepEqual(checkResult["application"], initResult["application"]) {
		t.Fatalf("evaluation drifted:\ninit:  %#v\ncheck: %#v", initResult["application"], checkResult["application"])
	}
}
