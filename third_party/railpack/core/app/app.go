package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/railwayapp/railpack/internal/utils"
	"gopkg.in/yaml.v2"
)

var ErrNoFileFound = errors.New("unable to find a matching file")

type App struct {
	Source   string
	root     *os.Root
	recorder *EvidenceRecorder

	cacheMu   sync.RWMutex
	globCache map[string][]string
}

func NewApp(sourcePath string) (*App, error) {
	return NewAppWithRecorder(sourcePath, NewEvidenceRecorder())
}

func NewAppWithRecorder(sourcePath string, recorder *EvidenceRecorder) (*App, error) {
	source, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, errors.New("failed to resolve app source directory")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("directory %s does not exist", source)
		}
		return nil, fmt.Errorf("open app source directory: %w", err)
	}
	if recorder == nil {
		recorder = NewEvidenceRecorder()
	}
	return &App{
		Source:    source,
		root:      root,
		recorder:  recorder,
		globCache: make(map[string][]string),
	}, nil
}

func (a *App) Close() error {
	if a == nil || a.root == nil {
		return nil
	}
	return a.root.Close()
}

func (a *App) Sub(name string) (*App, error) {
	normalized, code, err := normalizeRelativePath(name)
	if err != nil {
		a.record(Evidence{Operation: "sub", Path: safeEvidencePath(name), Outcome: "error", ErrorCode: code})
		return nil, err
	}
	root, err := a.root.OpenRoot(normalized)
	if err != nil {
		a.record(Evidence{Operation: "sub", Path: normalized, Outcome: "error", ErrorCode: errorCode(err)})
		return nil, err
	}
	a.record(Evidence{Operation: "sub", Path: normalized, Outcome: "opened"})
	return &App{
		Source:    filepath.Join(a.Source, filepath.FromSlash(normalized)),
		root:      root,
		recorder:  a.recorder,
		globCache: make(map[string][]string),
	}, nil
}

func (a *App) Mark() uint64 {
	return a.recorder.Mark()
}

func (a *App) EvidenceSince(mark uint64) []Evidence {
	return a.recorder.Since(mark)
}

func (a *App) EvidenceTruncated() bool {
	return a.recorder.Truncated()
}

func (a *App) record(entry Evidence) {
	if a != nil && a.recorder != nil {
		a.recorder.record(entry)
	}
}

