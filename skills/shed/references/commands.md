<!-- Generated from the shed CLI's own command registry. Do not edit by hand. -->

# `shed` CLI — full command reference

Every command, flag, and default below is generated from the registry the CLI
itself parses with, so this file cannot describe a flag the binary does not have.
Run `shed help --output json` for the same contract as machine-readable JSON.

## Global conventions

- `--output` accepts `human`, `json`, and `ndjson`, though the accepted set and
  the default differ per command — see each command below.
- In JSON and NDJSON modes, human progress goes to stderr so stdout stays
  parseable.
- Every command is non-interactive except `shed login`, which opens a browser
  and waits for it. With `--no-browser` it reads a verification code from stdin
  instead.
- `shed <directory>` is shorthand for `shed deploy <directory>`.
- A mistyped command gets a "did you mean" suggestion.
- Exit status is 1 on any error. The failure envelope in JSON is:

```json
{"type":"result","outcome":"failed",
 "failure":{"code":"...","message":"...","recoverable":false,"operation":"..."}}
```

`code` is stable; `message` is prose and may be reworded. Branch on the code.

## Commands at a glance

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

## Your project

### `shed deploy [directory]`

Build the project and run it locally, or send it up with --remote.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--dry-run` | bool | — | Inspect and package, without building or running |
| `--mock` | bool | — | Package and stop, for when Docker is unavailable |
| `--remote` | bool | — | Send the packaged bundle to Shed cloud |
| `--archive <path>` | string | — | Keep the generated .tar.gz at this path |
| `--output human\|json\|ndjson` | string | `human` | Choose the output format |
| `--project <name>` | string | — | Override the remote project name |
| `--request-id <id>` | string | — | Repeat a deploy without creating a second deployment |
| `--wait` | bool | — | Wait for the remote deployment to finish |
| `--detach` | bool | — | Return as soon as the cloud accepts the bundle |
| `--wait-timeout <duration>` | duration | `30s` | How long to follow before detaching |
| `--non-interactive` | bool | — | Accepted for compatibility; Shed never prompts |

Mutually exclusive: `--remote` with `--mock`; `--detach` with `--wait`; `--detach` with `--wait-timeout`.

Wrong argument count returns `usage: shed deploy [directory]`.

```
shed deploy                                    # Build and run this project
shed deploy . --dry-run --archive app.tar.gz   # Package it and look inside
shed deploy . --remote --output json           # Send it up, one JSON result
```

### `shed init [directory]`

Look at the project and write SHED.yaml, or SHED with --format shed.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--format yaml\|shed` | string | `yaml` | Definition format to write |
| `--output human\|json` | string | `human` | Choose the output format |

Wrong argument count returns `usage: shed init [directory] [--format yaml|shed] [--output human|json]`.

### `shed check [directory]`

Evaluate the definition and report every problem in it.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--output human\|json` | string | `human` | Choose the output format |

Wrong argument count returns `usage: shed check [directory] [--output human|json]`.

```
shed check --output json   # Every diagnostic at once, while authoring
```

### `shed schema`

Print the SHED file API.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--output human\|json` | string | `human` | Choose the output format |

Wrong argument count returns `usage: shed schema [--output human|json]`.

## Running deployments

### `shed status <deployment>`

Show, or wait for, a deployment's state.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--wait` | bool | — | Wait for a terminal deployment state |
| `--wait-timeout <duration>` | duration | `30s` | How long to follow before detaching |
| `--output human\|json\|ndjson` | string | `json` | Choose the output format |

Wrong argument count returns `usage: shed status <deployment-id> [--wait] [--wait-timeout D]`.

```
shed status <deployment> --wait --output ndjson   # Follow it to a final state
```

### `shed logs <deployment>`

Show build and runtime logs.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--stage system\|build\|runtime\|all` | string | `all` | Limit records to one pipeline stage |
| `--follow` | bool | — | Keep streaming new records |
| `--cursor <cursor>` | string | — | Resume after a stream cursor |
| `--output human\|json\|ndjson` | string | `human` | Choose the output format |

Wrong argument count returns `usage: shed logs <deployment-id> [--stage system|build|runtime|all] [--follow] [--cursor C]`.

### `shed open <url>`

Open a deployment in your browser.

Wrong argument count returns `usage: shed open <url>`.

### `shed stop <instance>`

Stop a local instance.

Wrong argument count returns `usage: shed stop <instance-id>`.

### `shed destroy <instance>`

Stop a local instance and forget it.

Wrong argument count returns `usage: shed destroy <instance-id>`.

### `shed cancel <deployment>`

Ask the cloud to cancel a deployment.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--output human\|json` | string | `json` | Choose the output format |

Wrong argument count returns `usage: shed cancel <deployment-id>`.

## Sharing

### `shed share <deployment> <email>`

Give someone access.

Wrong argument count returns `usage: shed share <deployment-id> <email>`.

### `shed revoke <deployment> <email>`

Take someone's access away.

Wrong argument count returns `usage: shed revoke <deployment-id> <email>`.

## Your account

### `shed login`

Sign in to Shed.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--token <pat>` | string | — | Sign in with a personal access token (skips the browser flow) |
| `--no-browser` | bool | — | Print the login URL instead of opening a browser |
| `--name <label>` | string | — | Name shown for this CLI token |

Wrong argument count returns `usage: shed login [--token <pat> | --no-browser] [--name <label>]`.

### `shed logout`

Sign out and revoke this machine's token.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--local` | bool | — | Remove local credentials without revoking the token |

Wrong argument count returns `usage: shed logout [--local]`.

### `shed whoami`

Show who you are signed in as.

Wrong argument count returns `usage: shed whoami`.

## About

### `shed upgrade`

Replace this binary with a newer released one.

Also spelled `update`.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--version <v>` | string | — | Switch to exactly this release instead of the latest |
| `--check` | bool | — | Report whether an upgrade is available, without installing |
| `--output human\|json` | string | `human` | Choose the output format |

Wrong argument count returns `usage: shed upgrade [--version <v>] [--check]`.

```
shed upgrade                         # Move to the latest release
shed upgrade --check --output json   # Just ask, one JSON result
```

### `shed version`

Print the CLI version.

Also spelled `--version` or `-v`.

### `shed help [command]`

Show this help, or one command's options.

Also spelled `--help` or `-h`.

| Flag | Type | Default | Purpose |
|---|---|---|---|
| `--output human\|json` | string | `human` | Choose the output format |

Wrong argument count returns `usage: shed help [command] [--output human|json]`.

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `SHED_TOKEN` | — | Authenticate with this token without storing it |
| `NO_COLOR` | — | Disable color and bold in human output |
