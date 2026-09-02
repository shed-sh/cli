# Shed CLI and Application Definition

## 1. Purpose

Shed is an execution and lifecycle platform for software created by coding
agents.

The primary workflow is:

```text
User request
    ↓
Coding agent
    ↓
Source code
    ↓
SHED application definition
    ↓
Shed CLI
    ↓
Content-addressed build
    ↓
Isolated running application
```

The CLI is primarily a machine interface used by coding agents. Human-friendly
output is secondary to deterministic execution, structured results, safe
retries, and clear lifecycle management.

## Implementation status and roadmap

This document describes the target product contract. The implementation now
reaches deterministic definition generation, source packaging, a local Docker
build/runtime boundary, and a complete client-side remote execution workflow.
The corresponding Shed cloud backend does **not** exist yet.

### Implemented

- CLI authentication and an `internal/execution` orchestration boundary for
  immutable bundle upload, idempotent deployment submission, resumable event/log
  streaming, status reconciliation, and cancellation. `shed --remote` uses this
  client; no matching backend is implemented in this repository.
- Railpack `v0.35.0` provider orchestration vendored as a nested Go module,
  excluding BuildKit lowering, image exporters, and CLI presentation code.
- Provider precedence and Procfile post-detection behavior preserved.
- Application filesystem access confined to `os.Root`, including path
  validation, symlink boundaries, rooted nested applications, and deterministic
  glob ordering.
- Evidence recording for file reads, parsing, stats, globbing, cache hits,
  digests, error categories, and phase grouping.
- Offline generation-context and resolver seams so provider tests do not invoke
  or download `mise`.
- E2E inspection against pinned Next.js and Node.js Git submodules. The suite
  currently generates a Railpack plan and validates provider selection,
  toolchains, start commands, and evidence.
- One deterministic `shed.run/v1alpha1` `SHED.yaml` contract containing the
  content closure, build image and commands, and runtime contract.
- Railpack-to-SHED lowering for Node and for Go modules. A Go project compiles
  and runs out of the same pinned `golang` image, because Railpack builds one
  self-contained binary and starts it directly.
- `shed init` scaffolding through Railpack. Existing definitions are loaded and
  validated without redetection.
- Canonical source manifests, separate content/archive digests, and a
  deterministic `tar.gz` transport.
- `shed deploy`, and its `shed <directory>` shorthand, build a local Docker image,
  start one stable per-project instance, and classify readiness. `--mock`
  preserves the offline archive/uploader path. Bare `shed` inspects nothing and
  prints the overview instead.
- Daemon-backed E2E tests verify source packaging, Docker build, serving code,
  stable reruns, source updates, and building after the original source tree has
  been deleted. CI runs these tests on Linux with Docker.
- Agent-safe remote commands support JSON and typed NDJSON, deterministic
  request IDs, a 30-second default wait followed by a resumable receipt,
  explicit detach/terminal waits, log-stage filtering, and no URL before ready.
- A Starlark `SHED` program (V1) with exactly three builtins — `build()`,
  `http_app()`, `glob()` — evaluated into the same `SHED.yaml` before
  packaging. `shed check` reports every diagnostic in one pass with stable
  codes and `SHED:line:col` positions; `shed schema` prints the authoring API;
  `shed init --format shed` writes a program whose evaluation round-trips.

### Not implemented yet

- The remote API is a fake-server-tested client contract only. The archive is
  not accepted by a real backend or passed to Werf yet.
- Static sites and SPAs do not yet have a first-class manifest/runtime target.
- Railpack detects more ecosystems than Shed can lower: automatic definition
  generation currently supports Node with npm, yarn, or pnpm, requires a
  lockfile, and rejects Bun and static variants.
- Generated definitions currently use deterministic conventions; hosted LLM
  generation is not implemented yet.
- Incremental missing-blob negotiation is not implemented yet.
- The local runtime is Docker-based; remote isolation, image retention, and
  hosted networking are not implemented.
- There is no artifact build cache, secret injection, capability approval, or
  provenance chain for the new application-definition path.

### Ordered next work

1. Add a first-class static workload to `SHED.yaml`, lower Railpack static/SPA
   detection into it, and verify plain-static and built-SPA archives locally.
2. Add compiled digest-pinned trusted images and keep Docker, Buildah, and Werf
   behind the builder interface.
3. Implement the backend half of the existing remote contract: upload
   registration/finalization, digest verification, immutable storage,
   request-ID idempotency, deployment snapshots, replayable NDJSON streams, and
   cancellation.
4. Replace the mock boundary with a remote worker that safely extracts the
   archive, validates the definition, invokes the sibling Werf builder, and
   pushes the resulting OCI image to a registry.
