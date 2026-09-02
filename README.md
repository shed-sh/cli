# Shed

[![CI](https://github.com/shed-sh/cli/actions/workflows/ci.yml/badge.svg)](https://github.com/shed-sh/cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/shed-sh/cli?display_name=tag&sort=semver)](https://github.com/shed-sh/cli/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Deploy small software from your terminal. One file describes the app, and `shed deploy` packages it into a deterministic archive, builds it, and runs it: locally in Docker, or on the Shed cloud with a live URL.

Shed is **agent-first**: it never prompts (except `shed login`), every command speaks `--output json`, and errors come back with stable codes and concrete next steps. It works the same from your terminal, from CI, and from a coding agent's shell.

Docs live at [shed.codes](https://shed.codes).

## Installation

```sh
# YOLO — CLI and the agent skill
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install.sh | sh

# CLI only
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-cli.sh | sh

# Skill only (Claude Code, Cursor, Codex, …)
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh

# npm
npm install -g @shed-sh/shed          # global
npm install -D @shed-sh/shed          # pin in a project

# macOS and Linux
brew install shed-sh/tap/shed

# Arch
paru -S shed-bin
```

The YOLO script installs both halves. The binary lands in `~/.local/bin` (override with `--bin-dir <dir>`; pin with `--version <v>`). The skill script clones one canonical copy into `~/.shed/skills` and symlinks it into every agent on the machine; git is its only requirement, and rerunning it updates all of them at once.

macOS and Linux, Intel and Apple silicon. Update any time with `shed upgrade`. The same install scripts are served at [shed.codes](https://shed.codes).

## Start a project

```sh
cd your-app
shed init          # reads the project, writes SHED.yaml
shed deploy .      # builds and runs it locally (needs Docker)
```

The local run packages a deterministic archive, builds an image, and starts one container. On success Shed prints a URL and an instance id.

## Deploy to the cloud

```sh
shed login                # opens a browser; the one interactive command
shed deploy . --remote    # builds on Shed, hands back a live URL
```

Follow a long build with `shed status <deployment> --wait`. Fetch logs with `shed logs <deployment>`.

## Author the definition

Everything Shed builds comes from `SHED.yaml` (or its Starlark form, `SHED`). Nothing on your machine leaks into the build, so a deploy that works on your laptop behaves the same anywhere else.

```yaml
apiVersion: shed.run/v1alpha1
kind: Application
content:
  include: [package.json, package-lock.json, server.js]
build:
  image: node:24
  commands:
    - [npm, ci]
run:
  command: [node, server.js]
  port: 8080
```

While authoring, ask shed instead of guessing:

```sh
shed check                                    # every problem at once
shed schema                                   # the SHED file API
shed deploy . --dry-run --archive app.tar.gz  # inspect what would ship
```

## The agent skill

The skill in [`skills/shed`](skills/shed) teaches an agent to write a clean `SHED.yaml` or Starlark `SHED`, deploy, read shed's structured errors, and debug ignore rules. It follows the [Agent Skills](https://skills.sh) specification, so one installed copy serves every agent you use. Claude Code users can also install it as a plugin:

```
/plugin marketplace add shed-sh/cli
/plugin install shed@shed
```

For agents without the skill, [`llms-full.txt`](llms-full.txt) is the entire documentation set as one file of model context.

## Reference

Use `--help` on any command to explore flags and examples:

```sh
shed help
shed deploy --help
shed help --output json
```

`shed help --output json` comes from the binary in front of you; if it and any document ever disagree, the binary is right.

## What's in this repository

| Path | What it is |
|---|---|
| `cmd/shed` | The CLI entry point. |
| `internal/` | Packaging, build, deploy, auth, and the SHED evaluators. |
| `internal/e2e` | Golden-path tests that spawn the real `shed` binary. |
| `skills/shed` | The agent skill and its references (partly generated; see below). |
| `install.sh`, `install-cli.sh`, `install-skills.sh` | The install scripts served at shed.codes and via GitHub raw. |
| `docs/` | Design and protocol documentation. |
| `third_party/railpack` | Vendored [Railpack](https://github.com/railwayapp/railpack) (its own MIT license), which powers project detection. |
| [Releases](https://github.com/shed-sh/cli/releases) | Binaries, installers, and checksums per version. |

## Developing

Go 1.26+, [Task](https://taskfile.dev), and Docker for local deploys.

```sh
task build            # build the CLI
task test             # unit, integration, and CLI e2e (no Docker)
task test-e2e-docker  # source-to-running-container, needs Docker
task check            # the full local CI suite
```

Parts of the documentation are generated from the CLI's own registries (`task generate`), so a reference can never describe a flag or builtin the binary lacks; CI fails when they drift. `AGENTS.md` carries the contributor guide.

## Security

Shed treats the project it deploys the way `make` treats a Makefile: the build steps the manifest declares will run, and they should be the only thing the project can make happen. Packaging refuses symlinks, the Starlark evaluator has no filesystem or network access, and archives are validated as they are extracted. Report vulnerabilities privately via [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE).
