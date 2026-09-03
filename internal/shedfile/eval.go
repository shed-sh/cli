package shedfile

import (
	"errors"
	"sort"
	"strconv"
	"strings"

	"go.starlark.net/resolve"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"

	"shed/internal/definition"
	"shed/internal/diag"
)

// maxExecutionSteps bounds one evaluation. The subset has no while loops and
// no recursion, so any real definition finishes in a tiny fraction of this;
// the cap only stops a pathological for-loop from hanging a deploy.
const maxExecutionSteps = 10_000_000

// Evaluate runs the SHED program against the collector universe — the files
// source.CollectFiles reports, post-ignore and post-structural-excludes — and
// returns the evaluated definition. Diagnostics accumulate across the whole
// pass: a file with five mistakes reports five diagnostics, not the first.
// The returned definition carries an empty Provider, because an authored file
// is authoritative and nothing was detected.
func Evaluate(src []byte, universe []string) (definition.GeneratedDefinition, []*diag.Error) {
	ev := &evaluator{universe: universe}
	options := &syntax.FileOptions{}
	parsed, err := options.Parse(FileName, src, 0)
	if err != nil {
		return definition.GeneratedDefinition{}, []*diag.Error{syntaxDiag(err)}
	}
	ev.lint(parsed)

	predeclared := ev.predeclared()
	program, err := starlark.FileProgram(parsed, predeclared.Has)
	if err != nil {
		ev.diags = append(ev.diags, resolveDiags(err)...)
		return definition.GeneratedDefinition{}, ev.diags
	}
	thread := &starlark.Thread{Name: "shedfile"}
	thread.SetMaxExecutionSteps(maxExecutionSteps)
	if _, err := program.Init(thread, predeclared); err != nil {
		ev.diags = append(ev.diags, evalDiag(err))
	}

	if ev.app == nil && len(ev.diags) == 0 {
		ev.report(syntax.Position{}, "missing_app", FileName+" declares no application.",
			"Register one: http_app(name = ..., build = ..., cmd = ..., port = ...)")
	}
	for _, build := range ev.builds {
		if !build.used {
			ev.report(build.pos, "unused_build", "this build() value is never used.",
				"Pass it to the app: http_app(build = <variable>, ...)")
		}
	}
	if len(ev.diags) > 0 {
		return definition.GeneratedDefinition{}, ev.diags
	}
	generated, lowerErr := lower(ev.app)
	if lowerErr != nil {
		return definition.GeneratedDefinition{}, []*diag.Error{lowerErr}
	}
	return generated, nil
}

// lower assembles the manifest exactly the way the Railpack generator does:
// srcs become content.include, PORT is injected when absent, and Marshal
// validates the result. A residual validation failure — an overlap between
// srcs entries, say — comes back positioned at the http_app call.
func lower(app *appCall) (definition.GeneratedDefinition, *diag.Error) {
	include := append([]string(nil), app.build.srcs...)
	sort.Strings(include)
	include = dedupe(include)

	environment := make(map[string]string, len(app.env)+1)
	for key, value := range app.env {
		environment[key] = value
	}
	if _, present := environment["PORT"]; !present {
		environment["PORT"] = strconv.Itoa(app.port)
	}

	manifest := definition.Manifest{
		APIVersion: definition.ManifestAPIVersion,
		Kind:       definition.ManifestKind,
		Metadata:   &definition.ManifestMetadata{Name: app.name},
		Content:    definition.ManifestContent{Include: include},
		Build:      definition.ManifestBuild{Image: app.build.image, Commands: app.build.commands},
		Run: definition.ManifestRun{
			Command:          app.cmd,
			WorkingDirectory: app.workingDirectory,
			User:             app.user,
			Environment:      environment,
			Port:             app.port,
			StopSignal:       app.stopSignal,
		},
	}
	encoded, err := manifest.Marshal()
	if err != nil {
		return definition.GeneratedDefinition{}, &diag.Error{
			Code:    "invalid_definition",
			Summary: app.pos.String() + ": the evaluated definition is not valid: " + err.Error(),
			Cause:   err,
		}
	}
	return definition.GeneratedDefinition{Manifest: manifest, Source: encoded}, nil
}

func dedupe(sorted []string) []string {
	result := sorted[:0]
	for index, value := range sorted {
		if index == 0 || value != sorted[index-1] {
			result = append(result, value)
		}
	}
	return result
}

func syntaxDiag(err error) *diag.Error {
	summary := err.Error()
	var syntaxErr syntax.Error
	if errors.As(err, &syntaxErr) {
		summary = syntaxErr.Pos.String() + ": " + syntaxErr.Msg
	}
	return &diag.Error{
		Code:    "syntax_error",
		Summary: summary,
		Hints: []string{
			FileName + " is Starlark: Python-shaped calls with named arguments.",
			"Print the full API: shed schema",
		},
		Cause: err,
	}
}

// resolveDiags converts static resolution failures. The interesting one is an
// undefined name — usually an author reaching for a constructor that does not
// exist yet — so that case states the whole V1 surface.
func resolveDiags(err error) []*diag.Error {
	var list resolve.ErrorList
	if !errors.As(err, &list) {
		var single resolve.Error
		if errors.As(err, &single) {
			list = resolve.ErrorList{single}
		}
	}
	if len(list) == 0 {
		return []*diag.Error{{Code: "syntax_error", Summary: err.Error(), Cause: err}}
	}
	diags := make([]*diag.Error, 0, len(list))
	for _, entry := range list {
		code := "syntax_error"
		hints := []string(nil)
		if name, undefined := strings.CutPrefix(entry.Msg, "undefined: "); undefined {
			code = "unknown_name"
			hints = []string{
				"Only build(), http_app(), and glob() exist" + didYouMeanBuiltin(name),
				"Not yet supported: " + strings.Join(notYetSupported, ", "),
			}
		}
		diags = append(diags, &diag.Error{
			Code:    code,
			Summary: entry.Pos.String() + ": " + entry.Msg + ".",
			Hints:   hints,
		})
	}
	return diags
}

func didYouMeanBuiltin(input string) string {
	const maxDistance = 2
	best, bestDistance := "", maxDistance+1
	for _, spec := range builtinSpecs {
		if distance := diag.EditDistance(strings.ToLower(input), spec.name); distance < bestDistance {
			best, bestDistance = spec.name, distance
		}
	}
	if bestDistance > maxDistance {
		return "."
	}
	return "; did you mean " + best + "()?"
}

// evalDiag converts a runtime failure — a genuine Starlark error such as an
// out-of-range index, not one of the recorded diagnostics — into a positioned
// diagnostic using the innermost frame inside the program.
func evalDiag(err error) *diag.Error {
	summary := err.Error()
	var evalErr *starlark.EvalError
	if errors.As(err, &evalErr) {
		for index := len(evalErr.CallStack) - 1; index >= 0; index-- {
			if frame := evalErr.CallStack[index]; frame.Pos.Filename() == FileName {
				summary = frame.Pos.String() + ": " + evalErr.Msg
				break
			}
		}
	}
	return &diag.Error{Code: "evaluation_error", Summary: summary, Cause: err}
}
