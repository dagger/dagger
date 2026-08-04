# HANDOFF: bash-less agent tooling for engine debugging

Where we are in the "sandboxed agent, higher-level tools" effort, for picking
up in a fresh session. Companion doc: `RESTORE.md` (the checkpoint/restore
undo-tooling plan — separate track, not restated here).

## The arc

Origin: studied a Claude Code transcript
(`2026-08-01-141807-lets-investigate-this.txt`, a bash-heavy engine-bug
investigation) and `modules/tui-qa/main.dang` to determine what modules/tools
let a bash-less agent do the same work. The proposal (agreed):

- **`engine-lab` module** (largest gap, NOT started): build engine+CLI from
  workspace source, run as a service; tools `exec`/`query`/`restart`/
  `debugGet` (+`debugJq`), `engineTest(pkg, run)`; async folded in (no
  separate `tasks` module). Its planned `engineLogs` tool was deliberately
  dissolved into the ReadLogs work below.
- **`git-history` module** (read-only archaeology: `log -S`, `show rev:path`,
  `branchesContaining`, `diffRange`) — NOT started.
- **`contributor` module** (branch/commit/push/PR; needs write creds, separate
  review posture) — NOT started.

Then a refinement took over: instead of an `engineLogs` tool, make **ReadLogs
work for services** (service logs associate to the span that created the
service) and **mark service spans for cheap discovery** (per the
`tui-surfacing` skill pattern). That became a 4-part plan, now 3/4 shipped.

## Shipped (commits on this branch)

1. **`feat: mark service spans, read their logs beneath install spans`**
   - `engine/clientdb`: `spanLookup` indexes cause-purpose span links
     (fed from EVERY snapshot row — links arrive late via
     `RunningService.addOriginSpanContexts`); `descendants()` follows them,
     so `SelectLogsBeneathSpan`/ReadLogs agree with dagui's tree. Service
     exit errors' traceparents (install spans) now work with ReadLogs.
   - `engine/telemetryattrs`: `ServiceAttr` (`dagger.io/service`, on the
     long-lived exec span in `core/service.go` `startContainer`) +
     `ServiceNameAttr` (hostname; canonical home, `core/modtree.go` aliases).
   - dagui seam: `SpanSnapshot.Service`/`ServiceName` via `ProcessAttribute`.

2. **`feat: add ListServices builtin for service discovery`**
   - `core/mcp.go`: `ListServices` next to ReadLogs — hostname, ports,
     `spanID` (exec span) + `installSpanIDs`, all ReadLogs-able.
   - `core/services.go`: `RunningServices(sessionID)` +
     `ServiceSpanContext()`/`InstallSpanContexts()` accessors.
   - The agent loop this whole effort targeted now closes:
     ListServices → spanID → ReadLogs(span, grep, limit) — the sandbox
     equivalent of `docker logs dagger-engine.dev | grep`.

3. **`feat: surface services in the TUI`** — `DB.SurfacedServices()`/
   `HasServices()`, the SERVICES final-report section (auxiliary: after main
   rows, never replaces them, not in FinalRender's existence gate;
   `span=<hex>` per row under RunningInAgent), `/spans` console tagging
   `[service <hostname>]` + hostname query matching.

4. **`engine-lab` module** (pending commit as of this handoff) —
   `modules/engine-lab/` (Dang, deps engineDev+cliDev, registered under
   `[env.dev.modules.engine-lab]` in dagger.toml), mirroring tui-qa's
   session-state pattern. Tools: `start`/`restart` (engine+CLI from workspace
   source; engine service replicates engine-dev `Service()` plus
   `--debugaddr 0.0.0.0:6060`), `exec` (expect-ANY, exit code + line-tail
   -truncated stdout/stderr), `query` (`dagger api query --no-load-module`
   via stdin), `debugGet`/`debugJq` (curl/jq against `dagger-engine:6060`
   service binding — non-exposed ports ARE reachable via bindings),
   `engineTest` (engineDev.test + rescue → PASS/FAIL+tail), `stop`
   (self-call reset). Live-QA'd end-to-end: start→exec→raw graphql→debug
   endpoints (~2min warm), debugJq keys of /debug/dagql/cache, engineTest
   PASS (./util/netrc) and FAIL/rescue paths. Gotcha found: `dagger call`
   chain flag registration collides on repeated arg names (start returns
   EngineLab which re-exposes start/restart), so `start` takes only the
   auto-injected Workspace — no `workdir` flag.

## Next steps (in rough priority)

1. **QA follow-up**: live-export timing of a *running* service span. In the
   live QA the service lived <1s (bad invocation: `dagger core container from
   --address=alpine:3.20 with-exec ... as-service start` pre-evaluates the
   withExec as a build step and the service ran alpine's default `/bin/sh`).
   Re-QA with a genuinely long-lived service (e.g. python http.server +
   exposed port) and confirm the exec span appears tagged in `/spans` (and
   ReadLogs returns its logs) *while it runs*, not just after End. ListServices
   is registry-backed and immune either way.
2. **Live-tree service affordance** (deferred from part 3): a chrome line /
   keybind, NOT checks-style promotion. Needs design.
3. `git-history` and `contributor` modules, and RESTORE.md's phased plan.

## Known landmines (don't chase, don't regress)

- Pre-existing failures: `TestConversationReportNestsSubAgent` (idtui);
  `TestTelemetry/TestGolden/*` need a real engine ("driver for scheme
  \"image\"" in this harness); `go vet ./core/` lostcancel at
  `core/services.go:930`; `golang:lint-all` baseline has 8 findings incl. a
  G101 on `LLMToolResultTokensAttr` (attrs.go) — none ours.
- Harness quirks observed (being cleaned up separately): occasional
  `Set the current workspace.` returned instead of a mutation report (the
  mutation DID apply — verify via `status`); transient
  `no workspace bound` edit/delegate failures right after a re-bind. The
  workspace-status transcript .txt files are NOT in git
  (an earlier note here claimed otherwise) — leave them alone. `msg.txt` is
  already gone.
- The `E`/`q` path in the TUI console after run completion restarted the
  command once during QA — screen state after quit is unreliable; prefer
  `/spans` + unit tests for report assertions.
- `grep` on a path that exists ONLY as a pending overlay edit (never
  written to the host yet) is a deterministic failure, not the transient
  this file used to claim. Host-side rg is run first and exits 2 when its
  filters match nothing; the overlay results are merged in afterwards. The
  `globs:` form is fixed (the diagnostic is now tolerated on both the
  host and in-engine paths, `engine.RipgrepNoFilesSearched`), but the
  `paths:` form naming a directory that exists only in the overlay still
  fails with rg's `No such file or directory`. That one is left alone
  deliberately: swallowing it would mask genuinely bad paths. Use
  `read`/`find` for brand-new paths.

## File map (this effort's code)

- emit: `core/service.go` (~line 726), `engine/telemetryattrs/attrs.go`
- log walk: `engine/clientdb/telemetry_store.go` (+`_test`)
- discovery: `core/mcp.go` (`ListServices`, `readLogsTool`),
  `core/services.go` accessors (+`core/services_test.go`)
- surfacing: `dagql/dagui/services.go` (+`_test`), `dagql/dagui/db.go` memos,
  `dagql/idtui/services_report.go` (+`_test`),
  `dagql/idtui/frontend_pretty.go` (`renderFinalReport` wiring),
  `dagql/idtui/frontend_console.go` (`consoleSpans`)
- patterns to mirror: `dagql/dagui/conversation.go`, `checks.go`;
  skills: `tui-surfacing`, `tui-console`, `engine-debugging`
