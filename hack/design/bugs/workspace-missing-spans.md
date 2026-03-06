# Workspace Missing Spans In TUI

## Status

Done

## What

`dagger functions` (and likely similar low-argument commands) shows too little progress in TUI at default verbosity on dev builds.

Observed symptom:

- After a few seconds, only `connect` is visible (often with `0.0s`), giving the impression that nothing is happening.
- Increasing verbosity eventually reveals spans that are otherwise hidden.

Expected behavior:

- At default verbosity, users should still see meaningful progress spans for expensive steps like module/workspace loading and type-definition loading.

## Why (current hypotheses)

1. Workspace-first module loading moved expensive work into span paths that are currently hidden at default verbosity.
2. Span attributes/encapsulation/internal tagging changed relative to `v0.20.0`, which altered default render visibility.
3. The CLI path for `dagger functions` may no longer emit top-level user-facing spans during the period that actually takes time.

## Task List

- [x] Create bug tracking doc with problem statement and hypotheses.
- [x] Reproduce on current dev with a controlled scenario and capture span output differences.
- [x] Identify the code path where spans are produced (CLI + engine) for `dagger functions`.
- [x] Find the specific visibility regression (span level/tagging/parenting).
- [x] Implement a minimal fix that restores meaningful default-verbosity progress.
- [x] Add/adjust tests (where feasible) for span metadata/visibility behavior.
- [x] Re-run repro and confirm visual behavior is restored.
- [x] Summarize root cause and fix details in this document.

## Investigation Log

- 2026-02-28: Opened bug doc and captured initial hypotheses and execution plan.
- 2026-02-28: Reproduced on dev playground path (`cd src/dagger && dagger functions`) where UI showed only `connect` at default verbosity while work continued.
- 2026-02-28: Traced CLI path:
  - Old behavior (`v0.20.0`) used `initializeDefaultModule`/`initializeModule`, which emits a visible top-level span `load module: <ref>`.
  - Current workspace-first behavior uses `initializeWorkspace`, which does not emit an equivalent visible top-level span.
- 2026-02-28: Identified visibility regression mechanism:
  - Most work is under spans marked encapsulated/internal (hidden until higher verbosity in `dagql/dagui/spans.go` + `dagql/dagui/opts.go` rules).
  - Without a non-encapsulated top-level span in this path, default verbosity can look idle even while module/workspace loading is running.
- 2026-02-28: Implemented candidate fix in `cmd/dagger/module_inspect.go`:
  - Added a non-encapsulated top-level span around `initializeWorkspace`: `load workspace`.
  - Goal: restore visible default-verbosity progress for `dagger functions`/`dagger call` workspace path.
- 2026-02-28: Validation:
  - Compile/smoke: `dagger call go --source=. test --pkgs=./cmd/dagger --run TestResolveLockMode` passed.
  - Playground visual repro rerun (`cd src/dagger && dagger functions`) now shows:
    - `load workspace` (visible at default verbosity)
    - nested `loading type definitions`
    - nested `currentWorkspace` / `defaultModule` spans
  - This removes the “only connect 0.0s” idle-looking state during long waits.
- 2026-02-28: Baseline check against `origin/main`:
  - Ran playground with `DAGGER_MODULE=github.com/dagger/dagger@main`.
  - Output still shows `load module: .` + child spans at default verbosity.
  - Conclusion: the regression is not on current `main`; it appears in the workspace-first branch path where `initializeDefaultModule` was replaced by `initializeWorkspace`.
- 2026-02-28: Implemented hierarchy-oriented engine spans:
  - In `engine/server/session.go`, `ensureModulesLoaded` now wraps each module load with visible spans:
    - `load module: <name-or-ref>` for workspace/implicit modules
    - `load extra module: <name-or-ref>` for `-m` modules
  - Each of these spans uses `telemetry.Encapsulate()` so internal resolver steps stay hidden by default.
- 2026-02-28: Reduced call-level UI noise:
  - In `dagql/dagui/opts.go`, default skip list now hides:
    - `Query.currentWorkspace`
    - `Workspace.defaultModule`
  - This removes helper-call traces that are implementation details for CLI startup.
- 2026-02-28: Final validation on patched branch via playground:
  - Command: `cd src/dagger && dagger functions`
  - Default verbosity now shows:
    - `connect`
    - `load workspace`
      - many `load module: <workspace module>` rows
      - `loading type definitions`
  - `currentWorkspace` and `defaultModule` are no longer shown.

## Root Cause

Two issues combined:

1. Workspace-first CLI initialization (`initializeWorkspace`) lacked an equivalent to the old visible `load module:*` startup signal, so default verbosity could look idle.
2. Workspace module loading in engine had no user-facing per-module spans, while helper GraphQL calls (`currentWorkspace/defaultModule`) were visible, producing poor signal-to-noise.

Before workspace-first flow, module-first initialization naturally exposed `load module:*`, so startup progress looked active and relevant.

## Fix

Three targeted changes:

- file: `cmd/dagger/module_inspect.go`
  - add visible top-level span `load workspace` in `initializeWorkspace`
- file: `engine/server/session.go`
  - add visible module-loading spans in `ensureModulesLoaded`:
    - `load module: <name-or-ref>`
    - `load extra module: <name-or-ref>`
  - mark each with `telemetry.Encapsulate()` to hide internal call trees
- file: `dagql/dagui/opts.go`
  - suppress helper-call spans in default TUI filtering:
    - `Query.currentWorkspace`
    - `Workspace.defaultModule`

Result: default verbosity emphasizes user-intent progress (workspace/module loading) rather than low-level GraphQL plumbing.

## Test Notes

No stable, low-cost automated assertion was found for this visual TTY rendering regression in this pass. Validation was done through:

1. engine compile/smoke in Dagger:
   - `dagger call go --source=. test --pkgs=./engine/server --run TestDoesNotExist` (pass)
2. dagui compile check:
   - `go test ./dagql/dagui -run TestDoesNotExist` (pass)
3. manual playground repro before/after:
   - `cd src/dagger && dagger functions`

## UI Hierarchy Workshop

Current candidate output hierarchy (patched branch):

- `connect`
- `load workspace`
  - `load module: <workspace-module-a>`
  - `load module: <workspace-module-b>`
  - `load module: <workspace-module-c>`
  - ...
  - `loading type definitions`

Desired default hierarchy (user feedback):

- `connect`
- `load workspace`
  - `load module: <workspace-module-a>`
  - `load module: <workspace-module-b>`
  - `load module: <workspace-module-c>`
- `load extra module: <module-from--m>`

Options to refine:

Implementation direction:

1. Keep `connect` and `load workspace` visible.
2. Add visible per-module spans at module load choke points (`load module: ...`, `load extra module: ...`).
3. Encapsulate/hide call-level scaffolding (`currentWorkspace`, `defaultModule`, type-introspection plumbing) at default verbosity.

Goal:

- default verbosity shows user-intent progress, not `strace`-like internals.
