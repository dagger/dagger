# Convert all integration tests to use fixtures

author: shykes
created: 2026-05-14
updated: 2026-05-15
after: SDK-specific tests removed from core

## Context

This cleanup should happen after the module-management command removal and
SDK-specific test deletion in this branch.

As of 2026-05-15, the module-management CLI implementation has been removed and
replacement core coverage has been added. SDK-specific authoring tests have been
removed from core and archived in `future/sdk-tests.md`.

The goal here is to convert the remaining core integration tests so they no
longer create Dagger modules dynamically as test setup.

## Goal

Make core integration tests fixture-based:

- no setup helper should call `dagger module init`
- no setup helper should call `dagger develop`
- no setup helper should call `dagger module install`
- no setup helper should call `dagger module update`
- no setup helper should call root module-dependency alias `dagger uninstall`
- no setup helper should synthesize a module by hand unless the synthetic files
  are a checked-in fixture

Tests should mount or copy prebuilt fixture modules from
`core/integration/testdata` and then exercise the behavior under test.

## Handoff Status

As of 2026-05-15, this migration is in progress on branch `workspace`.

Last code checkpoint:

- `ff00fd8a0 test: convert runtime parent secret fixtures`

Current strategy:

- Finish the fixture conversion pass first.
- Do not spend time on slow live integration runs until the conversion
  inventory is clean.
- Keep committing small behavior groups.
- For each conversion group, run `gofmt` and the cheap compile check:

  ```bash
  go test ./core/integration -run '^$'
  ```

