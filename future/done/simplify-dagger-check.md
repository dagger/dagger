# Simplify `dagger check`

## Goal

Make `dagger check` a local execution command again.

Today `dagger check` is intertwined with Dagger Cloud check-result lookup. Before
it runs local checks, it may infer a remote workspace address, query Dagger Cloud
for past Cloud Checks results, replay those results through the local TUI, and
exit with the replayed status instead of running anything locally.

This design removes that behavior from `dagger check`.

After the change:

```console
dagger check
dagger check go:lint
dagger check --skip '**e2e'
dagger check --failfast
dagger check --list
```

always operate on the selected workspace through the engine and the Checks API.
They do not query Dagger Cloud for previous Cloud Checks results.

This also removes these `dagger check` flags:

- `--past`
- `--no-past`
- `--run`

## Non-goals

This first pass is not a proposal to remove Cloud Checks as a product feature.

It also does not require removing the `dagger cloud check` management commands:

```console
dagger cloud check on
dagger cloud check off
dagger cloud check list
dagger cloud check status
```

Those commands configure Cloud-side automated checks for a workspace. They do not
need to be coupled to `dagger check` replay.

The main open product question is whether workspace commands should continue to
show Cloud Check annotations:

```console
dagger workspace remotes
dagger workspace activity
```

Those commands currently reuse the Cloud Check query helpers. Keeping them makes
this a focused CLI behavior simplification. Removing them turns this into a
larger Cloud Checks query-surface removal.

## Current behavior

The current `dagger check` implementation lives in
`internal/cmd/dagger/checks.go`.

`runChecksCommand` does this:

1. If not in `--list` mode and no `--skip` patterns were provided, call
   `maybeReplayPastChecks`.
2. `maybeReplayPastChecks` skips replay only when `--no-past` is set.
3. It calls `checkPastWorkspaceAddress` to find a remote workspace address.
4. If a remote address exists, it calls
   `cloudCLI.loadCloudCheckRowsForWorkspace`.
5. If matching Cloud rows exist, it calls `cloudCLI.replayCloudCheckResult`.
6. Unless `--run` was set, replay stops local execution.
7. If replay is not possible, `runChecksCommand` falls through to
   `runChecksNow`.

The effect is that a plain `dagger check` can become a Cloud query and local
telemetry replay instead of a local check run.

The `--past`, `--no-past`, and `--run` flags exist only to control that replay
branch:

- `--past` requires a past Cloud Checks result and fails if none is available.
- `--no-past` disables lookup and forces local execution.
- `--run` replays a past result but still runs local checks afterward.

`--past` is also mutually exclusive with `--skip` because past Cloud results
cannot be filtered by skip patterns in the current implementation.

## Code inventory

### Direct `dagger check` code

`internal/cmd/dagger/checks.go` contains the pieces that should be removed or
simplified:

- package globals: `checksPast`, `checksNoPast`, `checksRun`
- flag registration for `--past`, `--no-past`, `--run`
- mutual exclusion for `past/no-past/run`
- mutual exclusion for `past/skip`
- `runChecksCommand` replay branch
- `maybeReplayPastChecks`
- `checkPastWorkspaceAddress`

After simplification, `runChecksCommand` runs the checks directly, with no
replay wrapper in front of it.

### Cloud replay helpers

`internal/cmd/dagger/cloud_checks.go` contains reusable Cloud Checks query,
selection, rendering, and replay code.

The replay-only pieces are:

- `CloudCLI.TryReplayCloudChecksForWorkspace`
- `CloudCLI.replayCloudCheckResult`
- `renderCloudCheckReplayBanner`
- `renderCloudCheckRows`
- `replayCloudChecks`
- `newCloudCheckReplayFrontend`
- `syntheticCloudCheckSpan`
- `cloudCheckSpanAttributes`
- `cloudSpanBounds`
- `extendBounds`
- `randomHexID`
- `cloudChecksHaveTraces`
- helpers only used by replay, once references are removed

Several helpers in the same file are also used by workspace commands:

- `CloudCLI.loadCloudCheckRowsForWorkspace`
- `CloudCLI.loadCloudCheckRowsAcrossUserOrgs`
- `cloudRowsForWorkspaceAddress`
- `cloudCheckRows`
- selector and row filtering helpers
- summary and emoji summary helpers

