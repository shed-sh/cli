package e2e_test

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/railwayapp/railpack/core"
	"github.com/railwayapp/railpack/core/app"
	"github.com/railwayapp/railpack/core/resolver"
	"shed/internal/definition"
	"shed/internal/source"
)

type projectCase struct {
	name        string
	provider    string
	start       string
	toolchain   string
	packageable bool
}

func TestProjectDetectionAndPlanEndToEnd(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	fixtureRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "e2e", "repos")

	cases := []projectCase{
		{name: "nextjs-hello-world", provider: "node", start: "caddy run --config /Caddyfile --adapter caddyfile 2>&1", toolchain: "node", packageable: false},
		{name: "nextjs-postgres-auth-starter", provider: "node", start: "pnpm run start", toolchain: "node", packageable: true},
		{name: "next-learn", provider: "node", start: "pnpm run start", toolchain: "node", packageable: true},
		{name: "express-hello-world", provider: "node", start: "yarn run start", toolchain: "node", packageable: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			application, err := app.NewApp(filepath.Join(fixtureRoot, testCase.name))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = application.Close() })

			result := core.GenerateBuildPlanWithResolver(
				application,
				app.NewEnvironment(nil),
				&core.GenerateBuildPlanOptions{RailpackVersion: "e2e"},
				resolver.NewUnresolved(),
			)
			if !result.Success {
				t.Fatalf("generation failed: logs=%#v attempts=%#v", result.Logs, result.ProviderAttempts)
			}
			files, err := source.CollectFiles(filepath.Join(fixtureRoot, testCase.name))
			if err != nil {
				t.Fatalf("collect source: %v", err)
			}
			generated, err := (definition.RailpackGenerator{}).Generate(definition.GenerationInput{
				Files: files,
				Build: result,
			})
			if !testCase.packageable {
				if err == nil {
					t.Fatal("unsupported detected variant unexpectedly generated an executable definition")
				}
			} else if err != nil {
				t.Fatalf("generate SHED.yaml: %v", err)
			}
			if testCase.packageable {
				archive, err := source.Prepare(
					filepath.Join(fixtureRoot, testCase.name),
					filepath.Join(t.TempDir(), testCase.name+".tar.gz"),
					generated.YAML,
					generated.Manifest.Content.Include...,
				)
				if err != nil {
					t.Fatalf("prepare archive: %v", err)
				}
				if archive.Content.Digest == "" || archive.Digest == "" || archive.Content.FileCount < 2 {
					t.Fatalf("archive identity = %#v", archive)
				}
				if names := archiveEntries(t, archive.Path); !slices.Contains(names, definition.ManifestFileName) || !slices.Contains(names, "package.json") {
					t.Fatalf("archive entries = %#v", names)
				}
			}
			if len(result.DetectedProviders) != 1 || result.DetectedProviders[0] != testCase.provider {
				t.Fatalf("detected providers = %#v, want %q", result.DetectedProviders, testCase.provider)
			}
			if result.Plan == nil {
				t.Fatal("generation returned no deployment plan")
			}
			if result.Plan.Deploy.StartCmd != testCase.start {
				t.Fatalf("start command = %q, want %q", result.Plan.Deploy.StartCmd, testCase.start)
			}
			if len(result.ConfigurationEvidence) == 0 {
				t.Fatal("configuration evidence is empty")
			}
			if len(result.ProcfileEvidence) == 0 {
				t.Fatal("Procfile/configuration overlay evidence is empty")
			}

			selected := -1
			for index, attempt := range result.ProviderAttempts {
				if attempt.Selected {
					selected = index
				}
			}
			if selected < 0 || result.ProviderAttempts[selected].Name != testCase.provider {
				t.Fatalf("selected provider attempt = %#v", result.ProviderAttempts)
			}
			if len(result.ProviderAttempts[selected].DetectEvidence) == 0 || len(result.ProviderAttempts[selected].PlanEvidence) == 0 {
				t.Fatalf("provider evidence is incomplete: %#v", result.ProviderAttempts[selected])
			}

			if testCase.toolchain != "" {
				if _, ok := result.ResolvedPackages[testCase.toolchain]; !ok {
					t.Fatalf("requested toolchain %q missing from %#v", testCase.toolchain, result.ResolvedPackages)
				}
			}
			if os.Getenv("SHED_E2E_PRINT_PLAN") == "1" && testCase.name == "nextjs-hello-world" {
				encoded, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				t.Log(string(encoded))
			}
			if os.Getenv("SHED_E2E_PRINT_DEFINITION") == "1" && testCase.name == "next-learn" {
				encoded, err := json.MarshalIndent(generated.Manifest, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				t.Log(string(encoded))
			}
		})
	}
}

func archiveEntries(t *testing.T, filename string) []string {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = compressed.Close() }()
	reader := tar.NewReader(compressed)
	var names []string
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			return names
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		names = append(names, header.Name)
	}
}
