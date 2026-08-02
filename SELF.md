# SELF.md — toolset/prompt improvements from the tool-logs session retro

Plan for a fresh session, distilled from the retrospective of the session that
produced `fix: don't surface internal-span logs in LLM tool results` (see
msg.txt / that commit). Each item below was reviewed and decided; "parked"
items are recorded so they aren't lost, but need no action now.

Context you'll want open: `modules/tui-qa/main.dang`,
`modules/engine-lab/main.dang`, `modules/delegate/main.dang`,
`core/llm_object_tools.go` (toolLogs / llmToolLogsMaxLines / limitLines),
`core/mcp.go` (captureLogs), `dagql/idtui/frontend_console.go` (TUI console
routes), HANDOFF.md ("mark service spans, read their logs beneath install
spans" — why service logs appear under tool calls at all).

## 1. DONE (platform): tool-name collisions now namespace ALL tools

Fixed outside this repo and verified live: `engineLab_endpoint` resolves, no
more bare-`start` ambiguity. Two follow-ups while touching the modules below:

- Error strings should name tools accurately under namespacing:
  engine-lab's "No engine session. Call `start` first." is now doubly wrong
  (it's `engineLab_start` when collided). Phrase as "Call the engine-lab
  start tool first" (tui-qa's cross-module prompt wording already does this).
- Sweep both modules' systemPrompts for bare tool names that collide
  (`start`, `stop`) and phrase them module-relatively.

## 2. tui-qa: handle short-lived commands in `start`

Symptom: `start(args: ["version"])` → opaque "service exited before
healthcheck". Decision: on early exit, report the process exited (and with
what code) and `print(ctr.combinedOutput)`.

Sketch (modules/tui-qa/main.dang `start`): wrap the `.asService(...).start`
in `{ ... } rescue { e: Error => ... }` (see engine-lab's `engineTest` for
Dang rescue syntax). In the rescue arm, rerun the same command directly —
`runner.withExec(["dagger"] + args, expect: ReturnType.ANY)` — and return
"command exited immediately (exit code N) — the TUI console never came up;
for one-shot commands use the engine-lab exec tool" plus the tail-truncated
`combinedOutput`. Keep the session state unset in that path so later tools
still say "no active TUI session".

QA: `start(args: ["version"])` → friendly report; `start(args: ["agent"])`
still works; `stop` after the failed start doesn't error.

## 3. INVESTIGATE: huge tool-result tails from engineLab_restart / tuiQa_stop

What was observed: restart's result began "… 18677 lines omitted (use
ReadLogs(span: …) …" and the visible tail was engine-boot/git-ls-remote
noise; tuiQa_stop similarly showed a 6328-line marker + service-log tail.

First, establish what actually happened before changing anything. Analysis
from the retro to verify or refute:

- Context was probably NOT actually blown: the marker + ~8-line tail is
  `limitLines` doing its job (`llmToolLogsMaxLines = 8` in
  core/llm_object_tools.go). Confirm the result the model received really was
  ~10 lines, and figure out whether anything else (e.g. the capture itself)
  was expensive engine-side for an 18k-line subtree.
- The real problem is SIGNAL loss, not volume: `start` returns EngineLab
  (same-type rebind → `logsOrDone` → toolLogs), tail-keeping drops the one
  deliberate `print("Engine listening at …")` beneath thousands of service
  log lines. The engine's boot logs reach the capture because service exec
  spans cause-link under their install span (HANDOFF part 1), which sits
  beneath the tool-call span.
- Note: the internal-span filter from this session's fix does NOT help here —
  service exec spans aren't marked internal.

Candidate fixes to evaluate (pick after confirming the above):

a. Engine-side: exclude service-span logs (`dagger.io/service` attr, plus
   spans reached only via cause links?) from `toolLogs`/`captureLogs`
   captures — services are long-lived background noise with their own
   discovery path (ListServices + ReadLogs). Mirrors the internal-span
   filter's shape; add to the same `internalSpanFilter`-style walk.
b. Module-side: engine-lab `start`/`restart` return a compact report line
   (endpoint, health) so the signal is the RETURN value, not a print racing
   service logs. (Constraint: start must return EngineLab for state rebind,
   so its result goes through logsOrDone — which is why (a) matters.)
c. tuiQa `stop`/engineLab `stop`: same story — their confirmations should
   survive; with (a) they would.

Also noted, separate repo (no action here): the `go` tool should use a
persistent GOMODCACHE so every build doesn't re-download ~300 lines of
modules; and it should report exit status explicitly like exec does.

## 4. tui-qa: add a `span` detail tool (tuiQa_span)

The biggest missing affordance last session: nothing could answer "does this
span carry dagger.io/ui.internal?" — forcing a long static-analysis spiral
plus a behavioral probe.

- Engine side: add a console route in `dagql/idtui/frontend_console.go`
  (alongside /screen /key /type /resize /zoom /spans /help), e.g.
  `GET /span?id=<hex>`: name, status, timing, and the dagui-relevant flags
  (Internal, Boundary, Encapsulate/Encapsulated, RollUpLogs/RollUpSpans,
  Reveal, Passthrough, LLMRole/LLMTool, Service/ServiceName, Cached/Canceled),
  plus the parent chain to the root with each ancestor's flags — that chain is
  exactly what debugging "why is this hidden / why didn't its logs roll up"
  needs. Follow the existing console handler + `consoleSpans` patterns; check
  for existing console tests to extend.
- Module side: `span(spanHex: String!)` tool in modules/tui-qa/main.dang
  (GET via the existing `get` helper; @cache Never like its siblings), and a
  systemPrompt line: use `spans` to find ids, `span` to inspect one.
- QA: run any TUI session, pick a span id from `spans`, verify `span` shows
  attrs; specifically verify an internal span (e.g. a module-load span)
  reports Internal=true and its chain.

## 5. PARKED: generic "what the model sees == what the TUI shows"

Reusing idtui/dagui inside the engine itself (so captureLogs et al stop
hand-rolling dagui semantics like findRollUpSpan's internal/boundary walk) is
a separate initiative. Punt — do not start it from this plan.

## 6. delegate: document that sub-agents inherit NO state

Decision: documentation only. Sub-agents get the workspace (and therefore the
same tools), but none of the parent's session state: no running engine-lab
engine, no live TUI session, no bound-object state — `source.agents.compose`
composes fresh module instances (engine = null etc.).

Update all three texts in modules/delegate/main.dang: the module header
comment, the `delegate` tool docstring, and `systemPrompt`. Wording along the
lines of: "The sub-agent shares your WORKSPACE, not your session: tools that
hold live state (engine-lab's engine, tui-qa's TUI session) start fresh and
would have to rebuild — so either delegate tasks that are cheap to set up, or
pass any needed endpoints/details explicitly in the task text."

## Parked backlog (from the same retro; no decision yet — don't act, don't lose)

- Replay-probe recipe (canned conversation via api query → base64 →
  `dagger shell … | loop | transcript`) as a skill or doc note; it's the only
  key-free way to observe real LLM tool results. TestToolLogsExcludeInternal
  in core/integration/llm_test.go is a working reference.
- engineTest "PASS" can't distinguish zero-matched tests (false green);
  report counts.
- engine-lab `exec` stdin param (query already has one) to avoid heredoc
  escaping; possibly a file-drop affordance for the runner.
- golangci lint baseline is prose in HANDOFF.md (8 findings); make it a
  machine baseline so checks fail only on NEW findings.
- Follow-up bug seen live: Workspace-returning tool with an empty patch
  summary yields "Set the current workspace." (doc-string fallback in the
  applyStateReturn/describeObject path) — reproduced when writing msg.txt.
