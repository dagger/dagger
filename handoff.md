# Handoff: image pull telemetry (better-image-pull branch)

## Goal

Replace the opaque/noisy telemetry for image pulls with streaming progress: hide the dozens of `remotes.docker.resolver.HTTPRequest` spans, and render a 2-D braille progress bar (one cell per layer, filling 1→8 dots as bytes arrive) attributed up to the originating `container.from` call. Eventually reuse the same mechanism for git/HTTP/filesync transfers.

## Status: core feature done, committed, verified e2e

A cold `container.from("nginx")` now renders as:

```
✔ .from(address: "nginx"): Container! 4.9s
  ✔ pulling nginx:latest 3.3s ██████ 63 MB
  ✔ unpacking nginx:latest 1.0s ██████ 63 MB
```

PR: https://github.com/dagger/dagger/pull/13410

Commits on this branch:

- `fix(telemetry): hide registry HTTP noise in the TUI` — `telemetry.Encapsulate()` on the `pulling`/`resolving`/`pushing` spans (engine/server/resolver/resolver.go) hides the containerd/otelhttp HTTP child spans; `ShouldShow` (dagql/dagui/opts.go) no longer reveals encapsulated *failures* under successful parents, which buries the registry's routine 401 auth-challenge span (it always looks like an error). Reveal-on-failure is preserved: a genuinely failed pull still exposes the HTTP spans. New helper: `Span.EncapsulationHidden` (dagql/dagui/spans.go).
- `feat(telemetry): stream image pull progress as braille bars` — see architecture below.
- `chore(idtui): restore scrubbed hostname in golden` — see golden gotchas below.
- `test(idtui): cover partial progress bar fills` — was loose end #3. Viztest `PartialProgress` emits synthetic never-completing progress; golden covers attribution/+N/byte summary, and `TestRenderProgressBarFills` (dagql/idtui/progress_render_test.go) pins exact cell fills, because the scrub.Stabilize braille rule (util/scrub/scrub.go, predates branch, for nondeterministic roll-up dots) collapses braille runs in goldens.
- `feat(telemetry): stream image unpack progress` — encapsulated `unpacking <ref>` span in `ImportImage` (engine/snapshots/pull.go); containerd's native `diff.WithProgress` apply-opt streams compressed-bytes-read per layer, keyed by blob digest. Deduped layers skip Apply and emit nothing (= no warm-path noise). Shared emitter moved to `snapshots.EmitProgress` (engine/snapshots/progress.go).
- `feat(telemetry): render progress spans as labeled rows` — carrying progress reveals an encapsulated span (`EncapsulationHidden`, dagql/dagui/spans.go); collapsed rows surface descendants' progress as indented rows like the error-origin roll-up (`renderProgressSpanRow`). Bars are never merged across spans. `DisplayRef` (engine/snapshots/progress.go) drops digest + default registry from pulling/unpacking span names.
- `feat(idtui): block-element progress bars, name first` — progress uses block elements so braille means span status only (spinner + roll-up dots). Multi-item (2-D) = one bottom-up cell per item; single-item (1-D) = fixed 12-cell left-to-right track, picked automatically by item count. Bars trail the name/duration. Block runs escape the braille scrub, so goldens now lock exact fills.
- `chore(idtui): render progress bars faint` — whole progress row is dim except the status icon.
- `fix(idtui): auto-hide completed transfers from the live progress roll-up` — rolled-up progress rows previously rendered every descendant source unconditionally and the TUI's default verbosity (`dagger call` = ShowCompletedVerbosity) never GCs completed spans, so finished transfers pierced their collapsed ancestors forever, accumulating without bound on large traces. The roll-up filter is activity-based, NOT ShouldShow/GC-based (a first attempt with `ShouldShow` was a no-op at default verbosity — verified live). Completed transfers stay reachable live by expanding the parent (progress still reveals encapsulated spans in natural position). Live invalidation needs no new plumbing: an in-flight roll-up row renders a spinner child in the host SpanTreeView, tuist Update() propagation re-renders the host every tick, and the render that drops the row also dismounts the spinner.
- `fix(idtui): fold quick transfers into one roll-up line` + `fix(idtui): always fold completed transfers; "p" expands` — the policy iterated: first a 1s duration threshold (slow transfers kept individual rows), then per vito's review the thresholds went away entirely. Current policy: **in-flight transfers each get their own roll-up row immediately; completed ones always fold into one merged line**, live and in the final render alike, like `✔ 1 upload, 38 fetches 0.8s` — counts by kind (`transferSummary` maps the emitters' leading verb pulling/unpacking/fetching/uploading/downloading to nouns, unknown → "transfers", first-appearance order) plus the wall-clock union of their `dagui.Activity` (parallelism-aware; `Activity.Add` + `Duration`). No byte total on the merged line — fetch and unpack read the same bytes, same stance as never merging bars into parent titles. A fold of one renders as the real row. The **"p" keybind** toggles the focused row's fold into individual rows (distinct from tree expansion; `fe.progressExpanded`, contextual keymap entry "expand/collapse transfers", and a faint `p expand` hint on the focused merged line mirroring the error-origin "r jump" hint). Debug or verbosity ≥ ShowSpammyVerbosity also shows every transfer. Goldens unaffected (no roll-up forms in testdata; partial-progress renders in natural position). Tests: `TestRenderProgressSpanRowsAutoHide` (in-flight row immediate, completed fold, p-toggle), `TestRenderProgressMergedRollup` (merged text + union duration + p-toggle in final render), `TestRenderProgressSpanRows` (collapsed case asserts the merged line via `FinalRender`).
- viztest `TransientProgress` (dagql/idtui/viztest/main.go) — a long-lived collapsed "syncing layers" span containing one instantly-completing transfer, three instant `fetching ...apk` transfers, and one ~8s in-flight transfer; built for live QA of the auto-hide + merge (not a golden: wall-clock pacing). Verified e2e against a dev engine: live frames show only the in-flight transfer (post-threshold), the final report shows it individually plus `✔ 1 transfer, 3 fetches 0.0s` — and the workspace-load row demonstrated it on real transfers (`unpacking 2.9s ... 145 MB` individual, `✔ 1 upload, 1 fetch 0.8s` merged). `PartialProgress`'s emit helper is extracted to package-level `emitProgress`.

