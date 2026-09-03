package workflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/railwayapp/railpack/core"
	"github.com/railwayapp/railpack/core/app"
	"github.com/railwayapp/railpack/core/resolver"

	"shed/internal/builder"
	"shed/internal/definition"
	"shed/internal/deployment"
	"shed/internal/diag"
	localruntime "shed/internal/runtime"
	"shed/internal/shedfile"
	"shed/internal/source"
	"shed/internal/state"
)

type Options struct {
	Root         string
	Archive      string
	RequestID    string
	DryRun       bool
	Mock         bool
	Generator    definition.DefinitionGenerator
	Deployer     deployment.Deployer
	Builder      builder.Builder
	State        *state.Store
	Runtime      Runtime
	ReadyTimeout time.Duration
	// Progress receives phase transitions ("package" → "build" → "start" →
	// "ready") as the local deploy runs. Nil disables reporting.
	Progress ProgressReporter
}

// ProgressReporter is the sink for local-deploy phase events. It matches the
// subset of *diag.Progress that Run needs, so callers can either pass one
// directly or plug in a test double. Nil-safe: Run swaps in a no-op if unset.
type ProgressReporter interface {
	Start(phase string)
	Detail(text string)
	StageDone(phase, detail string)
	Fail(err error)
}

type noopProgress struct{}

func (noopProgress) Start(string)             {}
func (noopProgress) Detail(string)            {}
func (noopProgress) StageDone(string, string) {}
func (noopProgress) Fail(error)               {}

// LocalPhases lists the phase labels the local deploy emits, in order. CLI
// callers use it to seed a diag.Progress with the right stage set.
var LocalPhases = []string{"package", "build", "start", "ready"}

type Runtime interface {
	Start(context.Context, string, string, string, int, int) (localruntime.Instance, error)
	WaitReady(context.Context, localruntime.Instance, time.Duration) (localruntime.ReadinessResult, error)
	Stop(context.Context, localruntime.Instance) error
	Remove(context.Context, localruntime.Instance) error
}

type SourceResult struct {
	ID            string          `json:"id,omitempty"`
	ContentDigest string          `json:"contentDigest"`
	ArchiveDigest string          `json:"archiveDigest"`
	FileCount     int             `json:"fileCount"`
	Size          int64           `json:"size"`
	ArchivePath   string          `json:"archivePath,omitempty"`
	Manifest      source.Manifest `json:"manifest"`
}

type Result struct {
	Outcome       string                        `json:"outcome"`
	RequestID     string                        `json:"requestId,omitempty"`
	Application   definition.Manifest           `json:"application"`
	Source        SourceResult                  `json:"source"`
	Deployment    *deployment.Deployment        `json:"deployment,omitempty"`
	NextOperation string                        `json:"nextOperation"`
	Runtime       *localruntime.ReadinessResult `json:"runtime,omitempty"`
	Instance      *localruntime.Instance        `json:"instance,omitempty"`
}

// Prepared is the immutable handoff from project inspection to execution.
// Execution backends consume Archive and Manifest and must not inspect Root.
type Prepared struct {
	Root     string
	Manifest definition.Manifest
	// ManifestSource is the manifest as SHED.hcl bytes, the copy the archive
	// carries.
	ManifestSource []byte
	Archive        source.Archive
}

func (prepared Prepared) Close() error {
	return prepared.Archive.Close()
}

func Prepare(options Options) (Prepared, error) {
	root := options.Root
	if root == "" {
		root = "."
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Prepared{}, fmt.Errorf("resolve application root: %w", err)
	}
	generated, err := ResolveDefinition(absoluteRoot, options.Generator)
	if err != nil {
		return Prepared{}, err
	}
	archive, err := source.Prepare(absoluteRoot, options.Archive, generated.Source, generated.Manifest.Content.Include...)
	if err != nil {
		return Prepared{}, fmt.Errorf("prepare source archive: %w", err)
	}
	return Prepared{Root: absoluteRoot, Manifest: generated.Manifest, ManifestSource: generated.Source, Archive: archive}, nil
}

