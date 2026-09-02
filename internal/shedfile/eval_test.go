package shedfile

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"shed/internal/definition"
	"shed/internal/diag"
	"shed/internal/source"
)

var goUniverse = []string{"cmd/app/main.go", "go.mod", "go.sum", "internal/api/handler.go"}

const goProgram = `b = build(
    srcs = ["cmd", "go.mod", "go.sum", "internal"],
    image = "golang:1.26",
    commands = [
        ["go", "mod", "download"],
        ["go", "build", "-o", "out", "./cmd/app"],
    ],
)

http_app(
    name = "hello-api",
    build = b,
    cmd = ["./out"],
    port = 8080,
)
`

// The program and a hand-assembled manifest must marshal to the same bytes:
// the DSL is a different spelling of SHED.yaml, not a different contract.
func TestEvaluateMatchesHandWrittenManifest(t *testing.T) {
	generated, diags := Evaluate([]byte(goProgram), goUniverse)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", renderAll(diags))
	}
	expected := definition.Manifest{
		APIVersion: definition.ManifestAPIVersion,
		Kind:       definition.ManifestKind,
		Metadata:   &definition.ManifestMetadata{Name: "hello-api"},
		Content:    definition.ManifestContent{Include: []string{"cmd", "go.mod", "go.sum", "internal"}},
		Build: definition.ManifestBuild{
			Image:    "golang:1.26",
			Commands: [][]string{{"go", "mod", "download"}, {"go", "build", "-o", "out", "./cmd/app"}},
		},
		Run: definition.ManifestRun{
			Command:          []string{"./out"},
			WorkingDirectory: "/app",
			User:             "1000:1000",
			Environment:      map[string]string{"PORT": "8080"},
			Port:             8080,
			StopSignal:       "SIGTERM",
		},
	}
	expectedYAML, err := expected.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated.YAML, expectedYAML) {
		t.Fatalf("YAML differs:\n%s\n---\n%s", generated.YAML, expectedYAML)
	}
	if !reflect.DeepEqual(generated.Manifest, expected) {
		t.Fatalf("manifest = %#v", generated.Manifest)
	}
	if generated.Provider != "" {
		t.Fatalf("provider = %q; an authored file detects nothing", generated.Provider)
	}

	again, diags := Evaluate([]byte(goProgram), goUniverse)
	if len(diags) > 0 || !bytes.Equal(again.YAML, generated.YAML) {
		t.Fatalf("second evaluation differed: %v", renderAll(diags))
	}
}