## Architecture

**Convention** (engine/telemetryattrs/attrs.go): streaming progress flows over **OTel logs** — records carrying `dagger.io/progress.item` (e.g. layer digest), `.current`, `.total` (int64), `.unit`. Keyed by (span, item); latest record wins; emitters throttle (100ms) but must emit final state. Logs, not metrics, because the engine's metric reader flushes every 5s (engine/server/session.go, `metricReaderInterval`) — too coarse for live bars. Not span events: no streaming. Records MUST set an explicit empty-string body — an unset body becomes a nil `AnyValue` over OTLP and dagger/otel-go's `LogValueFromPB` logs `ERR unhandled otlpcommonv1.AnyValue` per record; the empty body also makes every text-log path skip them.

**Emitter** (engine/server/resolver/progress.go): `progressIngester` wraps the content store handed to `remotes.FetchHandler` in `Resolver.Pull` (resolver.go). Layer blobs (filtered by `images.IsLayerType`, total from descriptor size) emit throttled progress via `telemetry.Logger(ctx, "dagger.io/progress")`; the ctx carries the `pulling` span, so attribution is automatic. The `pulling` span is parented under the inner `Container.from` even though it runs during lazy evaluation — the dagql cache's call-context restoration handles that; no extra attribution plumbing was needed.

**TUI ingest** (dagql/dagui/progress.go): `DBLogExporter.Export` calls `db.ingestProgress` before text-log handling; progress records fold into `Span.Progress` (*SpanProgress, ordered items) and register the span in every ancestor's `Span.ProgressSpans` set. Covered by `TestIngestProgressLogs` (dagql/dagui/progress_test.go).

**Rendering** (dagql/idtui/frontend_pretty.go, `renderProgressBars`): a span renders only its OWN progress, trailing its name/duration. Multi-item = one bottom-up block cell per item (`verticalEighths`, gray=not started, yellow=in flight, green=complete, `+N` past 40 cells); single-item = 12-cell horizontal track (`renderProgressTrack`); everything faint. Progress-carrying spans are always visible: `HasProgress` escapes `EncapsulationHidden` (revealed when ancestors expanded), and collapsed rows roll up descendants' progress as indented rows (`renderProgressSpanRow` in `renderRowContentRest`, mirroring error origins, driven by `Span.ProgressSpans`).

