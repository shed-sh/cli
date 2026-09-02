# Designing SHED

A concept memo for the SHED language: what it is, what it should grow into, and
what it should refuse to grow into. Companion to
[cli-and-application-definition.md](./cli-and-application-definition.md), which
describes the definition surface shed ships today; this memo is about the
direction beyond it.

- Status: concept, in progress
- Version: v0.draft
- Written: August 2026

## 1. Origin

Shed already has a definition file: `SHED.yaml`. It is a strict, declarative
manifest that produces a deterministic, content-addressed archive; the builder,
the archive, the digests, and the deploy protocol all consume it and are
indifferent to how it was produced. That indifference is the opening: an
authored program that *evaluates* to the same manifest can add composition and
reuse without touching anything downstream.

`SHED` — no extension, an application definition first and a Starlark program
second — is that program. It is authoring-only. Determinism, idempotency, and
the remote protocol are unaffected by construction: the evaluator lowers to
`definition.Manifest`, the same shape `SHED.yaml` declares directly, and
everything past that boundary sees identical bytes.

Today the surface is deliberately tiny: three builtins — `build`, `http_app`,
`glob` — validated from one spec table that also generates the schema.
Diagnostics accumulate: a file with five mistakes reports five, not the first.
Any other name is an error. This memo is about what the surface should grow
into — and, more importantly, what it should refuse to grow into.

> **Thesis.** SHED is a language for describing workloads that ship in a
> container, wired to their build, their runtime, and their inputs. It is not
> a language for provisioning infrastructure, orchestrating pipelines, or
> standing in for the cloud. Its restraint is the feature.

## 2. Design principles

Six commitments — read them as constraints, not aspirations. Every proposed
addition below is defensible only if it holds against all six.

**1. Small surface, one function per thing.** Each workload type has its own
builtin: `app`, `worker`, `job`, `cron`. The alternative — one `service()` with
a stack of flags that quietly disagree (*cron and healthcheck are mutually
exclusive*) — trades clear naming for hidden validation. Different runtime
semantics deserve different names.

**2. Argv only. Never shell.** Every command is a list of strings.
`[sh, -lc, "…"]` is available when someone explicitly needs it, but the
language will never accept `"npm run build && npm start"` as a value in place
of an argv. Injection, quoting, and portable-across-shells arguments simply do
not exist in this surface.

**3. Diagnostics accumulate.** A single evaluation reports every problem it
finds. Authors get one pass to fix a whole file, not a fix-one, retry, fix-next
loop. Every diagnostic pairs a stable code, a human sentence, and a next step.

**4. Composition via named values.** `build()` returns a value that is passed
by variable to a workload. Two workloads sharing a build write it once. Any
future primitive that produces a value follows the same rule: assign, then
reference. Inline construction is a linter error, because it is exactly the
shape sharing later depends on.

**5. Declarative first.** Where the same intent can be expressed as data or as
code, prefer data. Environment overrides belong in a nested overlay, not in
`if ctx.env == "staging"` branches. The full language is available for the
small number of moments where it earns its keep.

**6. Refuse scope.** Provisioning cloud resources is not shed's job. Neither
is workflow orchestration, secret storage, or traffic routing. Where these
boundaries feel arbitrary, they are load-bearing: crossing them means owning a
much larger surface with much less confidence.

## 3. The primitive surface

Nine builtins. That is all shed exposes, and all it should. Three describe how
something is built; four describe what runs; two describe values whose
resolution the deploy defers.

### 3.1 build

```
build(*, srcs, image, commands = [])
```

Describes compilation. `srcs` is either a literal list of project-relative
paths or a `glob([…])` call. `image` is any Docker image reference. `commands`
is a list of argv lists — each one runs inside `image`, in order, and the
resulting filesystem is what ships.

```python
b = build(
    srcs = glob(["cmd/**", "internal/**", "go.mod", "go.sum"],
                exclude = ["**/*_test.go"]),
    image = "golang:1.26",
    commands = [
        ["go", "mod", "download"],
        ["go", "build", "-ldflags=-w -s", "-o", "out", "./cmd/api"],
    ],
)
```

A `build()` call must appear on the right-hand side of an assignment — it
cannot be written inline as an argument to a workload. The rule is groundwork:
sharing one build across workloads is a first-class shape, and the language
names the shape.

### 3.2 app

```
app(*, name, build | image, cmd, port, env = {}, …)
```

