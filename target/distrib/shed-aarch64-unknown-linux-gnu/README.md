# Shed

Draft CLI for deploying and sharing small software.

The binary is named `shed`. Railpack detects an application, Shed generates a
portable definition, and builders execute a deterministic source archive.

## Build

```bash
go build -o shed ./cmd/shed
```

## Implementation status

Shed currently completes the full archive-to-URL loop **locally**, not in the
cloud. The repository contains three working layers:

- Cloud authentication plus an agent-first remote execution client. `--remote`
  registers and uploads the immutable bundle, creates an idempotent deployment,
  follows resumable NDJSON build/runtime events, reconciles status, and supports
  cancellation. The backend endpoints are a tested client contract; no Shed
  backend, remote builder, hosted runtime, or public ingress is implemented here.
- The new Railpack integration can inspect real application source, select a
  provider, resolve requested toolchains offline, and generate a structured
  build plan with filesystem evidence. It is exercised against pinned Next.js
  and Node.js repositories in [`testdata/e2e`](testdata/e2e).
- `shed init` turns Railpack inspection into a strict `shed.run/v1alpha1`
  `SHED.yaml`. Definitions matching the current trusted catalog include both
  the local `build/run` projection and remote-builder `base/parts/apps`
  projection; other detected projects remain usable locally.
  `shed deploy` loads that file when present, packages its exact
  content closure, builds only from the resulting archive, starts the image,
  and verifies its declared HTTP port. Bare `shed` inspects nothing; it prints
  the overview, because reading a project is work the user has to ask for.

The local loop uses Docker as a replaceable builder/runtime boundary. It returns
one stable URL per project and reuses the running instance when content is
unchanged. Use `--mock` when Docker is unavailable; that path verifies the
complete archive and returns an `uploaded_mock` receipt.

| Input and destination | Current result |
| --- | --- |
| Supported Node/Next.js source → local Docker | Working and E2E tested |
| Go module source → local Docker | Working; builds and runs from the golang image |
| Hand-authored `SHED.yaml` → local Docker | Supported by the provider-neutral executor |
| Static/SPA source → local or cloud | Not yet supported by the manifest/runtime |
| Arbitrary source provider → local or cloud | Railpack may detect it, but automatic SHED lowering covers Node and Go only |
| Existing arbitrary tarball or OCI image | Not accepted as a public deployment input |
| SHED archive → mock upload | Working, but deliberately performs no network or deployment |
| SHED archive → remote deployment API | CLI implemented behind `--remote`; backend not implemented |
| SHED archive → cloud URL | Blocked on backend builder, runtime, ingress, and readiness |

The daemon-backed E2E suite also deletes the original source after packaging and
then builds and runs successfully from the archive alone:

```bash
task test-e2e-docker
```

To generate and inspect an archive without network access:

```bash
task build
./shed testdata/e2e/repos/next-learn \
  --dry-run \
  --archive /tmp/next-learn.tar.gz \
  --output json
tar -tzf /tmp/next-learn.tar.gz
```

Without `--dry-run`, Shed builds and starts the project locally:

```bash
./shed testdata/e2e/repos/next-learn --output json

# Offline archive-only fallback
./shed testdata/e2e/repos/next-learn --mock --output json
```

The remote path is explicit until a real cloud E2E is available. It is designed
for coding agents: it never prompts, emits one final JSON object (or typed NDJSON
progress), waits for 30 seconds by default, and returns a resumable deployment
receipt if the deployment is still running:

```bash
shed . --remote --output json --non-interactive
shed . --remote --detach --request-id agent-operation-42 --output json
shed status dep_123 --wait --output ndjson
shed logs dep_123 --stage build --follow --cursor cursor_12 --output ndjson
shed cancel dep_123 --output json
```

`--project` overrides stable project identity. Otherwise Shed uses
`metadata.name` from `SHED.yaml`, or a normalized directory basename for older
definitions. When `--request-id` is omitted, the CLI derives it from the project,
bundle digests, and runtime definition. A URL is emitted only after the backend
reports `ready`.

The endpoint sequence, deployment states, cursor rules, and remaining backend
work are specified in [the remote execution contract](docs/remote-execution.md).

To inspect the generated plan for the smallest Next.js fixture:

```bash
git submodule update --init --depth 1
SHED_E2E_PRINT_PLAN=1 go test ./internal/e2e \
  -run TestProjectDetectionAndPlanEndToEnd/nextjs-hello-world -v
```

The executable definition can be printed with:

```bash
SHED_E2E_PRINT_DEFINITION=1 go test ./internal/e2e \
  -run TestProjectDetectionAndPlanEndToEnd/next-learn -v
```

The plan currently includes the selected provider, requested toolchains,
install/build steps, runtime start command, and normalized evidence such as
checked paths and SHA-256 file digests.