## Verification loop

- Build dev engine + CLI: `./hack/build` (also resets the `dagger-engine.dev` container + volume = full cold cache). Run against it: `./hack/with-dev ./bin/dagger ...`.
- **Cold pulls**: `dagger core engine local-cache prune` does NOT force a re-pull — the persisted dagql cache keeps the materialized `from` result alive. Fast trick: pull a different image each attempt (nginx, redis, postgres, mariadb...). Full reset: `./hack/build`.
- Capture raw telemetry: `go run ./hack/otlpdump -addr 127.0.0.1:43180 -out /tmp/telemetry.jsonl`, then set `OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:43180` plus explicit `OTEL_EXPORTER_OTLP_LOGS_ENDPOINT`/`..._METRICS_ENDPOINT` (the generic var alone only delivers traces) and `OTEL_EXPORTER_OTLP_TRACES_LIVE=1` on the CLI. Inspect with jq (`.kind` is span/log/metric; progress records have `attrs["dagger.io/progress.item"]`). See also the `telemetry-capture` skill.
- Pretty TUI in a captured pty: `dagger query` falls back to plain when stdin is redirected; use `script -qec "./hack/with-dev ./bin/dagger core container from --address=nginx with-exec --args=ls stdout" /tmp/out.typescript` and grep for braille: `grep -aoP '[\x{2840}-\x{28FF}]+'`.

## Golden test gotchas (dagql/idtui)

- Regenerate one golden (preferred): `./hack/with-dev ./bin/dagger call engine-dev test-telemetry --run 'TestTelemetry/TestGolden/<name>' --update -y` — runs in the containerized harness (~5min), so no hostname/path artifacts; `-y` auto-applies the changeset.
- Local alternative: `./hack/with-dev go test ./dagql/idtui/ -run 'TestTelemetry/TestGolden/<name>' -update`. The TUI renders inside `./bin/dagger`, so `./hack/build` first after TUI changes, or the regen uses stale rendering.
- Running goldens against the local persistent engine produces two environment artifacts vs CI: the engine hostname (`name=<container-id>` doesn't match the `*.dagger.local` scrub pattern in util/scrub/scrub.go) and absolute repo paths (`/app/...` in CI). Hand-restore the scrubbed hostname line after regenerating, and treat local full-suite failures that only differ in those as noise. Real validation is CI / the containerized harness.

## Loose ends (rough value order)

1. **Git/HTTP/filesync emitters**: 1-D bars via the same convention (single item → track form renders automatically). Filesync currently has a 5s-gauge `FilesyncWrittenBytes` metric (engine/filesync/localfs.go) to upgrade.
2. **Full golden-suite harness validation pending**: Docker Hub 429-rate-limited the harness's binfmt setup pull (cold-pull testing exhausted the quota). partial-progress (the only progress-bearing golden) was regenerated locally + hostname-restored; PR CI is the backstop.
3. **Dagger Cloud**: progress records reach Cloud as empty-bodied logs; check how its UI treats them / teach it the convention — including the auto-hide rule: progress rows are an activity surface, so only roll up in-flight transfers (span snapshots carry StartTime/EndTime to derive this); completed ones render in natural tree position when their parent is expanded.
4. **dagger/otel-go hardening** (separate repo, optional): `LogValueFromPB` should handle nil `AnyValue`; `LogValueToPB` encodes empty bodies as the literal string `"INVALID"`. Eventually move the `dagger.io/progress.*` attrs upstream next to the other UI attrs.

## Misc

- `hack/otlpdump/` is committed as a dev tool. `dang.toml`/`pull.dang` in the repo root are Alex's manual test files — leave them untracked.
- The old buildkit progress-bar renderer this effectively resurrects: commented-out `renderVertexTasks`/`progChars` in dagql/idtui/frontend.go, and the `// TODO: ... 2-d progress` marker in frontend_pretty.go near `renderDuration`.
