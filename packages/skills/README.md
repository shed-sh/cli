# @shed-sh/skills

Installs the shed agent skill for Claude Code, Cursor, and Codex.

This is not the CLI. The CLI is [`@shed-sh/shed`](https://www.npmjs.com/package/@shed-sh/shed):

```sh
npx @shed-sh/shed
```

## Install the skill

```sh
# this machine
npx @shed-sh/skills

# this project
npx @shed-sh/skills --local
```

`--global` (the default) copies the skill into `~/.shed/skills` and symlinks it into every coding agent on the machine. `--local` copies it into `.claude/skills/shed` so the skill travels with the repo.

Without Node, use the shell installer:

```sh
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh
curl -fsSL https://raw.githubusercontent.com/shed-sh/cli/main/install-skills.sh | sh -s -- --local
```
