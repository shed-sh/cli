package definition

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/railwayapp/railpack/core"
	"github.com/railwayapp/railpack/core/plan"

	"shed/internal/diag"
)

type DefinitionGenerator interface {
	Generate(GenerationInput) (GeneratedDefinition, error)
}

type GenerationInput struct {
	Files []string
	Build *core.BuildResult
}

type GeneratedDefinition struct {
	Manifest Manifest `json:"manifest"`
	YAML     []byte   `json:"-"`
	// Provider is the Railpack family this definition was lowered from. It is
	// empty for a definition that was loaded from disk rather than detected,
	// because an existing file is authoritative and is never redetected.
	Provider string `json:"provider,omitempty"`
}

// RailpackGenerator lowers Railpack detection into the portable SHED contract.
// Runtime support is intentionally explicit: Railpack may detect more provider
// families than the currently installed builder catalog can execute.
type RailpackGenerator struct{}

// lowering is the provider-specific half of generation. Only the build image,
// the files that pin the dependency graph, the build commands, and any language
// defaults differ between families; the content closure, runtime command, port,
// and identity are assembled the same way for every provider.
type lowering struct {
	image        string
	dependencies []string
	commands     [][]string
	environment  map[string]string
	// remote projects the definition onto the remote builder catalog. It runs
	// after the common runtime values exist because the catalog describes the
	// running application, not just its build. Families the trusted catalog
	// cannot build leave it nil and stay local-only.
	remote func(stage, runCommand []string, environment map[string]string, port int) (builderRecipe, error)
}

func (RailpackGenerator) Generate(input GenerationInput) (GeneratedDefinition, error) {
	if input.Build == nil || !input.Build.Success || input.Build.Plan == nil {
		return GeneratedDefinition{}, errors.New("a successful Railpack inspection is required")
	}
	provider := ""
	if providers := input.Build.DetectedProviders; len(providers) > 0 {
		provider = providers[0]
	}
	var (
		lowered lowering
		err     error
	)
	switch provider {
	case "node":
		lowered, err = lowerNode(input)
	case "golang":
		lowered, err = lowerGo(input)
	default:
		return GeneratedDefinition{}, unsupportedProject(ProviderLabel(provider))
	}
	if err != nil {
		return GeneratedDefinition{}, err
	}

	stage := stagePaths(input.Files, lowered.dependencies)
	include := append(append([]string{}, lowered.dependencies...), stage...)
	sort.Strings(include)

	runCommand, err := splitCommand(strings.TrimSuffix(strings.TrimSpace(input.Build.Plan.Deploy.StartCmd), " 2>&1"))
	if err != nil || len(runCommand) == 0 {
		return GeneratedDefinition{}, fmt.Errorf("railpack runtime command %q is not a safe argv command", input.Build.Plan.Deploy.StartCmd)
	}
	port := 8080
	if rawPort := input.Build.Plan.Deploy.Variables["PORT"]; rawPort != "" {
		if parsed, parseErr := strconv.Atoi(rawPort); parseErr == nil && parsed > 0 && parsed <= 65535 {
			port = parsed
		}
	}
	environment := cloneStrings(input.Build.Plan.Deploy.Variables)
	if environment == nil {
		environment = make(map[string]string)
	}
	environment["PORT"] = strconv.Itoa(port)
	for key, value := range lowered.environment {
		if _, exists := environment[key]; !exists {
			environment[key] = value
		}
	}

	var recipe builderRecipe
	if lowered.remote != nil {
		if recipe, err = lowered.remote(stage, runCommand, environment, port); err != nil {
			return GeneratedDefinition{}, err
		}
	}
	manifest := Manifest{
		APIVersion: ManifestAPIVersion,
		Kind:       ManifestKind,
		Content:    ManifestContent{Include: include},
		Build:      ManifestBuild{Image: lowered.image, Commands: lowered.commands},
		Run: ManifestRun{
			Command:          runCommand,
			WorkingDirectory: "/app",
			User:             "1000:1000",
			Environment:      environment,
			Port:             port,
			StopSignal:       "SIGTERM",
		},
		Base:  recipe.base,
		Parts: recipe.parts,
		Apps:  recipe.apps,
	}
	yamlData, err := manifest.Marshal()
	if err != nil {
		return GeneratedDefinition{}, err
	}
	return GeneratedDefinition{Manifest: manifest, YAML: yamlData, Provider: provider}, nil
}

