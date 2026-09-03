package definition

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/railwayapp/railpack/core"
	"github.com/railwayapp/railpack/core/app"
	"github.com/railwayapp/railpack/core/resolver"
)

// These tests pin what Railpack detection does and does not give Shed. They are
// deliberately about the boundary rather than about our own code: the whole
// lowering rests on these properties, so a Railpack upgrade that changes one
// should fail here instead of silently changing what Shed generates.

func detectFixture(t *testing.T, files map[string]string) *core.BuildResult {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	application, err := app.NewApp(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	return core.GenerateBuildPlanWithResolver(application, app.NewEnvironment(nil),
		&core.GenerateBuildPlanOptions{RailpackVersion: "shed"}, resolver.NewUnresolved())
}

var detectionFixtures = map[string]map[string]string{
	"node": {
		"package.json":      `{"scripts":{"start":"node index.js"}}`,
		"package-lock.json": `{"lockfileVersion":3}`,
		"index.js":          "console.log(1)\n",
	},
	"golang": {
		"go.mod":  "module example.com/x\n\ngo 1.24\n",
		"main.go": "package main\n\nfunc main() {}\n",
	},
	"python": {
		"main.py":          "app = 1\n",
		"requirements.txt": "flask==3.0.0\n",
	},
	"ruby": {
		"Gemfile":   "source 'https://rubygems.org'\n",
		"config.ru": "run App\n",
	},
	"deno":       {"deno.json": `{"tasks":{}}`, "main.ts": "Deno.serve(() => new Response(\"hi\"))\n"},
	"rust":       {"Cargo.toml": "[package]\nname = \"x\"\nversion = \"0.1.0\"\nedition = \"2021\"\n", "src/main.rs": "fn main() {}\n"},
	"java":       {"pom.xml": "<project><modelVersion>4.0.0</modelVersion></project>\n"},
	"dotnet":     {"app.csproj": "<Project Sdk=\"Microsoft.NET.Sdk.Web\"></Project>\n"},
	"staticfile": {"index.html": "<h1>hi</h1>\n"},
	"shell":      {"start.sh": "#!/bin/sh\necho hi\n"},
	"php":        {"index.php": "<?php echo 1;\n", "composer.json": `{"require":{}}`},
}

// No provider reports a port, and the plan schema has no field for one: Railpack
// expects the host to inject $PORT and the application to read it. Shed's
// manifest therefore carries an assumed port, never a detected one, and
// `shed init` says so rather than presenting the default as a finding.
func TestRailpackReportsNoPortForAnyProvider(t *testing.T) {
	for name, files := range detectionFixtures {
		t.Run(name, func(t *testing.T) {
			result := detectFixture(t, files)
			if !result.Success || result.Plan == nil {
				t.Skipf("fixture did not produce a plan")
			}
			if port, present := result.Plan.Deploy.Variables["PORT"]; present {
				t.Fatalf("Railpack now reports PORT=%q for %s; Shed's assumed port and the "+
					"wording in `shed init` should switch to using it", port, name)
			}
		})
	}
}

// Detection is by filename, never by behaviour. Nothing distinguishes an HTTP
// server from a CLI tool or a batch worker, so a project that never listens is
// detected, lowered, and built exactly like one that does — and only fails at
// the readiness probe. Any workload contract has to come from SHED.hcl.
func TestDetectionCannotTellAServerFromAWorker(t *testing.T) {
	worker := detectFixture(t, map[string]string{
		"go.mod":  "module example.com/worker\n\ngo 1.24\n",
		"main.go": "package main\n\nimport \"time\"\n\nfunc main() { for { time.Sleep(time.Second) } }\n",
	})
	server := detectFixture(t, map[string]string{
		"go.mod":  "module example.com/server\n\ngo 1.24\n",
		"main.go": "package main\n\nimport \"net/http\"\n\nfunc main() { _ = http.ListenAndServe(\":8080\", nil) }\n",
	})
	if !worker.Success || !server.Success {
		t.Fatal("both fixtures should produce a plan")
	}
	if worker.Plan.Deploy.StartCmd != server.Plan.Deploy.StartCmd {
		t.Fatalf("start commands differ: worker %q, server %q",
			worker.Plan.Deploy.StartCmd, server.Plan.Deploy.StartCmd)
	}
	for key := range worker.Metadata {
		if key == "providers" {
			continue
		}
		if worker.Metadata[key] != server.Metadata[key] {
			t.Fatalf("metadata %q distinguishes them: worker %q, server %q",
				key, worker.Metadata[key], server.Metadata[key])
		}
	}
}

// Several providers emit a shell string, not an argv: parameter expansion for
// the port, environment assignment prefixes, redirection, or a glob. Shed runs
// commands without a shell, so these cannot be lowered as they stand — which is
// the real obstacle to supporting those families, not the build steps.
func TestSomeProviderStartCommandsAreShellStringsNotArgv(t *testing.T) {
	shellOnly := map[string]bool{
		"ruby":       true, // bundle exec rackup ... -p ${PORT:-3000}
		"java":       true, // java $JAVA_OPTS -jar target/*jar
		"dotnet":     true, // ASPNETCORE_URLS=http://0.0.0.0:${PORT:-3000} ./out/app
		"staticfile": true, // caddy run --config Caddyfile ... 2>&1
	}
	argvReady := map[string]bool{
		"node": true, "golang": true, "python": true, "deno": true, "shell": true,
	}

	for name, files := range detectionFixtures {
		wantShellOnly, checkedShell := shellOnly[name]
		wantArgv, checkedArgv := argvReady[name]
		if !checkedShell && !checkedArgv {
			continue
		}
		t.Run(name, func(t *testing.T) {
			result := detectFixture(t, files)
			if !result.Success || result.Plan == nil {
				t.Skipf("fixture did not produce a plan")
			}
			start := result.Plan.Deploy.StartCmd
			if start == "" {
				t.Skipf("fixture produced no start command")
			}
			_, err := splitCommand(start)
			switch {
			case wantShellOnly && err == nil:
				t.Fatalf("%s start command %q is now argv-safe; it could be lowered", name, start)
			case wantArgv && err != nil:
				t.Fatalf("%s start command %q is no longer argv-safe: %v", name, start, err)
			}
		})
	}
}
