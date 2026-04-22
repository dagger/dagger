# `dagger migrate` Settings Hints Investigation

Date started: 2026-04-20

## Problem

`dagger migrate` is supposed to write commented-out workspace settings hints into the generated `.dagger/config.toml`. The hints should be derived by inspecting every module in the migrated workspace and enumerating the arguments of that module's constructor.

Recent QA against a fresh clone of `https://github.com/dagger/dagger` showed no hint comments in the resulting config file after running `dagger migrate`, even though this branch has code and tests intended to cover the feature.

## Expected Behavior

For migrated modules with constructor args, `.dagger/config.toml` should include commented examples near each module section, for example:

```toml
[modules.go]
source = "../toolchains/go"

# settings.source = "./path" # Directory
# settings.version = "1.26" # string
```

If a `[modules.<name>.settings]` section already exists, hints should be inserted inside that section without the `settings.` prefix.

## QA Repro

User-reported repro:

```console
git clone https://github.com/dagger/dagger
cd dagger
dagger migrate
cat .dagger/config.toml
```

Observed: no `# settings...` hint comments appear in `.dagger/config.toml`.

Expected: at least modules with Go constructors, such as `toolchains/go`, should produce hints.

## Current Intended Code Path

- `cmd/dagger/migrate.go` calls the `Workspace.migrate` API and applies the returned changeset.
- `core/schema/workspace_migrate.go` plans migration with `workspace.PlanMigration`.
- The planned config bytes are parsed with `workspace.ParseConfig`.
- If the planned config contains migrated module sources under `.dagger/modules/...`, `workspaceMigrationPreparedDirectories` prepares an in-memory migrated directory so the main migrated module can be introspected before files are written.
- `collectWorkspaceSettingsHintsFromConfig` in `core/schema/workspace_settings_hints.go` iterates the planned modules and introspects each module constructor.
- If any hints are found, `workspace.UpdateConfigBytesWithHints` injects them into `plan.WorkspaceConfigData`.
- The changeset then writes the updated planned config.

## Relevant Files

- `cmd/dagger/migrate.go`: CLI wrapper for migration.
- `core/schema/workspace_migrate.go`: engine migration API; calls hint collection and rewrites planned config bytes.
- `core/schema/workspace_settings_hints.go`: module introspection and constructor arg to hint conversion.
- `core/workspace/config_document.go`: TOML rendering and comment insertion.
- `core/workspace/migrate.go`: pure migration plan construction.
- `core/workspace/legacy.go`: legacy `dagger.json` to workspace config projection.
- `core/integration/workspace_migration_test.go`: integration coverage for migrated settings hints.
- `core/workspace/config_test.go`: unit coverage for TOML hint insertion.
- `hack/designs/workspace.md`: design context for migration behavior.

## Current Test Coverage

Existing integration tests include:

- `local migrated modules include commented setting hints`
- `dot dagger source keeps toolchain and migrated main module hints`

Those tests use local fixture modules and assert strings such as:

```toml
# settings.greeting = "hello" # string
# settings.password = "env://MY_SECRET" # Secret
# settings.count = 42 # int
```

Existing unit tests in `core/workspace/config_test.go` verify that `UpdateConfigBytesWithHints` can insert hints under module sections and inside settings sections.

## Initial Observations From This Checkout

- Root `dagger.json` is still a legacy config with `source = ".dagger"` and many toolchains.
- There is no existing `.dagger/config.toml` in this checkout.
- Follow-up information: hints are generated in normal migration cases. The repro appears tied to cases where the pre-migration project module itself cannot load, such as an engine version mismatch. In that state, `dagger migrate` still completes, but hint generation failures are not visible in the default CLI output.
- The real `dagger/dagger` migration shape differs from the fixture tests:
  - Main module source becomes `modules/dagger-dev`, which resolves to `.dagger/modules/dagger-dev` from the config directory.
  - Toolchain modules become sources like `../toolchains/go`, which resolve from `.dagger/config.toml` back to repo-root `toolchains/go`.
  - Some toolchains use non-Go or remote SDKs, e.g. Dang SDK modules, and may fail introspection differently than the simple fixtures.
- `toolchains/go/main.go` has a constructor with many args, so at least the `go` module should produce hints if introspection reaches it.
- The hint collection path logs introspection failures with `slog.Warn` and continues. This means a broad introspection failure can silently produce no hints in normal CLI QA.

## Leading Hypotheses

