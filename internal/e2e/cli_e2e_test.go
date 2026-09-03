package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCLIBinarySpeaksThePublishedContract(t *testing.T) {
	home := isolatedHome(t)
	dir := t.TempDir()

	welcome := runShed(t, home, dir, 0)
	requireShedSuccess(t, welcome)
	if !strings.Contains(welcome.stdout, "shed init") || !strings.Contains(welcome.stdout, "Nothing was inspected") {
		t.Fatalf("welcome stdout = %q", welcome.stdout)
	}

	version := runShed(t, home, dir, 0, "version")
	requireShedSuccess(t, version, "version")
	if strings.TrimSpace(version.stdout) != cliE2EVersion {
		t.Fatalf("version = %q, want %q", version.stdout, cliE2EVersion)
	}

	help := runShed(t, home, dir, 0, "help", "--output", "json")
	requireShedSuccess(t, help, "help", "--output", "json")
	var contract struct {
		Type     string `json:"type"`
		Version  string `json:"version"`
		Commands []struct {
			Name string `json:"name"`
		} `json:"commands"`
	}
	decodeJSON(t, help.stdout, &contract)
	if contract.Type != "help" || contract.Version != cliE2EVersion {
		t.Fatalf("help contract = %#v", contract)
	}
	var names []string
	for _, command := range contract.Commands {
		names = append(names, command.Name)
	}
	for _, want := range []string{"deploy", "init", "check", "schema", "login"} {
		if !slices.Contains(names, want) {
			t.Fatalf("help commands = %v, missing %q", names, want)
		}
	}

	schema := runShed(t, home, dir, 0, "schema", "--output", "json")
	requireShedSuccess(t, schema, "schema", "--output", "json")
	var api struct {
		Builtins []any `json:"builtins"`
	}
	decodeJSON(t, schema.stdout, &api)
	if len(api.Builtins) != 3 {
		t.Fatalf("schema builtins = %#v", api.Builtins)
	}
}

func TestCLIAuthoringAndPackageLoop(t *testing.T) {
	home := isolatedHome(t)
	root := copyFixture(t, "docker-node")

	init := runShed(t, home, root, 0, "init", "--output", "json")
	requireShedSuccess(t, init, "init", "--output", "json")
	var created struct {
		Outcome string `json:"outcome"`
		Path    string `json:"path"`
	}
	decodeJSON(t, init.stdout, &created)
	if created.Outcome != "created" {
		t.Fatalf("init = %#v\nstderr:\n%s", created, init.stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "SHED.hcl")); err != nil {
		t.Fatal(err)
	}

	check := runShed(t, home, root, 0, "check", "--output", "json")
	requireShedSuccess(t, check, "check", "--output", "json")
	var validity struct {
		Outcome       string `json:"outcome"`
		NextOperation string `json:"nextOperation"`
	}
	decodeJSON(t, check.stdout, &validity)
	if validity.Outcome != "valid" || validity.NextOperation != "deploy" {
		t.Fatalf("check = %#v\nstderr:\n%s", validity, check.stderr)
	}

	archive := filepath.Join(t.TempDir(), "application.tar.gz")
	dryRun := runShed(t, home, root, 0, "deploy", ".", "--dry-run", "--archive", archive, "--output", "json")
	requireShedSuccess(t, dryRun, "deploy", ".", "--dry-run")
	var prepared struct {
		Outcome string `json:"outcome"`
		Source  struct {
			FileCount   int    `json:"fileCount"`
			ArchivePath string `json:"archivePath"`
		} `json:"source"`
	}
	decodeJSON(t, dryRun.stdout, &prepared)
	if prepared.Outcome != "prepared" || prepared.Source.FileCount < 2 || prepared.Source.ArchivePath != archive {
		t.Fatalf("dry-run = %#v\nstderr:\n%s", prepared, dryRun.stderr)
	}
	if names := archiveEntries(t, archive); !slices.Contains(names, "SHED.hcl") || !slices.Contains(names, "package.json") {
		t.Fatalf("archive entries = %#v", names)
	}

	mocked := runShed(t, home, root, 0, "deploy", ".", "--mock", "--output", "json")
	requireShedSuccess(t, mocked, "deploy", ".", "--mock")
	var receipt struct {
		Outcome    string `json:"outcome"`
		Deployment *struct {
			ID string `json:"id"`
		} `json:"deployment"`
	}
	decodeJSON(t, mocked.stdout, &receipt)
	if receipt.Outcome != "uploaded_mock" || receipt.Deployment == nil || receipt.Deployment.ID == "" {
		t.Fatalf("mock deploy = %#v\nstderr:\n%s", receipt, mocked.stderr)
	}
}

func TestInstallScriptsPrintUsage(t *testing.T) {
	root, err := callerRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"install.sh", "install-cli.sh", "install-skills.sh", "install-all.sh"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("sh", filepath.Join(root, name), "--help")
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s --help: %v\n%s", name, err, output)
			}
			text := string(output)
			if !strings.Contains(text, "Install the shed") {
				t.Fatalf("%s --help = %q", name, text)
			}
		})
	}

	skillsHelp, err := exec.Command("sh", filepath.Join(root, "install-skills.sh"), "--help").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--global", "--local", "This project"} {
		if !strings.Contains(string(skillsHelp), want) {
			t.Fatalf("install-skills.sh --help missing %q:\n%s", want, skillsHelp)
		}
	}
}

func TestInstallSkillsLocalCopiesIntoTheProject(t *testing.T) {
	root, err := callerRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(root, "install-skills.sh"), "--local", "--dir", project)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install-skills.sh --local: %v\n%s", err, output)
	}
	for _, rel := range []string{
		filepath.Join(".claude", "skills", "shed", "SKILL.md"),
		filepath.Join(".cursor", "skills", "shed", "SKILL.md"),
	} {
		if _, statErr := os.Stat(filepath.Join(project, rel)); statErr != nil {
			t.Fatalf("missing %s: %v\n%s", rel, statErr, output)
		}
	}
	if _, statErr := os.Stat(filepath.Join(project, ".codex", "skills", "shed")); !os.IsNotExist(statErr) {
		t.Fatalf("copied into .codex without a .codex directory: %v", statErr)
	}
}

func TestNpmSkillsLocalCopiesIntoTheProject(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to run the skills npm installer")
	}
	root, err := callerRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".cursor"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", filepath.Join(root, "packages", "skills", "install.js"), "--local", "--dir", project)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npx skills --local: %v\n%s", err, output)
	}
	for _, rel := range []string{
		filepath.Join(".claude", "skills", "shed", "SKILL.md"),
		filepath.Join(".cursor", "skills", "shed", "SKILL.md"),
	} {
		if _, statErr := os.Stat(filepath.Join(project, rel)); statErr != nil {
			t.Fatalf("missing %s: %v\n%s", rel, statErr, output)
		}
	}
}

func TestInstallSkillsDirRequiresLocal(t *testing.T) {
	root, err := callerRepositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", filepath.Join(root, "install-skills.sh"), "--dir", t.TempDir())
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected failure, got:\n%s", output)
	}
	if !strings.Contains(string(output), "--dir is only valid with --local") {
		t.Fatalf("stderr = %q", output)
	}
}
