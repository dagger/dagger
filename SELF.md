# SELF.md — rolling toolset/prompt plan (re-verified against the tree)

This file records what landed, what remains, and what to do next, in priority
order. Items 0 and 1 are CLOSED (kept for their lessons); items 2–5 are open.

THIRD accuracy pass (this session): every claim below was re-checked against
the source by a read-only pass, and item 1's one remaining caveat was
discharged by a live probe. Corrections made this pass:

- Item 1 is now LIVE-CONFIRMED, not merely test-proven — see below.
- Stale line refs fixed: `captureLogLines` is core/mcp.go:1275 and
  `limitIndirectLines` core/mcp.go:1830 (were cited as 1164/1719); the
  row-0 `var lastLogID int64` moved into `captureLogLines`
  (core/mcp.go:1299, was cited as captureLogs:1188).
- Removed a stale self-contradiction: the previous pass claimed the owed
  integration test "does NOT exist" and credited `directSpanFilter`. Neither
  is true — `TestToolLogsKeepReport` exists (core/integration/llm_test.go:437)
  and `directSpanFilter` was reverted (no such symbol in the tree; the
  classifier is still the depth-1 rule at core/mcp.go:1376–1377).
- `./logtest/` (the gotest-vs-dangtest A/B harness the item-1 lesson points
  at) NO LONGER EXISTS in the tree. The lesson still stands; the harness is
  gone, so recreate it if a cross-SDK telemetry diff is needed again.

