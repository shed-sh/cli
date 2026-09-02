# Repository Guidelines

## Project Structure & Module Organization

Shed is a Go 1.26.3 CLI for assembling and deploying small Next.js projects.

- `cmd/shed/main.go` is the executable entry point.
- `cmd/shed-docs/main.go` regenerates the documentation set — the Shed skill and `llms-full.txt`; it is a build-time tool and is not released.
- `internal/clispec/` declares every command and flag once. It backs argument parsing, `shed help`, `shed help --output json`, and the generated reference, so a flag added here reaches all four at once. Add commands here first; `handlers` in `internal/cli/app.go` must gain a matching entry.
- `internal/docs/` renders the documentation set from `internal/clispec` (commands) and `internal/shedfile`'s spec tables (the SHED language). Never hand-edit generated files — run `task generate`.
- `internal/cli/` parses commands and coordinates user-facing workflows.
- `internal/auth/` implements the encrypted CLI login protocol.
- `internal/credentials/` resolves and stores CLI tokens.
- `internal/railpack/` and the vendored Railpack module provide project detection.
- `internal/definition/` generates and validates the authoritative `SHED.yaml`.
- `internal/shedfile/` evaluates the authored Starlark `SHED` program into that same manifest and renders/validates it for `init --format shed`, `check`, and `schema`.
- `internal/source/` resolves content, creates deterministic archives, and safely extracts them.
- `internal/api/` contains the draft Shed cloud HTTP contract.
- `internal/config/` loads API settings and supports the protected-file credential fallback.
- Tests live beside their packages as `*_test.go` files.

Keep cloud protocol changes isolated in `internal/api`; keep filesystem and archive behavior in `internal/source`. Builders must consume the immutable archive and must not inspect the original project directory.

## Build, Test, and Development Commands

Run Task commands from the repository root:

```bash
task build                         # build the local CLI
task test                          # run all package tests
task test -- ./internal/source     # run one package's tests
task format                        # format Go source files
task generate                      # regenerate the documentation set from its registries
task generate-check                # fail if any generated documentation is stale
task plugin-validate               # validate the Claude Code plugin manifest
task lint                          # run golangci-lint as CI does
task typecheck                     # run go vet
task check                         # run the complete local CI suite
```

## The documentation pipeline

Documentation is a build artifact. Each generated file has one source of truth,
and `task check` (via `generate-check`) fails the build whenever a committed
file no longer matches what `shed-docs` would produce — including after edits to
any of the sources below:

| Generated file | Source of truth |
|---|---|
| `skills/shed/references/commands.md` | the `internal/clispec` registry |
| `skills/shed/references/starlark.md` | `builtinSpecs` in `internal/shedfile/spec.go` |
| `skills/shed/SKILL.md` | hand-written, except its `<!-- BEGIN GENERATED -->` regions |
| `llms-full.txt` | all of the above plus the hand-written references, concatenated |

So the workflow for any surface change is the same: edit the source of truth,
then run `task generate` and commit the regenerated files alongside it.

- Changing a **command or flag**: edit `internal/clispec`.
- Changing the **SHED language** — a builtin, an argument, a default, a doc
  string: edit `internal/shedfile/spec.go`. Validation, error hints,
  `shed schema`, and the reference all update from that one edit; a builtin
  cannot ship undocumented (`TestStarlarkReferenceDocumentsEveryBuiltin`).
- Changing a **hand-written reference** (`schema.md`, `errors.md`) or SKILL.md
  prose: edit the file itself, then still run `task generate`, because its
  content is folded into `llms-full.txt`.

`llms-full.txt` is the whole documentation set as one self-contained file — the
context to hand an agent that does not have the skill installed.

## The Claude Code skill

`skills/shed/` teaches coding agents how to drive the CLI. `references/commands.md`
and `references/starlark.md` are generated; `SKILL.md` is hand-written apart from
its `<!-- BEGIN GENERATED -->` regions; the other references are hand-written.

