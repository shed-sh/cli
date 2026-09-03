# Remote execution contract

`internal/execution` is the client-side lifecycle boundary between an immutable
SHED bundle and the Shed control plane. The package name describes the operation;
it does not introduce an `Execution` resource. `Deployment` remains the domain
resource and its identifier is the resume handle used by agents.

## Ownership boundary

```text
Railpack inspection → SHED.yaml → deterministic archive
                                      ↓
                              internal/execution
                                      ↓
                 bundle → deployment → events/status → result
```

The preparation side may inspect the project directory. Execution receives only
the validated manifest and immutable archive and must never infer from the
original source tree. Plain `shed deploy` selects this contract; the local
Docker path is opt-in with `--local`, and `--remote` is kept as a no-op alias
for invocations written when the cloud was the explicit choice.

## HTTP sequence

1. `POST /v1/cli/bundles` registers `{project, kind: "source", contentDigest}`.
   Bundles are content-addressed per project: the response carries the bundle
   ID, its `status`, and `reused`. A reused `ready` bundle skips every step
   until deployment creation.
2. While the bundle is `pending`, the CLI streams the complete gzip archive
   with an authenticated `PUT /v1/cli/bundles/{id}/archive` — there is no
   signed-URL indirection.
3. `POST /v1/cli/bundles/{id}/finalize` declares the archive digest and moves
   the bundle to `validating`; the CLI polls `GET /v1/cli/bundles/{id}` until
   validation settles at `ready` or `failed` (with a stable `failureCode`).
4. `POST /v1/cli/deployments` submits `{project, bundleId, runtime}` — the
   project is a plain slug string, `runtime` is the manifest's `run:` block —
   with a required `Idempotency-Key` header. Replays return the original
   deployment; a reused key with different content is a 409.
5. `GET /v1/cli/deployments/{id}/events?after=N&limit=M` returns the ordered
   event trail. The per-deployment `sequence` is the cursor: progress is
   followed by polling, and an interrupted follow resumes from the last
   sequence without replaying earlier records. There is no push stream.
6. `GET /v1/cli/deployments/{id}` returns the authoritative snapshot. The CLI
   calls it after a follow ends or times out so a lost terminal event cannot
   leave the agent with stale state. `url` is present once an origin exists.
7. `POST /v1/cli/deployments/{id}/cancel` requests desired state `cancelled`
   (202 on first request, 200 on idempotent replay).

The exact deployment states are `accepted`, `bundle_validating`, `build_queued`,
`building`, `verifying`, `provisioning`, `health_checking`, `ready`, `failed`,
`cancelling`, and `cancelled`. Terminal states are `ready`, `failed`, and
`cancelled`.

## Agent behavior

- `shed . --remote` follows progress for 30 seconds. If work is unfinished it
  exits successfully with `outcome: pending`, the deployment ID, current state,
  cursor, links, and `nextOperation: status`.
- `--detach` returns immediately after acceptance.
- `--wait` follows until a terminal state; combining it with
  `--wait-timeout D` bounds that wait.
- Human progress is written to stderr and the final result to stdout.
- JSON output is exactly one final object. NDJSON uses a `type` discriminator for
  each progress record and the final result.
- A URL is removed from non-ready snapshots and printed only for `ready`.
- `metadata.name` is stable project identity. `--project` overrides it; older
  definitions fall back to a normalized directory basename.
- `--request-id` is sent unchanged. When omitted, the CLI hashes project name,
  content/archive digests, and the complete manifest into a deterministic key.

## Backend state

Bench implements this contract end to end: bundle registration, upload,
validation, the deployment ledger with idempotency-conflict handling, and the
ordered event trail. Builds currently succeed through a static placeholder
engine and the runtime origin URL appears once the Knative engine is enabled
in production; real builds (the builder engine as Kubernetes Jobs) and cloud
E2E coverage remain before remote mode can become the default. This repository
tests the client side against in-process fake servers mirroring Bench's
handlers.

The `share`/`revoke` grant endpoints have no Bench implementation yet; those
commands fail cleanly against the current API.