Everything else verified TRUE: item 0's `noRepoGitDir` (core/directory.go:1873,
used :1901) + `gitDiagnostics` (:1791–1867) + tests
(core/directory_patch_test.go:135,180; core/integration/directory_test.go:1630);
item 1's `telemetry.Internal()`/`revealTransport` (core/llm_otel.go:69,158) +
TestLLMTransportSpanInternal + the Dang SDK stdio fix in BOTH runtimes
(core/sdk/dang/v1/helpers.go:101–102, v2/helpers.go:124–125) + the
report-agent fixture (zero-padded, main.dang:25) + core/mcp_test.go's
TestAssembleLines/TestLimitIndirectLines; item 2's delegate doc still
SOURCE-silent (modules/delegate/main.dang:18–22); item 3's bare PASS/FAIL
(modules/engine-lab/main.dang:307–317); item 4's `workdir: String! =
defaultWorkdir` (modules/tui-qa/main.dang:79). HANDOFF.md still carries the
engine-lab workdir gotcha (:69–72) and the golangci/vet baseline (:90–94).
`commit-tasks.sh` is gone — don't look for it.

Context to have open: `modules/delegate/main.dang`, `modules/tui-qa/main.dang`,
`modules/engine-lab/main.dang`, `core/mcp.go` (captureLogLines /
limitIndirectLines / internalSpanFilter), `core/llm_object_tools.go` (toolLogs),
`dagql/idtui/frontend_console.go`, `core/integration/llm_test.go`.

## Landed this session: tool results render the trace report

The agent-facing `check` tool returned a markdown table (pass/fail per check,
no detail, no notion of tests). Now EVERY tool call whose span has descendants
returns the pretty frontend's final report: the span tree plus CHECKS and
TESTS sections, exactly what a user sees at the CLI.

Nothing new was needed in idtui: `NewWithDB` + `FinalRender` is already a
headless DB→string renderer (frontend_dots.go uses it that way), tests already
surface via `DB.TestView()`/`renderGlobalTests`, and `core` already imports
idtui (core/terminal.go:19). The work was plumbing, and the plumbing is where
all the bugs were:

- `core/checks_trace_report.go` — rebuilds a `dagui.DB` from clientdb
  (`Span.ReadOnly()` → `ExportSpans`; logs re-exported from protobuf), cached
  per session with incremental cursors (per-call rebuild was O(session):
  80–125ms on a 3000-span session even when the output was empty), byte guard
  (2000B/line, 16KiB total, middle dropped).
- `core/llm_object_tools.go` — `toolLogs` routes: report when the tool-call
  span has descendants, else the old flat path. `clientdb.DB.HasDescendants`
  is the cheap in-memory pre-filter.
- `core/mcp.go` — `ReadTrace` builtin (span / check / test → most recent
  match), sibling of ReadLogs; `core/trace_target.go` resolves names.
- `dagql/idtui` — `NewASCIIReporterWithDB` (color profile pinned, not
  env-mutated); `FrontendOpts.RerunSuggestion` hook so the report can suggest
  `ReadTrace(check: …)` instead of `dagger check …` without idtui learning any
  tool names.
- `modules/editor/main.dang` — `check` runs the checks, `rescue`s the error
  `run` raises, and returns Void; the generic path renders the result. NOTE
  the constraint that forces this: `routeObjectMethodResult` attaches tool
  logs only for Changeset/Workspace/object/Void returns — a String return
  goes straight to `outputToLLM` with no report, and an erroring tool returns
  `toolErrorMessage` with no report either.

An intermediate design added a check-specific API (`CheckGroup.RunReport`, a
`runReport`/`traceReport` field, a `RunSpanID` anchor, a `Run`/`run` split) —
all deleted once the generic path landed. `core/checks.go` and
`core/schema/checks.go` are untouched by the final change. The lesson is worth
keeping: the first version routed by TOOL IDENTITY (checks are special), the
right one routes by TELEMETRY (anything with a subtree). Ask what the general
rule is before adding an API for the specific case.

SIX bugs found only by running it, each of which would have shipped silently:

1. clientdb's `SelectSpansSince`/`SelectLogsSince` can return FEWER rows than
   the limit while more remain (the store spills to files, a read stops at a
   chunk boundary). Stopping on a short page truncated the trace and dropped
   exactly the nested otelgotest spans, so `HasTests()` was false and TESTS
   silently never rendered. Page until an EMPTY page.
2. Feeding `db.LogExporter()` records only WHICH spans have logs; the rendered
   `┃` lines come from the frontend's own buffers. The reporter must be built
   first and fed the logs.
3. `promoteConversationLocked` hung the caller's own transcript off the primary
   span, so a tool result rendered the agent's conversation instead of its
   work. Scoped renders skip promotion.
4. Pruning `Service` spans isn't enough — dagui routes a service's logs to its
   ORIGIN span by dag digest, so service noise leaked in until surfaced
   services' origins were pruned too.
5. `neverExpand` suppresses expansion for `LLMTool`/rolled-up spans — exactly
   where a module tool's `print` lands — so the naive version ate the very
   sub-agent reports item 1 fixed. Raising global verbosity would have
   un-hidden internal+encapsulated spans (regressing TestToolLogsExcludeInternal);
   the fix is `ExpandSpans` on the tool-call root, which `IsExpanded` consults
   BEFORE `neverExpand`, plus an 8-line clamp on nested rows — the render-side
   analogue of direct-vs-indirect provenance.
6. `SurfacedChecks` is a reveal-independent walk over ALL spans, so the CHECKS
   section ignored the report's scope: one tool call rendered another call's
   checks, and a call that matched NO checks rendered someone else's failure
   in full. Worse, a non-empty CHECKS section suppresses the progress-tree
   fallback, so the tool call's own row and prints vanished. Scoped renders
   filter checks to the scoped subtree (`fe.reportChecks()`).

LESSON: same shape as items 0 and 1 below. Every one of these was invisible in
the output (a missing section, a plausible-looking wrong tree) rather than an
error, and each was found by rendering something real and reading it. The
guard against this class is a live paste, not a passing test.

GOTCHA for future live QA of tool results: the `replay/` model provider emits
no per-tool-call display spans (`toolCallCtx`, core/mcp.go:809, falls back to
the shared loop ctx), so every replayed tool call shares ONE scope span and
scoping bugs are invisible. Use `ReadTrace` (same scoped-report code path) for
offline A/B, or a live provider.

## Completed (older plan — keep for reference, no action)

1. Wording sweep: error strings + systemPrompts in engine-lab/tui-qa are
   module-relative ("Call the engine-lab start tool first").
2. tui-qa `start` rescues short-lived commands: reruns synchronously, prints
   exit code + tail-truncated output, leaves session state unset.
3. Service logs excluded from LLM tool results: `internalSpanFilter` gained
   `skipServices` (filters `dagger.io/service` spans AND install spans via
   `clientdb.CausalChildren` — service stdio lands on install spans, a
   plan-vs-reality find). toolLogs passes true, ReadLogs false.
4. `GET /span?id=<hex>` console route (status/timing/dagui flags/parent chain)
   + unit test + tui-qa `span(spanHex)` tool.
5. delegate module documents that sub-agents inherit no session STATE (but see
   open item 2: it says nothing about SOURCE).

## Closed items (lessons only — do not re-open)

### 0. delegateEdits changeset merges failed on git-worktree checkouts (FIXED)

`delegateEdits` died with `failed to merge parallel changesets: git apply:
exit status 128`, discarding the sub-agent's entire work — even on a LONE
delegation, so it was never a parallel-merge race.

ROOT CAUSE: nothing to do with the patch. This workspace is a `git worktree`
checkout, so its `.git` is a FILE reading `gitdir: …/.bare/worktrees/…`. That
path doesn't exist inside the engine, so `git apply` — which discovers a
repository even though patching a working tree needs none — died during
discovery (`fatal: not a git repository: (null)`) before parsing anything.

FIX: `applyGitPatch` (core/directory.go:1901) runs git with
`GIT_DIR=/nonexistent/dagger-no-repo`, stopping discovery dead. Bonus:
patching is hermetic — an embedded repo's core.autocrlf/fileMode can't
change the result.

Keeper alongside it: the failure now explains itself. git's stdout/stderr are
teed into a bounded `gitDiagnostics` buffer folded into the returned error,
and the error QUOTES the patch around every `<stdin>:N` git names
(`> 6: "+bye"`). That is the ONLY reason the real cause was findable; the bare
"exit status 128" had burned two sessions on wrong theories.

A speculative fix (tolerating patches with no trailing newline) was written
then REMOVED once the real cause was found — unexplained leniency in a patch
path is a liability, not a freebie.

LESSON: an error that reports only a subprocess exit status is a bug in its
own right. Check other exec sites for the same silence — `core/changeset.go`'s
`runGit` already does it right (`git %v: %w: %s` with CombinedOutput).

### 1. delegateEdits results drown the sub-agent's final report (FIXED, LIVE-CONFIRMED)

Symptom: `delegateEdits` tool results were "… N lines omitted …" + a tail of
raw LLM SSE events and engine-metrics lines, then the patch summary — the
sub-agent's REPORT was lost in 3 of 3 edit delegations.

Three causes, all fixed:

- SSE/metrics noise: `core/llm_otel.go`'s per-request "LLM HTTP" span teed raw
  bodies to span stdio carrying only `telemetry.Encapsulate()`. Now started
  with `telemetry.Internal()` (:69), with `revealTransport` (:158) clearing it
  on transport error / HTTP >= 400 so failures still surface bodies.
- Tail-only truncation: `llmToolLogsMaxLines` = 8 head-snipped even noise-free
  reports. Tool results now abridge by PROVENANCE: `captureLogLines`
  (core/mcp.go:1275) tags a line `direct` when its record sits on the tool-call
  span or a direct child (:1376–1377); `limitIndirectLines` (:1830) keeps every
  direct line verbatim and trims only nested work to the last 8 with a counted
  marker.
- The real bug: report lines weren't classified `direct` because the Dang SDK
  bound stdout to dagql's `call_exec` PROFILING span (`ui.passthrough=true`),
  two hops down. FIX in `core/sdk/dang/v{1,2}/helpers.go`: bind stdio with
  `trace.ContextWithSpanContext(ctx, dagql.UserFacingSpanContext(ctx))`.
  Containerized SDKs get this free (the executor injects the user-facing
  traceparent, engine/engineutil/executor_spec.go); an in-engine runtime has
  to ask.

An earlier "fix" — a `directSpanFilter` widening the CONSUMER's classifier —
passed its canary and was REVERTED as symptom-treatment.

Proof: `TestToolLogsKeepReport` (core/integration/llm_test.go:437) over the
`core/integration/llmtest/report-agent/` fixture, canary-verified both ways.
LIVE CONFIRMATION (this session, the free confirmation the last pass predicted):
a `delegateEdits` probe emitting 20 nested-noise lines then a 14-line report
returned ALL 14 report lines intact, with only the noise abridged ("779 lines
omitted" + the last 8 indirect lines). Working in the shipped engine.

LESSON (the expensive one): I had the smoking gun and misread it. The print
span was `ui.passthrough=true` — a span type whose whole purpose is "no
frontend renders this row" — and I taught the CONSUMER to cope instead of
fixing the PRODUCER's routing; every reader (ReadLogs, TUI, error origins)
would have needed the same widening. When two SDKs disagree, diff them
(a cross-SDK A/B would have pointed at the Dang SDK in minutes — the
`./logtest/` harness that did this is GONE; rebuild it if needed). And as in
item 0, the answer was already in prose: `beginOTelCallExec`'s doc comment
(dagql/otelprof_hooks.go) warns that "logs parented to a hidden span vanish
from the row that should show them" and names
`MarkProfilingSpan`/`UserFacingSpanContext` as the escape hatch. I dumped span
attributes and never read the comment next to the code that created them.

## Prioritized next

### 2. Sub-agents do NOT see workspace edits to their own tool modules

Proven earlier: a post-edit QA delegate had no `span` tool and old `start`
behavior — tools are composed from module source as loaded at the PARENT
session's start, not re-read from the workspace. STILL OPEN
(modules/delegate/main.dang:18–22 talks only about live STATE).
Actions:

- Extend the doc: "fresh module instances" is right about STATE but misleads
  about SOURCE. Add: module edits don't reach sub-agent toolsets; QA module
  edits via the CLI against a from-source engine instead.
- Record the QA recipe (below) somewhere durable (module doc or skill).

### 3. engineTest should report test counts

"PASS" is indistinguishable from zero matched tests — PROVEN:
`engineTest(pkg: ./core/integration, run: TestDirectory/TestPatchNoSuchTestZZZ)`
returns **PASS**. Any green result is worthless until this lands.
`modules/engine-lab/main.dang:307–317` should parse `go test` output (or
-json) and report run/pass/fail/skip counts, failing loudly on 0 matched.
Workaround used successfully this session: plant a `t.Fatal("VERIFY-MARKER-X")`
in each test the filter should match, run, and read the markers back out of
the telemetry — that proves both which tests matched AND that assertions bite,
for one run instead of a canary rebuild. Note the trace report now renders
TESTS sections with real counts, so the raw material for this is already
reaching the engine.

### 4. INVESTIGATE: `dagger call` flag collision on module arg `workdir`

`dagger call -m modules/tui-qa start --args version` fails with "flag already
exists: workdir" (any cwd; `--help` and `dagger shell` work). Either a CLI bug
(module arg vs call's own/workspace-context flag registration) worth fixing in
cmd/dagger, or rename tui-qa's `workdir` param
(modules/tui-qa/main.dang:79, used :121–122). Repro is one command; diagnose
before choosing. engine-lab already dodged this by dropping its own `workdir`
arg (HANDOFF.md:69–72).

### 5. captureLogs perf follow-ups

Each capture scans the whole session log stream from row 0
(`var lastLogID int64`, core/mcp.go:1299) and does an unmemoized SelectSpan +
proto unmarshal per row for the LLMRole/LLMTool noise check; full text is
assembled then thrown away down to 9 lines. Fine at current scale; fix if
tool-call latency grows with session length. NOTE: this is now the FALLBACK
path only (tool calls with no child telemetry) — the report path got the
incremental-cursor treatment this session (core/checks_trace_report.go's
per-session DB cache), which is the pattern to copy here.

## Recipes that worked — reuse them

- Wave pattern: fire independent delegateEdits in parallel, keep one
  read-only `delegate` for investigation; have the investigation emit an exact
  implementation plan (file:line, edge cases, test design) and feed it verbatim
  to a second delegateEdits.
- Canary delegate: to prove a test bites, delegate (non-edits, sandbox
  discarded) "revert X, run the test, expect FAIL".
- Read-only accuracy pass: delegate "verify these N claims against the tree,
  reading only, report TRUE/FALSE/CHANGED with file:line" — cheap, and it
  caught three stale line refs and a dead `./logtest/` reference this session.
- QA edited Dang modules via CLI (sub-agents won't see the edits):
  engineLab start, then
  `cd /src && dagger --progress=plain -m modules/tui-qa shell -c 'start --args version | screen'`.
  Run from the repo root — contextual `Workspace!` binds to CWD. Private `let`
  fields aren't callable from shell; chain public tools.
- Module typecheck without a full run: `dagger -m /src/modules/<m> functions`.

## Parked backlog (no decision yet — don't act, don't lose)

- delegate/delegateEdits step cap: REMOVED (`maxSteps` gone from both tools;
  `loop` runs uncapped) — it was too tight for real editing tasks. Watch for
  the opposite failure mode (a runaway sub-agent) and consider a much higher
  cap or a time budget if it shows up.
- Workspace-returning tool with doc-string fallback: "Set the current
  workspace." reproduced from a Write tool call that DID create a file, so the
  patch summary was non-empty yet the fallback still showed.
  applyStateReturn/describeObject path in core.
- Replay-probe recipe (canned conversation → `... | loop | transcript`) as a
  skill/doc note; TestToolLogsExcludeService/Internal and TestToolLogsKeepReport
  are working references.
- engine-lab `exec` stdin param (query already has one); file-drop affordance.
- golangci baseline as machine baseline (HANDOFF.md:90–94 lists 8 findings;
  `go vet ./core/` lostcancel at core/services.go:930 is among them — don't
  "fix" it in passing without checking HANDOFF context).
- Separate repo: `go` tool needs persistent GOMODCACHE (every build re-dumps
  ~300 download lines) and explicit exit-status reporting like exec.
- Reuse idtui/dagui semantics inside engine captures: still punted.
