package railpack_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/railwayapp/railpack/core/providers"
	"github.com/railwayapp/railpack/core/providers/procfile"
)

func TestProviderPrecedenceAndProcfileOverlay(t *testing.T) {
	providerList := providers.GetLanguageProviders()
	names := make([]string, len(providerList))
	for index, provider := range providerList {
		names[index] = provider.Name()
	}
	want := []string{
		"php",
		"golang",
		"java",
		"rust",
		"ruby",
		"Elixir",
		"python",
		"deno",
		"Dotnet",
		"node",
		"gleam",
		"cpp",
		"staticfile",
		"shell",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("provider order = %#v, want %#v", names, want)
	}

	overlay := (&procfile.ProcfileProvider{}).Name()
	if overlay != "procfile" {
		t.Fatalf("Procfile overlay name = %q", overlay)
	}
	for _, name := range names {
		if name == overlay {
			t.Fatal("Procfile must remain a post-detection overlay")
		}
	}
}

func TestVendoredProductionBoundary(t *testing.T) {
	vendorRoot := filepath.Join("..", "..", "third_party", "railpack")
	forbidden := []string{
		"github.com/moby/buildkit",
		"github.com/docker/",
		"github.com/containerd/",
		"github.com/urfave/cli",
	}

	err := filepath.WalkDir(vendorRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, importPath := range forbidden {
			if strings.Contains(string(contents), `"`+importPath) {
				t.Errorf("production file %s imports forbidden package %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	module, err := os.ReadFile(filepath.Join(vendorRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range forbidden {
		if strings.Contains(string(module), dependency) {
			t.Errorf("nested go.mod contains forbidden dependency %s", dependency)
		}
	}
}