// Each rule fails with a stable code, a SHED:line:col position, and — where
// one exists — a concrete suggestion. Agents branch on all three.
func TestRuleViolations(t *testing.T) {
	appTail := `
http_app(
    name = "hello",
    build = b,
    cmd = ["./out"],
    port = 8080,
)
`
	simpleBuild := `b = build(
    srcs = ["go.mod"],
    image = "golang:1.26",
)
`
	cases := []struct {
		name     string
		program  string
		code     string
		position string
		want     []string
	}{
		{
			name:     "unknown argument suggests the nearest one",
			program:  simpleBuild + "\nhttp_app(\n    name = \"hello\",\n    build = b,\n    cmd = [\"./out\"],\n    prot = 8080,\n)\n",
			code:     "unknown_argument",
			position: "SHED:6:9",
			want:     []string{`unknown argument "prot"`, "Did you mean: port", "http_app() accepts: name, build, cmd, port, env, working_directory, user, stop_signal"},
		},
		{
			name:     "positional arguments are rejected",
			program:  "b = build(\n    [\"go.mod\"],\n    image = \"golang:1.26\",\n    srcs = [\"go.mod\"],\n)\n" + appTail,
			code:     "positional_argument",
			position: "SHED:1:10",
			want:     []string{"keyword arguments only", "build(srcs = ..., image = ..., commands = ...)"},
		},
		{
			name:     "srcs takes a list not a string",
			program:  "b = build(\n    srcs = \"go.mod\",\n    image = \"golang:1.26\",\n)\n" + appTail,
			code:     "invalid_argument_type",
			position: "SHED:1:10",
			want:     []string{`argument "srcs" wants glob([...]) or list of paths, got string`},
		},
		{
			name:     "cmd takes an argv list not a shell string",
			program:  simpleBuild + "\nhttp_app(\n    name = \"hello\",\n    build = b,\n    cmd = \"./out\",\n    port = 8080,\n)\n",
			code:     "invalid_argument_type",
			position: "SHED:6:9",
			want:     []string{`argument "cmd" wants argv list of strings, got string`, `cmd = ["./out"]`},
		},
		{
			name:     "second app is rejected",
			program:  simpleBuild + appTail + "\nhttp_app(\n    name = \"second\",\n    build = b,\n    cmd = [\"./out\"],\n    port = 8081,\n)\n",
			code:     "duplicate_app",
			position: "SHED:13:9",
			want:     []string{"multiple apps are not supported yet"},
		},
		{
			name:     "unused build is an error",
			program:  simpleBuild + "c = build(\n    srcs = [\"go.mod\"],\n    image = \"golang:1.26\",\n)\n" + appTail,
			code:     "unused_build",
			position: "SHED:5:10",
			want:     []string{"never used", "http_app(build = <variable>, ...)"},
		},
		{
			name:     "inline build is rejected",
			program:  "http_app(\n    name = \"hello\",\n    build = build(srcs = [\"go.mod\"], image = \"golang:1.26\"),\n    cmd = [\"./out\"],\n    port = 8080,\n)\n",
			code:     "inline_build",
			position: "SHED:3:13",
			want:     []string{"may not be written inline", "b = build(...)"},
		},
		{
			name:     "name over the DNS budget",
			program:  simpleBuild + "\nhttp_app(\n    name = \"this-name-is-far-too-long-to-be-a-dns-label\",\n    build = b,\n    cmd = [\"./out\"],\n    port = 8080,\n)\n",
			code:     "invalid_name",
			position: "SHED:6:9",
			want:     []string{"not a valid application name", "lowercase DNS label of at most 30 characters"},
		},
		{
			name:     "unknown srcs entry names the nearest path",
			program:  "b = build(\n    srcs = [\"go.mo\"],\n    image = \"golang:1.26\",\n)\n" + appTail,
			code:     "unknown_src",
			position: "SHED:1:10",
			want:     []string{`"go.mo" does not match any collected file`, "Did you mean: go.mod"},
		},
		{
			name:     "missing required argument",
			program:  simpleBuild + "\nhttp_app(\n    name = \"hello\",\n    build = b,\n    cmd = [\"./out\"],\n)\n",
			code:     "missing_argument",
			position: "SHED:6:9",
			want:     []string{`missing required argument "port"`},
		},
		{
			name:     "unknown constructor states the whole surface",
			program:  simpleBuild + "\nworker(name = \"w\")\n" + appTail,
			code:     "unknown_name",
			position: "SHED:6:1",
			want:     []string{"undefined: worker", "Only build(), http_app(), and glob() exist", "param(), worker(), static_site()"},
		},
		{
			name:     "syntax error carries its position",
			program:  "b = build(\n    srcs = [\"go.mod\",\n",
			code:     "syntax_error",
			position: "SHED:",
			want:     nil,
		},
		{
			name:     "empty srcs cannot build anything",
			program:  "b = build(\n    srcs = glob([\"nothing/**\"]),\n    image = \"golang:1.26\",\n)\n" + appTail,
			code:     "empty_srcs",
			position: "SHED:1:10",
			want:     []string{"srcs selected no files"},
		},
		{
			name:     "declaring no app is an error",
			program:  "",
			code:     "missing_app",
			position: "",
			want:     []string{"declares no application", "http_app(name = ..., build = ..., cmd = ..., port = ...)"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, diags := Evaluate([]byte(testCase.program), goUniverse)
			found := findDiag(diags, testCase.code)
			if found == nil {
				t.Fatalf("no %s diagnostic in: %v", testCase.code, renderAll(diags))
			}
			if testCase.position != "" && !strings.HasPrefix(found.Summary, testCase.position) {
				t.Fatalf("position: summary = %q, want prefix %q", found.Summary, testCase.position)
			}
			full := found.Summary + "\n" + strings.Join(found.Hints, "\n")
			for _, want := range testCase.want {
				if !strings.Contains(full, want) {
					t.Fatalf("diagnostic %q missing %q", full, want)
				}
			}
		})
	}
}

// One pass reports every problem: the five-mistake file yields five
// diagnostics, so an agent fixes the file in one edit instead of five loops.
func TestMultipleErrorsReportedInOnePass(t *testing.T) {
	program := `b = build(
    srcs = ["go.mo"],
    image = "golang:1.26",
)
c = build(
    srcs = ["go.mod"],
    image = "golang:1.26",
)
http_app(
    name = "hello",
    build = b,
    cmd = "./out",
    prot = 8080,
)
`
	_, diags := Evaluate([]byte(program), goUniverse)
	if len(diags) != 5 {
		t.Fatalf("want 5 diagnostics, got %d: %v", len(diags), renderAll(diags))
	}
	for _, code := range []string{"unknown_src", "invalid_argument_type", "unknown_argument", "missing_argument", "unused_build"} {
		if findDiag(diags, code) == nil {
			t.Fatalf("missing %s in: %v", code, renderAll(diags))
		}
	}
}