## Releases and Homebrew

Publishing a GitHub release runs the release workflow. It builds signed-versioned
archives for macOS and Linux, attaches them to the release, and updates the
`shed-sh/homebrew-tap` formula. Before publishing the first release, create that
repository and add `HOMEBREW_TAP_GITHUB_TOKEN` as an Actions secret in this
repository. The token needs permission to write repository contents for
`shed-sh/homebrew-tap`.

Users can then install Shed with:

```bash
brew install shed-sh/tap/shed
```

## Try the local workflow

From a Node.js, Next.js, or Go project:

```bash
shed init
shed deploy
shed deploy . --output json
shed deploy . --dry-run --archive application.tar.gz
```

Running `shed` with no arguments prints the overview and inspects nothing.

The workflow:

- uses Railpack to scaffold `SHED.yaml` when it is absent;
- treats an existing `SHED.yaml` as authoritative and does not redetect;
- applies `.gitignore` and `.shedignore`;
- excludes `.git`, `.shed`, `node_modules`, build output, `.env*`, private keys, and common credential files;
- rejects symlinks and special files;
- records a canonical per-file source manifest;
- embeds `SHED.yaml`, `.shed-source.json`, and exact source bytes in a
  deterministic `tar.gz`;
- calculates separate content and archive SHA-256 digests;
- verifies and safely extracts the archive, then derives the Docker build and
  runtime solely from its `SHED.yaml`;
- treats any HTTP response below 500 as serving, while reporting HTTP 5xx and
  readiness failures separately;
- keeps a local instance ledger so rerunning a project is an update;
- can mock-upload without login, configuration, or network access with `--mock`.

The ignore parser intentionally supports only the common subset needed by the draft: comments, negation, directory patterns, root-anchored and path-anchored patterns, and shell-style wildcards. It is not yet a complete Git ignore implementation.

## The SHED file

