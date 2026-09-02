package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func newTestApp(t *testing.T, root string) *App {
	t.Helper()
	application, err := NewApp(root)
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}
	t.Cleanup(func() {
		if err := application.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return application
}

func writeTestFile(t *testing.T, filename, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(filename, []byte(contents), mode); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestAppRejectsUnsafePaths(t *testing.T) {
	root := t.TempDir()
	application := newTestApp(t, root)

	unsafe := []string{"", "../secret", "nested/../../secret", "/etc/passwd", `C:\\Windows\\system.ini`, "bad\x00path"}
	for _, name := range unsafe {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if application.HasFile(name) {
				t.Fatalf("HasFile(%q) unexpectedly succeeded", name)
			}
			if _, err := application.ReadFile(name); err == nil {
				t.Fatalf("ReadFile(%q) unexpectedly succeeded", name)
			}
		})
	}

	for _, evidence := range application.EvidenceSince(0) {
		if strings.HasPrefix(evidence.Path, "/") || strings.Contains(evidence.Path, root) {
			t.Fatalf("evidence leaked host path: %#v", evidence)
		}
	}
}

func TestAppConfinesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	writeTestFile(t, outside, "outside", 0o644)
	writeTestFile(t, filepath.Join(root, "inside", "value"), "inside", 0o644)
	if err := os.Symlink("inside/value", filepath.Join(root, "internal-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "absolute-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../"+filepath.Base(filepath.Dir(outside))+"/secret", filepath.Join(root, "escaping-link")); err != nil {
		t.Fatal(err)
	}

	application := newTestApp(t, root)
	contents, err := application.ReadFile("internal-link")
	if err != nil || contents != "inside" {
		t.Fatalf("internal symlink: contents=%q err=%v", contents, err)
	}
	for _, name := range []string{"absolute-link", "escaping-link"} {
		if _, err := application.ReadFile(name); err == nil {
			t.Fatalf("ReadFile(%q) unexpectedly escaped root", name)
		}
	}
	outsideErrors := 0
	for _, evidence := range application.EvidenceSince(0) {
		if evidence.Path == "absolute-link" || evidence.Path == "escaping-link" {
			if evidence.ErrorCode != "outside_root" {
				t.Fatalf("symlink error category = %#v", evidence)
			}
			outsideErrors++
		}
	}
	if outsideErrors != 2 {
		t.Fatalf("outside-root errors = %d", outsideErrors)
	}
}

func TestAppParsersExecutableAndNestedRoot(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "run.sh"), "#!/bin/sh\n", 0o755)
	writeTestFile(t, filepath.Join(root, "data.json5"), "{\"name\": \"shed\",}", 0o644)
	writeTestFile(t, filepath.Join(root, "data.yaml"), "name: shed\n", 0o644)
	writeTestFile(t, filepath.Join(root, "data.toml"), "name = 'shed'\n", 0o644)
	writeTestFile(t, filepath.Join(root, "invalid.json"), "{", 0o644)
	writeTestFile(t, filepath.Join(root, "assets", "package.json"), "{\"name\":\"assets\"}\n", 0o644)

	application := newTestApp(t, root)
	if !application.IsFileExecutable("run.sh") {
		t.Fatal("expected executable mode to be observed")
	}
	for name, parse := range map[string]func(any) error{
		"data.json5": func(value any) error { return application.ReadJSON("data.json5", value) },
		"data.yaml":  func(value any) error { return application.ReadYAML("data.yaml", value) },
		"data.toml":  func(value any) error { return application.ReadTOML("data.toml", value) },
	} {
		var value map[string]any
		if err := parse(&value); err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
	}
	var invalid map[string]any
	if err := application.ReadJSON("invalid.json", &invalid); err == nil {
		t.Fatal("invalid JSON unexpectedly parsed")
	}
	parseEvidence := application.EvidenceSince(0)
	if !slices.ContainsFunc(parseEvidence, func(entry Evidence) bool {
		return entry.Path == "invalid.json" && entry.ErrorCode == "invalid_format"
	}) {
		t.Fatalf("missing invalid_format evidence: %#v", parseEvidence)
	}

	mark := application.Mark()
	sub, err := application.Sub("assets")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sub.Close() })
	if !sub.HasFile("package.json") {
		t.Fatal("nested root did not find package.json")
	}
	if len(application.EvidenceSince(mark)) == 0 {
		t.Fatal("nested root did not share the evidence recorder")
	}
	if sub.HasFile("../run.sh") {
		t.Fatal("nested root escaped to its parent")
	}
}

func TestEvidenceUsesRawDigestAndDeterministicGlobs(t *testing.T) {
	root := t.TempDir()
	raw := []byte("first\r\nsecond\r\n")
	writeTestFile(t, filepath.Join(root, "b.txt"), "b", 0o644)
	writeTestFile(t, filepath.Join(root, "a.txt"), string(raw), 0o644)
	application := newTestApp(t, root)

	contents, err := application.ReadFile("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if contents != "first\nsecond\n" {
		t.Fatalf("newline normalization = %q", contents)
	}
	matches, err := application.FindFiles("*.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(matches, []string{"a.txt", "b.txt"}) {
		t.Fatalf("glob order = %v", matches)
	}
	if _, err := application.FindFiles("*.txt"); err != nil {
		t.Fatal(err)
	}

	wantDigestBytes := sha256.Sum256(raw)
	wantDigest := "sha256:" + hex.EncodeToString(wantDigestBytes[:])
	var sawDigest, sawCached bool
	for _, evidence := range application.EvidenceSince(0) {
		if evidence.Path == "a.txt" && evidence.Digest == wantDigest {
			sawDigest = true
		}
		if evidence.Operation == "glob" && evidence.Cached {
			sawCached = true
		}
		if strings.Contains(evidence.Path, contents) {
			t.Fatalf("evidence leaked file contents: %#v", evidence)
		}
	}
	if !sawDigest || !sawCached {
		t.Fatalf("digest=%v cached=%v", sawDigest, sawCached)
	}
}

func TestEvidenceDeduplicationAndLimits(t *testing.T) {
	recorder := NewEvidenceRecorder()
	recorder.record(Evidence{Operation: "stat", Path: "same", Outcome: "file"})
	recorder.record(Evidence{Operation: "stat", Path: "same", Outcome: "file"})
	if got := len(recorder.Since(0)); got != 1 {
		t.Fatalf("deduplicated entries = %d", got)
	}
	mark := recorder.Mark()
	recorder.record(Evidence{Operation: "stat", Path: "same", Outcome: "file"})
	if got := len(recorder.Since(mark)); got != 1 {
		t.Fatalf("phase-scoped entries = %d", got)
	}

	matches := make([]string, maxEvidenceMatches+1)
	for index := range matches {
		matches[index] = strings.Repeat("x", index+1)
	}
	kept, truncated := evidenceMatches(matches)
	if !truncated || len(kept) != maxEvidenceMatches {
		t.Fatalf("match limit: len=%d truncated=%v", len(kept), truncated)
	}

	limited := NewEvidenceRecorder()
	for index := 0; index <= maxEvidenceEntries; index++ {
		limited.record(Evidence{Operation: "stat", Path: string(rune(index)), Outcome: "missing"})
	}
	if !limited.Truncated() || len(limited.Since(0)) != maxEvidenceEntries {
		t.Fatalf("total limit: len=%d truncated=%v", len(limited.Since(0)), limited.Truncated())
	}
}
