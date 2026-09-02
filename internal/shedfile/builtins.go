package shedfile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"shed/internal/definition"
	"shed/internal/diag"
)

// evaluator accumulates everything one evaluation pass learns: the values the
// program created, the single registered application, and every diagnostic.
// Builtins record problems and return typed placeholders instead of failing,
// so one `shed check` reports the whole file rather than the first mistake.
type evaluator struct {
	universe []string
	diags    []*diag.Error
	builds   []*Build
	app      *appCall
}

// Build is the value a build() call returns. It is opaque on the Starlark
// side: the program can only pass it along to http_app.
type Build struct {
	pos      syntax.Position
	srcs     []string
	image    string
	commands [][]string
	used     bool
	// placeholder marks a Build created after its call already reported
	// diagnostics. It keeps evaluation going without ever being lowered.
	placeholder bool
}

func (b *Build) String() string        { return "build(...)" }
func (b *Build) Type() string          { return "build" }
func (b *Build) Freeze()               {}
func (b *Build) Truth() starlark.Bool  { return starlark.True }
func (b *Build) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: build") }

// Srcs is the value a glob() call returns: the matched files, resolved
// against the collector universe at the call site.
type Srcs struct {
	pos   syntax.Position
	files []string
}

func (s *Srcs) String() string        { return fmt.Sprintf("glob(%d files)", len(s.files)) }
func (s *Srcs) Type() string          { return "glob" }
func (s *Srcs) Freeze()               {}
func (s *Srcs) Truth() starlark.Bool  { return starlark.Bool(len(s.files) > 0) }
func (s *Srcs) Hash() (uint32, error) { return 0, fmt.Errorf("unhashable type: glob") }

// appCall is one http_app() invocation with every default already applied.
type appCall struct {
	pos              syntax.Position
	name             string
	build            *Build
	cmd              []string
	port             int
	env              map[string]string
	workingDirectory string
	user             string
	stopSignal       string
}

// report records one diagnostic. The position lands in the summary as
// SHED:line:col, the shape agents and editors already parse.
func (ev *evaluator) report(pos syntax.Position, code, summary string, hints ...string) {
	if pos.IsValid() {
		summary = pos.String() + ": " + summary
	}
	ev.diags = append(ev.diags, &diag.Error{Code: code, Summary: summary, Hints: hints})
}

func acceptedArguments(spec builtinSpec) string {
	names := make([]string, len(spec.args))
	for index, arg := range spec.args {
		names[index] = arg.name
	}
	return spec.name + "() accepts: " + strings.Join(names, ", ")
}

// builtin wraps one spec-checked implementation. It validates the call shape
// entirely from the spec table — positional use, unknown names, types,
// missing required arguments — and only invokes impl once the call is clean.
// A dirty call returns placeholder() so evaluation continues.
func (ev *evaluator) builtin(name string, impl func(pos syntax.Position, values map[string]any) starlark.Value, placeholder func(pos syntax.Position) starlark.Value) *starlark.Builtin {
	spec := specFor(name)
	return starlark.NewBuiltin(name, func(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
		pos := thread.CallFrame(1).Pos
		clean := true
		values := make(map[string]any)
		// attempted also covers arguments whose conversion failed, so one
		// mistake reports one diagnostic instead of a mismatch plus a
		// missing-argument echo.
		attempted := make(map[string]bool)

		positionalName := ""
		if len(spec.args) > 0 && spec.args[0].positional {
			positionalName = spec.args[0].name
		}
		switch {
		case len(args) == 0:
		case positionalName != "" && len(args) == 1:
			attempted[positionalName] = true
			if value, ok := ev.convert(pos, spec, spec.args[0], args[0]); ok {
				values[positionalName] = value
			} else {
				clean = false
			}
		default:
			hint := "Name every argument: " + callShape(spec)
			ev.report(pos, "positional_argument", fmt.Sprintf("%s() takes keyword arguments only.", spec.name), hint)
			clean = false
		}

		for _, kwarg := range kwargs {
			argName := string(kwarg[0].(starlark.String))
			arg, known := spec.arg(argName)
			if !known {
				hints := []string{}
				if closest := nearestArgument(spec, argName); closest != "" {
					hints = append(hints, "Did you mean: "+closest)
				}
				hints = append(hints, acceptedArguments(spec))
				ev.report(pos, "unknown_argument", fmt.Sprintf("%s() got an unknown argument %q.", spec.name, argName), hints...)
				clean = false
				continue
			}
			if attempted[argName] {
				// The positional patterns argument was also passed by name.
				ev.report(pos, "unknown_argument", fmt.Sprintf("%s() got argument %q twice.", spec.name, argName))
				clean = false
				continue
			}
			attempted[argName] = true
			value, ok := ev.convert(pos, spec, arg, kwarg[1])
			if !ok {
				clean = false
				continue
			}
			values[argName] = value
		}

		for _, arg := range spec.args {
			if !arg.required {
				continue
			}
			if !attempted[arg.name] {
				ev.report(pos, "missing_argument",
					fmt.Sprintf("%s() is missing required argument %q.", spec.name, arg.name),
					acceptedArguments(spec))
				clean = false
			}
		}

		if !clean {
			return placeholder(pos), nil
		}
		return impl(pos, values), nil
	})
}

