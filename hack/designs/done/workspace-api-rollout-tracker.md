# Workspace API Rollout Tracker

## Status: Archived

This temporary implementation tracker is complete.

Canonical contract source:

- the `workspace` PR description

The Workspace API path-contract rollout is done.

Active legacy-loading / compatibility tracking now lives in
[workspace-foundation-compat.md](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/workspace-foundation-compat.md).

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
- Completed rollout task `2`.
- Updated
  [workspace-foundation-compat.md](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/workspace-foundation-compat.md)
  with a short adoption note that references `workspace` PR `#11812` instead of
  duplicating the spec text.
- Started rollout task `3` analysis on `workspace`.
- Current `workspace` implementation still describes paths as:
  - relative => workspace root
  - absolute => rootfs root / sandbox root
- Current `workspace` tests still encode the old semantics, including the expectation
  that an absolute path like `/sub` resolves under the workspace root.
- `core.Workspace.Path` already exists and appears reusable for the target `ws.path`
  surface.
- `ws.address` is not exposed yet and will need to be added in the shared
  implementation pass.
- Workspace-side shared implementation is now in progress in a dedicated
  `workspace` worktree at
  `/Users/shykes/git/github.com/dagger/dagger_workspace_shared`.
- Shared implementation changes currently staged there:
  - `core/workspace.go`
    - add public `Address`
    - tighten `Path` doc to "workspace directory path relative to boundary"
  - `engine/server/session.go`
    - compute canonical local and remote workspace addresses
    - thread address population through workspace construction
  - `core/schema/workspace.go`
    - document the new contract on `directory`, `file`, and `findUp`
    - return absolute boundary-rooted paths from `findUp`
    - stop `findUp` at the workspace boundary
  - `core/schema/workspace_test.go`
    - unit tests for resolver behavior and public path formatting
  - `engine/server/session_lock_test.go`
    - focused tests for local/remote workspace address formatting
  - `core/integration/workspace_test.go`
    - update `findUp` expectations
    - add a nested-workspace contract test
- Verified green on the shared implementation:
  - `toolchains/engine-dev test --pkg=./core/schema --run='Test(ResolveWorkspacePath|WorkspaceAPIPath|MatchWorkspaceInclude)$'`
  - earlier focused `engine/server` address tests were green before the latest
    integration-helper iteration; reruns are still noisy and expensive
- New implementation detail discovered:
  - adding `Workspace.address` is a public schema change and will require generated
    schema/SDK updates on `workspace`
  - likely files:
    - `docs/docs-graphql/schema.graphqls`
    - `sdk/go/dagger.gen.go`
    - `sdk/typescript/src/api/client.gen.ts`
    - `sdk/python/src/dagger/client/gen.py`
    - `sdk/rust/crates/dagger-sdk/src/gen.rs`
    - `sdk/php/generated/Workspace.php`
- Current blocker while validating task `3` end-to-end:
  - the existing `workspace` integration helper pattern that scaffolds workspace
    modules under `.dagger/modules/<name>` is not a clean signal for this batch
  - path-contract tests that only need a callable module should use standalone module
    init instead of depending on workspace-module authoring
  - do not treat this helper/authoring mismatch as part of the Workspace path
    contract itself
- Completed rollout task `3` on `workspace`.
- Shared source commit on `workspace`:
  - `5e0b1e4a7` `workspace: adopt path contract`
- Cherry-picked the shared implementation into `workspace-plumbing`:
  - `bc8d8668e` `workspace: adopt path contract`
- The plumbing cherry-pick required two follow-up merge repairs before official
  generation could run cleanly:
  - `core/schema/workspace.go`
    - restored the missing `strings` import for `workspaceAPIPath`
  - `engine/server/session.go`
    - removed references to `workspace`-branch-only config state
      (`detected.Initialized`, `detected.Config`, `WorkspaceDirName`,
      `ConfigFileName`) while preserving the shared `ws.address` / `ws.path`
      contract on the public `core.Workspace`
- Regenerated the public surface on `workspace-plumbing` using official repo-root
  `dagger generate -y ...` functions, then trimmed unrelated stale-generator churn
  so this batch only carries the shared Workspace path-contract surface:
  - `docs/docs-graphql/schema.graphqls`
  - `docs/static/api/reference/index.html`
  - `docs/static/reference/php/Dagger/Workspace.html`
  - `sdk/go/dagger.gen.go`
  - `sdk/php/generated/Workspace.php`
  - `sdk/python/src/dagger/client/gen.py`
  - `sdk/rust/crates/dagger-sdk/src/gen.rs`
  - `sdk/typescript/src/api/client.gen.ts`
- Important note:
  - the regenerated plumbing outputs are not byte-identical to the `workspace`
    source commit, which confirms `workspace-plumbing` still has branch-specific
    public surface outside the shared path contract
  - for this batch, we are keeping only the Workspace-path-contract-related
    generated refresh instead of broad generator cleanup

## Rollout Order

1. [x] Finalize the Workspace API contract in the `workspace` PR description.
   - terminology: workspace directory, workspace boundary
   - path semantics: `.` / relative vs `/` / absolute
   - metadata: `ws.path`, `ws.address`

