# Vendored Railpack

- Repository: <https://github.com/railwayapp/railpack>
- Version: `v0.35.0`
- Commit: `03fddea9f8ef8ec94ffc0c165af1255bb880ac93`
- Imported: 2026-08-03
- License: MIT; see `LICENSE`

## Included

- Provider orchestration and validation from `core/`.
- `core/app`, `core/config`, `core/generate`, `core/logger`, `core/mise`,
  `core/plan`, `core/providers`, `core/resolver`, and `core/testing`.
- `internal/utils`, embedded provider assets, provider tests, and the examples
  referenced by those tests.

## Excluded

- BuildKit lowering and image export.
- Railpack CLI and presentation code.
- Documentation and examples not referenced by the retained provider tests.

## Local patches

- The application filesystem is confined with `os.Root` and records
  deterministic detection evidence.
- Provider orchestration exposes attempted-provider evidence by phase.
- Tests use an unresolved/offline package resolver and never download `mise`.
- Test generation reads idiomatic version files locally and asserts requested
  toolchains; upstream networked `mise` integration cases are skipped.
- Configuration reads use the confined application filesystem.
- The nested module manifest is pruned to the retained provider closure.
