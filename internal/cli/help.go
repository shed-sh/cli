package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"shed/internal/clispec"
	"shed/internal/diag"
)

// nearestCommand returns the command a typo most likely meant, or an empty
// string when nothing is close enough to be worth guessing.
func nearestCommand(input string) string {
	const maxDistance = 2
	best, bestDistance := "", maxDistance+1
	for _, candidate := range clispec.Commands() {
		distance := diag.EditDistance(strings.ToLower(input), candidate.Name)
		if distance < bestDistance {
			best, bestDistance = candidate.Name, distance
		}
	}
	if bestDistance > maxDistance {
		return ""
	}
	return best
}

// pair is one aligned "signature  description" row.
type pair struct{ left, right string }

// writePairs prints rows with the description column aligned past the longest
// signature, so help stays readable as commands and flags are added.
func writePairs(w io.Writer, gap int, pairs []pair) {
	width := 0
	for _, row := range pairs {
		if length := len(row.left); length > width {
			width = length
		}
	}
	for _, row := range pairs {
		_, _ = fmt.Fprintf(w, "  %-*s%s%s\n", width, row.left, strings.Repeat(" ", gap), row.right)
	}
}

func flagPairs(command clispec.Command) []pair {
	pairs := make([]pair, 0, len(command.Flags))
	for _, declared := range command.Flags {
		pairs = append(pairs, pair{declared.Signature(), declared.Summary})
	}
	return pairs
}

// outputCommands names the commands that speak JSON, derived from the registry
// so the sentence cannot outlive the flag it describes.
func outputCommands() string {
	var names []string
	for _, command := range clispec.Commands() {
		if _, ok := command.Flag("output"); ok {
			names = append(names, command.Name)
		}
	}
	switch len(names) {
	case 0, 1:
		return strings.Join(names, "")
	default:
		return strings.Join(names[:len(names)-1], ", ") + ", and " + names[len(names)-1]
	}
}

// printWelcome is what a bare `shed` prints. It names the first useful commands
// and says plainly that nothing was inspected, so an empty response never reads
// as a CLI that silently failed.
func (a *App) printWelcome() {
	style := diag.NewStyler(a.stdout)
	_, _ = fmt.Fprintln(a.stdout, "Shed deploys and shares small software.")
	_, _ = fmt.Fprintln(a.stdout)
	_, _ = fmt.Fprintln(a.stdout, style.Strong("Start here:"))
	_, _ = fmt.Fprint(a.stdout, `  shed init      Look at this project and write `+clispec.DefinitionFileName+`
  shed deploy    Build this project and run it locally
  shed login     Sign in to deploy to the cloud
  shed help      Every command, option, and agent contract

Nothing was inspected: Shed reads a project only when you ask it to.
Agents: `+outputCommands()+`
take --output json, and no command ever prompts.
Run shed help for the full contract.
`)
}

func (a *App) printHelp() {
	style := diag.NewStyler(a.stdout)
	_, _ = fmt.Fprintln(a.stdout, "Shed deploys and shares small software.")
	_, _ = fmt.Fprintln(a.stdout)
	_, _ = fmt.Fprintln(a.stdout, style.Strong("Usage:"))
	_, _ = fmt.Fprint(a.stdout, `  shed <command> [options]
  shed <directory> [options]   Shorthand for: shed deploy <directory>
`)

	width := 0
	for _, entry := range clispec.Commands() {
		if length := len(entry.Signature()); length > width {
			width = length
		}
	}
	for _, group := range clispec.Groups {
		_, _ = fmt.Fprintf(a.stdout, "\n%s\n", style.Strong(group.Title+":"))
		for _, entry := range group.Commands {
			_, _ = fmt.Fprintf(a.stdout, "  %-*s  %s\n", width, entry.Signature(), entry.Summary)
		}
	}

	if deploy, ok := clispec.Lookup("deploy"); ok {
		_, _ = fmt.Fprintln(a.stdout)
		_, _ = fmt.Fprintln(a.stdout, style.Strong("Options for deploy:"))
		writePairs(a.stdout, 3, flagPairs(deploy))
	}

	_, _ = fmt.Fprintln(a.stdout)
	_, _ = fmt.Fprintln(a.stdout, style.Strong("Working with coding agents:"))
	_, _ = fmt.Fprint(a.stdout, `  Shed never prompts, so every command is safe to run unattended.
  --output json      Exactly one JSON object on stdout, once the command ends.
                     Taken by `+outputCommands()+`.
  --output ndjson    One typed JSON record per line as the work happens.
  --request-id <id>  Repeat a deploy without creating a second deployment.

  Every option of every command: shed help <command>, or shed help --output json
  for the whole contract at once.

  Success and failure both arrive as that one object; failure looks like
  {"type":"result","outcome":"failed","failure":{"code":...}} and exits 1.
  Branch on failure.code, which is stable. The sentence beside it is prose
  and may be reworded. With --output json nothing else touches stdout, so
  the stream always parses; human progress is written to stderr instead.

  Authoring a definition is a loop: start from shed init, make an edit,
  and validate it with shed check --output json, which reports every
  diagnostic at once. shed schema prints the SHED file API.
`)

	var examples []pair
	for _, command := range clispec.Commands() {
		for _, example := range command.Examples {
			examples = append(examples, pair{example.Command, example.Comment})
		}
	}
	if len(examples) > 0 {
		_, _ = fmt.Fprintln(a.stdout)
		_, _ = fmt.Fprintln(a.stdout, style.Strong("Examples:"))
		writePairs(a.stdout, 3, examples)
	}
}