5. Run the produced image remotely with stable application identity, logs, and
   classified bind, early-exit, timeout, and HTTP failures.
6. Add stable ingress, then promote the currently explicit `--remote` path to
   plain `shed deploy` and return the verified URL.
7. Add a cloud E2E from fixture source through public HTTP response.
8. Add Railpack-to-SHED lowering and runtime E2E coverage for Python, Bun, Go
   workspaces, and additional retained providers.
9. Decide and secure public input contracts for prebuilt OCI images and SHED
   archives produced outside the CLI.
10. Add hosted LLM generation behind the generator boundary while preserving
    deterministic validation and content resolution.
11. Add plugin/MCP authentication, secrets, grants, approvals, TTL/suspend,
    rollback, caching, incremental CAS upload, and source-to-revision provenance.

Documentation is a definition-of-done requirement: every implementation
iteration must update the status/roadmap here or in the root README, include a
runnable example where behavior changed, and record the validation command.

### Current definition shape

The public definition is the only executable IR:

```yaml
apiVersion: shed.run/v1alpha1
kind: Application
metadata:
  name: example-app
content:
  include: [package.json, package-lock.json, server.js]
build:
  image: node:24
  commands:
    - [npm, ci]
run:
  command: [node, server.js]
  workingDirectory: /app
  user: "1000:1000"
  environment:
    PORT: "8080"
  port: 8080
  stopSignal: SIGTERM
```

The archive embeds this file, a canonical `.shed-source.json`, and the selected
source bytes. Builders receive only the archive. They verify its receipt,
extract it with traversal and link defenses, parse this definition, and execute
it without consulting the original project directory.

### Authored program shape (implemented V1)

A project may instead be defined by a `SHED` program — Starlark with three
predeclared builtins — that evaluates to the manifest above. The program is an
authoring format only: evaluation happens before packaging, the archive embeds
the evaluated `SHED.yaml`, and the `SHED` file itself is structurally excluded
from content.

```python
b = build(
    srcs = glob(["cmd/**", "go.mod", "go.sum"]),   # or a literal path list
    image = "golang:1.26",
    commands = [
        ["go", "mod", "download"],
        ["go", "build", "-o", "out", "./cmd/app"],
    ],
)

http_app(
    name = "hello-api",          # DNS label, at most 30 characters
    build = b,                   # passed by variable, never inline
    cmd = ["./out"],             # argv list, never a shell string
    port = 8080,
    env = {},                    # optional; PORT injected when absent
    working_directory = "/app",  # optional, these defaults
    user = "1000:1000",
    stop_signal = "SIGTERM",
)
```

The evaluator is kwargs-only, accepts one representation per field, allows
exactly one `http_app()`, rejects inline and unused `build()` values, and
resolves `glob()` and literal `srcs` entries against the collector universe —
post-ignore, post-structural-excludes — so secrets can never be selected.
Every violation is a positioned `SHED:line:col` diagnostic with a stable
snake_case code, and one evaluation reports all of them. `shed schema` prints
the full API. Not yet supported: `param()`, `worker()`, `static_site()`,
multiple apps, `detect()`, `load()`, build-time env; the remote `base/parts/
apps` projection stays generator-only.

Resolution precedence: a `SHED` program is evaluated when present; otherwise
`SHED.yaml` is parsed; otherwise Railpack detection scaffolds a definition.
Both files present is a `definition_conflict` error — two authoritative
definitions cannot decide one build.

### Planned static definition shape

Static output is a different workload kind, not an HTTP process with a guessed
server command. `static` and `run` are mutually exclusive:

```yaml
apiVersion: shed.run/v1alpha1
kind: Application
content:
  include: [package.json, package-lock.json, src, public]
build:
  image: node:24
  commands:
    - [npm, ci]
    - [npm, run, build]
static:
  directory: dist
  index: index.html
  fallback: index.html
```

`static.directory` is a normalized relative path in the post-build artifact.
`static.index` defaults to `index.html`. `static.fallback` is optional and, when
present, provides SPA history fallback; without it, missing paths return 404.
Shed selects and owns the trusted static server, MIME handling, HTTP port, and
readiness behavior. Static files remain part of the artifact digest, and any
change to the static contract changes the deployment revision digest.

## 2. Responsibility Boundary

The important boundary is:

> The agent is responsible for defining the application, its content, and its
> build requirements. Shed is responsible for validating, materializing,
> building, running, and governing that definition.

### Coding agent responsibilities

The coding agent is authoritative about the application it created.

It must define:

- which source files belong to the application;
- which files must be excluded;
- the programming language and runtime version;
- the required build tools;
- dependency manifests and lockfiles;
- build commands;
- expected build outputs;
- runtime processes;
- entry points;
- ports;
- service relationships;
- required environment variables;
- requested capabilities and grants;
- scheduled jobs and workers;
- whether persistent storage is required.

The agent already understands the code because it created or modified it. Shed
should not duplicate that work by independently attempting to infer the complete
application architecture.

### Shed responsibilities

Shed is authoritative about the execution environment.

It must:

- validate the application definition;
- resolve supported runtime and toolchain versions;
- calculate the content closure;
- canonicalize and hash application content;
- upload only missing content;
- execute reproducible builds;
- cache build results;
- provision an isolated runtime;
- assign identity and networking;
- enforce organization policies;
- request approval for sensitive capabilities;
- expose endpoints;
- report status, logs, health, and events;
- update, stop, suspend, and destroy applications;
- preserve provenance from source content to running revision.

## 3. The SHED File

The SHED file is not only a deployment manifest. It is an agent-generated
application definition containing:

1. The exact content included in the application.
2. The build environment required to transform that content.
3. The resulting executable processes.
4. The capabilities required by those processes.

It acts as an intermediate representation between coding agents and the Shed
runtime.

## 4. Content Definition

Content must be a first-class primitive. The content definition answers:

> Which exact bytes constitute this application version?

Example:

```python
content(
    name = "source",
    root = ".",
    include = [
        "src/**",
        "public/**",
        "package.json",
        "package-lock.json",
        "tsconfig.json",
    ],
    exclude = [
        ".git/**",
        "node_modules/**",
        "dist/**",
        ".env",
        "**/*.log",
    ],
)
```

The content declaration must support:

- root directory;
- include patterns;
- exclude patterns;
- executable file modes;
- symlink policy;
- path remapping;
- composition of multiple content targets;
- explicit generated inputs where necessary.

Secrets must never be included in content. Files such as `.env`, API keys,
local credentials, build caches, dependency caches, logs, temporary files, and
previous build outputs should be structurally excluded.

## 5. Canonical Content Addressing

Shed resolves a content declaration into a canonical file manifest.

Example:

```json
{
  "files": [
    {
      "path": "package.json",
      "digest": "sha256:...",
      "mode": "0644",
      "size": 1420
    },
    {
      "path": "src/server.ts",
      "digest": "sha256:...",
      "mode": "0644",
      "size": 5821
    }
  ]
}
```

The digest should include:

- normalized relative path;
- file content;
- file type;
- executable bit;
- symlink destination when preserved.

It should not include:

- modification timestamps;
- creation timestamps;
- local absolute paths;
- filesystem owner;
- host UID or GID;
- machine-specific metadata.

The resolved content tree receives a deterministic root digest:

```text
file digests
    ↓
directory digests
    ↓
content root digest
```

This enables:

- incremental uploads;
- cross-application deduplication;
- deterministic deployment identity;
- reproducible builds;
- exact revision comparison;
- build cache reuse;
- traceability from a running instance to exact source bytes.

## 6. Content Upload Protocol

The CLI should not blindly archive and upload the entire directory. The intended
protocol is:

```text
Resolve content declaration
    ↓
Calculate canonical manifest
    ↓
Calculate content root
    ↓
Send manifest to Shed
    ↓
Receive missing blob list
    ↓
Upload only missing blobs
    ↓
Commit immutable content object
```

CLI example:

```bash
shed content prepare --file SHED --output json
```

Possible result:

```json
{
  "content_digest": "sha256:root...",
  "file_count": 84,
  "total_size": 481203,
  "missing_blobs": [
    "sha256:blob-a...",
    "sha256:blob-b..."
  ]
}
```

The common deployment command may perform these steps automatically. Separate
content commands are primarily useful for debugging and advanced workflows.

## 7. Build Definition

Content alone is insufficient. Shed must also know the exact toolchain required
to transform the content into a runnable artifact.

The build definition must include:

- runtime family;
- runtime version;
- package manager;
- package-manager version where relevant;
- dependency lockfile;
- build commands;
- expected outputs;
- required system packages;
- build-time environment;
- target architecture where relevant.

Example:

```python
build(
    name = "web_build",
    content = ":source",
    runtime = node(
        version = "22.18.0",
    ),
    tools = [
        npm(version = "10.9.3"),
    ],
    install = [
        "npm",
        "ci",
    ],
    commands = [
        ["npm", "run", "build"],
    ],
    outputs = [
        "dist/**",
        "package.json",
        "package-lock.json",
    ],
)
```

The runtime version should not be represented as a vague value such as
`node:latest`. It should resolve to a precise toolchain identity:

```text
Node.js 22.18.0
npm 10.9.3
linux/amd64
base toolchain digest sha256:...
```

This identity becomes part of the build key.

## 8. Build Identity

A build result should be derived from:

```text
build key =
  hash(
    source content digest
    + normalized build definition
    + resolved toolchain digest
    + declared build-time inputs
  )
```

The result is another immutable content-addressed object:

```text
source content
    ↓ build
runtime artifact content
```

These identities are distinct:

- **Source content digest:** the exact application inputs produced by the agent.
- **Build artifact digest:** the exact filesystem or executable output produced
  by the build.
- **Deployment revision digest:** the runtime artifact plus runtime
  configuration.

Conceptually:

```text
source_digest
    ↓
build_digest
    ↓
artifact_digest
    ↓
revision_digest
```

## 9. Runtime Definition

The runtime definition describes how the built artifact is exposed. V1 has two
mutually exclusive workload kinds.

An HTTP process declares an argv command and port:

```yaml
run:
  command: [node, dist/server.js]
  workingDirectory: /app
  user: "1000:1000"
  environment:
    PORT: "3000"
  port: 3000
  stopSignal: SIGTERM
```

A static workload declares build output rather than inventing a runtime
command:

```yaml
static:
  directory: dist
  index: index.html
  fallback: index.html
```

Exactly one of `run` or `static` must be present. Static paths must be normalized
relative paths, must remain inside the built artifact, and must exist after the
build. Shed provides the static server and its port. The optional fallback is
returned only for paths that would otherwise be missing, which supports SPA
history routing without changing normal asset responses.

Workers, schedules, and multiple processes remain later schema extensions. They
must not be represented by overloading the V1 HTTP or static contracts.

## 10. Information Resolvable from the Codebase

Some application-definition fields can be scaffolded from conventional project
metadata.

For Node.js applications, Shed may examine:

- `package.json`;
- `package-lock.json`;
- `pnpm-lock.yaml`;
- `yarn.lock`;
- `.nvmrc`;
- `.node-version`;
- `engines.node`;
- `packageManager`;
- framework configuration;
- standard build scripts;
- standard output directories.

For Python:

- `pyproject.toml`;
- `uv.lock`;
- `poetry.lock`;
- `requirements.txt`;
- `.python-version`;
- common ASGI and WSGI entry points.

For Go:

- `go.mod`;
- `go.sum`;
- conventional `cmd/` layouts;
- declared Go toolchain versions.

For other ecosystems, Shed can support equivalent conventional metadata.

Discovery must be treated as scaffolding, not as the final authority:

> Shed may propose the application definition, but the coding agent owns and
> confirms it.

The agent should review or modify the generated definition because it
understands the intended application architecture better than a generic
repository scanner.

## 11. Scaffolding Workflow

The CLI should support generating a starting SHED file:

```bash
shed init
```

Possible structured result:

```json
{
  "outcome": "generated",
  "file": "SHED",
  "detected": {
    "runtime": {
      "family": "node",
      "version": "22"
    },
    "package_manager": {
      "name": "npm",
      "lockfile": "package-lock.json"
    },
    "build_command": [
      "npm",
      "run",
      "build"
    ],
    "possible_entrypoints": [
      [
        "npm",
        "start"
      ]
    ]
  },
  "uncertain_fields": [
    {
      "field": "content.include",
      "reason": "Both src/ and packages/ contain application code"
    },
    {
      "field": "processes",
      "reason": "A web server and worker were detected"
    }
  ]
}
```

The agent can then edit the generated file. The workflow becomes:

```text
Agent creates application
    ↓
shed init
    ↓
Shed scaffolds conventional fields
    ↓
Agent reviews and completes SHED file
    ↓
shed validate
    ↓
shed plan
    ↓
shed apply
```

This avoids forcing the agent to manually specify obvious metadata while
preserving the agent as the authoritative source.

## 12. Validation

Validation should separate three types of issues.

### Schema validation

The SHED file is syntactically or structurally invalid.

```json
{
  "code": "INVALID_BUILD_DEFINITION",
  "path": "builds.web.runtime.version",
  "message": "Node.js version must be an exact version or supported version constraint."
}
```

### Content validation

Declared content is missing, ambiguous, unsafe, or contains forbidden files.

```json
{
  "code": "POSSIBLE_SECRET_IN_CONTENT",
  "path": ".env",
  "message": "The selected content includes a file that may contain credentials.",
  "recoverable": true,
  "suggested_change": {
    "content.exclude": [
      ".env"
    ]
  }
}
```

### Platform validation

The application definition is valid but cannot be executed under the current
platform or organization policy.

