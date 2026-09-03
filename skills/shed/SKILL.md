---
name: shed
description: Help end-users of the `shed` CLI install it, describe an app in a clean `SHED.hcl` (or Starlark `SHED`), and deploy it — to the Shed cloud by default, or locally in Docker with `--local`. Trigger when the user runs `shed` (init/check/schema/deploy/status/logs/cancel/stop/destroy/login/whoami/share), edits `SHED.hcl` (one `application "<name>"` block), edits a Starlark `SHED` file, writes a `.shedignore`, or asks about shed error codes, the deploy state machine, or shed's JSON/NDJSON output contract. Do NOT trigger for unrelated Go CLIs, generic Dockerfiles, Railpack used outside of shed, or contributing to the shed codebase itself.
---

# shed

Shed packages an app into a deterministic `tar.gz`, builds it, and runs it — on the Shed cloud by default, or locally in Docker with `--local`.

The whole thing is driven by one file at the project root: **`SHED.hcl`** (declarative HCL) or **`SHED`** (Starlark that evaluates to the same shape). `shed init` writes one for you; from then on it is authoritative — shed never regenerates or heals it. Without one, `shed deploy` detects the project and generates the same definition in memory for that deploy alone; it never writes it.

Shed is agent-first: it never prompts except `shed login`, and every command supports `--output json` / `--output ndjson` with a stable failure envelope. Prefer JSON whenever you invoke shed programmatically.

## Install

```
curl -fsSL shed.codes/install.sh | sh
npx @shed-sh/shed
```

That installs the CLI, or runs it once via npx without putting it on PATH. `npm i -g @shed-sh/shed` (bun reads the same registry) is the persistent npm install. The skill is a separate package:

```
curl -fsSL shed.codes/install-skills.sh | sh                 # this machine
npx @shed-sh/skills
curl -fsSL shed.codes/install-skills.sh | sh -s -- --local   # this project
npx @shed-sh/skills --local
```

`--global` (the default) clones one copy into `~/.shed/skills` and symlinks it into every coding agent on the machine. `--local` copies it into `.claude/skills/shed` so the project carries the skill. git is the only requirement; rerun to update.

Docker is only needed for `--local`. `--mock` packages without Docker or the cloud, for checking what would ship.

Verify with `shed version`. Sign in with `shed login` before deploying.

## The three commands you actually run

```
shed init              # detect the project and write SHED.hcl
shed deploy .          # package → submit to the Shed cloud (`shed .` is shorthand; needs `shed login`)
shed deploy . --local  # package → build → run here, in Docker
```

Everything else operates on the deployment id or instance id printed by `deploy`. Full surface:

<!-- BEGIN GENERATED: commands -->
| Command | Purpose |
|---|---|
| `shed deploy [directory]` | Send the project to the Shed cloud, or build and run it here with --local |
| `shed init [directory]` | Look at the project and write SHED.hcl, or SHED with --format shed |
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

## A clean `SHED.hcl`

`SHED.hcl` describes, compactly, **what to ship, how to build it, and how to run it**. It is one `application` block; the label is the name, and there is no version or kind header. Every field below is real; nothing is optional-looking-required.

```hcl
application "my-app" {               # the label is the name: DNS label, ^[a-z0-9](?:[a-z0-9-]{0,28}[a-z0-9])?$
  content {
    include = [                      # required, non-empty; paths shed will package
      "package.json",
      "package-lock.json",
      "server.js",
    ]
  }

  build {
    image = "node:24"                # any Docker image ref
    commands = [                     # argv lists — NO SHELL
      ["npm", "ci"],
      ["npm", "run", "build"],
    ]
  }

  run {
    command           = ["node", "server.js"]   # required, non-empty argv
    port              = 8080                    # 1..65535; app must actually listen on it
    working_directory = "/app"                  # optional
    environment = {                             # optional; values are strings
      LOG_LEVEL = "info"
    }
  }
}
```

A real, working Go example lives at this repo's own `SHED.hcl`.

Rules that trip people up:

- **Unknown attributes and blocks are rejected.** Typos fail loudly.
- **Exactly one `application` block, and it needs its label.** `application {` without a name does not parse.
- **`content.include` is a whitelist.** Only these paths are packaged. Entries must be relative, no `.`/`..`/absolute, and **no overlap** (`a` and `a/b` together is an error).
- **No shell in commands.** Each command is argv (a list). `;`, `|`, `&`, `<`, `>`, `$`, backtick are rejected. If you truly need a shell: `[sh, -lc, "cmd1 && cmd2"]`.
- **Port must be real.** The container must actually listen on `run.port` (or `$PORT` when Railpack sets it), or the readiness check fails.
- **Structural excludes always apply** — `.git`, `node_modules`, `.next`, `dist`, `target`, `.env*`, `*.pem`, `*.key`, `*.log`, `SHED.hcl` itself, etc. — even if you put them in `include`. This is a hard rule to prevent secret leaks; do not try to bypass it.

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