// findMatches returns a list of paths matching a glob pattern, filtered by isDir.
func (a *App) findMatches(pattern string, isDir bool) ([]string, error) {
	matches, err := a.findGlob(pattern)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(matches))
	for _, match := range matches {
		info, err := a.root.Stat(match)
		if err != nil {
			continue
		}
		if info.IsDir() == isDir {
			paths = append(paths, match)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func (a *App) FindFiles(pattern string) ([]string, error) {
	return a.findMatches(pattern, false)
}

func (a *App) FindDirectories(pattern string) ([]string, error) {
	return a.findMatches(pattern, true)
}

func (a *App) findGlob(pattern string) ([]string, error) {
	normalized, code, err := normalizePattern(pattern)
	if err != nil {
		a.record(Evidence{Operation: "glob", Path: safeEvidencePath(pattern), Outcome: "error", ErrorCode: code})
		return nil, err
	}

	a.cacheMu.RLock()
	cached, ok := a.globCache[normalized]
	a.cacheMu.RUnlock()
	if ok {
		matches, truncated := evidenceMatches(cached)
		a.record(Evidence{Operation: "glob", Path: normalized, Outcome: "matched", MatchCount: len(cached), Matches: matches, MatchesTruncated: truncated, Cached: true})
		return append([]string(nil), cached...), nil
	}

	matches, err := doublestar.Glob(a.root.FS(), normalized)
	if err != nil {
		a.record(Evidence{Operation: "glob", Path: normalized, Outcome: "error", ErrorCode: errorCode(err)})
		return nil, err
	}
	sort.Strings(matches)
	a.cacheMu.Lock()
	a.globCache[normalized] = append([]string(nil), matches...)
	a.cacheMu.Unlock()
	evidencePaths, truncated := evidenceMatches(matches)
	a.record(Evidence{Operation: "glob", Path: normalized, Outcome: "matched", MatchCount: len(matches), Matches: evidencePaths, MatchesTruncated: truncated})
	return append([]string(nil), matches...), nil
}

func (a *App) HasFile(name string) bool {
	normalized, code, err := normalizeRelativePath(name)
	if err != nil {
		a.record(Evidence{Operation: "stat", Path: safeEvidencePath(name), Outcome: "error", ErrorCode: code})
		return false
	}
	info, err := a.root.Stat(normalized)
	if err != nil {
		outcome := "error"
		if errors.Is(err, os.ErrNotExist) {
			outcome = "missing"
		}
		a.record(Evidence{Operation: "stat", Path: normalized, Outcome: outcome, ErrorCode: errorCode(err)})
		return false
	}
	outcome := "file"
	if info.IsDir() {
		outcome = "directory"
	}
	a.record(Evidence{Operation: "stat", Path: normalized, Outcome: outcome})
	return true
}

func (a *App) HasMatch(pattern string) bool {
	files, err := a.FindFiles(pattern)
	if err != nil {
		return false
	}
	dirs, err := a.FindDirectories(pattern)
	if err != nil {
		return false
	}
	return len(files) > 0 || len(dirs) > 0
}

func (a *App) FindFilesWithContent(pattern string, regex *regexp.Regexp) []string {
	files, err := a.FindFiles(pattern)
	if err != nil {
		return nil
	}

	var matches []string
	for _, file := range files {
		content, err := a.ReadFile(file)
		if err == nil && regex.MatchString(content) {
			matches = append(matches, file)
		}
	}
	return matches
}

func (a *App) ReadFirstFileOf(names ...string) (string, string, error) {
	for _, name := range names {
		if !a.HasFile(name) {
			continue
		}
		contents, err := a.ReadFile(name)
		if err != nil {
			return "", "", err
		}
		return name, contents, nil
	}
	return "", "", ErrNoFileFound
}

func (a *App) ReadFile(name string) (string, error) {
	normalized, code, err := normalizeRelativePath(name)
	if err != nil {
		a.record(Evidence{Operation: "read", Path: safeEvidencePath(name), Outcome: "error", ErrorCode: code})
		return "", err
	}
	data, err := a.root.ReadFile(normalized)
	if err != nil {
		a.record(Evidence{Operation: "read", Path: normalized, Outcome: "error", ErrorCode: errorCode(err)})
		return "", fmt.Errorf("error reading %s: %w", normalized, err)
	}
	digest := sha256.Sum256(data)
	a.record(Evidence{Operation: "read", Path: normalized, Outcome: "read", Digest: "sha256:" + hex.EncodeToString(digest[:])})
	return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
}

func (a *App) ReadJSON(name string, value any) error {
	data, err := a.ReadFile(name)
	if err != nil {
		return err
	}
	jsonBytes, err := utils.StandardizeJSON([]byte(data))
	if err == nil {
		err = json.Unmarshal(jsonBytes, value)
	}
	return a.recordParse("parse_json", name, err)
}

func (a *App) ReadYAML(name string, value any) error {
	data, err := a.ReadFile(name)
	if err != nil {
		return err
	}
	return a.recordParse("parse_yaml", name, yaml.Unmarshal([]byte(data), value))
}

func (a *App) ReadTOML(name string, value any) error {
	data, err := a.ReadFile(name)
	if err != nil {
		return err
	}
	return a.recordParse("parse_toml", name, toml.Unmarshal([]byte(data), value))
}

func (a *App) recordParse(operation, name string, parseErr error) error {
	normalized, _, normalizeErr := normalizeRelativePath(name)
	if normalizeErr != nil {
		normalized = safeEvidencePath(name)
	}
	if parseErr != nil {
		a.record(Evidence{Operation: operation, Path: normalized, Outcome: "error", ErrorCode: "invalid_format"})
		return fmt.Errorf("error reading %s as %s: %w", normalized, strings.TrimPrefix(operation, "parse_"), parseErr)
	}
	a.record(Evidence{Operation: operation, Path: normalized, Outcome: "parsed"})
	return nil
}

func (a *App) IsFileExecutable(name string) bool {
	normalized, code, err := normalizeRelativePath(name)
	if err != nil {
		a.record(Evidence{Operation: "executable", Path: safeEvidencePath(name), Outcome: "error", ErrorCode: code})
		return false
	}
	info, err := a.root.Stat(normalized)
	if err != nil {
		a.record(Evidence{Operation: "executable", Path: normalized, Outcome: "error", ErrorCode: errorCode(err)})
		return false
	}
	executable := info.Mode().IsRegular() && info.Mode()&0o111 != 0
	a.record(Evidence{Operation: "executable", Path: normalized, Outcome: fmt.Sprint(executable)})
	return executable
}

func normalizeRelativePath(name string) (string, string, error) {
	normalized := strings.ReplaceAll(name, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') || normalized == "" {
		return "", "invalid_path", errors.New("invalid empty or NUL-containing path")
	}
	if path.IsAbs(normalized) || hasWindowsVolume(normalized) {
		return "", "outside_root", fmt.Errorf("path %q is outside root", safeEvidencePath(name))
	}
	normalized = path.Clean(normalized)
	if normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", "outside_root", fmt.Errorf("path %q is outside root", safeEvidencePath(name))
	}
	if !fs.ValidPath(normalized) {
		return "", "invalid_path", fmt.Errorf("invalid path %q", safeEvidencePath(name))
	}
	return normalized, "", nil
}

func normalizePattern(pattern string) (string, string, error) {
	normalized := strings.ReplaceAll(pattern, "\\", "/")
	if strings.ContainsRune(normalized, '\x00') || normalized == "" {
		return "", "invalid_path", errors.New("invalid empty or NUL-containing pattern")
	}
	if path.IsAbs(normalized) || hasWindowsVolume(normalized) {
		return "", "outside_root", fmt.Errorf("pattern %q is outside root", safeEvidencePath(pattern))
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return "", "outside_root", fmt.Errorf("pattern %q is outside root", safeEvidencePath(pattern))
		}
	}
	for strings.HasPrefix(normalized, "./") {
		normalized = strings.TrimPrefix(normalized, "./")
	}
	return normalized, "", nil
}

func hasWindowsVolume(name string) bool {
	return len(name) >= 2 && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) && name[1] == ':'
}

func safeEvidencePath(name string) string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(name, "\\", "/"), "\x00", "")
	switch {
	case path.IsAbs(normalized):
		return "<absolute>"
	case hasWindowsVolume(normalized):
		return "<volume>"
	case normalized == "":
		return "<invalid>"
	default:
		return normalized
	}
}