func lowerNode(input GenerationInput) (lowering, error) {
	manager := normalizeManager(input.Build.Metadata["nodePackageManager"])
	if manager == "" {
		return lowering{}, unsupportedProject("Node.js without a recognized package manager")
	}
	if manager == "bun" {
		return lowering{}, unsupportedProject("Node.js with Bun")
	}
	if input.Build.Metadata["nodeIsSPA"] == "true" {
		return lowering{}, unsupportedProject("a static single-page application")
	}
	dependencies, err := dependencyInputs(manager, input.Files)
	if err != nil {
		return lowering{}, err
	}
	commands := installCommands(manager)
	if command := railpackBuildCommand(input.Build.Plan); len(command) > 0 {
		commands = append(commands, command)
	}
	nodeRuntime := input.Build.Metadata["nodeRuntime"]
	return lowering{
		image:        "node:" + nodeMajor(input.Build),
		dependencies: dependencies,
		commands:     commands,
		environment:  map[string]string{"NODE_ENV": "production"},
		remote: func(stage, runCommand []string, environment map[string]string, port int) (builderRecipe, error) {
			return remoteBuilderRecipe(manager, nodeRuntime, dependencies, stage, runCommand, environment, port)
		},
	}, nil
}

// lowerGo compiles and runs out of the same image. Railpack builds a single
// self-contained binary and starts it directly, so the toolchain image already
// contains everything the runtime needs and no second stage is required.
func lowerGo(input GenerationInput) (lowering, error) {
	present := fileSet(input.Files)
	if !present["go.mod"] {
		if present["go.work"] {
			return lowering{}, unsupportedProject("a Go workspace without a root go.mod")
		}
		return lowering{}, &diag.Error{
			Code:    "missing_go_mod",
			Summary: "Shed detected a Go project but found no go.mod.",
			Facts:   []diag.Fact{{Label: "Expected", Value: "go.mod in the directory being deployed"}},
			Hints: []string{
				"Create the module, then commit it: go mod init <module-path>",
				"Or run Shed from the project root: shed deploy <directory>",
			},
		}
	}
	var dependencies []string
	for _, candidate := range []string{"go.mod", "go.sum", "go.work", "go.work.sum"} {
		if present[candidate] {
			dependencies = append(dependencies, candidate)
		}
	}
	// Unlike Node, a Go plan has no start script to fall back on: the runtime
	// command names a binary that only the build step produces.
	build := railpackBuildCommand(input.Build.Plan)
	if len(build) == 0 {
		return lowering{}, &diag.Error{
			Code:    "no_build_command",
			Summary: "Shed detected Go but found nothing it knows how to build.",
			Facts:   []diag.Fact{{Label: "Start command", Value: "./out, a binary only the build step can produce"}},
			Hints: []string{
				"Add main.go at the project root, or cmd/<name>/main.go",
				"Or name the command to build: GO_BIN=<name>",
			},
		}
	}
	return lowering{
		image:        "golang:" + goVersion(input.Build),
		dependencies: dependencies,
		commands:     [][]string{{"go", "mod", "download"}, build},
	}, nil
}

type builderRecipe struct {
	base  string
	parts map[string]ManifestPart
	apps  map[string]ManifestApp
}

func remoteBuilderRecipe(
	manager, nodeRuntime string,
	dependencyInputs, stage, runCommand []string,
	environment map[string]string,
	port int,
) (builderRecipe, error) {
	// The current trusted catalog intentionally exposes only the pinned Node
	// 24 + pnpm toolchain. Other Railpack outputs remain valid for local use
	// and simply omit the remote projection.
	if manager != "pnpm" {
		return builderRecipe{}, nil
	}
	if len(runCommand) == 0 {
		return builderRecipe{}, errors.New("remote builder run command is required")
	}
	// The generic Node runtime image deliberately excludes package-manager
	// binaries and package.json. A direct Procfile command is therefore required
	// until the builder grows a package-script launch contract. Next.js has its
	// own standalone server command below.
	if nodeRuntime != "next" && isPackageManagerCommand(runCommand) {
		return builderRecipe{}, nil
	}
	plugin := "node"
	prime := append([]string(nil), stage...)
	appCommand := append([]string(nil), runCommand...)
	appEnvironment := cloneStrings(environment)
	if nodeRuntime == "next" {
		plugin = "nextjs"
		prime = existingPaths(stage, "public")
		appCommand = []string{"node", "server.js"}
		appEnvironment["HOSTNAME"] = "0.0.0.0"
	}
	return builderRecipe{
		base: "node-24",
		parts: map[string]ManifestPart{
			"app": {
				Plugin: plugin,
				Source: ".",
				Dependencies: ManifestDependencies{
					Manager: manager,
					Inputs:  append([]string(nil), dependencyInputs...),
				},
				Stage: append([]string(nil), stage...),
				Prime: prime,
			},
		},
		apps: map[string]ManifestApp{
			"web": {
				Command:          []string{appCommand[0]},
				Args:             append([]string(nil), appCommand[1:]...),
				WorkingDirectory: "/app",
				User:             "1000:1000",
				Environment:      appEnvironment,
				Ports:            []string{strconv.Itoa(port) + "/tcp"},
				StopSignal:       "SIGTERM",
			},
		},
	}, nil
}