A long-running HTTP workload. Accepts either `build =` (produced from source)
or `image =` (a pre-built container reference) — exactly one, validated at
parse time. `cmd` is the process argv. `port` is the TCP port the container
listens on; a request from shed will reach it.

```python
app(
    name = "api",
    build = b,
    cmd = ["./out", "serve"],
    port = 8080,
    env = {
        "DATABASE_URL":  db.url,
        "LOG_LEVEL":     param("log_level", default = "info"),
        "STRIPE_SECRET": secret("stripe_secret"),
    },
    health     = "/healthz",
    pre_deploy = [migrate],
)
```

The rename from `http_app` to `app` reflects that the workload's type is the
runtime behavior, not the source. An `app` is what runs; how it was constructed
is a separate axis, expressed by whether `build` or `image` is present.

### 3.3 worker

```
worker(*, name, build | image, cmd, env = {}, restart = "on_failure", concurrency = 1)
```

A long-running process that does not listen on a port. Restarts on exit by
default; `restart = "never"` makes it a one-shot that shed still supervises.
`concurrency` declares how many parallel replicas the runtime should launch —
semantic meaning is left to the workload (a queue consumer will interpret it
as consumer count).

### 3.4 job

```
job(*, name, build | image, cmd, env = {}, timeout = "30m")
```

A one-shot task. Success is exit zero within `timeout`. Explicitly not
restarted: if the runtime saw the process exit non-zero, that is the job's
terminal state. Jobs are values — assign them to a variable and reference them
from `cron`, from `pre_deploy` hooks, or from a future `shed run <job>`
command.

```python
migrate = job(
    name = "migrate",
    build = b,
    cmd = ["./out", "migrate"],
    env = {"DATABASE_URL": db.url},
    timeout = "5m",
)
```

### 3.5 cron

```
cron(*, name, schedule, job)
```

A scheduled invocation of an existing `job` value. `schedule` accepts full cron
syntax (`"0 3 * * *"`) and the common shortcuts (`"@hourly"`, `"@daily"`).
Overlapping runs are not started: if the previous invocation is still executing
when the next tick arrives, the tick is skipped and recorded. This matches how
every well-behaved cron runner behaves; codifying it in the model means the
surface has no policy knobs to tune.

```python
cleanup = job(name = "cleanup", build = b,
              cmd = ["./out", "cleanup"])

cron(name = "nightly-cleanup", schedule = "0 3 * * *", job = cleanup)
```

### 3.6 glob

```
glob(patterns, *, exclude = [])
```

Selects files by doublestar pattern from the universe shed collects —
post-ignore, post-structural-exclude. Ignored files, dotfiles, and structural
excludes (`.git`, `node_modules`, `.env*`, `*.pem`) are removed before matching,
so `glob` cannot select them regardless of pattern. A bare directory name
selects everything under it. This is the one primitive with a positional
argument, on the theory that `glob(["cmd/**"])` reads well and
`glob(patterns = ["cmd/**"])` reads worse.

### 3.7 secret

```
secret(key) → Reference
```

A deferred value read from an encrypted sidecar (see §6). Anywhere shed
accepts a string in `env = {…}`, it also accepts a `Reference`: the runtime
resolves it at deploy time, injecting the plaintext as the container's
environment variable. The plaintext never appears in build image layers, in
the archive, in logs at info level, or in shed's own JSON output.

### 3.8 param

```
param(name, *, default = None) → Reference
```

A deploy-time parameter, supplied by `shed deploy --param name=value`. The
default is used when no value is passed. This is the primitive that makes one
SHED file expressive across environments without procedural branches or file
duplication.

### 3.9 external

```
external(name, url_env)
```

Declares a dependency shed knows about but does not manage. Returns a small
value with a typed `.url` attribute — a `Reference` — that reads from the named
environment variable on the host at deploy time. Wire it into workloads
exactly like a secret:

```python
db = external(name = "postgres", url_env = "DATABASE_URL")

app(name = "api", build = b, cmd = ["./out"], port = 8080,
    env = {"DATABASE_URL": db.url})
```

A workload that references `db.url` means shed will fail early if
`DATABASE_URL` is unset at deploy — the check moves from runtime crash to
definition-time diagnostic, and stays deterministic because the read happens
exactly once, up front.

### 3.10 A worked example

A realistic monorepo — an HTTP API, a queue worker, a nightly cleanup, a
database dependency, one secret, one deploy-time knob — fits in about thirty
lines:

```python
b = build(
    srcs = glob(["cmd/**", "internal/**", "go.mod", "go.sum"]),
    image = "golang:1.26",
    commands = [["go", "build", "-o", "out", "./cmd/api"]],
)

db = external(name = "postgres", url_env = "DATABASE_URL")

migrate = job(name = "migrate", build = b,
              cmd = ["./out", "migrate"],
              env = {"DATABASE_URL": db.url}, timeout = "5m")

cleanup = job(name = "cleanup", build = b,
              cmd = ["./out", "cleanup"],
              env = {"DATABASE_URL": db.url})

app(name = "api", build = b, cmd = ["./out", "serve"], port = 8080,
    env = {"DATABASE_URL":  db.url,
           "LOG_LEVEL":     param("log_level", default = "info"),
           "STRIPE_SECRET": secret("stripe_secret")},
    health     = "/healthz",
    pre_deploy = [migrate])

worker(name = "queue", build = b, cmd = ["./out", "consume"],
       env = {"DATABASE_URL":  db.url,
              "STRIPE_SECRET": secret("stripe_secret")},
       concurrency = 4)

cron(name = "nightly", schedule = "0 3 * * *", job = cleanup)
```

## 4. Fields, not primitives

The temptation with any DSL is to promote every concept to a top-level
function. Resist. If a concern belongs to *a specific workload*, it belongs on
the workload's keyword arguments — not in a sibling call at the top of the
file. Fields keep the shape of a workload declaration whole, and they keep
errors grounded (*app "api" has an invalid restart*, not *unknown top-level
call somewhere*).

| Concern                    | Belongs on                          | Field                                                                       |
| -------------------------- | ----------------------------------- | --------------------------------------------------------------------------- |
| Deploy hooks               | `app`, `worker`                     | `pre_deploy = [job_ref]`, `post_deploy = […]`                               |
| HTTP health check          | `app`                               | `health = "/healthz"`, `health_timeout = "30s"`                             |
| Restart policy             | `app`, `worker`, `job`              | `restart` ∈ {`on_failure`, `always`, `never`}, `max_retries`                |
| Graceful shutdown          | `app`, `worker`                     | `stop_signal`, `drain_timeout = "10s"`                                      |
| Deploy ordering            | `app`, `worker`                     | `depends_on = [other]`                                                      |
| Platform labels            | every workload                      | `labels = {"owner": "team-a"}`                                              |
| Preserve existing value    | anywhere a `Reference` fits         | `preserve()` — a sentinel, not a call                                       |

`depends_on` is a *hint* — not a scheduler. It means shed's rollout waits for
the referenced workload's health check before promoting the dependent. It does
not become a DAG runner with retries and skip conditions. When it wants to be
more than a hint, it is not `depends_on` anymore; it is a pipeline product.

## 5. Values that resolve later

Three primitives — `secret`, `param`, `external` — share a single underlying
type: `Reference`. A `Reference` is opaque at evaluation time. It has one
interface: *can be placed anywhere shed accepts a string in `env`, and is
resolved at deploy time to the actual value*.

This is the primitive that lets three unrelated features share one
implementation. The evaluator needs to know: (a) an `env` map value may be a
literal string or a `Reference`; (b) each concrete `Reference` subtype knows
how to resolve itself at deploy time; (c) rendering a definition for
`shed check` shows the `Reference`'s name and kind, never its resolved value.

Downstream benefits fall out for free. Diagnostics can distinguish *secret
`stripe_secret` is missing from the sidecar* from *param `log_level` was not
supplied and has no default* — both are Reference resolution failures with
typed context. A future `shed lint --strict` can flag env values whose
`Reference` subtype crosses trust boundaries the workload should not. And a
fourth primitive — cross-workload references, `api.url` — drops in without new
machinery.

> **Design invariant.** Any deferred value in the language is a `Reference`.
> If a new primitive can be expressed as a `Reference`, it should be — the
> alternative is inventing a parallel type system for a use case that already
> has one.

## 6. Secrets in git

The instinct — commit encrypted secrets alongside code, decrypt at deploy — is
right. The pattern is well-proven (Ansible Vault, git-crypt, agenix,
SealedSecrets), and there is a modern winner shed should adopt rather than
reinvent: **SOPS**.

SOPS encrypts only *values*. Keys stay readable. That single decision is what
makes it survive real use: `git diff` between two versions of a secrets file
shows which keys changed, which is impossible with vault-style opaque-blob
encryption. Its default backend is `age`, which is small, modern, and free of
the GPG baggage that made every previous "just commit encrypted secrets" tool
a pain to onboard. It has cloud-KMS backends for production. It is the format
Flux and every serious GitOps stack landed on.