// glob filters the same universe the collector reports, so ignored files and
// secrets are unreachable no matter how broad the pattern.
func TestGlobFiltersTheCollectorUniverse(t *testing.T) {
	root := t.TempDir()
	for filename, content := range map[string]string{
		"cmd/app/main.go": "package main\n",
		"go.mod":          "module example.com/app\n",
		".env":            "SECRET=1\n",
		"server.pem":      "key material\n",
		"README.md":       "# app\n",
		".gitignore":      "ignored/\n",
		"ignored/x.txt":   "x\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(filename))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	universe, err := source.CollectFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	program := `b = build(
    srcs = glob(["**"], exclude = ["README.md"]),
    image = "golang:1.26",
)

http_app(
    name = "hello",
    build = b,
    cmd = ["./out"],
    port = 8080,
)
`
	generated, diags := Evaluate([]byte(program), universe)
	if len(diags) > 0 {
		t.Fatalf("diagnostics: %v", renderAll(diags))
	}
	include := generated.Manifest.Content.Include
	for _, banned := range []string{".env", "server.pem", "ignored/x.txt", "README.md"} {
		for _, entry := range include {
			if entry == banned {
				t.Fatalf("%q leaked into include: %v", banned, include)
			}
		}
	}
	if !contains(include, "cmd/app/main.go") || !contains(include, "go.mod") {
		t.Fatalf("include = %v", include)
	}

	directory, diags := Evaluate([]byte(strings.Replace(program, `glob(["**"], exclude = ["README.md"])`, `glob(["cmd"])`, 1)), universe)
	if len(diags) > 0 || !reflect.DeepEqual(directory.Manifest.Content.Include, []string{"cmd/app/main.go"}) {
		t.Fatalf("directory glob: include=%v diags=%v", directory.Manifest.Content.Include, renderAll(diags))
	}
}

// Rendering and evaluating are inverses over the pure-literal subset.
func TestRenderRoundTrip(t *testing.T) {
	manifest := definition.Manifest{
		APIVersion: definition.ManifestAPIVersion,
		Kind:       definition.ManifestKind,
		Metadata:   &definition.ManifestMetadata{Name: "web-service"},
		Content:    definition.ManifestContent{Include: []string{"index.js", "package-lock.json", "package.json", "src"}},
		Build: definition.ManifestBuild{
			Image:    "node:22",
			Commands: [][]string{{"npm", "ci"}},
		},
		Run: definition.ManifestRun{
			Command:          []string{"node", "index.js"},
			WorkingDirectory: "/app",
			User:             "1000:1000",
			Environment:      map[string]string{"NODE_ENV": "production", "PORT": "3000"},
			Port:             3000,
			StopSignal:       "SIGTERM",
		},
	}
	yamlData, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(definition.GeneratedDefinition{Manifest: manifest, YAML: yamlData, Provider: "node"})
	if err != nil {
		t.Fatal(err)
	}
	universe := []string{"index.js", "package-lock.json", "package.json", "src/app.js"}
	evaluated, diags := Evaluate(rendered, universe)
	if len(diags) > 0 {
		t.Fatalf("rendered program does not evaluate:\n%s\n%v", rendered, renderAll(diags))
	}
	if !bytes.Equal(evaluated.YAML, yamlData) {
		t.Fatalf("round trip differs:\n%s\n---\n%s", evaluated.YAML, yamlData)
	}
	if !strings.Contains(string(rendered), "# Detected: Node.js") {
		t.Fatalf("rendered file lost its provenance:\n%s", rendered)
	}
}

func TestSchemaCoversEveryBuiltin(t *testing.T) {
	schema := APISchema()
	if len(schema.Builtins) != 3 {
		t.Fatalf("builtins = %d", len(schema.Builtins))
	}
	var stub strings.Builder
	RenderSchema(&stub)
	for _, name := range []string{"build(*, srcs, image, commands = [])", "http_app(*, name, build, cmd, port", "glob(patterns, *, exclude = [])"} {
		if !strings.Contains(stub.String(), name) {
			t.Fatalf("stub missing %q:\n%s", name, stub.String())
		}
	}
}

func findDiag(diags []*diag.Error, code string) *diag.Error {
	for _, diagnostic := range diags {
		if diagnostic.Code == code {
			return diagnostic
		}
	}
	return nil
}

func renderAll(diags []*diag.Error) []string {
	var out []string
	for _, diagnostic := range diags {
		out = append(out, diagnostic.Code+": "+diagnostic.Summary)
	}
	return out
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
