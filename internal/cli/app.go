package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/browser"

	"shed/internal/api"
	"shed/internal/clispec"
	"shed/internal/config"
	"shed/internal/credentials"
	"shed/internal/definition"
	"shed/internal/deployment"
	"shed/internal/diag"
	"shed/internal/execution"
	localruntime "shed/internal/runtime"
	"shed/internal/shedfile"
	"shed/internal/state"
	"shed/internal/workflow"
)

type App struct {
	stdin       io.Reader
	stdinReader *bufio.Reader
	stdout      io.Writer
	stderr      io.Writer
	version     string
	openBrowser func(string) error
	// executablePath reports the running binary, so upgrade can tell a
	// self-managed install from a package-managed one. A seam for the same
	// reason openBrowser is: tests cannot relocate the test binary.
	executablePath func() (string, error)
	credentials    credentialStore
	deployer       deployment.Deployer
	remote         execution.Backend
}

type credentialStore interface {
	Resolve(string) (credentials.Credential, error)
	Save(string, string) (credentials.SaveResult, error)
	Delete(string, credentials.Source) error
}

func New(stdout, stderr io.Writer, version string) *App {
	return &App{
		stdin:          os.Stdin,
		stdout:         stdout,
		stderr:         stderr,
		version:        version,
		openBrowser:    browser.OpenURL,
		executablePath: os.Executable,
		credentials:    credentials.New(config.TokenStore{}),
		deployer:       deployment.Mock{},
	}
}

// handler runs one command against its parsed arguments. Every handler is
// reached through handlers, keyed by the clispec catalog, so a command cannot be
// dispatchable without being documented or documented without being reachable.
type handler func(*App, context.Context, *clispec.Binding) error

var handlers = map[string]handler{
	"deploy":  (*App).deploy,
	"init":    (*App).initDefinition,
	"check":   (*App).checkDefinition,
	"schema":  (*App).schema,
	"login":   (*App).login,
	"logout":  (*App).logout,
	"whoami":  (*App).whoami,
	"share":   (*App).share,
	"revoke":  (*App).revoke,
	"logs":    (*App).logs,
	"status":  (*App).status,
	"stop":    (*App).stopLocal,
	"destroy": (*App).destroyLocal,
	"cancel":  (*App).cancelRemote,
	"open":    (*App).open,
	"upgrade": (*App).upgrade,
	"version": (*App).printVersion,
	"help":    (*App).help,
}

// Run dispatches one invocation. Running shed with no arguments prints the
// overview instead of deploying: inspecting a project is real work on the user's
// files, so it happens only when they name it.
func (a *App) Run(ctx context.Context, args []string) int {
	if len(args) == 0 {
		a.printWelcome()
		return 0
	}

	err := a.dispatch(ctx, args)

	// flag.ContinueOnError reports -h as an error after the flag package has already
	// printed usage. Asking for help is not a failure, so exit cleanly instead of
	// tacking "error: flag: help requested" onto the usage text.
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	// Commands that report every diagnostic themselves — check — still exit 1,
	// but printing the sentinel would repeat what is already on screen.
	if errors.Is(err, errAlreadyReported) {
		return 1
	}
	if err != nil {
		a.writeCommandError(err, requestedOutput(args))
		return 1
	}
	return 0
}

func (a *App) dispatch(ctx context.Context, args []string) error {
	command, ok := clispec.Lookup(args[0])
	if !ok {
		return a.deployShorthand(ctx, args)
	}
	return a.runCommand(ctx, command, args[1:])
}

// runCommand binds args to the command's declared flags and hands the result to
// its handler. Every constraint the spec states is enforced before the handler
// runs, so no command can act on arguments it would go on to reject.
func (a *App) runCommand(ctx context.Context, command clispec.Command, args []string) error {
	binding, err := clispec.Bind(command, args, a.stderr)
	if err != nil {
		return err
	}
	run, ok := handlers[command.Name]
	if !ok {
		return fmt.Errorf("shed: command %q has no handler", command.Name)
	}
	return run(a, ctx, binding)
}

func requestedOutput(args []string) string {
	for index, argument := range args {
		if strings.HasPrefix(argument, "--output=") {
			return strings.TrimPrefix(argument, "--output=")
		}
		if (argument == "--output" || argument == "-output") && index+1 < len(args) {
			return args[index+1]
		}
	}
	return "human"
}

