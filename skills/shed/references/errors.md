# shed error codes

Every failure in `--output json` mode has this envelope:

```json
{"type":"result","outcome":"failed",
 "failure":{
   "code":"...",
   "message":"...",
   "recoverable":false,
   "operation":"..."
 }}
```

- `code` is **stable**. Match against it, not `message`.
- `message` is prose and may change between releases.
- `recoverable` is `true` for HTTP 408/429/5xx on remote-api errors — retry with backoff.
- `operation` names the pipeline stage where the failure happened.

## Codes

### Init / detection

| Code | Meaning | Fix |
|---|---|---|
| `detection_failed` | Railpack could not identify the project type. | Add a lockfile (`package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `bun.lock`) or `go.mod`. Or hand-author `SHED.yaml`. |
| `unsupported_project` | Railpack identified the type, but shed does not autogenerate for it (today: Python, Ruby, Bun, static, Deno, Elixir, PHP, Rust, shell, Go workspaces). | Hand-author `SHED.yaml`. The executor is provider-neutral. |
| `missing_package_json` | Node detection ran but `package.json` is missing. | Add one, or hand-author. |
| `missing_lockfile` | Node project without a supported lockfile. | Commit `package-lock.json` / `npm-shrinkwrap.json` / `yarn.lock` / `pnpm-lock.yaml` / `bun.lock(b)`. |
| `missing_go_mod` | Go project without a root `go.mod` (workspaces-only). | Add a root `go.mod` or hand-author. |
| `no_build_command` | The plan produced no build step (rare). | Add a `build.commands` entry in hand-authored `SHED.yaml`. |

### Invocation

| Code | Meaning | Fix |
|---|---|---|
| `not_a_directory` | Path arg is not a directory. | Point `shed deploy` at a directory. |
| `directory_not_found` | Path arg does not exist. | Fix the path. |
| `unknown_command` | Not a known subcommand. | See the "did you mean" hint or run `shed help`. |

### Upgrade

| Code | Meaning | Fix |
|---|---|---|
| `upgrade_unsupported_install` | The binary is a source build, or belongs to a package manager (brew, npm, pacman). | Use the upgrade command the diagnostic names, or rebuild from source. |
| `release_unavailable` | The release host was unreachable, or the requested version is not a published release. | Check the network; pin an existing version with `shed upgrade --version <v>`. |
| `upgrade_failed` | The release installer did not finish, or the installed binary reports the wrong version. | The current binary is untouched; read the installer output. |

### Runtime / remote

| Code | Meaning | Fix |
|---|---|---|
| `operation_failed` | Generic wrapper for a downstream failure. | Read `message` and any `cause`. |
| `remote_api_error` | Failure on the remote HTTP API. `recoverable: true` for 408/429/5xx. | Retry (with backoff) if recoverable; otherwise inspect message. |

## Human rendering

The same errors render in a terminal-aware layout:

```
Error: could not detect a supported project

  code    detection_failed
  path    /path/to/app

Next steps:
  • Add a lockfile (package-lock.json, pnpm-lock.yaml, or yarn.lock)
  • Or hand-author SHED.yaml — the executor is provider-neutral
```

- Bold summary line.
- Aligned `label  value` fact block.
- `Next steps:` bulleted hints.
- Wrapped to terminal width, capped at 96 cols.
- No color under `NO_COLOR`, `TERM=dumb`, or when stdout is not a tty.