Rules that trip people up: every argument is named (only `glob`'s patterns may be positional); `build()` must be assigned to a variable, never written inline; commands are argv lists, never shell strings; `PORT` is injected from `port` when absent. Never have both `SHED` and `SHED.hcl` — that's a `definition_conflict` error.

Full builtin signatures, the language subset, and every diagnostic code in `references/starlark.md`; `shed schema` prints the same API from the binary itself.

## Authoring loop

Don't guess whether a definition is valid — ask shed:

```
shed check --output json     # every diagnostic at once
shed schema                  # the Starlark `SHED` API
```

`shed check` exits 1 with `outcome: "invalid"` and a `diagnostics` array on failure; on success it returns `outcome: "valid"` plus the evaluated `application` so you can confirm what shed actually parsed. Prefer it over `shed deploy` while iterating.

## Deploy: cloud (the default)

1. `cd` into the app root.
2. `shed init --output json` — writes `SHED.hcl`. The `definitionReport` in the JSON shows detected provider, includes, builds, run command, and whether the port is `assumed` or `declared`.
3. Review the file. From here it is authoritative — delete it to redetect.
4. `shed deploy . --output json` — packages and submits it. Returns a ready URL, or a `"pending"` receipt after 30s (see below).

What `deploy` does with the directory: if `SHED.hcl` or `SHED` exists it is used exactly as written. If neither exists, shed detects the project and generates the definition in memory for that deploy only — the project is not written to; step 2 is how you keep one. Either way the archive it ships carries the definition.

```
shed login                                    # opens a browser; the code comes back on its own (only interactive command)
shed . --output json                          # returns a ready URL, or a "pending" receipt after 30s
shed status <dep-id> --wait --output ndjson   # resume if it came back pending
shed logs   <dep-id> --stage build --follow --output ndjson
shed cancel <dep-id>
```

- Default wait timeout is 30s; a `"pending"` result **is not a failure** — resume with `shed status <id> --wait` rather than re-deploying.
- If a deploy errors, times out, or returns `pending`, report exactly what shed printed. Never invent a deployment URL.
- `--remote` is accepted and changes nothing; the cloud is already the default.

## Deploy: local (optional, needs Docker)

`shed deploy . --local` builds a Docker image on this machine and starts one container. On success prints `Ready: <url> (HTTP <code>)` and an instance id. `shed stop <instance-id>` to stop; `shed destroy <instance-id>` to also forget it. `--local`, `--mock`, and `--remote` are mutually exclusive.

Preview what will actually ship, without building:

```
shed deploy . --dry-run --archive /tmp/app.tar.gz --output json
tar -tzf /tmp/app.tar.gz
```

## `shed init` autodetection scope

`shed init` only detects **Node.js / Next.js** (npm, pnpm, yarn — lockfile required) and **Go modules** (root `go.mod`). Anything else — Python, Ruby, Bun, static SPAs, Rust, workspaces-only Go — errors with `unsupported_project`.

For those, **hand-author `SHED.hcl`**. The executor is provider-neutral: any Docker image + argv works.

## Reading errors

Every failure in `--output json` mode:

```json
{"type":"result","outcome":"failed","failure":{"code":"...","message":"...","recoverable":false,"operation":"..."}}
```

`code` is stable; `message` is prose. The ones you'll actually hit:

| Code | Meaning / fix |
|---|---|
| `unsupported_project` | Railpack detected the language but shed doesn't autogenerate for it. Hand-author `SHED.hcl`. |
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
- Do not edit `SHED.hcl` and re-run `shed init` expecting a merge. `init` only writes when the file is missing.
- Do not write a `static:` workload — validation rejects it. Serve static files with a real server in `run.command`.
- Do not claim a remote deployment succeeded unless shed printed a ready URL. If `--remote` failed or is `pending`, say so and resume with `shed status <id> --wait`.

## When this isn't enough

- `references/commands.md` — every subcommand and flag, generated from the CLI's own registry.
- `references/schema.md` — full `SHED.hcl` schema including the remote-builder projection.
- `references/starlark.md` — full Starlark `SHED` reference: builtins, call rules, language subset, diagnostic codes.
- `references/errors.md` — full error code table with source-of-truth file paths.
- `shed help --output json` at runtime — authoritative over anything written here.
- This repo's own `SHED.hcl` — a live, working Go example.
