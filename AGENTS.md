# Repository Guidelines

## Project Structure & Module Organization

Shed is a Go 1.26.3 CLI for assembling and deploying small Next.js projects.

- `cmd/shed/main.go` is the executable entry point.
- `packages/skills` is the `@shed-sh/skills` npm package — the Node installer for the agent skill. Its version must match `version` in `dist-workspace.toml`.
- `cmd/shed-docs/main.go` regenerates the documentation set — the Shed skill and `llms-full.txt`; it is a build-time tool and is not released.
- `internal/clispec/` declares every command and flag once. It backs argument parsing, `shed help`, `shed help --output json`, and the generated reference, so a flag added here reaches all four at once. Add commands here first; `handlers` in `internal/cli/app.go` must gain a matching entry.
- `internal/docs/` renders the documentation set from `internal/clispec` (commands) and `internal/shedfile`'s spec tables (the SHED language). Never hand-edit generated files — run `task generate`.
- `internal/cli/` parses commands and coordinates user-facing workflows.
- `internal/auth/` implements the encrypted CLI login protocol.
- `internal/credentials/` resolves and stores CLI tokens.
- `internal/railpack/` and the vendored Railpack module provide project detection.
- `internal/definition/` generates and validates the authoritative `SHED.hcl`.
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
task test-e2e                      # CLI subprocess golden-path tests
task test-e2e-docker               # source-to-running-container (needs Docker)
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

### Releasing

This repository is the whole product: source, releases, installers, and the
skill live here. A release is one tag:

```bash
git tag v<version> && git push origin v<version>
```

The tag must match `version` in `dist-workspace.toml`. It fires:

- `release.yml`, the whole release pipeline in one workflow, generated by dist
  from `dist-workspace.toml`: plan → build the four platform archives and the
  installers → publish the GitHub Release, the CLI npm package
  (`npx @shed-sh/shed`), and the skill npm package (`npx @shed-sh/skills`,
  both need `NPM_TOKEN`) → announce → smoke test.

The smoke stage is this repository's own sub-workflow, wired into the
pipeline through `post-announce-jobs` in `dist-workspace.toml`, so it runs
after the assets are live instead of racing the upload:

- `release-smoke.yml` — installs the published release through
  `install-cli.sh`, npm, and `npx @shed-sh/shed`, verifies the published
  `.sha256` matches its artifact, and fails unless the binary reports the
  tagged version.

Never edit `release.yml` by hand; change `dist-workspace.toml` and run
`dist generate`. dist checks the file is current on every release, so a
hand-edit fails the next tag.

The install scripts at the repository root are served at shed.codes and fetch
from this repository's releases. `install-cli.sh` delegates to each release's
own `shed-installer.sh`, so asset naming lives in exactly one place.

`install.sh` installs only the CLI. `install-skills.sh --global` (the default)
clones this repository into `~/.shed/skills` and symlinks `skills/shed` into
each coding agent found on the machine. `install-skills.sh --local` copies
the skill into the current project's `.claude/skills/shed`. git is the only
requirement. Claude Code can also load this repository as a plugin
(`.claude-plugin/`).

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

### Testing pyramid

1. **Unit tests** on leaf packages (`internal/source`, `internal/shedfile`, `internal/definition`, `internal/clispec`, …) — archive rules, schema, argument binding, and other pure logic.
2. **Integration tests** on `internal/cli` — the default place for command behavior. They call `App.Run` with buffers, not a subprocess. Put parsing, output shaping, error mapping, and flag matrices here.
3. **E2E tests** in `internal/e2e` — a small golden-path surface only. They spawn the real `shed` binary with an isolated `HOME`:
   - CLI subprocess tests (always): help/schema contract, `init` → `check` → `deploy --dry-run` / `--mock`, and install-script usage.
   - Detection tests against the pinned repositories in `testdata/e2e/repos`.
   - Docker tests (opt-in, `SHED_E2E_DOCKER=1` / `task test-e2e-docker`): source to a running HTTP server, including through the real CLI.

Treat e2e coverage as scarce. If an assertion can be expressed faithfully by calling `App.Run`, it belongs in `internal/cli`, not here. Do not use e2e for help-text wording, flag tables, diagnostic copy, or schema rendering unless the subprocess boundary itself is what is being validated.

Tests must stay correct under package-level parallelism and slow CI. Synchronize on observable conditions — never `time.Sleep` for startup, readiness, or cleanup. Subprocess failures must include stdout and stderr. Cleanup must target only owned containers, images, paths, and temp homes.

## Commit & Pull Request Guidelines

Git history is not included in this checkout, so no repository-specific commit convention can be inferred. Use concise, imperative subjects such as `Reject bundles with external symlinks`, and keep each commit focused.

Pull requests should explain user-visible behavior, call out API or bundle-format changes, and list verification commands. Link relevant issues. Include terminal output for CLI changes; add screenshots only for browser-opening behavior.

## Configuration & Security

Use `SHED_API_URL` to target a non-default API and `SHED_TOKEN` for temporary authentication. Never commit tokens, generated bundles, `.env` files, private keys, or credential files.
