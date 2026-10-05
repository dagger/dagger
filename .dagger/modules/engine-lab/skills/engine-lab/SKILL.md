---
name: engine-lab
description: Build and debug the Dagger engine from the workspace source with the EngineLab tools — the sandbox equivalent of the ./hack/dev + ./hack/with-dev loop. Read before using the engine-lab tools to run commands against a live from-source engine, poke its debug/pprof endpoints, profile wall-clock time with wcprof, run engine tests, or re-run repros after editing engine code.
---

# Engine Lab

You can build and debug the Dagger engine from the workspace source with the
EngineLab tools — the sandbox equivalent of the `./hack/dev` + `./hack/with-dev`
loop from the `engine-debugging` skill.

Workflow:

- the engine-lab start tool builds the engine + CLI from the workspace source
  and runs the engine as a persistent service with its debug endpoints enabled.
  The first build takes a while; later tools reuse the running engine.
- `dagger(args: [...])` runs `dagger <args>` against the live engine — pass the
  subcommand WITHOUT a leading "dagger" (e.g. `["call", "test"]`). The
  from-source CLI is on PATH and your CURRENT workspace tree is mounted at /src,
  re-mounted fresh on every call so your edits are always visible — only the
  engine *binary* is pinned until `restart`. Commands that talk to Dagger
  Cloud run authenticated when the module's `cloudCredentials` setting is
  configured in dagger.toml
  (`cloudCredentials = "file://~/.config/dagger/credentials.json"`, mounted
  where the CLI's auth code looks); without it the CLI runs logged out.
- `query` sends raw GraphQL straight to the engine (no module loaded).
- `debugGet`/`debugJq` hit the engine's :6060 debug endpoints (routes in
  cmd/engine/debug.go), e.g. /debug/pprof/goroutine?debug=2 for hangs; use
  debugJq to filter big JSON like /debug/dagql/cache.
- After editing engine code, `restart` rebuilds and replaces the running
  engine; then re-run your repro with `dagger`.
- `engineTest(pkg, run)` runs engine tests with their own ephemeral engine (no
  `start` needed), e.g. pkg "./core/integration" with run "TestSuite/TestSub".
- `dumpId(file, ...)` builds and runs the repo's own `cmd/dump-id` against a
  file in your workspace — no engine session needed. `file` is a
  workspace-relative path to a base64 call ID. Modes mirror the command's
  flags: `stats` (per-field counts, chain depth, expansion/re-execution
  counts — the mode for "why did this recipe run that call N times?"), `tree`,
  `jsonOutput`, `find` (regexp over Type.field names), `diff` (structural diff
  against a second recipe file), plus `lit`/`spine`/`depth` for verbosity and
  `limit` to cap the returned lines. The binary is rebuilt from your current
  tree, so edits to cmd/dump-id take effect immediately.
- Engine logs: ListServices shows the engine service's span ids;
  ReadLogs(span, grep, limit) reads them — the equivalent of
  `docker logs dagger-engine.dev | grep`.
- the engine-lab start tool prints the engine's tcp://<host>:1234 endpoint
  (`endpoint` re-prints it). If tui-qa tools are available, pass it to their
  start tool's `engineAddress` arg to run a TUI session against THIS engine — then
  `debugGet`/`debugJq`/ReadLogs introspect the very engine the TUI is driving
  (e.g. reproduce a hang in the TUI, then pprof it live). `restart` and the
  engine-lab stop tool break attached TUI sessions; restart them after.
- the engine-lab stop tool when done.

## Wall-clock profiling (wcprof)

wcprof is the engine's native wall-clock recorder (engine/wcprof). Unlike
OTel it records every dagql call — cache hits and do-not-cache calls
included — plus waits and exec phases, so it answers "what is the engine
doing N times per refresh?" and "where does this latency go?". Recording is
engine-wide: a TUI or agent session attached to the lab engine is recorded
with no extra flags.

The loop:

1. `wcprofEnable` turns recording on (`on: false` stops it; buffered events
   stay dumpable).
2. `wcprofCapture` (flush defaults to true) to throw away what was recorded
   before the window you care about.
3. Reproduce the workload — or, for idle/background behavior, just wait a
   fixed window (e.g. 30s) so counts are comparable between runs.