func Run(ctx context.Context, options Options) (Result, error) {
	report := options.Progress
	if report == nil {
		report = noopProgress{}
	}
	report.Start("package")
	prepared, err := Prepare(options)
	if err != nil {
		report.Fail(err)
		return Result{}, err
	}
	defer func() { _ = prepared.Close() }()
	archive := prepared.Archive
	report.StageDone("package", packageSummary(archive))

	archivePath := ""
	if !archive.Temporary {
		archivePath = archive.Path
	}
	result := Result{
		Outcome:     "prepared",
		RequestID:   options.RequestID,
		Application: prepared.Manifest,
		Source: SourceResult{
			ContentDigest: archive.Content.Digest,
			ArchiveDigest: archive.Digest,
			FileCount:     archive.Content.FileCount,
			Size:          archive.CompressedSize,
			ArchivePath:   archivePath,
			Manifest:      archive.Content,
		},
		NextOperation: "mock_upload",
	}
	if options.DryRun {
		return result, nil
	}
	if !options.Mock {
		return runLocal(ctx, options, prepared.Root, prepared.Manifest, archive, result)
	}
	deployer := options.Deployer
	if deployer == nil {
		deployer = deployment.Mock{}
	}
	registered, err := deployer.RegisterSource(ctx, deployment.RegisterRequest{
		RequestID:     options.RequestID,
		ContentDigest: archive.Content.Digest,
		ArchiveDigest: archive.Digest,
		Size:          archive.CompressedSize,
		FileCount:     archive.Content.FileCount,
	})
	if err != nil {
		return Result{}, fmt.Errorf("register source: %w", err)
	}
	if err := deployer.UploadSource(ctx, registered, archive); err != nil {
		return Result{}, fmt.Errorf("upload source: %w", err)
	}
	created, err := deployer.CreateDeployment(ctx, registered, archive.Digest)
	if err != nil {
		return Result{}, fmt.Errorf("create deployment: %w", err)
	}
	result.Outcome = "uploaded_mock"
	result.Source.ID = registered.ID
	result.Deployment = &created
	result.NextOperation = "build_image"
	return result, nil
}

func runLocal(ctx context.Context, options Options, root string, manifest definition.Manifest, archive source.Archive, result Result) (Result, error) {
	report := options.Progress
	if report == nil {
		report = noopProgress{}
	}
	store := options.State
	if store == nil {
		value, err := state.NewStore()
		if err != nil {
			report.Fail(err)
			return Result{}, err
		}
		store = &value
	}
	applicationID := state.ApplicationID(root)
	previous, err := store.Load(applicationID)
	if err != nil {
		report.Fail(err)
		return Result{}, err
	}
	runner := options.Runtime
	if runner == nil {
		runner = &localruntime.Docker{}
	}
	if previous.ContentDigest == archive.Content.Digest && previous.ContainerID != "" {
		instance := localruntime.Instance{ID: previous.InstanceID, Container: previous.ContainerID, HostPort: previous.HostPort, URL: previous.URL, ImageID: previous.ImageID, Digest: previous.ImageDigest}
		report.Start("ready")
		ready, readyErr := runner.WaitReady(ctx, instance, options.ReadyTimeout)
		if readyErr == nil {
			report.StageDone("ready", instance.URL)
			result.Outcome = "ready"
			result.NextOperation = "reuse_instance"
			result.Runtime = &ready
			result.Instance = &instance
			return result, nil
		}
	}
	if previous.ContainerID != "" {
		old := localruntime.Instance{ID: previous.InstanceID, Container: previous.ContainerID, HostPort: previous.HostPort, URL: previous.URL, ImageID: previous.ImageID, Digest: previous.ImageDigest}
		_ = runner.Stop(ctx, old)
		_ = runner.Remove(ctx, old)
	}
	buildEngine := options.Builder
	if buildEngine == nil {
		buildEngine = builder.Docker{}
	}
	report.Start("build")
	image, err := buildEngine.Build(ctx, archive)
	if err != nil {
		report.Fail(err)
		return Result{}, err
	}
	report.StageDone("build", image.ID)
	instanceID := previous.InstanceID
	if instanceID == "" {
		instanceID = "inst_" + applicationID[4:]
	}
	report.Start("start")
	instance, err := runner.Start(ctx, instanceID, image.ID, image.Digest, previous.HostPort, manifest.Run.Port)
	if err != nil {
		report.Fail(err)
		return Result{}, err
	}
	report.StageDone("start", instance.ID)
	report.Start("ready")
	ready, err := runner.WaitReady(ctx, instance, options.ReadyTimeout)
	if err != nil {
		_ = runner.Remove(ctx, instance)
		report.Fail(err)
		return Result{}, err
	}
	report.StageDone("ready", instance.URL)
	if err := store.Save(state.Instance{ApplicationID: applicationID, Root: root, InstanceID: instance.ID, ContainerID: instance.Container, ImageID: instance.ImageID, ImageDigest: instance.Digest, ContentDigest: archive.Content.Digest, HostPort: instance.HostPort, ContainerPort: manifest.Run.Port, URL: instance.URL}); err != nil {
		return Result{}, err
	}
	result.Outcome = "ready"
	result.NextOperation = "reuse_instance"
	result.Runtime = &ready
	result.Instance = &instance
	return result, nil
}