// deployShorthand handles `shed <directory>`. The shorthand is documented, so an
// unrecognized first word is far more likely to be a mistyped command than a
// project; saying so beats inspecting a directory nobody asked about.
func (a *App) deployShorthand(ctx context.Context, args []string) error {
	target := args[0]
	deploy, _ := clispec.Lookup("deploy")
	if strings.HasPrefix(target, "-") {
		return a.runCommand(ctx, deploy, args)
	}
	info, err := os.Stat(target)
	if err == nil && info.IsDir() {
		return a.runCommand(ctx, deploy, args)
	}
	if err == nil {
		return &diag.Error{
			Code:    "not_a_directory",
			Summary: fmt.Sprintf("%q is a file, and Shed deploys directories.", target),
			Hints:   []string{"Deploy the directory holding it: shed deploy " + filepath.Dir(target)},
		}
	}
	if looksLikePath(target) {
		return &diag.Error{
			Code:    "directory_not_found",
			Summary: fmt.Sprintf("There is no directory named %q.", target),
			Hints:   []string{"Deploy the current directory: shed deploy"},
		}
	}
	return unknownCommand(target)
}

func looksLikePath(value string) bool {
	return value == "." || value == ".." || filepath.IsAbs(value) || strings.ContainsRune(value, filepath.Separator) || strings.ContainsRune(value, '/')
}

func unknownCommand(name string) error {
	failure := &diag.Error{
		Code:    "unknown_command",
		Summary: fmt.Sprintf("%q is not a Shed command.", name),
	}
	if closest := nearestCommand(name); closest != "" {
		failure.Hints = append(failure.Hints, "Did you mean: shed "+closest)
	}
	failure.Hints = append(failure.Hints,
		"Deploy a directory: shed deploy <directory>",
		"See every command: shed help",
	)
	return failure
}

func (a *App) writeCommandError(err error, output string) {
	diagnostic, isDiagnostic := diag.As(err)
	if output != "json" && output != "ndjson" {
		if isDiagnostic {
			diag.Render(a.stderr, diagnostic)
			return
		}
		_, _ = fmt.Fprintf(a.stderr, "error: %v\n", err)
		return
	}
	failure := execution.Failure{Code: "operation_failed", Message: err.Error(), Recoverable: false}
	if isDiagnostic {
		failure.Code, failure.Message = diagnostic.Code, diagnostic.Error()
	}
	var apiError *api.Error
	if errors.As(err, &apiError) {
		if apiError.Code != "" {
			failure.Code = apiError.Code
		} else {
			failure.Code = "remote_api_error"
		}
		failure.Recoverable = apiError.StatusCode == 408 || apiError.StatusCode == 429 || apiError.StatusCode >= 500
	}
	_ = json.NewEncoder(a.stdout).Encode(map[string]any{"type": "result", "outcome": "failed", "failure": failure})
}