4. `wcprofCapture` again: it fetches the dump ONCE into private module state
   (never into your workspace — dumps hold 100k+ ops, tens of MB) and returns
   a summary: op count, span, dropped events, call outcomes, top clients and
   classes. Captures are kept under `name` (default `"default"`; re-using a
   name replaces that capture).
5. `wcprofReport(view, ...)` slices a capture (`capture`, default the
   latest). `class` (regexp),
   `excludeClass` (regexp to leave out — RE2 has no lookahead, so this is
   "everything except X"), `client` (substring) and `kind` filter every
   view; `limit` caps the lines.
   - `classes`: per kind+class count, self time total/p50/max, duration,
     duplicate executions and outcomes (hit/executed/joined/do_not_cache).
     Self time = duration minus child ops and waits; `self_tot` sums it
     across ops, concurrent ones included, so it can exceed wall time.
     Internal-kind ops (bookkeeping like `dagql.publishResult`) are left out
     of this and the summary's rankings unless you pass `kind: "internal"`.
   - `breakdown` (needs `class`): the matching ops' direct children by class,
     and "shapes" — parent ops grouped by identical child multisets with
     count and p50 duration. The view for "why is each Query.node slow?".
   - `clients`: ops per client over time buckets, and each client's top
     classes — who is generating the load, steadily or in bursts.
   - `tree`: one op's subtree (`op`, default the slowest matching op),
     `depth` levels below the root (1 = direct children), with waits.
     Children print in start order with their offset from the parent
     (`+31.2ms`); runs of a class with 4+ siblings collapse into one
     aggregate line. A call whose only content is its call_exec folds into
     one node, `(exec N)`, with the exec's children directly under it;
     `▸ N` marks N children not expanded at this depth (internal-kind ops
     such as `dagql.publishResult` not counted).
   - `children`: a flat table of one op's direct children and waits — id,
     start offset, duration, self, outcome, class — for "where did this
     op's time go?". `sortBy` start (default), dur or self. Walk down by
     re-rooting with `op`.
   - `critpath`: the chain of work an op's end actually waited on — the
     view for "what would make this faster?". Walking back from the end,
     each step takes the child or wait that ran latest and recurses into it,
     following waits into shared work (singleflight joins, lazy results,
     execs) rather than the waiter's own subtree. Per class it reports
     on-path own time, which sums to the roots' duration: concurrent work
     that never held anything up gets nothing, unlike summed self time.
     Roots are `op`, or every outermost op matching the filters (e.g.
     `class: "^Workspace[.]withCommit$", kind: "call"` for all of them);
     the longest root's path prints `depth` levels deep.
   - `waits`: who blocks on what — waits by reason, by target (op class or
     resource such as a lock) and by waiter → target, with count, total,
     p50 and max. Filters select waiters. Calls waiting on their own
     call_exec are left out (that time is the child's).
   - `jq`: name-resolved events (`{"type":"op","class":…,"client":…,
     "parent_class":…,"start_ms":…,"dur_ms":…,"self_ms":…,"outcome":…}`,
     plus waits and links) through your jq `filter`; `slurp` for
     aggregations like `group_by`. jq runs with `-c -r`: objects stay
     compact JSON and strings print raw, so end a filter in `@tsv` for
     plain tab-separated rows, e.g.
     `select(.class == "Query.node") | [.id, .dur_ms] | @tsv`.
   - `compare` (needs `against`): the capture against a baseline capture,
     per class: count, duration total and p50, and self total on each side,
     with deltas and ratios, sorted by the largest absolute change in
     duration total. The filters apply to both sides.

For a before/after comparison across engine builds, name the captures.
Captures live in module state, which survives `restart` (only the engine
is replaced), so:

1. `wcprofEnable`, `wcprofCapture` (flush), reproduce,
   `wcprofCapture(name: "before")`.
2. Edit the engine, `restart`, then `wcprofEnable` again (the new engine
   starts with recording off) and `wcprofCapture` to flush.
3. Reproduce the same workload, `wcprofCapture(name: "after")`.
4. `wcprofReport(view: "compare", capture: "after", against: "before")`,
   then drill into the classes that moved with `critpath` (did the change
   shorten what the workload waits on?), `classes`, `breakdown` or `tree`
   on either capture.

Captures are lost when the module state resets (e.g. a new session).