// callShape renders `build(srcs = ..., image = ..., commands = ...)` for hints.
func callShape(spec builtinSpec) string {
	parts := make([]string, len(spec.args))
	for index, arg := range spec.args {
		parts[index] = arg.name + " = ..."
	}
	return spec.name + "(" + strings.Join(parts, ", ") + ")"
}

func nearestArgument(spec builtinSpec, input string) string {
	const maxDistance = 2
	best, bestDistance := "", maxDistance+1
	for _, arg := range spec.args {
		if distance := diag.EditDistance(strings.ToLower(input), arg.name); distance < bestDistance {
			best, bestDistance = arg.name, distance
		}
	}
	if bestDistance > maxDistance {
		return ""
	}
	return best
}

// convert checks one argument against its single accepted representation and
// returns the Go value. On mismatch it reports invalid_argument_type and
// returns ok=false; it never guesses a coercion.
func (ev *evaluator) convert(pos syntax.Position, spec builtinSpec, arg argSpec, value starlark.Value) (any, bool) {
	mismatch := func(hints ...string) (any, bool) {
		ev.report(pos, "invalid_argument_type",
			fmt.Sprintf("%s() argument %q wants %s, got %s.", spec.name, arg.name, arg.kind.typeName(), value.Type()),
			hints...)
		return nil, false
	}
	switch arg.kind {
	case kindString:
		text, ok := starlark.AsString(value)
		if !ok {
			return mismatch()
		}
		return text, true
	case kindInt:
		var number int
		if err := starlark.AsInt(value, &number); err != nil {
			return mismatch()
		}
		return number, true
	case kindStringList:
		items, ok := stringList(value)
		if !ok {
			return mismatch()
		}
		return items, true
	case kindCommand:
		items, ok := stringList(value)
		if !ok || len(items) == 0 {
			return mismatch(fmt.Sprintf("Write an argv list: %s = [\"./out\"]", arg.name))
		}
		return items, true
	case kindCommands:
		list, ok := value.(*starlark.List)
		if !ok {
			return mismatch(fmt.Sprintf("Write a list of argv lists: %s = [[\"go\", \"build\", \"./...\"]]", arg.name))
		}
		commands := make([][]string, 0, list.Len())
		for index := 0; index < list.Len(); index++ {
			command, itemOK := stringList(list.Index(index))
			if !itemOK || len(command) == 0 {
				return mismatch(fmt.Sprintf("Write a list of argv lists: %s = [[\"go\", \"build\", \"./...\"]]", arg.name))
			}
			commands = append(commands, command)
		}
		return commands, true
	case kindStringDict:
		dict, ok := value.(*starlark.Dict)
		if !ok {
			return mismatch(fmt.Sprintf("Write a dict of strings: %s = {\"KEY\": \"value\"}", arg.name))
		}
		result := make(map[string]string, dict.Len())
		for _, item := range dict.Items() {
			key, keyOK := starlark.AsString(item[0])
			entry, entryOK := starlark.AsString(item[1])
			if !keyOK || !entryOK {
				return mismatch(fmt.Sprintf("Write a dict of strings: %s = {\"KEY\": \"value\"}", arg.name))
			}
			result[key] = entry
		}
		return result, true
	case kindBuild:
		build, ok := value.(*Build)
		if !ok {
			return mismatch("Assign the build first: b = build(...), then " + spec.name + "(build = b, ...)")
		}
		build.used = true
		return build, true
	case kindSrcs:
		if srcs, ok := value.(*Srcs); ok {
			return append([]string(nil), srcs.files...), true
		}
		entries, ok := stringList(value)
		if !ok {
			return mismatch(fmt.Sprintf("List the paths: %s = [\"cmd\", \"go.mod\"], or select them: %s = glob([\"cmd/**\"])", arg.name, arg.name))
		}
		resolved, resolvedOK := ev.resolveLiteralSrcs(pos, entries)
		return resolved, resolvedOK
	}
	return mismatch()
}

