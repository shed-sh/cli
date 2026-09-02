# Shed

[![Release](https://img.shields.io/github/v/release/shed-sh/cli?display_name=tag&sort=semver)](https://github.com/shed-sh/cli/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Deploy small software. One file describes your app — `shed deploy` packages it into a deterministic archive, builds it, and runs it: locally in Docker, or on the Shed cloud with a live URL.

Shed is **agent-first**: it never prompts (except `shed login`), every command speaks `--output json`, and errors come back with stable codes and concrete next steps — so it works the same from your terminal, your CI, and your coding agent's shell.

Docs live at [shed.codes](https://shed.codes).

## Install

There are two pieces: the **`shed` CLI** and the **agent skill** that teaches coding agents (Claude Code, Cursor, Codex) to drive it. Every cell works on its own — pick your column.

|  | curl | npm | bun | brew | paru (AUR) |
|---|---|---|---|---|---|
| **Both** | `curl -fsSL shed.codes/install.sh \| sh` | `npm i -g @shed-sh/shed` + the skill script | `bun add -g @shed-sh/shed` + the skill script | — | — |
| **CLI only** | `curl -fsSL shed.codes/install-cli.sh \| sh` | `npm i -g @shed-sh/shed` | `bun add -g @shed-sh/shed` | `brew install shed-sh/tap/shed` | `paru -S shed-bin` |
| **Skill only** | `curl -fsSL shed.codes/install-skills.sh \| sh` | — | — | — | — |

The binary lands in `~/.local/bin` (override with `--bin-dir <dir>`; pin with `--version <v>`). The skill script clones one canonical copy into `~/.shed/skills` and symlinks it into every agent on the machine — git is its only requirement, and rerunning it updates all agents at once. In a project, `npm i -D @shed-sh/shed` pins the CLI per repository.

macOS and Linux, Intel and Apple silicon. Update any time with `shed upgrade`.

## Quick start

```sh
cd your-app
shed init          # reads the project, writes SHED.yaml
shed deploy .      # builds and runs it locally (needs Docker)
```

For the cloud:

```sh
shed login                # opens a browser; the one interactive command
shed deploy . --remote    # builds on Shed, hands back a live URL
```

Everything Shed builds comes from `SHED.yaml` (or its Starlark form, `SHED`) — nothing on your machine leaks into the build, which is why the same command works from a laptop, CI, and an agent sandbox.

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

While authoring, ask shed instead of guessing: `shed check` reports every problem at once, `shed schema` prints the SHED file API.

## The agent skill

The skill in [`skills/shed`](skills/shed) teaches an agent to write a clean `SHED.yaml` or Starlark `SHED`, deploy, read shed's structured errors, and debug ignore rules. It follows the [Agent Skills](https://skills.sh) specification — one copy, symlinked into every agent you use. Claude Code users can also install it as a plugin:

```
/plugin marketplace add shed-sh/cli
/plugin install shed@shed
```

For agents without the skill, [`llms-full.txt`](llms-full.txt) is the entire documentation set as one file of model context.

## What's in this repository

| Path | What it is |
|---|---|
| `cmd/shed` | The CLI entry point. |
| `internal/` | Packaging, build, deploy, auth, and the SHED evaluators. |
| `skills/shed` | The agent skill and its references (partly generated — see below). |
| `install-*.sh` | The install scripts served at shed.codes. |
| `docs/` | Design and protocol documentation. |
| [Releases](https://github.com/shed-sh/cli/releases) | Binaries, installers, and checksums per version. |

## Development

Go 1.26+, [Task](https://taskfile.dev), and Docker for local deploys.

```sh
task build      # build the CLI
task test       # run all tests
task check      # the full local CI suite
```

Parts of the documentation are generated from the CLI's own registries (`task generate`), so a reference can never describe a flag or builtin the binary lacks — CI fails when they drift. `AGENTS.md` carries the contributor guide.

`shed help --output json` comes from the binary in front of you; if it and any document ever disagree, the binary is right.

## License

MIT.
