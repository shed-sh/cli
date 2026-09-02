// Command shed-docs regenerates the Shed skill's reference material from the
// clispec registry and the SHED evaluator's spec tables. It is a build-time
// tool, not part of the released binary:
// .goreleaser.yaml builds only ./cmd/shed.
//
// Run it with `task generate`. With -check it writes nothing and fails when the
// files on disk differ from what it would produce, which is what `task check`
// and CI use to keep the committed skill honest.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"shed/internal/docs"
)

func main() {
	check := flag.Bool("check", false, "report stale files instead of writing, and exit non-zero if any differ")
	flag.Parse()

	if err := run(*check); err != nil {
		fmt.Fprintf(os.Stderr, "shed-docs: %v\n", err)
		os.Exit(1)
	}
}

// target is one file this tool owns the content of.
type target struct {
	path    string
	content string
}

func run(check bool) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}

	targets, err := plan(root)
	if err != nil {
		return err
	}

	if check {
		return verify(targets)
	}
	for _, item := range targets {
		if err := write(item); err != nil {
			return err
		}
	}
	return nil
}

// plan computes every file's desired content without touching the disk, so the
// same code path decides what to write and what to check.
func plan(root string) ([]target, error) {
	reference, err := docs.RenderCommandsReference()
	if err != nil {
		return nil, err
	}
	starlark, err := docs.RenderStarlarkReference()
	if err != nil {
		return nil, err
	}

	skillPath := filepath.Join(root, "skills", "shed", "SKILL.md")
	skill, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, err
	}
	// Only the marked regions are replaced; the surrounding prose is
	// hand-written and is left exactly as it is found.
	injected, err := docs.InjectBlocks(string(skill), map[string]string{
		"commands": docs.RenderCommandTable(),
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", skillPath, err)
	}

	// The hand-written references join the generated ones in the consolidated
	// file, so an agent handed llms-full.txt has the entire documentation set.
	schema, err := os.ReadFile(filepath.Join(root, "skills", "shed", "references", "schema.md"))
	if err != nil {
		return nil, err
	}
	errorCodes, err := os.ReadFile(filepath.Join(root, "skills", "shed", "references", "errors.md"))
	if err != nil {
		return nil, err
	}
	agentContext := docs.RenderAgentContext(docs.AgentContext{
		Skill:    injected,
		Starlark: starlark,
		Schema:   string(schema),
		Commands: reference,
		Errors:   string(errorCodes),
	})

	return []target{
		{filepath.Join(root, "skills", "shed", "references", "commands.md"), reference},
		{filepath.Join(root, "skills", "shed", "references", "starlark.md"), starlark},
		{skillPath, injected},
		{filepath.Join(root, "llms-full.txt"), agentContext},
	}, nil
}

// verify compares the disk against the plan. It deliberately does not consult
// git: a newly generated file is untracked and a hand-edited neighbour is
// dirty, and neither fact says anything about whether generated content is
// current.
func verify(targets []target) error {
	var stale []string
	for _, item := range targets {
		existing, err := os.ReadFile(item.path)
		if err != nil {
			if os.IsNotExist(err) {
				stale = append(stale, item.path+" (missing)")
				continue
			}
			return err
		}
		if string(existing) != item.content {
			stale = append(stale, item.path)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	message := "the generated skill is out of date; run task generate and commit:"
	for _, path := range stale {
		message += "\n  " + path
	}
	return fmt.Errorf("%s", message)
}

// repositoryRoot walks up from the working directory to the module root, so the
// generator works from anywhere rather than only from the repository root.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod found above the working directory")
		}
		dir = parent
	}
}

func write(item target) error {
	if err := os.MkdirAll(filepath.Dir(item.path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(item.path, []byte(item.content), 0o644); err != nil {
		return err
	}
	fmt.Println("wrote", item.path)
	return nil
}