func (a *App) deploy(ctx context.Context, b *clispec.Binding) error {
	root := "."
	if b.NArg() == 1 {
		root = b.Arg(0)
	}
	output := b.String("output")

	// The definition comes first, on its own, so a person is told what deploy
	// is working from — and whether it just wrote SHED.hcl — before the stage
	// panel takes over the terminal. A dry run only looks; every other deploy
	// keeps what it detected, so the next one reads the same file.
	writeFormat := "hcl"
	if b.Bool("dry-run") {
		writeFormat = ""
	}
	resolved, err := workflow.ResolveDefinition(root, nil, writeFormat)
	if err != nil {
		return err
	}
	if output == "human" {
		a.writeResolution(resolved, root)
	}

	// The cloud is the default. Only an explicit --local, or a flag that never
	// leaves this machine (--dry-run, --mock), runs the Docker workflow; --remote
	// is accepted so older invocations keep working, and changes nothing.
	local := b.Bool("local") || b.Bool("dry-run") || b.Bool("mock")
	if !local {
		return a.deployRemote(ctx, root, resolved, b.String("archive"), b.String("project"), b.String("request-id"), output,
			b.Bool("detach"), b.Bool("wait"), b.Provided("wait-timeout"), b.Duration("wait-timeout"))
	}
	var progress *diag.Progress
	if output == "human" && !b.Bool("dry-run") && !b.Bool("mock") {
		progress = diag.NewProgress(a.stderr, workflow.LocalPhases...)
		defer progress.Close()
	}
	options := workflow.Options{
		Root:       root,
		Archive:    b.String("archive"),
		RequestID:  b.String("request-id"),
		DryRun:     b.Bool("dry-run"),
		Mock:       b.Bool("mock"),
		Deployer:   a.deployer,
		Definition: &resolved,
	}
	if progress != nil {
		options.Progress = progress
	}
	result, err := workflow.Run(ctx, options)
	if err != nil {
		return err
	}
	if progress != nil {
		progress.Finish("")
	}
	if output == "json" || output == "ndjson" {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}
	_, _ = fmt.Fprintf(a.stdout, "Packaged %d files (%s) for %s\n", result.Source.FileCount, formatBytes(result.Source.Size), result.Application.Build.Image)
	_, _ = fmt.Fprintf(a.stdout, "  content  %s\n", result.Source.ContentDigest)
	_, _ = fmt.Fprintf(a.stdout, "  archive  %s\n", result.Source.ArchiveDigest)
	if result.Source.ArchivePath != "" {
		_, _ = fmt.Fprintf(a.stdout, "  saved    %s\n", result.Source.ArchivePath)
	}
	switch {
	case result.Outcome == "ready" && result.Runtime != nil:
		_, _ = fmt.Fprintf(a.stdout, "Ready: %s (HTTP %d)\n", result.Runtime.URL, result.Runtime.StatusCode)
		if result.Instance != nil {
			_, _ = fmt.Fprintf(a.stdout, "Stop it with: shed stop %s\n", result.Instance.ID)
		}
	case result.Deployment != nil:
		_, _ = fmt.Fprintf(a.stdout, "Mock upload %s accepted; deployment %s is waiting for a builder.\n", result.Source.ID, result.Deployment.ID)
	default:
		_, _ = fmt.Fprintln(a.stdout, "Dry run: nothing was built, started, or uploaded.")
	}
	return nil
}

// writeResolution is the first thing a person sees from deploy: which
// definition it is working from, and whether it was just written, before the
// stage panel starts. It goes to stderr with the rest of the narration, so
// stdout stays the result alone.
func (a *App) writeResolution(resolved workflow.ResolvedDefinition, root string) {
	style := diag.NewStyler(a.stderr)
	manifest := resolved.Manifest
	path := filepath.Join(root, resolved.File)
	contract := fmt.Sprintf("Builds with %s, packages %s, runs %q on port %d (%s).",
		manifest.Build.Image, describePaths(manifest.Content.Include),
		strings.Join(manifest.Run.Command, " "), manifest.Run.Port, portSource(resolved.Provider != ""))
	switch {
	case resolved.Created:
		_, _ = fmt.Fprintf(a.stderr, "%s Nothing described this project yet, so Shed looked: %s.\n",
			style.Strong("Wrote "+path+"."), detectionSummary(resolved.Provider))
		_, _ = fmt.Fprintf(a.stderr, "  %s\n", contract)
		_, _ = fmt.Fprintln(a.stderr, "  Edit it any time; it is never regenerated. Delete it to detect again.")
	case resolved.Provider != "":
		_, _ = fmt.Fprintf(a.stderr, "%s Shed looked: %s. A deploy would write %s; this dry run writes nothing.\n",
			style.Strong("No definition here."), detectionSummary(resolved.Provider), path)
		_, _ = fmt.Fprintf(a.stderr, "  %s\n", contract)
	default:
		_, _ = fmt.Fprintf(a.stderr, "%s %s\n", style.Strong("Using "+path+"."), contract)
	}
	_, _ = fmt.Fprintln(a.stderr)
}

// errAlreadyReported marks a failure whose full story is already on screen;
// Run turns it into exit code 1 without printing anything more.
var errAlreadyReported = errors.New("diagnostics already reported")

func (a *App) initDefinition(_ context.Context, b *clispec.Binding) error {
	root := "."
	if b.NArg() == 1 {
		root = b.Arg(0)
	}
	generated, created, filename, err := workflow.Initialize(root, nil, b.String("format"))
	if err != nil {
		return err
	}
	path := filepath.Join(root, filename)
	if b.String("output") == "json" {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(newDefinitionReport(generated, created, path))
	}
	a.writeDefinitionReport(generated, created, path, root, filename)
	return nil
}

