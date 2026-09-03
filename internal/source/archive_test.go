package source

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestPrepareIsDeterministicAndExcludesSecrets(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "package.json", "{}", 0o644)
	writeTestFile(t, root, "package-lock.json", "{}", 0o644)
	writeTestFile(t, root, "src/server.js", "console.log('ok')", 0o755)
	writeTestFile(t, root, ".env", "SECRET=value", 0o644)
	writeTestFile(t, root, ".git", "gitdir: /outside/worktree", 0o644)
	manifest := []byte("application \"app\" {}\n")
	firstPath := filepath.Join(t.TempDir(), "first.tar.gz")
	secondPath := filepath.Join(t.TempDir(), "second.tar.gz")
	first, err := Prepare(root, firstPath, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(root, "src/server.js"), time.Now().Add(-time.Hour), time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	second, err := Prepare(root, secondPath, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if first.Content.Digest != second.Content.Digest || first.Digest != second.Digest {
		t.Fatalf("digests differ: %#v %#v", first, second)
	}
	firstBytes, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(firstBytes, secondBytes) {
		t.Fatal("archive bytes are not deterministic")
	}
	want := []string{".shed-source.json", "SHED.hcl", "package-lock.json", "package.json", "src/server.js"}
	if got := archiveNames(t, firstPath); !reflect.DeepEqual(got, want) {
		t.Fatalf("archive names = %#v, want %#v", got, want)
	}
	for _, entry := range first.Content.Files {
		if entry.Path == "src/server.js" && entry.Mode != "0755" {
			t.Fatalf("executable mode = %q", entry.Mode)
		}
		if entry.Path == ".env" {
			t.Fatal("secret entered canonical manifest")
		}
	}
}

func TestPrepareRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "package.json", "{}", 0o644)
	if err := os.Symlink("package.json", filepath.Join(root, "linked.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(root, filepath.Join(t.TempDir(), "bundle.tar.gz"), []byte("manifest")); err == nil {
		t.Fatal("expected symlink rejection")
	}
}

func TestPrepareRejectsMissingDeclaredContent(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "main.go", "package main\n", 0o644)
	if _, err := Prepare(root, filepath.Join(t.TempDir(), "bundle.tar.gz"), []byte("manifest"), "missing.txt"); err == nil {
		t.Fatal("expected missing declared content rejection")
	}
}

func writeTestFile(t *testing.T, root, relative, content string, mode os.FileMode) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func archiveNames(t *testing.T, filename string) []string {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = gzipReader.Close() }()
	reader := tar.NewReader(gzipReader)
	var names []string
	for {
		header, nextErr := reader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			t.Fatal(nextErr)
		}
		names = append(names, header.Name)
	}
	return names
}
