---
name: shed
description: Help end-users of the `shed` CLI install it, describe an app in a clean `SHED.yaml` (or Starlark `SHED`), and deploy it — locally with Docker or to the Shed cloud with `--remote`. Trigger when the user runs `shed` (init/check/schema/deploy/status/logs/cancel/stop/destroy/login/whoami/share), edits `SHED.yaml` (apiVersion `shed.run/v1alpha1`, kind `Application`), edits a Starlark `SHED` file, writes a `.shedignore`, or asks about shed error codes, the deploy state machine, or shed's JSON/NDJSON output contract. Do NOT trigger for unrelated Go CLIs, generic Dockerfiles, Railpack used outside of shed, or contributing to the shed codebase itself.
---

# shed

Shed packages an app into a deterministic `tar.gz`, builds it, and runs it — locally in Docker or on the Shed cloud with `--remote`.

The whole thing is driven by one file at the project root: **`SHED.yaml`** (declarative) or **`SHED`** (Starlark that evaluates to the same shape). `shed init` writes one for you; from then on it is authoritative — shed never regenerates or heals it.

Shed is agent-first: it never prompts except `shed login`, and every command supports `--output json` / `--output ndjson` with a stable failure envelope. Prefer JSON whenever you invoke shed programmatically.

## Install

```
curl -fsSL shed.codes/install.sh | sh
```

That installs both the CLI and this skill. One half at a time: `shed.codes/install-cli.sh` or `shed.codes/install-skills.sh`, piped to `sh` the same way. The CLI alone also ships as `npm i -g @shed-sh/shed` (bun reads the same registry), `brew install shed-sh/tap/shed`, and `paru -S shed-bin`. The skill script clones one canonical copy into `~/.shed/skills` and symlinks it into every coding agent on the machine; rerun it to update.

Requires Docker running for local builds. `--mock` skips Docker for packaging tests.

Verify with `shed version`. Sign in for cloud deploys with `shed login`.

## The three commands you actually run

```
shed init              # detect the project and write SHED.yaml
shed deploy .          # package → build → run locally (`shed .` is shorthand)
shed deploy . --remote # submit to the Shed cloud (needs `shed login`)
```

Everything else operates on the deployment id or instance id printed by `deploy`. Full surface:

<!-- BEGIN GENERATED: commands -->
| Command | Purpose |
|---|---|
| `shed deploy [directory]` | Build the project and run it locally, or send it up with --remote |
| `shed init [directory]` | Look at the project and write SHED.yaml, or SHED with --format shed |
| `shed check [directory]` | Evaluate the definition and report every problem in it |
| `shed schema` | Print the SHED file API |
| `shed status <deployment>` | Show, or wait for, a deployment's state |
| `shed logs <deployment>` | Show build and runtime logs |
| `shed open <url>` | Open a deployment in your browser |
| `shed stop <instance>` | Stop a local instance |
| `shed destroy <instance>` | Stop a local instance and forget it |
| `shed cancel <deployment>` | Ask the cloud to cancel a deployment |
| `shed share <deployment> <email>` | Give someone access |
| `shed revoke <deployment> <email>` | Take someone's access away |
| `shed login` | Sign in to Shed |
| `shed logout` | Sign out and revoke this machine's token |
| `shed whoami` | Show who you are signed in as |
| `shed upgrade` | Replace this binary with a newer released one |
| `shed version` | Print the CLI version |
| `shed help [command]` | Show this help, or one command's options |
<!-- END GENERATED: commands -->

At runtime, prefer `shed help <command>` and `shed help --output json` over guessing — they come from the binary in front of you and always win over anything written here.

## A clean `SHED.yaml`

`SHED.yaml` describes, compactly, **what to ship, how to build it, and how to run it**. Every field below is real; nothing is optional-looking-required.

```yaml
apiVersion: shed.run/v1alpha1        # required, exact
kind: Application                    # required, exact
metadata:
  name: my-app                       # DNS label: ^[a-z0-9](?:[a-z0-9-]{0,28}[a-z0-9])?$
content:
  include:                           # required, non-empty; paths shed will package
    - package.json
    - package-lock.json
    - server.js
build:
  image: node:24                     # any Docker image ref
  commands:                          # argv arrays — NO SHELL
    - [npm, ci]
    - [npm, run, build]
run:
  command: [node, server.js]         # required, non-empty argv
  port: 8080                         # 1..65535; app must actually listen on it
  workingDirectory: /app             # optional
  environment:                       # optional
    LOG_LEVEL: info
```