// checkReport is the one JSON object `shed check` prints: the verdict, every
// diagnostic, and — when valid — the evaluated application, so an agent can
// see exactly what its definition became without a second command.
type checkReport struct {
	Type          string               `json:"type"`
	Outcome       string               `json:"outcome"`
	Path          string               `json:"path"`
	Diagnostics   []checkDiagnostic    `json:"diagnostics"`
	Application   *definition.Manifest `json:"application,omitempty"`
	NextOperation string               `json:"nextOperation"`
}

type checkDiagnostic struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Hints   []string `json:"hints,omitempty"`
}

func (a *App) checkDefinition(_ context.Context, b *clispec.Binding) error {
	root := "."
	if b.NArg() == 1 {
		root = b.Arg(0)
	}
	generated, filename, diagnostics, err := workflow.Check(root)
	if err != nil {
		return err
	}
	path := filepath.Join(root, filename)
	if b.String("output") == "json" {
		report := checkReport{Type: "result", Outcome: "valid", Path: path, Diagnostics: []checkDiagnostic{}, NextOperation: "deploy"}
		if len(diagnostics) > 0 {
			report.Outcome, report.NextOperation = "invalid", "fix_diagnostics"
			for _, diagnostic := range diagnostics {
				report.Diagnostics = append(report.Diagnostics, checkDiagnostic{
					Code:    diagnostic.Code,
					Message: diagnostic.Error(),
					Hints:   diagnostic.Hints,
				})
			}
		} else {
			report.Application = &generated.Manifest
		}
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
		if len(diagnostics) > 0 {
			return errAlreadyReported
		}
		return nil
	}
	if len(diagnostics) > 0 {
		for index, diagnostic := range diagnostics {
			if index > 0 {
				_, _ = fmt.Fprintln(a.stderr)
			}
			diag.Render(a.stderr, diagnostic)
		}
		count := "1 problem"
		if len(diagnostics) != 1 {
			count = fmt.Sprintf("%d problems", len(diagnostics))
		}
		_, _ = fmt.Fprintf(a.stderr, "\n%s: %s in %s\n", filename, count, root)
		return errAlreadyReported
	}
	_, _ = fmt.Fprintln(a.stdout, describeDefinition(generated, false, path))
	return nil
}

