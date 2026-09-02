# shed

The `shed` CLI and its agent skill.

Shed packages a small app into a deterministic, content-addressed archive, then builds and runs it. This repository is where you install it from, and where the skill that teaches coding agents to drive it lives.

## Install

There are two pieces: the **`shed` CLI** (packages, builds, and deploys your app) and the **agent skill** (teaches your coding agents to drive it). Every cell below works on its own — pick your column.

|  | curl | npm | bun | brew | paru (AUR) |
|---|---|---|---|---|---|
| **Both** | `curl -fsSL shed.codes/install.sh \| sh` | `npm i -g @shed-sh/shed` + the skill script | `bun add -g @shed-sh/shed` + the skill script | — | — |
| **CLI only** | `curl -fsSL shed.codes/install-cli.sh \| sh` | `npm i -g @shed-sh/shed` | `bun add -g @shed-sh/shed` | `brew install shed-sh/tap/shed` | `paru -S shed-bin` |
| **Skill only** | `curl -fsSL shed.codes/install-skills.sh \| sh` | — | — | — | — |

Each install path is its own script — no mode flags. `shed.codes/install.sh` is an alias of `install-all.sh`.

The skill installs one way everywhere: `install-skills.sh` clones this repository into `~/.shed/skills` and symlinks the skill into each coding agent on the machine — git is its only requirement, and rerunning it updates every agent at once. Brew and the AUR carry only the CLI; pair them with the skill script when you want both.

With the curl scripts, the binary lands in `~/.local/bin` (override with `--bin-dir <dir>`); `--version <v>` pins a release. Run any script with `--help` for the rest.

## The skill

The skill teaches an agent how to use shed: writing `SHED.yaml` or the Starlark `SHED` file, packaging and deploying, reading shed's structured errors, and debugging ignore rules.

It follows the [Agent Skills](https://skills.sh) specification, so one copy is symlinked into every supported agent — Claude Code, Cursor, Codex, and the rest. Claude Code users can also install it as a plugin:

```
/plugin marketplace add shed-sh/cli
/plugin install shed@shed
```

## What the skill knows

- `shed init` autogenerates only for Node.js/Next.js and Go, so anything else needs a hand-authored `SHED.yaml`
- build and run commands are argv arrays with no shell, and how to spell one when you need it
- which files are always excluded from the bundle regardless of `content.include`
- how to read the `--output json` failure envelope, and which codes are worth branching on

| Path | What it is |
|---|---|
| `install-cli.sh` | Installs the CLI. |
| `install-skills.sh` | Installs the skill. |
| `install-all.sh` | Runs both. `install.sh` is its alias. |
| `skills/shed/SKILL.md` | The skill itself. |
| `skills/shed/references/commands.md` | Every command and flag. Generated. |
| `skills/shed/references/starlark.md` | The Starlark `SHED` language. Generated. |
| `skills/shed/references/schema.md` | The full `SHED.yaml` schema. |
| `skills/shed/references/errors.md` | Error codes and how to resolve them. |

## A note on edits

`skills/` is generated from the CLI's own command registry and mirrored here on each release, so `references/commands.md` cannot describe a flag the released binary lacks. Pull requests against generated files cannot be merged — please [open an issue](https://github.com/shed-sh/cli/issues) instead.

`shed help --output json` comes from the binary in front of you; this skill was generated when its release was cut. If they ever disagree, the binary is right.

## License

MIT.