### 6.1 Model

A sidecar file next to `SHED.yaml` — `.shed.secrets.yaml` — sops-encrypted,
committed. Keys stay readable so `git diff` is meaningful; values are
ciphertext:

```yaml
DATABASE_URL:  ENC[AES256_GCM,data:…,type:str]
STRIPE_SECRET: ENC[AES256_GCM,data:…,type:str]
sops:
    age:
      - recipient: age1…
        enc: |
          -----BEGIN AGE ENCRYPTED FILE-----
          …
    lastmodified: "2026-08-23T…"
    version: 3.9.1
```

In the SHED program, `secret("STRIPE_SECRET")` returns a `Reference` that
binds to that key. At deploy time, shed reads the sidecar, decrypts it against
the age key on disk (or a KMS credential in the cloud), and injects plaintext
as an environment variable on the container. Nothing else changes.

### 6.2 Implementation

Two paths, both viable:

1. **Shell out to the `sops` binary.** Zero new Go dependencies. Users install
   sops separately; when it is not on `$PATH`, shed prints a diagnostic with
   the install one-liner. Matches shed's existing "Docker is a runtime
   prerequisite, we do not vendor it" stance.
2. **Vendor `getsops/sops` as a library.** Everything in-process; no external
   dependency. Costs 5–8 MB on the binary because the sops module pulls the
   AWS, GCP, Azure, and Vault SDKs. Gated behind a build tag (`-tags sops`)
   it is fine; unconditional it is a lot.

Start with (1). If the missing-`sops` diagnostic proves to be the top
install-time friction, revisit.

### 6.3 Rules

- Encrypted values live in a sidecar file. Inline `ENC[…]` blobs in the SHED
  file itself are refused, always. Ansible Vault allowed this, it aged badly,
  do not repeat it.
- `shed check` does not decrypt. Diagnostics reference secrets by name.
- `shed deploy --dry-run` does not decrypt. The dry-run archive is meant to
  be inspectable, and inspectable is exactly what a decrypted secret should
  not be.
- Decryption happens once, at deploy, immediately before injection. No
  caching, no on-disk plaintext.
- Rotation is a sops workflow, not a shed workflow. Adding or removing a
  team member is `sops updatekeys`, and shed has no opinion on it.

## 7. Environments and parameters

Two mechanisms cover the whole space: parameters for values that vary,
overlays for whole sections that vary. Both are declarative. Neither requires
the SHED file to know which environment it is evaluating for.

### 7.1 Parameters

`param(name, default = …)` supplies deploy-time values. A parameter that has
no default and is not supplied fails `shed check`, so missing configuration is
caught before deploy, not at runtime.

```python
app(name = "api", …,
    env = {"LOG_LEVEL": param("log_level", default = "info")})

# shed deploy . --param log_level=debug
```

### 7.2 Overlays

For structural differences — an entire block that changes shape between
staging and production — a builtin overlays declarative maps onto a base. The
overlay function returns a plain dictionary; the workload consumes it as
usual.

```python
env = overlay(
    base    = {"LOG_LEVEL": "info", "REGION": "us-west-2"},
    staging = {"LOG_LEVEL": "debug"},
    prod    = {"REGION": "eu-central-1"},
)

app(name = "api", …, env = env)

# shed deploy . --env staging
```

The alternative — Railway's approach — is procedural:
`if ctx.env == "staging" { … }` scattered through the config function. That
has more expressive power at the cost of diff-readability. Reading "what does
prod actually get" then means running the code in your head against the prod
ctx. Declarative overlays trade a small amount of power for a large amount of
reviewability, and shed's file is short enough that the trade favors the
reader.

> **Not a mode.** The evaluator has no notion of *environment*. It always
> evaluates the same file the same way. Both `--param` and `--env` are consumed
> by the deploy driver, not by the language. This keeps the language decidable
> and the file's meaning single-valued.

## 8. What SHED will not become

Every one of these is a genuine gravitational pull. Naming them explicitly is
not a rejection of usefulness — it is the discipline of leaving useful things
to tools better suited to them.

**Provisioning cloud resources.** A `postgres()` builtin that creates a
database. A `bucket()` that provisions object storage. The path from these to
a state file, per-cloud drivers, drift detection, and destroy semantics is one
design decision long, and the destination is a worse Terraform. Terraform
exists. Pulumi exists. Both are better at this than shed will ever be. Where
shed needs to reference an external resource, it declares it as unmanaged
(`external()`) and stops.