func (a *App) schema(_ context.Context, b *clispec.Binding) error {
	if b.String("output") == "json" {
		encoder := json.NewEncoder(a.stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(shedfile.APISchema())
	}
	shedfile.RenderSchema(a.stdout)
	return nil
}

// definitionReport is what `shed init` returns. It is deliberately descriptive:
// an agent reading the JSON should learn what was detected and what will run
// without having to know the SHED.hcl schema, while `application` stays the
// authoritative copy for anything that does.
type definitionReport struct {
	Type          string              `json:"type"`
	Outcome       string              `json:"outcome"`
	Path          string              `json:"path"`
	Description   string              `json:"description"`
	Detected      string              `json:"detected"`
	Provider      string              `json:"provider,omitempty"`
	Toolchain     string              `json:"toolchain"`
	Includes      []string            `json:"includes"`
	Builds        [][]string          `json:"builds,omitempty"`
	Runs          runReport           `json:"runs"`
	Application   definition.Manifest `json:"application"`
	NextOperation string              `json:"nextOperation"`
}

type runReport struct {
	Command []string `json:"command"`
	Port    int      `json:"port"`
	// PortSource is "assumed" for a freshly detected project and "declared" for
	// one loaded from disk. Railpack reports no port for any provider — its
	// plans expect the host to inject $PORT — so a generated definition carries
	// Shed's default rather than an observed fact, and saying which is which
	// keeps the report from asserting something nothing established.
	PortSource string `json:"portSource"`
}

func portSource(detected bool) string {
	if detected {
		return "assumed"
	}
	return "declared"
}

func describePort(port int, detected bool, filename string) string {
	if detected {
		return fmt.Sprintf("%d, assumed — detection never reports a port, so correct it in %s if the app listens elsewhere", port, filename)
	}
	return fmt.Sprintf("%d, as declared in %s", port, filename)
}

func newDefinitionReport(generated definition.GeneratedDefinition, created bool, path string) definitionReport {
	manifest := generated.Manifest
	outcome := "unchanged"
	if created {
		outcome = "created"
	}
	return definitionReport{
		Type:        "result",
		Outcome:     outcome,
		Path:        path,
		Description: describeDefinition(generated, created, path),
		Detected:    detectionSummary(generated.Provider),
		Provider:    generated.Provider,
		Toolchain:   manifest.Build.Image,
		Includes:    manifest.Content.Include,
		Builds:      manifest.Build.Commands,
		Runs: runReport{
			Command: manifest.Run.Command, Port: manifest.Run.Port,
			PortSource: portSource(generated.Provider != ""),
		},
		Application:   manifest,
		NextOperation: "deploy",
	}
}

// detectionSummary is one phrase in both output formats, so the human report and
// the machine report can never disagree about what was found.
func detectionSummary(provider string) string {
	if provider == "" {
		// Loading beats detecting: the file on disk decides the build, and Shed
		// deliberately does not second-guess it by inspecting the source again.
		return "nothing; the existing file is authoritative and was not redetected"
	}
	return definition.ProviderLabel(provider)
}

func describeDefinition(generated definition.GeneratedDefinition, created bool, path string) string {
	manifest := generated.Manifest
	verb := "Validated"
	if created {
		verb = "Wrote"
	}
	return fmt.Sprintf("%s %s. Detected %s. Builds with %s, packages %s, then runs %q on port %d (%s).",
		verb, path, detectionSummary(generated.Provider), manifest.Build.Image,
		describePaths(manifest.Content.Include),
		strings.Join(manifest.Run.Command, " "), manifest.Run.Port,
		portSource(generated.Provider != ""))
}

// describePaths is "N top-level paths (a, b, c)", the content closure in one
// phrase.
func describePaths(include []string) string {
	paths := "1 top-level path"
	if count := len(include); count != 1 {
		paths = fmt.Sprintf("%d top-level paths", count)
	}
	return paths + " (" + describeInclude(include) + ")"
}

// writeDefinitionReport explains the definition rather than announcing that a
// file appeared. Everything it prints is read back out of the manifest, so the
// report cannot describe a build the manifest would not actually run.
func (a *App) writeDefinitionReport(generated definition.GeneratedDefinition, created bool, path, root, filename string) {
	style := diag.NewStyler(a.stdout)
	manifest := generated.Manifest
	if created {
		_, _ = fmt.Fprintln(a.stdout, style.Strong("Wrote "+path))
	} else {
		_, _ = fmt.Fprintln(a.stdout, style.Strong(path+" is already here and valid"))
	}
	_, _ = fmt.Fprintln(a.stdout)

	facts := []diag.Fact{{Label: "Detected", Value: detectionSummary(generated.Provider)}}
	facts = append(facts,
		diag.Fact{Label: "Toolchain", Value: manifest.Build.Image},
		diag.Fact{Label: "Includes", Value: describeInclude(manifest.Content.Include)},
	)
	if manifest.Metadata != nil && manifest.Metadata.Name != "" {
		facts = append(facts, diag.Fact{Label: "Project", Value: manifest.Metadata.Name})
	}
	for index, command := range manifest.Build.Commands {
		label := ""
		if index == 0 {
			label = "Builds"
		}
		facts = append(facts, diag.Fact{Label: label, Value: strings.Join(command, " ")})
	}
	facts = append(facts,
		diag.Fact{Label: "Runs", Value: strings.Join(manifest.Run.Command, " ")},
		diag.Fact{Label: "Port", Value: describePort(manifest.Run.Port, generated.Provider != "", filename)},
	)
	if manifest.Base != "" {
		facts = append(facts, diag.Fact{Label: "Cloud base", Value: manifest.Base})
	}
	diag.RenderFacts(a.stdout, facts)

	_, _ = fmt.Fprintln(a.stdout)
	_, _ = fmt.Fprintln(a.stdout, style.Strong("Next steps:"))
	target := ""
	if root != "." {
		target = " " + root
	}
	_, _ = fmt.Fprintf(a.stdout, "  Read %s; from here on it decides the build and is never regenerated\n", filename)
	_, _ = fmt.Fprintf(a.stdout, "  See exactly which files get packaged: shed deploy%s --dry-run --output json\n", target)
	_, _ = fmt.Fprintf(a.stdout, "  Deploy it: shed deploy%s\n", target)
	_, _ = fmt.Fprintf(a.stdout, "  Or build and run it here, in Docker: shed deploy%s --local\n", target)
}

// describeInclude keeps the content closure readable when a project has many
// top-level entries, while still saying how many were left out.
func describeInclude(include []string) string {
	const shown = 8
	if len(include) <= shown {
		return strings.Join(include, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(include[:shown], ", "), len(include)-shown)
}

func (a *App) remoteBackend() (execution.Backend, error) {
	if a.remote != nil {
		return a.remote, nil
	}
	return a.configuredClient()
}

func (a *App) deployRemote(ctx context.Context, root string, resolved workflow.ResolvedDefinition, archivePath, project, requestID, output string, detach, wait, waitTimeoutSet bool, waitTimeout time.Duration) error {
	// Packaging is the first row of the same panel the cloud stages fill in,
	// so the person watching sees one continuous story from archive to URL.
	progress, observer := a.remoteObserver(output, "package")
	if progress != nil {
		defer progress.Close()
		progress.Start("package")
	}
	fail := func(err error) error {
		if progress != nil {
			progress.Fail(err)
		}
		return err
	}
	prepared, err := workflow.Prepare(workflow.Options{Root: root, Archive: archivePath, Definition: &resolved})
	if err != nil {
		return fail(err)
	}
	defer func() { _ = prepared.Close() }()
	if progress != nil {
		progress.StageDone("package", workflow.PackageSummary(prepared.Archive))
	}
	projectName, err := definition.ProjectName(prepared.Root, project, prepared.Manifest)
	if err != nil {
		return fail(err)
	}
	backend, err := a.remoteBackend()
	if err != nil {
		return fail(err)
	}
	waitOptions := execution.WaitOptions{Mode: execution.WaitTimeout, Timeout: waitTimeout, Stage: execution.StageAll}
	if detach {
		waitOptions.Mode = execution.WaitDetach
	} else if wait && !waitTimeoutSet {
		waitOptions.Mode = execution.WaitTerminal
	}
	result, err := (execution.Coordinator{Backend: backend}).Execute(ctx, execution.Request{
		ProjectName: projectName,
		RequestID:   requestID,
		Manifest:    prepared.Manifest,
		Archive:     prepared.Archive,
	}, waitOptions, observer)
	if err != nil {
		return fail(err)
	}
	if progress != nil {
		if result.Outcome == "ready" {
			progress.Finish("")
		} else {
			progress.Close()
		}
	}
	resolution := resolved.Resolution()
	result.Definition = &resolution
	return a.writeExecutionResult(result, output)
}

// remoteStages orders the deployment state machine into user-facing panel rows.
// build_queued collapses into "build" and cancelling/cancelled don't get a row —
// they get rendered as a failed active stage instead.
var remoteStages = []string{"queue", "bundle", "build", "verify", "provision", "health"}

func stageForState(state execution.State) string {
	switch state {
	case execution.StateAccepted, execution.StateBuildQueued:
		return "queue"
	case execution.StateBundleValidating:
		return "bundle"
	case execution.StateBuilding:
		return "build"
	case execution.StateVerifying:
		return "verify"
	case execution.StateProvisioning:
		return "provision"
	case execution.StateHealthChecking:
		return "health"
	}
	return ""
}

// remoteObserver picks the right observer for the requested output.
//   - human: build a Progress panel and route State transitions into it. The
//     returned Progress must be Closed/Finished/Failed by the caller. Any
//     leadingStages come first in the panel, for work the caller does itself
//     before the cloud takes over.
//   - ndjson: passthrough encoder, no progress panel.
//   - json: no observer runs — a JSON result is emitted only at the end.
func (a *App) remoteObserver(output string, leadingStages ...string) (*diag.Progress, execution.Observer) {
	switch output {
	case "human":
		progress := diag.NewProgress(a.stderr, append(append([]string(nil), leadingStages...), remoteStages...)...)
		return progress, func(record execution.Record) error {
			if stage := stageForState(record.State); stage != "" {
				progress.Start(stage)
			}
			if record.Message != "" {
				progress.Detail(record.Message)
			}
			return nil
		}
	case "ndjson":
		return nil, func(record execution.Record) error {
			return json.NewEncoder(a.stdout).Encode(record)
		}
	}
	return nil, nil
}

// executionObserver is the line-oriented observer used by `logs --follow` in
// human mode: each record renders as `<colored-stage>: <message>`. Deploy and
// status go through remoteObserver instead.
func (a *App) executionObserver(output string) execution.Observer {
	styler := diag.NewStyler(a.stderr)
	return func(record execution.Record) error {
		switch output {
		case "human":
			stagePrefix := styler.Faint(string(record.Stage))
			if record.Stage == execution.StageBuild {
				stagePrefix = styler.Strong(string(record.Stage))
			}
			if record.Message != "" {
				_, _ = fmt.Fprintf(a.stderr, "%s: %s\n", stagePrefix, record.Message)
			} else if record.State != "" {
				_, _ = fmt.Fprintf(a.stderr, "%s: %s\n", styler.Faint("deployment"), record.State)
			}
		case "ndjson":
			return json.NewEncoder(a.stdout).Encode(record)
		}
		return nil
	}
}

func (a *App) writeExecutionResult(result execution.Result, output string) error {
	if output == "json" || output == "ndjson" {
		return json.NewEncoder(a.stdout).Encode(result)
	}
	if result.Outcome == "ready" {
		_, _ = fmt.Fprintln(a.stdout, result.Deployment.URL)
		return nil
	}
	_, _ = fmt.Fprintf(a.stdout, "Deployment %s is %s. Resume with: shed status %s --wait\n", result.Deployment.ID, result.Deployment.State, result.Deployment.ID)
	return nil
}

func (a *App) share(ctx context.Context, b *clispec.Binding) error {
	client, err := a.configuredClient()
	if err != nil {
		return err
	}
	if err := client.Share(ctx, b.Arg(0), b.Arg(1)); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.stdout, "Shared %s with %s\n", b.Arg(0), b.Arg(1))
	return nil
}

func (a *App) revoke(ctx context.Context, b *clispec.Binding) error {
	client, err := a.configuredClient()
	if err != nil {
		return err
	}
	if err := client.Revoke(ctx, b.Arg(0), b.Arg(1)); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.stdout, "Revoked %s from %s\n", b.Arg(1), b.Arg(0))
	return nil
}

