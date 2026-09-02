package clispec

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

// Binding is one command's parsed arguments.
type Binding struct {
	Command   Command
	flags     *flag.FlagSet
	bools     map[string]*bool
	strings   map[string]*string
	durations map[string]*time.Duration
}

// Bind registers the command's flags, parses args, and applies every constraint
// the spec declares. Validation happens here rather than inside handlers so that
// a rejected flag can never reach the work: `shed cancel <id> --output bogus`
// used to cancel the deployment and only then complain about the flag.
func Bind(command Command, args []string, errOutput io.Writer) (*Binding, error) {
	binding := &Binding{
		Command:   command,
		flags:     flag.NewFlagSet(command.Name, flag.ContinueOnError),
		bools:     map[string]*bool{},
		strings:   map[string]*string{},
		durations: map[string]*time.Duration{},
	}
	binding.flags.SetOutput(errOutput)

	for _, declared := range command.Flags {
		switch declared.Kind {
		case KindBool:
			binding.bools[declared.Name] = binding.flags.Bool(declared.Name, false, declared.Summary)
		case KindString:
			binding.strings[declared.Name] = binding.flags.String(declared.Name, declared.Default, declared.Summary)
		case KindDuration:
			binding.durations[declared.Name] = binding.flags.Duration(declared.Name, mustParseDuration(command, declared), declared.Summary)
		default:
			return nil, fmt.Errorf("clispec: command %q flag %q has unknown kind %q", command.Name, declared.Name, declared.Kind)
		}
	}

	if err := binding.flags.Parse(normalize(args, command.valueOptions())); err != nil {
		return nil, err
	}
	if err := binding.validate(); err != nil {
		return nil, err
	}
	return binding, nil
}

func (b *Binding) validate() error {
	count := b.flags.NArg()
	if count < b.Command.MinArgs || (b.Command.MaxArgs >= 0 && count > b.Command.MaxArgs) {
		return errors.New(b.Command.UsageLine)
	}
	for _, declared := range b.Command.Flags {
		if len(declared.Values) > 0 && !contains(declared.Values, b.String(declared.Name)) {
			return fmt.Errorf("--%s must be %s", declared.Name, oxfordJoin(declared.Values))
		}
	}
	for _, group := range b.Command.Exclusive {
		var active []string
		for _, name := range group {
			if b.active(name) {
				active = append(active, "--"+name)
			}
		}
		if len(active) > 1 {
			return fmt.Errorf("%s are mutually exclusive", strings.Join(active, " and "))
		}
	}
	for _, declared := range b.Command.Flags {
		if declared.Positive && b.Duration(declared.Name) <= 0 {
			return fmt.Errorf("--%s must be greater than zero", declared.Name)
		}
	}
	return nil
}

// active reports whether a flag participates in an exclusivity group. A bool
// counts when set true; anything else counts when explicitly provided, which is
// what makes `--detach --wait-timeout 5m` a conflict while a defaulted
// --wait-timeout is not.
func (b *Binding) active(name string) bool {
	declared, ok := b.Command.Flag(name)
	if !ok {
		return false
	}
	if declared.Kind == KindBool {
		return b.Bool(name)
	}
	return b.Provided(name)
}

// Bool returns a boolean flag's value.
func (b *Binding) Bool(name string) bool {
	value, ok := b.bools[name]
	if !ok {
		panic("clispec: " + b.Command.Name + " has no bool flag " + name)
	}
	return *value
}

// String returns a string flag's value.
func (b *Binding) String(name string) string {
	value, ok := b.strings[name]
	if !ok {
		panic("clispec: " + b.Command.Name + " has no string flag " + name)
	}
	return *value
}

// Duration returns a duration flag's value.
func (b *Binding) Duration(name string) time.Duration {
	value, ok := b.durations[name]
	if !ok {
		panic("clispec: " + b.Command.Name + " has no duration flag " + name)
	}
	return *value
}

// Provided reports whether the flag was named on the command line, as opposed
// to carrying its default. Wait behavior depends on the difference.
func (b *Binding) Provided(name string) bool {
	provided := false
	b.flags.Visit(func(current *flag.Flag) {
		if current.Name == name {
			provided = true
		}
	})
	return provided
}

// Args returns the positional arguments.
func (b *Binding) Args() []string { return b.flags.Args() }

// Arg returns one positional argument.
func (b *Binding) Arg(index int) string { return b.flags.Arg(index) }

// NArg returns the positional argument count.
func (b *Binding) NArg() int { return b.flags.NArg() }

// valueOptions lists every spelling that consumes the following argument,
// derived from the flag kinds so the table cannot fall behind the flags.
func (c Command) valueOptions() map[string]bool {
	options := map[string]bool{}
	for _, declared := range c.Flags {
		if declared.TakesValue() {
			options["--"+declared.Name] = true
			options["-"+declared.Name] = true
		}
	}
	return options
}

// normalize moves options ahead of positional arguments, because the flag
// package stops parsing at the first non-flag and Shed documents
// `shed deploy . --dry-run`.
func normalize(args []string, valueOptions map[string]bool) []string {
	var options []string
	var positional []string

	for index := 0; index < len(args); index++ {
		arg := args[index]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}

		options = append(options, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		if valueOptions[arg] {
			if index+1 < len(args) {
				index++
				options = append(options, args[index])
			}
		}
	}

	return append(options, positional...)
}

func mustParseDuration(command Command, declared Flag) time.Duration {
	if declared.Default == "" {
		return 0
	}
	value, err := time.ParseDuration(declared.Default)
	if err != nil {
		panic(fmt.Sprintf("clispec: command %q flag %q has unparsable default %q", command.Name, declared.Name, declared.Default))
	}
	return value
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
