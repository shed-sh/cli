package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/railwayapp/railpack/core/app"
	"github.com/railwayapp/railpack/core/config"
	"github.com/railwayapp/railpack/core/generate"
	"github.com/railwayapp/railpack/core/logger"
	"github.com/railwayapp/railpack/core/resolver"
)

func writeFixture(t *testing.T, root, name, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openFixtureApp(t *testing.T, root string) *app.App {
	t.Helper()
	application, err := app.NewApp(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.Close() })
	return application
}

func TestGenerateBuildPlanGroupsProviderEvidence(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"evidence","scripts":{"start":"node index.js"}}`)
	writeFixture(t, root, "index.js", "console.log('ready')\n")
	writeFixture(t, root, "Procfile", "web: node procfile.js\n")

	result := GenerateBuildPlanWithResolver(
		openFixtureApp(t, root),
		app.NewEnvironment(nil),
		&GenerateBuildPlanOptions{},
		resolver.NewUnresolved(),
	)
	if !result.Success {
		t.Fatalf("generation failed: %#v", result.Logs)
	}
	if len(result.ConfigurationEvidence) == 0 || result.ConfigurationEvidence[0].Path != defaultConfigFileName {
		t.Fatalf("configuration evidence = %#v", result.ConfigurationEvidence)
	}
	if len(result.ProcfileEvidence) == 0 {
		t.Fatal("missing Procfile evidence")
	}
	if result.Plan.Deploy.StartCmd != "node procfile.js" {
		t.Fatalf("Procfile overlay start command = %q", result.Plan.Deploy.StartCmd)
	}

	last := result.ProviderAttempts[len(result.ProviderAttempts)-1]
	if last.Name != "node" || !last.Matched || !last.Selected {
		t.Fatalf("selected attempt = %#v", last)
	}
	if len(last.DetectEvidence) == 0 || len(last.InitializeEvidence) == 0 || len(last.PlanEvidence) == 0 {
		t.Fatalf("phase evidence missing: %#v", last)
	}
}

func TestForcedProviderIsDistinctFromDetectedProvider(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"forced"}`)
	application := openFixtureApp(t, root)
	forced := "python"
	configuration := config.EmptyConfig()
	configuration.Provider = &forced
	ctx, err := generate.NewGenerateContextWithResolver(
		application,
		app.NewEnvironment(nil),
		configuration,
		logger.NewLogger(),
		resolver.NewUnresolved(),
	)
	if err != nil {
		t.Fatal(err)
	}

	selected, detected, attempts := getProviders(ctx, configuration)
	if selected == nil || selected.Name() != "python" || detected != "node" {
		t.Fatalf("selected=%v detected=%q", selected, detected)
	}
	pythonIndex := providerAttemptIndex(attempts, "python")
	nodeIndex := providerAttemptIndex(attempts, "node")
	if pythonIndex < 0 || nodeIndex < 0 {
		t.Fatalf("attempts = %#v", attempts)
	}
	if attempts[pythonIndex].Matched || !attempts[pythonIndex].Selected {
		t.Fatalf("forced attempt = %#v", attempts[pythonIndex])
	}
	if !attempts[nodeIndex].Matched || attempts[nodeIndex].Selected {
		t.Fatalf("detected attempt = %#v", attempts[nodeIndex])
	}
}