func (a *App) logs(ctx context.Context, b *clispec.Binding) error {
	id := b.Arg(0)
	if _, instance, found, err := a.findLocalInstance(id); err != nil {
		return err
	} else if found {
		return (&localruntime.Docker{}).Logs(ctx, localruntime.Instance{ID: instance.InstanceID, Container: instance.ContainerID}, a.stdout)
	}
	output := b.String("output")
	backend, err := a.remoteBackend()
	if err != nil {
		return err
	}
	var records []execution.Record
	observer := func(record execution.Record) error {
		if output == "json" {
			records = append(records, record)
			return nil
		}
		return a.executionObserver(output)(record)
	}
	lastCursor, err := backend.Stream(ctx, id, b.String("cursor"), execution.Stage(b.String("stage")), b.Bool("follow"), observer)
	if err != nil {
		return err
	}
	if output == "json" {
		return json.NewEncoder(a.stdout).Encode(map[string]any{"type": "result", "outcome": "complete", "deploymentId": id, "cursor": lastCursor, "records": records})
	}
	return nil
}

func (a *App) status(ctx context.Context, b *clispec.Binding) error {
	id := b.Arg(0)
	_, instance, found, err := a.findLocalInstance(id)
	if err != nil {
		return err
	}
	if found {
		ready, readyErr := (&localruntime.Docker{}).WaitReady(ctx, localruntime.Instance{ID: instance.InstanceID, Container: instance.ContainerID, HostPort: instance.HostPort, URL: instance.URL}, 3*time.Second)
		if readyErr != nil {
			return readyErr
		}
		return json.NewEncoder(a.stdout).Encode(ready)
	}
	output := b.String("output")
	backend, err := a.remoteBackend()
	if err != nil {
		return err
	}
	waitTimeoutSet := b.Provided("wait-timeout")
	options := execution.WaitOptions{Mode: execution.WaitDetach, Stage: execution.StageAll}
	if b.Bool("wait") && !waitTimeoutSet {
		options.Mode = execution.WaitTerminal
	} else if waitTimeoutSet {
		options.Mode, options.Timeout = execution.WaitTimeout, b.Duration("wait-timeout")
	}
	progress, observer := a.remoteObserver(output)
	if progress != nil {
		defer progress.Close()
	}
	result, err := (execution.Coordinator{Backend: backend}).Resume(ctx, id, options, observer)
	if err != nil {
		if progress != nil {
			progress.Fail(err)
		}
		return err
	}
	if progress != nil {
		if result.Outcome == "ready" {
			progress.Finish("")
		} else {
			progress.Close()
		}
	}
	return a.writeExecutionResult(result, output)
}

