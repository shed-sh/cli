package source

import (
	"os"
	"path/filepath"
	"testing"

	"shed/internal/definition"
)

func TestExtractVerifiesAndMaterializesSelfContainedArchive(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "main.py", "print('hello')\n", 0o755)
	manifest := definition.Manifest{
		APIVersion: definition.ManifestAPIVersion,
		Kind:       definition.ManifestKind,
		Content:    definition.ManifestContent{Include: []string{"main.py"}},
		Build:      definition.ManifestBuild{Image: "python:3.13"},
		Run:        definition.ManifestRun{Command: []string{"python", "main.py"}, Port: 8000},
	}
	data, err := manifest.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	archive, err := Prepare(root, filepath.Join(t.TempDir(), "source.tar.gz"), data, manifest.Content.Include...)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "extracted")
	got, err := Extract(archive, destination)
	if err != nil {
		t.Fatal(err)
	}
	if got.Build.Image != manifest.Build.Image {
		t.Fatalf("manifest=%+v", got)
	}
	if content, err := os.ReadFile(filepath.Join(destination, "main.py")); err != nil || string(content) != "print('hello')\n" {
		t.Fatalf("content=%q err=%v", content, err)
	}
}

func TestExtractRejectsReceiptMismatch(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "main.py", "print('hello')\n", 0o644)
	manifest := definition.Manifest{APIVersion: definition.ManifestAPIVersion, Kind: definition.ManifestKind, Content: definition.ManifestContent{Include: []string{"main.py"}}, Build: definition.ManifestBuild{Image: "python:3.13"}, Run: definition.ManifestRun{Command: []string{"python", "main.py"}, Port: 8000}}
	data, _ := manifest.Marshal()
	archive, err := Prepare(root, filepath.Join(t.TempDir(), "source.tar.gz"), data, "main.py")
	if err != nil {
		t.Fatal(err)
	}
	archive.Digest = "sha256:incorrect"
	if _, err := Extract(archive, filepath.Join(t.TempDir(), "extracted")); err == nil {
		t.Fatal("expected archive identity rejection")
	}
}