`SHED.yaml` stays the wire format, but a project can be defined by a `SHED`
program instead — Starlark, Python-shaped, built for agent editing. Shed
evaluates it into the exact same manifest before every deploy; the builder, the
archive, the digests, and the remote protocol only ever see the evaluated
`SHED.yaml`, so determinism and idempotency are unchanged. The program itself
is structurally excluded from the archive.

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
    name = "hello-api",
    build = b,
    cmd = ["./out"],
    port = 8080,
)
```

The rules, each enforced with a positioned `SHED:line:col` diagnostic and a
stable snake_case code:

1. Only `build()`, `http_app()`, and `glob()` exist; any other name is an error
   that states the whole surface.
2. Every argument is named; positional calls fail. Unknown and missing
   arguments fail with a "did you mean" and the accepted list.
3. Commands are argv lists, never shell strings.
4. `build()` is assigned to a variable and passed by name; inline
   `build = build(...)` and unused build values are errors.
5. Exactly one `http_app()` call registers the application.
6. `glob()` filters the same file universe packaging uses — post-ignore,
   post-structural-excludes — so `.env*`, private keys, and ignored files can
   never enter `srcs`. Literal `srcs` entries are validated against that
   universe too.

Not yet supported, and stated in the generated header comment: `param()`,
`worker()`, `static_site()`, multiple apps, `detect()`, `load()`, build-time
env.

The agent loop: start from `shed init --format shed`, validate every edit with
`shed check --output json` — one pass reports **all** diagnostics, not the
first — and print the authoring API with `shed schema`. A project may have
either `SHED` or `SHED.yaml`, never both; two definitions fail with
`definition_conflict`.

## Output

Human output is styled only when it is going to a terminal. A pipe, a redirect,
`NO_COLOR`, or `TERM=dumb` all produce plain bytes, so captured output never
carries escape sequences. Lines wrap to the terminal width, capped at 96 columns.

`shed init` reports what it found rather than announcing a file: the detected
provider, the toolchain image, the paths it will package, the build commands,
and the runtime command and port. `shed init --output json` returns the same
description as data — `detected`, `toolchain`, `includes`, `builds`, `runs`, a
one-line `description`, and the authoritative `application` manifest — so an
agent learns the same facts without having to parse `SHED.yaml` itself.
`shed check --output json` returns one object with the verdict, every
diagnostic (stable `code`, positioned `message`, `hints`), and the evaluated
`application` when valid; `shed schema --output json` returns the SHED file
API as data.

Coding agents should pass `--output json` for one final JSON object, or
`--output ndjson` for one typed record per line. Failures arrive as that same
object, `{"type":"result","outcome":"failed","failure":{"code":...}}` with exit
status 1. Branch on `failure.code`, which is stable; the sentence beside it is
prose and may be reworded. Shed never prompts, so no command needs a terminal.
`shed help` prints this contract alongside the commands.

## Proposed cloud flow

```bash
shed login
shed login --no-browser
shed whoami
shed deploy .
shed share dep_123 alice@company.com
shed logs dep_123
shed revoke dep_123 alice@company.com
shed open https://prototype.example.com
shed logout
```

`shed login` prints a link to the Shed portal and opens it. The link is built
from the CLI's own configuration, so it appears without any call to the API —
a control plane that is down cannot stop the browser from opening.

After signing in through Clerk and approving, the portal hands the code straight
back to a listener the CLI is holding on loopback, and nothing has to be typed.
Use `--no-browser` on remote or headless systems: the printed link opens on any
device, and the portal displays a single-use verification code to paste back
into the CLI instead.

The CLI ships pointing at the hosted control plane and portal, so a released
binary needs no configuration. Working against a local stack means overriding
both — they are development settings and are deliberately absent from the
end-user documentation:

```bash
SHED_API_URL=http://localhost:8080 shed deploy .
SHED_PORTAL_URL=http://localhost:3000 shed login
```

Set them in `$UserConfigDir/shed/config.json` (`apiUrl`, `portalUrl`) to avoid
repeating them.

The CLI stores login credentials in the operating-system keyring. When no
keyring is available, it warns and falls back to the owner-only Shed config
file. `SHED_TOKEN` overrides either stored credential without persisting the
environment value.

The complete portal, Yard, encryption, and persistence contract is documented
in [`docs/authentication.md`](docs/authentication.md).

The proposed machine-facing CLI, SHED application definition, content-addressed
build model, and agent/platform responsibility boundary are documented in
[`docs/cli-and-application-definition.md`](docs/cli-and-application-definition.md).

The draft client expects:

```text
POST   /v1/cli/auth/sessions          (protocol 1 only; the CLI no longer calls it)
POST   /v1/cli/auth/sessions/{id}/exchange
GET    /v1/cli/me
DELETE /v1/cli/auth/tokens/current
POST   /v1/cli/uploads
PUT    <source upload URL>
POST   /v1/cli/deployments
GET    /v1/cli/deployments/{id}/events
GET    /v1/cli/deployments/{id}/logs
POST   /v1/cli/deployments/{id}/grants
DELETE /v1/cli/deployments/{id}/grants/{email}
```

Portal-only (Clerk session, not called by the CLI):

```text
POST   /v1/portal/cli-auth/sessions/{id}/approve
GET    /v1/portal/me
```

Deployment events use newline-delimited JSON:

```json
{"stage":"build","message":"Building Next.js","status":"running"}
{"stage":"deploy","message":"Deployment is ready","status":"ready"}
```

This API contract is a draft and is deliberately isolated in `internal/api`.

## Current boundary

Railpack detection supports its retained provider catalog. The current local
builder catalog executes Node and Go applications; unsupported detected
providers fail explicitly during `shed init`. Hand-authored definitions can use
another Docker base and command set because the executor itself is
provider-neutral.

Three limits of that detection matter, and are pinned by tests in
[`internal/definition/detection_boundary_test.go`](internal/definition/detection_boundary_test.go):

- **No provider reports a port, and the Railpack plan schema has no field for
  one.** Its generated Caddy configs bind `:{$PORT:80}` and its Python, Ruby and
  .NET start commands expand `${PORT:-8000}`, because Railpack expects the host
  to inject `$PORT` at runtime. Shed's `run.port` is therefore an assumption,
  not a finding; `shed init` labels it as assumed, and the value only works for
  applications that read `$PORT`.
- **Detection is by filename, never by behaviour.** A Go batch worker and a Go
  HTTP server produce an identical plan, identical metadata, and an identical
  start command. Shed will package, build, and start a program that never
  listens, and only the readiness probe notices. Any workload contract has to
  come from `SHED.yaml`.
- **Several providers emit a shell string rather than an argv.** Ruby, Java,
  .NET and staticfile start commands carry parameter expansion, environment
  assignment prefixes, redirection, or a glob. Shed runs commands without a
  shell, so this — not the build steps — is the real obstacle to lowering those
  families.

Static sites must become a first-class SHED workload rather than being disguised
as an inferred Caddy or Node start command. The planned manifest keeps the build
contract and makes `static` mutually exclusive with `run`:

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
  fallback: index.html # optional SPA fallback
```

`static.directory` names a normalized relative directory in the build output.
Shed owns the trusted static server, MIME handling, HTTP port, and readiness;
the application does not need to declare a server command.

## Roadmap

### Completed and verified locally

- [x] Go 1.26.3 baseline across the module, CI, and release workflow.
- [x] Draft CLI authentication and cloud HTTP client contracts for source
  registration, archive upload, deployment creation, events, sharing, and logs.
  These clients do not constitute a cloud implementation.
