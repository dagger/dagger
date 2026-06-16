---
name: engine-debugging
description: Debug Dagger engine, core, dagql, filesync, cache, CI trace, panic, hang, leak, and performance issues. Use when investigating failing engine-dev tests, replaying Dagger Cloud traces, inspecting debug endpoints or pprof, capturing goroutine dumps, triaging dagql cache leaks, or analyzing /debug/dagql/cache snapshots.
---

# Engine Debugging

Start from evidence, not broad guesses.

## Core Workflow

1. Build the smallest repro that still shows the failure.
2. Capture long command output to a temp file under `/tmp`.
3. Inspect logs with `rg` and small `sed` windows instead of pasting full output.
4. Find the first divergence between expected and actual behavior.
5. Prefer targeted tests and recorded traces over broad package sweeps.

For full workflows and exact commands, read `../../internal-docs/debugging.md`.

## Test Repros

Use focused engine-dev test commands for integration-style engine repros:

```bash
dagger --progress=plain call engine-dev test --pkg ./core/integration --run='<TestSuiteName>/<SubtestName>' > /tmp/engine-debug.log 2>&1
```

Do not use broad `./...` test patterns during engine-debug loops. They can
accidentally include heavy integration packages and hide the actual failure.

During long runs, scan the log for panics and failures:

```bash
rg -n "panic:|fatal error:|SIGSEGV|--- FAIL:|^FAIL\\s|Error:|error:" /tmp/engine-debug.log
```

## CI Trace Replay

When a user provides a Dagger trace ID or a CI failure with a Dagger Cloud trace,
start from the recorded trace. Replaying a trace does not rerun the job; it
prints recorded telemetry locally for inspection.

```bash
dagger --progress=plain trace <trace-id> > /tmp/ci-trace-<trace-id>.log 2>&1
```

If the user provides only a GitHub PR or check URL, use the workflow in
`../../internal-docs/debugging.md` to map Dagger Cloud status URLs to check IDs
and trace IDs before deciding whether a local repro is needed.

## Hangs And Panics

For hung engine-dev tests, capture goroutine dumps from the inner dev engine
process. Follow the exact SIGQUIT command in `../../internal-docs/debugging.md`;
the target process matters.

After confirming SIGQUIT stack traces are in the log, do not wait indefinitely
for the hung test command to finish.

## Performance And Leaks

Use a persistent dev engine only for repeated performance/debug-endpoint loops,
such as pprof snapshots, metrics scraping, or comparing warm-state behavior.

For dagql cache leak triage, start with metrics before adding deep logs. The
metric names and interpretation details are in `../../internal-docs/debugging.md`.

## Internal Docs

Detailed implementation docs live in `../../internal-docs/`. These docs are
useful when debugging a specific subsystem and needing the current mental model:

- `cachebasics.md`: result model, `GetOrInitCall`, dependencies, public cache APIs
- `egraph.md`: symbolic equivalence, terms, eq-classes, hit selection
- `cache_persistence.md`: startup/shutdown persistence model
- `cache_pruning.md`: retention roots, persisted-edge pruning, size accounting
- `lazy_evaluation.md`: lazy result evaluation and object materialization
- `session_resources.md`: secret/socket handle model and session-compatible hits
- `filesync.md`: host/engine sync protocol and mirror/change-cache behavior
- `mutablecache.md`: mutable-backed objects such as HTTP, git mirrors, filesync mirrors, cache volumes
- `typedefs.md`: typedef identity and caching hot paths
- `dynamicinputs.md`: dynamic inputs and implicit cache scoping
- `dagqltypes.md`: nullable/list cache behavior
- `writingcoreapis.md`: practical guide for cache-aware core/schema APIs

Treat internal docs as context, not authority over the code. If you are changing
the implementation, your edits may make the docs stale; verify behavior against
the current code and tests.

## Cache Snapshot Analyzer

Use the bundled analyzer for streamed `/debug/dagql/cache` snapshots:

```bash
go run ./skills/engine-debugging/scripts/dagql-cache-analyzer.go /tmp/dagql.cache.1
```

It summarizes retained roots, result categories, and approximate cumulative
closures so large cache snapshots can be inspected offline.