A real, working Go example lives at this repo's own `SHED.yaml`.

Rules that trip people up:

- **Unknown top-level fields are rejected.** Typos fail loudly.
- **`content.include` is a whitelist.** Only these paths are packaged. Entries must be relative, no `.`/`..`/absolute, and **no overlap** (`a` and `a/b` together is an error).
- **No shell in commands.** Each command is argv (a list). `;`, `|`, `&`, `<`, `>`, `$`, backtick are rejected. If you truly need a shell: `[sh, -lc, "cmd1 && cmd2"]`.
- **Port must be real.** The container must actually listen on `run.port` (or `$PORT` when Railpack sets it), or the readiness check fails.
- **Structural excludes always apply** — `.git`, `node_modules`, `.next`, `dist`, `target`, `.env*`, `*.pem`, `*.key`, `*.log`, `SHED.yaml` itself, etc. — even if you put them in `include`. This is a hard rule to prevent secret leaks; do not try to bypass it.

Full schema (including the `base` / `parts` / `apps` remote-builder projection) in `references/schema.md`.

## The Starlark `SHED` alternative

`shed init --format shed` writes `SHED` (no extension) instead — a Starlark program that evaluates to the same manifest. Only the evaluated result ships; the file itself is never packaged. Three builtins exist and nothing else: `build()`, `http_app()` (exactly one call), `glob()`.

```python
b = build(
    srcs = glob(["cmd/**", "go.mod", "go.sum"]),
    image = "golang:1.26",
    commands = [["go", "build", "-o", "out", "./cmd/app"]],
)

http_app(name = "my-app", build = b, cmd = ["./out"], port = 8080)
```