func isPackageManagerCommand(command []string) bool {
	if len(command) == 0 {
		return false
	}
	return command[0] == "pnpm" || command[0] == "npm" || command[0] == "yarn"
}

func existingPaths(values []string, candidates ...string) []string {
	wanted := make(map[string]bool, len(candidates))
	for _, candidate := range candidates {
		wanted[candidate] = true
	}
	var result []string
	for _, value := range values {
		if wanted[value] {
			result = append(result, value)
		}
	}
	return result
}

func normalizeManager(value string) string {
	switch {
	case value == "npm":
		return "npm"
	case strings.HasPrefix(value, "yarn"):
		return "yarn"
	case strings.HasPrefix(value, "pnpm"):
		return "pnpm"
	case strings.HasPrefix(value, "bun"):
		return "bun"
	default:
		return ""
	}
}

// SupportedProjects describes the slice of Railpack detection that the builder
// catalog can currently execute. Shed states it verbatim wherever it turns a
// project down, so the boundary is never left to guesswork.
const SupportedProjects = "Node.js and Next.js (npm, pnpm, or yarn), and Go modules"

// HandWrittenHint points at the escape hatch. Anything detection cannot lower
// automatically can still be described by hand and built like any other project.
// The reference is a URL, not a repository path, because most people reach this
// message from an installed binary with no checkout to look in.
const HandWrittenHint = "Describe the project yourself in " + ManifestFileName

// DefinitionDocsHint names where the format is written down.
const DefinitionDocsHint = ManifestFileName + " reference: https://github.com/shed-sh/shed/blob/main/docs/cli-and-application-definition.md"

// unsupportedProject names the detection in the summary, not only in the facts,
// so the single line that reaches --output json still says what was found.
func unsupportedProject(detected string) error {
	return &diag.Error{
		Code:    "unsupported_project",
		Summary: fmt.Sprintf("Detected %s, which Shed cannot build automatically yet.", detected),
		Facts:   []diag.Fact{{Label: "Supported", Value: SupportedProjects}},
		Hints: []string{
			HandWrittenHint,
			DefinitionDocsHint,
			"Then build it the same way: shed deploy",
		},
	}
}

// ProviderLabel names a Railpack provider the way its users would. The raw
// provider string is kept alongside the friendly name because it is the term
// Railpack's own documentation uses.
func ProviderLabel(provider string) string {
	if provider == "" {
		return "an unrecognized application"
	}
	names := map[string]string{
		"deno":       "Deno",
		"golang":     "Go",
		"node":       "Node.js",
		"elixir":     "Elixir",
		"java":       "Java",
		"php":        "PHP",
		"python":     "Python",
		"ruby":       "Ruby",
		"rust":       "Rust",
		"shell":      "a shell application",
		"staticfile": "a static site",
	}
	if name, found := names[provider]; found {
		return fmt.Sprintf("%s (Railpack provider %q)", name, provider)
	}
	return fmt.Sprintf("Railpack provider %q", provider)
}

func fileSet(files []string) map[string]bool {
	present := make(map[string]bool, len(files))
	for _, filename := range files {
		present[filename] = true
	}
	return present
}

