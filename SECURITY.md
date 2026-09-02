# Security

## Reporting a vulnerability

Report vulnerabilities privately through [GitHub security advisories](https://github.com/shed-sh/cli/security/advisories/new); please don't open a public issue for anything exploitable. Include the version (`shed version`), the platform, and steps to reproduce.

## What Shed trusts, and what it doesn't

Knowing the trust model helps aim a report.

The project directory is semi-trusted. `shed deploy` executes the build recipe a `SHED.yaml`/`SHED` file declares; that is its job, like `make`. But the declared fields must be the only channel. The Starlark evaluator exposes no filesystem, network, or subprocess access, packaging refuses symlinks so a repo cannot pull in files outside itself, and archive extraction validates entry names and accepts regular files only. A repo that achieves anything its manifest doesn't declare is a vulnerability.

The control plane is authenticated but its content is not trusted. Responses from the Shed backend are parsed defensively, and tokens are keyed to the API URL in the OS keyring and never logged.

Environment variables and flags are trusted. They are the operator's channel, so attacks that require controlling them are out of scope.

## Release integrity

Release archives are published with per-artifact SHA-256 digests, and a post-release workflow installs through the public channels and checks published digests against the artifacts they describe. Install scripts and `shed upgrade` fetch over HTTPS only and refuse protocol downgrades on redirects.
