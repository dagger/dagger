# SELF.md — rolling toolset/prompt plan (updated after the implementation session)

Previous plan (items 1–4, 6) is DONE: implemented via parallel
delegate/delegateEdits waves, validated (unit + integration tests, live QA,
one canary run), and `commit-tasks.sh` at the repo root stages/commits it as
one commit per task. This file now records what landed, what THIS session
surfaced, and what to do next, in priority order.

Context to have open: `modules/delegate/main.dang`, `modules/tui-qa/main.dang`,
`modules/engine-lab/main.dang`, `core/mcp.go` (captureLogs /
internalSpanFilter), `core/llm_object_tools.go` (toolLogs),
`dagql/idtui/frontend_console.go`, `core/integration/llm_test.go`
(TestToolLogsExcludeService + llmtest/svc-agent).

## Completed (previous plan — keep for reference, no action)

1. Wording sweep: error strings + systemPrompts in engine-lab/tui-qa are
   module-relative ("Call the engine-lab start tool first").
2. tui-qa `start` rescues short-lived commands: reruns synchronously, prints
   exit code + tail-truncated output, leaves session state unset. QA'd via
   `dagger shell` (see recipes below).
3. Service logs excluded from LLM tool results: `internalSpanFilter` gained
   `skipServices` (filters `dagger.io/service` spans AND install spans via
   `clientdb.CausalChildren` — service stdio lands on install spans, a
   plan-vs-reality find). toolLogs passes true, ReadLogs false.
   `TestToolLogsExcludeService` proven red-without/green-with via a canary
   delegate that reverted the flag and watched it fail.
4. `GET /span?id=<hex>` console route (status/timing/dagui flags/parent chain
   with per-ancestor flags) + unit test + tui-qa `span(spanHex)` tool.
   QA'd live incl. internal spans and error cases.
6. delegate module documents that sub-agents inherit no session state.

## Prioritized next

### 1. delegateEdits results drown the sub-agent's final report

Observed every time this session: `delegateEdits` tool results were
"… N lines omitted …" + a tail of raw LLM SSE events (`event:
content_block_stop`, `data: {...}`) and engine-metrics log lines, then the
patch summary — the sub-agent's REPORT text was lost in 3 of 3 edit
delegations. Plain `delegate` survived fine because the report IS its return
value.

- First check the cheap module-side fix: in `modules/delegate/main.dang`,
  make delegateEdits' RETURN VALUE carry `lastReply` + patch summary as one
  string (like `delegate` does), instead of relying on logs to surface the
  report above the summary.
- Then re-test after the platform engine includes this session's service-log
  fix — the engine-metrics noise is nested-engine service logs and should
  disappear; the SSE noise may not (those records likely sit on LLM-loop or
  HTTP spans under the delegation span, not on LLMRole/LLMTool spans that
  captureLogs already skips). If SSE noise persists, find which span carries
  it (tuiQa `span` tool / ReadLogs) and extend the capture filter.

### 2. Sub-agents do NOT see workspace edits to their own tool modules

Proven this session: a post-edit QA delegate had no `span` tool and old
`start` behavior — tools are composed from module source as loaded at the
PARENT session's start, not re-read from the workspace. Actions:

- Correct/extend the item-6 wording in `modules/delegate/main.dang`: "fresh
  module instances" is right about STATE but misleads about SOURCE. Add:
  module edits don't reach sub-agent toolsets; QA module edits via the CLI
  against a from-source engine instead.
- Record the QA recipe (see recipes below) somewhere durable (module doc or
  skill), so the next session doesn't rediscover it.

### 3. engineTest should report test counts (promote from backlog)

"PASS" is indistinguishable from zero-matched tests. This session mitigated
with a canary delegate (revert the fix in a discarded sandbox, expect FAIL) —
works but costs an engine build. Better: `modules/engine-lab/main.dang`
engineTest parses `go test` output (or -json) and reports run/pass/fail/skip
counts; fail loudly on 0 matched.

### 4. INVESTIGATE: `dagger call` flag collision on module arg `workdir`

`dagger call -m modules/tui-qa start --args version` fails with "flag already
exists: workdir" (any cwd; `--help` works, `dagger shell` works). Either a
CLI bug (module arg vs call's own/workspace-context flag registration) worth
fixing in cmd/dagger, or rename tui-qa's `workdir` param. Repro is one
command; diagnose before choosing.

### 5. captureLogs perf follow-ups (from the item-3 investigation, verified)

Each capture scans the whole session log stream from row 0
(`core/mcp.go` captureLogs: `var lastLogID int64`) and does an unmemoized
SelectSpan + proto unmarshal per log row for the LLMRole/LLMTool noise check;
full text is assembled then thrown away down to 9 lines. Fine at current
scale; fix if tool-call latency grows with session length. The service filter
already prunes the worst repeat offender.

## Recipes that worked — reuse them

- Wave pattern: fire independent delegateEdits in parallel, keep one
  read-only `delegate` for investigation; have the investigation emit an
  exact implementation plan (file:line, edge cases, test design) and feed it
  verbatim to a second delegateEdits. The svc-agent fix landed first try off
  a plan like that.
- Canary delegate: to prove a test bites, delegate (non-edits, sandbox
  discarded) "revert X, run the test, expect FAIL". Report came back with
  verbatim assertion output.
- QA edited Dang modules via CLI (sub-agents won't see the edits):
  engineLab start, then
  `cd /src && dagger --progress=plain -m modules/tui-qa shell -c 'start --args version | screen'`.
  Run from the repo root — contextual `Workspace!` binds to CWD (from /tmp it
  built against an empty workspace and failed with a confusing go.mod error).
  Private `let` fields aren't callable from shell; chain public tools.
- Module typecheck without a full run: `dagger -m /src/modules/<m> functions`
  (loads + compiles the Dang source).

## Parked backlog (no decision yet — don't act, don't lose)

- Workspace-returning tool with doc-string fallback: "Set the current
  workspace." reproduced AGAIN this session — this time from a Write tool
  call that DID create a file (commit-tasks.sh), so the patch summary was
  non-empty yet the fallback still showed. Stronger repro than last time;
  applyStateReturn/describeObject path in core.
- Replay-probe recipe (canned conversation → `... | loop | transcript`) as a
  skill/doc note; TestToolLogsExcludeService and TestToolLogsExcludeInternal
  are both working references now.
- engine-lab `exec` stdin param (query already has one); file-drop affordance.
- golangci baseline as machine baseline (HANDOFF.md prose lists 8 findings;
  `go vet ./core/` lostcancel at core/services.go is among them — don't
  "fix" it in passing without checking HANDOFF context).
- Separate repo: `go` tool needs persistent GOMODCACHE (every build re-dumps
  ~300 download lines) and explicit exit-status reporting like exec.
- Item 5 from the old plan (reuse idtui/dagui semantics inside engine
  captures) stays punted.