func dependencyInputs(manager string, files []string) ([]string, error) {
	present := fileSet(files)
	if !present["package.json"] {
		return nil, &diag.Error{
			Code:    "missing_package_json",
			Summary: "Shed detected a Node.js project but found no package.json.",
			Facts:   []diag.Fact{{Label: "Expected", Value: "package.json in the directory being deployed"}},
			Hints:   []string{"Run Shed from the project root, or pass it: shed deploy <directory>"},
		}
	}
	candidates := map[string][]string{
		"npm":  {"package-lock.json", "npm-shrinkwrap.json"},
		"yarn": {"yarn.lock"},
		"pnpm": {"pnpm-lock.yaml"},
		"bun":  {"bun.lock", "bun.lockb"},
	}[manager]
	for _, lockfile := range candidates {
		if present[lockfile] {
			return []string{"package.json", lockfile}, nil
		}
	}
	return nil, &diag.Error{
		Code:    "missing_lockfile",
		Summary: "Shed needs a lockfile to build this project reproducibly.",
		Facts: []diag.Fact{
			{Label: "Package manager", Value: manager},
			{Label: "Expected", Value: strings.Join(candidates, " or ")},
		},
		Hints: []string{fmt.Sprintf("Create the lockfile, then commit it: %s install", manager)},
	}
}

func stagePaths(files, dependencyInputs []string) []string {
	excluded := make(map[string]bool, len(dependencyInputs))
	for _, filename := range dependencyInputs {
		excluded[filename] = true
	}
	topLevel := make(map[string]bool)
	for _, filename := range files {
		first := strings.SplitN(filename, "/", 2)[0]
		if excluded[first] || first == ManifestFileName || first == ".gitignore" || first == ".shedignore" {
			continue
		}
		topLevel[first] = true
	}
	result := make([]string, 0, len(topLevel))
	for filename := range topLevel {
		result = append(result, filename)
	}
	sort.Strings(result)
	return result
}

func installCommands(manager string) [][]string {
	switch manager {
	case "pnpm":
		return [][]string{{"corepack", "enable"}, {"pnpm", "install", "--frozen-lockfile"}}
	case "yarn":
		return [][]string{{"corepack", "enable"}, {"yarn", "install", "--frozen-lockfile"}}
	case "bun":
		return [][]string{{"bun", "install", "--frozen-lockfile"}}
	default:
		return [][]string{{"npm", "ci"}}
	}
}

func railpackBuildCommand(buildPlan *plan.BuildPlan) []string {
	for _, step := range buildPlan.Steps {
		for _, command := range step.Commands {
			execCommand, ok := command.(plan.ExecCommand)
			if !ok {
				continue
			}
			words, err := splitCommand(execCommand.Cmd)
			joined := strings.ToLower(strings.Join(words, " "))
			if err == nil && strings.Contains(joined, "build") && !strings.Contains(joined, "install") {
				return words
			}
		}
	}
	return nil
}

func nodeMajor(result *core.BuildResult) string {
	if pkg := result.ResolvedPackages["node"]; pkg != nil {
		for _, value := range []*string{pkg.ResolvedVersion, pkg.RequestedVersion} {
			if value != nil {
				major := strings.SplitN(strings.TrimPrefix(*value, "v"), ".", 2)[0]
				if _, err := strconv.Atoi(major); err == nil {
					return major
				}
			}
		}
	}
	return "22"
}

// goVersion picks the golang image tag. Railpack reports whatever go.mod
// declares, which is often a patch release such as 1.26.3; Docker Hub publishes
// a tag for every minor line but not for every patch, so the tag is narrowed to
// major.minor.
func goVersion(result *core.BuildResult) string {
	if pkg := result.ResolvedPackages["go"]; pkg != nil {
		for _, value := range []*string{pkg.ResolvedVersion, pkg.RequestedVersion} {
			if value == nil {
				continue
			}
			if version := majorMinor(*value); version != "" {
				return version
			}
		}
	}
	return "1.25"
}

func majorMinor(value string) string {
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(value), "v"), ".", 3)
	if len(parts) < 2 {
		return ""
	}
	for _, part := range parts[:2] {
		if _, err := strconv.Atoi(part); err != nil {
			return ""
		}
	}
	return parts[0] + "." + parts[1]
}

func splitCommand(value string) ([]string, error) {
	var result []string
	var current strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			result = append(result, current.String())
			current.Reset()
		}
	}
	for _, char := range value {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		if char == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if char == quote {
				quote = 0
			} else {
				current.WriteRune(char)
			}
			continue
		}
		switch char {
		case '\'', '"':
			quote = char
		case ' ', '\t', '\n':
			flush()
		case ';', '|', '&', '<', '>', '$', '`':
			return nil, errors.New("shell operators are not supported")
		default:
			current.WriteRune(char)
		}
	}
	if quote != 0 || escaped {
		return nil, errors.New("unterminated quote or escape")
	}
	flush()
	return result, nil
}

func cloneStrings(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
