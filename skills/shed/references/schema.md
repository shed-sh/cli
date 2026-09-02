# `SHED.yaml` full schema

Unknown top-level fields are rejected — decoding is strict. Once `SHED.yaml` exists, shed loads it and never regenerates or repairs it; delete the file to redetect.

## Header (always required)

```yaml
apiVersion: shed.run/v1alpha1
kind: Application
```

Both values are matched exactly.

## `metadata` (optional)

```yaml
metadata:
  name: my-app     # DNS label, ≤30 chars: ^[a-z0-9](?:[a-z0-9-]{0,28}[a-z0-9])?$
```

If omitted, the directory basename is sanitized to a DNS label. The 30-char cap mirrors a server-side budget — don't raise it.

## `content` (required)

```yaml
content:
  include:
    - package.json
    - src
    - public/logo.png
```

Rules:
- Non-empty list.
- Each entry is a normalized relative path.
- Rejected: empty string, `.`, `..`, absolute paths, backslash, NUL byte.
- No overlap: `a` and `a/b` in the same list is a validation error.
- Order does not matter; the archive is deterministic.

**Structural excludes always run** and cannot be bypassed by including them:

| Kind | Names |
|---|---|
| Directories | `.shed`, `.git`, `.next`, `.turbo`, `coverage`, `dist`, `node_modules`, `__pycache__`, `.venv`, `target` |
| Files | `.DS_Store`, `SHED`, `SHED.yaml`, `.shed-source.json`, `.npmrc`, `.pypirc`, `credentials.json` |
| Globs | `.env*`, `*.pem`, `*.key`, `*.log` |

`.gitignore` and `.shedignore` are also honored. Any pattern containing `/` is anchored to the project root; patterns with no `/` match a path segment anywhere.

## `build` (required)

```yaml
build:
  image: node:24
  commands:
    - [npm, ci]
    - [npm, run, build]
```

- `image`: single-line Docker image reference; any registry.
- `commands`: optional; each is an argv array — no shell. Empty arg strings and NUL bytes are rejected. Characters `;` `|` `&` `<` `>` `$` and backtick are refused (they imply a shell you don't have). To use a shell, spell it: `[sh, -lc, "a && b"]`.

## `run` (required)

```yaml
run:
  command: [node, dist/server.js]
  port: 8080
  workingDirectory: /app
  user: "1000:1000"
  environment:
    LOG_LEVEL: info
    DATABASE_URL: postgres://…
  stopSignal: SIGTERM
```

- `command`: non-empty argv (same rules as build commands).
- `port`: integer, 1..65535. **Assumed 8080** by autogeneration unless the Railpack plan set `PORT`; the app must actually listen on that port (or on `$PORT`) for the readiness probe to pass.
- `workingDirectory`, `user`, `stopSignal`: optional passthroughs.
- `environment`: `map[string]string`; keys are non-empty, no `=`, no NUL. Values are passed through unchanged.

## `base` (optional — remote-builder projection)

Emitted only for the currently trusted catalog (today: Node 24 + pnpm). Users typically do not write this by hand.

```yaml
base: node-24-pnpm
```

## `parts` (optional — remote-builder projection)

```yaml
parts:
  app:
    plugin: node          # or nextjs
    source: "."
    dependencies:
      manager: pnpm       # npm | yarn | pnpm | bun
      inputs:
        - package.json
        - pnpm-lock.yaml
    stage:
      - src
      - public
    prime:
      - src
```

Auto-emitted by `shed init` for Node projects that match the trusted catalog. Hand-authoring is possible but rarely needed.

## `apps` (optional — remote-builder projection)

```yaml
apps:
  web:
    command: [node]
    args: [dist/server.js]
    working-directory: /app
    user: "1000:1000"
    environment:
      PORT: "8080"
    ports:
      - "8080/tcp"
    stop-signal: SIGTERM
```

A hand-authored `SHED.yaml` describes exactly one workload, via `run:`.

## `static` (not supported)

A `static:` workload with `directory`, `index`, and `fallback` is not accepted — validation rejects it. Serve static files with a real server in `run.command` instead.

## Minimal working examples

Node/Express:

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

Go (matches the shed repo's own SHED.yaml):

```yaml
apiVersion: shed.run/v1alpha1
kind: Application
metadata:
  name: shed
content:
  include: [cmd, internal, go.mod, go.sum]
build:
  image: golang:1.26
  commands:
    - [go, build, -o, out, ./cmd/shed]
run:
  command: [./out]
  port: 8080
  environment:
    PORT: "8080"
  stopSignal: SIGTERM
```

Python (hand-authored — outside autogeneration scope):

```yaml
apiVersion: shed.run/v1alpha1
kind: Application
content:
  include: [app.py, requirements.txt]
build:
  image: python:3.12-slim
  commands:
    - [pip, install, --no-cache-dir, -r, requirements.txt]
run:
  command: [python, app.py]
  port: 8080
  environment:
    PORT: "8080"
```
