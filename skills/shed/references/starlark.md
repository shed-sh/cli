<!-- Generated from the SHED evaluator's own spec tables. Do not edit by hand. -->

# The Starlark `SHED` file — full reference

`SHED` (no extension, project root) is the authored alternative to `SHED.yaml`: a Starlark program that evaluates to the exact same manifest. It is an authoring format only — evaluation happens before packaging, the archive embeds the evaluated result, and the `SHED` file itself is structurally excluded from content. The builder, archive, digests, and remote protocol never see the program.

Every signature, argument, type, and default below is rendered from the same spec tables the evaluator validates calls with, so this file cannot promise an argument the binary rejects. Run `shed schema --output json` for the same contract as machine-readable JSON.

Resolution precedence: a `SHED` program is evaluated when present; otherwise `SHED.yaml` is parsed; otherwise detection scaffolds one. Both files present is a `definition_conflict` error — delete one; whichever remains decides the build.

`shed init --format shed` writes a starting file. `shed check` validates edits and reports **every** problem in one pass, not just the first.

## A complete example

```python
b = build(
    srcs = glob(["cmd/**", "go.mod", "go.sum"]),   # or a literal path list
    image = "golang:1.26",
    commands = [
        ["go", "mod", "download"],
        ["go", "build", "-o", "out", "./cmd/app"],
    ],
)

http_app(
    name = "hello-api",          # DNS label, at most 30 characters
    build = b,                   # passed by variable, never inline
    cmd = ["./out"],             # argv list, never a shell string
    port = 8080,
)
```

## The API — these functions and nothing else

Only the functions below are defined. Any other name — `param()`, `worker()`, `detect()` — is an `unknown_name` error listing the whole surface.

### `build(*, srcs, image, commands = [])`

Declare how the project is compiled. Assign the result to a variable and pass it to http_app: b = build(...).

| Argument | Type | Required | Meaning |
|---|---|---|---|
| `srcs` | glob([...]) or list of paths | yes | files the build can see: glob(["cmd/**"]) or a literal list of project paths |
| `image` | string | yes | toolchain image reference, such as "golang:1.26" |
| `commands` | list of argv lists | no (`[]`) | build steps, each an argv list such as ["go", "build", "-o", "out", "./cmd/app"] |

### `http_app(*, name, build, cmd, port, env = {}, working_directory = "/app", user = "1000:1000", stop_signal = "SIGTERM")`

Register the HTTP application to run. Exactly one http_app() call is allowed.

| Argument | Type | Required | Meaning |
|---|---|---|---|
| `name` | string | yes | project name: a lowercase DNS label of at most 30 characters |
| `build` | build() value | yes | the build() value, passed by variable: build = b |
| `cmd` | argv list of strings | yes | process argv list such as ["./out"], never a shell string |
| `port` | int | yes | TCP port the application listens on |
| `env` | dict of string to string | no (`{}`) | runtime environment; PORT is injected automatically when absent |
| `working_directory` | string | no (`"/app"`) | directory the command starts in |
| `user` | string | no (`"1000:1000"`) | uid:gid the process runs as |
| `stop_signal` | string | no (`"SIGTERM"`) | signal sent to stop the application |

### `glob(patterns, *, exclude = [])`

Select source files by pattern from the files Shed collects. Ignored and secret files are excluded before matching, so they can never be selected.

| Argument | Type | Required | Meaning |
|---|---|---|---|
| `patterns` | list of strings | yes | doublestar patterns such as "cmd/**"; a bare directory name selects everything under it |
| `exclude` | list of strings | no (`[]`) | patterns removed from the match |

## Call rules

- **Keyword arguments only.** `build("x")` is `positional_argument`; the hint spells the full call shape. The one exception is the argument marked positional in its signature — everything before the `*`.
- **One representation per argument.** No coercions ever: a string where an argv list belongs is `invalid_argument_type`, not silently wrapped.
- **Unknown or repeated arguments** are `unknown_argument`, with a did-you-mean for near-misses.
- **Commands are argv lists.** `cmd = ["./out"]`, `commands = [["npm", "ci"]]`. There is no shell; write one explicitly if needed: `["sh", "-lc", "a && b"]`.

## Structural rules