These should not be deleted in the minimal change while workspace commands still
show Cloud Check information.

### Cloud API client

`internal/cloud/checks.go` defines the Cloud GraphQL operations and data types:

- `Client.OrgChecks`
- `Client.UserChecks`
- `Client.ModuleChecks`
- `CheckCommit`
- `Check`
- related ref/event structs

The minimal `dagger check` change does not remove this file because workspace
commands still need `UserChecks` / `OrgChecks` data.

If the broader intent is to remove all CLI querying of Cloud check results, then
this file becomes a follow-up deletion candidate after checking for non-CLI
callers. In the current tree, the visible callers are in
`internal/cmd/dagger/cloud_checks.go`.

### Workspace command coupling

`internal/cmd/dagger/workspace.go` currently uses Cloud Check rows in two places:

- `WorkspaceRemotes` calls `annotateWorkspaceRemoteRows`, which calls
  `loadCloudCheckRowsAcrossUserOrgs` and renders a `CHECKS` column.
- `WorkspaceActivity` calls `loadCloudCheckRowsForWorkspace` and renders recent
  Cloud-derived activity rows.

If these are kept, most query and summary helpers remain.

If they are removed, the workspace command output and tests need a separate
design decision:

- `workspace remotes` can still enumerate refs from the engine's Git APIs, but
  the `CHECKS` column should be removed or left as `-`.
- `workspace activity` becomes meaningless without Cloud-derived check activity
  and should either be removed, renamed, or repointed at a different activity
  source.

## Proposed minimal change

### 1. Make `dagger check` always run checks

Drop the Cloud preflight from `runChecksCommand` so it runs checks
directly, folding in the old `runChecksNow` body (no separate wrapper):

```go
func runChecksCommand(cmd *cobra.Command, args []string) error {
    params := client.Params{ ... }
    // load the workspace and run/list checks locally
}
```

This keeps all existing local behavior:

- selected workspace support through the existing engine client
- check include patterns
- `--skip`
- `--failfast`
- `--list`
- `--no-generate`
- `--generate`
- current telemetry-driven TUI rendering

It removes the Cloud preflight and its fallback messages:

- `Cloud check lookup failed: ...; running checks now.`
- `No Cloud Checks result found for ...; running checks now.`
- `Replaying Cloud Checks result from ...`

### 2. Remove replay flags

Delete these flag registrations from `init` in `checks.go`:

```go
checksCmd.Flags().BoolVar(&checksPast, "past", false, ...)
checksCmd.Flags().BoolVar(&checksNoPast, "no-past", false, ...)
checksCmd.Flags().BoolVar(&checksRun, "run", false, ...)
```

Delete their backing globals:

```go
checksPast
checksNoPast
checksRun
```

Delete the associated mutual-exclusion groups:

```go
checksCmd.MarkFlagsMutuallyExclusive("past", "no-past", "run")
checksCmd.MarkFlagsMutuallyExclusive("past", "skip")
```

`--skip` no longer needs special handling. It becomes a normal local check
filter handled directly in `runChecksCommand`.

### 3. Delete `dagger check` replay helpers

Delete from `checks.go`:

- `maybeReplayPastChecks`
- `checkPastWorkspaceAddress`

Then remove imports that existed only for replay:

- `errors`
- possibly `strings`

Keep imports that are still needed by local check execution and listing.

### 4. Prune replay-only Cloud helpers

After removing `dagger check` callers, run a focused reference search:

```console
rg 'TryReplayCloudChecksForWorkspace|replayCloudCheckResult|replayCloudChecks|syntheticCloudCheckSpan|newCloudCheckReplayFrontend'
```

Delete functions that have no remaining callers.

Expected deletion candidates in `cloud_checks.go`:

- `TryReplayCloudChecksForWorkspace`
- `replayCloudCheckResult`
- `renderCloudCheckReplayBanner`
- `renderCloudCheckRows`
- `replayCloudChecks`
- `newCloudCheckReplayFrontend`
- `syntheticCloudCheckSpan`
- `cloudCheckSpanAttributes`
- `cloudSpanBounds`
- `extendBounds`
- `randomHexID`
- `cloudChecksHaveTraces`
- `checksFromRows`, if no remaining caller needs it
- `selectCloudCheckCommit`, if no remaining caller needs it
- `renderAmbiguousCloudChecks`, if no remaining caller needs it
- `cloudCheckSubject`, if only used by `selectCloudCheckCommit`
- `aggregateCloudResult`, if only used by replay

