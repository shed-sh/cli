# Shed

[![CI](https://github.com/shed-sh/cli/actions/workflows/ci.yml/badge.svg)](https://github.com/shed-sh/cli/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/shed-sh/cli?display_name=tag&sort=semver)](https://github.com/shed-sh/cli/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

Deploy small software from your terminal. `shed deploy` builds your app on the Shed cloud and hands back a live URL. The CLI is the only thing you install.

Shed is **agent-first**: it never prompts (except `shed login`), every command speaks `--output json`, and errors come back with stable codes and concrete next steps. It works the same from your terminal, from CI, and from a coding agent's shell.

Docs live at [shed.codes](https://shed.codes).

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install.sh | sh
npx @shed-sh/shed
```

`npm install -g @shed-sh/shed` puts `shed` on PATH. The installer takes `--bin-dir <dir>` and `--version <v>`. Update with `shed upgrade`.

## Deploy

```sh
cd your-app
shed login         # opens a browser; the one interactive command
shed deploy .      # builds on Shed, hands back a live URL
```

Deploy works from one file, `SHED.hcl`. If it is there, Shed uses it as written. If it is not, Shed looks at your project — Node.js, Next.js, or a Go module — writes one, and carries on, so you have something to read and edit. `shed init` writes it without deploying; `shed deploy --dry-run` writes nothing at all.

Follow a long build with `shed status <deployment> --wait`, and read its output with `shed logs <deployment>`.

## Run it here instead

```sh
shed deploy . --local
```

Optional, and needs Docker: this builds an image on your machine and starts one container. `shed stop <instance>` stops it.

## The definition

`SHED.hcl` is one `application` block — what to ship, how to build it, how to run it. Only what it names is sent, so a deploy behaves the same from your laptop, from CI, or from a teammate's machine.

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

`shed check` reports every problem in it at once, and `shed schema` prints the full API. There is also a Starlark form, `SHED`, for people who would rather write a program than a document: `shed init --format shed`.

## For coding agents

The [skill](skills/shed) teaches an agent to write a definition, deploy, and read Shed's errors. It follows the [Agent Skills](https://skills.sh) specification.

```sh
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh            # this machine
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh -s -- --local  # this project
npx @shed-sh/skills
```

Claude Code can install it as a plugin instead:

```
/plugin marketplace add shed-sh/cli
/plugin install shed@shed
```

For an agent without the skill, [`llms-full.txt`](llms-full.txt) is the whole documentation set in one file.

## Help

`shed help`, `shed <command> --help`, and `shed help --output json` all come from the binary in front of you. If a document ever disagrees with them, the binary is right.

## Contributing

Go 1.26+, [Task](https://taskfile.dev), and Docker for `--local` deploys and their tests. `task check` runs the full CI suite. [`AGENTS.md`](AGENTS.md) is the contributor guide.

## Security

Shed runs the build steps your definition declares, the way `make` runs a Makefile — and nothing else. Report vulnerabilities privately via [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE).
