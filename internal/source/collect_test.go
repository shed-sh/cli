package source

import (
	"slices"
	"testing"
)

// A root-anchored rule is how Go projects ignore their built binary, and the
// binary almost always shares its name with the cmd/ directory that produces it.
func TestRootAnchoredIgnoreRuleSparesSameNamedSubdirectory(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".gitignore", "/shed\n", 0o644)
	writeTestFile(t, root, "shed", "binary", 0o755)
	writeTestFile(t, root, "cmd/shed/main.go", "package main\n", 0o644)
	writeTestFile(t, root, "go.mod", "module example.com/shed\n", 0o644)

	files, err := CollectFiles(root)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(files, "cmd/shed/main.go") {
		t.Fatalf("root-anchored rule swallowed the subdirectory: %v", files)
	}
	if slices.Contains(files, "shed") {
		t.Fatalf("root-anchored rule failed to ignore the root binary: %v", files)
	}
}

func TestUnanchoredIgnoreRuleStillMatchesEveryLevel(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".gitignore", "coverage.html\nbuild/\n", 0o644)
	writeTestFile(t, root, "coverage.html", "report", 0o644)
	writeTestFile(t, root, "internal/coverage.html", "report", 0o644)
	writeTestFile(t, root, "internal/build/artifact.bin", "artifact", 0o644)
	writeTestFile(t, root, "main.go", "package main\n", 0o644)

	files, err := CollectFiles(root)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(files, []string{".gitignore", "main.go"}) {
		t.Fatalf("files = %v", files)
	}
}

func TestPathAnchoredIgnoreRuleMatchesOnlyThatPath(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, ".gitignore", "docs/generated\n", 0o644)
	writeTestFile(t, root, "docs/generated/api.md", "generated", 0o644)
	writeTestFile(t, root, "docs/manual.md", "manual", 0o644)
	writeTestFile(t, root, "internal/docs/generated/keep.md", "keep", 0o644)

	files, err := CollectFiles(root)
	if err != nil {
		t.Fatal(err)
	}

	if slices.Contains(files, "docs/generated/api.md") {
		t.Fatalf("anchored rule did not ignore its own path: %v", files)
	}
	if !slices.Contains(files, "internal/docs/generated/keep.md") || !slices.Contains(files, "docs/manual.md") {
		t.Fatalf("anchored rule reached beyond its path: %v", files)
	}
}
