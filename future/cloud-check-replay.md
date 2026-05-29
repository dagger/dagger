# Cloud Check Replay And Workspace Remotes

## Goal

`dagger check` should use the selected workspace address as its only Cloud
selection surface.

```console
$ dagger -W github.com/acme/hello@main check
```

For explicit remote workspace addresses, `dagger check` first asks Dagger Cloud
for the latest matching check result. If Cloud has a replayable result, the CLI
replays that result through the normal Dagger TUI and exits with the stored
status. If Cloud has no result, the CLI falls back to executing checks for the
selected workspace. Local and implicit workspaces execute normally.

There are no typed Cloud selector flags on `dagger check`.

## Workspace Remotes

`dagger workspace remotes` is the discovery command for selectable workspace
addresses. Each row should be directly usable as `-W`.

```console
$ dagger -W github.com/acme/hello workspace remotes
KIND    ADDRESS                                      CHECKS
branch  github.com/acme/hello@main                   green 12/12
branch  github.com/acme/hello@release                -
tag     github.com/acme/hello@v1.2.3                 green 12/12
pr      github.com/acme/hello@pull/4242/head         red 10/12
```

The address preserves the full workspace location, including a subdirectory,
and transposes only the remote ref:

```console
$ dagger -W github.com/acme/mono/services/api workspace remotes
KIND    ADDRESS                                             CHECKS
branch  github.com/acme/mono/services/api@main              green 8/8
tag     github.com/acme/mono/services/api@v1.2.3            -
```

Source-of-truth enumeration should come from the engine's Git APIs for branches
and tags. Cloud data is an annotation layer for `CHECKS`; missing Cloud data
must not hide a real remote ref.

Future PR-head support should produce the same row shape: one selectable remote
address per PR head.

## Workspace Activity

`dagger workspace activity` is the Cloud timeline view for the selected
workspace address. It is intentionally recent and Cloud-derived; unlike
`workspace remotes`, it does not claim to enumerate every selectable ref.

```console
$ dagger -W github.com/acme/hello workspace activity
KIND    ADDRESS                                CHECKS      UPDATED
branch  github.com/acme/hello@main             green 12/12  1h ago
pr      github.com/acme/hello@pull/4242/head    red 10/12    3h ago
```

## Replay

Cloud replay creates local synthetic telemetry and feeds it to the existing
frontend exporters. The replay trace is local-only and should not be exported
back to Cloud.

At a high level:

1. Select rows by normalized workspace address and optional check names.
2. Pick the latest unambiguous commit/result.
3. Create a synthetic root span named `dagger check`.
4. Add one synthetic child span per selected check.
5. For small result sets, stream original spans/logs and rewrite their trace ID.
6. For larger result sets, summarize each check with a synthetic span.
7. Exit `0` for green and non-zero for red, pending, ambiguous, or not found.