func stringList(value starlark.Value) ([]string, bool) {
	list, ok := value.(*starlark.List)
	if !ok {
		return nil, false
	}
	items := make([]string, 0, list.Len())
	for index := 0; index < list.Len(); index++ {
		item, itemOK := starlark.AsString(list.Index(index))
		if !itemOK {
			return nil, false
		}
		items = append(items, item)
	}
	return items, true
}

// resolveLiteralSrcs checks each literal path against the collector universe:
// an entry must name a collected file or a directory holding one. The check
// runs at the authoring position, so a typo fails here with a suggestion
// instead of surfacing later as a packaging error.
func (ev *evaluator) resolveLiteralSrcs(pos syntax.Position, entries []string) ([]string, bool) {
	ok := true
	for _, entry := range entries {
		if !ev.inUniverse(entry) {
			hints := []string{}
			if closest := ev.nearestPath(entry); closest != "" {
				hints = append(hints, "Did you mean: "+closest)
			}
			hints = append(hints, "See every file Shed collects: shed deploy --dry-run --output json")
			ev.report(pos, "unknown_src", fmt.Sprintf("srcs entry %q does not match any collected file.", entry), hints...)
			ok = false
		}
	}
	return entries, ok
}

func (ev *evaluator) inUniverse(entry string) bool {
	prefix := strings.TrimSuffix(entry, "/") + "/"
	for _, file := range ev.universe {
		if file == entry || strings.HasPrefix(file, prefix) {
			return true
		}
	}
	return false
}

// nearestPath suggests the collected path a typo most likely meant, comparing
// against files and every directory prefix in the universe.
func (ev *evaluator) nearestPath(input string) string {
	candidates := make(map[string]struct{})
	for _, file := range ev.universe {
		candidates[file] = struct{}{}
		for prefix := file; ; {
			slash := strings.LastIndex(prefix, "/")
			if slash < 0 {
				break
			}
			prefix = prefix[:slash]
			candidates[prefix] = struct{}{}
		}
	}
	const maxDistance = 2
	best, bestDistance := "", maxDistance+1
	for candidate := range candidates {
		if distance := diag.EditDistance(input, candidate); distance < bestDistance {
			best, bestDistance = candidate, distance
		} else if distance == bestDistance && (best == "" || candidate < best) {
			best = candidate
		}
	}
	if bestDistance > maxDistance {
		return ""
	}
	return best
}

func (ev *evaluator) predeclared() starlark.StringDict {
	return starlark.StringDict{
		"build":    ev.builtin("build", ev.buildImpl, ev.buildPlaceholder),
		"http_app": ev.builtin("http_app", ev.httpAppImpl, nonePlaceholder),
		"glob":     ev.builtin("glob", ev.globImpl, emptySrcsPlaceholder),
	}
}

