---
name: engine-debugging
description: Run Dagger repo tests and debug Dagger engine, core, dagql, filesync, cache, CI trace, panic, hang, leak, and performance issues. Use whenever an agent needs to run tests, choose a test command, or interpret test output in this repository, even before a failure is diagnosed; also use for engine-dev tests, Dagger Cloud trace replay, debug endpoints or pprof, goroutine dumps, panics, hangs, leaks, performance issues, and /debug/dagql/cache snapshots.
---

# Engine Debugging

This is the default guide for running Dagger engine/core tests and for debugging
the failures those tests expose.

## CLI Version Compatibility

> [!IMPORTANT]
> Commands in this guide use Dagger v1.0 or later. If your installed Dagger
> version is earlier than v1.0, replace every `dagger api call ...` invocation
> below with `dagger call ...`.

Start from evidence, not broad guesses.

## Core Loop

1. Write down the expected flow through the subsystem being debugged.
2. Log actual values at each boundary.
3. Find the first divergence.
4. Decide whether the bug is in identity construction, lookup, lifecycle,
   compatibility behavior, or an external integration boundary.

Use focused repros, recorded traces, and small log windows. Avoid dumping full
test output into the conversation.

Use Dagger's default progress UI. Do not pass `--progress=plain`.

Prefer small, high-signal log lines over broad dumps. Good debug logs identify
the boundary being checked and include the relevant stable IDs, digests, keys,
hit path, or lifecycle state needed to compare expected and actual behavior.

## Repro First

Use a tight test repro before adding logs.

Recommended integration command format:

```bash
dagger api call engine-dev test --pkg ./core/integration --run='<TestSuiteName>/<SubtestName>'
```

This command rebuilds the dev engine, runs it as an ephemeral service, and then
runs tests against it. Output includes:

- dev engine build output
- test runner output
- engine logs/printlns
- test logs, such as `t.Logf`

Capture output to a file under `/tmp` to avoid overwhelming terminal context:

```bash
dagger api call engine-dev test --pkg ./core/integration --run='<TestSuiteName>/<SubtestName>' > /tmp/engine-debug.log 2>&1
rg -n "panic:|--- FAIL:|^FAIL\s" /tmp/engine-debug.log
```

During long runs, periodically grep for panics. If the engine panics, tests may
hang indefinitely:

```bash
rg -n "panic:|fatal error:|SIGSEGV|stack trace" /tmp/engine-debug.log
```

If a test appears hung, capture a goroutine dump from the inner dev engine
process with `SIGQUIT`. Follow this closely so SIGQUIT is not sent to the wrong
process:

```bash
engine_ctr="$(docker ps --format '{{.Names}}' | rg '^dagger-engine-v' | head -n1)"
docker exec "$engine_ctr" sh -lc '
for p in /proc/[0-9]*; do
  pid=${p#/proc/}
  [ "$pid" = "1" ] && continue
  cmd="$(tr "\0" " " < "$p/cmdline" 2>/dev/null || true)"
  case "$cmd" in
    *"/usr/local/bin/dagger-engine"*)
      echo "sending SIGQUIT to inner dagger-engine pid=$pid" >&2
      kill -QUIT "$pid"
      exit 0
      ;;
  esac
done
echo "no inner dagger-engine process found" >&2
exit 1
'
```

Then inspect the same run log for the dump:

```bash
rg -n "goroutine [0-9]+|fatal error:|SIGQUIT|chan receive|chan send|semacquire|sync\\.Mutex|deadlock" /tmp/engine-debug.log
```

After sending SIGQUIT, the tests may hang. Once you confirm the log has SIGQUIT
stack traces, you are done and do not need to wait for the test hang to end.

To compare behavior against an engine from another git ref:

```bash
dagger api call engine-dev --source 'https://github.com/dagger/dagger#main' test --pkg ./core/integration --run='TestSomeSuite/TestSomeSubtestYouWant'
```

Do not run multiple suites in parallel unless necessary. Each suite is CPU-heavy
and concurrent runs significantly degrade performance.

Do not use broad `./...` when running tests during engine-debug loops. You can
accidentally capture integration tests or other tests you did not mean to run.

`./core/integration`, `./dagql/idtui`, and `./dagql/idtui/multiprefixw` are
integration-style test packages, not quick unit loops. Avoid running them during
tight debug cycles unless you explicitly need those integration paths.

## CI Trace Replay

When a failure happens in CI, start from the trace if one is available. The user
may provide either a raw trace ID or a command copied from the web UI, such as:

```bash
dagger trace <trace-id>
```

Replay that trace locally and capture it to a temp file:

```bash
dagger trace <trace-id> > /tmp/ci-trace-<trace-id>.log 2>&1
```

This does not rerun the CI job. It fetches and prints the recorded trace. Keep
the full output in `/tmp`, inspect it with `rg`, and avoid dumping the whole
trace into the conversation.

### Finding Trace IDs From GitHub PR Checks

If the user gives a GitHub PR URL instead of a trace ID, first inspect the PR's
commit statuses and collect the Dagger Cloud target URLs for the checks of
interest. With GitHub CLI this usually looks like:

```bash
pr_url='https://github.com/dagger/dagger/pull/13119'
head_sha="$(gh pr view "$pr_url" --json headRefOid --jq .headRefOid)"
gh api "repos/dagger/dagger/commits/$head_sha/status" \
  --jq '.statuses[] | select(.target_url | startswith("https://dagger.cloud/")) | [.state, .context, .target_url] | @tsv'
```

For failed checks, add `select(.state != "success")`. A Dagger status target URL
has this shape:

```text
https://dagger.cloud/{org}/checks/{moduleRef}@{moduleVersion}?check={checkName}
```