**Volumes and mounts.** The Compose-file trap. The moment mounts are
declarable, users expect shed to reason about durability, host paths,
permissions, node affinity, and volume plugins. That is a Kubernetes-shaped
problem, and shed is not going to solve it. Workloads that need state talk to
a database over the network — declared via `external()`, resolved via
`db.url` — like every other service.

**Cross-file imports.** `load("shared/build.sky")` is the instinct. In
practice it opens: what is the search path? Can imports reach into `$HOME`?
Into git submodules? What about caching and invalidation? None of these have
unambiguous answers, and the current one-file-per-project rule handles the
95% case. If shared builds across projects becomes the single most-requested
feature, revisit. Not before.

**Region placement and topology.** `replicas = { "us-west-2": 3 }` reads as
configuration but behaves as deployment policy. The former belongs in a file;
the latter belongs at the deploy call site or in a platform. Shed pushes
topology to `shed deploy --region …` flags and stays out of describing where
the workload physically runs.

**Full pipeline orchestration.** A `pipeline(name, steps=[…])` with inter-job
dependencies, retries, and failure policies is a workflow product. Airflow is
that product. Dagster is that product. Prefect is that product. Shed's
`pre_deploy` covers the small case where a migration runs before a rollout;
anything more sophisticated wants a workflow engine, not this file.

## 9. Prior art, briefly

Two systems are worth naming directly, because SHED sits between them in a
specific way.

**Railway (`.railway/railway.ts`).** Same shape family. A configuration
function that returns a project of services, with typed cross-service
references (`db.env.DATABASE_URL`), a resource-preserving sentinel
(`preserve()`), and pre-built image sources. SHED borrows the cross-workload
wiring pattern wholesale — the direction of use is proven. Where they diverge:
Railway makes environments procedural (`ctx.isEnvironment("staging")`) and
reaches into provisioning (`postgres()`, `volume()`, `bucket()`). Shed keeps
environments declarative and keeps provisioning out.

**Docker Compose.** Same problem domain (local, containerized workloads
described in a file), different design tradition. Compose is a maximalist YAML
with a decade of accreted fields; SHED is a minimalist DSL. Compose collapses
"how it is built" and "how it runs" into one `services` block; SHED separates
`build()` as a value shared between workloads. Compose has mounts; SHED
refuses them. Compose is a fine tool. It is not the tool shed is trying to
become.

Terraform and Pulumi are the tools SHED is *most* often confused with, and the
confusion is worth naming: they describe infrastructure state and reconcile it
toward a target. Shed describes application shape and hands it to a builder.
Both are declarative, both are file-based, both use variables — but the shape
of what they own is completely different. When someone reaches for shed to
provision an RDS instance, the answer is not to add the feature; it is to
point at Terraform.

## 10. Open questions

These are the decisions still to make. None of them block the shape of the
surface; all of them affect its ergonomics.

1. **Rename with or without an alias.** `http_app` → `app` is a beta-tier
   break. The migration is one `sed`. An alias for one release is kinder but
   keeps two names in the schema. Lean: break it cleanly.
2. **The inline-build lint.** Today, `build()` must be assigned to a variable.
   That rule pays off when workloads share builds — genuinely useful once
   `job`/`worker`/`cron` land. Whether it should remain in force for one-off
   single-workload files is a judgment call. Lean: keep it, because
   consistency across every workload builtin outweighs the one-off
   ergonomics.
3. **Cron accepting `@shortcuts`.** Full cron plus the common shortcuts
   (`@hourly`, `@daily`, `@weekly`) covers real cases without inventing custom
   syntax. Rejecting overlapping runs is model, not policy — the spec should
   state it as a guarantee, not a flag.
4. **Cross-workload references.** `api.url`, `api.env["X"]`. The pattern is
   right; the exact accessor names and what they resolve to at deploy time —
   internal DNS, private domain, public URL — needs a separate short design
   note.
5. **`preserve()` as a sentinel or a builtin.** Sentinel is cleaner (nothing
   new in the surface). Builtin is more discoverable via `shed schema`. Lean:
   builtin, because it earns its place next to `secret` and `param`.
6. **Overlay merge semantics.** Shallow or deep. Shallow is predictable; deep
   is convenient. Lean: shallow with a documented pattern for the nested
   case, because deep-merge semantics have surprises that never fully go
   away.
7. **Labels on every workload.** Not a design question so much as a "when."
   Adding `labels = {…}` costs almost nothing; every mature IaC eventually
   wishes it had them from day one. Add early.