2. [x] Reflect that decision briefly in `workspace-plumbing`.
   - reference the `workspace` PR description as canonical
   - record plumbing-specific implications only

3. [x] Implement the shared Workspace path semantics on `workspace`.
   - resolver behavior for relative vs absolute paths
   - `ws.path`
   - `ws.address`
   - unit tests for the contract
   - dogfood/integration updates as needed

4. [x] Cherry-pick the shared implementation commits into `workspace-plumbing`.
   - cherry-pick only the genuinely shared path-contract commits
   - do not blindly transplant branch-specific glue or broad test churn

5. [~] Adapt `workspace-plumbing` to the new contract.
   - wire the shared semantics into the current plumbing/session architecture
   - keep workspace traversal as the runtime path
   - shared path contract and generated public surface are in place
   - remaining work is branch-specific runtime compat (`defaultPath`, generator
     include matching, and other ledger items)

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

Confirmed diagnosis on `workspace-plumbing` after the path-contract rollout:

- this is **not** a new mismatch in the Workspace API contract itself
- it is the pre-existing root-`/` sandbox bug in
  [pathutil.go](/Users/shykes/git/github.com/dagger/dagger_workspace/engine/client/pathutil/pathutil.go)
- in the failing integration container:
  - workspace boundary is `/`
  - workspace directory is `/app`
  - legacy `defaultPath="./app-config.txt"` resolves to boundary-relative
    `app/app-config.txt`
  - `currentWorkspace.file(...)` then asks `SandboxedRelativePath("app", "/")`
    for the parent directory
  - the helper incorrectly checks for a `"//"` prefix when `root == "/"`, so
    valid children like `/app` are rejected as escaping `/`
- the correct fix is therefore:
  - keep the new Workspace path contract unchanged
  - fix `SandboxedRelativePath(..., "/")`
  - add a focused unit test for the root-`/` case
  - then rerun the blueprint/toolchain regressions
- targeted integration reruns immediately exposed an unrelated stale branch-test
  blocker in
  [workspace_test.go](/Users/shykes/git/github.com/dagger/dagger_workspace/core/integration/workspace_test.go):
  - `gitBase` references no longer exist and should be `workspaceBase`
  - one helper still shells out through `dagger module init` instead of the
    restored top-level `dagger init`
  - this is test-only drift from the shared `workspace` cherry-pick, not part of
    the runtime fix itself
- after fixing the test-only blocker:
  - `TestBlueprint/TestBlueprintUseLocal/use_local_blueprint` is now green
  - the next remaining targeted failure is
    `TestToolchain/TestToolchainsWithConfiguration/override constructor defaultPath argument`
  - diagnosis: legacy toolchain constructor customizations are still only
    carrying primitive `default` values through `ConfigDefaults`; constructor
    `defaultPath` customizations are dropped during legacy parsing, so the
    module still uses its original `./app-config.txt` instead of the customized
    `./custom-config.txt`
  - fix direction:
    - keep the root-`/` sandbox fix
    - restore only the minimal legacy constructor-customization threading
      needed for `defaultPath` / `defaultAddress` / `ignore` on already-loaded
      modules
    - do not reintroduce a second module-loading path
  - current local remediation status:
  - parser cleanup completed:
    - `core/workspace/legacy.go` now unmarshals legacy `dagger.json` into the
      authoritative `core/modules.ModuleConfig` types instead of maintaining a
      private duplicate schema in the workspace package
    - `legacy.go` remains a narrow compat extractor only; it does not load
      modules or define an alternate config authority
    - this reduces schema drift risk while keeping workspace detection free of
      the stricter `modules.ParseModuleConfig(...)` version-compat gate
  - code changes are in progress in:
    - `core/workspace/legacy.go`
    - `engine/server/session.go`
    - `core/module.go`
    - `core/workspace/legacy_test.go`
    - `engine/server/session_test.go`
  - there is also one test-only compile fix in:
    - `core/integration/workspace_test.go`
    - this is stale helper drift from the earlier shared `workspace` cherry-pick,
      not part of the runtime compat design
  - current compile status of the local patch:
    - `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core/workspace`
      -> green
    - `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./engine/server`
      -> green
    - `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core`
      -> green
    - `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core/integration`
      -> green
    - `env GOCACHE=/tmp/go-build go test ./core/workspace -run TestParseLegacyPins -count=1`
      -> green
  - current validation gap:
    - the exact runtime repro for
      `TestToolchain/TestToolchainsWithConfiguration/override constructor defaultPath argument`
      still needs a decisive green/red result
    - direct host `go test ./core/integration ...` is blocked on Darwin by the
      known `engine/buildkit` Linux-only build constraints
    - the `toolchains/engine-dev` harness run gets past the old immediate
      failure, but is too slow/noisy for a fast validation loop
  - no commit should be made for this batch until that runtime validation is
    complete

This bug should be fixed **after** the new Workspace path contract lands, but
without bending the contract around the legacy behavior.