```json
{
  "code": "UNSUPPORTED_NODE_VERSION",
  "requested": "16.20.2",
  "supported": [
    "20.19.4",
    "22.18.0"
  ],
  "recoverable": true
}
```

## 13. Proposed CLI Surface

### Core agent workflow

```text
shed init
shed validate
shed plan
shed apply
shed status
shed logs
shed exec
shed destroy
```

### Content operations

```text
shed content list
shed content digest
shed content diff
shed content prepare
```

### Build operations

```text
shed build plan
shed build apply
shed build inspect
shed build logs
```

These may be advanced commands. In the common case, `shed apply` performs
content resolution, upload, build, and deployment automatically.

### Platform discovery

```text
shed capabilities
shed runtimes
shed toolchains
shed policies
```

For example:

```bash
shed toolchains --runtime node --output json
```

### Lifecycle and governance

```text
shed access
shed grant
shed secret
shed stop
shed start
shed restart
shed extend
shed destroy
```

## 14. Inspect–Plan–Apply Semantics

The central workflow should be:

```text
SHED file
    ↓
validate
    ↓
plan
    ↓
apply
```

`plan` should return:

- resolved content digest;
- files included and excluded;
- resolved runtime versions;
- selected build tools;
- build commands;
- build-cache status;
- expected outputs;
- requested runtime resources;
- networking requirements;
- capability requests;
- approval requirements;
- resulting application changes.

Example:

```json
{
  "outcome": "planned",
  "plan_id": "plan_01K...",
  "content": {
    "digest": "sha256:source...",
    "files": 84,
    "size": 481203
  },
  "build": {
    "runtime": {
      "family": "node",
      "version": "22.18.0"
    },
    "tools": [
      {
        "name": "npm",
        "version": "10.9.3"
      }
    ],
    "cache": "miss",
    "commands": [
      [
        "npm",
        "ci"
      ],
      [
        "npm",
        "run",
        "build"
      ]
    ]
  },
  "deployment": {
    "action": "create",
    "visibility": "private",
    "ttl_seconds": 604800,
    "processes": [
      {
        "name": "web",
        "kind": "http",
        "port": 3000
      }
    ]
  },
  "approval_required": false
}
```

`apply` should execute that exact immutable plan rather than independently
recalculating an unrelated deployment.

## 15. Agent-Friendly Requirements

Every command must support:

```text
--output json
--non-interactive
--request-id <id>
```

Mutating operations must be idempotent.

Results must include:

- stable outcome;
- resource identifiers;
- current state;
- warnings;
- actionable errors;
- next valid operations.

The CLI should never require an agent to parse progress bars, colors, decorative
output, or interactive prompts.

## 16. Target Feature Priority After Inspection

The items below are the target product priorities after the currently
implemented inspection substrate. The ordered execution roadmap is maintained
at the top of this document and in the root README.

### V1

- SHED file schema;
- explicit content definition;
- canonical content manifest;
- content digest calculation;
- incremental blob upload;
- Node.js toolchain support;
- exact Node.js version resolution;
- npm build support;
- dependency lockfile validation;
- source-to-artifact build caching;
- one HTTP process;
- one static workload with optional SPA fallback;
- private endpoint;
- structured validation;
- plan and apply;
- status and logs;
- destroy;
- JSON output;
- idempotency;
- provenance.

### V1.5

- Python and Go toolchains;
- workers and scheduled jobs;
- multiple processes;
- multiple content targets;
- path remapping;
- grants and secret references;
- access management;
- TTL and automatic sleep;
- build and content diff;
- rollback.

### Later

- persistent volumes;
- multi-service application environments;
- content-layer composition across repositories;
- reusable build targets;
- snapshot and restore;
- multiple architectures;
- custom system packages;
- custom builders;
- hermetic local builds;
- direct MCP integration;
- policy-driven toolchain upgrades.

## 17. Core Product Principle

Shed should not attempt to become another coding agent.

The coding agent creates the software and defines its executable closure. Shed
turns that definition into a reproducible, content-addressed, isolated, governed
application.

The core contract is:

```text
Agent defines:
    content
    toolchain
    build
    processes
    requirements

Shed provides:
    validation
    content addressing
    reproducible execution
    isolation
    identity
    networking
    lifecycle
    governance
```

The SHED file is therefore not merely configuration. It is the deterministic
handoff between an agent that understands the software and a platform that
understands how to operate it.

The main correction to the previous CLI plan is that `shed init` may scaffold
conventional information, but `shed inspect` should not become a second coding
agent. The generated SHED definition remains the coding agent's responsibility
and the authoritative deployment input.
