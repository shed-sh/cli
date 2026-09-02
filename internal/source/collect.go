package source

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// CollectFiles returns a deterministic, structurally filtered list of source
// files. It performs no language or framework detection.
func CollectFiles(root string) ([]string, error) {
	matcher := loadIgnoreMatcher(root)
	var files []string
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename == root {
			return nil
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		if matcher.ignored(relative, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks are not supported in source content: %q", filepath.ToSlash(relative))
		}
		if !entry.IsDir() {
			files = append(files, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("collect source files: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

type ignoreRule struct {
	pattern string
	negate  bool
	dirOnly bool
	// anchored marks a pattern that may only match from the project root, which
	// Git spells with a leading or embedded slash. Without it, `/shed` (the built
	// binary) would also swallow `cmd/shed/`, a layout every Go project uses.
	anchored bool
}

type ignoreMatcher struct{ rules []ignoreRule }

var structurallyExcludedDirectories = map[string]struct{}{
	".shed": {}, ".git": {}, ".next": {}, ".turbo": {}, "coverage": {},
	"dist": {}, "node_modules": {}, "__pycache__": {}, ".venv": {}, "target": {},
}

var structurallyExcludedFiles = map[string]struct{}{
	".DS_Store": {}, ".git": {}, "SHED": {}, "SHED.yaml": {}, SourceManifestFileName: {},
	".npmrc": {}, ".pypirc": {}, "credentials.json": {},
}

func loadIgnoreMatcher(root string) ignoreMatcher {
	var matcher ignoreMatcher
	for _, name := range []string{".gitignore", ".shedignore"} {
		matcher.rules = append(matcher.rules, readIgnoreFile(filepath.Join(root, name))...)
	}
	return matcher
}

func readIgnoreFile(filename string) []ignoreRule {
	file, err := os.Open(filename)
	if err != nil {
		return nil
	}
	defer func() { _ = file.Close() }()
	var rules []ignoreRule
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		rule := ignoreRule{}
		if strings.HasPrefix(line, "!") {
			rule.negate = true
			line = strings.TrimPrefix(line, "!")
		}
		rule.dirOnly = strings.HasSuffix(line, "/")
		line = strings.TrimSuffix(line, "/")
		if strings.HasPrefix(line, "/") {
			rule.anchored = true
			line = strings.TrimPrefix(line, "/")
		}
		if strings.Contains(line, "/") {
			rule.anchored = true
		}
		if line != "" {
			rule.pattern = filepath.ToSlash(line)
			rules = append(rules, rule)
		}
	}
	return rules
}

func (m ignoreMatcher) ignored(relative string, isDir bool) bool {
	relative = filepath.ToSlash(relative)
	base := path.Base(relative)
	if isDir {
		if _, ok := structurallyExcludedDirectories[base]; ok {
			return true
		}
	}
	if _, ok := structurallyExcludedFiles[base]; ok {
		return true
	}
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".log") {
		return true
	}
	ignored := false
	for _, rule := range m.rules {
		if rule.dirOnly && !isDir {
			continue
		}
		if matchesIgnoreRule(rule, relative) {
			ignored = !rule.negate
		}
	}
	return ignored
}

func matchesIgnoreRule(rule ignoreRule, relative string) bool {
	if rule.anchored {
		matched, _ := path.Match(rule.pattern, relative)
		return matched || strings.HasPrefix(relative, rule.pattern+"/")
	}
	for _, part := range strings.Split(relative, "/") {
		if matched, _ := path.Match(rule.pattern, part); matched {
			return true
		}
	}
	return false
}
