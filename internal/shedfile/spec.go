// Package shedfile evaluates the authored SHED program into the same
// definition.Manifest that SHED.hcl declares directly. The program is an
// authoring convenience only: the builder, the archive, the digests, and the
// remote protocol never see it, so determinism and idempotency are unaffected.
package shedfile

// FileName is the authored program. It has no extension on purpose: the file
// is an application definition first and a Starlark program second.
const FileName = "SHED"

// argKind names one accepted representation for an argument. Each field has
// exactly one, so an author never has to guess which of several spellings a
// value takes.
type argKind int

const (
	kindString argKind = iota
	kindInt
	kindStringList
	kindCommand  // one argv list, such as ["./out"]
	kindCommands // a list of argv lists
	kindStringDict
	kindBuild // the value returned by build(), passed by variable
	kindSrcs  // glob([...]) or a literal list of project paths
)

// typeName is the wording shared by error messages and the schema, so what a
// failure demands and what the documentation promises can never differ.
func (kind argKind) typeName() string {
	switch kind {
	case kindString:
		return "string"
	case kindInt:
		return "int"
	case kindStringList:
		return "list of strings"
	case kindCommand:
		return "argv list of strings"
	case kindCommands:
		return "list of argv lists"
	case kindStringDict:
		return "dict of string to string"
	case kindBuild:
		return "build() value"
	case kindSrcs:
		return "glob([...]) or list of paths"
	}
	return "value"
}

type argSpec struct {
	name     string
	kind     argKind
	required bool
	// positional marks the one argument a builtin accepts without its name.
	// Only glob's patterns has it; everything else is keyword-only.
	positional bool
	// defaultLiteral is the Starlark spelling of the value an omitted optional
	// argument takes, shown verbatim in the schema.
	defaultLiteral string
	doc            string
}

type builtinSpec struct {
	name string
	doc  string
	args []argSpec
}

func (spec builtinSpec) arg(name string) (argSpec, bool) {
	for _, candidate := range spec.args {
		if candidate.name == name {
			return candidate, true
		}
	}
	return argSpec{}, false
}

// builtinSpecs is the single source of truth for the V1 surface. Kwarg
// validation, accepted-argument lists, did-you-mean suggestions, and the
// schema command all read from here, so errors and documentation cannot
// disagree.
var builtinSpecs = []builtinSpec{
	{
		name: "build",
		doc:  "Declare how the project is compiled. Assign the result to a variable and pass it to http_app: b = build(...).",
		args: []argSpec{
			{name: "srcs", kind: kindSrcs, required: true,
				doc: "files the build can see: glob([\"cmd/**\"]) or a literal list of project paths"},
			{name: "image", kind: kindString, required: true,
				doc: "toolchain image reference, such as \"golang:1.26\""},
			{name: "commands", kind: kindCommands, defaultLiteral: "[]",
				doc: "build steps, each an argv list such as [\"go\", \"build\", \"-o\", \"out\", \"./cmd/app\"]"},
		},
	},
	{
		name: "http_app",
		doc:  "Register the HTTP application to run. Exactly one http_app() call is allowed.",
		args: []argSpec{
			{name: "name", kind: kindString, required: true,
				doc: "project name: a lowercase DNS label of at most 30 characters"},
			{name: "build", kind: kindBuild, required: true,
				doc: "the build() value, passed by variable: build = b"},
			{name: "cmd", kind: kindCommand, required: true,
				doc: "process argv list such as [\"./out\"], never a shell string"},
			{name: "port", kind: kindInt, required: true,
				doc: "TCP port the application listens on"},
			{name: "env", kind: kindStringDict, defaultLiteral: "{}",
				doc: "runtime environment; PORT is injected automatically when absent"},
			{name: "working_directory", kind: kindString, defaultLiteral: `"/app"`,
				doc: "directory the command starts in"},
			{name: "user", kind: kindString, defaultLiteral: `"1000:1000"`,
				doc: "uid:gid the process runs as"},
			{name: "stop_signal", kind: kindString, defaultLiteral: `"SIGTERM"`,
				doc: "signal sent to stop the application"},
		},
	},
	{
		name: "glob",
		doc:  "Select source files by pattern from the files Shed collects. Ignored and secret files are excluded before matching, so they can never be selected.",
		args: []argSpec{
			{name: "patterns", kind: kindStringList, required: true, positional: true,
				doc: "doublestar patterns such as \"cmd/**\"; a bare directory name selects everything under it"},
			{name: "exclude", kind: kindStringList, defaultLiteral: "[]",
				doc: "patterns removed from the match"},
		},
	},
}

func specFor(name string) builtinSpec {
	for _, spec := range builtinSpecs {
		if spec.name == name {
			return spec
		}
	}
	panic("shedfile: unknown builtin " + name)
}

// notYetSupported is stated wherever the surface is described — the generated
// header, the schema, and the unknown-name error — so an author or agent
// learns the boundary from the tool instead of by trial.
var notYetSupported = []string{
	"param()", "worker()", "static_site()", "multiple apps", "detect()", "load()", "build-time env",
}
