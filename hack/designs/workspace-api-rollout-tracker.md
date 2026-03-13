# Workspace API Rollout Tracker

## Status: Temporary Working Note

This file is a temporary implementation tracker.

It is **not** the canonical Workspace API spec.

Canonical contract source:

- the `workspace` PR description

This tracker exists to coordinate the rollout across `workspace` and
`workspace-plumbing` without copying the full spec into multiple docs.

Delete or fold this file away once the path contract is implemented and reflected in
the long-lived design/docs surface.

## Why This Exists

We are settling the design of:

- the Workspace API path model
- filesystem access semantics
- user-facing terminology

The current `workspace-plumbing` test failures are downstream of that design seam.
They should not define the contract by accident.

## Agreed Contract

### Terminology

| User-facing term | Meaning |
|------------------|---------|
| Workspace directory | The selected workspace location. This may be explicit (`.dagger`) or detected by fallback. |
| Workspace boundary | The enclosing filesystem boundary inside which workspace paths are valid. Today this is detected from git root with fallback to workspace directory. |

### Path Semantics

| API surface | Meaning |
|-------------|---------|
| `.` | The workspace directory |
| relative paths | Paths relative to the workspace directory |
| `/` | The workspace boundary |
| absolute paths | Paths relative to the workspace boundary |
| `ws.path` | Workspace directory path relative to the workspace boundary |
| `ws.address` | Canonical Dagger address of the workspace |

### Explicit Non-Goals For This Batch

- no public `ws.root`
- no second runtime compat path for legacy loading
- no rollback of `check` / `generate` away from workspace traversal
- no CLI-owned `dagger.json` mutation
- no test-driven redefinition of workspace path semantics

## Canonical Doc Strategy

Short term:

- the `workspace` PR description is the source of truth for the Workspace API contract
- branch-local docs reference it and record only local implications

Branch docs:

- `workspace`: focused design docs remain scoped sub-documents
- `workspace-plumbing`: `workspace-foundation-compat.md` records adoption and compat
  consequences only

## Progress Log

### 2026-03-13

- Completed rollout task `1`.
- Updated `workspace` PR [#11812](https://github.com/dagger/dagger/pull/11812) with a
  `Workspace API Path Contract` section.
- The canonical contract now explicitly defines:
  - `workspace directory`
  - `workspace boundary`
  - `.` / relative-path semantics
  - `/` / absolute-path semantics
  - `ws.path`
  - `ws.address`
  - no public `ws.root`

## Rollout Order

1. [x] Finalize the Workspace API contract in the `workspace` PR description.
   - terminology: workspace directory, workspace boundary
   - path semantics: `.` / relative vs `/` / absolute
   - metadata: `ws.path`, `ws.address`

2. Reflect that decision briefly in `workspace-plumbing`.
   - reference the `workspace` PR description as canonical
   - record plumbing-specific implications only

3. Implement the shared Workspace path semantics on `workspace`.
   - resolver behavior for relative vs absolute paths
   - `ws.path`
   - `ws.address`
   - unit tests for the contract
   - dogfood/integration updates as needed

4. Cherry-pick the shared implementation commits into `workspace-plumbing`.
   - cherry-pick only the genuinely shared path-contract commits
   - do not blindly transplant branch-specific glue or broad test churn

5. Adapt `workspace-plumbing` to the new contract.
   - wire the shared semantics into the current plumbing/session architecture
   - keep workspace traversal as the runtime path

6. Fix remaining plumbing regressions against the new contract.
   - first: legacy `defaultPath` resolution
   - then: generator include matching and remaining runtime mismatches

7. Resume the test campaign.
   - non-integration packages
   - `test-base`
   - broader integration slices

## Commit Strategy

Use `workspace` as the semantic source of truth.

Recommended stack on `workspace`:

1. docs/PR description: finalize the Workspace API contract
2. shared implementation: path semantics and unit tests
3. workspace-only glue: dogfood modules, branch-specific integration changes

Recommended stack on `workspace-plumbing`:

1. cherry-pick shared implementation commits
2. adapt plumbing-specific glue
3. fix compat/runtime regressions against the new contract

## Current Plumbing Focus

After the authoring-surface restore, the next important runtime bug is:

- legacy `defaultPath` loading through the Workspace API resolves against the wrong
  path model

Representative failures:

- `TestBlueprint/TestBlueprintUseLocal/use_local_blueprint`
- `TestToolchain/TestMultipleToolchains/install_multiple_toolchains`

Representative error:

```text
load contextual arg "config": load legacy default file "./app-config.txt":
workspace file "./app-config.txt": path "app" resolves outside root "/"
```

This bug should be fixed **after** the new Workspace path contract lands, not by
bending the contract around the legacy behavior.
