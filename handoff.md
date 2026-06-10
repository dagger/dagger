# Handoff: image pull telemetry (better-image-pull branch)

## Goal

Replace the opaque/noisy telemetry for image pulls with streaming progress: hide the dozens of `remotes.docker.resolver.HTTPRequest` spans, and render a 2-D braille progress bar (one cell per layer, filling 1→8 dots as bytes arrive) attributed up to the originating `container.from` call. Eventually reuse the same mechanism for git/HTTP/filesync transfers.

## Status: core feature done, committed, verified e2e

A cold `container.from("nginx")` now renders as:

```
✔ .from(address: "nginx"): Container! 1.3s ⣿⣿⣿⣿⣿⣿ 33 MB
```

Commits on this branch (not pushed at handoff time):

- `fix(telemetry): hide registry HTTP noise in the TUI` — `telemetry.Encapsulate()` on the `pulling`/`resolving`/`pushing` spans (engine/server/resolver/resolver.go) hides the containerd/otelhttp HTTP child spans; `ShouldShow` (dagql/dagui/opts.go) no longer reveals encapsulated *failures* under successful parents, which buries the registry's routine 401 auth-challenge span (it always looks like an error). Reveal-on-failure is preserved: a genuinely failed pull still exposes the HTTP spans. New helper: `Span.EncapsulationHidden` (dagql/dagui/spans.go).
- `feat(telemetry): stream image pull progress as braille bars` — see architecture below.
- `chore(idtui): restore scrubbed hostname in golden` — see golden gotchas below.

## Architecture

**Convention** (engine/telemetryattrs/attrs.go): streaming progress flows over **OTel logs** — records carrying `dagger.io/progress.item` (e.g. layer digest), `.current`, `.total` (int64), `.unit`. Keyed by (span, item); latest record wins; emitters throttle (100ms) but must emit final state. Logs, not metrics, because the engine's metric reader flushes every 5s (engine/server/session.go, `metricReaderInterval`) — too coarse for live bars. Not span events: no streaming. Records MUST set an explicit empty-string body — an unset body becomes a nil `AnyValue` over OTLP and dagger/otel-go's `LogValueFromPB` logs `ERR unhandled otlpcommonv1.AnyValue` per record; the empty body also makes every text-log path skip them.

**Emitter** (engine/server/resolver/progress.go): `progressIngester` wraps the content store handed to `remotes.FetchHandler` in `Resolver.Pull` (resolver.go). Layer blobs (filtered by `images.IsLayerType`, total from descriptor size) emit throttled progress via `telemetry.Logger(ctx, "dagger.io/progress")`; the ctx carries the `pulling` span, so attribution is automatic. The `pulling` span is parented under the inner `Container.from` even though it runs during lazy evaluation — the dagql cache's call-context restoration handles that; no extra attribution plumbing was needed.

**TUI ingest** (dagql/dagui/progress.go): `DBLogExporter.Export` calls `db.ingestProgress` before text-log handling; progress records fold into `Span.Progress` (*SpanProgress, ordered items) and register the span in every ancestor's `Span.ProgressSpans` set. Covered by `TestIngestProgressLogs` (dagql/dagui/progress_test.go).

**Rendering** (dagql/idtui/frontend_pretty.go, `renderProgressBars`, called from `renderStepTitle` next to `renderRollUpDots`): one braille cell per item via the existing `brailleDots` table (gray=not started, yellow=in flight, green=complete), `+N` overflow past 40 cells, aggregate humanized byte count. Ownership rule: a row renders its own `Progress`, plus items from `ProgressSpans` sources when the row is collapsed, or when no deeper visible row will render them (`spanRendersOwnProgress` walks `ParentSpan` chain with `ShouldShow`) — that's how bars land on `.from` while the encapsulated `pulling` span stays hidden.

## Verification loop

- Build dev engine + CLI: `./hack/build` (also resets the `dagger-engine.dev` container + volume = full cold cache). Run against it: `./hack/with-dev ./bin/dagger ...`.
- **Cold pulls**: `dagger core engine local-cache prune` does NOT force a re-pull — the persisted dagql cache keeps the materialized `from` result alive. Fast trick: pull a different image each attempt (nginx, redis, postgres, mariadb...). Full reset: `./hack/build`.
- Capture raw telemetry: `go run ./hack/otlpdump -addr 127.0.0.1:43180 -out /tmp/telemetry.jsonl`, then set `OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:43180` plus explicit `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`/`..._METRICS_ENDPOINT` (the generic var alone only delivers traces) and `OTEL_EXPORTER_OTLP_TRACES_LIVE=1` on the CLI. Inspect with jq (`.kind` is span/log/metric; progress records have `attrs["dagger.io/progress.item"]`).
- Pretty TUI in a captured pty: `dagger query` falls back to plain when stdin is redirected; use `script -qec "./hack/with-dev ./bin/dagger core container from --address=nginx with-exec --args=ls stdout" /tmp/out.typescript` and grep for braille: `grep -aoP '[\x{2840}-\x{28FF}]+'`.

## Golden test gotchas (dagql/idtui)

- Regenerate one golden: `./hack/with-dev go test ./dagql/idtui/ -run 'TestTelemetry/TestGolden/<name>' -update`. The TUI renders inside `./bin/dagger`, so `./hack/build` first after TUI changes, or the regen uses stale rendering.
- Running goldens against the local persistent engine produces two environment artifacts vs CI: the engine hostname (`name=<container-id>` doesn't match the `*.dagger.local` scrub pattern in util/scrub/scrub.go) and absolute repo paths (`/app/...` in CI). Hand-restore the scrubbed hostname line after regenerating, and treat local full-suite failures that only differ in those as noise. Real validation is CI / the containerized harness.

## Loose ends (rough value order)

1. **Unpack progress**: `importImageLayer` (engine/snapshots/pull.go) — the decompress/apply phase, ~40% of cold-pull wall time — emits nothing. Same convention; consider whether it shares the layer item names (digest) under a separate span or distinct item names.
2. **Git/HTTP/filesync emitters**: 1-D bars via the same convention. Filesync currently has a 5s-gauge `FilesyncWrittenBytes` metric (engine/filesync/localfs.go) to upgrade.
3. **Renderer test coverage**: add a viztest function (dagql/idtui/viztest/main.go) emitting synthetic partial progress + a golden, to lock in partial-fill rendering — live pulls finish too fast to capture mid-flight frames.
4. **Dagger Cloud**: progress records reach Cloud as empty-bodied logs; check how its UI treats them / teach it the convention.
5. **dagger/otel-go hardening** (separate repo, optional): `LogValueFromPB` should handle nil `AnyValue`; `LogValueToPB` encodes empty bodies as the literal string `"INVALID"`. Eventually move the `dagger.io/progress.*` attrs upstream next to the other UI attrs.

## Misc

- `hack/otlpdump/` is committed as a dev tool. `dang.toml`/`pull.dang` in the repo root are Alex's manual test files — leave them untracked.
- The old buildkit progress-bar renderer this effectively resurrects: commented-out `renderVertexTasks`/`progChars` in dagql/idtui/frontend.go, and the `// TODO: ... 2-d progress` marker in frontend_pretty.go near `renderDuration`.
