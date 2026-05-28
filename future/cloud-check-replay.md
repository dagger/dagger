# Cloud Check Lookup, Replay, And Coordinate Listing

## Goal

`dagger check` with Cloud selectors should look up existing Dagger Cloud Checks
results, replay their traces through the normal Dagger TUI, and exit with the
stored result. It must not rerun checks.

Cloud selectors are provider and workspace-state dimensions such as
`github-repo`, `github-pr`, `git-branch`, `git-tag`, `git-sha`, and
`workspace`. Their presence switches `dagger check` into Cloud lookup mode.
Result and artifact selectors such as `check`, `go-test`, and `go-module`
narrow a selected Cloud result; by themselves, they do not change local
`dagger check` behavior.

`dagger list` should expose the same coordinate system as Artifacts and
Collections: dimensions are typed columns, selectors are repeatable dimension
flags, and listing is a projection over matching rows.

There is no public `gate` object and no public `matrix` object in this design.

## Coordinate Model

Dagger Cloud Checks history is a table of workspace-state check results.

Rows have coordinate dimensions:

```text
github-account
github-repo
github-pr
git-branch
git-tag
git-sha
workspace
check
```

Rows also carry result metadata:

```text
result
updatedAt
duration
traceID
traceURL
```

Field naming in this spec uses camelCase for proposed backend and JSON fields,
dotted snake case for OpenTelemetry attributes, and uppercase labels only for
human-readable table headers.

Later, Modules v2 artifact dimensions join naturally:

```text
type
go-module
go-test
package
service
```

Selector rules match Artifacts and Collections:

- Dimension names become flags: `--github-repo=acme/hello`, `--go-test=TestFoo`.
- Repeating the same dimension is OR: `--github-pr=4241 --github-pr=4242`.
- Different dimensions are AND: `--github-repo=acme/hello --github-pr=4242`.
- Canonical dimension names are singular: `check`, `go-test`, `github-pr`.
- Plural aliases may exist for ergonomics, but display and docs should prefer
  canonical names.

## `dagger check`

Local behavior stays unchanged when no Cloud selectors are present:

```console
$ dagger check
```

Cloud selector behavior is lookup and replay:

```console
$ dagger check --github-repo=acme/hello --github-pr=4242
Replaying Cloud Checks result from 2m ago
Workspace: github.com/acme/hello
Ref:       PR #4242 merge
SHA:       a1b2c3d
Result:    red

# normal completed TUI renders here
```

Narrowing to checks:

```console
$ dagger check --github-repo=acme/hello --github-pr=4242 --check=lint
```

If selectors match multiple workspace states, default to the latest result only
when there is an obvious logical subject, such as the latest SHA for PR 4242. If
ambiguity remains, do not replay. Print matching rows and ask the user to narrow
with more selectors.

Miss:

```console
No Cloud check result found for github-repo=acme/hello github-pr=4242.

Checks are normally ingested from GitHub webhooks. Proposed repair command:
  dagger integration sync github --repo=acme/hello --pr=4242
```

Exit code comes from the stored result:

```text
green -> 0
red -> 1
system/error/not found/ambiguous -> non-zero
```

## Trace Replay

Input from backend:

```text
checkResults(selectors, mode: exact | latest-per-subject | all) -> rows[] {
  dimensions
  result
  updatedAt
  checks[] { name, result, traceID, traceURL, startedAt, finishedAt }
}
```

`dagger check` uses `exact` when selectors identify one workspace state, and
`latest-per-subject` when selectors identify one logical subject with multiple
known states, such as one PR with multiple SHAs. `dagger list` uses `all` so it
can project every matching coordinate row.

Replay algorithm:

1. Create one local synthetic trace ID.
2. Create synthetic root span `dagger check`.
3. Add attributes:

```text
dagger.io/replay = true
dagger.io/replay.source = cloud-checks
github.repo = acme/hello
github.pr = 4242
git.sha = a1b2c3d
workspace = github.com/acme/hello
```

4. Create one synthetic child span per check.
5. Stream each original check trace from Cloud.
6. Rewrite imported spans to the synthetic trace ID.
7. Rewrite each imported root span's parent span ID to the synthetic check span.
8. Preserve imported span IDs and child relationships unless a span ID collision
   requires remapping.
9. Rewrite log records to the synthetic trace ID and matching span IDs.
10. Feed transformed spans and logs into `Frontend.SpanExporter()` and
    `Frontend.LogExporter()`.

The TUI receives ordinary OpenTelemetry spans and logs, so it renders normally.
Synthetic replay is local-only and must not be exported back to Cloud. Preserve
`original.trace_id` and `original.trace_url` as attributes.

Synthetic span timing:

- Imported spans keep original timestamps and durations.
- Synthetic check spans cover min-start to max-end of their imported traces.
- Synthetic root spans cover min-start to max-end of all checks.
- Because all spans are already ended, rendering appears immediate.

## `dagger list`

Syntax:

```console
$ dagger list <dimension> [selectors...]
```

Examples:

```console
$ dagger list github-repo --github-account=acme
GITHUB-REPO       RESULT  UPDATED
acme/hello        red     2m ago
acme/api          green   9m ago
```

```console
$ dagger list github-pr --github-repo=acme/hello
GITHUB-PR  RESULT  GIT-SHA   UPDATED
4242       red     a1b2c3d   2m ago
4241       green   d4e5f6a   8m ago
```

```console
$ dagger list check --github-repo=acme/hello --github-pr=4242
CHECK  RESULT  DURATION  TRACE
lint   green   18s       https://dagger.cloud/...
test   red     1m12s     https://dagger.cloud/...
build  green   43s       https://dagger.cloud/...
```

Projection rule for multi-column output:

`dagger list <dimension>` returns distinct matching coordinate tuples. The
requested dimension is always shown. Any unselected dimensions needed to
disambiguate the returned tuples are also shown as context columns. Selector
dimensions are hidden by default because the user already supplied them.

Summary columns such as `RESULT`, `UPDATED`, `DURATION`, and `TRACE` are
metadata, not coordinate dimensions. For grouped projections, `RESULT` is the
latest selected workspace-state rollup with strict precedence:

```text
red > pending > green
```

So a group is red if any selected check is red, pending if there are pending
checks and no red checks, and green only when all selected checks are green.

Example where more than one dimension remains:

```console
$ dagger list check --github-repo=acme/hello
GITHUB-PR  GIT-BRANCH  CHECK  RESULT  UPDATED
4242       -           lint   green   2m ago
4242       -           test   red     2m ago
-          main        lint   green   5m ago
-          main        test   green   5m ago
```

`-` means that dimension is not present for that row.

JSON output should preserve the raw coordinate row shape:

```json
{
  "dimensions": {
    "github-repo": "acme/hello",
    "github-pr": 4242,
    "check": "test"
  },
  "result": "red",
  "traceURL": "https://dagger.cloud/..."
}
```

## Backend Minimum

Minimum proposed backend read APIs:

```text
checkResults(selectors, mode)
checkCoordinates(selectors, project)
```

`checkCoordinates` returns the same coordinate rows and metadata used by
`checkResults`, projected for `dagger list`. That keeps grouping and
disambiguation rules in one coordinate-row model instead of duplicating them in
the CLI.

Proposed write and repair command remains separate:

```console
$ dagger integration sync github --repo=acme/hello --pr=4242
```

`dagger check` consumes history. `dagger list` discovers coordinates from
history. `dagger integration sync` creates or refreshes history.
