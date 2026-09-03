# Shed

[![CI](https://github.com/shed-sh/cli/actions/workflows/ci.yml/badge.svg)](https://github.com/shed-sh/cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/shed-sh/cli?display_name=tag&sort=semver)](https://github.com/shed-sh/cli/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Deploy small software from your terminal. `shed deploy` builds your app on the Shed cloud and hands back a live URL — the CLI is the only thing you install. Docs at [shed.codes](https://shed.codes).

**Agent-first:** it never prompts (except `shed login`), every command speaks `--output json`, and errors carry stable codes and concrete next steps — the same from your terminal, from CI, or from a coding agent's shell.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install.sh | sh   # or: npx @shed-sh/shed
```

`npm install -g @shed-sh/shed` puts `shed` on PATH. The installer takes `--version <v>` and `--bin-dir <dir>`. Update with `shed upgrade`.

## Deploy

```sh
cd your-app
shed login       # opens a browser; the one interactive command
shed deploy .    # builds on Shed, hands back a live URL
```

Deploy works from one file, `SHED.hcl`. If it is there, Shed uses it as written; if it is not, Shed reads your project — Node.js, Next.js, or a Go module — writes one, and carries on. `shed init` writes it without deploying, `--dry-run` writes nothing at all.

Follow a long build with `shed status <deployment> --wait` and read its output with `shed logs <deployment>`. `shed deploy . --local` builds and runs it on your own machine instead — needs Docker, and `shed stop <instance>` stops it.

## The definition

One `application` block: what to ship, how to build it, how to run it. Only what it names is sent, so a deploy behaves the same from your laptop, from CI, or from a teammate's machine.

```hcl
application "my-app" {
  content {
    include = ["package.json", "package-lock.json", "server.js"]
  }

  build {
    image    = "node:24"
    commands = [["npm", "ci"]]
  }

  run {
    command = ["node", "server.js"]
    port    = 8080
  }
}
```

`shed check` reports every problem in it at once, and `shed schema` prints the full API. `shed init --format shed` writes a Starlark `SHED` instead, for people who would rather write a program than a document.

## For coding agents

The [skill](skills/shed) teaches an agent to write a definition, deploy, and read Shed's errors. It follows the [Agent Skills](https://skills.sh) specification.

```sh
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh                  # this machine
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh -s -- --local    # this project
```

Or `npx @shed-sh/skills`, or in Claude Code `/plugin marketplace add shed-sh/cli` then `/plugin install shed@shed`. For an agent without the skill, [`llms-full.txt`](llms-full.txt) is the whole documentation set in one file.

## Anything else

`shed help`, `shed <command> --help`, and `shed help --output json` come from the binary in front of you. If a document ever disagrees with them, the binary is right.

**Contributing** — Go 1.26+, [Task](https://taskfile.dev), and Docker for `--local` tests; `task check` runs the full CI suite. [`AGENTS.md`](AGENTS.md) is the contributor guide.

**Security** — Shed runs the build steps your definition declares, the way `make` runs a Makefile, and nothing else. Report vulnerabilities privately via [SECURITY.md](SECURITY.md).

**License** — [MIT](LICENSE).