For public repos, the Cloud GraphQL API can map that URL data to check IDs and
trace IDs without rerunning anything:

```bash
curl -sS -X POST https://api.dagger.cloud/query \
  -H 'Content-Type: application/json' \
  --data '{
    "query": "query($org:String!,$moduleRef:String!,$moduleVersion:String!){ org(name:$org){ moduleChecks(moduleRef:$moduleRef,moduleVersion:$moduleVersion){ commitSHA checks { id name status traceId spanId moduleRef moduleVersion } } } }",
    "variables": {
      "org": "dagger",
      "moduleRef": "github.com/dagger/dagger",
      "moduleVersion": "e7600fda40142627a4206ec04de3a5f702be5a45"
    }
  }' > /tmp/ci-checks.json

jq -r --arg check 'test-split:test-base' \
  '.data.org.moduleChecks[].checks[]
   | select(.name == $check)
   | [.status, .name, .id, .traceId]
   | @tsv' /tmp/ci-checks.json
```

If the Dagger Cloud URL contains `run=<checkID>`, prefer that exact check ID.
Current GitHub status URLs often only include `check=<name>`, so the lookup is
"latest matching check for this org/module/version/name"; be careful after
reruns and prefer the non-success/latest row that matches the status being
debugged.

Once you have the trace ID, replay it with `dagger trace ...` and capture output
to `/tmp` as described above.

Start with the usual failure scan:

```bash
rg -n "panic:|fatal error:|SIGSEGV|--- FAIL:|^FAIL\s|Error:|error:" /tmp/ci-trace-<trace-id>.log
```

Then inspect around the interesting spans:

```bash
rg -n "TestName|FieldName|module name|command text" /tmp/ci-trace-<trace-id>.log
sed -n '<start>,<end>p' /tmp/ci-trace-<trace-id>.log
```

Use the replayed trace to identify the exact failing call, subtest, generated
command, or engine error. Once the failing surface is clear, decide whether to
reproduce it locally with a tight `dagger api call engine-dev ...` command or
debug directly from the recorded CI trace.

## Performance Debugging With Persistent Dev Engine

For most testing/debugging flows, prefer ephemeral engines via:

```bash
dagger api call engine-dev ...
```

For performance debugging, such as pprof snapshots, repeated profiling loops, or
endpoint inspection, use a persistent dev engine running in Docker.

### Start Persistent Dev Engine

```bash
docker rm -fv dagger-engine.dev
docker volume rm dagger-engine.dev
./hack/dev
```

Notes:

- The container is named `dagger-engine.dev`.
- This engine persists across commands/runs, so it is better for iterative perf
  investigation.
- A clean reset is often desirable for consistent baselines, but is not always
  required; it depends on whether cache/warm state is part of what you are
  measuring.

### Run Commands Against Persistent Engine

Use `./hack/with-dev` to target the running `dagger-engine.dev`:

```bash
./hack/with-dev go test -v -count=1 -run='TestWorkspace/TestWorkspaceContentAddressed/storing_a_Directory' ./core/integration/
```

You can also run Dagger commands through the same wrapper:

```bash
./hack/with-dev ./bin/dagger ...
```

Important CLI gotcha:

- If you do `./hack/with-dev bash -c 'dagger ...'`, you may accidentally pick
  up a non-dev `dagger` binary from `PATH`.
- In shell-wrapped commands, explicitly use `./bin/dagger` to avoid ambiguity.

### Docker-Level Debugging

Because the engine is a normal Docker container, you can use standard Docker
tools:

- `docker logs dagger-engine.dev`
- `docker exec -it dagger-engine.dev sh`
- `docker kill -s <SIGNAL> dagger-engine.dev`

### pprof and Debug Endpoints

The dev engine exposes debug endpoints on `localhost:6060`.

- Current routes are defined in `cmd/engine/debug.go`.
- Use whichever endpoint/tooling fits the question: point-in-time snapshots,
  time-window captures, pprof profiles, or debug endpoint snapshots.

Example heap profile capture over 15 seconds:

```bash
curl 'http://localhost:6060/debug/pprof/heap?seconds=15' > /tmp/heap.pprof
```

Then inspect with:

```bash
go tool pprof /tmp/heap.pprof
```

General profiling guidance:

- Choose profile type and capture window based on the symptom.
- For long-running or phase-specific regressions, align profile capture timing
  with the relevant test phase.
- Keep artifacts organized by run so diffs/comparisons are straightforward.

## Metrics-First Leak Triage

When debugging leaked dagql cache refs, start with Prometheus metrics before
adding deep logs.

Enable metrics on the target engine:

```bash
_EXPERIMENTAL_DAGGER_METRICS_ADDR=0.0.0.0:9090
_EXPERIMENTAL_DAGGER_METRICS_CACHE_UPDATE_INTERVAL=1s
```

Current high-signal metrics:

- `dagger_connected_clients`
- `dagger_dagql_cache_entries`

Interpretation:

1. If `dagger_connected_clients` is `0` but `dagger_dagql_cache_entries` stays
   above the warmed baseline, refs may still be retained.
2. `dagger_dagql_cache_entries` is an index-entry count, not a unique-result
   count. The same shared result may appear in multiple indexes.

Practical scrape tip for nested-engine integration tests:

- Prefer scraping via a container bound to the engine service, such as
  `curl http://dev-engine:9090/metrics`.
- Scraping from the test process via endpoint hostname may fail DNS resolution
  in some test networks.

Useful correlation log during session teardown:

- `engine/server/session.go` logs `released dagql cache refs for session` with
  `beforeEntries` and `afterEntries`.
- If `afterEntries` trends upward across completed sessions, session close may
  not be releasing all refs.

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
- `version-gating.md`: schema views, `engineVersion` gates, workspace v1 test fixtures

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