// printCommandHelp is `shed help <command>`. The top-level screen stays short by
// showing only deploy's options; every other command's surface lives here.
func (a *App) printCommandHelp(command clispec.Command) {
	style := diag.NewStyler(a.stdout)
	_, _ = fmt.Fprintln(a.stdout, style.Strong("shed "+command.Signature()))
	_, _ = fmt.Fprintln(a.stdout)
	_, _ = fmt.Fprintln(a.stdout, "  "+command.Summary)

	if len(command.Flags) > 0 {
		_, _ = fmt.Fprintln(a.stdout)
		_, _ = fmt.Fprintln(a.stdout, style.Strong("Options:"))
		writePairs(a.stdout, 3, flagPairs(command))
	}
	for _, group := range command.Exclusive {
		_, _ = fmt.Fprintf(a.stdout, "\n  --%s cannot be combined.\n", strings.Join(group, " and --"))
	}
	if len(command.Examples) > 0 {
		_, _ = fmt.Fprintln(a.stdout)
		_, _ = fmt.Fprintln(a.stdout, style.Strong("Examples:"))
		var examples []pair
		for _, example := range command.Examples {
			examples = append(examples, pair{example.Command, example.Comment})
		}
		writePairs(a.stdout, 3, examples)
	}
}

// The help contract is a published surface: agents branch on it, so it is
// declared separately from clispec's internal field names rather than marshaled
// straight from the registry.
type helpContract struct {
	Type        string            `json:"type"`
	Version     string            `json:"version"`
	Commands    []commandContract `json:"commands"`
	Environment []envContract     `json:"environment"`
}

type commandContract struct {
	Name      string            `json:"name"`
	Group     string            `json:"group"`
	Aliases   []string          `json:"aliases,omitempty"`
	Usage     string            `json:"usage,omitempty"`
	Summary   string            `json:"summary"`
	UsageLine string            `json:"usageLine,omitempty"`
	MinArgs   int               `json:"minArgs"`
	MaxArgs   int               `json:"maxArgs"`
	Flags     []flagContract    `json:"flags,omitempty"`
	Exclusive [][]string        `json:"exclusive,omitempty"`
	Examples  []exampleContract `json:"examples,omitempty"`
}

type flagContract struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Default    string   `json:"default,omitempty"`
	Summary    string   `json:"summary"`
	Values     []string `json:"values,omitempty"`
	TakesValue bool     `json:"takesValue"`
}

type exampleContract struct {
	Command string `json:"command"`
	Comment string `json:"comment,omitempty"`
}

type envContract struct {
	Name    string `json:"name"`
	Default string `json:"default,omitempty"`
	Summary string `json:"summary"`
}

func newHelpContract(version string) helpContract {
	contract := helpContract{Type: "help", Version: version}
	for _, group := range clispec.Groups {
		for _, command := range group.Commands {
			entry := commandContract{
				Name:      command.Name,
				Group:     group.Title,
				Aliases:   command.Aliases,
				Usage:     command.Usage,
				Summary:   command.Summary,
				UsageLine: command.UsageLine,
				MinArgs:   command.MinArgs,
				MaxArgs:   command.MaxArgs,
				Exclusive: command.Exclusive,
			}
			for _, declared := range command.Flags {
				entry.Flags = append(entry.Flags, flagContract{
					Name:       declared.Name,
					Type:       string(declared.Kind),
					Default:    declared.Default,
					Summary:    declared.Summary,
					Values:     declared.Values,
					TakesValue: declared.TakesValue(),
				})
			}
			for _, example := range command.Examples {
				entry.Examples = append(entry.Examples, exampleContract{Command: example.Command, Comment: example.Comment})
			}
			contract.Commands = append(contract.Commands, entry)
		}
	}
	for _, variable := range clispec.Environment {
		contract.Environment = append(contract.Environment, envContract{Name: variable.Name, Default: variable.Default, Summary: variable.Summary})
	}
	return contract
}

func (a *App) writeHelpJSON() error {
	encoder := json.NewEncoder(a.stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(newHelpContract(a.version))
}