// packageSummary is the trailing note the "package" stage shows once done —
// file count and compressed size, without the digests (which stay in the JSON
// output for anyone who needs them).
func packageSummary(archive source.Archive) string {
	return fmt.Sprintf("%d files, %s", archive.Content.FileCount, humanSize(archive.CompressedSize))
}

func humanSize(size int64) string {
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

// firstLine keeps a Railpack log usable inside an aligned evidence block. Its
// detection failure message is a multi-paragraph document that lists every
// language Railpack knows, including ones Shed cannot build.
func firstLine(message string) string {
	line, _, _ := strings.Cut(message, "\n")
	return strings.TrimSpace(line)
}

func firstLog(result *core.BuildResult) string {
	if len(result.Logs) == 0 {
		return "no supported provider produced a plan"
	}
	return result.Logs[len(result.Logs)-1].Msg
}

// ResolveDefinition loads the authoritative definition when one is present —
// the SHED program or the SHED.hcl manifest, never both. Detection is only
// scaffolding for projects that do not have a definition yet.
func ResolveDefinition(root string, generator definition.DefinitionGenerator) (definition.GeneratedDefinition, error) {
	program, manifest, err := readDefinitionFiles(root)
	if err != nil {
		return definition.GeneratedDefinition{}, err
	}
	if program != nil {
		generated, diags := evaluateProgram(root, program)
		if len(diags) > 0 {
			return definition.GeneratedDefinition{}, firstDiagnostic(diags)
		}
		return generated, nil
	}
	if manifest != nil {
		parsed, parseErr := definition.ParseManifest(manifest)
		if parseErr != nil {
			return definition.GeneratedDefinition{}, parseErr
		}
		return definition.GeneratedDefinition{Manifest: parsed, Source: manifest}, nil
	}
	return GenerateDefinition(root, generator)
}

// readDefinitionFiles reads whichever definition files exist and fails when
// both do: two authoritative definitions cannot decide one build.
func readDefinitionFiles(root string) (program, manifest []byte, err error) {
	program, programErr := os.ReadFile(filepath.Join(root, shedfile.FileName))
	if programErr != nil {
		if !errors.Is(programErr, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("read %s: %w", shedfile.FileName, programErr)
		}
		program = nil
	}
	manifest, manifestErr := os.ReadFile(filepath.Join(root, definition.ManifestFileName))
	if manifestErr != nil {
		if !errors.Is(manifestErr, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("read %s: %w", definition.ManifestFileName, manifestErr)
		}
		manifest = nil
	}
	if program != nil && manifest != nil {
		return nil, nil, &diag.Error{
			Code:    "definition_conflict",
			Summary: fmt.Sprintf("This project has two definitions: %s and %s.", shedfile.FileName, definition.ManifestFileName),
			Facts:   []diag.Fact{{Label: "Directory", Value: root}},
			Hints:   []string{"Delete one of them; whichever remains decides the build."},
		}
	}
	return program, manifest, nil
}

// evaluateProgram runs the SHED program against the same collected file
// universe that packaging uses, so srcs can only ever select files that
// would actually ship.
func evaluateProgram(root string, program []byte) (definition.GeneratedDefinition, []*diag.Error) {
	universe, err := source.CollectFiles(root)
	if err != nil {
		return definition.GeneratedDefinition{}, []*diag.Error{{Code: "collect_failed", Summary: err.Error(), Cause: err}}
	}
	return shedfile.Evaluate(program, universe)
}

// firstDiagnostic keeps single-error commands like deploy readable: they stop
// at the first problem and point at `shed check`, which reports every one.
func firstDiagnostic(diags []*diag.Error) error {
	first := *diags[0]
	if len(diags) > 1 {
		first.Hints = append(append([]string(nil), first.Hints...),
			fmt.Sprintf("%d diagnostics in total — see them all: shed check", len(diags)))
	}
	return &first
}

// Check validates the authored definition and reports every diagnostic it
// finds, rather than stopping at the first. It returns the evaluated
// definition when the diagnostics list is empty, and the name of the file it
// checked either way.
func Check(root string) (definition.GeneratedDefinition, string, []*diag.Error, error) {
	program, manifest, err := readDefinitionFiles(root)
	if err != nil {
		return definition.GeneratedDefinition{}, "", nil, err
	}
	if program != nil {
		generated, diags := evaluateProgram(root, program)
		return generated, shedfile.FileName, diags, nil
	}
	if manifest != nil {
		parsed, parseErr := definition.ParseManifest(manifest)
		if parseErr != nil {
			return definition.GeneratedDefinition{}, definition.ManifestFileName,
				[]*diag.Error{{Code: "invalid_definition", Summary: parseErr.Error(), Cause: parseErr}}, nil
		}
		return definition.GeneratedDefinition{Manifest: parsed, Source: manifest}, definition.ManifestFileName, nil, nil
	}
	return definition.GeneratedDefinition{}, "", nil, &diag.Error{
		Code:    "missing_definition",
		Summary: "There is no definition to check.",
		Facts:   []diag.Fact{{Label: "Looked for", Value: shedfile.FileName + " or " + definition.ManifestFileName}},
		Hints: []string{
			"Create one: shed init",
			"Shed can also build without one, by detection: shed deploy",
		},
	}
}

func GenerateDefinition(root string, generator definition.DefinitionGenerator) (definition.GeneratedDefinition, error) {
	application, err := app.NewApp(root)
	if err != nil {
		return definition.GeneratedDefinition{}, fmt.Errorf("open application: %w", err)
	}
	defer func() { _ = application.Close() }()
	buildResult := core.GenerateBuildPlanWithResolver(application, app.NewEnvironment(nil), &core.GenerateBuildPlanOptions{RailpackVersion: "shed"}, resolver.NewUnresolved())
	if !buildResult.Success {
		return definition.GeneratedDefinition{}, &diag.Error{
			Code:    "detection_failed",
			Summary: "Shed could not find an application to build here.",
			Facts: []diag.Fact{
				{Label: "Directory", Value: root},
				{Label: "Railpack", Value: firstLine(firstLog(buildResult))},
				{Label: "Supported", Value: definition.SupportedProjects},
			},
			Hints: []string{
				"Run Shed from the project root, or point it at one: shed deploy <directory>",
				definition.HandWrittenHint,
				definition.DefinitionDocsHint,
			},
		}
	}
	files, err := source.CollectFiles(root)
	if err != nil {
		return definition.GeneratedDefinition{}, err
	}
	if generator == nil {
		generator = definition.RailpackGenerator{}
	}
	generated, err := generator.Generate(definition.GenerationInput{
		Files: files,
		Build: buildResult,
		Name:  definition.SanitizeProjectName(filepath.Base(filepath.Clean(root))),
	})
	if err != nil {
		return definition.GeneratedDefinition{}, fmt.Errorf("generate %s: %w", definition.ManifestFileName, err)
	}
	return generated, nil
}

// Initialize writes a fresh definition in the requested format ("hcl" or
// "shed"), or validates the one already on disk — whichever file exists,
// regardless of the requested format, because an existing definition is
// authoritative and is never regenerated. It returns the definition, whether
// a file was written, and the name of the file involved.
func Initialize(root string, generator definition.DefinitionGenerator, format string) (definition.GeneratedDefinition, bool, string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return definition.GeneratedDefinition{}, false, "", err
	}
	program, manifest, err := readDefinitionFiles(absoluteRoot)
	if err != nil {
		return definition.GeneratedDefinition{}, false, "", err
	}
	if program != nil {
		generated, diags := evaluateProgram(absoluteRoot, program)
		if len(diags) > 0 {
			return definition.GeneratedDefinition{}, false, shedfile.FileName, firstDiagnostic(diags)
		}
		return generated, false, shedfile.FileName, nil
	}
	if manifest != nil {
		parsed, parseErr := definition.ParseManifest(manifest)
		return definition.GeneratedDefinition{Manifest: parsed, Source: manifest}, false, definition.ManifestFileName, parseErr
	}
	generated, err := GenerateDefinition(absoluteRoot, generator)
	if err != nil {
		return definition.GeneratedDefinition{}, false, "", err
	}
	payload, filename := generated.Source, definition.ManifestFileName
	if format == "shed" {
		if payload, err = shedfile.Render(generated); err != nil {
			return definition.GeneratedDefinition{}, false, "", err
		}
		filename = shedfile.FileName
	}
	if err := writeAtomically(absoluteRoot, filename, payload); err != nil {
		return definition.GeneratedDefinition{}, false, "", err
	}
	return generated, true, filename, nil
}

func writeAtomically(root, filename string, payload []byte) error {
	temporary, err := os.CreateTemp(root, ".SHED-*.tmp")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, filepath.Join(root, filename))
}