- After the conversion pass, run targeted live Dagger integration tests with
  the harness described in [Verification](#verification).

Validation so far:

- Every committed conversion batch through `ff00fd8a0` was formatted.
- `go test ./core/integration -run '^$'` passed after the committed batches.
- The uncommitted `module_runtime_behavior_test.go` fixture conversion was
  formatted and `go test ./core/integration -run '^$'` passed.
- The uncommitted `module_python_test.go`, `module_typescript_test.go`, and
  `client_generator_test.go` fixture conversions were formatted and
  `go test ./core/integration -run '^$'` passed.
- Slow live integration runs were paused after the conversion-first direction.

Shared fixture helpers already added in `core/integration/module_helpers_test.go`:

- `moduleFixture`
- `moduleEntrypointFixture`
- `withModuleFixture`
- `withModuleEntrypointFixture`
- `withWorkspaceFixture`
- `fixtureJoin`
- `withTestdataFixture`
- `withTestdataFile`
- `copyTestdataFixture`
- `goGitBase`

Committed conversion areas so far:

- shared fixture helpers
- Go integration tests
- secret integration tests
- cache integration tests
- module self-call tests
- workspace integration tests
- dang dependency setup
- isolated Go fixtures
- legacy fixture setup, partially
- module error fixtures
- persistence and MCP fixtures
- module dependency runtime fixtures
- workspace compatibility environment fixtures
- CA cert module fixtures, partially
- interface, constructor, source map, validation, definition, and service
  module fixtures
- custom SDK fixtures
- benchmark module fixtures
- cross-session fixtures
- shell fixtures
- runtime secret and runtime parent-field fixtures
- module runtime behavior fixtures, uncommitted
- module call, path input, config, and type fixtures, uncommitted
- module Python, TypeScript, and client generator fixtures, uncommitted

Current broad inventory from the worktree after the uncommitted large module
runtime/schema and SDK/client-generator fixture conversions:

```text
core/integration/container_test.go:1
core/integration/module_terminal_test.go:7
core/integration/cacert_test.go:1
core/integration/module_up_test.go:1
core/integration/envfile_test.go:3
core/integration/module_helpers_test.go:5
core/integration/workspace_compat_test.go:1
core/integration/legacy_test.go:35
core/integration/gitcredential_test.go:3
core/integration/module_deprecation_test.go:3
core/integration/workspace_selection_test.go:3
core/integration/client_test.go:1
```

The broad inventory is intentionally conservative. Inspect each hit before
converting: some tests may be authoring-only coverage that should move out of
core instead of becoming a fixture.

Recommended next order:

1. Convert or move the smaller host-CLI one-offs:

   - `legacy_test.go`
   - `module_terminal_test.go`
   - `envfile_test.go`
   - `gitcredential_test.go`
   - `workspace_selection_test.go`
   - `workspace_compat_test.go`
   - `module_deprecation_test.go`
   - `module_up_test.go`
   - `container_test.go`
   - `client_test.go`
   - remaining `cacert_test.go`

2. Delete or shrink the dynamic helpers in `module_helpers_test.go` once no
   tests depend on them.

## Why

1. Removed module-management commands

   Dynamic module creation makes the core test suite depend on commands that
   are being removed from the CLI. It also makes test setup inconsistent: some
   tests already use checked-in fixtures, while others build equivalent modules
   on the fly.

2. Avoid an unnecessary setup refactor

   Module execution is expected to stop running codegen. If tests keep
   dynamically creating modules, every setup path would need to be refactored
   to run codegen explicitly before the module can execute. That refactor would
   only preserve a setup style we want to remove anyway. Fixture-based tests
   avoid the throwaway work: codegen for fixture modules can be managed like
   codegen elsewhere in the repo, with generated files checked in or refreshed
   by the normal repo codegen process.

3. Faster e2e tests

   Running and re-running module setup has runtime cost. Reusing checked-in
   fixtures avoids repeated scaffold, dependency, and generation work during
   integration tests. Any reduction in e2e test time is valuable.

After this cleanup, test failures should point at the behavior under test, not
at module scaffolding setup.

## Non-Goals

Do not delete behavior coverage merely because it currently uses dynamic module
setup. If the behavior belongs in core, keep it and convert the setup.

Do not recreate SDK authoring workflows in core fixtures. SDK authoring
behavior belongs in SDK-as-module repos; see `future/sdk-tests.md`.

Do not introduce a generic string-templating module factory that recreates
`module init` under another name. Prefer explicit, checked-in fixtures.

## Existing Fixture Shape

The repository already has fixture modules under:

- `core/integration/testdata/modules`
- `core/integration/testdata/checks`
- `core/integration/testdata/generators`
- `core/integration/testdata/services`
- `core/integration/testdata/sdks`
- `core/integration/testdata/test-blueprint`

New fixtures should generally extend this tree instead of creating a separate
fixture root.

Recommended layout for new module fixtures:

```text
core/integration/testdata/modules/<sdk>/<fixture-name>/
  dagger.json
  <sdk-owned source files>
  generated files, if the test needs checked-in generated output
```

Use more specific roots such as `services`, `checks`, or `generators` when the
fixture is tied to that feature area rather than to a generic SDK module.

## Helper Migration

Replace dynamic setup helpers with fixture-mount helpers.

Current dynamic helpers to remove or shrink:

| Helper | Current problem | Future replacement |
|---|---|---|
| `modInit` | runs `dagger module init` | mount a fixture module |
| `withModInit` | runs `dagger module init` | mount/write from fixture |
| `withModInitAt` | runs `dagger module init` at a path | mount fixture at target path |
| `daggerInitPython` | runs `dagger module init --sdk=python` | mount Python fixture |
| `daggerInitPythonAt` | runs Python init at a path | mount Python fixture at target path |
| ad hoc `With(daggerExec("module", "init", ...))` | dynamic module setup | add/mount fixture |
| ad hoc `With(daggerExec("module", "install", ...))` | dynamic dep setup | predeclare fixture dependency config |

Add small fixture helpers that make test intent explicit. Examples:

```go
func withModuleFixture(t testing.TB, c *dagger.Client, dst, fixture string) dagger.WithContainerFunc
func moduleFixture(t testing.TB, c *dagger.Client, fixture string) *dagger.Container
func withWorkspaceFixture(t testing.TB, c *dagger.Client, dst, fixture string) dagger.WithContainerFunc
```

The helper names can differ, but they should make these choices obvious:

- source fixture path
- destination path in the test container
- whether the fixture is a standalone module or a workspace
- whether generated files are included

## Fixture Rules

Fixtures should be stable, readable, and minimal.

- Keep one fixture per behavior shape, not one fixture per test when a fixture
  can be safely shared.
- Keep fixtures small enough that the behavior under test is obvious.
- Prefer committed `dagger.json` and dependency config over mutating config in
  setup.
- Keep generated files only when the test needs generated files as input.
- If a test verifies regeneration or codegen diffs, that behavior probably
  belongs in an SDK-as-module repo, not core.
- Avoid tests that modify a shared fixture in place. Mount the fixture into the
  container and mutate the container copy.
- Use descriptive fixture names such as `go/local-dep-parent`,
  `python/pip-lock`, or `typescript/other-module-types`.

## Migration Order

1. Inventory all remaining dynamic module setup calls:
   - `daggerExec("module", "init", ...)`
   - `daggerExec("develop", ...)`
   - `daggerExec("module", "install", ...)`
   - `daggerExec("module", "update", ...)`
   - `daggerExecRaw("uninstall", ...)`
   - `modInit`
   - `withModInit`
   - `withModInitAt`
   - `daggerInitPython`
   - `daggerInitPythonAt`
2. Group usages by behavior area: runtime, schema, workspace, cache,
   cross-session, services, legacy, shell, client generation.
3. For each group, decide whether an existing fixture already covers the setup.
4. Add missing fixtures under `core/integration/testdata`.
5. Replace dynamic setup calls with fixture helpers.
6. Delete or shrink dynamic setup helpers once no tests depend on them.
7. Run targeted integration packages after each group, then run the broader
   suite.

Use the broad scanner below while finishing the cleanup. It catches helper
calls, nested Dagger exec helpers, host-side Dagger commands, and simple
container `dagger module ...` execs.

```bash
rg -n \
  -e 'dagger(NonNested)?Exec(Raw)?\([^)]*"module",\s*"(init|install|update)"' \
  -e 'hostDagger(Exec|Command)\([^\n]*"module",\s*"(init|install|update)"' \
  -e 'workspaceSelectionDaggerExec\([^)]*"module",\s*"(init|install|update)"' \
  -e 'WithExec\(\[\]string\{"dagger",\s*"module",\s*"(init|install|update)"' \
  -e 'dagger(NonNested)?Exec(Raw)?\([^)]*"(develop|uninstall)"' \
  -e 'hostDagger(Exec|Command)\([^\n]*"(develop|uninstall)"' \
  -e '\b(modInit|withModInit|withModInitAt|daggerInitPython|daggerInitPythonAt)\b' \
  core/integration
rg -c \
  -e 'dagger(NonNested)?Exec(Raw)?\([^)]*"module",\s*"(init|install|update)"' \
  -e 'hostDagger(Exec|Command)\([^\n]*"module",\s*"(init|install|update)"' \
  -e 'workspaceSelectionDaggerExec\([^)]*"module",\s*"(init|install|update)"' \
  -e 'WithExec\(\[\]string\{"dagger",\s*"module",\s*"(init|install|update)"' \
  -e 'dagger(NonNested)?Exec(Raw)?\([^)]*"(develop|uninstall)"' \
  -e 'hostDagger(Exec|Command)\([^\n]*"(develop|uninstall)"' \
  -e '\b(modInit|withModInit|withModInitAt|daggerInitPython|daggerInitPythonAt)\b' \
  core/integration
```

The narrower original scanner is still useful for the main helper patterns, but
it misses host-side command setup and `daggerNonNestedExec`:

```bash
rg -n \
  -e 'daggerExec(Raw)?\([^)]*"module",\s*"(init|install|update)"' \
  -e 'daggerExec(Raw)?\([^)]*"(develop|uninstall)"' \
  -e '\b(modInit|withModInit|withModInitAt|daggerInitPython|daggerInitPythonAt)\b' \
  core/integration
rg -c \
  -e 'daggerExec(Raw)?\([^)]*"module",\s*"(init|install|update)"' \
  -e 'daggerExec(Raw)?\([^)]*"(develop|uninstall)"' \
  -e '\b(modInit|withModInit|withModInitAt|daggerInitPython|daggerInitPythonAt)\b' \
  core/integration
```

## Keep In Core

The following categories should stay in core and use fixtures:

- engine and `ModuleSource` semantics
- SDK-neutral schema behavior
- runtime behavior
- local dependency runtime behavior
- workspace selection and workspace loading behavior
- cache and cross-session behavior
- services, secrets, and networking behavior
- shell behavior that is not about removed module-management commands
- legacy compatibility behavior

## Move Out Instead

If a test is mainly about authoring or maintaining a module, move or adapt it
out of core instead of fixture-converting it here.

Examples:

- creating a new SDK module
- generating SDK code
- refreshing generated bindings
- adding, removing, or updating module dependencies as an authoring operation
- SDK-specific package manager detection
- SDK-specific bootstrap templates

For Go, move these to `github.com/shykes/dagger-go-sdk`. For other SDKs, move
them to the corresponding SDK-as-module repo when it exists.

## Verification

`core/integration` is not run directly with native `go test`. Use the Dagger
test harness:

```bash
dagger call engine-dev test --pkg=./core/integration --run='<target>' --test-verbose
```

Prefer targeted runs while converting each group. The development branch may
have unrelated failures, so compare failures against the touched test area
before treating them as fixture-conversion regressions.

## Done Criteria

This cleanup is complete when:

- no remaining core integration setup calls removed module-management commands
- dynamic module setup helpers are gone or no longer call removed commands
- remaining core tests use checked-in fixtures for modules and workspaces
- the relevant core integration test suites pass with fixture-based setup