Its audience is **end users of the shed CLI**, and it is published to a public
repository on release. So it must never cite internal source paths, describe
unreleased or unbuilt functionality, or reference commits and issues — a reader
cannot see any of that. Describe only what the released binary does.

### Publishing

`shed-sh/cli` is the public face of this repository. It receives:

- release binaries, installers, the Homebrew formula, and the npm package
  (`@shed-sh/shed`, needs an `NPM_TOKEN` secret), from dist (`release.yml`,
  configured by `dist-workspace.toml`) — the GitHub Release is created there
  because this repository is private and a private repository's release assets
  cannot be downloaded by `install.sh`. `repository` in `dist-workspace.toml`
  must stay pointed at the public repo: dist bakes it into the installers as
  the download URL. `install.sh` delegates the CLI install to the release's
  own `shed-installer.sh`, so asset naming lives in exactly one place;
- the `shed-bin` AUR package, from `publish-aur.yml`, rendered from
  `.github/aur/*.tmpl` with the release's checksums (needs an
  `AUR_SSH_PRIVATE_KEY` secret; a no-op until it is configured);
- `skills/`, the plugin manifests, the install scripts (`install-cli.sh`,
  `install-skills.sh`, `install-all.sh`, plus `install.sh` published as an
  alias of the last), and the public README, from
  `.github/workflows/publish-skill.yml` on the release tag. That workflow ends
  by installing shed through the public path and checking `shed version`, so a
  release that cannot be installed fails loudly instead of shipping quietly.

Everything mirrored there lives under `.github/public-repo/` here, and the
public repository is never hand-edited — an edit made there is lost on the next
release. Both jobs need `PUBLIC_REPO_TOKEN`, a token with `contents:write` on
`shed-sh/cli`.

`install-skills.sh` clones the public repository into `~/.shed/skills` and
symlinks `skills/shed` into each coding agent found on the machine, so one
canonical copy serves every agent and rerunning the script (a `git pull`)
updates all of them at once. git is its only requirement.

Install it locally with `cp -R skills/shed ~/.claude/skills/shed`, or from this
repository as a plugin.

Use `--list-files` to review bundle inclusion and `--output prototype.tar.gz` to inspect the archive.

## Development Workflow

1. Use the repository's `task` targets instead of invoking Go, formatting, or lint commands directly.
2. While developing, run focused tests with `task test -- <package_path>`, for example `task test -- ./internal/auth ./internal/cli`.
3. After finishing a feature, run `task format`, then `task check`. Fix every formatting, lint, test, type-check, and build failure before committing.
4. Immediately before committing, run `task check` again against the final working tree. Do not commit or push when any check fails.

## Coding Style & Naming Conventions

Follow standard Go conventions and let `gofmt` define tabs, spacing, and import grouping. Package names should be short, lowercase nouns. Exported identifiers use `PascalCase`; unexported identifiers use `camelCase`. Keep command handlers small and return errors to `App.Run`, which owns user-facing error output and exit codes.

Prefer the standard library unless a dependency clearly simplifies the implementation. Preserve deterministic archive ordering and exclusions for `.env*`, credentials, build output, and external symlinks.

## Testing Guidelines

Use Go's `testing` package. Name tests `Test<Behavior>`, for example `TestAssembleRejectsSymlinkOutsideProject`. Build isolated fixtures with `t.TempDir()` and helper functions marked with `t.Helper()`. Add regression tests for ignore-rule precedence, archive contents, argument normalization, and security boundaries. Run `task check` before submitting changes; no numeric coverage threshold is currently configured.

## Commit & Pull Request Guidelines

Git history is not included in this checkout, so no repository-specific commit convention can be inferred. Use concise, imperative subjects such as `Reject bundles with external symlinks`, and keep each commit focused.

Pull requests should explain user-visible behavior, call out API or bundle-format changes, and list verification commands. Link relevant issues. Include terminal output for CLI changes; add screenshots only for browser-opening behavior.

## Configuration & Security

Use `SHED_API_URL` to target a non-default API and `SHED_TOKEN` for temporary authentication. Never commit tokens, generated bundles, `.env` files, private keys, or credential files.