func (ev *evaluator) buildPlaceholder(pos syntax.Position) starlark.Value {
	return &Build{pos: pos, placeholder: true}
}

func nonePlaceholder(syntax.Position) starlark.Value { return starlark.None }

func emptySrcsPlaceholder(pos syntax.Position) starlark.Value { return &Srcs{pos: pos} }

func (ev *evaluator) buildImpl(pos syntax.Position, values map[string]any) starlark.Value {
	srcs := values["srcs"].([]string)
	image := values["image"].(string)
	commands, _ := values["commands"].([][]string)

	clean := true
	if strings.TrimSpace(image) == "" || strings.ContainsAny(image, "\r\n\x00") {
		ev.report(pos, "invalid_argument_type", "build() argument \"image\" must be a non-empty single-line image reference.")
		clean = false
	}
	if len(srcs) == 0 {
		ev.report(pos, "empty_srcs", "srcs selected no files, and a build with nothing to read cannot produce anything.",
			"Select the project sources: srcs = glob([\"**\"]) matches every collected file")
		clean = false
	}
	if !clean {
		return ev.buildPlaceholder(pos)
	}
	build := &Build{pos: pos, srcs: srcs, image: image, commands: commands}
	ev.builds = append(ev.builds, build)
	return build
}

func (ev *evaluator) httpAppImpl(pos syntax.Position, values map[string]any) starlark.Value {
	if ev.app != nil {
		ev.report(pos, "duplicate_app", "a second http_app() call: multiple apps are not supported yet.",
			"Keep exactly one http_app() and delete the rest.")
		return starlark.None
	}
	app := &appCall{
		pos:              pos,
		name:             values["name"].(string),
		build:            values["build"].(*Build),
		cmd:              values["cmd"].([]string),
		port:             values["port"].(int),
		workingDirectory: "/app",
		user:             "1000:1000",
		stopSignal:       "SIGTERM",
	}
	if env, ok := values["env"].(map[string]string); ok {
		app.env = env
	}
	if workingDirectory, ok := values["working_directory"].(string); ok {
		app.workingDirectory = workingDirectory
	}
	if user, ok := values["user"].(string); ok {
		app.user = user
	}
	if stopSignal, ok := values["stop_signal"].(string); ok {
		app.stopSignal = stopSignal
	}
	if !definition.ValidProjectName(app.name) {
		ev.report(pos, "invalid_name", fmt.Sprintf("%q is not a valid application name.", app.name),
			"Use a lowercase DNS label of at most 30 characters, such as \"my-app\".")
	}
	if app.port < 1 || app.port > 65535 {
		ev.report(pos, "invalid_argument_type", "http_app() argument \"port\" must be between 1 and 65535.")
	}
	ev.app = app
	return starlark.None
}

func (ev *evaluator) globImpl(pos syntax.Position, values map[string]any) starlark.Value {
	patterns := values["patterns"].([]string)
	exclude, _ := values["exclude"].([]string)

	clean := true
	for _, pattern := range append(append([]string(nil), patterns...), exclude...) {
		if !doublestar.ValidatePattern(pattern) {
			ev.report(pos, "invalid_pattern", fmt.Sprintf("glob pattern %q is not a valid pattern.", pattern))
			clean = false
		}
	}
	if !clean {
		return &Srcs{pos: pos}
	}
	var files []string
	for _, file := range ev.universe {
		if matchesAny(patterns, file) && !matchesAny(exclude, file) {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	return &Srcs{pos: pos, files: files}
}

// matchesAny applies a pattern to the file itself and, so a bare directory
// name works the way authors expect, to everything under a directory it names.
func matchesAny(patterns []string, file string) bool {
	for _, pattern := range patterns {
		if matched, _ := doublestar.Match(pattern, file); matched {
			return true
		}
		if matched, _ := doublestar.Match(strings.TrimSuffix(pattern, "/")+"/**", file); matched {
			return true
		}
	}
	return false
}
