# Shed end-to-end repositories

The E2E suite runs Shed's detection and plan generation against pinned Git
submodules instead of synthetic manifests. The repositories are intentionally
small and cover the main JavaScript paths:

- `render-examples/nextjs-hello-world`: minimal Next.js application;
- `vercel/nextjs-postgres-auth-starter`: Next.js application with a realistic
  dependency graph;
- `vercel/next-learn`: official Next.js learning project using pnpm;
- `render-examples/express-hello-world`: minimal non-Next Node/Express app.

The parent repository pins the following revisions through Git submodule
gitlinks:

| Repository | Revision |
| --- | --- |
| `render-examples/nextjs-hello-world` | `a0f64db8` |
| `vercel/nextjs-postgres-auth-starter` | `fde8ecf1` |
| `vercel/next-learn` | `bb255844` |
| `render-examples/express-hello-world` | `039c3477` |

Initialize them after cloning with:

```sh
git submodule update --init --depth 1
```

The regular E2E tests do not install JavaScript dependencies or execute the
Git-submodule projects. They inspect the checked-out source, generate the
offline application plan and `SHED.yaml`, and create a canonical deterministic
source archive for every fixture. No network upload occurs.

`docker-node` is a separate, dependency-free runtime fixture. The opt-in Docker
tests package its source, verify the embedded definition and source manifest,
delete the original source before one build, start the resulting image, and
check its HTTP body. Lifecycle coverage also verifies an unchanged rerun reuses
the same container and URL, while a source update keeps the URL and serves the
new code. Tests remove their containers and images during cleanup.

Run the complete source-to-running-code test with an active Docker daemon:

```sh
task test-e2e-docker
```

To print the complete generated JSON for the smallest Next.js example:

```sh
SHED_E2E_PRINT_PLAN=1 go test ./internal/e2e \\
  -run TestProjectDetectionAndPlanEndToEnd/nextjs-hello-world -v
```

To print the executable Shed definition generated from that plan:

```sh
SHED_E2E_PRINT_DEFINITION=1 go test ./internal/e2e \\
  -run TestProjectDetectionAndPlanEndToEnd/next-learn -v
```

Generate an inspectable archive through the public CLI:

```sh
task build
./shed testdata/e2e/repos/next-learn \\
  --dry-run \\
  --archive /tmp/next-learn.tar.gz \\
  --output json
tar -tzf /tmp/next-learn.tar.gz
```
