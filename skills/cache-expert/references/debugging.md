# Debugging Cache Issues

This guide focuses on practical debugging for current dagql + filesync cache behavior.

## Core Loop

1. Write down expected identity flow (ID -> cache key -> result ID returned).
2. Log actual values at each boundary.
3. Find first divergence.
4. Decide whether the bug is:

- wrong identity construction
- wrong cache index lookup
- wrong lifecycle/release behavior
- stale compatibility behavior (TTL/session/buildkit integration)

## Repro First

Use a tight test repro before adding logs.

Recommended integration command format:

```bash
dagger --progress=plain call engine-dev test --pkg ./core/integration --run='<TestSuiteName>/<SubtestName>'
```

This command rebuilds the dev engine, runs it as an ephemeral service, and then runs tests against it.
Output includes:

- dev engine build output
- test runner output
- engine logs/printlns
- test logs (e.g. `t.Logf`)

Capture output to a file under `/tmp` to avoid overwhelming terminal context:

```bash
dagger --progress=plain call engine-dev test --pkg ./core/integration --run='<TestSuiteName>/<SubtestName>' > /tmp/cache-debug.log 2>&1
rg -n "panic:|--- FAIL:|^FAIL\s" /tmp/cache-debug.log
```

During long runs, periodically grep for panics. If the engine panics, tests may hang indefinitely:

```bash
rg -n "panic:|fatal error:|SIGSEGV|stack trace" /tmp/cache-debug.log
```

If a test appears hung (engine still alive but no test progress), capture a goroutine dump from the *inner* dev engine process with `SIGQUIT` (THESE INSTRUCTIONS MUST BE FOLLOWED CLOSELY TO AVOID SENDING SIGQUIT TO THE WRONG PROCESS):

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
rg -n "goroutine [0-9]+|fatal error:|SIGQUIT|chan receive|chan send|semacquire|sync\\.Mutex|deadlock" /tmp/cache-debug.log
```

AFTER SENDING SIGQUIT the tests may hang. Once you confirm the log output has SIGQUIT stack traces, you are done and don't need to wait for the test hang to end.

To compare behavior against an engine from another git ref:

```bash
dagger --progress=plain call engine-dev --source 'https://github.com/dagger/dagger#main' test --pkg ./core/integration --run='TestSomeSuite/TestSomeSubtestYouWant'
```

Do not run multiple suites in parallel unless necessary; each suite is CPU-heavy and concurrent runs significantly degrade performance.

DO NOT EVER USE broad `./...` WHEN RUNNING TESTS AS YOU WILL ACCIDENTALLY CAPTURE INTEGRATION TESTS OR OTHER TESTS YOU DID NOT MEAN TO RUN.

`./core/integration`, `./dagql/idtui` and `./dagql/idtui/multiprefixw` are integration-style test packages (not quick unit loops). Avoid running them during tight cache-debug cycles unless you explicitly need those integration paths.

## CI Trace Replay

When a failure happens in CI, start from the trace if one is available. The
user may provide either a raw trace ID or a command copied from the web UI,
such as:

```bash
dagger trace <trace-id>
```

Replay that trace locally with plain progress and capture it to a temp file:

```bash
dagger --progress=plain trace <trace-id> > /tmp/ci-trace-<trace-id>.log 2>&1
```

This does not rerun the CI job. It fetches and prints the recorded trace in the
same style as local `--progress=plain` output, so the rest of this debugging
guide applies: keep the full output in `/tmp`, inspect it with `rg`, and avoid
dumping the whole trace into the conversation.

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

Once you have the trace ID, replay it with `dagger --progress=plain trace ...`
and capture output to `/tmp` as described above.

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
reproduce it locally with a tight `dagger --progress=plain call engine-dev ...`
command or debug directly from the recorded CI trace.

## Performance Debugging With Persistent Dev Engine

For most testing/debugging flows, prefer ephemeral engines via:

```bash
dagger --progress=plain call engine-dev ...
```

However, for performance debugging (pprof snapshots, repeated profiling loops, endpoint inspection), use a persistent dev engine running in Docker.

### Start Persistent Dev Engine

```bash
docker rm -fv dagger-engine.dev
docker volume rm dagger-engine.dev
./hack/dev
```

Notes:

- The container is named `dagger-engine.dev`.
- This engine persists across commands/runs, so it is better for iterative perf investigation.
- A clean reset is often desirable for consistent baselines, but is not always required (depends on whether cache/warm state is part of what you're measuring).

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

- If you do `./hack/with-dev bash -c 'dagger ...'`, you may accidentally pick up a non-dev `dagger` binary from `PATH`.
- In shell-wrapped commands, explicitly use `./bin/dagger` to avoid ambiguity.

### Docker-Level Debugging

Because the engine is a normal Docker container, you can use standard Docker tools:

- `docker logs dagger-engine.dev`
- `docker exec -it dagger-engine.dev sh`
- `docker kill -s <SIGNAL> dagger-engine.dev`

### pprof and Debug Endpoints

The dev engine exposes debug endpoints on `localhost:6060`.

- Current routes are defined in `cmd/engine/debug.go` (see route setup near line 29).
- Use whichever endpoint/tooling fits the question (point-in-time snapshots vs time-window captures).

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
- For long-running or phase-specific regressions, align profile capture timing with the relevant test phase.
- Keep artifacts organized by run so diffs/comparisons are straightforward.

## Metrics-First Leak Triage

When debugging leaked dagql cache refs, start with Prometheus metrics before adding deep logs.

Enable metrics on the target engine:

```bash
_EXPERIMENTAL_DAGGER_METRICS_ADDR=0.0.0.0:9090
_EXPERIMENTAL_DAGGER_METRICS_CACHE_UPDATE_INTERVAL=1s
```

Key metrics:

- `dagger_connected_clients`
- `dagger_dagql_cache_entries`
- `dagger_dagql_cache_ongoing_calls_entries`
- `dagger_dagql_cache_completed_calls_entries`
- `dagger_dagql_cache_completed_calls_by_content_entries`
- `dagger_dagql_cache_ongoing_arbitrary_entries`
- `dagger_dagql_cache_completed_arbitrary_entries`

Interpretation:

1. If `connected_clients` is `0` but `dagql_cache_entries` stays non-zero, refs are retained.
1. Use bucket metrics to localize leak class:

- `completed_calls` growth: call-result refs not released.
- `ongoing_calls` growth: waiter/cancel path likely stuck.
- `*_arbitrary_*` growth: opaque/arbitrary cache path leak.

1. `dagger_dagql_cache_entries` is index-entry count, not unique-result count.
   The same shared result may appear in multiple indexes.

Practical scrape tip for nested-engine integration tests:

- Prefer scraping via a container bound to the engine service (`curl http://dev-engine:9090/metrics`).
- Scraping from the test process via endpoint hostname may fail DNS resolution in some test networks.