- `build()` must be assigned to a variable and passed on by name — `http_app(build = b)`. Written inline it is `inline_build`; assigned but never passed anywhere it is `unused_build`.
- Exactly one `http_app()` call registers the application: a second is `duplicate_app`, none at all is `missing_app`.
- `build()` with `srcs` that selects no files is `empty_srcs` — a build with nothing to read cannot produce anything.

## `srcs` and the collector universe

`glob()` and literal `srcs` lists both resolve against the **collector universe**: the files shed would actually package, after `.gitignore`/`.shedignore` and the structural excludes (`.git`, `node_modules`, `dist`, `target`, `.env*`, `*.pem`, `*.key`, `*.log`, `SHED`, `SHED.yaml`, …). Ignored and secret files are excluded *before* matching, so they can never be selected.

- Glob results are sorted, so evaluation is deterministic. An invalid pattern is `invalid_pattern`.
- A literal entry that names no collected file or directory is `unknown_src`, with a did-you-mean suggestion.
- See exactly what shed collects with `shed deploy --dry-run --output json`.

## The language subset

The file is Starlark, so ordinary program shapes work: variables, `def` functions, list/dict comprehensions, string operations, and the standard universe (`len`, `str`, `range`, …). Deliberately restricted:

- No `while` loops, no recursion, no `set`.
- No `if`/`for` statements at the top level (they work inside `def`; comprehensions work anywhere).
- Globals cannot be reassigned.
- `load()` is not supported — the file is self-contained.
- Evaluation is step-capped, so a pathological loop fails instead of hanging a deploy.

Not yet supported (stated by the tool itself in errors, the schema, and the generated header): param(), worker(), static_site(), multiple apps, detect(), load(), build-time env. The remote-builder `base`/`parts`/`apps` projection cannot be expressed here — keep `SHED.yaml` if you deploy one of those.

## What evaluation produces

The single `http_app()` lowers into the same manifest `SHED.yaml` declares directly:

- `srcs` → `content.include`, sorted and deduplicated.
- `PORT` is injected into `environment` from `port` when absent.
- The result passes the full manifest validation; a residual failure — say, overlapping `srcs` entries like `cmd` and `cmd/app` — is `invalid_definition`, positioned at the `http_app` call.

`shed init --format shed` and evaluation are inverses: rendering a definition and evaluating it reproduces the definition exactly.

## Diagnostics

`shed check` accumulates diagnostics across the whole pass — a file with five mistakes reports five, each positioned `SHED:line:col` with a stable snake_case code and concrete hints. In `--output json`:

```json
{"type":"result","outcome":"invalid","path":"SHED",
 "diagnostics":[{"code":"...","message":"SHED:3:5: ...","hints":["..."]}],
 "nextOperation":"fix_diagnostics"}
```

On success: `outcome: "valid"` plus the evaluated `application`, so you can confirm what shed actually parsed.

| Code | Trigger |
|---|---|
| `syntax_error` | the file does not parse as Starlark |
| `unknown_name` | an undefined name — anything beyond the builtins above and your own variables |
| `positional_argument` | a keyword-only builtin called with positional arguments |
| `unknown_argument` | an argument name the builtin does not accept, or one passed twice |
| `missing_argument` | a required argument absent |
| `invalid_argument_type` | wrong type, empty/multiline `image`, or `port` outside 1..65535 |
| `invalid_pattern` | a `glob()` pattern that is not a valid doublestar pattern |
| `unknown_src` | a literal `srcs` entry matching no collected file |
| `empty_srcs` | `srcs` selected no files |
| `inline_build` | `build(...)` not assigned to a variable as the whole right-hand side |
| `unused_build` | a `build()` value never passed to `http_app` |
| `duplicate_app` | a second `http_app()` call |
| `missing_app` | no `http_app()` call at all |
| `invalid_name` | `name` is not a lowercase DNS label of ≤30 characters |
| `evaluation_error` | a genuine Starlark runtime error, e.g. an out-of-range index |
| `invalid_definition` | the lowered manifest fails validation, e.g. overlapping `srcs` paths |
| `definition_conflict` | both `SHED` and `SHED.yaml` exist in the project |

Source of truth at runtime: `shed schema --output json` prints the API from the same tables that validate calls, and `shed check --output json` reports what the binary in front of you actually accepts — both always win over this document.
