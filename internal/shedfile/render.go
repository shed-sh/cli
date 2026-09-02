package shedfile

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"shed/internal/definition"
)

// header is written at the top of every generated SHED file. It states the
// whole contract in place, because the reader most likely to edit the file
// next is an agent that will never open the documentation.
const header = `# SHED — the application definition. Shed evaluates this file before every
# deploy; only the evaluated result ships, never the program.
#
# The rules:
#   1. Only build(), http_app(), and glob() exist; nothing else is defined.
#   2. Every argument is named: build(srcs = ...). Positional calls fail.
#   3. Commands are argv lists, never shell strings: cmd = ["./out"].
#   4. Assign build() to a variable and pass it on: http_app(build = b).
#   5. Exactly one http_app() call registers the application.
#   6. Not yet supported: ` + "%s" + `.
#
# Validate edits with: shed check    Print the API with: shed schema
`

// Render writes the definition as SHED source. The output stays inside the
// pure-literal subset the evaluator accepts, so rendering and evaluating are
// inverses: Evaluate(Render(m)) reproduces m exactly.
func Render(generated definition.GeneratedDefinition) ([]byte, error) {
	manifest := generated.Manifest
	if manifest.Metadata == nil || manifest.Metadata.Name == "" {
		return nil, errors.New("render " + FileName + ": the definition has no metadata.name")
	}

	var out strings.Builder
	fmt.Fprintf(&out, header, wrapList(notYetSupported, "#      "))
	if generated.Provider != "" {
		out.WriteString("\n# Detected: " + definition.ProviderLabel(generated.Provider) + "\n")
	} else {
		out.WriteString("\n")
	}
	if manifest.Base != "" || len(manifest.Parts) > 0 || len(manifest.Apps) > 0 {
		out.WriteString("# The cloud builder projection (base/parts/apps) cannot be expressed here\n")
		out.WriteString("# yet and was omitted; keep SHED.yaml instead if you deploy with --remote.\n")
	}

	out.WriteString("b = build(\n")
	out.WriteString("    srcs = " + renderStringList(manifest.Content.Include, 1) + ",\n")
	out.WriteString("    image = " + quote(manifest.Build.Image) + ",\n")
	if len(manifest.Build.Commands) > 0 {
		out.WriteString("    commands = [\n")
		for _, command := range manifest.Build.Commands {
			out.WriteString("        " + renderInlineStrings(command) + ",\n")
		}
		out.WriteString("    ],\n")
	}
	out.WriteString(")\n\nhttp_app(\n")
	out.WriteString("    name = " + quote(manifest.Metadata.Name) + ",\n")
	out.WriteString("    build = b,\n")
	out.WriteString("    cmd = " + renderInlineStrings(manifest.Run.Command) + ",\n")
	if generated.Provider != "" {
		out.WriteString("    # Assumed: detection never reports a port. Correct it if the app\n")
		out.WriteString("    # listens elsewhere.\n")
	}
	out.WriteString("    port = " + strconv.Itoa(manifest.Run.Port) + ",\n")

	environment := renderableEnvironment(manifest.Run)
	if len(environment) > 0 {
		keys := make([]string, 0, len(environment))
		for key := range environment {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) == 1 {
			out.WriteString("    env = {" + quote(keys[0]) + ": " + quote(environment[keys[0]]) + "},\n")
		} else {
			out.WriteString("    env = {\n")
			for _, key := range keys {
				out.WriteString("        " + quote(key) + ": " + quote(environment[key]) + ",\n")
			}
			out.WriteString("    },\n")
		}
	}
	if manifest.Run.WorkingDirectory != "/app" {
		out.WriteString("    working_directory = " + quote(manifest.Run.WorkingDirectory) + ",\n")
	}
	if manifest.Run.User != "1000:1000" {
		out.WriteString("    user = " + quote(manifest.Run.User) + ",\n")
	}
	if manifest.Run.StopSignal != "SIGTERM" {
		out.WriteString("    stop_signal = " + quote(manifest.Run.StopSignal) + ",\n")
	}
	out.WriteString(")\n")
	return []byte(out.String()), nil
}

// renderableEnvironment drops the PORT the evaluator injects on its own, so
// the rendered file carries only the entries an author actually chose.
func renderableEnvironment(run definition.ManifestRun) map[string]string {
	result := make(map[string]string, len(run.Environment))
	for key, value := range run.Environment {
		if key == "PORT" && value == strconv.Itoa(run.Port) {
			continue
		}
		result[key] = value
	}
	return result
}

func renderStringList(values []string, indentLevel int) string {
	if len(values) <= 4 {
		return renderInlineStrings(values)
	}
	indent := strings.Repeat("    ", indentLevel)
	var out strings.Builder
	out.WriteString("[\n")
	for _, value := range values {
		out.WriteString(indent + "    " + quote(value) + ",\n")
	}
	out.WriteString(indent + "]")
	return out.String()
}

func renderInlineStrings(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = quote(value)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// quote produces a double-quoted literal that Starlark reads back verbatim.
// Go and Starlark share their escape syntax for everything a validated
// manifest can contain.
func quote(value string) string {
	return strconv.Quote(value)
}

// wrapList joins short phrases into comment-width lines with the given
// continuation prefix, so the header stays readable as the list grows.
func wrapList(values []string, continuationPrefix string) string {
	var lines []string
	current := ""
	for index, value := range values {
		phrase := value
		if index < len(values)-1 {
			phrase += ","
		}
		switch {
		case current == "":
			current = phrase
		case len(current)+1+len(phrase) > 60:
			lines, current = append(lines, current), phrase
		default:
			current += " " + phrase
		}
	}
	lines = append(lines, current)
	return strings.Join(lines, "\n"+continuationPrefix)
}
