# `SHED.hcl` full schema

`SHED.hcl` is one `application` block. The block type is the document kind and the label is the application's name — there is no version or kind header to write. Decoding is strict: an attribute or block the schema does not declare is an error, so typos fail loudly. Once `SHED.hcl` exists, shed loads it and never regenerates or repairs it; delete the file to redetect.

## `application "<name>"` (required, exactly one)

```hcl
application "my-app" {
  content { … }
  build   { … }
  run     { … }
}
```

- The label is the name: a DNS label of at most 30 characters, `^[a-z0-9](?:[a-z0-9-]{0,28}[a-z0-9])?$`. `shed init` derives it from the directory name. The 30-char cap mirrors a server-side budget — don't raise it.
- `content`, `build`, and `run` are each required exactly once.

## `content` (required)

```hcl
content {
  include = ["package.json", "src", "public/logo.png"]
}
```

Rules:
- `include` is a non-empty list of strings.
- Each entry is a normalized relative path.
- Rejected: empty string, `.`, `..`, absolute paths, backslash, NUL byte.
- No overlap: `a` and `a/b` in the same list is a validation error.
- Order does not matter; the archive is deterministic.

**Structural excludes always run** and cannot be bypassed by including them:

| Kind | Names |
|---|---|
| Directories | `.shed`, `.git`, `.next`, `.turbo`, `coverage`, `dist`, `node_modules`, `__pycache__`, `.venv`, `target` |
| Files | `.DS_Store`, `SHED`, `SHED.hcl`, `.shed-source.json`, `.npmrc`, `.pypirc`, `credentials.json` |
| Globs | `.env*`, `*.pem`, `*.key`, `*.log` |

`.gitignore` and `.shedignore` are also honored. Any pattern containing `/` is anchored to the project root; patterns with no `/` match a path segment anywhere.

## `build` (required)

```hcl
build {
  image = "node:24"
  commands = [
    ["npm", "ci"],
    ["npm", "run", "build"],
  ]
}
```

- `image`: single-line Docker image reference; any registry.
- `commands`: optional; a list of argv lists — no shell. Empty arg strings and NUL bytes are rejected. Characters `;` `|` `&` `<` `>` `$` and backtick are refused (they imply a shell you don't have). To use a shell, spell it: `["sh", "-lc", "a && b"]`.

## `run` (required)

```hcl
run {
  command           = ["node", "dist/server.js"]
  port              = 8080
  working_directory = "/app"
  user              = "1000:1000"
  environment = {
    LOG_LEVEL    = "info"
    DATABASE_URL = "postgres://…"
  }
  stop_signal = "SIGTERM"
}
```

- `command`: non-empty argv list (same rules as build commands).
- `port`: integer, 1..65535. **Assumed 8080** by autogeneration unless the Railpack plan set `PORT`; the app must actually listen on that port (or on `$PORT`) for the readiness probe to pass.
- `working_directory`, `user`, `stop_signal`: optional passthroughs.
- `environment`: an object of string values; keys are non-empty, no `=`, no NUL. Values are passed through unchanged. Quote numbers: `PORT = "8080"`.

## `base` (optional — remote-builder projection)

Emitted only for the currently trusted catalog (today: Node 24 + pnpm). Users typically do not write this by hand.

```hcl
base = "node-24"
```

## `part "<name>"` (optional — remote-builder projection)

```hcl
part "app" {
  plugin = "node"          # or "nextjs"
  source = "."
  stage  = ["src", "public"]
  prime  = ["src"]

  dependencies {
    manager = "pnpm"       # npm | yarn | pnpm | bun
    inputs  = ["package.json", "pnpm-lock.yaml"]
  }
}
```

Auto-emitted by `shed init` for Node projects that match the trusted catalog. Hand-authoring is possible but rarely needed. Each `part` label must be unique.

## `app "<name>"` (optional — remote-builder projection)

```hcl
app "web" {
  command           = ["node"]
  args              = ["dist/server.js"]
  working_directory = "/app"
  user              = "1000:1000"
  environment = {
    PORT = "8080"
  }
  ports       = ["8080/tcp"]
  stop_signal = "SIGTERM"
}
```

A hand-authored `SHED.hcl` describes exactly one workload, via `run`.

## `static` (not supported)

A `static` block with `directory`, `index`, and `fallback` is not accepted — decoding rejects it as an unknown block. Serve static files with a real server in `run.command` instead.

## Minimal working examples

Node/Express:

```hcl
application "hello-express" {
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

Go (matches the shed repo's own SHED.hcl):

```hcl
application "shed" {
  content {
    include = ["cmd", "internal", "go.mod", "go.sum"]
  }

  build {
    image    = "golang:1.26"
    commands = [["go", "build", "-o", "out", "./cmd/shed"]]
  }

  run {
    command = ["./out"]
    port    = 8080
    environment = {
      PORT = "8080"
    }
    stop_signal = "SIGTERM"
  }
}
```

Python (hand-authored — outside autogeneration scope):

```hcl
application "hello-python" {
  content {
    include = ["app.py", "requirements.txt"]
  }

  build {
    image    = "python:3.12-slim"
    commands = [["pip", "install", "--no-cache-dir", "-r", "requirements.txt"]]
  }

  run {
    command = ["python", "app.py"]
    port    = 8080
    environment = {
      PORT = "8080"
    }
  }
}
```