func (a *App) cancelRemote(ctx context.Context, b *clispec.Binding) error {
	backend, err := a.remoteBackend()
	if err != nil {
		return err
	}
	deployment, err := backend.Cancel(ctx, b.Arg(0))
	if err != nil {
		return err
	}
	if b.String("output") == "human" {
		_, _ = fmt.Fprintf(a.stdout, "Deployment %s is %s.\n", deployment.ID, deployment.State)
		return nil
	}
	return json.NewEncoder(a.stdout).Encode(execution.Result{Type: "result", Outcome: "pending", Deployment: deployment, NextOperation: "status"})
}

func (a *App) stopLocal(ctx context.Context, b *clispec.Binding) error {
	applicationID, instance, found, err := a.findLocalInstance(b.Arg(0))
	if err != nil {
		return err
	}
	if !found {
		return errors.New("local instance was not found")
	}
	runner := &localruntime.Docker{}
	if err := runner.Stop(ctx, localruntime.Instance{ID: instance.InstanceID, Container: instance.ContainerID}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.stdout, "Stopped %s\n", instance.InstanceID)
	_ = applicationID
	return nil
}

func (a *App) destroyLocal(ctx context.Context, b *clispec.Binding) error {
	applicationID, instance, found, err := a.findLocalInstance(b.Arg(0))
	if err != nil {
		return err
	}
	if !found {
		return errors.New("local instance was not found")
	}
	if err := (&localruntime.Docker{}).Remove(ctx, localruntime.Instance{ID: instance.InstanceID, Container: instance.ContainerID}); err != nil {
		return err
	}
	store, err := state.NewStore()
	if err != nil {
		return err
	}
	if err := store.Remove(applicationID); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(a.stdout, "Destroyed %s\n", instance.InstanceID)
	return nil
}