1. The migrated main module can fail constructor introspection, for example because its legacy `engineVersion` is incompatible with the current engine. These failures are logged with `slog.Warn`, but are not surfaced in the default `dagger migrate` UX.
2. The prepared in-memory migrated directory used for `.dagger/modules/...` sources may not match the real repo shape closely enough for the migrated main module to load.
3. Relative source resolution may behave differently for the real repo than in the fixture tests, especially around sources written relative to `.dagger/config.toml` such as `../toolchains/go`.
4. One failing module or SDK dependency may be poisoning the hint introspection context, even though the current code appears to continue per module.
5. The integration tests verify small happy paths but do not exercise the real `dagger/dagger` legacy config, the large toolchain set, or remote SDK/Dang modules.

## Trace Observation: Duplicate `Workspace.migrate` Spans

User observed at least two sibling `Workspace.migrate` spans in the TUI / trace visualizer when running `dagger migrate`.

This is explainable from the current CLI/API shape:

- `cmd/dagger/migrate.go` first calls `dag.CurrentWorkspace().Migrate().ID(ctx)`.
- It then reloads the migration with `dag.LoadWorkspaceMigrationFromID(migrationID)`.
- Later calls such as `migration.Changes().ID(ctx)`, `changes.IsEmpty(ctx)`, `migration.Steps(ctx)`, patch preview, and export can load IDs whose receiver chain includes the original `Workspace.migrate` call.
- Loading an object by ID replays the ID's call chain through `dagql.Server.Load`.
- `Workspace.migrate` is marked `DoNotCache("Plans workspace migration against live host filesystem")`, so each replay can produce another real execution/span instead of a quiet cache hit.

So duplicate sibling spans are probably normal for the current implementation and not necessarily a TUI bug. They are still worth tracking because migration planning reads live host state and duplicate executions can hide which exact plan produced the final changeset. This could matter if one execution collects hints and another does not.

## Next Investigation Steps

1. Reproduce in an isolated clone or container and save the generated `.dagger/config.toml`, stderr, and any engine logs that include `could not introspect constructor args for workspace settings hints`.
2. Add temporary instrumentation or a narrow test helper around `collectWorkspaceSettingsHintsFromConfig` to report per-module outcomes for the real root `dagger.json`.
3. Verify that `introspectConfiguredModuleArgs` resolves the real planned sources as expected:
   - `modules/dagger-dev` -> `.dagger/modules/dagger-dev` -> prepared migrated directory
   - `../toolchains/go` -> repo-root `toolchains/go`
4. Check whether `constructorHintsFromModule` sees a valid main object constructor for `toolchains/go`.
5. Add a regression test based on the real `dagger/dagger` legacy config shape, preferably minimized to the smallest module set that reproduces no hints.
6. Decide whether hint introspection failures during migration should surface as migration warnings, debug output, or test-only diagnostics instead of disappearing into `slog.Warn`.
7. Decide whether `dagger migrate` should avoid repeated `Workspace.migrate` planning by fetching the needed migration fields in one request, returning a changeset directly, caching the migration plan for the command, or otherwise avoiding ID reloads that replay the non-cacheable call.

## Open Questions

- Does `dagger migrate` generate zero hints for every module, or are hints generated and then lost during later config rendering or changeset application?
- Are warnings emitted anywhere accessible during the failing QA run?
- Does the failure reproduce with only the `go` toolchain from the real repo, or only with the full toolchain set?
- Does the problem require a fresh remote clone, or does it reproduce in this local checkout with the current dirty branch?
- Do Dang SDK toolchains affect the shared introspection context for subsequent modules?

## Current Status

Patch in progress:

- `core/schema/workspace_settings_hints.go` now returns migration warnings for failed config-driven hint introspection instead of only logging them.
- `core/schema/workspace_migrate.go` now fails migration by default when modules cannot be loaded to generate settings hints, with a simple message: `could not load modules to generate settings hints; use --force to migrate anyway`.
- `cmd/dagger/migrate.go` adds `-f, --force` and passes it to `Workspace.migrate(force: true)`. In force mode, hint failures become migration warnings and any successfully collected hints are still written.
- `core/integration/workspace_migration_test.go` has a regression case where a migrated main module has an incompatible future engine version; default migration should fail, while forced migration should still write toolchain hints and print a warning for the skipped main-module hints.

Verification so far:

- `go test ./core/workspace` passes locally.
- Linux compile-only checks pass with `GOCACHE=/tmp/go-build-dagger-workspace GOOS=linux GOARCH=amd64 go test -c ./core/schema -o /tmp/schema.test`, plus the same command for `./core/integration` and `./cmd/dagger`.
- A focused Dagger-run integration attempt reached the new subtest but failed before exercising migration because the nested test CLI rejected `--skip-workspace-modules`; that looks like a local toolchain/version mismatch in the test harness.