Expected import cleanup in `cloud_checks.go` after replay deletion:

- `crypto/rand`
- `encoding/hex`
- `io`
- `github.com/dagger/dagger/dagql/dagui`
- `github.com/dagger/dagger/dagql/idtui`
- `github.com/dagger/dagger/util/cleanups`
- possibly `sync` and `errgroup` only if cross-org loading changes too

Do not delete row-loading, filtering, and summary helpers if workspace commands
still need Cloud Check annotations.

### 5. Update tests

Delete tests that exist only for `dagger check --past` behavior:

- the `"past and skip are mutually exclusive"` subtest in
  `core/integration/checks_test.go`
- `TestCheckPastWorkspaceAddress` in `internal/cmd/dagger/workspace_test.go`

Delete replay-specific unit tests in `internal/cmd/dagger/cloud_checks_test.go`
when their target helpers are removed:

- `TestSyntheticCloudCheckSpanMarksCheckStatus`
- `TestCloudCheckReplayFrontendFollowsProgressMode`

Keep tests for row filtering and workspace summaries if workspace Cloud Check
annotations remain:

- `TestCloudCheckRowsAndSelectors`
- `TestCloudChecksEmojiSummary`
- workspace activity/remotes tests that assert Cloud summary rendering

Keep integration coverage that proves the remaining command shape:

- `dagger check --skip failing-*` still runs locally and passes.
- `dagger check -l` is unchanged.
- `dagger check --failfast` is unchanged.

No test asserts that `--past`, `--no-past`, and `--run` are now unknown flags.
They never shipped in a stable release, so cobra's default unknown-flag
handling is enough and a dedicated test would just be noise.

## Broader cleanup option

If the real product decision is "remove query results of Cloud checks from the
CLI entirely", extend the change beyond `dagger check`.

That broader version should also remove or redesign:

- Cloud Check `CHECKS` annotations in `dagger workspace remotes`
- `dagger workspace activity`, if it has no non-Cloud source
- `CloudCLI.loadCloudCheckRowsForWorkspace`
- `CloudCLI.loadCloudCheckRowsAcrossUserOrgs`
- `cloudRowsForWorkspaceAddress`
- `cloudCheckRows`
- row filtering, grouping, and summary helpers
- `internal/cloud/checks.go`, if no other package calls its APIs
- the associated tests in `cloud_checks_test.go` and workspace tests

This should be a separate explicit decision because it changes more user-visible
commands than the `dagger check` simplification requested here.

## Compatibility notes

This is a breaking CLI behavior change for users relying on:

- `dagger check --past`
- `dagger check --no-past`
- `dagger check --run`
- automatic replay from plain `dagger check`

The simpler mental model is the compatibility tradeoff:

- `dagger check` means "run checks now".
- Cloud Checks remain Cloud-managed automation.
- Workspace commands may still display Cloud state if we keep that surface.

If we want a migration path, the old flags can fail with tailored messages for
one release instead of becoming generic unknown flags immediately. That would
require leaving hidden/deprecated flag definitions in place and returning an
explicit error such as:

```text
dagger check --past has been removed; use Dagger Cloud to inspect past Cloud Checks results.
```

The cleanest implementation is to remove the flags outright unless the release
process requires a deprecation window.

## Suggested implementation sequence

1. Simplify `runChecksCommand` and remove `--past`, `--no-past`, `--run`.
2. Delete `maybeReplayPastChecks` and `checkPastWorkspaceAddress`.
3. Run `go test` or at least package tests to expose dead references.
4. Delete replay-only helpers from `cloud_checks.go`.
5. Update integration/unit tests for removed flags and removed helpers.
6. Run focused tests:

```console
go test ./internal/cmd/dagger
go test ./core/integration -run Checks
```

1. Run broader CLI checks if time allows.

## Expected end state

`dagger check` no longer imports product behavior from Cloud Checks. Its command
path is direct:

```text
CLI args -> CurrentWorkspace().Checks(...) -> list or run CheckGroup
```

Cloud Checks query code either remains scoped to workspace/cloud commands or is
removed in a separate broader cleanup.