func (a *App) findLocalInstance(id string) (string, state.Instance, bool, error) {
	store, err := state.NewStore()
	if err != nil {
		return "", state.Instance{}, false, err
	}
	applicationID, instance, err := store.Find(id)
	return applicationID, instance, instance.InstanceID != "", err
}

func (a *App) open(_ context.Context, b *clispec.Binding) error {
	target := b.Arg(0)
	if !strings.HasPrefix(target, "https://") && !strings.HasPrefix(target, "http://") {
		return errors.New("open expects an http or https URL")
	}
	return a.openBrowser(target)
}

func (a *App) printVersion(_ context.Context, _ *clispec.Binding) error {
	_, _ = fmt.Fprintln(a.stdout, a.version)
	return nil
}

// help prints the whole surface, one command's surface, or the machine-readable
// contract that agents read instead of parsing the human screen.
func (a *App) help(_ context.Context, b *clispec.Binding) error {
	if b.String("output") == "json" {
		return a.writeHelpJSON()
	}
	if b.NArg() == 1 {
		command, ok := clispec.Lookup(b.Arg(0))
		if !ok {
			return unknownCommand(b.Arg(0))
		}
		a.printCommandHelp(command)
		return nil
	}
	a.printHelp()
	return nil
}

func formatBytes(size int64) string {
	const (
		kib = 1024
		mib = 1024 * kib
	)
	switch {
	case size >= mib:
		return fmt.Sprintf("%.1f MiB", float64(size)/mib)
	case size >= kib:
		return fmt.Sprintf("%.1f KiB", float64(size)/kib)
	default:
		return fmt.Sprintf("%d B", size)
	}
}