Useful correlation log (session teardown):

- `engine/server/session.go` logs:
  - `released dagql cache refs for session` with `beforeEntries` and `afterEntries`
- If `afterEntries` trends upward across completed sessions, session close is not releasing all refs.

## Where to Log (Most Useful)

### ID + cache key construction

- `dagql/objects.go`
  - `preselect`: log `newID`, returned `cacheCfgResp.CacheKey.ID`, and decoded args after rewrite
  - `newCacheKey`: log `ID`, `DoNotCache`, `TTL`, `ConcurrencyKey`

### Base cache lookup/store

- `dagql/cache.go`
  - `GetOrInitCall`: log `callKey`, `storageKey`, `contentKey`, hit path taken
  - content fallback branch: log source result ID and returned ID override
  - `wait`: log index insertion (`storageKey`, `resultCallKey`, `contentDigestKey`)

### Session wrapper behavior

- `dagql/session_cache.go`
  - forced `DoNotCache` retries (`noCacheNext`)
  - session close races (`isClosed` checks)

### TTL persistence path

- `dagql/cache.go` around DB select/update logic
- `dagql/db/queries.go` compare-and-upsert behavior

### Filesync-specific cache behavior

- `engine/filesync/change_cache.go` for change dedupe/wait/release
- `engine/filesync/localfs.go` for conflict detection (`verifyExpectedChange`) and release timing

## Fast Triage Checklists

### Unexpected miss

Check in order:

1. Did `GetCacheConfig` rewrite ID unexpectedly?
2. Did args decoded from final ID differ from intended runtime args?
3. Did recipe digest change because of view/module/nth/sensitive-arg behavior?
4. Did storage key differ due to TTL/session behavior?
5. If only content should match, does returned ID actually carry content digest?

### Unexpected hit

Check:

1. Which index hit (`storageKey` vs `contentDigestKey`)?
2. Was this an intended content-digest hit?
3. If content hit, was returned ID properly overridden to requested recipe?

### Session-specific oddities

Check:

1. Did prior error set `noCacheNext` for this key?
2. Was call forced to `DoNotCache` and then reinserted?
3. Did session close during execution and release result unexpectedly?

### TTL confusion

Check:

1. Was TTL set for this field?
2. Was result marked safe to persist cache metadata?
3. Was DB metadata updated (or intentionally skipped)?
4. Did expiration force new storage key generation?

## Common Failure Modes

- Cache-config hook rewrites ID but execution args were assumed from old selector args.
- Content-digest reuse works, but returned ID identity is wrong (or assumed wrong).
- Errors trigger forced no-cache retry and hide expected cache behavior on next call.
- Missing releases keep stale entries alive longer than expected.
- Filesync path conflict errors caused by not releasing change-cache refs at sync end.

## Minimal Logging Principle

Prefer small, high-signal log lines with:

- call ID digest
- content digest
- storage key
- hit path (`storage`, `content`, `miss`, `ongoing`)
- whether result ID was overridden

This usually narrows root cause quickly without overwhelming logs.
