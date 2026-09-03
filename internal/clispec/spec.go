// Package clispec is the single declarative description of Shed's command line.
//
// One registry backs four consumers: flag parsing, the help screen, the
// machine-readable contract behind `shed help --output json`, and the generated
// reference documentation under skills/. Anything a user can type is described
// here exactly once, so a flag cannot exist in the parser but be missing from
// help, and documentation cannot describe a command the binary does not have.
//
// The package is pure data plus small derivations. It imports nothing from the
// rest of Shed so that the documentation generator can read the command surface
// without linking the CLI.
package clispec

import "strings"

// DefinitionFileName mirrors definition.ManifestFileName. It is duplicated here
// to keep this package a leaf, and TestDefinitionFileNameMatchesManifest pins
// the two together.
const DefinitionFileName = "SHED.hcl"

// Kind is a flag's value type. It doubles as the type name reported in JSON.
type Kind string

const (
	KindBool     Kind = "bool"
	KindString   Kind = "string"
	KindDuration Kind = "duration"
)

// Flag describes one option. Summary is the only prose written for it: the flag
// package, the help screen, and the generated docs all read this same string.
type Flag struct {
	Name string
	Kind Kind
	// Default is the value as typed, empty when the zero value applies.
	Default string
	Summary string
	// Values enumerates the accepted values. When set, Bind rejects anything
	// else and help renders them in place of a placeholder.
	Values []string
	// Placeholder names the argument in help, e.g. "<path>". Ignored when
	// Values is set, and defaulted from Kind when empty.
	Placeholder string
	// Positive requires a duration greater than zero.
	Positive bool
}

// TakesValue reports whether the option consumes the following argument. Arg
// normalization derives its option table from this, so a value-taking flag can
// never be half-registered.
func (f Flag) TakesValue() bool { return f.Kind != KindBool }

// Argument renders the value part of the flag in help output.
func (f Flag) Argument() string {
	if !f.TakesValue() {
		return ""
	}
	if len(f.Values) > 0 {
		return strings.Join(f.Values, "|")
	}
	if f.Placeholder != "" {
		return f.Placeholder
	}
	if f.Kind == KindDuration {
		return "<duration>"
	}
	return "<value>"
}

// Signature is the flag as help prints it, e.g. "--output human|json|ndjson".
func (f Flag) Signature() string {
	if argument := f.Argument(); argument != "" {
		return "--" + f.Name + " " + argument
	}
	return "--" + f.Name
}

// Example is one invocation shown under a command.
type Example struct {
	Command string
	Comment string
}

// Command describes one subcommand.
type Command struct {
	Name string
	// Aliases are alternate spellings accepted by dispatch, e.g. "--version".
	Aliases []string
	// Usage is the positional part shown in help, e.g. "[directory]".
	Usage   string
	Summary string
	// Description says what the command does with what it is given, in a
	// short paragraph, for commands whose one-line Summary cannot carry it.
	Description string
	Flags       []Flag
	// Exclusive lists flag groups of which at most one may be active. A bool
	// counts as active when true; any other flag counts when explicitly given.
	Exclusive [][]string
	// MinArgs and MaxArgs bound the positional arguments. MaxArgs of -1 is
	// unbounded.
	MinArgs int
	MaxArgs int
	// UsageLine is the error returned when the positional count is wrong. It is
	// written out rather than derived because several commands word it with
	// their options included.
	UsageLine string
	Examples  []Example
}

// Signature is "name usage", the left column of the help listing.
func (c Command) Signature() string {
	if c.Usage == "" {
		return c.Name
	}
	return c.Name + " " + c.Usage
}

// Flag returns the named flag.
func (c Command) Flag(name string) (Flag, bool) {
	for _, candidate := range c.Flags {
		if candidate.Name == name {
			return candidate, true
		}
	}
	return Flag{}, false
}

// Group is a titled section of the help listing.
type Group struct {
	Title    string
	Commands []Command
}

// Commands returns every command, in help order.
func Commands() []Command {
	var all []Command
	for _, group := range Groups {
		all = append(all, group.Commands...)
	}
	return all
}

// Lookup resolves a command by name or alias.
func Lookup(name string) (Command, bool) {
	for _, command := range Commands() {
		if command.Name == name {
			return command, true
		}
		for _, alias := range command.Aliases {
			if alias == name {
				return command, true
			}
		}
	}
	return Command{}, false
}

// EnvVar documents one environment variable that changes Shed's behavior.
type EnvVar struct {
	Name    string
	Default string
	Summary string
}

// oxfordJoin renders a value list the way the usage errors word it: "a or b"
// for two, "a, b, or c" beyond that.
func oxfordJoin(values []string) string {
	switch len(values) {
	case 0:
		return ""
	case 1:
		return values[0]
	case 2:
		return values[0] + " or " + values[1]
	default:
		return strings.Join(values[:len(values)-1], ", ") + ", or " + values[len(values)-1]
	}
}