- [x] Railpack `v0.35.0` provider closure vendored as a nested Go module with its
  provider order, fixtures, tests, and Procfile overlay preserved.
- [x] Rooted application filesystem using `os.Root`, safe path and symlink
  handling, deterministic globbing, and bounded evidence recording.
- [x] Offline resolver and local version-file inspection for provider tests.
- [x] Structured provider attempts with separate detection, initialization, plan,
  configuration, and Procfile evidence.
- [x] One authoritative `shed.run/v1alpha1` `SHED.yaml` contract containing content,
  build image/commands, and runtime command/environment/port.
- [x] `shed init`, existing-definition loading, and Railpack-only scaffolding.
- [x] Canonical source manifests and deterministic archives with separate content
  and archive identities.
- [x] Safe archive extraction and execution based only on the packaged
  `SHED.yaml` and source bytes.
- [x] `shed deploy` and its `shed <directory>` shorthand using a local Docker build/runtime
  boundary, stable per-project instances, readiness classification, and a
  digest-verifying `--mock` fallback.
- [x] End-to-end coverage against pinned Next.js and Express repositories using Git
  submodules.
- [x] Archive-boundary E2E coverage that removes the original source before Docker
  build and runtime verification.
- [x] `task check` wiring for Shed and the nested Railpack module.
- [x] Agent-first `internal/execution` coordinator with the deployment state
  machine, deterministic idempotency keys, finite/default/detached waits,
  cursor-based stream resumption, status reconciliation, and cancellation.
- [x] Remote CLI surface behind `--remote`, including `--project`, `--detach`,
  `--wait`, `--wait-timeout`, JSON/NDJSON output, stage-filtered logs, status,
  and cancel commands. The HTTP protocol is covered with fake-server tests.

### Next workload task

- [ ] **WORKLOAD-01 — Add the static workload contract.** Extend `SHED.yaml`
  with mutually exclusive `run` and `static` targets; validate the output
  directory, index, and optional SPA fallback; lower Railpack static/SPA plans;
  add archive-only Docker E2E fixtures for a plain static site and a built SPA.

### Required for the first cloud URL

- [ ] **CLOUD-01 — Harden the builder inputs.** Replace floating development
  bases with compiled, digest-pinned trusted images and record the resolved
  image/toolchain identity in the build key.
- [ ] **CLOUD-02 — Implement remote source/deployment endpoints.** The CLI client
  and execution coordinator are complete. Add the backend endpoints and
  storage for source registration, signed archive upload, digest/size
  verification, request-ID idempotency, immutable bundle records, deployment
  snapshots, replayable streams, and cancellation. Keep `--mock` as the offline
  test double.
- [ ] **CLOUD-03 — Run the remote Werf worker.** Consume an immutable source
  record, safely extract the archive, validate `SHED.yaml`, build an OCI image
  through the sibling Werf project, push it to a registry, and record
  source-to-image provenance.
- [ ] **CLOUD-04 — Implement the hosted runtime.** Pull the image, start exactly
  one declared HTTP or static workload in isolation, retain logs and exit state,
  and give each application a stable identity across updates.
- [ ] **CLOUD-05 — Add ingress and readiness.** Assign a stable public/private
  hostname, preserve it across revisions, distinguish process exit,
  port-not-bound, timeout, and HTTP 5xx, and switch traffic only after readiness
  succeeds.
- [ ] **CLOUD-06 — Promote and verify the remote loop.** Once the backend is
  live, make remote execution the bare-`shed` default, retain an explicit local
  mode, and add a cloud E2E that starts from a fixture directory and asserts the
  response body through public ingress.

### Required for broader arbitrary applications

- [ ] **PLATFORM-01 — Expand Railpack lowering.** Go modules and Node are
  lowered today. Add Python, Bun, Go workspaces, additional Node variants, and
  other retained providers with provider-specific E2E fixtures.
- [ ] **PLATFORM-02 — Define additional input contracts.** Decide whether
  prebuilt OCI images and externally produced SHED archives are supported public
  inputs; validate provenance and trust boundaries before accepting either.
- [ ] **PLATFORM-03 — Add production lifecycle features.** Build caching,
  incremental CAS upload, secrets, grants, approvals, TTL/suspend, rollback,
  and complete source-to-revision provenance.
- [ ] **PLATFORM-04 — Add agent distribution.** Publish CLI authentication plus
  Codex and Claude marketplace adapters; use MCP OAuth for hosted operations
  while keeping source packaging in the CLI or another workspace-local
  component.

### Documentation rule

Documentation is part of each implementation iteration. Any code change that
changes behavior, the CLI surface, generated output, test fixtures, or roadmap
must update this README or the detailed design document in the same change.
Every completed milestone should include a runnable example and its validation
command.
