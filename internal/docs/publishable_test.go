package docs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The skill is mirrored to a public repository on release, and its audience is
// end users of the CLI rather than contributors. Anything that only makes sense
// from inside this repository is therefore a defect in the skill, not merely an
// oversight in the publishing step — a reader cannot open internal/, resolve a
// commit, or run a task target, and telling them about unbuilt work discloses
// roadmap they did not ask for.
var forbidden = []struct {
	name    string
	pattern *regexp.Regexp
	why     string
}{
	{
		name:    "internal source path",
		pattern: regexp.MustCompile(`\binternal/[a-z]+/`),
		why:     "readers cannot open this repository's source",
	},
	{
		name:    "task target",
		pattern: regexp.MustCompile("`?\\btask (generate|check|build|test)\\b"),
		why:     "readers have the binary, not the Taskfile",
	},
	{
		name:    "commit reference",
		pattern: regexp.MustCompile(`\bcommit\s+` + "`?" + `[0-9a-f]{7,40}`),
		why:     "readers cannot resolve commits in a private repository",
	},
	{
		name:    "unbuilt-state disclosure",
		pattern: regexp.MustCompile(`(?i)not yet implemented|on the roadmap|roadmap but|in this repo state|fake-server|does not exist yet`),
		why:     "describe what the released binary does, not what it does not do yet",
	},
}

// TestSkillIsPublishable keeps the skill shippable to a public audience at all
// times, so publishing stays a file copy rather than a review.
func TestSkillIsPublishable(t *testing.T) {
	root := filepath.Join("..", "..", "skills")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for number, line := range strings.Split(string(content), "\n") {
			for _, rule := range forbidden {
				if match := rule.pattern.FindString(line); match != "" {
					t.Errorf("%s:%d contains a %s (%q): %s", path, number+1, rule.name, match, rule.why)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}
}

// TestSkillHasFrontmatter checks the one thing that silently stops a skill from
// ever loading: a SKILL.md whose YAML frontmatter is missing or unterminated.
func TestSkillHasFrontmatter(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "skills", "shed", "SKILL.md"))
	if err != nil {
		t.Fatalf("reading SKILL.md: %v", err)
	}
	text := string(content)
	if !strings.HasPrefix(text, "---\n") {
		t.Fatal("SKILL.md does not open with YAML frontmatter, so the skill will never load")
	}
	closing := strings.Index(text[4:], "\n---\n")
	if closing < 0 {
		t.Fatal("SKILL.md frontmatter is never closed")
	}
	frontmatter := text[4 : closing+4]
	for _, field := range []string{"name:", "description:"} {
		if !strings.Contains(frontmatter, field) {
			t.Errorf("SKILL.md frontmatter has no %s", field)
		}
	}
}

func TestSkillsNpmVersionMatchesRelease(t *testing.T) {
	root := filepath.Join("..", "..")
	workspace, err := os.ReadFile(filepath.Join(root, "dist-workspace.toml"))
	if err != nil {
		t.Fatal(err)
	}
	pkgJSON, err := os.ReadFile(filepath.Join(root, "packages", "skills", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	distVersion := firstTOMLVersion(string(workspace))
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pkgJSON, &pkg); err != nil {
		t.Fatal(err)
	}
	if pkg.Name != "@shed-sh/skills" {
		t.Fatalf("skills package name = %q", pkg.Name)
	}
	if pkg.Version != distVersion {
		t.Fatalf("packages/skills version = %q, dist-workspace.toml version = %q", pkg.Version, distVersion)
	}
}

func firstTOMLVersion(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "version = \"") && strings.HasSuffix(line, "\"") {
			return strings.Trim(line[len("version = "):], "\"")
		}
	}
	return ""
}