Rules that trip people up: every argument is named (only `glob`'s patterns may be positional); `build()` must be assigned to a variable, never written inline; commands are argv lists, never shell strings; `PORT` is injected from `port` when absent. Never have both `SHED` and `SHED.yaml` — that's a `definition_conflict` error.

Full builtin signatures, the language subset, and every diagnostic code in `references/starlark.md`; `shed schema` prints the same API from the binary itself.

## Authoring loop

Don't guess whether a definition is valid — ask shed:

```
shed check --output json     # every diagnostic at once
shed schema                  # the Starlark `SHED` API
```

`shed check` exits 1 with `outcome: "invalid"` and a `diagnostics` array on failure; on success it returns `outcome: "valid"` plus the evaluated `application` so you can confirm what shed actually parsed. Prefer it over `shed deploy` while iterating.

## Deploy: local

1. `cd` into the app root.
2. `shed init --output json` — writes `SHED.yaml`. The `definitionReport` in the JSON shows detected provider, includes, builds, run command, and whether the port is `assumed` or `declared`.
3. Review the file. From here it is authoritative — delete it to redetect.
4. `shed deploy .` — builds a Docker image and starts one container. On success prints `Ready: <url> (HTTP <code>)` and an instance id.
5. `shed stop <instance-id>` to stop; `shed destroy <instance-id>` to also forget it.

Preview what will actually ship, without building:

```
shed deploy . --dry-run --archive /tmp/app.tar.gz --output json
tar -tzf /tmp/app.tar.gz
```

## Deploy: remote (Shed cloud)

```
shed login                                    # opens a browser; the code comes back on its own (only interactive command)
shed . --remote --output json                 # returns a ready URL, or a "pending" receipt after 30s
shed status <dep-id> --wait --output ndjson   # resume if it came back pending
shed logs   <dep-id> --stage build --follow --output ndjson
shed cancel <dep-id>
```

- `--remote` and `--mock` are mutually exclusive; `--detach` and `--wait` are mutually exclusive.
- Default wait timeout is 30s; a `"pending"` result **is not a failure** — resume with `shed status <id> --wait` rather than re-deploying.
- If `--remote` errors, times out, or returns `pending`, report exactly what shed printed. Never invent a deployment URL.

## `shed init` autodetection scope

`shed init` only detects **Node.js / Next.js** (npm, pnpm, yarn — lockfile required) and **Go modules** (root `go.mod`). Anything else — Python, Ruby, Bun, static SPAs, Rust, workspaces-only Go — errors with `unsupported_project`.

For those, **hand-author `SHED.yaml`**. The executor is provider-neutral: any Docker image + argv works.

## Reading errors

Every failure in `--output json` mode:

```json
{"type":"result","outcome":"failed","failure":{"code":"...","message":"...","recoverable":false,"operation":"..."}}
```

`code` is stable; `message` is prose. The ones you'll actually hit:

| Code | Meaning / fix |
|---|---|
| `unsupported_project` | Railpack detected the language but shed doesn't autogenerate for it. Hand-author `SHED.yaml`. |
| `detection_failed` | Railpack couldn't identify the app. Add a lockfile / `go.mod`, or hand-author. |
| `missing_package_json` / `missing_lockfile` | Node project without `package.json` or one of `package-lock.json` / `npm-shrinkwrap.json` / `yarn.lock` / `pnpm-lock.yaml` / `bun.lock(b)`. |
| `missing_go_mod` | Go project without a root `go.mod`. Workspaces-only won't work. |
| `not_a_directory` / `directory_not_found` | Bad path argument to `shed deploy`. |
| `unknown_command` | Typos; shed prints "did you mean". |
| `remote_api_error` | `--remote` HTTP failure. `recoverable: true` for 408/429/5xx — retry. |
| `operation_failed` | Generic wrapper — read `message` and any nested cause. |

Full table in `references/errors.md`.

## Deployment state machine

Non-terminal: `accepted`, `bundle_validating`, `build_queued`, `building`, `verifying`, `provisioning`, `health_checking`.
Terminal: `ready`, `failed`, `cancelled`.

`Result.Outcome` collapses to `ready` / `failed` / `cancelled` / `pending`, with `NextOperation` = `""` / `logs` / `""` / `status`. `pending` always includes the deployment id — resume via `shed status <id> --wait`.

## Ignore rules

Both `.gitignore` and `.shedignore` are read. Supported subset: `#` comments, `!` negation, trailing `/` for directory-only, leading `/` for root-anchor, shell wildcards.

**Any pattern containing `/` is anchored to the project root.** So `/build` ignores only a top-level `build`; `build` alone matches a `build` segment anywhere. Getting this backwards is the usual reason an ignore rule "does nothing".

Debug ignores with `shed deploy . --dry-run --output json` and read `source.manifest.files`.

## Environment and auth

- `SHED_TOKEN` — override stored credential without persisting.
- Credentials live in the OS keyring; fallback file is `$UserConfigDir/shed/config.json` (mode 0600) with a warning.
- `shed logout --local` deletes creds without contacting the server (useful if it's down).

## Do NOT do these things

- Do not write shell operators (`&&`, `|`, `;`, `$VAR`, backticks) inside `build.commands` or `run.command` entries. Use `[sh, -lc, "..."]` if you truly need a shell.
- Do not put `.env`, `*.pem`, `*.key`, `node_modules`, `.git` in `content.include` — they are silently stripped.
- Do not edit `SHED.yaml` and re-run `shed init` expecting a merge. `init` only writes when the file is missing.
- Do not write a `static:` workload — validation rejects it. Serve static files with a real server in `run.command`.
- Do not claim a remote deployment succeeded unless shed printed a ready URL. If `--remote` failed or is `pending`, say so and resume with `shed status <id> --wait`.

## When this isn't enough

- `references/commands.md` — every subcommand and flag, generated from the CLI's own registry.
- `references/schema.md` — full `SHED.yaml` schema including the remote-builder projection.
- `references/starlark.md` — full Starlark `SHED` reference: builtins, call rules, language subset, diagnostic codes.
- `references/errors.md` — full error code table with source-of-truth file paths.
- `shed help --output json` at runtime — authoritative over anything written here.
- This repo's own `SHED.yaml` — a live, working Go example.
